#!/usr/bin/env python3
"""Run the pinned nRF52 PHY with real MDK register layouts in host memory."""
import argparse
import os
from pathlib import Path
import subprocess
import re
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--infinitime', type=Path, required=True)
    parser.add_argument('--tinygo-root', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    revision = subprocess.check_output(
        ['git', '-C', str(args.infinitime), 'rev-parse', 'HEAD'], text=True).strip()
    if revision != '6c119eb52206b580b556b41633dddc1e1b66a8da':
        raise SystemExit('Unexpected InfiniTime revision; review PHY harness')
    nimble = args.infinitime.resolve() / 'src/libs/mynewt-nimble'
    port = root / 'internal/ble_nimble/port'
    tests = root / 'internal/ble_nimble/tests'
    includes = [tests / 'phy', port / 'include', args.tinygo_root / 'lib/nrfx/mdk']
    includes += [nimble / path for path in (
        'porting/nimble/include', 'nimble/include', 'nimble/host/include',
        'nimble/controller/include', 'nimble/drivers/nrf52/include',
        'nimble/drivers/nrf52/src')]
    with tempfile.TemporaryDirectory(prefix='gopine-phy-test-') as output:
        executable = Path(output) / 'phy'
        # The driver's packet-copy routine contains two ARM-only asm blocks.
        # They are outside the exercised power paths; trap if ever reached.
        source = (nimble / 'nimble/drivers/nrf52/src/ble_phy.c').read_text()
        source, count = re.subn(r'__asm__ volatile \(.*?\n\s*\);',
                                'assert(!"ARM packet copy reached in host test");',
                                source, flags=re.S)
        if count != 2:
            raise SystemExit('Pinned PHY asm changed; review host harness')
        (Path(output) / 'ble_phy.c').write_text(source)
        command = ['clang', '-g', '-O1', '-fsanitize=address,undefined',
                   '-ffunction-sections', '-fdata-sections',
                   '-Wl,--gc-sections', '-Wno-pointer-to-int-cast',
                   '-include', str(port / 'config.h')]
        command += [f'-I{output}'] + [f'-I{path}' for path in includes]
        command += [str(tests / 'phy_power_test.c'), '-o', str(executable)]
        subprocess.run(command, check=True)
        subprocess.run([str(executable)], check=True,
                       env={**os.environ, 'ASAN_OPTIONS': 'detect_leaks=0'})


if __name__ == '__main__':
    main()
