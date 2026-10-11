#!/usr/bin/env python3
"""Exercise the pinned NimBLE host/service against a simulated HCI controller.

This covers host connection/discovery/teardown, not nRF radio IRQs or Go stacks.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
from ble_crypto_sources import prepare_crypto


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--infinitime', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    vendor = args.infinitime.resolve()
    revision = subprocess.check_output(['git', '-C', str(vendor), 'rev-parse', 'HEAD'], text=True).strip()
    if revision != '6c119eb52206b580b556b41633dddc1e1b66a8da':
        raise SystemExit('Unexpected InfiniTime revision; use the same pin as build-ble.py')
    nimble = vendor / 'src/libs/mynewt-nimble'
    port = root / 'internal/ble_nimble/port'
    tests = root / 'internal/ble_nimble/tests'
    section = (vendor / 'src/CMakeLists.txt').read_text().split('set(NIMBLE_SRC', 1)[1].split(')', 1)[0]
    sources = list(dict.fromkeys(vendor / 'src' / s for s in re.findall(r'libs/[^\s]+\.c', section)))
    sources = [s for s in sources if '/host/' in str(s) or '/transport/ram/' in str(s)
               or s.name in ('os_mbuf.c', 'os_mempool.c', 'mem.c', 'endian.c', 'os_msys_init.c')]
    sources = [s for s in sources if s.name not in ('ble_sm.c', 'ble_gatts.c', 'ble_hs_stop.c', 'ble_store_ram.c', 'ble_uuid.c')]
    sources += [port / name for name in ('npl.c', 'alloc.c', 'stop.c', 'sm.c', 'gatts.c', 'bond.c', 'ancs.c', 'privacy.c', 'uuid.c')]
    sources += [tests / 'host_test.c']
    includes = [tests / 'host', tests, port / 'include']
    includes += [nimble / path for path in (
        'porting/nimble/include', 'nimble/include', 'nimble/host/include',
        'nimble/host/src', 'nimble/controller/include',
        'nimble/host/services/gap/include', 'nimble/host/services/gatt/include',
        'nimble/host/util/include', 'nimble/transport/ram/include',
        'nimble/host/store/ram/include', 'nimble/host/store/ram/src', 'ext/tinycrypt/include')]
    with tempfile.TemporaryDirectory(prefix='gopine-host-test-') as output:
        executable = Path(output) / 'host'
        # The same companion payloads are decoded by Go's UI integration test.
        fixtures = json.loads((root / 'testdata/infinilink_wire.json').read_text())
        header = []
        for name, encoded in fixtures['packets'].items():
            if not re.fullmatch('[a-z][a-z0-9_]*', name):
                raise SystemExit('Invalid companion fixture name')
            payload = bytes.fromhex(encoded)
            header.append(f'static const uint8_t companion_{name}[]={{' +
                          ','.join(str(byte) for byte in payload) + '};')
        (Path(output) / 'companion_fixtures.h').write_text('\n'.join(header) + '\n')
        sources += prepare_crypto(nimble, Path(output))
        command = ['clang', '-g', '-O1', '-fsanitize=address,undefined',
                   '-Wno-pointer-to-int-cast', '-DBLE_NPL_OS_ALIGNMENT=8',
                   '-DGOPINE_BLE_ALLOC_AUDIT',
                   '-DGOPINE_ECC_YIELD=gopine_test_ecc_yield',
                   '-include', str(port / 'config.h')]
        command += [f'-I{p}' for p in includes]
        command += [f'-I{output}']
        command += [str(s) for s in sources] + ['-o', str(executable)]
        subprocess.run(command, check=True)
        subprocess.run([str(executable)], check=True, timeout=30,
                       env={**os.environ, 'ASAN_OPTIONS': 'detect_leaks=0',
                            'UBSAN_OPTIONS': 'halt_on_error=1'})
        print('host integration: passed ASan/UBSan')


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError as error:
        raise SystemExit(f'Host integration failed (exit {error.returncode})')
