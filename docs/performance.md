# Memory, rendering, and firmware size

## Candidate 0.3.32: dependency audit (2026-10-11)

The [linked-code audit](releases/0.3.32.md) removes 1,504 flash bytes by replacing
Bluetooth UUID `sprintf` calls with bounded hexadecimal formatting, preserving
all wire operations. Its formatter frame falls from 112 to 24 bytes. A separate
experiment replacing `encoding/binary` with seven vendored primitives saved only
8 bytes and was discarded. The ELF report now breaks down time, reflection/type
metadata, text/numeric helpers, and general C formatting; the latter must remain
absent. Existing Go formatting/crypto/CRC guards are retained.

Battery confirmation UI occupies existing padding, leaving the UI at 712 bytes.
Static RAM, heap region, and selected stack budgets remain unchanged from 0.3.31.
The final image is 296,260 bytes and has not been flashed.

## Candidate 0.3.31: recycled storage and exclusive OTA (2026-10-11)

The [0.3.31 measurements](releases/0.3.31.md) compare the preceding display/UI
candidate with the same version/date/time values. Code plus data falls by 4,228
bytes. Static RAM is unchanged: 22,132 bytes reserved, 43,404-byte heap region.
The updater uses 512 fewer raw object bytes before first OTA; after first use its
cached workspace brings retained storage to 584 bytes, 24 more than before.
This reduces allocation churn rather than claiming a smaller active-update heap.

The receiver reuses its readback buffer for all synchronous validation reads,
and notification slots move indices instead of whole records. The BLE build now
checks TinyGo allocation diagnostics for these paths and includes ARM object
sizes from the exact ELF's DWARF. The checked direct-update stack path falls
from 4,744 to 3,928 bytes; notification handling falls from 4,752 to 4,520 bytes.
The largest selected path plus the 2,048-byte reserve is 7,968 of 8,192 bytes.
These checks do not measure physical free heap or bound every mixed Go/C call.

Exclusive OTA defers background feature callbacks and settings writes, suppresses
alarm/PIN overlays and vibration, and removes suspended notification wakeups.
Power, input, host pumping, and watchdog servicing continue. Automated lifecycle
tests verify cancellation restores normal work without losing the overdue timer.
No upload window increase is included; four queued packets would require only
12 raw bytes beyond the existing 984-byte shared payload, while twenty would
require 3,980 more bytes before alignment. This candidate has not been flashed.

## Charger-wake candidate 0.3.14 (2026-10-09, boot/KEEP confirmed)

Adds two PORT/SENSE charger inputs, a one-shot 150 ms debounce, and a full-screen
power notice using the existing clock font. The notice redraw allocates zero in
host tests and matches full-frame pixels for charging, stopped, full and unplugged
states. It adds no sleeping ADC polls and uses the usual display timeout.

The MCUboot image is **246,460 bytes** (2,272 bytes above 0.3.13). Reserved RAM is
**20,340 bytes**, the heap region **45,196 bytes**, and GPIO ISR frame **48 bytes**.
The selected direct-update stack path is **5,944 bytes** plus the 2,048-byte
reserve inside the 8,192-byte stack; settings-save is **5,808 bytes** before reserve.
These selected budgets do not replace hardware stack/current measurements.
Go race tests/vet and both standalone/BLE builds pass; the exact packaged ELF
passes the resource gate. Four SDL previews are in `build/previews/0.3.14/`.

Package: `build/ota/gopine-dfu-0.3.14.zip`; SHA-256
`1dee149fff8861e1ab15e50d7fd2603dc7f157c60559706d2d056d1cbb4b60fa`. Recovery received all bytes in 2 minutes 59 seconds, validated the image,
and was sent activation/reset on 2026-10-09. Reset acknowledgment was unavailable;
the user subsequently confirmed successful boot and KEEP. See the transfer record in
[OTA notes](ota.md).

## Direct-update candidate 0.3.13 (2026-10-09, not installed)

Adds an explicitly opened, foreground BLE firmware receiver with readback,
streamed image verification, Cancel/Retry and local Install confirmation. The
running firmware and factory recovery are preserved during transfer. The new
50 ms UI deadline exists only while the update screen is open; there is no
new sleep polling or periodic battery sample.

The packaged image is **244,188 bytes**. Reserved RAM remains **20,340 bytes**
and the heap region **45,196 bytes** (a region size, not measured free heap).
The receiver reuses 192-byte readback and 200-byte packet buffers, avoiding those
allocations per chunk. Existing flash-driver and validation allocations remain.
Watch update redraws allocate zero in host tests and match full-frame pixels.

The exact packaged ELF passes selected-path checks: direct transfer/verification
**5,824 bytes**, local installation
**4,160 bytes**, with a 2,048-byte deeper-call reserve inside the
8,192-byte task stack. These are regression budgets, not whole-program stack bounds.
Go race tests/vet, 42 Python tests, six C ASan/UBSan suites, and standalone/BLE
builds pass. Tests cover malformed images, flash readback, simulated power cuts,
resumption, cancellation, expiry, alarm interruption, and local activation.
The Linux updater's prepared direct-mode screen was also checked in the browser.

Package: `build/ota/gopine-dfu-0.3.13.zip`; SHA-256
`147e1ec08311278a21f4cb23fd13554e6bdedeaf70473ec63a4d84cbbfbbb60c`.
The watch still runs confirmed 0.3.7. Initial recovery installation, a subsequent
direct transfer, real interruption/rollback, radio responsiveness, phone behavior
and current measurements remain hardware checks.

## Flash candidate 0.3.12 (2026-10-09, not installed)

This finishes the phone battery-reporting path: the standard Battery Service
uses the UI's existing samples and notifies only when the percentage changes.
There are no additional ADC reads or sleep polling deadlines. It includes all
weather, music, notification and draw work described below.

The standalone and BLE OTA builds, Go race suite, vet, all 35 Python updater/
resource/release tests, and five C port suites under ASan/UBSan pass. The exact
packaged ELF passes the resource gates: 20,340 bytes reserved below the heap,
45,196-byte heap region, 8,192-byte task stack, 2,048-byte deeper-call reserve.
The selected notification and settings-save paths remain 4,504 and 5,616 bytes.

The MCUboot image is **233,084 bytes**. `build/ota/gopine-dfu-0.3.12.zip` has
SHA-256 `ae07551c6dd0484b84cf9e27a88bb1ba753c484f3301e1b4470fb5040dc627f6`.
Receiver validation, boot/KEEP, phone exchange and power measurements are pending.

## Notification candidate 0.3.11 (2026-10-09, not installed)

The bounded ANS receiver and inbox build on 0.3.10. Controlled BLE MCUboot builds
use the same Go/TinyGo toolchain, cached radio sources and date/time inputs as the
draw pass. [Notification behavior and remaining phone checks](time.md#notification-inbox-candidate-0311)
are documented separately.

| Resource | 0.3.10 | 0.3.11 |
|---|---:|---:|
| Linked flash | 226,216 B | 232,580 B |
| Reserved RAM below heap, including alignment | 19,828 B | 20,340 B |
| Heap region | 45,708 B | 45,196 B |
| Selected settings-save core stack path | 5,504 B | 5,616 B |

The inbox state allocates **664 bytes once**, on first opening or receipt. It
holds four fixed messages, selection, quiet mode and vibration deadlines. The C
transport adds a fixed two-packet mailbox; Go consumes at most two per loop pass.
Subsequent receipt and redraw use bounded storage. Host allocation tests and the
TinyGo allocation report show no recurring notification drawing allocation,
including long labels. This is allocation accounting, not a measured peak-free
heap figure. The existing 8 KiB C arena is unchanged; actual phone-side allocation
peaks still require hardware testing.

The heap region remains above the 44 KiB target. The 8,192-byte task stack,
2,048-byte deeper-call reserve and 2,880-byte display strip are unchanged. The new
notification receive path is included in the resource checker and passes.
No background polling is added: the radio's existing update flag wakes the loop,
and only an active message vibration contributes its 150 ms cutoff deadline.

The race suite, vet, standalone build, BLE OTA build and resource gates pass.
C mailbox tests pass ASan/UBSan; the five-second parser fuzz run exercised
1,431,724 cases. Tests cover screen-off motor cutoff, quiet mode, five-second
burst throttling, alarm priority, bounded replacement, paged text, read/dismiss/
clear behavior, and direct-versus-strip pixel equality. Simulator screenshots
cover inbox, empty/quiet states, unread clock, reading, paging, calls and eviction.
The packaged MCUboot image is **232,652 bytes**. `build/ota/gopine-dfu-0.3.11.zip`
has SHA-256
`c337121d4e084f688579a0705a76322dc8278496ee401baa821cd5d16eff00aa`.
This candidate is not flashed; notification interoperability and current
consumption are unmeasured.

## Draw optimization candidate 0.3.10 (2026-10-09)

This local candidate builds on 0.3.9 and has not been flashed. The watch remains
on 0.3.7 for its battery trial. A host CPU profile attributed 61.6% of full-frame
rendering time to the display strip CRC, followed by glyph drawing and packed
pixel writes. This pass addresses those costs:

- Use table-free, seed-zero [XXH32](https://github.com/Cyan4973/xxHash/blob/dev/doc/xxhash_spec.md)
  for display-change fingerprints. Journal CRCs and firmware verification are
  unchanged. The fingerprint remains 32 bits per strip, with the same possibility
  of collisions as any finite fingerprint.
- Write packed RGB444 pixels directly while preserving the adjacent pixel's
  nibble and the existing alpha blending behavior.
- Read text metrics without decoding glyph pixels, and prepare coverage colors
  once per text line. The palette is temporary stack storage, not retained RAM.
- Skip off-strip music and city text preparation, and format weather receipt
  age in a fixed buffer. Weather and forecast now allocate nothing per redraw
  in normal Go, down from 30 allocations / 720 bytes.

### Host rendering measurements

Medians of five 300 ms benchmark runs on an AMD Ryzen 9 7950X, using normal Go
1.27.1. Before is the 0.3.9 renderer; after is 0.3.10. These measure rasterization
and hashing, excluding SPI transport and host framebuffer copying. They do not
establish Cortex-M4 timing or battery savings.

| Redraw | Before | After | CPU time reduction |
|---|---:|---:|---:|
| Clock, full | 183.4 µs | 51.1 µs | 72.2% |
| Music, full | 237.8 µs | 82.7 µs | 65.2% |
| Weather, full | 225.3 µs | 77.8 µs | 65.5% |
| Forecast, full | 243.0 µs | 91.4 µs | 62.4% |
| Stopwatch, partial | 36.5 µs | 15.0 µs | 58.8% |
| Countdown, partial | 33.9 µs | 13.4 µs | 60.5% |
| Update hold, partial | 30.1 µs | 8.5 µs | 71.9% |

All tested redraw paths allocate zero bytes after initialization. Reproduce with:

```sh
go test -run '^$' -bench 'BenchmarkRender|BenchmarkPhoneRender|BenchmarkAnimation' \
  -benchmem -benchtime=300ms -count=5 .
```

### Resource cost and validation

Controlled BLE MCUboot builds with identical date/time/version inputs use
226,216 bytes of linked flash, up 1,044 bytes from 0.3.9. RAM reserved below the
heap is unchanged at 19,828 bytes; the heap region remains 45,708 bytes. The
8,192-byte task stack and 2,880-byte display strip are unchanged. Resource gates
pass, and the TinyGo allocation report adds no drawing heap allocations.

The race suite, vet, standalone and BLE OTA builds pass. Tests compare packed
pixel blending against the generic renderer, fast font metrics against TinyFont,
and text pixels across all five UI fonts. The hash test checks lengths 0–4096
and four alignments against a corpus generated with libxxhash 0.8.2. All 33
simulator screenshots are byte-for-byte identical to 0.3.9.

The packaged MCUboot image is 226,292 bytes. `build/ota/gopine-dfu-0.3.10.zip`
has SHA-256
`2c12072e94d20a304c38179ae6b05951019a30f168312c4946eb3b4a30ec8abf`.
Device redraw timing, phone interoperability, and current consumption remain
unmeasured for this candidate.

## Optimization candidate 0.3.7 (2026-10-09)

Compared with the 0.3.6 source, using Go 1.27.1 and TinyGo 0.42.0, the BLE
MCUboot target, the same cached BLE library, and identical date/time/version
linker inputs. Version 0.3.7 was uploaded on 2026-10-09; the user confirmed boot
and tapped KEEP. See [OTA validation](ota.md#resource-and-awake-work-optimization-037).

| Resource | Before | After |
|---|---:|---:|
| Linked flash (`tinygo -size=short`) | 215,176 B | 206,804 B |
| RAM reserved below `_heap_start`, including alignment | 20,824 B | 19,316 B |
| Heap region | 44,712 B | 46,220 B |
| Runtime CRC slicing table allocated from heap | 8,192 B | 0 B |
| Settings comparison temporary per loop (TinyGo) | 288 B | 0 B |
| Checked settings-save core stack path | 6,064 B | 5,496 B |

Flash falls by 8,372 bytes (3.9%). Static reservation falls by 1,508 bytes, and
the CRC implementation avoids another 8,192-byte retained heap allocation.
Together these improve the RAM budget by 9,700 bytes; this is not a measurement
of peak free heap. The 8,192-byte task stack and 2,880-byte display strip remain.
The checked save path plus its 2,048-byte reserve fits the task stack, without
relaxing any budgets.

The pass makes these changes:

- Parse the fixed build date and format clock/sync text directly, removing
  general `time.Parse`/`time.Format` machinery from the firmware.
- Compare persisted settings in place. TinyGo previously allocated a 288-byte
  returned key on each loop pass, including watchdog-only wakeups. The new
  allocation report confirms that temporary is gone.
- Replace the standard CRC with a compatible IEEE CRC using a 1,024-byte
  immutable flash table. The old implementation had a 1,024-byte static RAM
  table and lazily allocated an additional 8,192-byte slicing table. The new
  checksum allocates nothing; disassembly shows a single flash word load per
  byte. Known answers, all one-byte inputs, random inputs and fuzzing compare
  it with the standard implementation.
- Rasterize/hash only the number band for stopwatch/countdown animation and
  the number/progress bands for update hold feedback. Other visible changes
  use a full pass; wake and invalidated strips force repaint. Tests compare
  partial output pixel-for-pixel with full frames through control, lap, power,
  message and timer changes. Valid-strip flags now use one bit per strip.
- Reuse journal scan/readback scratch space, including when skipping many
  interrupted records. The 160-byte record format, 8 KiB arena, CRC values,
  commit/readback verification and erase policy are unchanged. Existing saved
  records remain compatible; this pass does not reduce reserved settings flash
  or weaken power-loss recovery.
- Sample battery every five seconds while awake, down from every second.
  Wake/alert refresh and fresh pre-write voltage checks remain immediate.
  Battery and charging indicators can now lag by up to five seconds while
  already awake. See [power behavior](power.md#awake-work-in-candidate-037).

### CPU tradeoff and animation measurements

Medians of three normal-Go runs on an AMD Ryzen 9 7950X. Both columns below use
the **new CRC implementation**; they isolate the benefit of narrowing redraws.
They exclude display transport and are not Cortex-M4 cycle measurements.

| Animation | Full raster/hash | Partial raster/hash | Allocations |
|---|---:|---:|---:|
| Stopwatch | 190.3 µs | 42.3 µs | 0 |
| Countdown | 208.9 µs | 35.5 µs | 0 |
| Update hold | 255.6 µs | 31.3 µs | 0 |

The smaller CRC trades checksum throughput for RAM. In the host full-frame
benchmark, clock rendering increased from 68.4 to 181.4 µs, and stopwatch
rendering from 76.1 to 191.5 µs. The old host implementation uses accelerated
CRC; those ratios cannot establish the watch's regression or savings. On the
watch the old implementation used a software slicing table. Partial redraws
avoid most raster/checksum work for frequent animations, but full repaint
latency, actual CPU time and current still need device measurements. No display
brightness, refresh cadence, timer precision or contact-tracking interval was
reduced.

### Validation and reproduction

The race suite, vet, resource-checker tests, CRC compatibility/fuzz checks,
partial-frame comparisons, journal power-cut tests and awake battery-read
count test pass. The 10-second CRC fuzz run checked 2,428,916 inputs. Both the
standalone PineTime and BLE MCUboot OTA builds pass. The packaged 0.3.7 MCUboot
image is 206,876 bytes; `build/ota/gopine-dfu-0.3.7.zip` has SHA-256
`fa8b1df9ff1a02e4803452b55ce0076aca440475063e21898855f27b6c1cb76b`.

Firmware resource gates also reject accidental reintroduction
of standard CRC or general time formatting and check the animation-comparison
stack path. Keep the comparison helper out of the long-lived loop stack frame:
inlining it exceeded the existing save-path budget during development.

```sh
go test -race ./...
go vet ./...
go test -run '^$' -bench BenchmarkAnimation -benchmem -count=3 .
go test ./internal/ieeecrc -fuzz FuzzCompatible -fuzztime 10s -parallel 4
python3 scripts/test_check_resources.py
tinygo build -target=./targets/pinetime-mcuboot-ble.json -size=short \
  -print-allocs='(main\.|checkpoint|ieeecrc)' \
  -ldflags='-X main.firmwareTime=12:00:00 -X main.firmwareDate=2026-10-09 -X main.firmwareVersion=0.3.7' \
  -o /tmp/gopine-opt-after.elf .
python3 scripts/check-resources.py /tmp/gopine-opt-after.elf
bash scripts/build-ota.sh --ble 0.3.7
```

## Phone candidate 0.3.9 (2026-10-09, not installed)

Controlled TinyGo 0.42.0 / Go 1.27.1 BLE MCUboot builds use the same 12:00:00,
2026-10-09 time seed. Version 0.3.9 adds music, connection policy, NPL deadline
queries and RF clock management to the 0.3.8 weather candidate.

| Resource | 0.3.8 weather | 0.3.9 phone/music |
|---|---:|---:|
| Linked flash | 213,408 B | 225,172 B |
| Reserved RAM below heap | 19,316 B | 19,828 B |
| Heap region | 46,220 B | 45,708 B |
| Main task stack | 8,192 B | 8,192 B |
| Display strip | 2,880 B | 2,880 B |

The increment adds 11,764 linked flash bytes and 512 reserved RAM bytes. The
selected phone-update stack path uses 4,368 bytes; the largest checked path is
settings-save at 5,504 bytes, both within the 8,192-byte task stack with a
2,048-byte deeper-call reserve. These selected paths are regression budgets,
not full call-graph bounds or runtime free-heap measurements.

TinyGo reports a one-time phone-state allocation and a lazy weather cache when
weather arrives during a phone session, but no allocations in phone rendering.
Track/artist are bounded to 40 bytes each. Unchanged metadata and position writes
do not signal redraw; the display stays asleep during phone traffic. The bridge
retains the existing 8 KiB C arena; real initialization/traffic peaks need device
measurement with the extra service attributes.

Standalone and BLE OTA builds, the race suite, vet, strip comparisons, sleeping
session/peer-change tests, 12 resource-checker tests and C ASan/UBSan checks pass.
The final package contains a **225,244-byte** MCUboot image:
`build/ota/gopine-dfu-0.3.9.zip`, SHA-256
`9a4aad4969bf6345e097381d50a5e0b19f2ea5084945d106e481357003e61ab9`.
It has not been uploaded. Phone interoperability, HFXO event timing, connected
power and post-disconnect baseline remain hardware acceptance checks.

## Earlier optimization pass (2026-10-05)

Measurements from the 2026-10-05 optimization pass, using Go 1.27.1,
TinyGo 0.42.0, and the existing BLE MCUboot target. Both builds use the same
time/date seed and cached BLE library. TinyGo's default `-opt=z` remains enabled;
bounds checks, garbage collection, and DWARF stack diagnostics remain available.
These measurements precede the 0.3.2 OTA upload. The user subsequently confirmed
that goPine works on the watch and tapped KEEP; see [OTA validation](ota.md#memory-and-rendering-optimization-candidate-032).

| Resource | Before | After |
|---|---:|---:|
| Linked flash (`tinygo -size=short`) | 313,036 B | 209,692 B |
| RAM reserved below `_heap_start`, including system stack/alignment | 23,232 B | 20,384 B |
| Heap region | 42,304 B | 45,152 B |
| Display strip allocated from heap | 5,760 B | 2,880 B |
| Main task stack allocated from heap | 8,192 B | 8,192 B |
| Linked font glyph data, excluding indices | 37,882 B | 25,501 B |

The flash reduction is approximately 33%. The heap region grows by 2,848 bytes and the
strip allocation shrinks by 2,880 bytes. These are budget components, not a
measurement of free runtime heap: strip hashes, persistent closures, UI state,
other live objects, and GC metadata also consume memory.

## Changes

- Screen-change keys compare visible numeric values instead of allocating
  formatted strings. Sleeping skips key generation. Static settings and closed
  sync screens no longer redraw for invisible clock changes.
- Common clock, stopwatch, countdown, settings, and hold-feedback drawing paths
  use fixed-size text buffers and integer formatting. Word wrapping avoids
  temporary word slices and repeated concatenation. Generic `fmt` is removed
  from the firmware; error context still supports unwrapping.
- The loop and updater reuse drawing callbacks. TinyGo's allocation report
  identified a per-frame escaping closure/timestamp that normal Go optimized
  away. Their allocations now occur when the loop/progress session is created.
- Packed RGB444 rectangle fills write pairs directly, preserving edge nibbles.
  Black clears use `clear`. Eight-row strips halve the buffer and service input
  twice as often during full rasterization. A full frame still sends 86,400
  pixel bytes, now in up to 30 bitmap calls; SPI transaction overhead increases.
- Fonts use a per-glyph choice between raw nibbles and minimal-byte row RLE
  packets, with offsets into visible rows. Transparent and opaque runs have no
  payload; partial coverage stays lossless. Decoding clips directly into the
  strip without a glyph buffer. The row offsets spend 4,690 more data bytes than
  the most compact tested encoding to reduce decoding work. Bold24 is
  limited to 43 required characters; general message fonts keep printable ASCII.
  See the [font format and regeneration guide](../internal/uifont/README.md).
- OTA validation uses a concrete, reduced copy of Go's generic SHA-256 core,
  retaining its copyright/license and integrity checks. It avoids linking
  initialization for unrelated FIPS algorithms. Packaging tools still use the
  standard library, independently. See [provenance](../internal/imagesha256/README.md).

Text and controls outside the current strip are rejected before glyph layout.
The common six-pixel rounded corner uses 36 precomputed coverage counts in flash
instead of repeating its 4×4 supersampling. All retained glyphs and the corner
coverage match the original pixels.

PineTime already uses the nRF52832's SPI DMA at 8 MHz, the DC/DC regulator, and
the low-frequency RTC. This pass retains those hardware paths. It does not
introduce a second framebuffer, extra goroutines, or a runtime font rasterizer.

## Host benchmarks

Medians of three runs on an AMD Ryzen 9 7950X, normal Go, excluding display
transport and host framebuffer copies. The render benchmark redraws and hashes
the complete UI, including unchanged strips. These are not Cortex-M4 timings.

| Operation | Before | After | Allocations before → after |
|---|---:|---:|---:|
| Clock frame key | 134 ns | 32.5 ns | 2 → 0 |
| Stopwatch frame key | 399 ns | 21.6 ns | 10 → 0 |
| Clock render | 95.9 µs | 53.2 µs | 30 → 0 |
| Stopwatch render | 128.2 µs | 58.7 µs | 45 → 0 |
| Update/hold render | 209.7 µs | 124.4 µs | 120 → 0 |
| Settings render | 271.1 µs | 82.0 µs | 30 → 0 |

The allocation checks warm up the strip first. They do not claim that startup,
persistence, radio setup, error handling, or every UI screen is allocation-free.
TinyGo's allocation diagnostics also show no per-frame allocations in the
common text/raster helpers; actual watch heap and timing still need measurement.

## Validation and reproduction

```sh
go test -race ./...
go vet ./...
go test -run '^$' -bench 'Benchmark(FrameKey|Render)' -benchmem -count=3 .
go test ./internal/imagesha256 -fuzz FuzzStreaming -fuzztime 10s
python3 scripts/generate-fonts.py --stats
gofmt -w internal/uifont/data.go
python3 scripts/test_check_resources.py
tinygo build -target=./targets/pinetime-mcuboot-ble.json -size=short \
  -print-allocs='(main\.|uifont)' \
  -ldflags='-X main.firmwareTime=12:00:00 -X main.firmwareDate=2026-10-05' \
  -o /tmp/gopine-optimized.elf .
python3 scripts/check-resources.py /tmp/gopine-optimized.elf
```

The BLE build requires the library produced by `scripts/build-ble.py`, as in
the normal OTA workflow. The standalone, MCUboot, BLE MCUboot, and provisioning
targets all build. The race suite, vet, resource checker, font regeneration,
original-coverage fingerprints, clipped raster comparisons, and SHA-256
known-answer/streaming/fuzz checks pass. SDL screenshots were inspected for
the clock, hold prompt, stopwatch, and date editor.

The checked settings-save core stack path is 5,568 bytes, versus 5,432 before.
Adding the checker's 2,048-byte reserve still fits the 8,192-byte task stack.
This remains a selected-path regression check, not a complete call-graph proof.
The resource gate now also rejects accidental relinking of generic `fmt` or
the FIPS module.

The 0.3.2 boot/KEEP smoke test is confirmed. Dedicated checks of touch/hold
responsiveness, timer wake/vibration, BLE sync, and OTA/rollback remain useful.
Host results do not establish physical display latency, energy savings, or
peak free heap.

## Candidate 0.3.15: pairing, shutdown and orientation

The final BLE OTA image is 277,132 bytes. Static RAM reservation is 22,388 bytes,
leaving a 43,148-byte heap region (not a measurement of free heap). The task stack
remains 8,192 bytes; the selected largest path is direct update at 6,024 bytes,
plus the unchanged 2,048-byte deeper-call reserve. Pairing's selected Go path is
3,288 bytes. C stack-usage output is retained in `build/ble/stack-usage.txt`; the
largest single C frame is bond initialization at 552 bytes. These figures do not
prove the full mixed Go/C stack bound, especially during cryptographic work.

The race suite, vet, eight C ASan/UBSan harnesses, 42 Python tests and the exact
ELF resource gate pass. Tests cover host work deferred during HCI waits, every
bond-record write cutoff, saved keys/CCCDs reloaded after simulated reboot,
Forget waiting for disconnect, pairing cancellation and leading-zero codes,
and orientation persistence, sleep/wake, touch transforms and SDL pixels.
Pairing and flipped-screen previews were inspected. A local 0.3.15 image
completed direct transfer and watch verification; boot/KEEP remains unconfirmed.
Real InfiniLink bonding/reconnect and the reported Done crash await hardware validation.
