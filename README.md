# goPine

goPine is a digital clock with alarms, stopwatch, and countdown for the PineTime smartwatch, written in Go
with [TinyGo](https://tinygo.org/). It also includes an SDL2 desktop simulator
that uses the same clock and font rendering code as the watch build.

The latest public release is **0.3.34**. It fixes incomplete Bluetooth shutdown:
the encryption/address-resolution peripheral is now explicitly disabled when
Bluetooth is turned off. The local 0.3.34 build was installed and confirmed with
KEEP on 2026-10-16. Its effect on battery runtime still needs measurement.

This release includes the intervening candidates' saved Bluetooth preference,
time/weather/music integration, support for two paired devices, Apple
notifications, faster protocol-2 firmware transfers, and memory reductions.
The interface groups tools and messages under **Apps**, with a persistent battery
indicator, light/dark themes, three brightness levels, touch wake and rotation
under **Settings → Display**. The Weather app button is currently disabled.
Phone connections are opt-in. Notification delivery has been confirmed on the
watch; full physical InfiniLink completion and reconnect reliability still need
validation.

Use the [browser updater](https://distortions81.github.io/goPineTime/) with a BLE
build of goPine **0.3.22 or newer** already installed, or download the application
ZIP from [GitHub Releases](https://github.com/Distortions81/goPineTime/releases/latest).
Older firmware needs a one-time transition using its original in-app sender,
or a wired installation. Native protocol-2 packages cannot be installed through
InfiniTime recovery.

See the [0.3.34 release notes](docs/releases/0.3.34.md),
[power investigation](docs/power.md#bluetooth-off-investigation-2026-10-16),
[update instructions](docs/ota.md), and [roadmap](docs/roadmap.md) for validation
limits and setup details. Earlier candidate histories remain in
[release notes](docs/releases/).

## Desktop simulator

The application can run on Linux using the SDL2 desktop simulator:

```sh
go run .
```

![goPine running in the desktop simulator](gopine-simulator.png)

The simulator opens a 240 by 240 window and runs the same clock application
used by the watch build. Space simulates the side button (sleep/wake); an alert
wakes the window and shows vibration pulses in its title. Set
`GOPINE_SIM_STORAGE=/path/to/watch.flash` to preserve settings and timers across
simulator restarts. The optional file uses the watch's flash-journal format.

## Requirements

- Go 1.27.1 or newer in the Go 1.27 release line
- TinyGo 0.42.0
- SDL2 development libraries (desktop simulator only)
- LLVM command-line tools (`llvm-nm`, `llvm-dwarfdump`, `llvm-objdump`) for the
  BLE build's resource-budget check

On Debian or Ubuntu, install SDL2 with:

```sh
sudo apt install libsdl2-dev
```

## PineTime firmware

Use the [browser updater](https://distortions81.github.io/goPineTime/) to send
firmware directly over Bluetooth from a supported browser. It works with goPine
0.3.22 or newer already on the watch, validates the firmware ZIP, resumes interrupted
transfers, and waits for on-watch **INSTALL** and **KEEP**. See the
[browser updater guide](docs/web-updater.md) for compatibility and local builds.

For **Bluetooth OTA with on-watch confirmation**, see [OTA setup](docs/ota.md).
Download the firmware ZIP from [GitHub Releases](https://github.com/Distortions81/goPineTime/releases)
to update without compiling. Releases are built and checked automatically from
version tags; [release instructions](docs/ota.md#automated-tagged-releases) describe publishing.
Once the watch runs 0.3.22 or later, use
`bash scripts/ota-update.sh VERSION --web` for local browser controls
and updates inside goPine. The updater prepares and checks the package first;
the watch shows progress and waits for **Install**, then **KEEP** after reboot.
Native OTA protocol 2 acknowledges durable flash bytes through notifications
and allows payloads up to 236 bytes at MTU 247. Legacy recovery upload tools,
protocol-1 compatibility and Nordic DFU packages are removed. Emergency recovery
and trial rollback remain in the bootloader. The first transition from 0.3.21
uses the preserved local in-app sender; subsequent updates use protocol 2.
See the [0.3.22 notes](docs/releases/0.3.22.md). A one-time wired setup is needed
when migrating from the standalone build below.

Build a **standalone, wired-only** image with the project's PineTime target. The
linker value seeds the watch's low-power RTC with the current local time:

```sh
tinygo build -target=./targets/pinetime-gopine.json -ldflags="-X main.firmwareTime=$(date +%H:%M:%S) -X main.firmwareDate=$(date +%Y-%m-%d)" -o goPine.hex .
```

Flash it with the programmer configured for your PineTime development setup:

```sh
tinygo flash -target=./targets/pinetime-gopine.json -ldflags="-X main.firmwareTime=$(date +%H:%M:%S) -X main.firmwareDate=$(date +%Y-%m-%d)" .
```

**Do not use this standalone target after installing MCUboot:** it starts at
address zero and overwrites the bootloader. Use `scripts/build-ota.sh` instead.

The PineTime's 32.768 kHz RTC keeps time while the CPU sleeps, but it is not a
battery-backed calendar clock and resets when the watch reboots. The OTA build
seeds local date/time (use `TZ=America/Denver bash scripts/build-ota.sh 0.3.18`,
or your own timezone). Set the correct time after flashing in **Settings →
Time & Date**. Planned goPine OTA/revert resets now hand off the date/hour in
flash and minutes/seconds in retention registers, with no periodic flash writes.
Restored time is approximate: reboot/recovery time is not counted. Unexpected
resets or power loss fall back to the build seed. With Bluetooth enabled under
**Settings → Bluetooth**, the watch accepts valid time updates automatically from the paired,
authenticated, encrypted phone connection. The companion must send a fresh local
time to reflect timezone or DST changes. Manual Time & Date controls remain
available. The former standalone time proposal flow remains in the transport
and tests but is no longer exposed by the normal UI. The 0.3.18 candidate adds
the ANCS client needed for InfiniLink's normal connection mode; allow notification
sharing when iOS asks. See [pairing and validation](docs/pairing.md).
Automatic phone time and the planned-reset handoff need dedicated hardware tests.

The watch face uses large, high-contrast time with 12-hour time by default.
A battery gauge and readable percentage show estimated remaining charge;
the gauge contains a lightning bolt only while charging. Low battery is amber,
and the icon changes color with external power (external power alone does not
prove the battery is full). The clock omits redundant power-status text and
gesture hints; a small AM/PM sits beside the time on the same baseline.
The estimate still comes from
battery voltage, not a calibrated fuel gauge. The display and backlight turn off after
15 seconds. Every screen shows a compact percentage and battery icon at the top
right, with a bolt during active charging. Settings has no battery footer.
**Settings → Display** offers **Theme: Dark/Light**, **Backlight:
High/Low/Medium**, **Touch wake: Off/On**, and **Rotation: Normal/Flipped**.
Changing brightness or enabling touch wake first shows battery-use information
with **Cancel / Confirm**. Turning touch wake off is immediate. Existing settings
default to Dark and High.
**Touch wake** defaults to **Off** to save battery;
the touch controller sleeps with the screen. Press the side button to wake the
screen or turn it off immediately; a long press still allows a bootloader
watchdog reset. Enable Touch to wake to also wake with a screen tap. Touch works
normally while the screen is on and extends its timeout. Alarms and countdowns
wake the screen with either setting. The choice persists alongside other settings
in MCUboot builds and simulators configured with storage. See the
[sleep power audit](docs/power.md) for remaining background activity and hardware checks.

The watch wakes on charger connection, disconnection, and charging
start/stop, showing a full-screen battery gauge and large percentage. Tap to
return to the previous screen, or let the normal 15-second timeout turn the
screen off. Alarms and update/sync screens retain priority. Detection uses GPIO
interrupts with a brief debounce; it adds no periodic battery polling during
sleep. ETA is omitted until a charging model can provide a credible estimate.
In the desktop simulator, **C** cycles unplugged/charging/powered states.

Swipe **left** on the clock to open Settings; swipe **right** or use Back to
return. **Time & Date** includes **Set Time**, **Set Date**, and a 12/24-hour
toggle. Tap +/− to adjust fields; Save applies them, while Cancel, swipe-right,
Back, or sleeping discards the draft. Setting time resets seconds to zero;
setting date preserves the current time of day. Years 2000–2099 are supported.
Time/date adjustments participate in the planned-reset handoff. The 12/24-hour
choice is now saved along with alarms and timers after a short edit delay.
Time changes do not change sleep, touch-hold, or update-expiry timers.
See [time and sync notes](docs/time.md) for limitations and the PC/phone plan.

Swipe **right** from the clock to open **Apps**: Alarms, Stopwatch, Timer, Music,
Messages, and a visibly disabled Weather button. Stopwatch supports lap,
pause/resume, and reset. Countdown supports durations up to 23:59:59, pause/resume,
and reset. Five alarms support once, daily, or weekdays. Alerts wake the display,
pulse the vibration motor, and offer dismissal or a five-minute alarm snooze.
The side button dismisses an alert; alerts stop after one minute.

Settings and timer state persist in MCUboot builds. Running timers use saved
timestamps and the best available reboot clock; a later accepted time sync
reconciles restored operations that have not been changed. An explicit clock
`initialized` flag distinguishes an approximate calendar from an unset clock.
Without a usable calendar, duration timers resume from saved durations and
calendar alarms wait for time/date setup. See [clock tools and persistence](docs/timers.md)
for controls, reboot behavior, storage migration, and tests.

Settings also includes **Firmware Update**. In BLE builds from 0.3.13 onward,
press and hold for three seconds to open **Ready to connect** inside goPine.
The screen stays on, shows transfer progress, and offers Cancel throughout.
After the watch verifies the image, tap **Install**, then **KEEP** after reboot.
Brief Bluetooth disconnects can resume within the same transfer. The session
expires after ten minutes without transfer activity; Cancel ends it
with the current firmware still running. At least 20% battery or external power
is required. Normal phone connections pause when this update mode opens; the
candidate resumes enabled auto-connect after leaving the update screens. The
0.3.27 candidate reserves three live Bluetooth slots in update mode: both saved
devices can reconnect while the updater uses the third. Status subscriptions do
not claim a transfer; the sender that starts or resumes it owns the writes.
From candidate 0.3.31, update mode suspends notifications, music/weather work,
alarm delivery, vibration, pairing overlays, and settings saves. Bluetooth
transfer handling, power checks, watchdog servicing, and Cancel remain active.
After leaving update mode, normal services resume and overdue timers can ring.
Versions through 0.3.12 opened InfiniTime recovery after the hold.

An unconfirmed OTA build shows **KEEP / REVERT** instead. The simulator supports
mouse clicks, horizontal drags, and press-and-hold; run `GOPINE_SIM_TRIAL=1 go run .`
to exercise the confirmation UI. KEEP returns to a clock with no update button.

Rendering uses one reusable 240×8 RGB444 strip (2,880 bytes), batching text and
buttons into at most 30 bitmap transfers for a full frame. Unchanged strips
are skipped; hold feedback updates at 10Hz without clearing the whole screen.
No full-screen framebuffer is needed. The 0.2.3 interface has been installed
over Bluetooth and visually checked on the watch; hold timing and redraw
responsiveness still need focused hardware checks.

Version 0.2.4 uses a true-black background, dark-charcoal
cards, and native-size antialiased DejaVu Sans text. Four-bit glyph coverage is
blended against the actual background. Version 0.3.0 replaces tiny metadata
labels with readable 18px text, including editor units and clock warnings.
Font data is kept in immutable tables, with no runtime TTF parser
or full-screen smoothing. See [font generation and licensing](internal/uifont/README.md).
The small `internal/gfx` package provides crisp filled/outlined boxes plus
antialiased lines, circles, discs, and filled rounded boxes, all clipped to the
current strip. The later optimization pass halves the strip buffer and adds
lossless font compression; see [performance measurements](docs/performance.md).
The original graphics revision, including
the touch-hold repair, was installed over Bluetooth on 2026-10-05; the user
reported everything appeared to work. See [OTA validation notes](docs/ota.md)
for the test scope and remaining dedicated hardware checks.

## Phone features

Open **Settings → Bluetooth**, tap **Turn on**, and confirm its battery-use
explanation. When Bluetooth is enabled,
the same button becomes **Pair a device** and renews the pairing window without
interrupting an existing connection. **Paired devices** opens saved-pairing
information. Pairing has one home in Settings; feature screens use the connection
automatically. Up to two saved devices can supply time, weather, music metadata,
and notifications through their authenticated, encrypted connections.
Incoming phone writes require the saved bond and a full
16-byte encryption key. The auto-connect preference survives a confirmed reboot;
new installations and older settings default to Off. **Turn off** disables
automatic reconnection and retains the bond. The phone app controls whether and
when it reconnects; the watch cannot force a suspended app to run.

Connections survive screen sleep and navigation between apps. When changed
settings need an internal flash save, the candidate briefly pauses the phone
connection, waits for radio shutdown to finish, saves, and resumes auto-connect.
Reconnect advertising retains the existing slow 1–1.5-second intervals after its
initial fast window. The unused motion and heart-rate sensors remain powered
down; no sensor polling has been added for weather.

**Apps → Weather** is temporarily disabled and does not open a screen when tapped.
The Bluetooth weather receiver and cache remain available; supported companion
packets still update them. InfiniLink's newer development branch can
push weather on foreground connection and location/app refresh; classic 1.0.2
has no weather sender. The newer app's periodic background trigger depends
on motion notifications that goPine does not send. Unmodified Gadgetbridge's
firmware-version gate currently prevents weather delivery to goPine 0.3.x.

**Apps → Music** provides track/artist text, play/pause, previous/next, and volume
controls. Controls require a companion subscription; play/pause also requires
reported playback state. Tapping track text sends a metadata refresh hint that
InfiniLink accepts and Gadgetbridge ignores. InfiniLink targets Apple Music and
requires its music/volume permissions. Android notification forwarding uses the
ANS inbox, opened through **Apps → Messages**. iPhone notifications arrive through
ANCS into the same inbox, including updates and removals. It preserves silent/pre-existing
notification flags and clears Apple session data when disconnecting. Watch-side
Dismiss affects the local inbox; remote notification actions and system-wide
iPhone media control are not implemented.
The inbox's **BUZZ ON / QUIET** control mutes immediately. Re-enabling buzz
requires confirmation explaining motor use; Quiet still receives messages over
Bluetooth. No battery-life percentage or duration is promised without measurement.
See [phone setup, companion limits, and hardware checks](docs/pairing.md).

## Verification

```sh
go test ./...
go vet ./...
go test -run 'TestScript|TestRuntime|TestUnsetClock|TestTimersCreatedBefore' .
tinygo build -target=./targets/pinetime-gopine.json -ldflags="-X main.firmwareTime=00:00:00" -o /tmp/goPine.hex .
bash scripts/build-ota.sh --ble 0.3.18
```

The scripted tests advance a virtual clock through the production event loop,
including asleep deadlines, snooze, radio cancellation, and interrupted updater
holds. They do not wait for real minutes to pass. Set `GOPINE_TEST_SCREENSHOTS`
to a directory when running tests to export the SDL-rendered screens.

BLE OTA builds now check the linked ELF's task-stack size, heap region, and
selected call-path frame budgets before packaging. The JSON resource report is
saved beside the firmware. This rejects the oversized frames in the failed
0.3.0 trial; it is a regression guard, not a complete static stack proof or a
measurement of free runtime heap. See [resource audit](docs/timers.md#failed-030-hardware-trial-and-resource-audit).

```sh
python3 scripts/test_check_resources.py
python3 scripts/check-resources.py build/ota/gopine-0.3.18.elf
```

## Recovery: red, green and blue

The stock InfiniTime bootloader uses pinecone colors and the **physical side
button** to choose what boots:

| Color | Button action during the pinecone animation | Result |
| --- | --- | --- |
| **Green** | Leave the button released. | Normal boot, including any pending update or automatic trial rollback. |
| **Blue** | Hold, then release at blue before it turns red. | Request rollback to the firmware still in the secondary slot. |
| **Red** | Hold past blue, then release at red. | Restore InfiniTime recovery so you can upload firmware over Bluetooth. |

Hold the side button until the watch restarts to reach the bootloader. Keep
holding into the animation for blue/red, or release as it restarts for green.
After red, leave the button released and let restoration/restart finish.

**Blue cannot restore an overwritten goPine version.** With recovery-based OTA,
the fallback is normally recovery; red recovery and new uploads reuse the
secondary slot. Recovery has no on-screen Cancel/Back. Prepare the uploader
before entering recovery, connect promptly, and tap **KEEP** after goPine boots.

See the [full recovery reference](docs/ota.md#recovery-reference-red-green-and-blue)
for connection failures, blank screens, rollback limits, and upstream sources.
