# goPine Bluetooth OTA

goPine 0.3.22 introduces OTA protocol 2. The browser and Linux uploaders support
only this protocol and native `gopine-update-VERSION.zip` packages. The bootloader's
emergency InfiniTime recovery image and trial rollback remain available.
Older update protocols, Nordic DFU packages and the repository's recovery sender
are no longer supported. Historical release notes describe earlier behavior.

## Update inside goPine (0.3.22 and newer)

Use the [browser updater](https://distortions81.github.io/goPineTime/) with a
supported Web Bluetooth browser, or prepare the Linux updater:

```sh
bash scripts/ota-update.sh VERSION --package /path/to/gopine-update-VERSION.zip --web \
  --adapter hci1 --address C9:9E:15:7A:69:B4
```

Omit `--package` to build first. The wrapper installs pinned Bleak 0.22.3 and
needs Linux/BlueZ; gatttool, pexpect and the recovery DFU controller are gone.
The saved address must be goPine's address. Old saved recovery identities are
ignored; specify the goPine address once to save a new target.

1. Prepare the package and open the printed local URL before opening update mode.
2. Open **Settings → Firmware Update**, hold for three seconds, and leave the
   watch on **Ready to connect**. Keep it near the adapter. At least 20% battery
   or external power is required. Other Bluetooth devices can remain enabled.
3. Click **Connect and send update**. The watch receives and verifies firmware
   while remaining inside goPine. Brief disconnects resume checked flash bytes
   in the same session. Cancel, Back, the side button or a timer alert ends it.
4. Tap **INSTALL**, then **KEEP** after boot. Uploading never installs remotely.
   Reset or REVERT before KEEP requests the preceding image through MCUboot.

`--ready` starts the prepared uploader immediately; `--prepare-only` validates
without connecting. `--port` changes the local page's port. Only one uploader
can hold the upload lock. Logs are saved under `build/ota/`. The local HTTP
server binds loopback and requires the page's token for transfer actions.
The local page records boot/KEEP only through the user's confirmation button.

The Linux sender reuses an existing connection to the selected watch on the
selected adapter before looking for advertisements. A connected watch stops
advertising, so discovery alone cannot find that existing PC connection. There
is no need to pair or trust the watch in the PC's Bluetooth settings for an
update. A per-target lock prevents the Linux uploader and pairing diagnostics
from using or disconnecting the same watch concurrently; other devices and
adapters remain available. This lock coordinates goPine tools, not unrelated
desktop applications.

A session expires after ten minutes without activity. Cancel or Retry discards
its staged transfer; watch reboot or a new process cannot resume it. Automatic
reconnect waits up to 90 seconds without progress. The browser instance keeps
its image and session fixed until the page is closed.

## OTA protocol 2

Service: `00060000-78fc-48fe-8e23-433b3a1942d0`; control/data/status use suffixes
`00060001`/`00060002`/`00060003`. Integers are little-endian. Clients must subscribe
to status notifications before writing. There is no protocol-1 fallback.

- Begin: `[1, session:u32, size:u32]`; Verify/Cancel: `[2 or 3, session:u32]`.
- Data: `[session:u32, offset:u32, bytes:1..236]`, using acknowledged ATT writes
  or negotiated Write Commands as described below.
  Actual payload is limited to negotiated `ATT MTU - 11`; at MTU 247 it is 236.
- Status: `[protocol=2, state, error, ATT-value-limit, total:u32, received:u32,
  session:u32]`. Waiting/receiving/verifying/ready/failed are states 1–5.

Starting with 0.3.28, CONTROL also supports a four-byte capability read:
`[revision=1, packet-window=2, flags=1, reserved=0]`. Flag bit 0 enables DATA
Write Commands (writes without response). No characteristic handles or STATUS
fields change. Older write-only CONTROL receivers keep the existing single-packet,
acknowledged-write transfer. The sender also checks the DATA property and native
API; a Python backend whose command size is too small keeps full-size acknowledged
writes with the two-packet window.

Two packets may await flash confirmation, including the packet being processed.
The Go loop writes and reads back each packet before releasing its reserved slot,
draining at most two per foreground tick. A notification can acknowledge both
packets cumulatively. Senders serialize native GATT operations and validate each
acknowledgement against an issued packet boundary. They read status once at
connection, including reconnects, rather than polling per chunk. Only changed
snapshots are notified; temporary transmit-buffer failures retain the snapshot
for retry. A missing acknowledgement reconnects, discards uncommitted queued data,
and resumes at the watch's authoritative durable offset. Wrong-session, duplicate,
out-of-order or full-window Write Commands are silently discarded without advancing
the offset. VERIFY cannot pass queued or uncommitted data.

The explicit OTA owner requests a 7.5–15 ms connection interval with no peripheral
latency once per connection. A central may decline; normal phone links keep their
parameters. The 50 ms foreground fallback remains confined to update mode; pending
BLE work wakes the loop immediately. Python and browser senders report total,
data and verification durations. The first upload installing 0.3.28 still uses the
old receiver; the faster path becomes available after INSTALL and KEEP.

The receiver erases and verifies the secondary trailer before writing, validates
MCUboot structure/vectors/SHA-256, rejects extra image-tail bytes, and leaves boot
magic erased until local Install. Install rechecks power and integrity and leaves
`image_ok` erased for a trial. Neither transfer nor install writes the bootloader,
factory recovery, settings or the running application slot.

The native ZIP contains an MCUboot application BIN and `manifest.json`:

```json
{"format":"gopine-update","protocol":2,"image":"gopine-app-image-VERSION.bin","sha256":"IMAGE_SHA256"}
```

No Nordic init packet, CRC16 or DFU tail-padding workaround is emitted. ZIP CRC32,
application SHA-256, MCUboot SHA-256 and optional release-package SHA-256 validate
integrity. These hashes are not firmware authentication.

## Transition from 0.3.21

The installed receiver determines transfer speed. The first upgrade to 0.3.22
must use the original in-app sender; subsequent uploads use protocol 2. A local
one-time sender is preserved in ignored `build/ota/v1-transition/` for this watch.
It is not part of the maintained updater. Otherwise install through wired setup.
Native protocol-2 ZIPs cannot be sent to the InfiniTime recovery clock. Emergency
recovery can still use an external compatible tool with an official InfiniTime
DFU release; this repository no longer builds packages for that route.

## Download a release

[GitHub Releases](https://github.com/Distortions81/goPineTime/releases) provides
`gopine-update-VERSION.zip`, `SHA256SUMS`, build metadata, installation instructions
and a memory-budget report. Download the application ZIP and verify it with:

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

## Automated tagged releases

After committing and pushing the intended source, create and push an annotated
version tag. For example, the next release after 0.3.22:

```sh
git tag -a v0.3.23 -m 'goPine 0.3.23'
git push origin v0.3.23
```

The **OTA release** workflow runs on `v*` tag pushes. It accepts
`vMAJOR.MINOR.PATCH` or `vMAJOR.MINOR.PATCH+BUILD` within MCUboot's numeric
version limits; prerelease suffixes and leading zeros are rejected. The tag's
version is embedded in both the displayed firmware version and MCUboot header.
Add optional release-specific notes in `docs/releases/VERSION.md` before tagging;
they appear above the generated installation instructions.

The workflow runs host tests/vet, the BLE sanitizer checks, a TinyGo 0.42.0 build
using the pinned InfiniTime port, the linked-image memory gate, and ZIP/hash
validation. It publishes only after all checks pass. The publish job creates a
draft, uploads the OTA ZIP, checksums, installation notes, provenance and memory
report, then publishes the release. A failed upload can be retried from Actions;
an already published release is never overwritten. Use a new tag for changed
firmware; do not move published tags. Create the tag to trigger this workflow,
rather than manually publishing an empty GitHub release first.

Only the publish job receives repository write permission. No personal access
token is needed. This workflow produces application-only OTA assets; wired
bootstrap images are deliberately excluded. Automated checks do not establish
device boot, KEEP, wake reliability or battery life. The earlier locally tested
0.3.22 package recorded below has a different build clock/hash from the CI release.

## One-time wired setup (standalone goPine to MCUboot)

This replaces the existing standalone firmware. Use a working 3.3V-logic SWD
probe and common ground; keep the battery-connected watch powered by its own
charger. Do not connect a second power supply to the watch's VCC pad.
Keep the wired recovery method available until KEEP and REVERT have both been
tested. Back up internal flash before replacing firmware if you need to retain
the existing image.

```sh
bash scripts/build-ota.sh --bootstrap 0.2.0
```

The script downloads pinned official assets and checks SHA-256 before use:

| Component | Official release | SHA-256 |
| --- | --- | --- |
| Bootloader | [1.0.1](https://github.com/InfiniTimeOrg/pinetime-mcuboot-bootloader/releases/tag/1.0.1) | `4d25ea801c7859069881a4e601cd25f7598ad16114b2a806be401865d255d72a` |
| BLE recovery (`spinor.bin`) | [0.14.1](https://github.com/InfiniTimeOrg/InfiniTime/releases/tag/0.14.1) | `3bf992adb44282f384b1944387abaea2a141afd24498e1688280e842247536dc` |

The older recovery release is intentional: it is the factory recovery image
documented by [InfiniTime](https://github.com/InfiniTimeOrg/InfiniTime/blob/main/doc/gettingStarted/updating-software.md),
not a claim that 0.14.1 is the newest full InfiniTime firmware. Downloaded binary
assets are ignored by Git; upstream licensing applies to those binaries.

**There are two ordered wired flashes:**

1. Flash `build/bootstrap/gopine-recovery-setup-0.2.0.hex` with sector erase, and
   reset. This standalone helper writes/verifies the external factory recovery
   area and erases/verifies the secondary slot's last sector. Wait for the watch
   to say **Recovery ready**. If setup fails or power is lost, re-run this step;
   do not proceed to the next HEX. This helper is for migration from standalone
   goPine only: it replaces any existing bootloader at address zero.
2. Only after that success, flash `build/bootstrap/gopine-bootstrap-0.2.0.hex`
   with sector erase and reset. It contains the stock bootloader and a confirmed
   goPine image, and explicitly clears stale internal trailer/scratch state.
   The watch should show the pine-cone boot screen, then the clock.

For example, with pyOCD and an already verified CMSIS-DAP connection:

```sh
pyocd flash -t nrf52832 -f 1000000 -O auto_unlock=false -e sector build/bootstrap/gopine-recovery-setup-0.2.0.hex
pyocd reset -t nrf52832 -f 1000000 -O auto_unlock=false
# STOP and check the watch says "Recovery ready" before continuing.
pyocd flash -t nrf52832 -f 1000000 -O auto_unlock=false -e sector build/bootstrap/gopine-bootstrap-0.2.0.hex
pyocd reset -t nrf52832 -f 1000000 -O auto_unlock=false
```

Select the probe explicitly with `--uid` if more than one is attached. Do not
use a mass erase or automatic unlock/recover operation. The initial-install HEX
covers internal `0x00000..0x7cfff`; it leaves UICR and the final spare pages alone.
The helper only writes external `0x00000..0x3ffff` and `0xb3000..0xb3fff`.
Neither touches the external filesystem at `0xb4000` and above.

The helper is deliberately installed **before MCUboot** so that an old pending
update cannot preempt provisioning. It is never emitted as an OTA ZIP. Normal
OTA builds do not contain or rewrite the recovery asset.

## Build and format details

```sh
# Optional: choose the tool path and reproducible initial time.
TINYGO=/path/to/tinygo FIRMWARE_TIME=12:00:00 bash scripts/build-ota.sh 0.2.1

# Host tests and both target configurations.
go test ./...
go vet ./...
tinygo build -target=./targets/pinetime-gopine.json -o /tmp/gopine-standalone.hex .
tinygo build -target=./targets/pinetime-mcuboot.json -o /tmp/gopine-ota.elf .
```

Versions are MCUboot `major.minor.revision[+build]` values with 8/8/16/32-bit
unsigned components. Build outputs include the ELF for debugging, an image BIN,
and a native update ZIP. **Only the ZIP is for OTA upload.** Do not flash the raw BIN or
ELF at address zero. `cmd/otapack` checks ELF load addresses, vector values,
image bounds, and the bootloader hash for initial-install packages.

The native ZIP contains `manifest.json` and an application `.bin`, with no
recovery DFU init packet or tail padding. The BIN has a 32-byte MCUboot header
and SHA-256 TLV, with no preconfirmed trailer.

| Region | Location |
| --- | --- |
| MCUboot bootloader | Internal `0x0000..0x6fff` |
| Bootloader log/relocated vectors | Internal `0x7000..0x7fff` |
| Primary image / executable vectors | Internal `0x8000` / `0x8020` |
| Primary slot size / confirm flag | `0x74000` bytes / internal `0x7bfe8` |
| Swap scratch | Internal `0x7c000..0x7cfff` |
| goPine bond journal (0.3.15 candidate) | Internal `0x7d000..0x7dfff` |
| goPine planned-reset clock journal (0.2.4) | Internal `0x7e000..0x7ffff` |
| Factory recovery | External `0x00000..0x3ffff` |
| Secondary image | External `0x40000..0xb3fff` |
| Existing filesystem (untouched) | External `0xb4000..0x3fffff` |

Both project targets reserve an 8KB fallback goroutine stack. TinyGo 0.42 cannot
calculate this program's maximum stack statically and otherwise falls back to
2KB. The first hardware setup exposed a stack overflow into globals with that
default; do not bypass the project targets or override the stack size downward.
The packager rejects ELF files with smaller task stacks, and the bootstrap
build retains a matching helper ELF for SWD diagnosis. The corrected helper was
reflashed with full readback verification; its CPU fault registers then stayed
clear, and the watch reported **Recovery ready**. The MCUboot + goPine bootstrap
was then installed and all 512,000 bytes read back correctly, accounting for
the stock bootloader's expected vector-table copy at `0x7f00..0x7fff`; UICR
remained unchanged. Bluetooth upload and trial boot have since been verified;
on-watch REVERT also restored recovery. KEEP was subsequently verified by its
confirmation-word readback and the user; a normal reboot after KEEP is pending.

Hardware bring-up also exposed two startup issues: the CST816S could reject the
first I2C setup command, and MCUboot left the low-brightness backlight channel
enabled. goPine now performs the touch-controller wake reads before bounded
configuration retries and explicitly owns all three backlight channels. After
the application-only SWD repair, readback matched, touch interrupts were enabled,
the I2C error register was clear, and all backlight channels were off during sleep.

The linker reserves the last primary sector for swap status/trailer. Starting
recovery is allowed only from a confirmed image. It invalidates the destination
trailer first, checks the copied image by readback, marks recovery permanent,
and writes boot magic last. Keep only programs the confirmation word; it never
erases the trailer. Flash waits are bounded and feed the bootloader watchdog.
Host tests inject failures at every staging mutation and check that the source,
filesystem, and trial rollback image are protected. They are not a substitute
for power-interruption tests with real flash hardware.

## Security and limitations

Images remain unsigned. SHA-256 and ZIP checksums protect against accidental
corruption; they do not authenticate who supplied the image. Update mode requires
a local hold, but its Bluetooth connection does not require pairing.

Automated tests cover bounds, readback, verification, cancellation, packet
handoff, real-host notifications, resume and timeout behavior. They do not
establish physical radio throughput, battery life or power-loss reliability.
`python3 scripts/ota_benchmark.py` runs the actual Python sender against a
deterministic event-driven radio/flash model. Browser sender tests use the same
timing scenarios. These comparisons test the pipeline under controlled latency;
they are not physical-watch throughput measurements.

## Hardware acceptance checklist

On 2026-10-04, goPine staged recovery successfully and the watch displayed its
InfiniTime logo. Using the Linux host's TP-Link USB adapter and upstream Legacy
DFU controller, a 166,640-byte goPine 0.2.2 image transferred and passed receiver
validation. SWD reads then confirmed version 0.2.2, `copy_done = 1`, an erased
`image_ok` flag (unconfirmed trial), and clear CPU fault registers. The user
tapped REVERT; subsequent reads verified the official recovery header/vectors,
its confirmed trailer, and clear fault registers. A second Bluetooth upload
completed; the user tapped KEEP and SWD verified `image_ok = 0x00000001`. The watch
then entered recovery again after the user reported an unexpected update prompt.
Whether repeated touch events or an unclear transition led to that prompt is
not established; successful re-entry does not establish that the UI behaved
correctly. A third Bluetooth upload restored goPine, with a set confirmation
flag and clear CPU fault registers; the user reported that it was working.
An earlier interrupted transfer was retried successfully without reflashing
over SWD; its cause was not established. The user also reported needing a side-button
sleep/wake cycle before START worked once; that interaction still needs to be
reproduced and diagnosed.

- [x] Provision recovery, install MCUboot, and verify boot on the actual watch.
- [x] Start recovery and transfer a goPine ZIP from the Linux host.
- [x] Choose REVERT on the watch and verify that confirmed recovery returns.
- [x] Choose KEEP on the next trial and verify the confirmation flag.
- [ ] Reset after KEEP and verify that goPine stays installed.
- [x] Start another update after KEEP and complete the repeated OTA cycle
  (0.3.1 KEEP followed by recovery upload and KEEP on 0.3.2; see the
  [0.3.2 validation record](#memory-and-rendering-optimization-candidate-032)).
- [ ] Finish checking battery readings, touch controls and sleep/wake behavior.
- [ ] Test a deliberately canceled BLE transfer and reconnect without wired repair.
- [ ] Only with SWD recovery available, test power interruption while staging and
  swapping. Do not rely on host tests as proof of flash atomicity.

## Settings and rendering revision (0.2.3)

The clock's UPDATE button was removed to prevent extra KEEP-position taps from
opening a new update prompt. Swipe-left opens Settings; swipe-right goes back.
A separate update screen requires a fresh, stationary three-second touch with
a countdown and progress bar. No elapsed-time timer alone can start recovery.
The side-button/watchdog path is serviced during continuous touch, and wake
gestures cannot operate controls. The 12/24-hour setting is in-memory only.
The clock uses enlarged sans-serif digits, a battery gauge with a charging-only
lightning bolt, a readable percentage, and distinct low-battery/external-power
labels. Battery estimation and the underlying voltage curve are unchanged.

Text and controls are rasterized into a reusable 5,760-byte strip buffer, with
changed-strip detection instead of per-pixel LCD transactions. Host tests check
pixel equivalence, skipped transfers, retry after transfer errors, navigation,
hold cancellation, power/state guards, and SDL input/rendering. These tests do
not establish physical touch sensitivity, timing, or watch frame rate.

On 2026-10-04, the host uploaded the 171,828-byte 0.2.3 image over Bluetooth;
the receiver accepted validation and the activation/reset command was sent.
The user reported that the new interface looked great on the watch. Detailed
interaction checks remain pending: a full three-second hold, early release,
dragging away, sleep/wake, repeated KEEP taps, and persistence after KEEP and
reboot. The bootloader and recovery images are unchanged; a reliable blue-menu
exit from recovery still needs a controlled test before any upload/red recovery
overwrites the rollback copy. This UI revision does not resolve that question.

## Antialiased rendering revision (0.2.4)

This revision adds native-size, four-bit antialiased text, a true-black
background, dark-charcoal cards, and small drawing helpers for antialiased
circles, diagonal lines, and rounded corners. Straight box edges stay crisp.
Coverage blends against the destination rather than assuming a white
background. The clock removes the power-status card and swipe hint, and places
a small AM/PM beside the digits on the same baseline. Swipe-left still opens
Settings. Small Picopixel metadata remains pixel-aligned. The existing
5,760-byte strip buffer and changed-strip transfer skipping are unchanged.

Settings now has a Time & Date submenu with manual hour/minute and date editors,
Save/Cancel, and the 12/24-hour preference. Sleep discards unfinished edits.
Civil time uses an application offset, leaving runtime deadlines unchanged.
The OTA build seeds both local date and time. A planned OTA/revert reset now
hands off the date/hour through a compact flash journal and seconds within that
hour through the two retention registers. The snapshot is taken after staging,
just before reset; identical hour/format anchors reuse their existing record.
There are no periodic flash writes. The next goPine boot consumes the handoff
once and marks restored time approximate. Bootloader/recovery time is not
counted, and arbitrary other firmware is not expected to preserve this format.
Cold starts and unexpected resets fall back to the build seed. This handoff is
not hardware-validated yet. Bluetooth time synchronization is still a separate
follow-up. See [time notes](time.md).

Font tables are generated ahead of time from DejaVu Sans and stored as immutable
strings in flash; ordinary builds need neither Python nor font files. See
`internal/uifont/README.md` for regeneration and the font license. No new Go
dependency, bootloader change, or recovery change is introduced.

Host tests cover coverage blending, geometry clipping and symmetry, glyph data,
and pixel equivalence across strip boundaries. Standalone, provisioning, and
MCUboot builds are checked. Build a fresh time-seeded package with
`bash scripts/build-ota.sh 0.2.4` before uploading. Watch installation and user
feedback are recorded below; redraw speed has not been quantitatively measured.

### Touch-hold repair and watch validation

The installed build's hold indicator was reported to disappear immediately.
Inspection found that a single failed I2C read or malformed coordinate canceled
the entire gesture, and display drawing could delay input sampling. Without an
on-device trace, these are candidate causes, not a confirmed hardware diagnosis.

The repair is based on actual PineTime implementations:

- [InfiniTime CST816S](https://github.com/InfiniTimeOrg/InfiniTime/blob/main/src/drivers/Cst816s.cpp)
  enables periodic touch interrupts (`0xFA = 0x70`). Its
  [touch handler](https://github.com/InfiniTimeOrg/InfiniTime/blob/main/src/touchhandler/TouchHandler.cpp)
  ignores invalid reports instead of interpreting them as a release.
- [TinyGo PineTime board](https://github.com/aykevl/board/blob/main/board-pinetime.go)
  documents sporadic bad coordinates and retains the last valid point. Its
  latched, continuous-reading approach is different from InfiniTime's IRQ-driven
  approach; it does not establish that all between-interrupt reads must fail.
- [wasp-os PineTime board](https://github.com/wasp-os/wasp-os/blob/master/wasp/boards/pinetime/watch.py.in)
  uses the [CST816S driver](https://github.com/wasp-os/wasp-os/blob/master/wasp/drivers/cst816s.py),
  which reads on falling-edge interrupts and ignores failed reads. Its gesture
  interface is not itself a model for goPine's three-second confirmation.

goPine now reads reports only after IRQs and tolerates brief invalid reports
without advancing the hold. More than 250ms without valid contact cancels it
and requires release before rearming; this is a goPine safety bound, not a
claimed hardware specification. Valid release or movement cancels immediately.
An unusable release position also cancels without activating a control.
Sampling between render strips and an allocation-free event queue keep drawing
from starving input. Queued release/cancel events remove obsolete holds.
The non-rotated text path preserves strip clipping instead of rasterizing every
antialiased glyph fifteen times. No controller register, bootloader, or recovery
change is needed.

Host regressions cover intermittent bad coordinates, read failures, missing
IRQs, release/rearm, queue overflow, and text pixel equivalence.

On 2026-10-05, after the user rebooted the watch into recovery, the host uploaded
the 215,308-byte 0.2.4 image containing commit `027967f` through USB Bluetooth
adapter hci1. Transfer took 2 minutes 25 seconds; the receiver accepted firmware
validation and the activation/reset command was sent. Earlier connection
timeouts had occurred before any firmware transfer. The user subsequently
reported that everything appeared to work, following a request to check KEEP
and a short hold/release. This is a successful on-watch smoke test, not a
separately recorded result for every gesture. Deliberate full-hold recovery
re-entry, movement cancellation, and planned-reset clock retention still need
dedicated checks. The bootloader and recovery images were not changed.

Later on 2026-10-05, the host sent the 325,724-byte 0.3.0 BLE candidate through
the same recovery-based Legacy DFU flow using USB adapter hci1. All bytes were
acknowledged in 3 minutes 39 seconds, and the receiver reported successful
firmware validation. The activation/reset command was sent, but the connection
dropped before its write acknowledgement; no transfer retry was attempted.
The user subsequently reported the InfiniTime screen and no observed goPine
confirmation screen. Successful boot/KEEP is therefore not established; the
0.3.0 candidate must not be treated as hardware-validated. Resource inspection
found an insufficient task-stack budget (see [timers](timers.md)). No second
upload was attempted. This upload did not replace the bootloader or the stored
recovery image.

The subsequent 0.3.1 retry on 2026-10-05 sent all 313,124 bytes through hci1
in 3 minutes 34 seconds. The receiver reported successful firmware validation,
and the activation/reset command was sent. The sender then timed out waiting
for the activation write acknowledgement, with a GATT invalid-file-descriptor
warning. No automatic retry was attempted. The user subsequently confirmed
pressing KEEP in goPine and reported that a short countdown woke the sleeping
watch and vibrated. This establishes a successful boot/KEEP and countdown
smoke test, not complete clock-tool validation. This application upload did not
replace the bootloader or recovery image.

### Memory and rendering optimization candidate (0.3.2)

On 2026-10-05, the host uploaded the 209,828-byte 0.3.2 BLE candidate through
recovery using USB adapter hci1. All bytes transferred in 2 minutes 22 seconds,
and the receiver reported successful firmware validation. The activation/reset
command was sent, but its write acknowledgement timed out with a GATT
invalid-file-descriptor warning. No transfer retry was attempted. The user
subsequently reported that goPine works and confirmed tapping KEEP, establishing
successful boot and confirmation of this candidate. Detailed timing, power,
and long-running stability measurements remain pending. This application
upload did not replace the bootloader or stored recovery image.

Package: `build/ota/gopine-dfu-0.3.2.zip`; SHA-256:
`204244c5d3ac9956b9c57eabd2b200176b8c8140b6ec868388186a032f495f02`.
The resource gate passed for this exact ELF. Optimization measurements and
verification are recorded in [performance notes](performance.md).

### Touch wake and visible hold countdown candidate (0.3.3)

On 2026-10-06, the host uploaded the 212,668-byte 0.3.3 BLE candidate through
recovery at `C9:9E:15:7A:69:B5` using USB adapter hci1. An initial connection
attempt timed out before sending firmware. The next connection verified the
Legacy DFU service and transferred the complete image in 2 minutes 24 seconds,
with receiver byte counts checked during transfer. Receiver firmware validation
succeeded, and activation/reset was sent. Its write acknowledgement timed out;
no transfer retry was attempted after activation. The user then confirmed that
goPine booted and they tapped KEEP, establishing successful boot and on-watch
confirmation. Sleep current, repeated touch reinitialization, and the new hold
layout still need dedicated hardware checks. Bootloader and stored recovery
were unchanged.

This candidate adds the default-Off Touch to wake setting and sleep power
changes described in [the power audit](power.md). The firmware-update countdown
and progress bar now appear above the fixed PRESS AND HOLD button.

Package: `build/ota/gopine-dfu-0.3.3.zip`; SHA-256:
`7769895a9f893290ff661ff2f23e266a750dda5bdca1c28b6c70f1a07d44aee9`.
Host tests and the resource gate passed for the packaged build. The transfer log
is `build/ota/gopine-upload-0.3.3.log`; its one-shot sender is
`build/ota/upload-0.3.3.py`, wrapping the pinned InfiniTime Legacy DFU controller
with an explicit adapter/address, package hash, service verification, strict
write/notification checks, and no automatic transfer retries.

### Sleep power candidate (0.3.4)

On 2026-10-07, the host sent all 213,988 bytes through recovery at
`C9:9E:15:7A:69:B5` using hci1 in 2 minutes 24 seconds. Initial connections
failed before DFU started. Restarting recovery and connecting directly with the
prepared sender resolved the connection problem. Receiver byte counts and final
firmware validation passed. Activation/reset was sent; its acknowledgement
timed out and no automatic retry followed. The user confirmed that goPine
booted and they tapped KEEP. Sleep current and extended wake testing remain
pending; boot confirmation alone does not establish a battery-life improvement.

This candidate disables unused sensors, avoids periodic battery samples asleep,
uses event/deadline sleep, and adopts measured PineTime discharge data for the
percentage estimate. See [the power audit](power.md).

Package: `build/ota/gopine-dfu-0.3.4.zip`; SHA-256:
`6bb8616b65bfe1caa6941fb71b0a71ca120b5a1847637f7749a991333c0305b0`.
Host tests, standalone/BLE builds, the resource gate, and BLE port sanitizer
checks passed. Transfer log: `build/ota/gopine-upload-0.3.4.log`.

### Resource and awake-work optimization (0.3.7)

On 2026-10-09, the host sent all 206,876 bytes through InfiniTime recovery at
`C9:9E:15:7A:69:B5` using USB adapter hci1 in 2 minutes 20 seconds. Two direct
connection attempts failed before any transfer. A brief LE discovery on the
same adapter detected the expected InfiniTime address; the subsequent direct
connection verified its Legacy DFU service and succeeded. Receiver byte counts
and final firmware validation passed, and activation/reset was sent. Its
acknowledgement was unavailable, so no transfer retry followed. The user
confirmed goPine booted and they tapped KEEP. This establishes installation,
not a measured battery-life improvement.

Package: `build/ota/gopine-dfu-0.3.7.zip`; SHA-256:
`fa8b1df9ff1a02e4803452b55ce0076aca440475063e21898855f27b6c1cb76b`.
Transfer log: `build/ota/upload-0.3.7-20261009-175403-402942.log`.
See [resource measurements](performance.md) and [power behavior](power.md).

### Phone-feature flash candidate (0.3.12, prepared only)

The 233,084-byte application-only package includes weather, music, opt-in phone
connections, the Android-compatible notification inbox, draw optimizations and
updated battery reporting. Host tests, standalone/BLE builds, updater tests,
BLE sanitizer checks and the exact ELF resource gate pass. See
[candidate measurements](performance.md#flash-candidate-0312-2026-10-09-not-installed).

Package: `build/ota/gopine-dfu-0.3.12.zip`; SHA-256:
`ae07551c6dd0484b84cf9e27a88bb1ba753c484f3301e1b4470fb5040dc627f6`.
The saved target remains recovery `C9:9E:15:7A:69:B5` via USB adapter hci1.
No transfer or boot/KEEP is established yet. iPhone ANCS/pairing remain absent.
After installation, confirm KEEP and check the clock, sleep/wake and one timer
before opting into a ten-minute phone session. Exercise music, weather and
notification delivery, then turn the phone connection Off for a comparable
battery baseline. Persistent radio and message vibration change that baseline.

### Direct-update candidate (0.3.13, prepared only)

The 0.3.13 candidate contains all 0.3.12 features plus the in-app receiver and
Linux direct sender described above. The **244,188-byte** MCUboot image passed
host tests, both embedded builds and the linked-resource gate. No firmware was
sent to the watch during preparation or browser preview. The installed version
remains 0.3.7 until a fresh recovery upload and user-confirmed boot/KEEP.

Package: `build/ota/gopine-dfu-0.3.13.zip`; SHA-256:
`147e1ec08311278a21f4cb23fd13554e6bdedeaf70473ec63a4d84cbbfbbb60c`.
Use recovery `C9:9E:15:7A:69:B5` via hci1 for this first installation. Once kept,
use direct mode at goPine `C9:9E:15:7A:69:B4` for the next candidate. Direct BLE
transfer and MCUboot rollback still need their first hardware validation.

### Charger-wake upload (0.3.14, boot/KEEP confirmed)

Supersedes the unflashed 0.3.13 candidate with charger start/stop and unplug wake
screens. The 246,460-byte image passed Go race tests, vet, standalone/BLE builds
and the resource gate. The earlier 0.3.13 uploader was stopped before sending
any firmware. On 2026-10-09 the recovery uploader sent all 246,460 bytes of
0.3.14 via hci1 to `C9:9E:15:7A:69:B5` in 2 minutes 59 seconds. Recovery
reported successful firmware validation and the host sent activation/reset.
The final reset acknowledgment was unavailable; no retry was attempted.
The user confirmed successful boot and tapped KEEP on 2026-10-09. Installed
firmware is now 0.3.14.

Package: `build/ota/gopine-dfu-0.3.14.zip`; SHA-256:
`1dee149fff8861e1ab15e50d7fd2603dc7f157c60559706d2d056d1cbb4b60fa`.
This initial upload used recovery via hci1. Real charger edges and a subsequent
in-app update still need hardware verification.

### Local 0.3.15 — direct transfer verified; boot/KEEP pending

The earlier local `gopine-dfu-0.3.15.zip` contained a 277,132-byte image with the connected
shutdown fix, passkey pairing/bond storage and saved flipped-screen option.
SHA-256: `4f311e91f91a1149320e03f228afaf5bf67eecaec797b426e1efbbe360fd6637`.
Host tests and resource gates pass. On 2026-10-09 the in-goPine uploader reached
277,132/277,132 bytes and the watch verified the image. The first service-discovery
attempt disconnected before data; the sender reconnected and completed the full
transfer. Log: `build/ota/upload-0.3.15-20261009-203113-475386.log`.
The user has been asked to tap Install and then KEEP; boot/KEEP confirmation is
pending, so the last confirmed installed version is still 0.3.14. Follow
[InfiniLink acceptance](pairing.md) after confirmation.

The published release is rebuilt from its tagged source commit. Its package
checksum is recorded in the release assets; the local checksum above identifies
only the earlier hardware-transfer test.

### Published 0.3.16 — recovery validated; boot/KEEP confirmed

On 2026-10-09, the TP-Link adapter on hci1 sent the published 277,588-byte
0.3.16 application through InfiniTime recovery in 3 minutes 7 seconds. The
watch reported successful firmware validation and the host sent activation/reset.
The reset acknowledgment was unavailable; no retry was attempted. The user
confirmed successful boot and tapped KEEP on 2026-10-09. The confirmed installed
firmware is now 0.3.16. This does not yet verify the Chrome/Linux in-app update
path or phone pairing on 0.3.16.

Package: `build/ota/release-0.3.16/gopine-dfu-0.3.16.zip`; SHA-256:
`cdfe5dcd4d5faa71fad52dc8951f92b232161e3520c9cd0103bbb7b777e23674`.
Log: `build/ota/upload-0.3.16-20261009-225044-234692.log`.
Two earlier attempts failed before firmware was sent. A short LE discovery
confirmed the recovery identity and Legacy DFU service before the successful
connection; goPine's **Ready to connect** screen is the separate in-app service.

### Local 0.3.17 — recovery validated; boot/KEEP confirmed

On 2026-10-10, the TP-Link adapter on hci1 sent the 279,724-byte local 0.3.17
application through InfiniTime recovery in 3 minutes 13 seconds. The receiver
reported successful firmware validation and the uploader sent activation/reset.
The reset acknowledgment was unavailable; no retry was attempted.
The user confirmed successful boot and tapped KEEP on 2026-10-10, establishing
0.3.17 as the installed version. They subsequently reported a crash before the
pairing code appeared on the first attempt and incomplete pairing after code
entry on the second. Phone pairing, automatic time, weather delivery,
reconnect behavior, and battery consumption remain hardware checks.

Package: `build/ota/gopine-dfu-0.3.17.zip`; SHA-256:
`078ce945e7b78d304f0849735616b2a9824e825b7c805865961c566695028270`.
Log: `build/ota/upload-0.3.17-20261010-001042-172389.log`.
Earlier direct-mode scans sent no firmware. After the user restarted the watch,
BlueZ identified its separate recovery address and Legacy DFU service; the
uploader verified that service again before sending the package.

### Local 0.3.18 — recovery validated; boot/KEEP confirmed

The local 285,476-byte 0.3.18 candidate adds an ANCS client for iPhone alerts,
puts pairing only in Settings → Phone, and defers Bluetooth host callbacks out
of rendering and feature packet frames. Race tests, vet, eleven C sanitizer
suites, resource checks, rendering checks, and the full BLE OTA build passed.
Offline package validation passed. On 2026-10-10, the TP-Link adapter on hci1
transferred all 285,476 bytes through InfiniTime recovery in 3 minutes 15 seconds.
Receiver firmware validation succeeded and activation/reset was sent. The reset
acknowledgment was unavailable; no retry followed activation. The user confirmed
successful boot and KEEP, establishing 0.3.18 as the installed version.
Log: `build/ota/upload-0.3.18-20261010-010754-272165.log`.
A preceding recovery connection timed out before sending firmware; restarting
recovery allowed the successful attempt above.
Log: `build/ota/upload-0.3.18-20261010-010550-759108.log`.
The direct upload attempt on 2026-10-10 used the earlier 285,428-byte candidate
(SHA-256 `e1ad3d624a606e668e9bd4f41a25b196f2153ce805f1b1a4e5ebf750da27961b`).
It disconnected repeatedly during service
discovery and sent no firmware. The user confirmed that the watch rebooted when
the uploader connected. That attempt stopped; recovery is needed to install the
candidate without using the installed application's failing connection path.
The user then requested fixing the problem before flashing. Investigation
reproduced invalid GATT state on the second host start. The rebuilt candidate
registers its fixed service database once and preserves it across host restarts; its
real-host sanitizer test passes 100 discovery cycles and 10 phone passkey/update
handoffs. The firmware updater's nested full-host pump was also removed.
Log: `build/ota/upload-0.3.18-20261010-003848-546655.log`.
See [candidate notes and hardware acceptance](releases/0.3.18.md).

Rebuilt package: `build/ota/gopine-dfu-0.3.18.zip` (214,055 bytes); SHA-256:
`b933f263acfac64ff2cfe116b9941829e813d100caa33ae0fab9ccdd8454fef5`.

After boot/KEEP, the user reported stalled pairing, a short-lived code/iPhone
prompt after clearing saved pairing, and quick sleep in Phone setup. The local
0.3.19 follow-up keeps setup awake for two minutes and defers settings-save
disconnects before and during security negotiation. Race tests, vet, the BLE
firmware build and resource gate pass. The first 0.3.19 attempt incorrectly used
recovery while the watch was already on goPine's Ready to connect screen. It
timed out before any firmware was sent. After the user clarified the mode, the
in-app uploader transferred all 285,916 bytes and the watch verified the image.
The user confirmed INSTALL, successful boot and KEEP on 2026-10-10, establishing
0.3.19 as the installed baseline. Direct transfer log:
`build/ota/upload-0.3.19-20261010-012727-648045.log`.
The unnecessary recovery attempt is recorded in:
Log: `build/ota/upload-0.3.19-20261010-012610-244656.log`.
Package: `build/ota/gopine-dfu-0.3.19.zip` (214,325 bytes); image 285,916 bytes;
SHA-256: `be1884213f30830e26bd1f5849beab787d052208058b239a6fbd2c72d9291606`.
See [0.3.19 validation and remaining hardware checks](releases/0.3.19.md).
AGENTS.md records that update requests mean goPine is already ready, recovery is
only a fallback, and no routine boot/KEEP confirmation question should follow.

The user subsequently reported that 0.3.19 still slept quickly during setup and
lost the pairing code/iPhone prompt. The prepared 0.3.20 candidate fixes a
reproduced missing handoff from queued Bluetooth host events to the main loop
and renews Phone setup visibility after waking. It also services controller work
during key calculation and retains Phone INFO failure details. The latest build
also restores controller phone identity keys and address resolution after reset;
real-host tests cover eight private-address
reconnects and a rejected controller restore without extra flash writes.
It also enables the GATT client operations accidentally disabled with the central
radio role, allowing the ANCS client to discover and subscribe to phone services.
Real host/GATT notification lifecycle tests pass; physical iOS remains untested.
Package: `build/ota/gopine-dfu-0.3.20.zip` (218,087 bytes); image
291,252 bytes; SHA-256:
`4aed1febc7350dc9fd2c3611f0ffeebbe3395afe8c4766823d59548da7dd1692`.
See [0.3.20 tests and hardware limits](releases/0.3.20.md). The installed baseline
last confirmed with boot/KEEP remains 0.3.19. No compression or new transport is
included in this follow-up.

On 2026-10-10, a bounded TP-Link scan found the configured watch advertising
goPine Update. The prepared direct uploader then sent all 291,252 bytes of
0.3.20 on `hci1`; the watch verified the complete image and offered INSTALL.
Log: `build/ota/upload-0.3.20-20261010-023723-130669.log`. Approximately 6 minutes
19 seconds elapsed from log creation through receiver validation, with no
transfer retry shown. This is a measurement of the 0.3.19 receiver; the new
receiver's speed is not measured yet. The user was instructed to tap INSTALL,
then KEEP. Installation, boot/KEEP and InfiniLink pairing have not been reported;
receiver validation does not prove those outcomes. No routine confirmation
question follows the update, as requested in AGENTS.md.

<!-- recovery-reference -->
## Recovery reference: red, green and blue

These are **pinecone colors during boot**, selected with the **physical side
button**. They apply to the stock InfiniTime MCUboot bootloader (including
1.0.1 used here), not the on-screen goPine update button.

| Color | How to select it | What it does |
| --- | --- | --- |
| **Green — normal boot** | Leave the side button released while the pinecone fills. | Runs the installed application, applying a pending update or automatic trial rollback if needed. |
| **Blue — rollback** | Hold during the pinecone animation; release when it turns blue, before red. | Requests the firmware in the previous/secondary slot, even after KEEP. |
| **Red — recovery** | Keep holding past blue; release when the pinecone turns red. | Restores the stored InfiniTime recovery firmware for a new Bluetooth OTA upload. |

To reach this screen from a running or stuck application, hold the side button
until the watch restarts. For blue/red, keep holding into the pinecone animation,
then release at the desired color. For normal boot, release as it restarts.
Follow the colors rather than counting seconds. After red, allow the restore,
possible backlight flashes and automatic restart to finish with the button
released. Do not interrupt an active restore or firmware transfer.

**Blue is not a guaranteed way back to the previous goPine.** It can only use
what remains in the secondary slot. In-app updates retain the preceding goPine
as the trial fallback until that slot is reused. Red recovery and new uploads
reuse the slot; overwritten firmware cannot be recovered with blue. The blue
gesture is documented upstream but has not been validated as a cancel path here.

If the watch already shows **InfiniTime recovery**, use a separate compatible
DFU tool and an official InfiniTime package, or the wired setup below. goPine's
native protocol-2 ZIP and maintained uploader cannot connect to this clock.
Recovery has no on-screen Cancel/Back. Do not repeat activation after successful
receiver validation solely because a reset acknowledgement was missing.

If the screen stays blank, charge the watch and try the side-button restart.
If red cannot start recovery, Bluetooth OTA alone may not repair it; see the
repository's [wired setup instructions](https://github.com/Distortions81/goPineTime/blob/main/docs/ota.md#one-time-wired-setup-standalone-gopine-to-mcuboot).

Sources: [stock bootloader 1.0.1 boot flow](https://github.com/InfiniTimeOrg/pinetime-mcuboot-bootloader/blob/1.0.1/README.md#boot-flow)
and [button/restore implementation](https://github.com/InfiniTimeOrg/pinetime-mcuboot-bootloader/blob/1.0.1/libs/pinetime_boot/src/pinetime_boot.c).
