import struct
import unittest
from pairing_probe import decode_trace


class TraceTests(unittest.TestCase):
    def test_initial_and_empty(self):
        self.assertEqual(decode_trace(bytes([1, 0, 0, 0])), {'phase': 'no_security_exchange', 'events': []})

    def test_failed_exchange(self):
        value = bytes([1, 3, 2, 0]) + struct.pack('<IBBH', 2000, 4, 0, 3)
        value += struct.pack('<IBBH', 3000, 8, 4, 0x204)
        value += struct.pack('<IBBH', 4000, 7, 4, 0x208)
        result = decode_trace(value)
        self.assertEqual(result['events'][1]['rounds'], 4)
        self.assertEqual(result['events'][1]['phase'], 'passkey_rounds')
        self.assertEqual(result['events'][2]['result'], 0x208)
        self.assertNotIn('pin', str(result))

    def test_reject_malformed(self):
        for value in (b'', bytes([2, 0, 0, 0]), bytes([1, 25, 0, 0]), bytes([1, 1, 0, 0]), bytes([1, 0, 0, 0, 1])):
            with self.assertRaises(ValueError):
                decode_trace(value)

    def test_security_metadata_without_secrets(self):
        value = bytes([1, 3, 4, 31])
        for event, result in ((13, 0x1005), (14, 0x1f1b), (15, 0)):
            value += struct.pack('<IBBH', 42, event, 0, result)
        events = decode_trace(value)['events']
        self.assertTrue(events[0]['encrypted'])
        self.assertFalse(events[0]['authenticated'])
        self.assertTrue(events[0]['bonded'])
        self.assertEqual(events[0]['key_size'], 16)
        self.assertFalse(events[1]['our_key']['authenticated'])
        self.assertTrue(events[1]['peer_key']['authenticated'])
        self.assertFalse(events[2]['our_key']['present'])

    def test_early_security_failure_statuses(self):
        value = bytes([1, 3, 0, 0])
        for event, status in ((16, 5), (17, 0x405), (18, 0x212)):
            value += struct.pack('<IBBH', 42, event, 0, status)
        events = decode_trace(value)['events']
        self.assertEqual(events, [
            {'watch_ms': 42, 'event': 'ltk_restore_lookup', 'result': 5},
            {'watch_ms': 42, 'event': 'pair_request_rejected', 'result': 0x405},
            {'watch_ms': 42, 'event': 'startup_failure', 'result': 0x212},
        ])


if __name__ == '__main__':
    unittest.main()
