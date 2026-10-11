"""Offline native-operation timeout, cancellation and reconnect regressions."""
import asyncio
from contextlib import redirect_stdout
import io
import pathlib
import struct
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import direct_upload as direct


class Watch:
    def __init__(self, hang):
        self.hang = hang
        self.hung = False
        self.phase = direct.WAITING
        self.total = self.offset = self.session = 0
        self.native = self.max_native = self.connections = 0
        self.events = []


class Client:
    def __init__(self, watch):
        self.watch = watch
        self.callback = None
        self.services = SimpleNamespace(
            get_service=lambda _: object(),
            get_characteristic=lambda uuid: SimpleNamespace(
                properties=['read', 'write'] if uuid == direct.CONTROL else ['write-without-response'],
                max_write_without_response_size=244))

    async def native(self, stage, action=lambda: None):
        watch = self.watch
        watch.native += 1
        watch.max_native = max(watch.max_native, watch.native)
        assert watch.native == 1, 'New native operation overlapped cancellation cleanup'
        watch.events.append((stage, 'start'))
        try:
            result = action()
            if watch.hang == stage and not watch.hung:
                watch.hung = True
                try:
                    await asyncio.Event().wait()
                except asyncio.CancelledError:
                    watch.events.append((stage, 'cancel'))
                    # Real backend cleanup can yield before releasing its D-Bus
                    # request. The next GATT operation must wait for this.
                    await asyncio.sleep(0.01)
                    watch.events.append((stage, 'cancelled'))
                    raise
            return result
        finally:
            watch.native -= 1
            watch.events.append((stage, 'end'))

    async def __aenter__(self):
        self.watch.connections += 1
        await self.native('connect')
        return self

    async def __aexit__(self, *_):
        assert self.callback is None
        await self.native('disconnect')

    def status(self):
        watch = self.watch
        return struct.pack('<BBBBIII', 2, watch.phase, 0, 244,
                           watch.total, watch.offset, watch.session)

    def notify(self):
        if self.callback:
            self.callback(direct.STATUS, self.status())

    async def start_notify(self, _, callback):
        # A partial setup can install a callback before its native await stalls.
        await self.native('subscribe', lambda: setattr(self, 'callback', callback))

    async def stop_notify(self, _):
        try:
            await self.native('unsubscribe')
        finally:
            self.callback = None

    async def read_gatt_char(self, characteristic):
        if characteristic == direct.CONTROL:
            return await self.native('capabilities', lambda: bytes((1, 2, 1, 0)))
        return await self.native('status', self.status)

    async def write_gatt_char(self, characteristic, packet, response):
        watch = self.watch
        if characteristic == direct.CONTROL:
            assert response
            def command():
                if packet[0] == 1:
                    assert watch.phase == direct.WAITING
                    watch.session, watch.total = struct.unpack_from('<II', packet, 1)
                    watch.phase = direct.RECEIVING
                else:
                    assert packet[0] == 2 and watch.offset == watch.total
                    watch.phase = direct.READY
                self.notify()
            return await self.native('control', command)
        assert not response
        def commit():
            session, offset = struct.unpack_from('<II', packet)
            assert session == watch.session and offset == watch.offset
            watch.offset += len(packet) - 8
            self.notify()
        # The watch can commit data even when BlueZ's local write promise stalls.
        return await self.native('data', commit)


class NativeTimeoutTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.timeout = patch.object(direct, 'GATT_TIMEOUT_SECONDS', 0.02)
        self.timeout.start()
        self.addCleanup(self.timeout.stop)

    async def bounded(self, coroutine):
        task = asyncio.create_task(coroutine)
        try:
            done, _ = await asyncio.wait([task], timeout=0.5)
            self.assertIn(task, done, 'Native request did not enforce its own timeout')
            return await task
        finally:
            if not task.done():
                task.cancel()
                try:
                    await task
                except asyncio.CancelledError:
                    pass

    async def test_native_timeouts_finish_cancellation_before_unsubscribe(self):
        for stage in ('subscribe', 'capabilities', 'data'):
            with self.subTest(stage=stage):
                watch = Watch(stage)
                client = Client(watch)
                with self.assertRaises(TimeoutError):
                    await self.bounded(direct.send_connected(client, bytes(1000), 42, lambda *_: None))
                self.assertTrue(watch.hung)
                self.assertEqual(watch.max_native, 1)
                self.assertEqual(watch.native, 0)
                self.assertIsNone(client.callback)
                self.assertLess(watch.events.index((stage, 'cancelled')),
                                watch.events.index(('unsubscribe', 'start')))
                if stage != 'data':
                    self.assertNotIn(('data', 'start'), watch.events)

    async def test_stalled_unsubscribe_is_bounded_without_hiding_verified_result(self):
        watch = Watch('unsubscribe')
        client = Client(watch)
        with redirect_stdout(io.StringIO()):
            await self.bounded(direct.send_connected(client, bytes(1000), 42, lambda *_: None))
        self.assertEqual(watch.phase, direct.READY)
        self.assertEqual(watch.offset, 1000)
        self.assertEqual(watch.native, 0)
        self.assertEqual(watch.max_native, 1)
        self.assertIsNone(client.callback)
        self.assertIn(('unsubscribe', 'cancelled'), watch.events)

    async def test_upload_reconnect_waits_for_cleanup_and_resumes_committed_write(self):
        watch = Watch('data')
        args = SimpleNamespace(package=pathlib.Path('fixture.zip'), sha256='fixture',
                               address='C9:9E:15:7A:69:B4', adapter='hci1', session=42)
        clients, progress = [], []
        def factory(*_, **__):
            client = Client(watch)
            clients.append(client)
            return client
        async def connected(*_):
            return object()
        sleep = asyncio.sleep
        async def quick_retry(delay):
            await sleep(0 if delay == 1 else delay)
        with patch.object(direct.ota_package, 'read_package', return_value=(None, None, bytes(1000), None)), \
                patch.object(direct.ota_package, 'progress', side_effect=lambda done, _: progress.append(done)), \
                patch.object(direct.asyncio, 'sleep', quick_retry), redirect_stdout(io.StringIO()):
            await self.bounded(direct.upload(args, scanner=object(), client_factory=factory,
                                             connected_finder=connected))
        self.assertEqual(watch.connections, 2)
        self.assertEqual(watch.max_native, 1)
        self.assertEqual(watch.native, 0)
        self.assertEqual(watch.phase, direct.READY)
        self.assertEqual(watch.offset, 1000)
        self.assertIn(236, progress)
        self.assertTrue(all(client.callback is None for client in clients))
        cancelled = watch.events.index(('data', 'cancelled'))
        unsubscribe = watch.events.index(('unsubscribe', 'end'))
        disconnected = watch.events.index(('disconnect', 'end'))
        second_connect = watch.events.index(('connect', 'start'), 1)
        self.assertLess(cancelled, unsubscribe)
        self.assertLess(unsubscribe, disconnected)
        self.assertLess(disconnected, second_connect)


if __name__ == '__main__':
    unittest.main()
