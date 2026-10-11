"""Validate native goPine application packages for OTA protocol 2."""
import hashlib
import io
import json
import pathlib
import struct
import zipfile

MAX_IMAGE = 0x73000
MAX_PACKAGE = 1024 * 1024

def read_package(path, expected=None, version=None):
    if pathlib.Path(path).stat().st_size > MAX_PACKAGE:
        raise ValueError('Firmware ZIP must be no larger than 1 MiB')
    data = pathlib.Path(path).read_bytes()
    if len(data) > MAX_PACKAGE:
        raise ValueError('Firmware ZIP must be no larger than 1 MiB')
    digest = hashlib.sha256(data).hexdigest()
    if expected and digest != expected:
        raise ValueError('Prepared OTA package changed; prepare it again')
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        names = archive.namelist()
        if len(names) != 2 or len(set(names)) != 2 or 'manifest.json' not in names:
            raise ValueError('Expected one goPine protocol-2 application ZIP')
        if any(i.file_size > MAX_IMAGE for i in archive.infolist()):
            raise ValueError('Oversized update entry')
        manifest = json.loads(archive.read('manifest.json'))
        if not isinstance(manifest, dict) or set(manifest) != {'format', 'protocol', 'image', 'sha256'} or manifest['format'] != 'gopine-update' or manifest['protocol'] != 2:
            raise ValueError('Expected goPine update format with protocol 2')
        binary_name = manifest['image']
        if not isinstance(binary_name, str) or set(names) != {'manifest.json', binary_name} or '/' in binary_name or '\\' in binary_name:
            raise ValueError('Manifest does not match ZIP entries')
        image = archive.read(binary_name)
        if manifest['sha256'] != hashlib.sha256(image).hexdigest():
            raise ValueError('Application checksum mismatch')
    if not 80 <= len(image) <= MAX_IMAGE:
        raise ValueError('Invalid application size')
    magic, load, header, protected, body, flags = struct.unpack_from('<IIHHII', image)
    if (magic, load, header, protected, flags) != (0x96f3b83d, 0, 32, 0, 0) or not 8 <= body <= len(image)-72:
        raise ValueError('Not a supported MCUboot application')
    sp, reset = struct.unpack_from('<II', image, 32)
    if not (0x20000000 < sp <= 0x20010000 and sp % 8 == 0 and reset & 1 and 0x8020 <= (reset & ~1) < 0x8020+body):
        raise ValueError('Application is not linked for PineTime MCUboot')
    end = 32 + body
    tlv = image[end:end+40]
    if tlv[:8] != bytes.fromhex('0769280010002000') or tlv[8:] != hashlib.sha256(image[:end]).digest():
        raise ValueError('MCUboot image hash mismatch')
    if len(image) != end + 40:
        raise ValueError('Unexpected image tail')
    major, minor, revision, build = struct.unpack_from('<BBHI', image, 20)
    image_version = f'{major}.{minor}.{revision}' + (f'+{build}' if build else '')
    if version and version not in (image_version, image_version+'+0'):
        raise ValueError(f'Package version {image_version} does not match {version}')
    return digest, image_version, image, None


last_progress = -1

def progress(done, total, **kwargs):
    global last_progress
    percent = done * 100 // total
    if percent // 5 != last_progress or done == total:
        last_progress = percent // 5
        print(f'Upload {percent}% ({done}/{total} bytes)', flush=True)

