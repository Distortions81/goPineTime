#!/usr/bin/env python3
"""Offline package/protocol and local HTTP tests; never connect to a watch."""
import hashlib
import http.client
import io
import json
import pathlib
import re
import struct
import tempfile
import threading
import unittest
from unittest.mock import Mock, patch
import zipfile

import ota_package as upload
import ota_update as updater


def fixture():
    data = bytearray(48)
    struct.pack_into('<IIHHII', data, 0, 0x96f3b83d, 0, 32, 0, 16, 0)
    struct.pack_into('<BBHI', data, 20, 0, 3, 4, 0)
    struct.pack_into('<II', data, 32, 0x20010000, 0x8029)
    data += bytes.fromhex('0769280010002000') + hashlib.sha256(data).digest()
    return bytes(data)


class PackageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = pathlib.Path(self.temp.name)/'app.zip'

    def write(self, image=None, bad_hash=False, extra=False):
        good_image = fixture()
        with zipfile.ZipFile(self.path, 'w') as z:
            payload = good_image if image is None else image
            manifest = {'format': 'gopine-update', 'protocol': 2, 'image': 'app.bin', 'sha256': hashlib.sha256(payload).hexdigest()}
            if bad_hash: manifest['sha256'] = '0'*64
            if extra: manifest['bootloader'] = {}
            z.writestr('manifest.json', json.dumps(manifest))
            z.writestr('app.bin', good_image if image is None else image)

    def test_valid_application_and_exact_package_hash(self):
        self.write()
        digest, version, image, _ = upload.read_package(self.path, version='0.3.4')
        self.assertEqual(version, '0.3.4')
        self.assertEqual(image, fixture())
        upload.read_package(self.path, digest)
        with self.assertRaises(ValueError): upload.read_package(self.path, '0'*64)
        with self.assertRaises(ValueError): upload.read_package(self.path, version='0.3.5')

    def test_rejects_corruption_and_non_application_packages(self):
        image = fixture()
        for data, bad_hash, extra in [(image[:-1]+b'\0', False, False), (image, True, False), (image, False, True)]:
            self.write(data, bad_hash, extra)
            with self.assertRaises(ValueError): upload.read_package(self.path)

    def test_wrong_link_address_rejected_even_with_correct_hash(self):
        image = bytearray(fixture());struct.pack_into('<I', image, 36, 0x1001)
        image[56:] = hashlib.sha256(image[:48]).digest()
        self.write(image)
        with self.assertRaises(ValueError): upload.read_package(self.path)

    def test_explicit_target_is_required_and_validated(self):
        self.assertEqual(updater.target_config(None, None, {'address':'c9:9e:15:7a:69:b5','adapter':'hci1'})['address'], 'C9:9E:15:7A:69:B5')
        for address, adapter in [('', ''), ('--help','hci1'), ('C9:9E:15:7A:69:B5','hci1; sh')]:
            with self.assertRaises(ValueError): updater.target_config(address, adapter, {})


class FakeJob:
    def __init__(self): self.calls=0;self.state='ready'
    def snapshot(self): return {'state':self.state}
    def start(self):
        if self.state != 'ready': return False
        self.calls+=1;self.state='uploading';return True
    def confirm(self): return False


class JobTests(unittest.TestCase):
    def test_transfer_results_require_keep_or_explicit_retry(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(updater, 'OUTPUT', pathlib.Path(temp)):
            for code, state in [(0, 'awaiting_install'), (3, 'failed'), (1, 'connection_failed'), (2, 'failed')]:
                job = updater.UploadJob(pathlib.Path('unused.zip'), 'hash', '0.3.4',
                                        {'address': 'C9:9E:15:7A:69:B5', 'adapter': 'hci1'})
                process = Mock()
                process.stdout = iter(['Upload 50% (100/200 bytes)\n'])
                process.wait.return_value = code
                context = Mock()
                context.__enter__ = Mock(return_value=process)
                context.__exit__ = Mock(return_value=False)
                with patch.object(updater.subprocess, 'Popen', return_value=context) as launch:
                    self.assertTrue(job.start())
                    job.worker.join()
                    self.assertEqual(job.snapshot()['state'], state)
                    self.assertEqual(job.snapshot()['progress'], 50)
                    self.assertEqual(launch.call_count, 1)
                    if code == 0:
                        self.assertFalse(job.start())
                        self.assertTrue(job.confirm())
                        self.assertEqual(job.snapshot()['state'], 'confirmed')
                    elif code in (2, 3):
                        self.assertFalse(job.start())
                        self.assertFalse(job.confirm())
                    else:
                        self.assertFalse(job.confirm())
                        process.stdout = iter([])
                        self.assertTrue(job.start())
                        job.worker.join()
                        self.assertEqual(launch.call_count, 2)


class WebTests(unittest.TestCase):
    def setUp(self):
        self.job=FakeJob()
        self.server=updater.make_server(self.job,0)
        self.thread=threading.Thread(target=self.server.serve_forever,daemon=True)
        self.thread.start()
        self.addCleanup(self.close)
        self.origin=f'http://127.0.0.1:{self.server.server_port}'
        _, page=self.request('GET','/')
        self.token=re.search("const token='([^']+)'",page.decode())[1]

    def close(self):
        self.server.shutdown();self.server.server_close();self.thread.join()

    def request(self,method,path,headers=None):
        c=http.client.HTTPConnection('127.0.0.1',self.server.server_port)
        c.request(method,path,headers=headers or {})
        r=c.getresponse();result=(r.status,r.read());c.close();return result

    def test_page_and_polling_never_start_a_transfer(self):
        for _ in range(3):
            self.assertEqual(self.request('GET','/status',{'X-OTA-Token':self.token})[0],200)
        self.assertEqual(self.job.calls,0)

    def test_rejects_cross_origin_missing_token_and_rebinding(self):
        for h in [{}, {'Origin':'https://example.com','X-OTA-Token':self.token}, {'Host':'evil.example','X-OTA-Token':self.token}]:
            self.assertEqual(self.request('POST','/upload',h)[0],403)
        self.assertEqual(self.job.calls,0)

    def test_explicit_click_starts_once_and_rejects_duplicate(self):
        h={'Origin':self.origin,'X-OTA-Token':self.token}
        self.assertEqual(self.request('POST','/upload',h)[0],200)
        self.assertEqual(self.request('POST','/upload',h)[0],409)
        self.assertEqual(self.job.calls,1)
        self.assertEqual(self.request('POST','/confirm',h)[0],409)


if __name__ == '__main__': unittest.main()
