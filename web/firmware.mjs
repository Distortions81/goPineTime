// Application-only PineTime DFU packages. No package code is ever executed.
export const MAX_IMAGE_BYTES = 0x73000;
export const MAX_PACKAGE_BYTES = 1024 * 1024;

export class FirmwareError extends Error {
  constructor(message) { super(message); this.name = 'FirmwareError'; }
}

function check(ok, message) { if (!ok) throw new FirmwareError(message); }
function bytesOf(value) {
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
  throw new FirmwareError('Choose a firmware ZIP file.');
}
function equal(a, b) { return a.length === b.length && a.every((byte, i) => byte === b[i]); }
function hex(bytes) { return [...bytes].map(byte => byte.toString(16).padStart(2, '0')).join(''); }
async function sha256(bytes) { return new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)); }

const crcTable = Uint32Array.from({length: 256}, (_, value) => {
  for (let bit = 0; bit < 8; bit++) value = (value >>> 1) ^ ((value & 1) ? 0xedb88320 : 0);
  return value >>> 0;
});
function crc32(bytes) {
  let crc = 0xffffffff;
  for (const byte of bytes) crc = (crc >>> 8) ^ crcTable[(crc ^ byte) & 255];
  return (crc ^ 0xffffffff) >>> 0;
}
function checkExtra(bytes) {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  let offset = 0;
  while (offset < bytes.length) {
    check(offset + 4 <= bytes.length, 'Invalid ZIP extra field.');
    check(view.getUint16(offset, true) !== 1, 'ZIP64 firmware packages are not supported.');
    offset += 4 + view.getUint16(offset + 2, true);
    check(offset <= bytes.length, 'Truncated ZIP extra field.');
  }
}

async function inflate(compressed, size) {
  let stream;
  try { stream = new DecompressionStream('deflate-raw'); }
  catch { throw new FirmwareError('This browser cannot decompress firmware. Use a current Chrome or Edge browser.'); }
  const reader = new Blob([compressed]).stream().pipeThrough(stream).getReader();
  const result = new Uint8Array(size);
  let offset = 0;
  try {
    while (true) {
      const {value, done} = await reader.read();
      if (done) break;
      check(offset + value.length <= size, 'ZIP entry expands beyond its declared size.');
      result.set(value, offset);
      offset += value.length;
    }
    check(offset === size, 'Truncated firmware ZIP entry.');
  } catch (error) {
    await reader.cancel().catch(() => {});
    if (error instanceof FirmwareError) throw error;
    throw new FirmwareError('Firmware ZIP could not be decompressed.');
  } finally { reader.releaseLock(); }
  return result;
}

async function readZip(bytes) {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const u16 = offset => view.getUint16(offset, true);
  const u32 = offset => view.getUint32(offset, true);
  let end = -1;
  for (let i = bytes.length - 22; i >= Math.max(0, bytes.length - 65557); i--) {
    if (u32(i) === 0x06054b50 && i + 22 + u16(i + 20) === bytes.length) { end = i; break; }
  }
  check(end >= 0, 'Choose an intact goPine update ZIP file.');
  check(u16(end + 4) === 0 && u16(end + 6) === 0 && u16(end + 8) === 2 && u16(end + 10) === 2,
    'Expected one goPine update ZIP containing two files.');
  const directory = u32(end + 16), directorySize = u32(end + 12);
  check(directory + directorySize === end, 'Invalid firmware ZIP directory.');
  const entries = [];
  const names = new Set();
  let cursor = directory;
  const decoder = new TextDecoder('utf-8', {fatal: true});
  for (let i = 0; i < 2; i++) {
    check(cursor + 46 <= end && u32(cursor) === 0x02014b50, 'Invalid firmware ZIP entry.');
    const flags = u16(cursor + 8), method = u16(cursor + 10), crc = u32(cursor + 16);
    const compressedSize = u32(cursor + 20), size = u32(cursor + 24);
    const nameSize = u16(cursor + 28), extraSize = u16(cursor + 30), commentSize = u16(cursor + 32);
    const next = cursor + 46 + nameSize + extraSize + commentSize;
    check(next <= end && u16(cursor + 34) === 0, 'Invalid firmware ZIP entry.');
    check((flags & ~0x0808) === 0 && (method === 0 || method === 8), 'Unsupported or encrypted firmware ZIP.');
    check(size <= MAX_IMAGE_BYTES && compressedSize <= MAX_PACKAGE_BYTES, 'Oversized firmware ZIP entry.');
    const nameBytes = bytes.slice(cursor + 46, cursor + 46 + nameSize);
    let name;
    try { name = decoder.decode(nameBytes); } catch { throw new FirmwareError('Invalid ZIP filename.'); }
    check(name.length > 0 && !/[\\/\x00-\x1f]/.test(name) && name !== '.' && name !== '..' && !names.has(name),
      'Firmware ZIP filenames must be unique and contain no paths.');
    names.add(name);
    checkExtra(bytes.subarray(cursor + 46 + nameSize, cursor + 46 + nameSize + extraSize));
    entries.push({name, nameBytes, flags, method, crc, compressedSize, size, local: u32(cursor + 42)});
    cursor = next;
  }
  check(cursor === end && names.has('manifest.json'), 'Expected a goPine update manifest.');
  // Contiguous, nonoverlapping local entries prevent aliases and ambiguous ZIPs.
  entries.sort((a, b) => a.local - b.local);
  cursor = 0;
  const files = new Map();
  for (const entry of entries) {
    const {local, flags, method, compressedSize, size, crc, name, nameBytes} = entry;
    check(local === cursor && local + 30 <= directory && u32(local) === 0x04034b50, 'Invalid or overlapping ZIP entries.');
    const nameSize = u16(local + 26), extraSize = u16(local + 28);
    const start = local + 30 + nameSize + extraSize, finish = start + compressedSize;
    check(finish <= directory && u16(local + 6) === flags && u16(local + 8) === method &&
      equal(bytes.subarray(local + 30, local + 30 + nameSize), nameBytes), 'ZIP headers do not match.');
    checkExtra(bytes.subarray(local + 30 + nameSize, start));
    if (flags & 8) {
      check((u32(local + 14) === 0 || u32(local + 14) === crc) &&
        (u32(local + 18) === 0 || u32(local + 18) === compressedSize) &&
        (u32(local + 22) === 0 || u32(local + 22) === size), 'ZIP headers do not match.');
      cursor = finish;
      check(cursor + 12 <= directory, 'Truncated ZIP descriptor.');
      if (u32(cursor) === 0x08074b50) cursor += 4;
      check(cursor + 12 <= directory && u32(cursor) === crc && u32(cursor + 4) === compressedSize && u32(cursor + 8) === size,
        'ZIP descriptor does not match.');
      cursor += 12;
    } else {
      check(u32(local + 14) === crc && u32(local + 18) === compressedSize && u32(local + 22) === size, 'ZIP headers do not match.');
      cursor = finish;
    }
    const compressed = bytes.subarray(start, finish);
    const data = method === 0 ? compressed.slice() : await inflate(compressed, size);
    check(data.length === size && crc32(data) === crc, 'Firmware ZIP checksum mismatch.');
    files.set(name, data);
  }
  check(cursor === directory, 'Unexpected data in firmware ZIP.');
  return files;
}

/** Validate ZIP layout, manifest, MCUboot header/vectors/SHA-256 and application SHA-256.
 * expectedSha256 is the checksum of the complete ZIP, supplied by release metadata.
 * These checks establish package integrity; they are not a firmware signature.
 */
export async function inspectPackage(buffer, {expectedSha256} = {}) {
  const source = bytesOf(buffer);
  check(source.length >= 22 && source.length <= MAX_PACKAGE_BYTES, 'Firmware ZIP must be no larger than 1 MiB.');
  const bytes = source.slice();
  const digest = hex(await sha256(bytes));
  if (expectedSha256 !== undefined) {
    check(typeof expectedSha256 === 'string' && /^[0-9a-f]{64}$/i.test(expectedSha256), 'Invalid release checksum.');
    check(digest === expectedSha256.toLowerCase(), 'Downloaded firmware does not match the release checksum.');
  }
  const files = await readZip(bytes);
  check(files.get('manifest.json').length <= 16384, 'Oversized firmware manifest.');
  let manifest;
  try { manifest = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(files.get('manifest.json'))); }
  catch { throw new FirmwareError('Invalid firmware manifest.'); }
  check(manifest && typeof manifest === 'object' && Object.keys(manifest).sort().join(',') === 'format,image,protocol,sha256' &&
    manifest.format === 'gopine-update' && manifest.protocol === 2, 'Expected a native goPine protocol-2 application package.');
  check(typeof manifest.image === 'string' && manifest.image !== 'manifest.json' && files.has(manifest.image),
    'Firmware manifest does not match the ZIP entries.');
  const image = files.get(manifest.image);
  check(manifest.sha256 === hex(await sha256(image)), 'Application checksum mismatch.');
  check(image.length >= 80 && image.length <= MAX_IMAGE_BYTES, 'Invalid application size.');
  const view = new DataView(image.buffer, image.byteOffset, image.byteLength);
  const body = view.getUint32(12, true);
  check(view.getUint32(0, true) === 0x96f3b83d && view.getUint32(4, true) === 0 && view.getUint16(8, true) === 32 &&
    view.getUint16(10, true) === 0 && view.getUint32(16, true) === 0 && body >= 8 && body <= image.length - 72,
    'Not a supported MCUboot application.');
  const stack = view.getUint32(32, true), reset = view.getUint32(36, true), address = reset & ~1;
  check(stack > 0x20000000 && stack <= 0x20010000 && stack % 8 === 0 && (reset & 1) && address >= 0x8020 && address < 0x8020 + body,
    'Application is not linked for PineTime MCUboot.');
  const end = 32 + body;
  check(equal(image.subarray(end, end + 8), Uint8Array.of(7, 0x69, 0x28, 0, 0x10, 0, 0x20, 0)) &&
    equal(image.subarray(end + 8, end + 40), await sha256(image.subarray(0, end))), 'MCUboot image hash mismatch.');
  check(image.length === end + 40, 'Unexpected image tail.');
  const major = image[20], minor = image[21], revision = view.getUint16(22, true), build = view.getUint32(24, true);
  return {image, version: `${major}.${minor}.${revision}${build ? `+${build}` : ''}`, sha256: digest};
}
