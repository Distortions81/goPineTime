#!/usr/bin/env python3
"""Regenerate checked-in AA font tables; not needed for ordinary firmware builds.

Requires Pillow 12.2.0 and the specified DejaVu Sans 2.37 TTF files.
The exact input hashes are checked so a different system font cannot silently
change layout. Run from the repository root (see internal/uifont/README.md).
"""
import argparse
import hashlib
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont, __version__


def checked(path, digest):
    if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise SystemExit(f"Unexpected font SHA-256: {path}")
    return path


def quoted(data):
    return " +\n".join('"' + ''.join(f'\\x{v:02x}' for v in data[i:i+64]) + '"'
                         for i in range(0, len(data), 64)) or '""'


def encode_runs(values):
    """Choose minimum-byte literal/zero/full packets using dynamic programming."""
    size = len(values)
    costs = [0] * (size + 1)
    choices = [None] * size
    for start in range(size - 1, -1, -1):
        best = size + 1
        for count in range(1, min(128, size - start) + 1):
            cost = 1 + (count + 1) // 2 + costs[start + count]
            if cost < best:
                best, choices[start] = cost, (0, count)
        if values[start] in (0, 15):
            count = 0
            while count < min(64, size - start) and values[start + count] == values[start]:
                count += 1
                cost = 1 + costs[start + count]
                if cost < best:
                    kind = 128 if values[start] == 0 else 192
                    best, choices[start] = cost, (kind, count)
        costs[start] = best

    output = bytearray()
    start = 0
    while start < size:
        kind, count = choices[start]
        output.append(kind + count - 1)
        if kind == 0:
            for offset in range(0, count, 2):
                low = values[start + offset + 1] if offset + 1 < count else 0
                output.append(values[start + offset] << 4 | low)
        start += count
    return bytes(output)


def make_font(name, path, size, first, last, stats=False):
    font = ImageFont.truetype(str(path), size, layout_engine=ImageFont.Layout.BASIC)
    data, index = bytearray(), bytearray()
    raw_size, compressed = 0, 0
    pixel_top, pixel_bottom = 0, 0
    for code in range(first, last+1):
        ch = chr(code)
        left, top, right, bottom = font.getbbox(ch, anchor='ls')
        width, height = right-left, bottom-top
        pixel_top, pixel_bottom = min(pixel_top, top), max(pixel_bottom, bottom)
        advance = round(font.getlength(ch))
        assert 0 <= width < 128 and 0 <= height <= 255 and 0 <= advance <= 255
        assert -128 <= left <= 127 and -128 <= top <= 127
        bitmap = Image.new('L', (width, height))
        ImageDraw.Draw(bitmap).text((-left, -top), ch, font=font, fill=255, anchor='ls')
        values = [(v*15+127)//255 for v in bitmap.tobytes()]
        payload, offsets = bytearray(), bytearray()
        for row in range(height):
            offsets.extend(len(payload).to_bytes(2, 'little'))
            payload.extend(encode_runs(values[row*width:(row+1)*width]))
        offsets.extend(len(payload).to_bytes(2, 'little'))
        runs = offsets + payload
        padded = values + ([0] if len(values) % 2 else [])
        packed = bytes((a<<4)|b for a,b in zip(padded[::2], padded[1::2]))
        use_runs = len(runs) < len(packed)
        raw_size += 5 + len(packed)
        compressed += use_runs
        if len(data) > 65535:
            raise ValueError(f'{name}: glyph offset exceeds 16-bit index')
        index.extend(len(data).to_bytes(2, 'big'))
        # The width's high bit marks compressed coverage; all glyphs are <128px.
        data.extend((width | (128 if use_runs else 0), height, advance, left & 255, top & 255))
        data.extend(runs if use_runs else packed)
    if stats:
        print(f'{name}: {len(index)//2} glyphs, {raw_size} -> {len(data)} data bytes, '
              f'{compressed} RLE glyphs')
    line_height = sum(font.getmetrics())
    return (f'var {name} = Font{{first:{first},last:{last},lineHeight:{line_height},\n'
            f'pixelTop:{pixel_top},pixelBottom:{pixel_bottom},\n'
            f'index:{quoted(index)},\ndata:{quoted(data)},\n}}\n')


def main():
    if __version__ != '12.2.0':
        raise SystemExit(f'Use Pillow 12.2.0 to reproduce the tables (found {__version__})')
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--regular', type=Path, default=Path('/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf'))
    parser.add_argument('--bold', type=Path, default=Path('/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf'))
    parser.add_argument('--output', type=Path, default=Path('internal/uifont/data.go'))
    parser.add_argument('--stats', action='store_true', help='Report glyph coverage sizes')
    args = parser.parse_args()
    regular = checked(args.regular, 'ae7b7855e115a5966d8b1b3f80f254ccc117ec86f9965e202ee2940453837280')
    bold = checked(args.bold, '5c1247acef7f2b8522a31742c76d6adcb5569bacc0be7ceaa4dc39dd252ce895')
    result = '// Code generated by scripts/generate-fonts.py; DO NOT EDIT.\n'
    result += '// DejaVu Sans coverage data; see LICENSE.txt for font attribution.\npackage uifont\n\n'
    for name, path, size, first, last in (
        ('Regular18', regular, 18, 32, 126),
        ('Clock', bold, 60, 48, 58),
    ):
        # Regular18 retains printable ASCII for all UI and general text.
        result += make_font(name, path, size, first, last, args.stats) + '\n'
    args.output.write_text(result)


if __name__ == '__main__':
    main()
