#!/usr/bin/env python3
"""Regression budgets for the PineTime BLE ELF, not a whole-program stack proof.

Read actual DWARF frame sizes and the linked task-stack table. Check selected
core paths with 2 KiB reserved for deeper calls, interrupts and runtime work.
Indirect calls/recursion prevent TinyGo from proving a complete upper bound;
hardware testing is still required. No firmware is modified or uploaded.
"""
import argparse
import json
from pathlib import Path
import re
import struct
import subprocess

ROOT = "runtime.run$1$gowrapper"
RUN = "main.run"
LOOP = "(*main.watchLoop).run"
TICK = "(*main.watchUI).tickTimers"
FLUSH = "(*main.settingsPersistence).flush"
SAVE = "(*github.com/Distortions81/goPine/internal/checkpoint.Journal).save"
RESERVE = 2048
PORT_ISR = "gopine_port_interrupt"
INPUT_VECTORS = "main.inputVectors"
BLE_CRYPTO_FRAMES = (
    "gopine_ble_poll", "pump", "ble_hs_process_rx_data_queue", "ble_hs_rx_data",
    "ble_hs_hci_evt_acl_process", "ble_l2cap_rx", "ble_sm_rx",
    "ble_sm_process_result", "ble_sm_sc_public_key_rx", "ble_sm_sc_public_key_exec",
    "ble_sm_sc_ensure_keys_generated", "ble_sm_alg_gen_key_pair", "uECC_make_key",
    "EccPoint_compute_public_key", "ble_sm_alg_gen_dhkey", "uECC_shared_secret",
    "EccPoint_mult", "ble_ll_conn_event_end", "ble_ll_conn_next_event",
    "ble_ll_sched_conn_reschedule", "ble_ll_conn_end",
)

MEMORY_TYPES = (
    "main.watchUI", "main.notificationState", "main.phoneState", "main.weatherState",
    "main.directUpdater", "main.updateWorkspace", "main.settingsPersistence",
    "main.timerResume", "main.frameRenderer", "main.pineTimeDisplay",
    "github.com/Distortions81/goPine/internal/ota.Receiver",
    "github.com/Distortions81/goPine/internal/notifications.Inbox",
    "github.com/Distortions81/goPine/internal/notifications.Message",
)


def object_sizes(text, names=MEMORY_TYPES):
    """Read target object sizes, not host Go sizes or measured live heap."""
    entries = {}
    named = {}
    for match in re.finditer(r"^0x([0-9a-f]+):[^\n]*DW_TAG_([^\n]+)\n(.*?)(?=^0x[0-9a-f]+:|\Z)", text, re.M | re.S):
        address, kind, body = match.groups()
        size = re.search(r"DW_AT_byte_size\s+\((0x[0-9a-f]+|\d+)\)", body)
        target = re.search(r"DW_AT_type\s+\((0x[0-9a-f]+)", body)
        entries[int(address, 16)] = (int(size[1], 0) if size else None,
                                     int(target[1], 16) if target and kind.strip() == "typedef" else None)
        name = re.search(r'DW_AT_name\s+\("([^"\n]+)"\)', body)
        if name and name[1] in names:
            named[name[1]] = int(address, 16)
    result = {}
    for name, address in named.items():
        seen = set()
        while address in entries and address not in seen:
            seen.add(address)
            size, address = entries[address]
            if size is not None:
                result[name] = size
                break
    return result


def recycling_allocations(text):
    # The compiler filter selects Receiver, ValidateWithScratch and Inbox
    # methods. Their owned storage is created by callers, outside hot paths.
    return [line for line in text.splitlines() if "object allocated on the heap" in line
            and re.search(r"internal/(?:ota/(?:receiver|image)|notifications/notifications)\.go:", line)]


def parse_c_frames(text):
    frames = {}
    for line in text.splitlines():
        match = re.fullmatch(r".*:\d+:([^\t]+)\t(\d+)\tstatic", line)
        if match:
            name, size = match.groups()
            frames[name] = max(frames.get(name, 0), int(size))
    return frames


def parse_symbols(text):
    symbols = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([0-9a-fA-F]+) ([A-Za-z]) (.+)", line.strip())
        if match:
            symbols[match[3]] = int(match[1], 16)
    return symbols


def largest_ram_symbols(text, limit=20):
    """Rank linked static objects; this excludes allocations inside the Go heap."""
    objects = []
    for line in text.splitlines():
        match = re.fullmatch(r"([0-9a-fA-F]+) ([0-9a-fA-F]+) ([bBdDsS]) (.+)", line.strip())
        if match:
            address, size = int(match[1], 16), int(match[2], 16)
            if 0x20000000 <= address < 0x20010000 and size:
                objects.append({"name": match[4], "bytes": size})
    return sorted(objects, key=lambda value: (-value["bytes"], value["name"]))[:limit]


def linked_dependencies(text):
    """Selected symbol groups, excluding aliases and compiler-inlined code."""
    groups = {name: {"flash_symbol_bytes": 0, "ram_symbol_bytes": 0} for name in
              ("time", "reflection", "text_and_numbers", "libc_formatting")}
    seen = set()
    for line in text.splitlines():
        match = re.fullmatch(r"([0-9a-fA-F]+) ([0-9a-fA-F]+) ([a-zA-Z]) (.+)", line.strip())
        if not match:
            continue
        address, size, name = int(match[1], 16), int(match[2], 16), match[4]
        group = None
        if name.startswith(("time.", "(time.", "(*time.")):
            group = "time"
        elif name.startswith(("reflect/", "reflect.", "internal/reflectlite.", "(internal/reflectlite.", "(*internal/reflectlite.", "runtime.reflect", "runtime.interfaceEqual")):
            group = "reflection"
        elif name.startswith(("strings.", "strconv.", "internal/strconv.", "unicode", "bytes.")):
            group = "text_and_numbers"
        elif name in ("sprintf", "snprintf", "vsprintf", "vsnprintf", "vfprintf", "__i_vfprintf"):
            group = "libc_formatting"
        if group is None or not size or (address, size) in seen:
            continue
        seen.add((address, size))
        key = "ram_symbol_bytes" if 0x20000000 <= address < 0x20010000 else "flash_symbol_bytes"
        groups[group][key] += size
    return groups


def parse_frames(text):
    frames, address = {}, None
    for line in text.splitlines():
        match = re.search(r"\bFDE .*\bpc=([0-9a-fA-F]+)\.\.\.([0-9a-fA-F]+)", line)
        if match:
            address = int(match[1], 16)
            frames[address] = 0
        elif re.search(r"\bCIE\s*$", line):
            address = None
        elif address is not None:
            match = re.search(r"DW_CFA_def_cfa_offset: \+?(\d+)", line)
            if match and frames[address] is not None:
                frames[address] = max(frames[address], int(match[1]))
            # Fail closed for frames our simple SP-relative parser cannot bound.
            if re.search(r"DW_CFA_(def_cfa_expression|def_cfa_register|def_cfa_sf|def_cfa_offset_sf)", line):
                frames[address] = None
    return frames


def stack_table(text):
    data = bytearray()
    for line in text.splitlines():
        match = re.match(r"\s*[0-9a-fA-F]+\s+((?:[0-9a-fA-F]{8}\s+)+)", line)
        if match:
            data.extend(bytes.fromhex(match[1]))
    if len(data) != 4:
        raise ValueError("Expected one task-stack entry; new tasks require a budget review")
    return struct.unpack("<I", data)[0]


def audit(symbols, by_address, task_stack, c_frames=None):
    def frame(name, required=True):
        if name not in symbols:
            if required:
                raise ValueError(f"Required function missing: {name}")
            return 0  # Inlined optional helpers are covered by their caller frame.
        size = by_address.get(symbols[name])
        if size is None:
            raise ValueError(f"Missing or unsupported DWARF frame: {name}")
        return size

    sizes = {name: frame(name) for name in (ROOT, RUN, LOOP, TICK, FLUSH, SAVE)}
    callbacks = [name for name in symbols if re.fullmatch(r"main\.(?:openApplication|run)\$\d+", name)]
    if not callbacks:
        raise ValueError("Application callbacks missing; review changed call paths")
    # Deliberately retain a callback frame even when the compiler tail-calls it.
    callback = max(frame(name) for name in callbacks)
    base = sizes[ROOT] + sizes[RUN]
    live = base + sizes[LOOP]
    snapshot = frame("(*main.watchUI).runtimeSnapshot", False)
    calendar = frame("main.calendarMillis", False)
    paths = {
        "initialization": base + frame("main.openApplication", False) + frame("main.buildClockTime"),
        "timer": live + sizes[TICK] + frame("(*main.watchUI).loadRuntime", False) + calendar,
        "settings_save": live + callback + frame("(*main.timeSyncController).saveSettings", False) + frame("(*main.settingsPersistence).update", False) + sizes[FLUSH] + max(sizes[SAVE], snapshot + calendar),
        "input": live + frame("(*main.watchUI).handle", False) + callback,
        "frame_change": live + frame("main.changedStrips", False),
        "phone_update": live + frame("(*main.timeSyncController).update", False) + frame("(*main.timeSyncController).updatePhone", False) + max(frame("(main.pineTimeRadio).TakeMusic", False), frame("(main.pineTimeRadio).TakeWeather", False)),
        "bluetooth_host": live + frame("(*main.timeSyncController).update", False) + frame("(main.pineTimeRadio).Service", False),
        "apple_notification_update": live + frame("(*main.watchUI).receiveAppleNotifications", False) + max(frame("(main.pineTimeRadio).TakeAppleNotification", False), frame("(*github.com/Distortions81/goPine/internal/notifications.Inbox).ApplyApple", False)),
        "notification_update": live + frame("(*main.timeSyncController).update", False) + frame("(*main.timeSyncController).updatePhone", False) + frame("(*main.watchUI).receiveNotifications", False) + max(frame("(main.pineTimeRadio).TakeNotification", False), frame("(*github.com/Distortions81/goPine/internal/notifications.Inbox).Add", False)),
        "weather_update": live + frame("(*main.timeSyncController).update", False) + frame("(*main.timeSyncController).updateWeather", False) + frame("(main.pineTimeRadio).TakeWeather", False),
        "pairing_update": live + frame("(*main.timeSyncController).updatePairing", False) + frame("(main.pineTimeRadio).ForgetPhone", False),
        "render": live + frame("(*main.frameRenderer).renderStrips", False) + frame("(*main.watchLoop).run$1", False),
    }
    phone = live + frame("(*main.timeSyncController).update", False) + frame("(*main.timeSyncController).updatePhone", False)
    paths["phone_preflight_save"] = live + callback + sizes[FLUSH] + max(sizes[SAVE], snapshot + calendar)
    paths["phone_time"] = phone + frame("(*main.timeSyncController).receivePhoneTime", False) + max(
        frame("(main.pineTimeRadio).Take", False), frame("github.com/Distortions81/goPine/internal/timesync.Decode", False),
        frame("github.com/Distortions81/goPine/internal/timesync.Normalize", False))
    verify = frame("(*github.com/Distortions81/goPine/internal/ota.Receiver).Verify", False) + frame("github.com/Distortions81/goPine/internal/ota.ValidateWithScratch", False)
    paths["direct_update"] = live + frame("(*main.directUpdater).tick", False) + max(
        frame("(*github.com/Distortions81/goPine/internal/ota.Receiver).Write", False),
        frame("(*github.com/Distortions81/goPine/internal/ota.Receiver).Begin", False) + frame("github.com/Distortions81/goPine/internal/ota.checkErasedTrailer", False), verify)
    paths["direct_install"] = live + callback + frame("(*main.directUpdater).install", False) + frame("(*github.com/Distortions81/goPine/internal/ota.Receiver).Commit", False) + verify
    crypto_frames = {}
    if c_frames is not None:
        for name in BLE_CRYPTO_FRAMES:
            if name not in c_frames:
                raise ValueError(f"Required BLE C frame missing: {name}")
            crypto_frames[name] = c_frames[name]
        # Conservative selected path: sum both key-generation and ECDH
        # alternatives, plus another pump for the controller service point.
        # Retain the general reserve for unlisted callees and interrupts.
        paths["pairing_crypto_controller"] = paths["bluetooth_host"] + sum(crypto_frames.values()) + c_frames["pump"]
    heap = symbols["_heap_end"] - symbols["_heap_start"]
    errors = []
    port_frame = frame(PORT_ISR)
    if port_frame > 256:
        errors.append(f"GPIO PORT interrupt frame exceeds 256 bytes: {port_frame}")
    if symbols[INPUT_VECTORS] & 255:
        errors.append("Input vector table must be aligned to 256 bytes")
    if task_stack != 8192:
        errors.append("Task stack changed from 8192 bytes; review heap and stack budgets together")
    if heap < 40 * 1024:
        errors.append(f"Heap region shrank below 40 KiB: {heap} bytes")
    if sizes[RUN] > 256:
        errors.append(f"Long-lived main.run frame exceeds 256 bytes: {sizes[RUN]}")
    # These dependencies previously retained tens of KiB of unrelated code.
    # Keep the embedded formatting and image-checksum paths deliberately small.
    if any(name.startswith(("fmt.", "(*fmt.")) for name in symbols):
        errors.append("Generic fmt linked into firmware; use the integer UI/error helpers")
    if any("crypto/internal/fips140" in name for name in symbols):
        errors.append("FIPS module linked into firmware; use the image SHA-256 subset")
    if any(name.startswith("hash/crc32.") for name in symbols):
        errors.append("Standard CRC linked into firmware; its slicing table allocates 8 KiB of heap")
    if "(time.Time).Format" in symbols:
        errors.append("General time formatting linked into firmware; use the fixed UI formats")
    if any(name in symbols for name in ("sprintf", "snprintf", "vsprintf", "vsnprintf", "vfprintf", "__i_vfprintf", "gopine_upstream_uuid_to_str")):
        errors.append("Generic C formatting linked into firmware; use the bounded UUID formatter")
    for name, size in paths.items():
        if size + RESERVE > task_stack:
            errors.append(f"{name}: core frames {size} + reserve {RESERVE} > stack {task_stack}")
    return {
        "task_stack_bytes": task_stack,
        "gpio_port_isr_frame_bytes": port_frame,
        "ram_reserved_bytes": symbols["_heap_start"] - 0x20000000,
        "heap_region_bytes": heap,
        "display_strip_bytes": 2880,
        "deeper_call_reserve_bytes": RESERVE,
        "frames_bytes": sizes,
        "callback_frame_budget_bytes": callback,
        "core_path_frames_bytes": paths,
        "pairing_crypto_c_frames_bytes": crypto_frames,
        "errors": errors,
        "limitation": "Selected-path regression budgets, not full call-graph bounds or measured free heap.",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("elf", type=Path)
    parser.add_argument("--report", type=Path)
    parser.add_argument("--ble-stack-usage", type=Path)
    parser.add_argument("--allocations", type=Path)
    args = parser.parse_args()

    def output(tool, *options):
        return subprocess.check_output([tool, *options, str(args.elf)], text=True)

    symbols = parse_symbols(output("llvm-nm", "--numeric-sort"))
    frames = parse_frames(output("llvm-dwarfdump", "--debug-frame"))
    stack = stack_table(output("llvm-objdump", "-s", "-j", ".tinygo_stacksizes"))
    c_frames = parse_c_frames(args.ble_stack_usage.read_text()) if args.ble_stack_usage else None
    result = audit(symbols, frames, stack, c_frames)
    result["elf"] = str(args.elf)
    sized_symbols = output("llvm-nm", "--print-size")
    result["largest_static_ram_symbols"] = largest_ram_symbols(sized_symbols)
    result["linked_dependency_groups"] = linked_dependencies(sized_symbols)
    result["dependency_size_limitation"] = "Selected named symbols only; inlined code is charged to callers, aliases are deduplicated. Not total dependency cost or heap usage."
    result["target_object_sizes_bytes"] = object_sizes(output("llvm-dwarfdump", "--debug-info"))
    result["object_size_limitation"] = "Raw ARM object sizes; excludes allocator rounding, GC metadata, object counts and temporary allocations. Not measured free heap."
    if args.allocations:
        result["recycling_allocations"] = recycling_allocations(args.allocations.read_text())
        result["errors"].extend("Recycling path allocates: " + line for line in result["recycling_allocations"])
    if args.report:
        args.report.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result, indent=2))
    return bool(result["errors"])


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"Resource check failed: {error}")
