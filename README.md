# goPine

goPine is a digital clock with alarms, stopwatch, and countdown for the PineTime smartwatch, written in Go
with [TinyGo](https://tinygo.org/). It also includes an SDL2 desktop simulator
that uses the same clock and font rendering code as the watch build.

The latest public release is **0.3.16**, which fixes a Linux Bluetooth pairing
conflict during firmware updates. The browser updater selects the latest release
automatically. Current source targets **0.3.28**, a candidate that brings time,
weather, music, and supported notifications together under **Settings → Phone**,
with a saved auto-connect preference and automatic time from the paired phone.
It also includes in-app firmware updates, charger wake screens, and a saved
flipped-screen option. Phone connections are opt-in. The candidate adds an ANCS
client for iPhone notifications; the user has confirmed notification delivery,
while completion of the InfiniLink connection remains unverified.
The [0.3.19 follow-up](docs/releases/0.3.19.md) keeps phone setup awake for two
minutes and defers settings-save disconnects while pairing is in progress.
The [0.3.20 follow-up](docs/releases/0.3.20.md) fixes a missed wake signal for
queued Bluetooth host events and renews setup visibility after waking Phone.
It also services controller work during pairing's key calculation, following
InfiniTime's controller-priority behavior. Phone → INFO retains pairing errors.
It restores the controller's saved phone identity keys after reset, following
InfiniTime's bond restore, so private-address changes preserve the pairing.
It also enables the GATT client operations required for ANCS; disabling the BLE
central radio role had inadvertently disabled those operations as well.
The full simulated Secure Connections exchange and saved-bond reconnect now
pass; these checks do not establish a working physical InfiniLink connection.
The watch received and verified 0.3.20 through the in-app updater on 2026-10-10;
INSTALL and boot/KEEP have not been reported. The user subsequently reported
entering the pairing code without completion; INFO showed **No saved phone**.
Physical pairing remains unresolved. The [0.3.21 follow-up](docs/releases/0.3.21.md)
accepts authenticated feature writes while the final identity exchange is
pending, preventing a redundant ATT authentication error. Their data stays
queued until bonding completes and is cleared if security fails. It also
shows the pairing stage and completed code-check rounds on the PIN screen,
and retains the last stage in INFO after failure. The watch received and verified
0.3.21 through the in-app updater on 2026-10-10; INSTALL and boot/KEEP have not
been reported.

The [0.3.23 follow-up](docs/releases/0.3.23.md) corrects slow advertising
on the first failed phone connection and renews the fast window from CONNECT
PHONE without interrupting pairing. First pairing now follows the reference
central-triggered flow. A non-secret retained Bluetooth trace and a physical
pairing probe support diagnosis. The watch received and verified 0.3.23 through
the in-app updater on 2026-10-10. Physical probes could not discover the watch in
Phone mode; physical InfiniLink bonding remains unresolved.

The [0.3.24 candidate](docs/releases/0.3.24.md) adds an
[automated pairing harness](docs/pairing-harness.md), prevents unusable bonds from
occupying the phone slot, services Bluetooth during hardware settling delays,
and repairs ANCS retry wakeups. A retained physical trace shows authentication
failures; full physical InfiniLink pairing remains unverified. The watch received
and verified 0.3.24 through the in-app updater on 2026-10-10; INSTALL, boot and
KEEP have not been reported.

The [0.3.25 candidate](docs/releases/0.3.25.md) fixes a reproduced failure to
advertise after restarting Bluetooth with a saved phone: duplicate controller
identity entries were rejected by hardware but incorrectly accepted by the
test harness. It also fixes the bond-save wakeup and adds early-failure traces.
The watch received and verified 0.3.25 through the in-app updater on 2026-10-10;
INSTALL, boot and KEEP have not been reported. Physical InfiniLink completion
remains unverified.

The [0.3.26 candidate](docs/releases/0.3.26.md) restores InfiniTime's immediate
Battery read, which InfiniLink's development branch uses to finish its connection screen. The user
has confirmed notification delivery following 0.3.25; InfiniLink itself still
shows Connecting. A regression now follows the app's single initial read and
fails with goPine's previous Battery encryption gate. The watch received and
verified 0.3.26 through the in-app updater on 2026-10-10 with its saved phone
pairing retained. INSTALL, boot and KEEP have not been reported.

The user's **Home, Charts, Settings** interface instead matches classic
InfiniLink 1.0.2, which waits for a nonempty firmware revision. The
[0.3.27 candidate](docs/releases/0.3.27.md) adds the missing Device Information
service with the actual goPine version. Physical InfiniLink completion remains
unverified; working Bluetooth alone does not establish that app condition.

The [0.3.28 candidate](docs/releases/0.3.28.md) adds a negotiated two-packet OTA
pipeline, writes without response where supported, and shorter upload connection
intervals. Both senders retain compatibility with older protocol-2 receivers and
report transfer timing. The faster receiver must be installed before its speed
can be measured on a subsequent transfer.

Use the [browser updater](https://distortions81.github.io/goPineTime/) with goPine
0.3.22 or newer already installed, or download the application ZIP from
[GitHub Releases](https://github.com/Distortions81/goPineTime/releases/latest).
See the [0.3.16 release notes](docs/releases/0.3.16.md),
[0.3.17 candidate notes](docs/releases/0.3.17.md), and [roadmap](docs/roadmap.md)
for changes and validation limits. **0.3.19 booted successfully and was confirmed
with KEEP on 2026-10-10. The preceding 0.3.17 crashed before showing a pairing
code on one attempt and did not complete after code entry on another.** The candidate's phone
compatibility, reconnect behavior, and battery consumption require hardware tests.
On 0.3.18, the user reported a setup screen that slept quickly and a pairing
code/iPhone prompt that disappeared before entry. Clearing saved pairing keys
allowed a fresh code but did not establish a successful connection.
On 0.3.19, setup still slept quickly after waking it, and the pairing code and
iPhone prompt still disappeared. Phone pairing remains a hardware failure.
The [0.3.18 candidate](docs/releases/0.3.18.md) adds Apple notifications, makes
Settings → Phone the sole pairing entry, reduces Bluetooth callback nesting,
and fixes a reproduced GATT service-memory defect when Bluetooth restarts.

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
resets or power loss fall back to the build seed. In the 0.3.17 candidate,
**Settings → Phone** accepts valid time updates automatically from the paired,
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
15 seconds. **Settings → Display → Touch Wake** defaults to **Off** to save battery;
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

Swipe **right** from the clock to open **Apps**. Stopwatch supports lap,
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
expires after ten minutes without transfer activity; Cancel or an alarm ends it
with the current firmware still running. At least 20% battery or external power
is required. Normal phone connections pause when this update mode opens; the
candidate resumes enabled auto-connect after leaving the update screens. The
0.3.27 candidate reserves three live Bluetooth slots in update mode: both saved
devices can reconnect while the updater uses the third. Status subscriptions do
not claim a transfer; the sender that starts or resumes it owns the writes.
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

In the **0.3.18 candidate**, open **Settings → Phone → CONNECT PHONE**, pair once,
and leave **AUTO CONNECT: ON**. Pairing has one home in Settings; feature screens
use the connection automatically.
Time, weather, music metadata, and notifications share one authenticated,
encrypted connection. Incoming phone writes require the saved bond and a full
16-byte encryption key. The auto-connect preference survives a confirmed reboot;
new installations and older settings default to Off. **BLUETOOTH OFF** disables
automatic reconnection and retains the bond. The phone app controls whether and
when it reconnects; the watch cannot force a suspended app to run.

Connections survive screen sleep and navigation between apps. When changed
settings need an internal flash save, the candidate briefly pauses the phone
connection, waits for radio shutdown to finish, saves, and resumes auto-connect.
Reconnect advertising retains the existing slow 1–1.5-second intervals after its
initial fast window. The unused motion and heart-rate sensors remain powered
down; no sensor polling has been added for weather.

**Apps → Weather** shows current conditions and five forecast days received from
the phone, retaining them after disconnect until reboot. Tap **C/F** to change
units. Receipt age is displayed, with a warning after 24 hours; it does not claim
the age of the provider's observation. InfiniLink's newer development branch can
push weather on foreground connection and location/app refresh; classic 1.0.2
has no weather sender. The newer app's periodic background trigger depends
on motion notifications that goPine does not send. Unmodified Gadgetbridge's
firmware-version gate currently prevents weather delivery to goPine 0.3.x.

**Apps → Music** provides track/artist text, play/pause, previous/next, and volume
controls. Controls require a companion subscription; play/pause also requires
reported playback state. Tapping track text sends a metadata refresh hint that
InfiniLink accepts and Gadgetbridge ignores. InfiniLink targets Apple Music and
requires its music/volume permissions. Android notification forwarding uses the
ANS inbox. The 0.3.18 candidate receives iPhone notifications through ANCS into
the same Inbox, including updates and removals. It preserves silent/pre-existing
notification flags and clears Apple session data when disconnecting. Watch-side
Dismiss affects the local inbox; remote notification actions and system-wide
iPhone media control are not implemented.
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
