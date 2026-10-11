#!/usr/bin/env python3
"""Stage the browser updater and a checked, same-origin published firmware ZIP."""
import argparse
import datetime
import json
import os
import pathlib
import re
import shutil
import tempfile
import urllib.parse
import urllib.request

from ota_package import read_package
from release_assets import version_from_tag

ROOT = pathlib.Path(__file__).resolve().parents[1]
MIN_VERSION = (0, 3, 22, 0)
MAX_PACKAGE = 1024 * 1024
MAX_JSON = 2 * 1024 * 1024
MAX_INFO = 16 * 1024
STATIC_SUFFIXES = {'.html', '.css', '.js', '.mjs', '.svg', '.png', '.ico', '.webp'}


def repository_name(value):
    if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}', value):
        raise ValueError('Expected GitHub owner/repository')
    if any(part in {'.', '..'} for part in value.split('/')):
        raise ValueError('Invalid GitHub repository path')
    return value


def version_key(tag):
    version = version_from_tag(tag)
    number, _, build = version.partition('+')
    return tuple(map(int, number.split('.'))) + (int(build or 0),)


def github_url(url):
    parsed = urllib.parse.urlsplit(url)
    if (parsed.scheme != 'https' or parsed.username or parsed.password or parsed.port
            or not (parsed.hostname == 'api.github.com'
                    or (parsed.hostname or '').endswith('.githubusercontent.com'))):
        raise ValueError('Download must stay on GitHub HTTPS hosts')
    return parsed


class GitHubRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        before, after = github_url(request.full_url), github_url(newurl)
        redirected = super().redirect_request(request, fp, code, msg, headers, newurl)
        if redirected and before.netloc != after.netloc:
            # Asset downloads redirect to GitHub's storage CDN. Never forward tokens.
            redirected.remove_header('Authorization')
        return redirected


class GitHub:
    def __init__(self, repository, token=None):
        self.repository = repository_name(repository)
        self.base = f'https://api.github.com/repos/{self.repository}'
        self.token = token
        self.opener = urllib.request.build_opener(GitHubRedirect())

    def fetch(self, path, limit=MAX_JSON, binary=False):
        if not path.startswith('/') or path.startswith('//'):
            raise ValueError('Expected a repository-relative GitHub API path')
        url = self.base + path
        github_url(url)
        headers = {'Accept': 'application/octet-stream' if binary else 'application/vnd.github+json',
                   'User-Agent': 'goPineTime-Pages-builder', 'X-GitHub-Api-Version': '2022-11-28'}
        if self.token:
            headers['Authorization'] = f'Bearer {self.token}'
        with self.opener.open(urllib.request.Request(url, headers=headers), timeout=30) as response:
            length = response.headers.get('Content-Length')
            if length is not None and (not length.isdigit() or int(length) > limit):
                raise ValueError('GitHub response exceeds download limit')
            data = response.read(limit + 1)
        if len(data) > limit:
            raise ValueError('GitHub response exceeds download limit')
        return data if binary else json.loads(data)

    def releases(self):
        result = []
        for page in range(1, 11):
            batch = self.fetch(f'/releases?per_page=100&page={page}')
            if not isinstance(batch, list):
                raise ValueError('Expected a GitHub release list')
            result.extend(batch)
            if len(batch) < 100:
                return result
        raise ValueError('Release list exceeds bounded lookup; archive old releases before publishing')

    def tag_commit(self, tag):
        value = self.fetch('/git/ref/tags/' + urllib.parse.quote(tag, safe='')).get('object', {})
        for _ in range(8):
            sha = value.get('sha')
            if not isinstance(sha, str) or not re.fullmatch(r'[0-9a-f]{40}', sha):
                raise ValueError('GitHub tag did not resolve to a source commit')
            if value.get('type') == 'commit':
                return sha
            if value.get('type') != 'tag':
                break
            value = self.fetch('/git/tags/' + sha).get('object', {})
        raise ValueError('GitHub tag did not resolve to a source commit')

    def asset(self, asset, maximum):
        identity, size = asset.get('id'), asset.get('size')
        if type(identity) is not int or identity <= 0 or type(size) is not int or not 0 < size <= maximum:
            raise ValueError('Invalid release asset ID or size')
        data = self.fetch(f'/releases/assets/{identity}', limit=maximum, binary=True)
        if len(data) != size:
            raise ValueError('Release asset size changed during download')
        return data


def select_release(releases):
    candidates = []
    for release in releases:
        if not isinstance(release, dict):
            raise ValueError('Invalid GitHub release metadata')
        if release.get('draft') is not False or release.get('prerelease') is not False:
            continue
        tag = release.get('tag_name')
        if not isinstance(tag, str) or len(tag) > 64:
            continue
        try:
            key = version_key(tag)
        except ValueError:
            continue
        if key >= MIN_VERSION:
            candidates.append((key, release))
    if not candidates:
        return None
    return max(candidates, key=lambda item: item[0])[1]


def checked_firmware(client, output):
    release = select_release(client.releases())
    if release is None:
        return None
    tag = release['tag_name']
    version = version_from_tag(tag)
    filename = f'gopine-update-{version}.zip'
    assets = release.get('assets')
    if not isinstance(assets, list) or len(assets) > 100:
        raise ValueError('Invalid release asset list')
    selected = {}
    for asset in assets:
        if not isinstance(asset, dict):
            raise ValueError('Invalid release asset metadata')
        name = asset.get('name')
        if name in (filename, 'build-info.json'):
            if name in selected:
                raise ValueError('Duplicate firmware release asset')
            selected[name] = asset
    if set(selected) != {filename, 'build-info.json'}:
        raise ValueError('Latest compatible release is missing checked firmware assets')
    info = json.loads(client.asset(selected['build-info.json'], MAX_INFO))
    if not isinstance(info, dict):
        raise ValueError('Invalid firmware build-info')
    expected = {'version': version, 'tag': tag, 'repository': client.repository,
                'package': filename, 'variant': 'ble'}
    if any(info.get(key) != value for key, value in expected.items()):
        raise ValueError('Firmware build-info does not match the published release')
    sha, commit = info.get('sha256'), info.get('commit')
    if not isinstance(sha, str) or not re.fullmatch(r'[0-9a-f]{64}', sha):
        raise ValueError('Invalid package SHA-256 in build-info')
    if (not isinstance(commit, str) or not re.fullmatch(r'[0-9a-f]{40}', commit)
            or commit != client.tag_commit(tag)):
        raise ValueError('Firmware source commit does not match the release tag')
    published = release.get('published_at')
    if not isinstance(published, str) or not re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z', published):
        raise ValueError('Invalid release publication date')
    datetime.datetime.fromisoformat(published.replace('Z', '+00:00'))
    data = client.asset(selected[filename], MAX_PACKAGE)
    package = output / filename
    package.write_bytes(data)
    digest, _, image, _ = read_package(package, expected=sha, version=version)
    if type(info.get('image_bytes')) is not int or info['image_bytes'] != len(image):
        raise ValueError('Firmware image length does not match build-info')
    return {'version': version, 'tag': tag, 'commit': commit,
            'package': f'firmware/{filename}', 'sha256': digest,
            'size': len(data), 'imageBytes': len(image),
            'releaseUrl': f'https://github.com/{client.repository}/releases/tag/{urllib.parse.quote(tag, safe="")}',
            'publishedAt': published}


def copy_static(source, output):
    if not (source / 'index.html').is_file():
        raise ValueError('Web source must contain index.html')
    for path in sorted(source.rglob('*')):
        relative = path.relative_to(source)
        if any(part.startswith('.') or part in {'test', 'tests', 'node_modules', 'firmware'} for part in relative.parts):
            continue
        if path.is_symlink():
            raise ValueError('Web source must not contain symbolic links')
        if path.is_dir() or path.suffix not in STATIC_SUFFIXES or '.test.' in path.name:
            continue
        destination = output / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, destination)


def build(source, output, repository, client=None, offline=False):
    repository_name(repository)
    source, output = pathlib.Path(source).resolve(), pathlib.Path(output).resolve()
    if source == output or source in output.parents or output in source.parents:
        raise ValueError('Web source and output must not contain one another')
    if output.exists() and (not output.is_dir() or any(output.iterdir())):
        raise ValueError('Web output directory must be empty')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.gopine-web-', dir=output.parent) as temporary:
        stage = pathlib.Path(temporary) / 'site'
        stage.mkdir()
        copy_static(source, stage)
        firmware_dir = stage / 'firmware'
        firmware_dir.mkdir()
        if not offline:
            client = client or GitHub(repository, os.environ.get('GH_TOKEN'))
            if client.repository != repository:
                raise ValueError('GitHub client repository mismatch')
        firmware = None if offline else checked_firmware(client, firmware_dir)
        manifest = {'schema': 1, 'repository': repository, 'firmware': firmware}
        (firmware_dir / 'latest.json').write_text(json.dumps(manifest, indent=2) + '\n')
        (stage / '.nojekyll').touch()
        if output.exists():
            output.rmdir()
        stage.rename(output)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repository', default=os.environ.get('GITHUB_REPOSITORY', 'Distortions81/goPineTime'))
    parser.add_argument('--source', type=pathlib.Path, default=ROOT / 'web')
    parser.add_argument('--output', type=pathlib.Path, default=ROOT / 'build/web')
    parser.add_argument('--offline', action='store_true', help='Stage UI without fetching any releases')
    args = parser.parse_args()
    result = build(args.source, args.output, args.repository, offline=args.offline)
    firmware = result['firmware']
    print(f'Staged updater at {args.output}; firmware: {firmware["version"] if firmware else "local ZIP only"}')


if __name__ == '__main__':
    main()
