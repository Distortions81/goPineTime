import asyncio
from contextlib import redirect_stdout
import io
import struct
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, Mock, patch
import direct_upload as direct

class Client:
    def __init__(self, size=500, offset=0, session=0, state=direct.WAITING):
        self.total,self.offset,self.session,self.state=(0 if state==direct.WAITING else size),offset,session,state
        self.writes=[]; self.reads=0; self.callback=None
    async def read_gatt_char(self,char):
        self.reads+=1
        return self.raw_status()
    def raw_status(self):
        return struct.pack('<BBBBIII',2,self.state,0,244,self.total,self.offset,self.session)
    async def start_notify(self,char,callback): self.callback=callback
    async def stop_notify(self,char): self.callback=None
    def notify(self):
        if self.callback: self.callback(direct.STATUS,self.raw_status())
    async def write_gatt_char(self,char,data,response):
        assert response
        self.writes.append((char,bytes(data)))
        if char==direct.CONTROL:
            if data[0]==1:
                self.session,self.total=struct.unpack_from('<II',data,1);self.offset=0;self.state=direct.RECEIVING
            elif data[0]==2:self.state=direct.READY
            else:raise AssertionError('Remote install/cancel command')
        else:
            session,offset=struct.unpack_from('<II',data)
            assert session==self.session and offset==self.offset
            self.offset+=len(data)-8
        self.notify()

class Tests(unittest.IsolatedAsyncioTestCase):
    async def test_transfer_and_no_remote_install(self):
        c=Client();progress=[]
        await direct.send_connected(c,bytes(500),42,lambda d,t:progress.append(d))
        self.assertEqual(c.offset,500)
        self.assertEqual(c.reads,1)
        self.assertEqual([len(data)-8 for char,data in c.writes if char==direct.DATA],[236,236,28])
        self.assertEqual(progress[-1],500)
        self.assertEqual([p[0] for char,p in c.writes if char==direct.CONTROL],[1,2])
    async def test_resume_at_acknowledged_offset(self):
        c=Client(offset=192,session=42,state=direct.RECEIVING)
        await direct.send_connected(c,bytes(500),42,lambda d,t:None)
        char,data=c.writes[0]
        self.assertEqual(char,direct.DATA)
        self.assertEqual(struct.unpack_from('<I',data,4)[0],192)
    async def test_wrong_session_is_never_overwritten(self):
        c=Client(session=43,state=direct.RECEIVING)
        with self.assertRaises(direct.Rejected):
            await direct.send_connected(c,bytes(500),42,lambda d,t:None)
        self.assertFalse(c.writes)
    async def test_already_verified_does_not_resend(self):
        c=Client(offset=500,session=42,state=direct.READY)
        await direct.send_connected(c,bytes(500),42,lambda d,t:None)
        self.assertFalse(c.writes)
    async def test_notification_timeout_resumes_committed_bytes(self):
        from unittest.mock import patch
        class LostNotification(Client):
            def notify(self):
                if self.offset == 236: return
                super().notify()
        c=LostNotification()
        wait=direct.StatusNotifications.wait
        async def short_wait(instance,predicate,timeout=15,validate=lambda status:None):
            return await wait(instance,predicate,timeout=0.01,validate=validate)
        with patch.object(direct.StatusNotifications,'wait',short_wait):
            with self.assertRaises(TimeoutError):
                await direct.send_connected(c,bytes(500),42,lambda d,t:None)
        self.assertEqual(c.offset,236)
        self.assertIsNone(c.callback)
        await direct.send_connected(c,bytes(500),42,lambda d,t:None)
        offsets=[struct.unpack_from('<I',data,4)[0] for char,data in c.writes if char==direct.DATA]
        self.assertEqual(offsets,[0,236,472])
        self.assertEqual(c.reads,2)

    async def test_resume_never_moves_backwards(self):
        c=Client(offset=100,session=42,state=direct.RECEIVING)
        with self.assertRaises(direct.Rejected):
            await direct.send_connected(c,bytes(500),42,lambda d,t:None,minimum_offset=236)
        self.assertFalse(c.writes)

    async def test_reset_after_progress_never_sends_another_begin(self):
        client = Client()
        with self.assertRaisesRegex(direct.Rejected, 'lost previously acknowledged'):
            await direct.send_connected(client, bytes(500), 42, lambda d, t: None, minimum_offset=236)
        self.assertFalse(client.writes)

    async def test_notification_failure_is_rejected_and_unsubscribed(self):
        class FailedNotification(Client):
            def notify(self):
                if self.state==direct.RECEIVING: self.state=direct.FAILED
                super().notify()
        c=FailedNotification()
        with self.assertRaises(direct.Rejected):
            await direct.send_connected(c,bytes(500),42,lambda d,t:None)
        self.assertIsNone(c.callback)
        self.assertFalse(any(char==direct.DATA for char,_ in c.writes))

    def test_reject_invalid_status(self):
        for data in (b'',bytes(16),struct.pack('<BBBBIII',1,2,0,244,100,0,42),struct.pack('<BBBBIII',2,2,0,244,100,101,42),struct.pack('<BBBBIII',2,5,1,244,100,50,42)):
            with self.assertRaises(direct.Rejected):direct.decode_status(data)


class ConnectedTargetTests(unittest.IsolatedAsyncioTestCase):
    address = 'C9:9E:15:7A:69:B4'
    adapter = 'hci1'
    path = '/org/bluez/hci1/dev_C9_9E_15_7A_69_B4'

    def properties(self, **overrides):
        return {'Address': self.address, 'Adapter': '/org/bluez/hci1',
                'Connected': True, 'Alias': 'InfiniTime', **overrides}

    async def find(self, properties):
        reader = AsyncMock(return_value=properties)
        def device_factory(address, name, details, rssi):
            return SimpleNamespace(address=address, name=name, details=details, rssi=rssi)
        device = await direct.connected_target(self.address, self.adapter, reader, device_factory)
        reader.assert_awaited_once_with(self.path)
        return device

    async def test_connected_target_preserves_exact_bluez_path(self):
        properties = self.properties(Address=self.address.lower())
        device = await self.find(properties)
        self.assertEqual(device.address, self.address)
        self.assertEqual(device.details, {'path': self.path, 'props': properties})

    async def test_other_adapter_or_address_and_disconnected_cache_are_not_reused(self):
        for properties in (None, {}, self.properties(Connected=False),
                           self.properties(Adapter='/org/bluez/hci0'),
                           self.properties(Address='C9:9E:15:7A:69:B5')):
            with self.subTest(properties=properties):
                self.assertIsNone(await self.find(properties))

    async def test_actual_lookup_errors_are_not_misreported_as_missing_device(self):
        reader = AsyncMock(side_effect=PermissionError('D-Bus access denied'))
        with self.assertRaises(PermissionError):
            await direct.connected_target(self.address, self.adapter, reader)

    async def run_upload(self, connected, advertised):
        args = SimpleNamespace(address=self.address, adapter=self.adapter,
                               package='candidate.zip', sha256='hash', session=42)
        scanner = SimpleNamespace(find_device_by_address=AsyncMock(return_value=advertised))
        lookup = AsyncMock(return_value=connected)
        selected = []
        class UploadClient(Client):
            def __init__(self):
                super().__init__()
                self.services = SimpleNamespace(get_service=lambda uuid: object() if uuid == direct.SERVICE else None)
                self.cleaned = False
            async def __aenter__(self):
                return self
            async def __aexit__(self, *exc):
                self.cleaned = True
        client = UploadClient()
        def factory(device, **kwargs):
            selected.append((device, kwargs))
            return client
        # If no usable device is selected, the next deadline check expires.
        # Replace only the uploader's clock; asyncio still uses real monotonic
        # time for its notification and connection deadlines.
        with patch.object(direct.ota_package, 'read_package', return_value=(None, None, bytes(500), None)), \
             patch.object(direct, 'time', SimpleNamespace(monotonic=Mock(side_effect=[0, 0, 91, 91, 91, 91]))), \
             redirect_stdout(io.StringIO()):
            await direct.upload(args, scanner, factory, lookup)
        return client, selected, scanner, lookup

    async def test_preconnected_watch_transfers_without_advertisements_or_scan(self):
        device = await self.find(self.properties())
        client, selected, scanner, lookup = await self.run_upload(device, None)
        self.assertEqual(client.offset, 500)
        self.assertTrue(client.cleaned)
        self.assertIs(selected[0][0], device)
        self.assertEqual(selected[0][1]['adapter'], self.adapter)
        scanner.find_device_by_address.assert_not_awaited()
        lookup.assert_awaited_once_with(self.address, self.adapter)

    async def test_absent_or_disconnected_watch_falls_back_to_scanning(self):
        for properties in (None, self.properties(Connected=False),
                           self.properties(Adapter='/org/bluez/hci0'),
                           self.properties(Address='C9:9E:15:7A:69:B5')):
            with self.subTest(properties=properties):
                connected = await self.find(properties)
                advertised = object()
                client, selected, scanner, lookup = await self.run_upload(connected, advertised)
                self.assertEqual(client.offset, 500)
                self.assertTrue(client.cleaned)
                self.assertIs(selected[0][0], advertised)
                scanner.find_device_by_address.assert_awaited_once_with(self.address, timeout=5, adapter=self.adapter)


class DBusLookupTests(unittest.IsolatedAsyncioTestCase):
    async def lookup(self, reply=None, block=False):
        class DBusError(RuntimeError):
            pass
        class Bus:
            closed = False
            messages = []
            async def connect(self):
                return self
            async def call(self, message):
                self.messages.append(message)
                if block:
                    await asyncio.Future()
                return reply
            def disconnect(self):
                self.closed = True
        bus = Bus()
        modules = {
            'dbus_fast': SimpleNamespace(BusType=SimpleNamespace(SYSTEM='system'),
                DBusError=DBusError, Message=SimpleNamespace,
                MessageType=SimpleNamespace(ERROR='error', METHOD_RETURN='return')),
            'dbus_fast.aio': SimpleNamespace(MessageBus=lambda **kwargs: bus),
        }
        return bus, modules, DBusError

    async def test_lookup_only_reads_exact_device_and_closes_bus(self):
        props = {'Connected': SimpleNamespace(value=True)}
        reply = SimpleNamespace(message_type='return', signature='a{sv}', body=[props])
        bus, modules, _ = await self.lookup(reply)
        with patch.dict('sys.modules', modules):
            result = await direct.bluez_device_properties(ConnectedTargetTests.path)
        self.assertEqual(result, {'Connected': True})
        self.assertTrue(bus.closed)
        self.assertEqual(len(bus.messages), 1)
        message = bus.messages[0]
        self.assertEqual(message.path, ConnectedTargetTests.path)
        self.assertEqual((message.interface, message.member, message.body),
                         ('org.freedesktop.DBus.Properties', 'GetAll', ['org.bluez.Device1']))

    async def test_missing_object_is_distinct_from_access_denied(self):
        for name in ('org.freedesktop.DBus.Error.UnknownObject',
                     'org.freedesktop.DBus.Error.UnknownMethod',
                     'org.freedesktop.DBus.Error.AccessDenied'):
            with self.subTest(name=name):
                reply = SimpleNamespace(message_type='error', error_name=name, body=['lookup error'])
                bus, modules, error = await self.lookup(reply)
                with patch.dict('sys.modules', modules):
                    if name.endswith('AccessDenied'):
                        with self.assertRaises(error):
                            await direct.bluez_device_properties(ConnectedTargetTests.path)
                    else:
                        self.assertIsNone(await direct.bluez_device_properties(ConnectedTargetTests.path))
                self.assertTrue(bus.closed)

    async def test_cancelled_lookup_still_closes_bus(self):
        bus, modules, _ = await self.lookup(block=True)
        with patch.dict('sys.modules', modules):
            task = asyncio.create_task(direct.bluez_device_properties(ConnectedTargetTests.path))
            while not bus.messages:
                await asyncio.sleep(0)
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task
        self.assertTrue(bus.closed)


class HostStateTests(unittest.TestCase):
    def test_cli_lock_covers_async_client_cleanup_on_success_and_error(self):
        for fail in (False, True):
            with self.subTest(fail=fail):
                events = []
                class Lock:
                    active = False
                    def __init__(self, adapter, address):
                        self.target = adapter, address
                    def __enter__(self):
                        Lock.active = True
                        events.append('lock')
                    def __exit__(self, *exc):
                        Lock.active = False
                        events.append('unlock')
                class ClientContext:
                    async def __aenter__(inner):
                        self.assertTrue(Lock.active)
                    async def __aexit__(inner, *exc):
                        await asyncio.sleep(0)
                        self.assertTrue(Lock.active)
                        events.append('client_cleanup')
                async def upload(args):
                    async with ClientContext():
                        events.append('upload')
                        if fail:
                            raise RuntimeError('simulated connection failure')
                argv = ['direct_upload.py', '--package', 'candidate.zip', '--sha256', 'hash',
                        '--adapter', 'hci1', '--address', ConnectedTargetTests.address, '--session', '42']
                with patch('sys.argv', argv), patch.dict('sys.modules', {'ble_session': SimpleNamespace(TargetLock=Lock)}), \
                     patch.object(direct, 'upload', upload):
                    if fail:
                        with self.assertRaisesRegex(RuntimeError, 'simulated connection failure'):
                            direct.main()
                    else:
                        direct.main()
                self.assertFalse(Lock.active)
                self.assertEqual(events, ['lock', 'upload', 'client_cleanup', 'unlock'])

    def test_direct_success_waits_for_user_install(self):
        import pathlib
        import tempfile
        from unittest.mock import Mock, patch
        import ota_update
        with tempfile.TemporaryDirectory() as directory:
            job=ota_update.UploadJob(pathlib.Path('candidate.zip'),'hash','0.3.22',{'address':'C9:9E:15:7A:69:B4','adapter':'hci1'},)
            process=Mock();process.stdout=iter(['Upload 100% (100/100 bytes)\n']);process.wait.return_value=0
            context=Mock();context.__enter__=Mock(return_value=process);context.__exit__=Mock(return_value=False)
            with patch.object(ota_update,'OUTPUT',pathlib.Path(directory)),patch.object(ota_update.subprocess,'Popen',return_value=context) as launch:
                self.assertTrue(job.start());job.worker.join()
                self.assertEqual(job.state,'awaiting_install')
                self.assertIn('--session',launch.call_args.args[0])
                self.assertFalse(job.start())
                self.assertTrue(job.confirm())

class PipelineSimulationTests(unittest.TestCase):
    def test_actual_sender_pipeline_removes_latency_wait_without_native_concurrency(self):
        from ota_benchmark import benchmark
        result = benchmark()['profiles']
        self.assertGreater(result['latency_bound']['speedup'], 1.9)
        self.assertLess(result['flash_bound']['speedup'], 1.1)
        for modes in result.values():
            for mode in ('legacy', 'window_response', 'window_wnr'):
                self.assertEqual(modes[mode]['max_native'], 1)
                self.assertLessEqual(modes[mode]['max_queue'], 2)
                self.assertEqual(modes[mode]['packets'], 200)
        self.assertEqual(result['latency_bound']['legacy']['max_queue'], 1)
        self.assertEqual(result['latency_bound']['window_wnr']['max_queue'], 2)

    def test_wnr_backend_size_limit_uses_full_response_packets(self):
        from ota_benchmark import run_scenario
        result = run_scenario(size=1000, wnr_limit=20)
        self.assertEqual(result['packets'], 5)
        self.assertEqual(result['wnr_packets'], 0)
        self.assertEqual(result['connections'], 1)

    def test_missing_notification_is_covered_by_cumulative_flash_ack(self):
        from ota_benchmark import run_scenario
        result = run_scenario(size=1000, fault='lost_notification')
        self.assertEqual(result['connections'], 1)
        self.assertNotIn(236, result['progress'])
        self.assertEqual(result['progress'][-1], 1000)

    def test_lost_packet_and_queue_pressure_resume_only_verified_flash(self):
        from ota_benchmark import run_scenario
        for fault in ('lost_packet', 'queue_pressure'):
            with self.subTest(fault=fault):
                result = run_scenario(size=1000, fault=fault)
                self.assertEqual(result['connections'], 2)
                self.assertEqual(result['progress'][-1], 1000)
                self.assertEqual(result['max_native'], 1)
                self.assertLessEqual(result['max_queue'], 2)
        self.assertEqual(run_scenario(size=1000, fault='queue_pressure')['offsets'],
                         [0, 236, 472, 236, 472, 708, 944])

    def test_corrupt_identity_boundary_readback_or_reset_never_verifies(self):
        from ota_benchmark import run_scenario
        for fault in ('wrong_session', 'bad_boundary', 'readback', 'reset', 'bad_caps'):
            with self.subTest(fault=fault), self.assertRaises(direct.Rejected):
                run_scenario(size=1000, fault=fault)


if __name__ == '__main__':
    unittest.main()
