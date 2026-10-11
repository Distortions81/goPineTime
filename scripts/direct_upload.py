#!/usr/bin/env python3
"""Send a prepared image to goPine; activation is always an on-watch action."""
import argparse
import asyncio
import pathlib
import re
import struct
import time

import ota_package

BASE = '-78fc-48fe-8e23-433b3a1942d0'
SERVICE = '00060000' + BASE
CONTROL = '00060001' + BASE
DATA = '00060002' + BASE
STATUS = '00060003' + BASE
WAITING, RECEIVING, VERIFYING, READY, FAILED = range(1, 6)
GATT_TIMEOUT_SECONDS = 15


async def gatt_call(operation):
    # wait_for waits for native cancellation to settle before returning; the
    # client context can then disconnect before any reconnect starts.
    return await asyncio.wait_for(operation, GATT_TIMEOUT_SECONDS)

class Rejected(RuntimeError):
    pass


def decode_status(raw):
    if len(raw) != 16 or raw[0] != 2 or raw[1] not in range(1, 6):
        raise Rejected('This uploader requires goPine OTA protocol 2 (0.3.22 or newer)')
    total, offset, session = struct.unpack_from('<III', raw, 4)
    if offset > total:
        raise Rejected('Invalid received-byte count')
    if raw[1] == FAILED or raw[2]:
        raise Rejected('Watch stopped the update. Read its message, then use Retry on the watch.')
    # Byte 3 is the actual negotiated maximum ATT write value, reported by
    # the server because BlueZ clients may report a constant MTU of 23.
    return raw[1], total, offset, session, max(0, min(236, raw[3] - 8))


async def read_status(client):
    return decode_status(await gatt_call(client.read_gatt_char(STATUS)))


class StatusNotifications:
    """One bounded snapshot: no polling or per-packet notification queue."""
    def __init__(self):
        self.status = None
        self.error = None
        self.changed = asyncio.Event()

    def receive(self, _, raw):
        try:
            self.status = decode_status(raw)
        except Rejected as error:
            self.error = error
        self.changed.set()

    async def wait(self, predicate, timeout=15, validate=lambda status: None):
        async def receive():
            while True:
                self.changed.clear()
                if self.error:
                    raise self.error
                if self.status is not None:
                    validate(self.status)
                    if predicate(self.status):
                        return self.status
                await self.changed.wait()
        return await asyncio.wait_for(receive(), timeout)


async def transfer_capabilities(client):
    """Old protocol-2 receivers have a write-only control characteristic."""
    services = getattr(client, 'services', None)
    lookup = getattr(services, 'get_characteristic', None)
    control = lookup(CONTROL) if lookup else None
    if control is None or 'read' not in control.properties:
        return 1, 0
    raw = bytes(await gatt_call(client.read_gatt_char(CONTROL)))
    if raw != bytes((1, 2, 1, 0)):
        raise Rejected('Unsupported OTA transfer capabilities')
    data = lookup(DATA)
    maximum = 0
    if data is not None and 'write-without-response' in data.properties:
        maximum = getattr(data, 'max_write_without_response_size', 0)
        if not isinstance(maximum, int) or maximum < 1:
            maximum = 0
    return 2, maximum


class TransferTiming:
    def __init__(self):
        self.clock = asyncio.get_running_loop().time
        self.started = self.clock()
        self.stage = None
        self.since = self.started
        self.seconds = {'data': 0.0, 'verify': 0.0}

    def phase(self, stage):
        now = self.clock()
        if self.stage:
            self.seconds[self.stage] += now - self.since
        self.stage, self.since = stage, now

    def summary(self):
        return (f"Transfer timing: total {self.clock()-self.started:.1f}s; "
                f"data {self.seconds['data']:.1f}s; verification {self.seconds['verify']:.1f}s.")


async def send_connected(client, image, session, progress, minimum_offset=0, timings=None):
    notifications = StatusNotifications()
    try:
        await gatt_call(client.start_notify(STATUS, notifications.receive))
        # Status always reports flash readback, never radio queue acceptance.
        notifications.status = await read_status(client)
        state, total, offset, token, chunk = notifications.status
        window, without_response_limit = await transfer_capabilities(client)
        if without_response_limit < chunk + 8:
            without_response_limit = 0
        mode = 'without response' if without_response_limit else 'acknowledged'
        print(f'Transfer mode: window {window}, {mode} writes, {chunk}-byte payloads.', flush=True)
        if state == WAITING:
            if minimum_offset:
                raise Rejected('Watch lost previously acknowledged firmware bytes')
            if total or offset or token:
                raise Rejected('Inconsistent waiting state')
            await gatt_call(client.write_gatt_char(CONTROL, struct.pack('<BII', 1, session, len(image)), response=True))
            state, total, offset, token, chunk = await notifications.wait(lambda s: s[0] != WAITING)

        def identity(status):
            if status[3] != session or status[1] != len(image):
                raise Rejected('A different transfer is staged. Cancel or Retry on the watch first.')
            if status[2] < offset:
                raise Rejected('Watch lost acknowledged firmware bytes')

        identity((state, total, offset, token, chunk))
        if offset < minimum_offset:
            raise Rejected('Watch lost previously acknowledged firmware bytes')
        progress(offset, total)
        if chunk < 1:
            raise Rejected('Watch has no active ATT connection')
        if state == READY:
            if offset != total:
                raise Rejected('Verified image is incomplete')
            progress(len(image), len(image))
        else:
            if timings:
                timings.phase('data' if state == RECEIVING else 'verify')
            # Native GATT operations remain sequential. Only durable-ACK waits
            # overlap: no more than the advertised two packet boundaries exist.
            sent, outstanding = offset, []
            while state == RECEIVING and offset < total:
                if notifications.error:
                    raise notifications.error
                latest = notifications.status
                identity(latest)
                state, _, acknowledged, _, chunk = latest
                if state != RECEIVING:
                    raise Rejected('Watch entered verification before receiving the complete image')
                if acknowledged > offset:
                    if acknowledged not in outstanding:
                        raise Rejected('Watch acknowledgement does not match a sent chunk boundary')
                    outstanding = [end for end in outstanding if end > acknowledged]
                    offset = acknowledged
                    progress(offset, total)
                if offset == total:
                    break
                if chunk < 1:
                    raise Rejected('Watch has no active ATT connection')
                if len(outstanding) < window and sent < total:
                    end = min(total, sent + chunk)
                    packet = struct.pack('<II', session, sent) + image[sent:end]
                    outstanding.append(end)
                    sent = end
                    # Do not shrink a full negotiated packet to an old BlueZ
                    # WNR limit of20: response writes retain the two-slot window.
                    await gatt_call(client.write_gatt_char(DATA, packet, response=without_response_limit < len(packet)))
                    continue
                await notifications.wait(lambda s: s[2] > offset or s[0] != RECEIVING, validate=identity)
            if offset != total:
                raise Rejected('Watch entered verification before receiving the complete image')
            if timings:
                timings.phase('verify')
            if state == RECEIVING:
                await gatt_call(client.write_gatt_char(CONTROL, struct.pack('<BI', 2, session), response=True))
            elif state != VERIFYING:
                raise Rejected('Unexpected transfer state')
            status = await notifications.wait(lambda s: s[0] == READY, timeout=45, validate=identity)
            if status[1:4] != (len(image), len(image), session):
                raise Rejected('Verified image does not match this transfer')
        print('Watch verified the image. Tap INSTALL, then KEEP after reboot.', flush=True)
    finally:
        if timings:
            timings.phase(None)
        try:
            await gatt_call(client.stop_notify(STATUS))
        except Exception:
            pass  # Disconnect cleanup must not hide the transfer result.


def target_device_path(address, adapter):
    if not re.fullmatch(r'(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}', address):
        raise ValueError('Invalid configured watch address')
    if not re.fullmatch(r'hci[0-9]+', adapter):
        raise ValueError('Invalid configured Bluetooth adapter')
    return '/org/bluez/' + adapter + '/dev_' + address.upper().replace(':', '_')


async def bluez_device_properties(path):
    """Read only the configured Device1; never start discovery or alter it."""
    from dbus_fast import BusType, DBusError, Message, MessageType
    from dbus_fast.aio import MessageBus
    bus = MessageBus(bus_type=BusType.SYSTEM)
    try:
        await bus.connect()
        reply = await bus.call(Message(
            destination='org.bluez', path=path,
            interface='org.freedesktop.DBus.Properties', member='GetAll',
            signature='s', body=['org.bluez.Device1']))
        if reply is None:
            raise RuntimeError('BlueZ did not answer the configured-device lookup')
        if reply.message_type == MessageType.ERROR:
            if reply.error_name in (
                'org.freedesktop.DBus.Error.UnknownObject',
                'org.freedesktop.DBus.Error.UnknownInterface',
                'org.freedesktop.DBus.Error.UnknownMethod',
                'org.bluez.Error.DoesNotExist',
            ):
                # BlueZ can answer UnknownMethod when the object at this exact
                # path has not been created by discovery yet.
                return None
            message = reply.body[0] if reply.body and isinstance(reply.body[0], str) else 'Device lookup failed'
            raise DBusError(reply.error_name, message)
        if reply.message_type != MessageType.METHOD_RETURN or reply.signature != 'a{sv}':
            raise RuntimeError('Unexpected BlueZ configured-device response')
        return {name: value.value for name, value in reply.body[0].items()}
    finally:
        bus.disconnect()


async def connected_target(address, adapter, properties_reader=None, device_factory=None):
    """Reuse only a connected Device1 matching both configured identity fields."""
    path = target_device_path(address, adapter)
    properties = await asyncio.wait_for((properties_reader or bluez_device_properties)(path), 3)
    if not properties or properties.get('Connected') is not True:
        return None
    if (properties.get('Adapter') != '/org/bluez/' + adapter or
            not isinstance(properties.get('Address'), str) or
            properties['Address'].upper() != address.upper()):
        return None
    if device_factory is None:
        from bleak.backends.device import BLEDevice
        device_factory = BLEDevice
    # Match the BLEDevice details produced by Bleak's BlueZ scanner. Supplying
    # the existing path avoids Bleak's implicit advertisement scan; its client
    # skips Device1.Connect if BlueZ still reports this device connected.
    name = properties.get('Alias') or properties.get('Name')
    return device_factory(address.upper(), name,
                          {'path': path, 'props': properties}, properties.get('RSSI', -127))


async def upload(args, scanner=None, client_factory=None, connected_finder=None):
    if scanner is None:
        from bleak import BleakClient, BleakScanner
        scanner, client_factory = BleakScanner, BleakClient
    connected_finder = connected_finder or connected_target
    _, _, image, _ = ota_package.read_package(args.package, args.sha256)
    timings = TransferTiming()
    deadline = time.monotonic() + 90
    last_progress = 0
    def progress(done, total):
        nonlocal last_progress, deadline
        if done > last_progress:
            deadline = time.monotonic() + 90
            last_progress = done
        ota_package.progress(done, total)
    print('Connecting to goPine. Leave the watch on Ready to connect.', flush=True)
    while time.monotonic() < deadline:
        try:
            device = await connected_finder(args.address, args.adapter)
            if device is None:
                device = await scanner.find_device_by_address(args.address, timeout=5, adapter=args.adapter)
            else:
                print(f'Using the configured watch connection on {args.adapter}.', flush=True)
            if device is None:
                continue
            async with client_factory(device, timeout=15, adapter=args.adapter) as client:
                if client.services.get_service(SERVICE) is None:
                    raise Rejected('Selected watch does not support goPine direct updates')
                await send_connected(client, image, args.session, progress, last_progress, timings)
                print(timings.summary(), flush=True)
                return
        except Rejected:
            raise
        except Exception as error:
            print(f'Connection interrupted: {error}. Reconnecting to acknowledged bytes…', flush=True)
            await asyncio.sleep(1)
    raise TimeoutError('Watch not reached. Keep goPine on its update screen and retry.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--package', type=pathlib.Path, required=True)
    parser.add_argument('--sha256', required=True)
    parser.add_argument('--address', required=True)
    parser.add_argument('--adapter', required=True)
    parser.add_argument('--session', type=int, required=True)
    args = parser.parse_args()
    if not 0 < args.session <= 0xffffffff:
        parser.error('session must be a nonzero uint32')
    from ble_session import TargetLock
    with TargetLock(args.adapter, args.address):
        asyncio.run(upload(args))

if __name__ == '__main__':
    try:
        main()
    except (Exception, KeyboardInterrupt) as error:
        print(f'Direct update stopped: {error}', flush=True)
        raise SystemExit(1)
