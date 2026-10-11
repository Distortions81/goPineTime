#!/usr/bin/env python3
"""Bounded hardware-in-loop pairing checks for the configured goPine watch.

The default probe repeats real BlueZ pairing through RequestPasskey and rejects
before PIN entry. Full mode requires one hidden, manually supplied watch PIN,
then checks companion GATT startup, security metadata and bonded reconnects.
Neither mode establishes InfiniLink/iOS compatibility. No firmware is sent.
"""
import argparse
import asyncio
from dataclasses import dataclass
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import struct
import sys

from ble_session import SessionLockError, TargetLock
from pairing_probe import BATTERY_UUID, PHASES, TRACE_UUID, decode_trace

ADAPTER = 'hci1'
ADDRESS = 'C9:9E:15:7A:69:B4'
DEVICE_PATH = '/org/bluez/hci1/dev_C9_9E_15_7A_69_B4'
LIMIT = 'BlueZ hardware test; does not validate InfiniLink, iOS, or rendered watch UI'
COMPANION_FIXTURE = Path(__file__).resolve().parent.parent / 'testdata/infinilink_wire.json'
FIRMWARE_UUID = '00002a26-0000-1000-8000-00805f9b34fb'


class CheckFailed(Exception):
    """A check failed; its message is a fixed diagnostic, never peer input."""


class AgentReject(Exception):
    pass


def error_fields(error):
    # Do not serialize exception text: a library or agent could include a PIN,
    # user-entered data, or a peer payload in it. Exact fixed strings emitted
    # by Bleak may select a fixed code, but unknown text remains private.
    fields = {'error_type': type(error).__name__}
    known_messages = {
        'failed to discover services, device disconnected': 'disconnected_during_service_discovery',
        'Not connected': 'not_connected',
        'Client is already connected': 'already_connected',
        '[org.bluez.Error.Failed] Operation failed with ATT error: 0x0e (Unlikely Error)': 'att_unlikely_error',
    }
    if isinstance(error, TimeoutError):
        fields['error_code'] = 'deadline_exceeded'
    elif str(error) in known_messages:
        fields['error_code'] = known_messages[str(error)]
    name = getattr(error, 'type', getattr(error, 'dbus_error', ''))
    if isinstance(name, str) and re.fullmatch(r'org\.bluez\.Error\.[A-Za-z]+', name):
        fields['dbus_error'] = name
    if isinstance(error, CheckFailed):
        fields['reason'] = str(error)
    return fields


def rejected_pair(error):
    name = getattr(error, 'type', getattr(error, 'dbus_error', ''))
    return name in ('org.bluez.Error.AuthenticationRejected',
                    'org.bluez.Error.AuthenticationCanceled',
                    'org.bluez.Error.AuthenticationFailed',
                    'org.bluez.Error.Rejected', 'org.bluez.Error.Canceled')


def trace_report(value):
    """Keep complete records from a truncated trace without claiming success."""
    value = bytes(value)
    try:
        return {'complete': True, 'bytes': len(value), **decode_trace(value)}
    except ValueError:
        report = {'complete': False, 'bytes': len(value), 'events': []}
        if len(value) >= 4 and value[0] == 1 and value[1] <= 24:
            report.update(declared_records=value[1], phase=PHASES.get(value[2], 'unknown'))
            count = min(value[1], (len(value) - 4) // 8)
            partial = bytes([1, count, value[2], value[3]]) + value[4:4 + 8 * count]
            report['events'] = decode_trace(partial)['events']
        return report


class Recorder:
    def __init__(self, path):
        path.parent.mkdir(parents=True, exist_ok=True)
        self.file = path.open('x')

    def emit(self, event, **fields):
        line = json.dumps({'time': datetime.now(timezone.utc).isoformat(),
                           'event': event, **fields}, sort_keys=True)
        self.file.write(line + '\n')
        self.file.flush()
        print(line, flush=True)

    def close(self):
        self.file.close()


@dataclass
class Settings:
    mode: str = 'probe'
    cycles: int = 3
    connect_timeout: float = 20
    operation_timeout: float = 8
    pair_timeout: float = 25
    pin_timeout: float = 60
    cleanup_timeout: float = 8
    cooldown: float = 1
    pin_hold: float = 0.5
    write_features: bool = False
    write_time: bool = False


async def hidden_pin(timeout):
    """Read a bounded line without echo or a worker thread stuck in getpass."""
    import termios
    fd = sys.stdin.fileno()
    loop = asyncio.get_running_loop()
    future = loop.create_future()
    original = None
    data = bytearray()

    def readable():
        chunk = os.read(fd, 1)
        if not chunk or chunk in (b'\n', b'\r'):
            if not future.done():
                future.set_result(bytes(data))
        elif len(data) < 7:
            data.extend(chunk)
        elif not future.done():
            future.set_exception(CheckFailed('PIN must contain exactly six digits'))

    try:
        if os.isatty(fd):
            original = termios.tcgetattr(fd)
            quiet = list(original)
            quiet[3] &= ~(termios.ECHO | termios.ECHONL)
            termios.tcsetattr(fd, termios.TCSANOW, quiet)
        print('Enter the six-digit code displayed on the watch: ', end='', file=sys.stderr, flush=True)
        loop.add_reader(fd, readable)
        value = await asyncio.wait_for(future, timeout)
        if not re.fullmatch(rb'[0-9]{6}', value):
            raise CheckFailed('PIN must contain exactly six digits')
        return int(value)
    finally:
        loop.remove_reader(fd)
        if original is not None:
            termios.tcflush(fd, termios.TCIFLUSH)
            termios.tcsetattr(fd, termios.TCSANOW, original)
        for index in range(len(data)):
            data[index] = 0
        print(file=sys.stderr, flush=True)


class PasskeyPolicy:
    def __init__(self, settings, recorder, pin_reader=hidden_pin):
        self.settings, self.recorder, self.pin_reader = settings, recorder, pin_reader
        self.cycle = 0
        self.requested = 0
        self.rejected = False
        self.unexpected = False
        self.allow_pin = False

    def begin(self, cycle):
        self.cycle = cycle
        self.requested = 0
        self.rejected = self.unexpected = False
        self.allow_pin = self.settings.mode == 'full' and cycle == 1

    async def request(self, device):
        if device != DEVICE_PATH:
            raise AgentReject()
        self.requested += 1
        self.recorder.emit('pin_requested', cycle=self.cycle)
        if self.settings.mode == 'probe' and self.requested == 1:
            await asyncio.sleep(self.settings.pin_hold)
            self.rejected = True
            self.recorder.emit('pin_rejected_by_probe', cycle=self.cycle)
            raise AgentReject()
        if not self.allow_pin or self.requested != 1:
            self.unexpected = True
            self.recorder.emit('unexpected_pin_request', cycle=self.cycle)
            raise AgentReject()
        # No PIN enters recorder fields, command-line arguments, or GATT.
        return await self.pin_reader(self.settings.pin_timeout)

    def reject_association(self, kind):
        self.unexpected = True
        self.recorder.emit('unexpected_association', cycle=self.cycle, kind=kind)


class Harness:
    def __init__(self, settings, transport, recorder, pin_reader=hidden_pin):
        self.settings, self.transport, self.recorder = settings, transport, recorder
        self.policy = PasskeyPolicy(settings, recorder, pin_reader)
        self.cycle = 0
        self.failed_phase = None
        self.pair_in_flight = False
        self.last_security_marker = None
        self.last_connect_marker = None

    async def phase(self, name, operation, timeout=None):
        self.recorder.emit('phase_start', cycle=self.cycle, phase=name)
        started = asyncio.get_running_loop().time()
        try:
            result = await asyncio.wait_for(operation, timeout or self.settings.operation_timeout)
        except Exception as error:
            self.failed_phase = self.failed_phase or name
            self.recorder.emit('phase_result', cycle=self.cycle, phase=name, passed=False,
                               elapsed_ms=round((asyncio.get_running_loop().time() - started) * 1000),
                               **error_fields(error))
            raise
        self.recorder.emit('phase_result', cycle=self.cycle, phase=name, passed=True,
                           elapsed_ms=round((asyncio.get_running_loop().time() - started) * 1000))
        return result

    async def capture_trace(self, when):
        try:
            value = await asyncio.wait_for(self.transport.read(TRACE_UUID), self.settings.operation_timeout)
            report = trace_report(value)
            self.recorder.emit('watch_trace', cycle=self.cycle, when=when, **report)
            return report['complete']
        except Exception as error:
            self.recorder.emit('trace_unavailable', cycle=self.cycle, when=when, **error_fields(error))
            return False

    async def companion_gatt(self):
        # Expectations come from two pinned companion generations, independently
        # of the firmware service table. Classic Home additionally requires DIS.
        fixture = json.loads(COMPANION_FIXTURE.read_text())
        expected = {**fixture['companion_contract']['characteristics'],
                    **fixture['classic_contract']['characteristics']}
        actual = await self.transport.characteristic_properties(
            [item['uuid'] for item in expected.values()])
        for name, item in expected.items():
            if item['uuid'] not in actual:
                raise CheckFailed('Companion characteristic missing: ' + name)
            if not set(item['properties']).issubset(actual[item['uuid']]):
                raise CheckFailed('Companion characteristic properties missing: ' + name)
        self.recorder.emit('companion_contract', cycle=self.cycle,
                           source_revision=fixture['source_revision'],
                           classic_source_revision=fixture['classic_contract']['source_revision'],
                           characteristics=len(expected))

    async def battery_read(self):
        value = await self.transport.read(BATTERY_UUID)
        if len(value) != 1 or value[0] > 100:
            raise CheckFailed('Invalid Battery response')
        self.recorder.emit('battery_read', cycle=self.cycle, level=value[0])

    async def firmware_read(self):
        # Classic 1.0.2 stays on Connecting until DIS supplies a nonempty UTF-8
        # Firmware Revision. A working Battery read alone cannot satisfy it.
        value = await self.transport.read(FIRMWARE_UUID)
        try:
            revision = bytes(value).decode('utf-8')
        except UnicodeDecodeError:
            raise CheckFailed('Firmware Revision is not valid UTF-8') from None
        if not revision:
            raise CheckFailed('Firmware Revision is empty')
        # Do not include arbitrary peer-provided strings in diagnostic logs.
        self.recorder.emit('firmware_read', cycle=self.cycle, bytes=len(value))

    async def authenticated_link(self):
        # Battery is public and proves neither encryption nor authentication.
        # Require the watch's explicit security-state record before claiming a bond is
        # usable for phone features. Old firmware without it cannot pass full.
        value = await self.transport.read(TRACE_UUID)
        report = decode_trace(bytes(value))
        self.recorder.emit('watch_trace', cycle=self.cycle, when='security_check',
                           complete=True, bytes=len(value), **report)
        events = report['events']
        secured = [index for index, event in enumerate(events) if event['event'] == 'link_security']
        if not secured:
            raise CheckFailed('Firmware did not provide current authenticated-link metadata')
        index = secured[-1]
        security = events[index]
        security_marker = (security['watch_ms'], security['result'])
        if security['result'] != 0x1007:
            raise CheckFailed('Link is not encrypted, authenticated, bonded and using a 16-byte key')
        if any(event['event'] in ('connect', 'disconnect', 'host_reset', 'phone_stop')
               for event in events[index + 1:]):
            raise CheckFailed('Security metadata belongs to an earlier connection')
        # Fresh pairing can fill the trace ring with passkey rounds and evict
        # its connect record. Reconnect has no such rounds and must retain it.
        connects = [position for position, event in enumerate(events[:index])
                    if event['event'] == 'connect' and event['result'] == 0]
        connect_marker = events[connects[-1]]['watch_ms'] if connects else None
        if self.cycle > 1:
            if not connects:
                raise CheckFailed('No current reconnect event accompanies security metadata')
            if security_marker == self.last_security_marker or connect_marker == self.last_connect_marker:
                raise CheckFailed('Reconnect returned stale security or connection evidence')
            if any(event['event'] == 'passkey_action' for event in events[connects[-1] + 1:]):
                raise CheckFailed('Watch requested a fresh passkey on bonded reconnection')
        self.last_security_marker = security_marker
        self.last_connect_marker = connect_marker

    async def assert_bond(self, expected):
        if await self.transport.paired() != expected:
            raise CheckFailed('Unexpected PC bond state; existing bonds are never removed')

    async def pair(self):
        self.pair_in_flight = True
        try:
            await self.transport.pair()
        except Exception as error:
            if (self.settings.mode == 'probe' and self.policy.requested == 1 and
                    self.policy.rejected and not self.policy.unexpected and rejected_pair(error)):
                self.pair_in_flight = False
                return
            raise
        self.pair_in_flight = False
        if self.settings.mode == 'probe':
            raise CheckFailed('Pair unexpectedly succeeded in a probe that must reject before bonding')
        if self.policy.requested != 1 or self.policy.unexpected:
            raise CheckFailed('Pair succeeded without the expected single passkey exchange')

    async def features(self):
        packets = json.loads(COMPANION_FIXTURE.read_text())['packets']
        mapping = {'current_v1': '00050001-78fc-48fe-8e23-433b3a1942d0',
                   'forecast': '00050001-78fc-48fe-8e23-433b3a1942d0',
                   'status': '00000002-78fc-48fe-8e23-433b3a1942d0',
                   'artist': '00000003-78fc-48fe-8e23-433b3a1942d0',
                   'track': '00000004-78fc-48fe-8e23-433b3a1942d0',
                   'alert': '00002a46-0000-1000-8000-00805f9b34fb'}
        for name, uuid in mapping.items():
            value = bytearray.fromhex(packets[name])
            if name in ('current_v1', 'forecast'):
                # Keep fixture structure while making the opt-in weather test
                # current; stale source timestamps would hide otherwise valid
                # packets in the watch's weather display.
                struct.pack_into('<Q', value, 2, int(datetime.now(timezone.utc).timestamp()))
            await self.phase('feature_' + name, self.transport.write(uuid, value))
        self.recorder.emit('feature_limit', cycle=self.cycle,
                           limit='ATT write acceptance only; inspect watch rendering separately')

    async def cycle_body(self):
        if self.settings.mode == 'trace':
            await self.phase('connect_discover', self.transport.connect(), self.settings.connect_timeout)
            if not await self.capture_trace('retained'):
                self.failed_phase = 'trace'
                raise CheckFailed('Complete retained pairing trace could not be read')
            return
        if self.settings.mode == 'probe' or self.cycle == 1:
            # On firmware before 0.3.26, BlueZ Connect/service discovery can
            # make its Battery plugin request security through the desktop
            # default agent. Pair on our agent's bus before GATT access so
            # this probe also works with those older protected Battery reads.
            # Current Battery access is public. Never reconnect after rejection.
            await self.phase('discover', self.transport.discover(), self.settings.connect_timeout)
            await self.phase('unpaired', self.assert_bond(False))
            timeout = self.settings.pair_timeout
            if self.settings.mode == 'full':
                timeout += self.settings.pin_timeout
            await self.phase('pair', self.pair(), timeout)
        await self.phase('bond_state', self.assert_bond(self.settings.mode == 'full'))
        if self.settings.mode == 'full':
            await self.phase('connect_discover', self.transport.connect(), self.settings.connect_timeout)
            await self.phase('companion_gatt', self.companion_gatt())
            # InfiniLink reads Battery once, then subscribes in the same
            # discovery callback. Do not hide a failed read behind retries.
            await self.phase('battery_read', self.battery_read())
            await self.phase('battery_subscribe', self.transport.subscribe(BATTERY_UUID))
            await self.phase('firmware_read', self.firmware_read())
            await self.phase('authenticated_link', self.authenticated_link())
            if self.policy.unexpected or (self.cycle > 1 and self.policy.requested):
                raise CheckFailed('Bonded reconnection requested a new PIN')
            if self.cycle == 1 and self.settings.write_features:
                await self.features()
            if self.cycle == 1 and self.settings.write_time:
                now = datetime.now().astimezone()
                value = struct.pack('<H8B', now.year, now.month, now.day, now.hour,
                                    now.minute, now.second, now.isoweekday(), 0, 1)
                await self.phase('write_current_time', self.transport.write(
                    '00002a2b-0000-1000-8000-00805f9b34fb', value))

    async def cleanup_cycle(self):
        ok = True
        if self.pair_in_flight:
            try:
                await self.phase('cancel_pairing', self.transport.cancel_pairing(), self.settings.cleanup_timeout)
                self.pair_in_flight = False
            except Exception:
                ok = False
        # Attempt this even after failed connect/Pair: Bleak may have partially
        # connected, and a pairing rejection can leave useful watch records.
        if self.settings.mode != 'trace':
            await self.capture_trace('after')
        try:
            await self.phase('disconnect', self.transport.disconnect(), self.settings.cleanup_timeout)
        except Exception:
            ok = False
        return ok

    async def run(self):
        results = []
        teardown_ok = True
        self.recorder.emit('run_start', mode=self.settings.mode, adapter=ADAPTER, address=ADDRESS,
                           cycles=1 if self.settings.mode == 'trace' else self.settings.cycles, limit=LIMIT)
        try:
            await self.phase('setup', self.transport.open(self.policy), self.settings.cleanup_timeout)
            await self.phase('preflight', self.transport.preflight(self.settings.mode))
            for cycle in range(1, (1 if self.settings.mode == 'trace' else self.settings.cycles) + 1):
                self.cycle = cycle
                self.failed_phase = None
                self.policy.begin(cycle)
                passed = False
                self.recorder.emit('cycle_start', cycle=cycle)
                try:
                    await self.cycle_body()
                    passed = True
                except Exception as error:
                    self.recorder.emit('cycle_error', cycle=cycle, **error_fields(error))
                finally:
                    cleaned = await self.cleanup_cycle()
                    passed = passed and cleaned and not self.policy.unexpected
                    result = {'cycle': cycle, 'passed': passed, 'failed_phase': self.failed_phase,
                              'pin_requests': self.policy.requested, 'cleanup_complete': cleaned}
                    results.append(result)
                    self.recorder.emit('cycle_result', **result)
                # Do not start another connection while cancellation or teardown
                # is uncertain. Full mode preserves its newly created bond.
                if not cleaned or (self.settings.mode == 'full' and not passed):
                    break
                if cycle < self.settings.cycles and self.settings.mode != 'trace':
                    await asyncio.sleep(self.settings.cooldown)
        except Exception as error:
            self.recorder.emit('run_error', **error_fields(error))
        finally:
            try:
                await self.phase('teardown', self.transport.close(), self.settings.cleanup_timeout)
            except Exception:
                teardown_ok = False
        expected = 1 if self.settings.mode == 'trace' else self.settings.cycles
        passed = len(results) == expected and all(item['passed'] for item in results) and teardown_ok
        self.recorder.emit('summary', passed=passed, passed_cycles=sum(item['passed'] for item in results),
                           completed_cycles=len(results), total_cycles=expected, teardown_complete=teardown_ok,
                           bond_policy='never_remove', limit=LIMIT)
        return 0 if passed else 1


class BlueZTransport:
    """All device operations remain on hci1; never request default-agent status."""
    def __init__(self):
        self.bus = self.manager = self.client = self.device = None
        self.found_device = None
        self.registered = False
        self.agent_path = '/org/gopine/PairingHarness'

    async def open(self, policy):
        from bleak import BleakClient, BleakScanner
        from dbus_fast import BusType, DBusError
        from dbus_fast.aio import MessageBus
        from dbus_fast.service import ServiceInterface, method
        self.client_class = BleakClient
        self.scanner_class = BleakScanner
        self.bus = await MessageBus(bus_type=BusType.SYSTEM).connect()
        if policy.settings.mode == 'trace':
            return

        class Agent(ServiceInterface):
            def __init__(self):
                super().__init__('org.bluez.Agent1')

            @method()
            def Release(self):
                pass

            @method()
            async def RequestPasskey(self, device: 'o') -> 'u':
                try:
                    return await policy.request(device)
                except Exception:
                    raise DBusError('org.bluez.Error.Rejected', 'Pairing harness rejected the request')

            @method()
            def RequestPinCode(self, device: 'o') -> 's':
                policy.reject_association('legacy_pin')
                raise DBusError('org.bluez.Error.Rejected', 'Legacy pairing is not tested')

            @method()
            def RequestConfirmation(self, device: 'o', passkey: 'u'):
                policy.reject_association('numeric_comparison')
                raise DBusError('org.bluez.Error.Rejected', 'Expected display-only passkey pairing')

            @method()
            def RequestAuthorization(self, device: 'o'):
                policy.reject_association('authorization')
                raise DBusError('org.bluez.Error.Rejected', 'Unsolicited pairing is denied')

            @method()
            def AuthorizeService(self, device: 'o', uuid: 's'):
                raise DBusError('org.bluez.Error.Rejected', 'Service authorization is not tested')

            @method()
            def Cancel(self):
                pass

        intro = await self.bus.introspect('org.bluez', '/org/bluez')
        self.manager = self.bus.get_proxy_object('org.bluez', '/org/bluez', intro).get_interface('org.bluez.AgentManager1')
        self.bus.export(self.agent_path, Agent())
        await self.manager.call_register_agent(self.agent_path, 'KeyboardDisplay')
        self.registered = True

    async def objects(self):
        intro = await self.bus.introspect('org.bluez', '/')
        manager = self.bus.get_proxy_object('org.bluez', '/', intro).get_interface('org.freedesktop.DBus.ObjectManager')
        return await manager.call_get_managed_objects()

    async def preflight(self, mode):
        objects = await self.objects()
        adapter = objects.get('/org/bluez/' + ADAPTER, {}).get('org.bluez.Adapter1', {})
        if not adapter or not adapter['Powered'].value:
            raise CheckFailed('The configured hci1 adapter must already be powered')
        device = objects.get(DEVICE_PATH, {}).get('org.bluez.Device1', {})
        if mode != 'trace' and device.get('Paired') and device['Paired'].value:
            raise CheckFailed('This PC already has a bond; refusing to replace or remove it')

    async def connect(self):
        # Pair normally leaves the watch connected and no longer advertising.
        # Reuse the discovered BLEDevice so Bleak does not start another scan
        # and fail to find that already-connected peripheral by address.
        self.client = self.client_class(self.found_device or ADDRESS, adapter=ADAPTER, timeout=15)
        await self.client.connect()
        if not self.client.is_connected:
            raise CheckFailed('Connection did not become active')
        # Bleak connect includes service resolution. Explicitly check the
        # expected services rather than treating any connected device as a pass.
        for uuid in (BATTERY_UUID, TRACE_UUID):
            if not self.client.services.get_characteristic(uuid):
                raise CheckFailed('Expected goPine characteristics were not discovered')
        intro = await self.bus.introspect('org.bluez', DEVICE_PATH)
        proxy = self.bus.get_proxy_object('org.bluez', DEVICE_PATH, intro)
        self.device = proxy.get_interface('org.bluez.Device1')

    async def discover(self):
        # Scanning creates the selected BlueZ device object without Connect or
        # GATT service discovery. Pair itself owns subsequent link setup.
        found = await self.scanner_class.find_device_by_address(ADDRESS, timeout=12, adapter=ADAPTER)
        if found is None:
            raise CheckFailed('Configured watch was not discovered on hci1')
        self.found_device = found
        intro = await self.bus.introspect('org.bluez', DEVICE_PATH)
        proxy = self.bus.get_proxy_object('org.bluez', DEVICE_PATH, intro)
        self.device = proxy.get_interface('org.bluez.Device1')

    async def paired(self):
        objects = await self.objects()
        properties = objects.get(DEVICE_PATH, {}).get('org.bluez.Device1', {})
        if 'Paired' not in properties:
            raise CheckFailed('Configured BlueZ device disappeared')
        return properties['Paired'].value

    async def pair(self):
        # Same D-Bus connection as our non-default agent registration.
        await self.device.call_pair()

    async def cancel_pairing(self):
        if self.device:
            try:
                await self.device.call_cancel_pairing()
            except Exception as error:
                if getattr(error, 'type', '') != 'org.bluez.Error.DoesNotExist':
                    raise

    async def read(self, uuid):
        if self.client and self.client.is_connected:
            return await self.client.read_gatt_char(uuid)
        # Probe cleanup reads only if Pair left this connection and services
        # usable. Never create a new connection to retrieve a failed trace.
        objects = await self.objects()
        device = objects.get(DEVICE_PATH, {}).get('org.bluez.Device1', {})
        if not device.get('Connected') or not device['Connected'].value:
            raise CheckFailed('Watch is disconnected; failure trace remains on watch')
        for path, interfaces in objects.items():
            characteristic = interfaces.get('org.bluez.GattCharacteristic1', {})
            if path.startswith(DEVICE_PATH + '/') and characteristic.get('UUID') and characteristic['UUID'].value == uuid:
                intro = await self.bus.introspect('org.bluez', path)
                proxy = self.bus.get_proxy_object('org.bluez', path, intro)
                return await proxy.get_interface('org.bluez.GattCharacteristic1').call_read_value({})
        raise CheckFailed('Trace characteristic is not available on the existing connection')

    async def write(self, uuid, value):
        await self.client.write_gatt_char(uuid, value, response=True)

    async def characteristic_properties(self, uuids):
        return {uuid: set(characteristic.properties) for uuid in uuids
                if (characteristic := self.client.services.get_characteristic(uuid)) is not None}

    async def subscribe(self, uuid):
        # Startup only requires subscription acceptance; the preceding read
        # supplied the value. It does not require an immediate notification.
        await self.client.start_notify(uuid, lambda characteristic, value: None)

    async def disconnect(self):
        # Attempt disconnect even when connect failed during service discovery.
        if self.client:
            await self.client.disconnect()
            if self.client.is_connected:
                raise CheckFailed('Watch remains connected after disconnect')
            self.client = None
        elif self.device:
            try:
                await self.device.call_disconnect()
            except Exception as error:
                if getattr(error, 'type', '') != 'org.bluez.Error.NotConnected':
                    raise
        self.device = None

    async def close(self):
        try:
            await self.disconnect()
            if self.registered:
                await self.manager.call_unregister_agent(self.agent_path)
                self.registered = False
        finally:
            # Disconnecting the bus also removes its agent if explicit cleanup
            # failed or timed out. Do not await another operation in this final
            # path, so cancellation cannot exceed the cleanup deadline.
            if self.bus:
                self.bus.disconnect()


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=('probe', 'trace', 'full'), default='probe')
    parser.add_argument('--cycles', type=int, default=3, help='probe attempts or full pairing plus reconnects (1..10)')
    parser.add_argument('--log', type=Path)
    parser.add_argument('--write-features', action='store_true', help='full mode only: replace watch weather/music and add a test alert')
    parser.add_argument('--write-time', action='store_true', help='full mode only: set watch time from this computer')
    args = parser.parse_args(argv)
    if not 1 <= args.cycles <= 10:
        parser.error('cycles must be 1..10')
    if (args.write_features or args.write_time) and args.mode != 'full':
        parser.error('feature/time writes require explicit --mode full')
    return args


def main(argv=None):
    args = parse_args(argv)
    path = args.log or Path('build/ota') / ('pairing-harness-' + datetime.now().strftime('%Y%m%d-%H%M%S-%f') + '.jsonl')
    recorder = Recorder(path)
    settings = Settings(mode=args.mode, cycles=args.cycles, write_features=args.write_features, write_time=args.write_time)
    try:
        with TargetLock(ADAPTER, ADDRESS):
            return asyncio.run(Harness(settings, BlueZTransport(), recorder).run())
    except SessionLockError as error:
        print(f'Pairing check failed: {error}', file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        recorder.emit('interrupted', passed=False)
        return 130
    finally:
        recorder.close()
        print(f'Pairing log: {path}', flush=True)


if __name__ == '__main__':
    sys.exit(main())
