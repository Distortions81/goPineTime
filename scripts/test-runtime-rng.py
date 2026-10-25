#!/usr/bin/env python3
"""Exercise the PineTime RNG cleanup with TinyGo's real driver on a host model.

Only the nRF register access is modeled. The runtime getter and go:linkname
bridge are compiled unchanged, so a stale driver flag makes a later read fail.
This checks lifecycle behavior, not electrical current or interrupt timing.
"""
import argparse
from pathlib import Path
import re
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tinygo-root', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parent.parent
    machine = args.tinygo_root / 'src/machine'
    source = (machine / 'machine_nrf.go').read_text()
    getter = re.search(r'^func getRNG\(\).*?^}\n', source, re.M | re.S)
    if not getter or 'var rngStarted = false' not in source:
        raise SystemExit('TinyGo RNG driver changed; review the runtime bridge')
    fixtures = root / 'internal/powercheck/testdata'
    with tempfile.TemporaryDirectory(prefix='gopine-rng-test-') as output:
        output = Path(output)
        (output / 'machine').mkdir()
        (output / 'nrf').mkdir()
        (output / 'go.mod').write_text('''module rngcheck

go 1.23.0

require (
    machine v0.0.0
    device/nrf v0.0.0
)
replace machine => ./machine
replace device/nrf => ./nrf
''')
        (output / 'machine/go.mod').write_text('module machine\n\ngo 1.23.0\n')
        (output / 'nrf/go.mod').write_text('module device/nrf\n\ngo 1.23.0\n')
        (output / 'machine/driver.go').write_text(
            'package machine\nimport "device/nrf"\nvar rngStarted = false\n' + getter[0])
        # Retain TinyGo's public wrapper as well as the private getter/flag.
        wrapper = (machine / 'machine_nrf_bare.go').read_text()
        (output / 'machine/rng.go').write_text(wrapper[wrapper.index('package machine'):])
        bridge = (root / 'rng_pinetime.go').read_text()
        (output / 'rng.go').write_text(bridge[bridge.index('package main'):])
        (output / 'nrf/registers.go').write_text((fixtures / 'rng_registers.go').read_text())
        (output / 'rng_test.go').write_text((fixtures / 'rng_test.go').read_text())
        subprocess.run(['go', 'test', '-count=1', '-v', '.'], cwd=output, check=True)


if __name__ == '__main__':
    main()
