#!/usr/bin/env python3
"""Release version, artifact integrity, and publication input checks (offline)."""
import hashlib
import json
import pathlib
import struct
import tempfile
import unittest
import zipfile

from release_assets import stage, version_from_tag
from test_ota_update import fixture


class ReleaseTests(unittest.TestCase):
    def test_tag_matches_mcuboot_version_range(self):
        for tag in ['v0.3.4', 'v1.2.3+4', 'v255.255.65535+4294967295']:
            self.assertEqual(version_from_tag(tag), tag[1:])
        for tag in ['0.3.4', 'v0.3', 'v0.3.4-rc1', 'v01.3.4', 'v0.3.4+0',
                    'v256.0.0', 'v0.256.0', 'v0.0.65536', 'v0.0.0+4294967296',
                    'v0.3.4\n', 'v0.3.4; echo bad']:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                version_from_tag(tag)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.source = pathlib.Path(self.temp.name)
        self.output = self.source / 'release'
        image = fixture()
        image = bytearray(image)
        struct.pack_into('<BBHI', image, 20, 0, 3, 15, 0)
        image[56:] = hashlib.sha256(image[:48]).digest()
        manifest = {'format': 'gopine-update', 'protocol': 2, 'image': 'app.bin', 'sha256': hashlib.sha256(image).hexdigest()}
        with zipfile.ZipFile(self.source / 'gopine-update-0.3.15.zip', 'w') as archive:
            archive.writestr('manifest.json', json.dumps(manifest))
            archive.writestr('app.bin', image)
        self.report = self.source / 'gopine-resources-0.3.15.json'
        self.report.write_text(json.dumps({'errors': []}))

    def stage(self):
        stage('v0.3.15', 'a'*40, 'owner/repo', self.source, self.output)

    def test_release_contains_only_intended_assets_with_matching_checksums(self):
        self.stage()
        expected = {'gopine-update-0.3.15.zip', 'gopine-resources-0.3.15.json',
                    'build-info.json', 'INSTALL.md'}
        self.assertEqual({p.name for p in self.output.iterdir()}, expected | {'SHA256SUMS'})
        sums = (self.output / 'SHA256SUMS').read_text().splitlines()
        self.assertEqual(len(sums), len(expected))
        for line in sums:
            digest, name = line.split('  ')
            self.assertEqual(hashlib.sha256((self.output / name).read_bytes()).hexdigest(), digest)
        info = json.loads((self.output / 'build-info.json').read_text())
        self.assertEqual(info['commit'], 'a'*40)
        self.assertEqual(info['version'], '0.3.15')
        self.assertEqual(info['sha256'], hashlib.sha256((self.output / info['package']).read_bytes()).hexdigest())

    def test_install_notes_are_brief_and_link_to_setup(self):
        self.stage()
        notes = (self.output / 'INSTALL.md').read_text()
        self.assertIn('https://owner.github.io/repo/', notes)
        self.assertIn('goPine 0.3.22+', notes)
        self.assertIn('**INSTALL**', notes)
        self.assertIn('**KEEP**', notes)
        readme = 'https://github.com/owner/repo/blob/' + 'a'*40 + '/README.md'
        for anchor in ['build-and-first-install', 'bluetooth-updates', 'recovery']:
            self.assertIn(readme + '#' + anchor, notes)
        self.assertLess(len(notes.split()), 100)

    def test_failed_resource_gate_never_stages_assets(self):
        for report in [{'errors': ['stack exceeds budget']}, {}]:
            self.report.write_text(json.dumps(report))
            with self.assertRaises(ValueError):
                self.stage()
            self.assertFalse(self.output.exists())

    def test_does_not_mix_stale_assets_or_accept_invalid_provenance(self):
        with self.assertRaises(ValueError):
            stage('v0.3.15', 'HEAD', 'owner/repo', self.source, self.output)
        with self.assertRaises(ValueError):
            stage('v0.3.15', 'a'*40, 'owner/repo/other', self.source, self.output)
        self.output.mkdir()
        (self.output / 'stale.zip').write_bytes(b'keep')
        with self.assertRaises(ValueError):
            self.stage()
        self.assertEqual((self.output / 'stale.zip').read_bytes(), b'keep')


if __name__ == '__main__':
    unittest.main()
