import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {deflateRawSync} from 'node:zlib';
import {FirmwareError, inspectPackage, MAX_PACKAGE_BYTES, MAX_IMAGE_BYTES} from './firmware.mjs';

const hash = bytes => createHash('sha256').update(bytes).digest();
function crc32(bytes) {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
  }
  return (crc ^ 0xffffffff) >>> 0;
}
function makeImage({build = 0, tail = Buffer.alloc(0), changeHeader} = {}) {
  const header = Buffer.alloc(32);
  header.writeUInt32LE(0x96f3b83d, 0);
  header.writeUInt16LE(32, 8);
  header.writeUInt32LE(256, 12);
  header[20] = 0; header[21] = 3;
  header.writeUInt16LE(15, 22); header.writeUInt32LE(build, 24);
  const body = Buffer.alloc(256, 0x5a);
  body.writeUInt32LE(0x20010000, 0); body.writeUInt32LE(0x8041, 4);
  changeHeader?.(header, body);
  const content = Buffer.concat([header, body]);
  return Buffer.concat([content, Buffer.from('0769280010002000', 'hex'), hash(content), tail]);
}
function entriesFor(image = makeImage()) {
  return [
    {name: 'application.bin', bytes: image},
    {name: 'manifest.json', bytes: Buffer.from(JSON.stringify({format: 'gopine-update', protocol: 2,
      image: 'application.bin', sha256: hash(image).toString('hex')}))},
  ];
}
function zip(entries = entriesFor(), {deflate = false, descriptor = false} = {}) {
  const locals = [], central = [];
  let offset = 0;
  for (const entry of entries) {
    const name = Buffer.from(entry.name), bytes = entry.bytes;
    const compressed = deflate ? deflateRawSync(bytes) : bytes;
    const size = entry.declaredSize ?? bytes.length;
    const crc = entry.crc ?? crc32(bytes);
    const flags = entry.flags ?? (descriptor ? 8 : 0);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50); local.writeUInt16LE(20, 4);
    local.writeUInt16LE(flags, 6); local.writeUInt16LE(deflate ? 8 : 0, 8);
    if (!descriptor) {
      local.writeUInt32LE(crc, 14); local.writeUInt32LE(compressed.length, 18); local.writeUInt32LE(size, 22);
    }
    local.writeUInt16LE(name.length, 26);
    const record = Buffer.alloc(46);
    record.writeUInt32LE(0x02014b50); record.writeUInt16LE(20, 6);
    record.writeUInt16LE(flags, 8); record.writeUInt16LE(deflate ? 8 : 0, 10);
    record.writeUInt32LE(crc, 16); record.writeUInt32LE(compressed.length, 20); record.writeUInt32LE(size, 24);
    record.writeUInt16LE(name.length, 28); record.writeUInt32LE(offset, 42);
    let suffix = Buffer.alloc(0);
    if (descriptor) {
      suffix = Buffer.alloc(16); suffix.writeUInt32LE(0x08074b50);
      suffix.writeUInt32LE(crc, 4); suffix.writeUInt32LE(compressed.length, 8); suffix.writeUInt32LE(size, 12);
    }
    const block = Buffer.concat([local, name, compressed, suffix]);
    locals.push(block); central.push(record, name); offset += block.length;
  }
  const directory = Buffer.concat(central), end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50); end.writeUInt16LE(entries.length, 8); end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(directory.length, 12); end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}
const rejects = async (bytes, pattern) => assert.rejects(inspectPackage(bytes), error => error instanceof FirmwareError && pattern.test(error.message));

test('validates a stored package and preserves a uint32 build version', async () => {
  const image = makeImage({build: 0xffffffff}), bytes = zip(entriesFor(image));
  const result = await inspectPackage(bytes, {expectedSha256: hash(bytes).toString('hex').toUpperCase()});
  assert.equal(result.version, '0.3.15+4294967295');
  assert.deepEqual(result.image, new Uint8Array(image));
  assert.equal(result.sha256, hash(bytes).toString('hex'));
});
test('validates deflated packages with streaming ZIP descriptors', async () => {
  const image = makeImage();
  const result = await inspectPackage(zip(entriesFor(image), {deflate: true, descriptor: true}));
  assert.equal(result.version, '0.3.15');
  assert.deepEqual(result.image, new Uint8Array(image));
});
test('bounds package allocation and rejects mismatched release hashes', async () => {
  await rejects(new Uint8Array(MAX_PACKAGE_BYTES + 1), /1 MiB/);
  await assert.rejects(inspectPackage(zip(), {expectedSha256: 'f'.repeat(64)}), /release checksum/);
  await assert.rejects(inspectPackage(zip(), {expectedSha256: 'bad'}), /Invalid release checksum/);
});
test('rejects truncated, appended, and ambiguous ZIP directories', async () => {
  const bytes = zip();
  await rejects(bytes.subarray(0, bytes.length - 4), /intact/);
  await rejects(Buffer.concat([bytes, Buffer.from([1])]), /intact/);
  const wrongOffset = Buffer.from(bytes);
  wrongOffset.writeUInt32LE(0xffffffff, wrongOffset.length - 6);
  await rejects(wrongOffset, /directory/);
  const overlap = Buffer.from(bytes), central = overlap.readUInt32LE(overlap.length - 6);
  const second = central + 46 + overlap.readUInt16LE(central + 28);
  overlap.writeUInt32LE(0, second + 42);
  await rejects(overlap, /overlapping/);
});
test('rejects duplicates, path entries, encryption, and extra images', async () => {
  for (const name of ['application.bin', '../application.dat', 'folder/application.dat', 'folder\\application.dat']) {
    const entries = entriesFor(); entries[1].name = name;
    await rejects(zip(entries), /filenames/);
  }
  const encrypted = entriesFor(); encrypted[0].flags = 1;
  await rejects(zip(encrypted), /encrypted/);
  await rejects(zip([...entriesFor(), {name: 'bootloader.bin', bytes: Buffer.alloc(1)}]), /two files/);
});
test('checks ZIP CRC and header consistency before trusting data', async () => {
  const entries = entriesFor(); entries[0].crc = 7;
  await rejects(zip(entries), /checksum/);
  const bytes = zip(); bytes.writeUInt16LE(8, 8);
  await rejects(bytes, /headers/);
});
test('bounds declared and actual decompression sizes', async () => {
  const oversized = entriesFor(); oversized[0].declaredSize = MAX_IMAGE_BYTES + 1;
  await rejects(zip(oversized), /Oversized/);
  const bomb = entriesFor(); bomb[0].bytes = Buffer.alloc(400000); bomb[0].declaredSize = 1;
  await rejects(zip(bomb, {deflate: true}), /expands/);
});
test('rejects non-application manifests and mismatched filenames', async () => {
  const entries = entriesFor();
  entries[1].bytes = Buffer.from('{"manifest":{"bootloader":{},"dfu_version":0.5}}');
  await rejects(zip(entries), /protocol-2/);
  entries[1].bytes = Buffer.from(JSON.stringify({format:'gopine-update',protocol:2,image:'manifest.json',sha256:'0'.repeat(64)}));
  await rejects(zip(entries), /manifest does not match/);
  entries[1].bytes = Buffer.from('null');
  await rejects(zip(entries), /protocol-2/);
});
test('rejects images with wrong MCUboot header or PineTime load vectors', async () => {
  await rejects(zip(entriesFor(makeImage({changeHeader: header => header.writeUInt32LE(1)}))), /MCUboot/);
  for (const stack of [0x20000000, 0x20010008, 0x20000009]) {
    await rejects(zip(entriesFor(makeImage({changeHeader: (_, body) => body.writeUInt32LE(stack)}))), /PineTime/);
  }
  for (const reset of [0x8020, 0x8001, 0x8121]) {
    await rejects(zip(entriesFor(makeImage({changeHeader: (_, body) => body.writeUInt32LE(reset, 4)}))), /PineTime/);
  }
});
test('validates MCUboot hash, exact image length, and application checksum independently', async () => {
  const damaged = makeImage(); damaged[50] ^= 1;
  await rejects(zip(entriesFor(damaged)), /image hash/);
  await rejects(zip(entriesFor(makeImage({tail: Buffer.alloc(4)}))), /image tail/);
  await rejects(zip(entriesFor(makeImage({tail: Buffer.alloc(1, 255)}))), /image tail/);
  const badHash = entriesFor(); const manifest = JSON.parse(badHash[1].bytes); manifest.sha256 = '0'.repeat(64);
  badHash[1].bytes = Buffer.from(JSON.stringify(manifest));
  await rejects(zip(badHash), /Application checksum/);
});
