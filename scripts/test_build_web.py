#!/usr/bin/env python3
"""Offline release selection, provenance, bounds, and Pages staging checks."""
import hashlib
import io
import json
import pathlib
import struct
import tempfile
import unittest
import urllib.request
import zipfile
from unittest.mock import patch

import build_web as web

REPOSITORY = 'Distortions81/goPineTime'
COMMIT = 'a' * 40


def package(version=(0, 3, 22, 0)):
    image = bytearray(48)
    struct.pack_into('<IIHHII', image, 0, 0x96f3b83d, 0, 32, 0, 16, 0)
    struct.pack_into('<BBHI', image, 20, *version)
    struct.pack_into('<II', image, 32, 0x20010000, 0x8029)
    image += bytes.fromhex('0769280010002000') + hashlib.sha256(image).digest()
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        archive.writestr('manifest.json', json.dumps({'format':'gopine-update','protocol':2,
                          'image':'app.bin','sha256':hashlib.sha256(image).hexdigest()}))
        archive.writestr('app.bin', image)
    return output.getvalue(), len(image)


def release(tag='v0.3.22', **kwargs):
    return dict(tag_name=tag, draft=False, prerelease=False, published_at='2026-10-09T12:00:00Z', **kwargs)


class FakeGitHub(web.GitHub):
    def __init__(self):
        super().__init__(REPOSITORY)
        self.calls = []
        self.data, image_size = package()
        self.info = dict(version='0.3.22', tag='v0.3.22', repository=REPOSITORY,
                         variant='ble', package='gopine-update-0.3.22.zip', commit=COMMIT,
                         sha256=hashlib.sha256(self.data).hexdigest(), image_bytes=image_size)
        self.release = release(assets=[])
        self.tags = {'object': {'sha': COMMIT, 'type': 'commit'}}
        self.update()

    def update(self):
        self.info_bytes = json.dumps(self.info).encode()
        self.release['assets'] = [dict(id=1, name='build-info.json', size=len(self.info_bytes)),
                                  dict(id=2, name='gopine-update-0.3.22.zip', size=len(self.data))]

    def fetch(self, path, limit=web.MAX_JSON, binary=False):
        self.calls.append(path)
        values = {'/releases?per_page=100&page=1': [self.release],
                  '/releases/assets/1': self.info_bytes, '/releases/assets/2': self.data,
                  '/git/ref/tags/v0.3.22': self.tags,
                  '/git/tags/' + 'b'*40: {'object': {'type': 'commit', 'sha': COMMIT}}}
        return values[path]


class WebBuildTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = pathlib.Path(self.temp.name)
        self.source = self.root / 'web'
        self.source.mkdir()
        (self.source / 'index.html').write_text('<h1>goPine</h1>')
        (self.source / 'app.mjs').write_text('export const ready = true;')
        self.output = self.root / 'published'
        self.client = FakeGitHub()

    def build(self):
        return web.build(self.source, self.output, REPOSITORY, client=self.client)

    def test_valid_release_is_validated_and_staged_on_same_origin(self):
        manifest = self.build()
        firmware = manifest['firmware']
        self.assertEqual(manifest, json.loads((self.output / 'firmware/latest.json').read_text()))
        self.assertEqual(firmware['package'], 'firmware/gopine-update-0.3.22.zip')
        self.assertEqual((self.output / firmware['package']).read_bytes(), self.client.data)
        self.assertEqual(firmware['commit'], COMMIT)
        self.assertEqual(firmware['sha256'], hashlib.sha256(self.client.data).hexdigest())
        self.assertEqual(firmware['releaseUrl'], 'https://github.com/' + REPOSITORY + '/releases/tag/v0.3.22')
        self.assertTrue((self.output / 'index.html').exists())
        self.assertTrue((self.output / '.nojekyll').exists())
        self.assertFalse((self.output / 'firmware/build-info.json').exists())

    def test_no_compatible_release_retains_local_zip_updater(self):
        self.client.release['tag_name'] = 'v0.3.4'
        manifest = self.build()
        self.assertIsNone(manifest['firmware'])
        self.assertEqual(self.client.calls, ['/releases?per_page=100&page=1'])
        self.assertEqual(list((self.output / 'firmware').iterdir()), [self.output / 'firmware/latest.json'])

    def test_selects_latest_stable_compatible_version(self):
        releases = [release('v0.3.12'), release('v0.3.21'), release('v0.3.22+9'), release('v0.3.22+10'),
                    release('v0.3.14'), release('v0.3.16-rc1'), release('v1.0.0'), release('v2.0.0')]
        releases[-2]['prerelease'] = True
        releases[-1]['draft'] = True
        self.assertEqual(web.select_release(releases)['tag_name'], 'v0.3.22+10')
        self.assertIsNone(web.select_release([release('v0.3.12'), release('../evil'), release('v' + '1'*200)]))

    def test_provenance_and_package_failures_leave_no_site(self):
        changes = [('repository', 'other/repo'), ('variant', 'plain'), ('version', '0.3.14'),
                   ('tag', 'v0.3.14'), ('package', '../gopine.zip'), ('package', 'a'*300),
                   ('sha256', '0'*64), ('sha256', None), ('commit', 'b'*40),
                   ('commit', 'HEAD'), ('image_bytes', 89), ('image_bytes', True)]
        for key, value in changes:
            with self.subTest(key=key, value=value):
                self.client = FakeGitHub()
                self.client.info[key] = value
                self.client.update()
                with self.assertRaises(ValueError):
                    self.build()
                self.assertFalse(self.output.exists())

    def test_image_version_is_checked_independently_of_build_info(self):
        self.client.data, _ = package((0, 3, 14, 0))
        self.client.info['sha256'] = hashlib.sha256(self.client.data).hexdigest()
        self.client.update()
        with self.assertRaisesRegex(ValueError, 'version'):
            self.build()
        self.assertFalse(self.output.exists())

    def test_missing_duplicate_and_oversized_assets_fail_closed(self):
        mutations = [lambda assets: assets.pop(), lambda assets: assets.append(assets[0]),
                     lambda assets: assets[1].update(size=web.MAX_PACKAGE+1),
                     lambda assets: assets[0].update(size=web.MAX_INFO+1),
                     lambda assets: assets[1].update(id='../other'),
                     lambda assets: assets[1].update(size=1)]
        for mutate in mutations:
            self.client = FakeGitHub()
            mutate(self.client.release['assets'])
            with self.subTest(mutate=mutate), self.assertRaises(ValueError):
                self.build()
            self.assertFalse(self.output.exists())

    def test_annotated_tag_must_resolve_to_exact_commit(self):
        self.client.tags = {'object': {'sha': 'b'*40, 'type': 'tag'}}
        self.build()
        self.assertIn('/git/tags/' + 'b'*40, self.client.calls)

    def test_offline_build_never_reads_network_and_excludes_test_fixtures(self):
        (self.source / 'test').mkdir()
        (self.source / 'test/fixture.js').write_text('test')
        (self.source / 'protocol.test.mjs').write_text('test')
        (self.source / '.hidden.js').write_text('hidden')
        (self.source / 'notes.md').write_text('notes')
        (self.source / 'firmware').mkdir()
        (self.source / 'firmware/latest.json').write_text('untrusted')
        result = web.build(self.source, self.output, REPOSITORY, client=self.client, offline=True)
        self.assertEqual(self.client.calls, [])
        self.assertIsNone(result['firmware'])
        self.assertEqual({str(p.relative_to(self.output)) for p in self.output.rglob('*') if p.is_file()},
                         {'index.html', 'app.mjs', '.nojekyll', 'firmware/latest.json'})

    def test_refuses_stale_output_symlink_or_recursive_directory(self):
        self.output.mkdir()
        (self.output / 'keep').write_text('keep')
        with self.assertRaises(ValueError):
            self.build()
        self.assertEqual((self.output / 'keep').read_text(), 'keep')
        with self.assertRaises(ValueError):
            web.build(self.source, self.source / 'output', REPOSITORY, offline=True)
        (self.source / 'escape.js').symlink_to(self.output / 'keep')
        with self.assertRaises(ValueError):
            web.build(self.source, self.root / 'new-output', REPOSITORY, offline=True)

    def test_repository_and_download_urls_are_bounded(self):
        for repository in ['owner/repo/other', '../repo', 'owner/..', 'a'*101+'/repo', 'owner/repo?other']:
            with self.subTest(repository=repository), self.assertRaises(ValueError):
                web.repository_name(repository)
        for url in ['http://api.github.com/a', 'https://api.github.com.evil/a',
                    'https://user@api.github.com/a', 'https://api.github.com:443/a']:
            with self.subTest(url=url), self.assertRaises(ValueError):
                web.github_url(url)

    def test_download_redirect_never_forwards_token_to_asset_cdn(self):
        request = urllib.request.Request('https://api.github.com/repos/owner/repo/releases/assets/1',
                                         headers={'Authorization': 'Bearer test', 'Accept': 'application/octet-stream'})
        redirect = web.GitHubRedirect().redirect_request(request, None, 302, 'Found', {},
                                                       'https://release-assets.githubusercontent.com/file')
        self.assertIsNone(redirect.get_header('Authorization'))
        with self.assertRaises(ValueError):
            web.GitHubRedirect().redirect_request(request, None, 302, 'Found', {}, 'https://example.com/file')

    def test_release_listing_is_paginated_and_bounded(self):
        first = [release('v0.3.1')] * 100
        with patch.object(self.client, 'fetch', side_effect=[first, [release('v0.3.22')]]) as fetch:
            self.assertEqual(len(self.client.releases()), 101)
            self.assertEqual(fetch.call_args.args[0], '/releases?per_page=100&page=2')
        with patch.object(self.client, 'fetch', return_value=first), self.assertRaises(ValueError):
            self.client.releases()

    def test_http_download_is_bounded_even_without_content_length(self):
        class Response:
            headers = {}

            def __enter__(self):
                return self

            def __exit__(self, *args):
                pass

            def read(self, limit):
                return b'x' * limit

        client = web.GitHub(REPOSITORY)
        response = Response()
        with patch.object(client.opener, 'open', return_value=response):
            with self.assertRaisesRegex(ValueError, 'download limit'):
                client.fetch('/releases/assets/1', limit=20, binary=True)
            response.headers = {'Content-Length': '21'}
            with patch.object(response, 'read') as read, self.assertRaisesRegex(ValueError, 'download limit'):
                client.fetch('/releases/assets/1', limit=20, binary=True)
            read.assert_not_called()

    def test_invalid_latest_release_does_not_fall_back_to_older_firmware(self):
        older = self.client.release.copy()
        older['tag_name'] = 'v0.3.14'
        self.client.release['assets'] = []
        with patch.object(self.client, 'releases', return_value=[older, self.client.release]):
            with self.assertRaisesRegex(ValueError, 'missing checked firmware assets'):
                self.build()
        self.assertFalse(self.output.exists())


if __name__ == '__main__':
    unittest.main()
