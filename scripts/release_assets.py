#!/usr/bin/env python3
"""Validate tagged OTA firmware and stage only downloadable release assets."""
import argparse
import hashlib
import json
import pathlib
import re
import shutil

from ota_package import read_package

PIN = '6c119eb52206b580b556b41633dddc1e1b66a8da'


def version_from_tag(tag):
    number = r'(0|[1-9][0-9]*)'
    match = re.fullmatch(r'v' + number + r'\.' + number + r'\.' + number + r'(?:\+' + number + r')?', tag)
    if not match:
        raise ValueError('Release tag must be vMAJOR.MINOR.PATCH[+BUILD], without leading zeros')
    values = [int(n or 0) for n in match.groups()]
    if any(n > limit for n, limit in zip(values, [255, 255, 65535, 4294967295])):
        raise ValueError('Release version exceeds MCUboot version field limits')
    # MCUboot represents an omitted build number and +0 identically.
    if tag.endswith('+0'):
        raise ValueError('Omit +0 from release tags')
    return tag[1:]


def stage(tag, commit, repository, source, output):
    version = version_from_tag(tag)
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise ValueError('Expected full source commit SHA')
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository):
        raise ValueError('Expected GitHub owner/repository')
    package = source / f'gopine-update-{version}.zip'
    digest, _, image, _ = read_package(package, version=version)
    report = source / f'gopine-resources-{version}.json'
    budgets = json.loads(report.read_text())
    if budgets.get('errors') != []:
        raise ValueError('A passing memory resource report is required')
    if output.exists() and any(output.iterdir()):
        raise ValueError('Release output directory must be empty')
    output.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(package, output / package.name)
    shutil.copyfile(report, output / report.name)
    info = dict(version=version, tag=tag, commit=commit, repository=repository,
                tinygo='0.42.0', infinitime_commit=PIN, variant='ble', ota_protocol=2,
                image_bytes=len(image), package=package.name, sha256=digest)
    (output / 'build-info.json').write_text(json.dumps(info, indent=2) + '\n')
    docs = f'https://github.com/{repository}/blob/{commit}/docs/ota.md'
    owner, name = repository.split('/')
    browser_updater = f'https://{owner.lower()}.github.io/{name}/'
    changes_path = pathlib.Path(__file__).resolve().parents[1] / 'docs/releases' / f'{version}.md'
    changes = changes_path.read_text().strip() + '\n\n' if changes_path.exists() else ''
    notes = f'''# goPine {version}

{changes}Install with the [goPine Bluetooth updater]({browser_updater}), or download
**{package.name}** for an application-only PineTime OTA update.
The bootloader and stored InfiniTime recovery image are not replaced.

## Update from goPine 0.3.22 or later

The browser updater works with **BLE builds of goPine 0.3.22 or later** already
installed on the watch. Use a browser with Web Bluetooth, such as Chrome or Edge
on a supported computer, or Chrome on Android. Safari and Firefox do not support
this connection. Linux may need additional browser configuration.

1. Charge to at least **20%** or leave the watch on its charger. Tap **KEEP** if
   the current firmware is still a trial. Disconnect phone apps and keep the
   watch close to the computer or phone running the updater.
2. Open the [browser updater]({browser_updater}). Choose **Use latest release**
   if it shows **{version}**, or **Choose a ZIP file** with **{package.name}**.
   The browser validates the package before sending it.
3. On goPine, open **Settings → Firmware Update** and hold to connect. Leave the
   watch on **Ready to connect**, then click **Connect & update** in the browser
   and choose **goPine Update**.
4. Keep the tab open and the computer or phone awake until the watch verifies
   the complete image. If Bluetooth disconnects, keep the same page open so it
   can resume from the watch's acknowledged offset.
5. Tap **INSTALL** on the watch, then **KEEP** after goPine boots successfully.
   A reset before KEEP returns to the available fallback.

Download the ZIP and SHA256SUMS. On Linux, verify it with
`sha256sum --check --ignore-missing SHA256SUMS`.
For local browser controls, run:

```sh
bash scripts/ota-update.sh {version} --package /path/to/{package.name} --web
```

On first use, specify `--adapter hci1 --address YOUR_GOPINE_MAC`.
Native packages require OTA protocol 2. Older firmware needs a one-time
transition using its original in-app sender, or a wired installation.
Emergency InfiniTime recovery remains in the bootloader; this repository no
longer supplies a recovery DFU sender or Nordic DFU packages.

The watch must already have a compatible MCUboot/InfiniTime recovery setup.
Read the [setup, recovery behavior and update instructions]({docs}) before
installing. This is prototype firmware. Automated build/package/memory checks
do not establish hardware stability or battery life for a release build.
The initial clock fallback uses the source commit's UTC date/time; set or sync
local time after installation if necessary.

Source: `{commit}`. Built with TinyGo 0.42.0 and the pinned InfiniTime BLE port.
See build-info.json for provenance and {report.name} for memory-budget results.
'''
    # Keep the recovery reference last in both release notes and INSTALL.md.
    ota_docs = pathlib.Path(__file__).resolve().parents[1] / 'docs/ota.md'
    recovery = ota_docs.read_text().split('<!-- recovery-reference -->\n', 1)[1]
    notes += '\n' + recovery
    (output / 'INSTALL.md').write_text(notes)
    sums = [f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n'
            for p in sorted(output.iterdir())]
    (output / 'SHA256SUMS').write_text(''.join(sums))
    print(f'Staged {package.name}: {digest}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['version', 'stage'])
    parser.add_argument('tag')
    parser.add_argument('--commit')
    parser.add_argument('--repository')
    parser.add_argument('--source', type=pathlib.Path, default=pathlib.Path('build/ota'))
    parser.add_argument('--output', type=pathlib.Path, default=pathlib.Path('build/release'))
    args = parser.parse_args()
    if args.command == 'version':
        print(version_from_tag(args.tag))
    else:
        if not args.commit or not args.repository:
            parser.error('stage requires --commit and --repository')
        stage(args.tag, args.commit, args.repository, args.source, args.output)


if __name__ == '__main__':
    main()
