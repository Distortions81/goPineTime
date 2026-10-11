#!/usr/bin/env python3
"""Probe physical PineTime pairing without entering a PIN or saving a PC bond.

Phone mode: read Battery, explicitly request central SMP pairing, record whether
BlueZ reaches RequestPasskey, then reject it. Battery is public in 0.3.26 and
newer; older firmware can request security during that read.
Trace mode: read retained watch events (e.g. in OTA mode after an iPhone failure).
Always use the selected adapter/address; never power another adapter or flash.
"""
import argparse
import asyncio
from datetime import datetime, timezone
import json
from pathlib import Path
import struct
import sys

from ble_session import SessionLockError, TargetLock

TRACE_UUID = '00070001-78fc-48fe-8e23-433b3a1942d0'
BATTERY_UUID = '00002a19-0000-1000-8000-00805f9b34fb'
EVENTS = {1: 'phone_start', 2: 'connect', 3: 'security_request',
          4: 'passkey_action', 5: 'passkey_injection', 6: 'security_result',
          7: 'disconnect', 8: 'security_progress', 9: 'host_reset',
          10: 'advertising_error', 11: 'repeat_pairing_rejected', 12: 'phone_stop',
          13: 'link_security', 14: 'saved_bond_metadata', 15: 'matching_peer_bond',
          16: 'ltk_restore_lookup', 17: 'pair_request_rejected', 18: 'startup_failure'}
PHASES = {0: 'no_security_exchange', 1: 'starting', 2: 'passkey_rounds',
          3: 'dhkey_check', 4: 'encryption', 5: 'identity_exchange', 6: 'complete'}


def decode_key_flags(value):
    return dict(present=bool(value & 1), ltk=bool(value & 2),
                authenticated=bool(value & 4), secure_connections=bool(value & 8),
                key_16_bytes=bool(value & 16))


def decode_trace(value):
    if len(value) < 4 or value[0] != 1 or value[1] > 24 or len(value) != 4 + 8 * value[1]:
        raise ValueError('Invalid watch pairing trace')
    events = []
    for offset in range(4, len(value), 8):
        uptime, event, rounds, result = struct.unpack_from('<IBBH', value, offset)
        record = {'watch_ms': uptime, 'event': EVENTS.get(event, f'unknown_{event}'), 'result': result}
        if event == 8:
            record.update(phase=PHASES.get(result >> 8, 'unknown'), rounds=result & 31)
        elif event == 13:
            record.update(encrypted=bool(result & 1), authenticated=bool(result & 2),
                          bonded=bool(result & 4), key_size=result >> 8)
        elif event in (14, 15):
            record.update(our_key=decode_key_flags(result & 255),
                          peer_key=decode_key_flags(result >> 8))
        events.append(record)
    report = {'phase': PHASES.get(value[2], 'unknown'), 'events': events}
    if value[3]:
        report['saved_our_key'] = decode_key_flags(value[3])
    return report


class Recorder:
    def __init__(self, path):
        path.parent.mkdir(parents=True, exist_ok=True)
        self.file = path.open('x')

    def emit(self, event, **fields):
        value = {'time': datetime.now(timezone.utc).isoformat(), 'event': event, **fields}
        line = json.dumps(value, sort_keys=True)
        print(line, flush=True)
        self.file.write(line + '\n')
        self.file.flush()

    def close(self):
        self.file.close()


async def run(args, recorder):
    from bleak import BleakClient
    from dbus_fast import BusType, DBusError
    from dbus_fast.aio import MessageBus
    from dbus_fast.service import ServiceInterface, method

    class ProbeAgent(ServiceInterface):
        def __init__(self):
            super().__init__('org.bluez.Agent1')
            self.requested = False

        @method()
        def Release(self):
            recorder.emit('agent_released')

        @method()
        async def RequestPasskey(self, device: 'o') -> 'u':
            if device != device_path:
                raise DBusError('org.bluez.Error.Rejected', 'Probe is limited to the configured watch')
            self.requested = True
            recorder.emit('pin_requested')
            await asyncio.sleep(2)  # Leave the on-watch prompt observable briefly.
            raise DBusError('org.bluez.Error.Rejected', 'Probe stops before PIN entry or bonding')

        @method()
        def RequestPinCode(self, device: 'o') -> 's':
            raise DBusError('org.bluez.Error.Rejected', 'Legacy pairing is not tested')

        @method()
        def RequestConfirmation(self, device: 'o', passkey: 'u'):
            # Never log passkeys or auto-approve a different association model.
            recorder.emit('unexpected_numeric_comparison')
            raise DBusError('org.bluez.Error.Rejected', 'Expected display-only passkey pairing')

        @method()
        def RequestAuthorization(self, device: 'o'):
            raise DBusError('org.bluez.Error.Rejected', 'Authorization outside PIN probe is denied')

        @method()
        def AuthorizeService(self, device: 'o', uuid: 's'):
            raise DBusError('org.bluez.Error.Rejected', 'Service authorization is outside this probe')

        @method()
        def Cancel(self):
            recorder.emit('agent_cancelled')

    device_path = f'/org/bluez/{args.adapter}/dev_' + args.address.replace(':', '_')
    bus = await MessageBus(bus_type=BusType.SYSTEM).connect()
    agent = ProbeAgent()
    manager = None
    agent_path = '/org/gopine/PairingProbe'
    registered = False
    try:
        intro = await bus.introspect('org.bluez', '/org/bluez')
        manager = bus.get_proxy_object('org.bluez', '/org/bluez', intro).get_interface('org.bluez.AgentManager1')
        bus.export(agent_path, agent)
        await manager.call_register_agent(agent_path, 'KeyboardDisplay')
        registered = True
        if args.check_agent:
            recorder.emit('agent_ready', capability='KeyboardDisplay', changes_default_agent=False)
            return 0
        # Pair is invoked on this same bus so BlueZ uses this agent, without
        # replacing the desktop's default agent or modifying other adapters.
        seen = 0
        trace_ok = False
        for cycle in range(args.cycles if args.mode == 'probe' else 1):
            agent.requested = False
            recorder.emit('cycle_start', cycle=cycle + 1, adapter=args.adapter, address=args.address)
            client = BleakClient(args.address, adapter=args.adapter, timeout=12)
            try:
                await asyncio.wait_for(client.connect(), 15)
                recorder.emit('connected', characteristics=len(client.services.characteristics))
                intro = await bus.introspect('org.bluez', device_path)
                proxy = bus.get_proxy_object('org.bluez', device_path, intro)
                device = proxy.get_interface('org.bluez.Device1')
                props = proxy.get_interface('org.freedesktop.DBus.Properties')
                paired = (await props.call_get('org.bluez.Device1', 'Paired')).value
                if paired and args.mode != 'trace':
                    raise RuntimeError('This PC already has a bond; refusing to replace or remove it')
                async def trace():
                    try:
                        value = await asyncio.wait_for(client.read_gatt_char(TRACE_UUID), 8)
                        recorder.emit('watch_trace', **decode_trace(value))
                        return True
                    except Exception as error:
                        recorder.emit('trace_unavailable', error=str(error))
                        return False
                if args.mode == 'trace':
                    trace_ok = await trace()
                    continue
                try:
                    value = await asyncio.wait_for(client.read_gatt_char(BATTERY_UUID), 5)
                    recorder.emit('battery_read', read_succeeded=True, level=int(value[0]))
                except Exception as error:
                    # Preserve the legacy event name for existing log readers;
                    # Battery is public from 0.3.26, so this is any read failure.
                    recorder.emit('protected_read', error=str(error))
                # Explicit Pair tests SMP independently of public Battery.
                # On older firmware, BlueZ may not retry a protected ATT read.
                try:
                    await asyncio.wait_for(device.call_pair(), 20)
                    recorder.emit('unexpected_pair_success')
                    raise RuntimeError('Probe must not complete bonding')
                except asyncio.TimeoutError:
                    await asyncio.wait_for(device.call_cancel_pairing(), 5)
                    recorder.emit('pair_timeout', pin_requested=agent.requested)
                except DBusError as error:
                    recorder.emit('pair_result', error=str(error), pin_requested=agent.requested)
                if agent.requested:
                    seen += 1
                await trace()
            except Exception as error:
                recorder.emit('cycle_error', error=str(error), pin_requested=agent.requested)
                if agent.requested:
                    seen += 1
            finally:
                # A failed connect may still own a BlueZ connection or monitor.
                await asyncio.wait_for(client.disconnect(), 8)
                recorder.emit('cycle_end', cycle=cycle + 1)
            await asyncio.sleep(1)
        if args.mode == 'probe':
            recorder.emit('summary', requested_pin_cycles=seen, total_cycles=args.cycles,
                          limit='Stops before PIN entry; does not validate full bonding or iOS')
            return 0 if seen == args.cycles else 1
        return 0 if trace_ok else 1
    finally:
        try:
            if registered:
                await manager.call_unregister_agent(agent_path)
        finally:
            bus.disconnect()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--adapter', default='hci1')
    parser.add_argument('--address', default='C9:9E:15:7A:69:B4')
    parser.add_argument('--mode', choices=('probe', 'trace'), default='probe')
    parser.add_argument('--cycles', type=int, default=3)
    parser.add_argument('--log', type=Path)
    parser.add_argument('--check-agent', action='store_true', help='register and remove the test agent without connecting')
    args = parser.parse_args(argv)
    if not 1 <= args.cycles <= 10:
        parser.error('cycles must be 1..10')
    if not args.adapter.startswith('hci') or not args.adapter[3:].isdigit():
        parser.error('adapter must be an hci index')
    import re
    if not re.fullmatch(r'(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}', args.address):
        parser.error('address must be a Bluetooth address')
    args.address = args.address.upper()
    path = args.log or Path('build/ota') / ('pairing-' + datetime.now().strftime('%Y%m%d-%H%M%S-%f') + '.jsonl')
    recorder = Recorder(path)
    try:
        with TargetLock(args.adapter, args.address) as session:
            args.adapter, args.address = session.adapter, session.address
            return asyncio.run(run(args, recorder))
    except SessionLockError as error:
        print(f'Pairing probe failed: {error}', file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        recorder.emit('interrupted')
        return 130
    finally:
        recorder.close()
        print(f'Pairing log: {path}', flush=True)


if __name__ == '__main__':
    sys.exit(main())
