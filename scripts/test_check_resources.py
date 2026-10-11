#!/usr/bin/env python3
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("resources", Path(__file__).with_name("check-resources.py"))
resources = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resources)


class ResourceChecks(unittest.TestCase):
    def test_linked_dependencies_deduplicate_aliases_and_separate_ram(self):
        result = resources.linked_dependencies("\n".join([
            "00010000 00000020 T sprintf", "00010000 00000020 T vfprintf",
            "00010100 00000010 t time.Date", "20001000 00000040 b time.localLoc",
            "00010200 00000030 t runtime.interfaceEqual", "00010300 00000008 t strings.IndexByte",
            "00010400 000000ff t main.timeThing"]))
        self.assertEqual(result["libc_formatting"]["flash_symbol_bytes"], 32)
        self.assertEqual(result["time"], {"flash_symbol_bytes": 16, "ram_symbol_bytes": 64})
        self.assertEqual(result["reflection"]["flash_symbol_bytes"], 48)
        self.assertEqual(result["text_and_numbers"]["flash_symbol_bytes"], 8)

    def test_generic_c_formatter_regression_fails(self):
        for name in ("sprintf", "vfprintf", "gopine_upstream_uuid_to_str"):
            symbols, frames = self.fixture()
            symbols[name] = 0x1234
            self.assertTrue(any("Generic C formatting" in error for error in resources.audit(symbols, frames, 8192)["errors"]))

    def test_recycling_allocation_gate(self):
        receipt = "/repo/internal/notifications/notifications.go:32: object allocated on the heap: escapes"
        receiver = "/repo/internal/ota/receiver.go:10: object allocated on the heap: size 272"
        verifier = "/repo/internal/ota/image.go:40: object allocated on the heap: escapes"
        normal = "/repo/main.go:97: object allocated on the heap: workspace creation"
        self.assertEqual(resources.recycling_allocations("\n".join([receipt, receiver, verifier, normal])),
                         [receipt, receiver, verifier])
        self.assertEqual(resources.recycling_allocations(""), [])

    def test_target_object_sizes_follow_aliases_without_counting_children(self):
        dwarf = '''
0x00000001: DW_TAG_typedef
  DW_AT_name ("main.directUpdater")
  DW_AT_type (0x00000002 "structure ")
0x00000002: DW_TAG_structure_type
  DW_AT_byte_size (0x24)
0x00000003:   DW_TAG_member
  DW_AT_name ("other")
  DW_AT_type (0x00000004)
0x00000004: DW_TAG_base_type
  DW_AT_byte_size (4)
0x00000005: DW_TAG_typedef
  DW_AT_name ("cycle")
  DW_AT_type (0x00000005)
'''
        self.assertEqual(resources.object_sizes(dwarf, ["main.directUpdater", "cycle"]), {"main.directUpdater": 36})

    def test_crypto_controller_path_includes_nested_c_frames(self):
        symbols, frames = self.fixture()
        c_frames = {name: 32 for name in resources.BLE_CRYPTO_FRAMES}
        result = resources.audit(symbols, frames, 8192, c_frames)
        self.assertIn("pairing_crypto_controller", result["core_path_frames_bytes"])
        c_frames["EccPoint_mult"] = 5000
        errors = resources.audit(symbols, frames, 8192, c_frames)["errors"]
        self.assertTrue(any("pairing_crypto_controller" in error for error in errors))
        del c_frames["EccPoint_mult"]
        with self.assertRaises(ValueError):
            resources.audit(symbols, frames, 8192, c_frames)

    def fixture(self):
        names = [resources.ROOT, resources.RUN, resources.LOOP, resources.TICK,
                 resources.FLUSH, resources.SAVE, "main.buildClockTime",
                 "main.openApplication$3", "main.openApplication"]
        symbols = {name: 0x8000 + 32 * i for i, name in enumerate(names)}
        frames = dict(zip(symbols.values(), [48, 104, 2256, 1088, 1032, 1328, 752, 664, 1976]))
        symbols[resources.PORT_ISR] = 0xa000
        frames[0xa000] = 40
        symbols[resources.INPUT_VECTORS] = 0x20001000
        symbols.update(_heap_start=0x20005AC0, _heap_end=0x20010000)
        return symbols, frames

    def test_budgets(self):
        symbols, frames = self.fixture()
        result = resources.audit(symbols, frames, 8192)
        self.assertEqual(result["errors"], [])
        self.assertEqual(result["core_path_frames_bytes"]["settings_save"], 5432)
        self.assertEqual(result["heap_region_bytes"], 42304)

    def test_old_nested_frame_fails(self):
        symbols, frames = self.fixture()
        frames[symbols[resources.RUN]] = 4272
        result = resources.audit(symbols, frames, 8192)
        self.assertTrue(any("main.run" in error for error in result["errors"]))
        self.assertTrue(any("settings_save" in error for error in result["errors"]))

    def test_input_vector_and_interrupt_stack(self):
        symbols, frames = self.fixture()
        symbols[resources.INPUT_VECTORS] += 4
        frames[symbols[resources.PORT_ISR]] = 512
        errors = resources.audit(symbols, frames, 8192)["errors"]
        self.assertTrue(any("aligned" in error for error in errors))
        self.assertTrue(any("interrupt frame" in error for error in errors))

    def test_missing_port_handler_fails_closed(self):
        symbols, frames = self.fixture()
        del symbols[resources.PORT_ISR]
        with self.assertRaises(ValueError):
            resources.audit(symbols, frames, 8192)

    def test_stack_increase_is_not_a_silent_fix(self):
        symbols, frames = self.fixture()
        self.assertTrue(resources.audit(symbols, frames, 16384)["errors"])

    def test_heap_shrink_fails(self):
        symbols, frames = self.fixture()
        symbols["_heap_start"] += 4096
        self.assertTrue(resources.audit(symbols, frames, 8192)["errors"])

    def test_phone_stack_growth_fails(self):
        symbols, frames = self.fixture()
        symbols["(*main.timeSyncController).updatePhone"] = 0x9000
        frames[0x9000] = 4096
        errors = resources.audit(symbols, frames, 8192)["errors"]
        self.assertTrue(any("phone_update" in error for error in errors))

    def test_bluetooth_and_apple_notification_stack_growth_fails(self):
        for name, path in (("(main.pineTimeRadio).Service", "bluetooth_host"),
                           ("(*main.watchUI).receiveAppleNotifications", "apple_notification_update")):
            symbols, frames = self.fixture()
            symbols[name] = 0x9000
            frames[0x9000] = 4096
            errors = resources.audit(symbols, frames, 8192)["errors"]
            self.assertTrue(any(path in error for error in errors))

    def test_weather_stack_growth_fails(self):
        symbols, frames = self.fixture()
        symbols["(*main.timeSyncController).updateWeather"] = 0x9000
        frames[0x9000] = 4096
        errors = resources.audit(symbols, frames, 8192)["errors"]
        self.assertTrue(any("weather_update" in error for error in errors))

    def test_notification_stack_growth_fails(self):
        symbols, frames = self.fixture()
        symbols["(*main.watchUI).receiveNotifications"] = 0x9000
        frames[0x9000] = 4096
        errors = resources.audit(symbols, frames, 8192)["errors"]
        self.assertTrue(any("notification_update" in error for error in errors))

    def test_direct_update_stack_growth_fails(self):
        symbols, frames = self.fixture()
        symbols["(*main.directUpdater).tick"] = 0x9000
        frames[0x9000] = 4096
        self.assertTrue(any("direct_update" in error for error in resources.audit(symbols,frames,8192)["errors"]))

    def test_large_dependencies_cannot_silently_return(self):
        for name in ("fmt.Sprintf", "(*fmt.pp).printValue", "crypto/internal/fips140.CAST",
                     "hash/crc32.ChecksumIEEE", "(time.Time).Format"):
            symbols, frames = self.fixture()
            symbols[name] = 0x9000
            self.assertTrue(resources.audit(symbols, frames, 8192)["errors"], name)

    def test_missing_debug_frame_fails_closed(self):
        symbols, frames = self.fixture()
        del frames[symbols[resources.LOOP]]
        with self.assertRaises(ValueError):
            resources.audit(symbols, frames, 8192)

    def test_dwarf_and_symbols(self):
        frames = resources.parse_frames("""
0000 CIE
  DW_CFA_def_cfa: SP +0
0010 FDE cie=0000 pc=00008020...00008040
  DW_CFA_def_cfa_offset: +36
  DW_CFA_def_cfa_offset: +104
  DW_CFA_def_cfa_offset: +0
0030 FDE cie=0000 pc=00008040...00008080
  DW_CFA_def_cfa_expression: unknown
0050 FDE cie=0000 pc=00008080...00008084
""")
        self.assertEqual(frames, {0x8020: 104, 0x8040: None, 0x8080: 0})
        self.assertEqual(resources.parse_symbols("00008020 t main.run\n20010000 A _heap_end\n"),
                         {"main.run": 0x8020, "_heap_end": 0x20010000})

    def test_stack_table_is_little_endian_and_single_task(self):
        self.assertEqual(resources.stack_table(" 56850 00200000                             . ..\n"), 8192)
        for dump in ("", " 56850 00200000 00200000    . .. . ..\n"):
            with self.assertRaises(ValueError):
                resources.stack_table(dump)


if __name__ == "__main__":
    unittest.main()
