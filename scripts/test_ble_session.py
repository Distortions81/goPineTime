"""Offline process-level exclusion and diagnostic cleanup checks."""
import asyncio
from contextlib import redirect_stderr, redirect_stdout
import io
import os
from pathlib import Path
import select
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import ble_session
from ble_session import SessionBusy, SessionLockError, TargetLock
import pairing_harness
import pairing_probe

ADDRESS = 'C9:9E:15:7A:69:B4'
CHILD = '''
import sys
from ble_session import TargetLock, SessionBusy
try:
    with TargetLock(sys.argv[1], sys.argv[2]):
        print('held', flush=True)
        if len(sys.argv) > 3:
            input()
except SessionBusy:
    sys.exit(73)
'''


class TargetLockTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.patch = patch.object(ble_session.tempfile, 'gettempdir', return_value=self.temp.name)
        self.patch.start()
        self.addCleanup(self.patch.stop)
        self.directory = Path(self.temp.name) / f'gopine-ble-{os.geteuid()}'
        # Another checkout / working directory must use the same target lock.
        self.other_checkout = Path(self.temp.name) / 'other-checkout'
        self.other_checkout.mkdir()
        self.env = dict(os.environ, TMPDIR=self.temp.name,
                        PYTHONPATH=str(Path(__file__).resolve().parent))

    def child(self, adapter='hci1', address=ADDRESS):
        return subprocess.run([sys.executable, '-c', CHILD, adapter, address],
                              env=self.env, cwd=self.other_checkout, text=True,
                              capture_output=True, timeout=5)

    def test_cross_process_conflict_canonicalizes_adapter_and_address(self):
        with TargetLock('HCI01', ADDRESS.lower()):
            result = self.child()
            self.assertEqual(result.returncode, 73, result.stderr)
            self.assertEqual(result.stdout, '')

    def test_other_targets_and_adapters_can_run_concurrently(self):
        with TargetLock('hci1', ADDRESS):
            for adapter, address in [('hci2', ADDRESS), ('hci1', 'C9:9E:15:7A:69:B5')]:
                result = self.child(adapter, address)
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_exception_releases_without_unlinking_lock_file(self):
        with self.assertRaisesRegex(RuntimeError, 'test cleanup'):
            with TargetLock('hci1', ADDRESS):
                lockfile, = self.directory.iterdir()
                inode = lockfile.stat().st_ino
                raise RuntimeError('test cleanup')
        result = self.child()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(lockfile.stat().st_ino, inode)
        self.assertEqual(self.directory.stat().st_mode & 0o777, 0o700)
        self.assertEqual(lockfile.stat().st_mode & 0o777, 0o600)

    def test_process_death_releases_lock(self):
        child = subprocess.Popen([sys.executable, '-c', CHILD, 'hci1', ADDRESS, 'hold'],
                                 env=self.env, cwd=self.other_checkout, text=True,
                                 stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            readable, _, _ = select.select([child.stdout], [], [], 5)
            self.assertTrue(readable, 'lock holder did not start')
            self.assertEqual(child.stdout.readline().strip(), 'held')
            with self.assertRaises(SessionBusy):
                with TargetLock('hci1', ADDRESS):
                    self.fail('Concurrent session acquired the target')
            child.terminate()
            child.wait(timeout=5)
            with TargetLock('hci1', ADDRESS):
                pass
        finally:
            if child.poll() is None:
                child.kill()
            child.communicate(timeout=5)

    def test_rejects_insecure_directory(self):
        self.directory.mkdir(mode=0o700)
        self.directory.chmod(0o755)
        with self.assertRaises(SessionLockError):
            with TargetLock('hci1', ADDRESS):
                self.fail('Used a non-private directory')

    def test_rejects_symlink_lock_file_without_changing_destination(self):
        with TargetLock('hci1', ADDRESS):
            pass
        lockfile, = self.directory.iterdir()
        lockfile.unlink()
        destination = Path(self.temp.name) / 'unrelated'
        destination.write_text('untouched')
        lockfile.symlink_to(destination)
        with self.assertRaises(SessionLockError):
            with TargetLock('hci1', ADDRESS):
                self.fail('Followed a lock file symlink')
        self.assertEqual(destination.read_text(), 'untouched')

    def test_diagnostic_cli_holds_lock_through_asyncio_shutdown(self):
        for module in (pairing_harness, pairing_probe):
            with self.subTest(module=module.__name__):
                cleaned = []

                async def background():
                    try:
                        await asyncio.Event().wait()
                    finally:
                        with self.assertRaises(SessionBusy):
                            with TargetLock('hci1', ADDRESS):
                                self.fail('Target released before async cleanup')
                        cleaned.append(True)

                async def run(*args):
                    with self.assertRaises(SessionBusy):
                        with TargetLock('hci1', ADDRESS):
                            self.fail('Target not locked during diagnostic run')
                    asyncio.create_task(background())
                    await asyncio.sleep(0)
                    return 0

                target = pairing_harness.Harness if module is pairing_harness else pairing_probe
                with patch.object(target, 'run', run), redirect_stdout(io.StringIO()):
                    result = module.main(['--log', str(Path(self.temp.name) / (module.__name__ + '.jsonl'))])
                self.assertEqual(result, 0)
                self.assertEqual(cleaned, [True])
                self.assertEqual(self.child().returncode, 0)

    def test_diagnostic_cli_busy_is_concise_and_never_runs_bluetooth(self):
        for module in (pairing_harness, pairing_probe):
            with self.subTest(module=module.__name__), TargetLock('hci1', ADDRESS):
                stderr = io.StringIO()
                with patch.object(module.asyncio, 'run') as run, redirect_stdout(io.StringIO()), redirect_stderr(stderr):
                    result = module.main(['--log', str(Path(self.temp.name) / (module.__name__ + '-busy.jsonl'))])
                self.assertEqual(result, 1)
                run.assert_not_called()
                self.assertIn('Another goPine Bluetooth session', stderr.getvalue())
                self.assertNotIn('Traceback', stderr.getvalue())

    def test_diagnostic_cli_keyboard_interrupt_releases_target(self):
        for module in (pairing_harness, pairing_probe):
            with self.subTest(module=module.__name__):
                def interrupted(coroutine):
                    coroutine.close()
                    raise KeyboardInterrupt
                with patch.object(module.asyncio, 'run', interrupted), redirect_stdout(io.StringIO()):
                    result = module.main(['--log', str(Path(self.temp.name) / (module.__name__ + '-interrupt.jsonl'))])
                self.assertEqual(result, 130)
                self.assertEqual(self.child().returncode, 0)


class ProbeCleanupTests(unittest.IsolatedAsyncioTestCase):
    async def run_probe(self, check_agent=False, unregister_error=None):
        state = SimpleNamespace(disconnected=False, client_disconnected=False)

        class FakeBus:
            async def connect(self):
                return self
            async def introspect(self, *args):
                return None
            def get_proxy_object(self, *args):
                return self
            def get_interface(self, *args):
                return self
            def export(self, *args):
                pass
            async def call_register_agent(self, *args):
                pass
            async def call_unregister_agent(self, *args):
                if unregister_error:
                    raise unregister_error
            def disconnect(self):
                state.disconnected = True

        class FakeClient:
            def __init__(self, *args, **kwargs):
                self.is_connected = False
            async def connect(self):
                raise RuntimeError('service discovery failed')
            async def disconnect(self):
                state.client_disconnected = True

        class Interface:
            def __init__(self, *args):
                pass

        async def no_sleep(*args):
            pass

        modules = {
            'bleak': SimpleNamespace(BleakClient=FakeClient),
            'dbus_fast': SimpleNamespace(BusType=SimpleNamespace(SYSTEM=0), DBusError=RuntimeError),
            'dbus_fast.aio': SimpleNamespace(MessageBus=lambda **kwargs: FakeBus()),
            'dbus_fast.service': SimpleNamespace(ServiceInterface=Interface, method=lambda: lambda function: function),
        }
        args = SimpleNamespace(adapter='hci1', address=ADDRESS, mode='trace', cycles=1, check_agent=check_agent)
        recorder = SimpleNamespace(emit=lambda *args, **kwargs: None)
        with patch.dict(sys.modules, modules), patch.object(pairing_probe.asyncio, 'sleep', no_sleep):
            if unregister_error:
                with self.assertRaises(type(unregister_error)):
                    await pairing_probe.run(args, recorder)
            else:
                self.assertEqual(await pairing_probe.run(args, recorder), 1)
        return state

    async def test_failed_partial_connect_still_disconnects_client(self):
        state = await self.run_probe()
        self.assertTrue(state.client_disconnected)
        self.assertTrue(state.disconnected)

    async def test_agent_cleanup_failure_or_cancellation_still_disconnects_bus(self):
        for error in (RuntimeError('unregister failed'), asyncio.CancelledError()):
            state = await self.run_probe(check_agent=True, unregister_error=error)
            self.assertTrue(state.disconnected)


if __name__ == '__main__':
    unittest.main()
