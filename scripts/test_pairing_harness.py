import asyncio
from contextlib import redirect_stderr
import io
import json
import os
import struct
import unittest
from unittest.mock import patch

import pairing_harness as harness


class Recorder:
    def __init__(self):
        self.events = []

    def emit(self, event, **fields):
        self.events.append({'event': event, **fields})

    def summary(self):
        return next(item for item in reversed(self.events) if item['event'] == 'summary')


class BlueZError(Exception):
    def __init__(self, name, message=''):
        self.type = 'org.bluez.Error.' + name
        super().__init__(self.type + ': ' + message)


class Transport:
    def __init__(self):
        self.policy = None
        self.bond = False
        self.connected = False
        self.connect_count = self.disconnect_count = self.cancel_count = self.pair_count = 0
        self.discover_count = 0
        self.closed = False
        self.calls = []
        self.writes = []
        self.trace = None
        self.fail_connect = set()
        self.pair_behavior = 'request'
        self.battery_behavior = 'normal'
        self.firmware_value = b'goPine 0.3.27'
        self.missing_characteristic = None
        self.missing_property = None
        self.fail_subscribe = False
        self.subscriptions = []
        self.fail_disconnect = False
        self.fail_close = False
        self.slow_connect = False
        self.slow_discover = False
        self.unexpected_reconnect_pin = False

    async def open(self, policy):
        self.calls.append('open')
        self.policy = policy

    async def preflight(self, mode):
        self.calls.append('preflight')
        if mode != 'trace' and self.bond:
            raise harness.CheckFailed('This PC already has a bond; refusing to replace or remove it')

    async def connect(self):
        self.calls.append('connect')
        self.connect_count += 1
        if self.slow_connect:
            await asyncio.sleep(1)
        if self.connect_count in self.fail_connect:
            raise BlueZError('Failed', 'Device not found')
        self.connected = True

    async def discover(self):
        self.calls.append('discover')
        self.discover_count += 1
        if self.slow_discover:
            await asyncio.sleep(1)
        if self.discover_count in self.fail_connect:
            raise BlueZError('Failed', 'Device not found')

    async def read(self, uuid):
        self.calls.append({harness.TRACE_UUID: 'read_trace',
                           harness.BATTERY_UUID: 'read_battery',
                           harness.FIRMWARE_UUID: 'read_firmware'}[uuid])
        if not self.connected:
            raise harness.CheckFailed('Watch is disconnected')
        if uuid == harness.TRACE_UUID:
            if isinstance(self.trace, Exception):
                raise self.trace
            if self.trace is not None:
                return self.trace
            value = bytes([1, 1 + int(self.bond), 6 if self.bond else 0, 0])
            value += struct.pack('<IBBH', self.connect_count * 1000, 2, 0, 0)
            if self.bond:
                value += struct.pack('<IBBH', self.connect_count * 1000 + 100, 13, 0, 0x1007)
            return value
        if uuid == harness.FIRMWARE_UUID:
            if isinstance(self.firmware_value, Exception):
                raise self.firmware_value
            return self.firmware_value
        if self.battery_behavior == 'timeout':
            await asyncio.sleep(1)
        if self.battery_behavior == 'bad':
            return bytes([255])
        if self.battery_behavior == 'empty':
            return b''
        if self.battery_behavior == 'other_error':
            raise BlueZError('Failed', 'ATT error: 0x0e')
        # Battery is a public value even when the link has no usable security.
        if self.unexpected_reconnect_pin and self.connect_count > 1:
            try:
                await self.policy.request(harness.DEVICE_PATH)
            except harness.AgentReject:
                pass
        return bytes([75])

    async def characteristic_properties(self, uuids):
        self.calls.append('characteristic_properties')
        properties = {uuid: {'read', 'write', 'notify'} for uuid in uuids
                      if uuid != self.missing_characteristic}
        if self.missing_property:
            uuid, prop = self.missing_property
            if uuid in properties:
                properties[uuid].discard(prop)
        return properties

    async def subscribe(self, uuid):
        self.calls.append('subscribe_battery')
        if self.fail_subscribe:
            raise BlueZError('Failed')
        self.subscriptions.append(uuid)

    async def paired(self):
        self.calls.append('paired')
        return self.bond

    async def pair(self):
        self.calls.append('pair')
        self.pair_count += 1
        self.connected = True
        if self.pair_behavior == 'success_without_pin':
            self.bond = True
            return
        if self.pair_behavior == 'reject_without_pin':
            raise BlueZError('AuthenticationRejected')
        if self.pair_behavior == 'timeout':
            await asyncio.sleep(1)
        if self.pair_behavior == 'comparison':
            self.policy.reject_association('numeric_comparison')
            raise BlueZError('AuthenticationRejected')
        try:
            await self.policy.request(harness.DEVICE_PATH)
        except harness.AgentReject:
            if self.pair_behavior == 'pin_then_timeout':
                await asyncio.sleep(1)
            if self.pair_behavior == 'success_after_rejection':
                self.bond = True
                return
            if self.pair_behavior == 'unrelated_error':
                raise BlueZError('Failed')
            raise BlueZError('AuthenticationRejected')
        self.bond = True

    async def cancel_pairing(self):
        self.calls.append('cancel')
        self.cancel_count += 1

    async def write(self, uuid, value):
        self.writes.append((uuid, value))

    async def disconnect(self):
        self.calls.append('disconnect')
        self.disconnect_count += 1
        self.connected = False
        if self.fail_disconnect:
            raise BlueZError('Failed')

    async def close(self):
        self.calls.append('close')
        self.closed = True
        if self.fail_close:
            raise BlueZError('Failed')


class HarnessTests(unittest.IsolatedAsyncioTestCase):
    async def run_harness(self, transport=None, **options):
        transport = transport or Transport()
        recorder = Recorder()
        settings = harness.Settings(cycles=options.pop('cycles', 1), cooldown=0, pin_hold=0,
                                    operation_timeout=0.02, connect_timeout=0.02,
                                    pair_timeout=0.02, pin_timeout=0.02,
                                    cleanup_timeout=0.02, **options)
        async def pin_reader(timeout):
            return 123456
        code = await harness.Harness(settings, transport, recorder, pin_reader).run()
        return code, transport, recorder

    async def test_probe_requires_all_milestones_on_every_cycle(self):
        code, transport, recorder = await self.run_harness(cycles=3)
        self.assertEqual(code, 0)
        self.assertEqual(transport.pair_count, 3)
        self.assertEqual(transport.connect_count, 0)
        self.assertEqual(transport.disconnect_count, 3)
        self.assertTrue(transport.closed)
        self.assertFalse(transport.bond)
        self.assertEqual(recorder.summary()['passed_cycles'], 3)
        self.assertEqual(sum(item['event'] == 'pin_requested' for item in recorder.events), 3)
        self.assertEqual(sum(item['event'] == 'watch_trace' for item in recorder.events), 3)
        self.assertNotIn('read_battery', transport.calls)
        self.assertFalse(transport.writes)

    async def test_no_discovery_never_passes(self):
        transport = Transport()
        transport.fail_connect = {1, 2, 3}
        code, transport, recorder = await self.run_harness(transport, cycles=3)
        self.assertEqual(code, 1)
        self.assertEqual(transport.pair_count, 0)
        self.assertEqual(recorder.summary()['passed_cycles'], 0)
        self.assertTrue(transport.closed)
        failures = [item for item in recorder.events if item['event'] == 'cycle_result']
        self.assertEqual([item['failed_phase'] for item in failures], ['discover'] * 3)

    async def test_failed_cycle_does_not_erase_next_cycle_evidence(self):
        transport = Transport()
        transport.fail_connect = {2}
        code, transport, recorder = await self.run_harness(transport, cycles=3)
        self.assertEqual(code, 1)
        self.assertEqual(recorder.summary()['passed_cycles'], 2)
        self.assertEqual(recorder.summary()['completed_cycles'], 3)
        self.assertEqual(transport.pair_count, 2)

    async def test_discovery_has_deadline_and_cleanup(self):
        transport = Transport()
        transport.slow_discover = True
        code, transport, recorder = await self.run_harness(transport)
        self.assertEqual(code, 1)
        self.assertTrue(transport.closed)
        self.assertEqual(transport.disconnect_count, 1)
        self.assertTrue(any(item.get('error_type') == 'TimeoutError' for item in recorder.events))

    async def test_preexisting_bond_is_never_touched(self):
        for mode in ('probe', 'full'):
            with self.subTest(mode=mode):
                transport = Transport()
                transport.bond = True
                code, transport, recorder = await self.run_harness(transport, mode=mode)
                self.assertEqual(code, 1)
                self.assertTrue(transport.bond)
                self.assertTrue(transport.closed)
                self.assertNotIn('connect', transport.calls)
                self.assertNotIn('pair', transport.calls)

    async def test_trace_allows_existing_bond_and_does_not_pair(self):
        transport = Transport()
        transport.bond = True
        code, transport, recorder = await self.run_harness(transport, mode='trace', cycles=3)
        self.assertEqual(code, 0)
        self.assertEqual(transport.connect_count, 1)
        self.assertNotIn('pair', transport.calls)
        self.assertNotIn('read_battery', transport.calls)
        self.assertTrue(transport.bond)

    async def test_partial_trace_keeps_complete_records_but_fails_trace_mode(self):
        transport = Transport()
        transport.trace = bytes([1, 2, 2, 0]) + struct.pack('<IBBH', 42, 7, 0, 0x205) + b'\x00'
        code, transport, recorder = await self.run_harness(transport, mode='trace')
        self.assertEqual(code, 1)
        report = next(item for item in recorder.events if item['event'] == 'watch_trace')
        self.assertFalse(report['complete'])
        self.assertEqual(report['events'][0]['result'], 0x205)
        self.assertEqual(report['declared_records'], 2)

    async def test_missing_trace_never_passes_trace_mode(self):
        transport = Transport()
        transport.trace = BlueZError('Failed')
        code, transport, recorder = await self.run_harness(transport, mode='trace')
        self.assertEqual(code, 1)
        self.assertFalse(recorder.summary()['passed'])

    async def test_probe_trace_failure_does_not_falsify_observed_pin_milestone(self):
        transport = Transport()
        transport.trace = BlueZError('Failed')
        code, transport, recorder = await self.run_harness(transport)
        self.assertEqual(code, 0)
        self.assertEqual(sum(item['event'] == 'trace_unavailable' for item in recorder.events), 1)

    async def test_pair_false_success_and_unrelated_failure_never_pass(self):
        for behavior in ('success_without_pin', 'success_after_rejection',
                         'reject_without_pin', 'comparison', 'unrelated_error'):
            with self.subTest(behavior=behavior):
                transport = Transport()
                transport.pair_behavior = behavior
                code, transport, recorder = await self.run_harness(transport)
                self.assertEqual(code, 1)
                self.assertEqual(recorder.summary()['passed_cycles'], 0)
                self.assertTrue(transport.closed)

    async def test_pair_timeout_never_passes_even_after_pin_request(self):
        for behavior in ('timeout', 'pin_then_timeout'):
            with self.subTest(behavior=behavior):
                transport = Transport()
                transport.pair_behavior = behavior
                code, transport, recorder = await self.run_harness(transport)
                self.assertEqual(code, 1)
                self.assertEqual(transport.cancel_count, 1)
                self.assertTrue(transport.closed)
                self.assertLess(transport.calls.index('cancel'), transport.calls.index('disconnect'))

    async def test_interruption_cancels_pair_and_cleans_up(self):
        transport = Transport()
        transport.pair_behavior = 'timeout'
        recorder = Recorder()
        settings = harness.Settings(cycles=1, pair_timeout=10, cooldown=0, pin_hold=0)
        task = asyncio.create_task(harness.Harness(settings, transport, recorder).run())
        while transport.pair_count == 0:
            await asyncio.sleep(0)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertEqual(transport.cancel_count, 1)
        self.assertEqual(transport.disconnect_count, 1)
        self.assertTrue(transport.closed)
        self.assertFalse(any(item.get('passed') for item in recorder.events
                             if item['event'] == 'cycle_result'))

    async def test_full_rejects_broken_battery(self):
        for behavior in ('bad', 'empty', 'timeout', 'other_error'):
            with self.subTest(behavior=behavior):
                transport = Transport()
                transport.battery_behavior = behavior
                code, transport, recorder = await self.run_harness(transport, mode='full')
                self.assertEqual(code, 1)
                self.assertEqual(transport.pair_count, 1)
                self.assertEqual(transport.calls.count('read_battery'), 1)
                self.assertFalse(transport.subscriptions)
                self.assertTrue(transport.closed)

    async def test_disconnect_failure_prevents_retries_and_success(self):
        transport = Transport()
        transport.fail_disconnect = True
        code, transport, recorder = await self.run_harness(transport, cycles=3)
        self.assertEqual(code, 1)
        self.assertEqual(transport.discover_count, 1)
        self.assertEqual(recorder.summary()['passed_cycles'], 0)
        self.assertTrue(transport.closed)

    async def test_teardown_failure_prevents_overall_pass(self):
        transport = Transport()
        transport.fail_close = True
        code, transport, recorder = await self.run_harness(transport)
        self.assertEqual(code, 1)
        self.assertFalse(recorder.summary()['teardown_complete'])

    async def test_full_pairs_once_then_reconnects_and_keeps_bond(self):
        code, transport, recorder = await self.run_harness(mode='full', cycles=3)
        self.assertEqual(code, 0)
        self.assertEqual(transport.pair_count, 1)
        self.assertEqual(transport.connect_count, 3)
        self.assertTrue(transport.bond)
        self.assertFalse(transport.writes)
        self.assertLess(transport.calls.index('pair'), transport.calls.index('connect'))
        self.assertLess(transport.calls.index('pair'), transport.calls.index('read_battery'))
        self.assertNotIn('123456', repr(recorder.events))

    async def test_companion_startup_reads_once_before_subscribing_on_each_connection(self):
        code, transport, recorder = await self.run_harness(mode='full', cycles=3)
        self.assertEqual(code, 0)
        self.assertEqual(transport.subscriptions, [harness.BATTERY_UUID] * 3)
        startup = [call for call in transport.calls
                   if call in ('read_battery', 'subscribe_battery')]
        self.assertEqual(startup, ['read_battery', 'subscribe_battery'] * 3)
        self.assertEqual(transport.calls.count('read_firmware'), 3)
        self.assertFalse(transport.writes)

    async def test_companion_required_uuid_and_properties_are_checked(self):
        fixture = json.loads(harness.COMPANION_FIXTURE.read_text())
        expected = {**fixture['companion_contract']['characteristics'],
                    **fixture['classic_contract']['characteristics']}
        for name, item in expected.items():
            for prop in [None, *item['properties']]:
                with self.subTest(name=name, property=prop):
                    transport = Transport()
                    if prop is None:
                        transport.missing_characteristic = item['uuid']
                    else:
                        transport.missing_property = item['uuid'], prop
                    code, transport, recorder = await self.run_harness(transport, mode='full')
                    self.assertEqual(code, 1)
                    self.assertNotIn('read_battery', transport.calls)
                    failure = next(item for item in recorder.events if item['event'] == 'cycle_result')
                    self.assertEqual(failure['failed_phase'], 'companion_gatt')

    async def test_classic_home_requires_firmware_even_when_battery_works(self):
        for value in (b'', b'\xff', BlueZError('Failed', 'read failed')):
            with self.subTest(value=type(value).__name__):
                transport = Transport()
                transport.firmware_value = value
                code, transport, recorder = await self.run_harness(transport, mode='full')
                self.assertEqual(code, 1)
                self.assertEqual(recorder.summary()['passed_cycles'], 0)
                self.assertTrue(any(item['event'] == 'battery_read' for item in recorder.events))
                self.assertEqual(transport.subscriptions, [harness.BATTERY_UUID])
                self.assertEqual(transport.calls.count('read_firmware'), 1)
                failure = next(item for item in recorder.events if item['event'] == 'cycle_result')
                self.assertEqual(failure['failed_phase'], 'firmware_read')
                self.assertTrue(transport.closed)

    async def test_firmware_peer_string_is_not_logged(self):
        transport = Transport()
        transport.firmware_value = b'private peer content 123456'
        code, transport, recorder = await self.run_harness(transport, mode='full')
        self.assertEqual(code, 0)
        self.assertNotIn('private peer content', repr(recorder.events))
        self.assertNotIn('123456', repr(recorder.events))
        event = next(item for item in recorder.events if item['event'] == 'firmware_read')
        self.assertEqual(event['bytes'], len(transport.firmware_value))

    async def test_companion_subscription_failure_does_not_pass(self):
        transport = Transport()
        transport.fail_subscribe = True
        code, transport, recorder = await self.run_harness(transport, mode='full')
        self.assertEqual(code, 1)
        self.assertEqual(transport.calls.count('read_battery'), 1)
        self.assertTrue(transport.closed)
        failure = next(item for item in recorder.events if item['event'] == 'cycle_result')
        self.assertEqual(failure['failed_phase'], 'battery_subscribe')

    async def test_full_fails_if_reconnect_asks_for_new_pin(self):
        transport = Transport()
        transport.unexpected_reconnect_pin = True
        code, transport, recorder = await self.run_harness(transport, mode='full', cycles=3)
        self.assertEqual(code, 1)
        self.assertEqual(transport.connect_count, 2)
        self.assertTrue(transport.bond)

    async def test_full_rejects_success_without_pin(self):
        transport = Transport()
        transport.pair_behavior = 'success_without_pin'
        code, transport, recorder = await self.run_harness(transport, mode='full', cycles=3)
        self.assertEqual(code, 1)
        self.assertEqual(transport.connect_count, 0)

    async def test_full_never_claims_authentication_from_battery_alone(self):
        for result in (0x0000, 0x1001, 0x1005, 0x0f07):
            with self.subTest(result=result):
                transport = Transport()
                transport.trace = bytes([1, 1, 6, 0]) + struct.pack('<IBBH', 1100, 13, 0, result)
                code, transport, recorder = await self.run_harness(transport, mode='full')
                self.assertEqual(code, 1)
                self.assertEqual(recorder.summary()['passed_cycles'], 0)
                self.assertTrue(any(item['event'] == 'battery_read' for item in recorder.events))
                self.assertEqual(transport.subscriptions, [harness.BATTERY_UUID])

    async def test_full_requires_new_security_trace_firmware(self):
        transport = Transport()
        transport.trace = bytes([1, 0, 0, 0])
        code, transport, recorder = await self.run_harness(transport, mode='full')
        self.assertEqual(code, 1)

    async def test_full_rejects_stale_security_from_previous_connection(self):
        transport = Transport()
        transport.trace = bytes([1, 2, 6, 0]) + struct.pack('<IBBH', 1100, 13, 0, 0x1007)
        transport.trace += struct.pack('<IBBH', 1200, 2, 0, 0)
        code, transport, recorder = await self.run_harness(transport, mode='full')
        self.assertEqual(code, 1)

    async def test_full_reconnect_requires_fresh_connect_and_security_records(self):
        transport = Transport()
        transport.trace = bytes([1, 2, 6, 0]) + struct.pack('<IBBH', 1000, 2, 0, 0)
        transport.trace += struct.pack('<IBBH', 1100, 13, 0, 0x1007)
        code, transport, recorder = await self.run_harness(transport, mode='full', cycles=3)
        self.assertEqual(code, 1)
        self.assertEqual(recorder.summary()['passed_cycles'], 1)
        self.assertEqual(transport.connect_count, 2)

    async def test_features_and_time_are_explicit_and_only_written_once(self):
        code, transport, recorder = await self.run_harness(mode='full', cycles=2,
                                                         write_features=True, write_time=True)
        self.assertEqual(code, 0)
        self.assertEqual(len(transport.writes), 7)
        times = [value for uuid, value in transport.writes if '2a2b' in uuid]
        self.assertEqual(len(times), 1)
        self.assertEqual(len(times[0]), 10)

    async def test_policy_rejects_wrong_device_without_reading_pin(self):
        async def forbidden(timeout):
            self.fail('Wrong device prompted for a PIN')
        policy = harness.PasskeyPolicy(harness.Settings(mode='full'), Recorder(), forbidden)
        policy.begin(1)
        with self.assertRaises(harness.AgentReject):
            await policy.request('/org/bluez/hci0/dev_OTHER')
        self.assertEqual(policy.requested, 0)

    async def test_hidden_pin_from_stdin_has_no_logged_value(self):
        read_fd, write_fd = os.pipe()
        try:
            os.write(write_fd, b'123456\n')
            with os.fdopen(read_fd) as stream, patch.object(harness.sys, 'stdin', stream), redirect_stderr(io.StringIO()) as output:
                self.assertEqual(await harness.hidden_pin(0.1), 123456)
                self.assertNotIn('123456', output.getvalue())
        finally:
            os.close(write_fd)

    async def test_hidden_pin_deadline_does_not_leave_reader_running(self):
        read_fd, write_fd = os.pipe()
        try:
            with os.fdopen(read_fd) as stream, patch.object(harness.sys, 'stdin', stream), redirect_stderr(io.StringIO()):
                with self.assertRaises(TimeoutError):
                    await harness.hidden_pin(0.01)
                self.assertFalse(asyncio.get_running_loop().remove_reader(read_fd))
        finally:
            os.close(write_fd)


class PureTests(unittest.TestCase):
    def test_classic_fixture_records_actual_home_readiness_gate(self):
        fixture = json.loads(harness.COMPANION_FIXTURE.read_text())
        classic = fixture['classic_contract']
        self.assertEqual(classic['source_revision'], 'd9f13a5d785b25b30870eb6ea99d4716b554a9b0')
        self.assertEqual(classic['marketing_version'], '1.0.2')
        self.assertEqual(classic['characteristics']['firmware_revision'], {
            'uuid': '00002a26-0000-1000-8000-00805f9b34fb',
            'properties': ['read'], 'discovery_actions': ['read'], 'readiness': 'nonempty_utf8'})
        self.assertEqual(harness.FIRMWARE_UUID, classic['characteristics']['firmware_revision']['uuid'])
        sources = {item['path']: item for item in classic['source_files']}
        self.assertEqual(sources['InfiniLink/View Components/Home View/WelcomeView.swift']['sha256'],
                         'ba8331bece0dc31350903843fa5d3e5def503be7cce420d1491c408be3d4ba23')
        self.assertEqual(sources['InfiniLink/BLE/DeviceInfo.swift']['sha256'],
                         '1389abc6f05d71e32ca06e68aa22ac01ab3dbb552faee506d7212fc0c4c5af3b')

    def test_companion_fixture_records_battery_and_cts_startup_contract(self):
        fixture = json.loads(harness.COMPANION_FIXTURE.read_text())
        self.assertEqual(fixture['source_revision'], '60abe2d2c67aff67726855374385a499d9353a94')
        contract = fixture['companion_contract']['characteristics']
        self.assertEqual(contract['battery'], {
            'uuid': '00002a19-0000-1000-8000-00805f9b34fb',
            'properties': ['read', 'notify'], 'discovery_actions': ['read', 'subscribe']})
        self.assertEqual(contract['current_time'], {
            'uuid': '00002a2b-0000-1000-8000-00805f9b34fb',
            'properties': ['write'], 'discovery_actions': ['retain', 'write_current_time']})
        self.assertEqual(contract['music_control']['discovery_actions'], ['subscribe', 'retain'])

    def test_exception_text_never_logs_secrets(self):
        error = BlueZError('Failed', 'PIN 123456 failed')
        self.assertNotIn('123456', str(harness.error_fields(error)))

    def test_known_disconnect_is_classified_without_logging_exception_text(self):
        error = Exception('failed to discover services, device disconnected')
        self.assertEqual(harness.error_fields(error), {
            'error_type': 'Exception', 'error_code': 'disconnected_during_service_discovery'})

    def test_classification_requires_exact_fixed_message(self):
        for message in ('failed to discover services, device disconnected; PIN 123456',
                        '123456 Not connected', 'PIN 123456 failed'):
            with self.subTest(message=message):
                self.assertEqual(harness.error_fields(Exception(message)), {'error_type': 'Exception'})

    def test_fixed_att_failure_classification(self):
        message = '[org.bluez.Error.Failed] Operation failed with ATT error: 0x0e (Unlikely Error)'
        self.assertEqual(harness.error_fields(Exception(message)), {
            'error_type': 'Exception', 'error_code': 'att_unlikely_error'})
        self.assertEqual(harness.error_fields(Exception(message + ' PIN 123456')), {
            'error_type': 'Exception'})

    def test_timeout_and_dbus_name_keep_safe_diagnostics(self):
        self.assertEqual(harness.error_fields(TimeoutError('PIN 123456')), {
            'error_type': 'TimeoutError', 'error_code': 'deadline_exceeded'})
        self.assertEqual(harness.error_fields(BlueZError('AuthenticationFailed', 'PIN 123456')), {
            'error_type': 'BlueZError', 'dbus_error': 'org.bluez.Error.AuthenticationFailed'})

    def test_wrong_trace_versions_and_short_packets(self):
        for value in (b'', b'\x01', bytes([2, 0, 0, 0]), bytes([1, 25, 0, 0])):
            self.assertFalse(harness.trace_report(value)['complete'])

    def test_cli_feature_writes_require_full_mode(self):
        for args in (['--write-time'], ['--write-features'], ['--cycles', '0']):
            with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                harness.parse_args(args)
        args = harness.parse_args(['--mode', 'full', '--write-time'])
        self.assertTrue(args.write_time)


if __name__ == '__main__':
    unittest.main()
