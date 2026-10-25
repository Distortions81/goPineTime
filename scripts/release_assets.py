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
    docs = f'https://github.com/{repository}/blob/{commit}/README.md'
    owner, name = repository.split('/')
    browser_updater = f'https://{owner.lower()}.github.io/{name}/'
    notes = f'''# goPine {version}

Update with the [browser updater]({browser_updater}) or download
`{package.name}`. Requires goPine 0.3.22+ with Bluetooth support.
Open **Settings → Firmware Update**, transfer, then tap **INSTALL** and **KEEP**.

[Build and first install]({docs}#build-and-first-install) ·
[Update instructions]({docs}#bluetooth-updates) ·
[Recovery]({docs}#recovery)

Checksums: `SHA256SUMS`. Build details: `build-info.json`.
'''
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
