#!/usr/bin/env python3
"""Host-test the cooperative port primitives, not the nRF52 radio hardware."""
import argparse
import os
import pathlib
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--infinitime", type=pathlib.Path, required=True)
    args = parser.parse_args()
    root = pathlib.Path(__file__).resolve().parent.parent
    port = root / "internal/ble_nimble/port"
    tests = root / "internal/ble_nimble/tests"
    includes = [tests, port / "include",
                args.infinitime.resolve() / "src/libs/mynewt-nimble/nimble/include"]
    with tempfile.TemporaryDirectory(prefix="gopine-ble-test-") as output:
        for name in ("alloc", "npl", "weather", "music", "notification", "ancs", "update", "device_info", "phone_time", "sm", "bond", "bond_store"):
            executable = pathlib.Path(output) / name
            command = ["clang", "-g", "-fsanitize=address,undefined", "-Wno-pointer-to-int-cast"]
            command += [f"-I{p}" for p in includes]
            if name in ("bond_store", "update", "device_info", "phone_time", "sm", "ancs"):
                nimble = args.infinitime.resolve() / "src/libs/mynewt-nimble"
                command += ["-include", str(port / "config.h")]
                command += [f"-I{nimble / path}" for path in (
                    "porting/nimble/include", "nimble/host/include", "nimble/host/store/ram/include", "nimble/host/store/ram/src")]
                if name == "sm":
                    command += [f"-I{nimble / 'nimble/host/src'}",
                                "-O1", "-ffunction-sections", "-fdata-sections", "-Wl,--gc-sections"]
            if name in ("alloc", "npl"):
                command += [str(port / f"{name}.c")]
            command += [str(tests / f"{name}_test.c"), "-o", str(executable)]
            subprocess.run(command, check=True)
            # LeakSanitizer cannot operate under the local ptrace sandbox.
            # These two primitives allocate only static storage, not host heap.
            subprocess.run([str(executable)], check=True,
                           env={**os.environ, "ASAN_OPTIONS": "detect_leaks=0"})
            print(f"{name}: passed ASan/UBSan")


if __name__ == "__main__":
    main()
