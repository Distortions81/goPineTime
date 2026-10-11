# Time setting and PC/phone synchronization

Current confirmed boot/KEEP baseline: **0.3.14**. A local **0.3.15** image
completed direct transfer and watch verification; boot/KEEP remains unconfirmed.
Version **0.3.15** adds secure pairing, persistent bond storage and a connected
shutdown fix. See [InfiniLink pairing and hardware acceptance](pairing.md).
Versioned sections below describe their original increments.

In the current interface, pairing lives under **Settings → Bluetooth**. Tap
**Turn on**, or **Pair a device** when already enabled. Time arrives automatically
over an authenticated connection; manual controls remain under **Time & Date**.
Music and Messages are in **Apps**. Weather is temporarily disabled in Apps,
while its Bluetooth receiver remains available.

## Available in version 0.2.4

Swipe left from the clock, open Time & Date, then choose Set Time or Set Date.
Large +/− controls edit a draft. Save applies it; Cancel, Back, swipe-right, or
sleeping discards it. Hours wrap through midnight/noon and update AM/PM; minutes
wrap independently. Saving time uses the current date and sets seconds to zero.
Date editing supports 2000–2099, clamps invalid days when changing month/year,
and preserves the current time of day on Save. Format toggles 12/24-hour display.

The application clock is an offset from the runtime clock. Manual changes do
not shift touch-hold durations, sleep, battery polling, or updater deadlines.
The simulator only adjusts its own watch clock, never the computer's clock.
The same clock setter can later accept validated Bluetooth time updates.

There are no periodic clock writes. Version 0.3.0 persists user settings and
timer operations after a short edit delay; see [clock tools](timers.md).
Software-controlled OTA/revert resets now have a compact, one-shot time handoff
(below). Cold starts, invalid handoffs, and unexpected resets use the build seed.
There is no timezone database or automatic DST handling. The handoff does not
measure reboot/recovery time; accurate time still needs setting or synchronization.

### Planned reboot/update handoff

The MCUboot build saves a clock handoff when goPine is about to perform a software reset:
after successful recovery staging for OTA, or after waking external flash for
Revert, immediately before `arm.SystemReset()`. Failed/canceled staging, clock
edits, normal running, sleep, and KEEP do not create a clock handoff. Settings
and timer snapshots have their own change-triggered saves; there is no hourly timer.

- The clock anchor stores integer **hours since 2000-01-01**, using 20 bits
  for 2000–2099. Version 0.3.0's 160-byte journal snapshots also hold settings,
  timestamped timers, validity flags, sequence, CRC32, and a final commit word.
  Saving an unchanged hour/format reuses the latest snapshot. Timer timestamps
  are used to estimate elapsed durations, not to invent a running calendar after
  an unplanned reset.
- `POWER.GPREGRET` and `GPREGRET2` provide two retained bytes. Twelve bits hold
  seconds within the hour (0–3599); two hold the flash sequence modulo four;
  one is parity and one is a validity/commit flag. Writes invalidate the flag
  first and set it last. Clear the registers before any flash save, so an
  interrupted transaction cannot pair old seconds with a new hour.
- On boot, both parts must validate and their sequence tags must match. Restore
  the saved local calendar and show **Set or sync time**. Consume the
  register handoff immediately, so a subsequent unplanned reset cannot reuse
  stale time. A missing/corrupt handoff does not restore an old flash-only hour.

Only the fraction of a second is intentionally discarded. Time spent writing
the handoff, rebooting, swapping images, or waiting in Bluetooth recovery is
unmeasured and is also lost. Retention registers store values; they do not count
elapsed time. A long update therefore still needs manual/Bluetooth resync.
Power loss clears the register half; a cold boot uses the build seed. A hard
fault, watchdog/long-button reset, or debugger reset bypassing the callback has
no fresh handoff. Older/different firmware need not understand or preserve it.

The journal uses two 4 KiB internal pages at **0x7e000–0x7ffff**, outside the
primary image/trailer, scratch (0x7c000–0x7cfff), and spare page (0x7d000).
Each page has a 16-byte ownership header and 25 records. Starting erased, the
two pages accept 50 different snapshots before the first erase; later rotation
erases only the older page, never the newest committed record. Unknown storage
without a recognized header is left untouched. A partially programmed initial
header that cannot be recognized also fails closed. A failed/uncertain write
stops further writes through that journal instance until reboot reopens it.
The old 16-byte clock-only format is read and migrated on the first save. The
new snapshot is committed before retiring old headers. Older goPine builds
cannot read the new format and fall back to their build seed on downgrade.
Reducing erases matters more than the number of zero bits: hardware still
programs aligned 32-bit words and erases whole pages.

New flash writes require external power or at least 20% estimated charge.
Reusing an existing hour/format needs only the registers and works without a
flash write. Invalid dates or storage errors leave no valid handoff; clock
storage is best-effort and never blocks OTA/rollback. No persistence backend is
enabled in standalone/provisioning builds (their image can use all flash).
Desktop persistence is opt-in with `GOPINE_SIM_STORAGE=/path/to/watch.flash`.

The clock has separate `initialized` and `approximate` flags. A valid full
build date/time or planned-reset handoff initializes an approximate calendar;
manual setting or accepted sync establishes the supplied calendar. With no
valid date (such as the default 1970 seed), `initialized` is false: calendar
alarms remain unarmed, no calendar handoff is written, and restored duration
timers use saved durations instead of subtracting timestamps from epoch zero.
Original timer timestamps are retained for reconciliation after time setup.

Tests cover every retained second/tag, single-bit corruption, invalidation and
commit order, flash cuts between word writes, page rotation, foreign data,
sequence wrap, same-hour deduplication, timezone/calendar reconstruction,
low-power/error paths, and consuming the handoff exactly once. These are host
tests, not proof of behavior across the actual bootloader/recovery. The local
bootloader project, InfiniTime application, and TinyGo runtime sources inspected
do not use these registers, but dependency/firmware changes require a fresh
ownership audit. Clock-retention checks across on-watch reset and a full OTA
cycle are still pending, distinct from the successful 0.2.4 installation. No
bootloader or recovery image has been modified.

### Hardware constraints

The nRF52832 RTC is a low-frequency counter, not an independent calendar RTC.
It keeps running while the CPU sleeps but its peripheral state resets on system
reset. Neither TinyGo nor MCUboot supplies an always-running calendar service
under goPine. InfiniTime runs separately as recovery, not underneath goPine.
The current upstream InfiniTime DateTime constructor seeds January 1 of its
compile year; its Bluetooth Current Time Service can then accept the real time.

A saved timestamp can restore an approximate clock, but cannot measure the
interval spent rebooting, in recovery, or with no power. A retained-RAM snapshot
would have the same missing-interval problem, and requires a memory region
protected by both bootloader and application. The stock bootloader uses the
full 64 KiB RAM region and starts its stack at 0x20010000, so simply reserving
the top of RAM in goPine alone is not safe.

Sources:

- [Nordic RTC peripheral documentation](https://docs.nordicsemi.com/r/bundle/ps_nrf52832/page/rtc.html)
- [Nordic explanation of RTC reset behavior](https://devzone.nordicsemi.com/f/nordic-q-a/60552/rtc-registers-state-after-soft-reset-and-ota)
- [Nordic retention-register size discussion](https://devzone.nordicsemi.com/f/nordic-q-a/1935/definitive-information-on-gpregret-register)
- [InfiniTime MCUboot memory map](https://github.com/InfiniTimeOrg/InfiniTime/blob/main/gcc_nrf52-mcuboot.ld)
- [InfiniTime DateTime initialization](https://github.com/InfiniTimeOrg/InfiniTime/blob/main/src/components/datetime/DateTimeController.cpp)

`scripts/build-ota.sh` seeds local date/time using the host's timezone, or `TZ`.
`FIRMWARE_DATE=YYYY-MM-DD` and `FIRMWARE_TIME=HH:MM:SS` can override the seed.
The seed cannot account for build/upload delay; correct it on the watch afterward.
Direct TinyGo builds can supply `main.firmwareDate` and `main.firmwareTime`
through `-ldflags`. Omitting the date retains the legacy 1970-01-01 seed.

## Sync implementation status

`internal/timesync` now implements strict ten-byte Bluetooth Current Time
encoding/decoding plus a transport-independent confirmation session. It rejects
incomplete dates, invalid calendar fields, wrong weekday values, unsolicited
updates, overwritten pending proposals, and expired confirmations. Acceptance
adds elapsed runtime since receipt. Tests cover byte layout, local-time
semantics, rollover during confirmation, cancellation, expiry, and 32-bit integer
arithmetic. These are protocol/session tests, not radio interoperability tests.

The opt-in Bluetooth candidate implements the radio bridge, sync screen, and PC
sender. On 2026-10-05, the 0.2.5 image passed OTA validation and the user reached
Sync Time. The host detected its `InfiniTime` advertisement and CTS service at
`C9:9E:15:7A:69:B4` once, but subsequent discovery/direct connection attempts
failed even with a reported running countdown. The USB adapter still detected
other devices. After the user rebooted the watch, the automated PC sender
connected immediately on discovery and received a successful write response
for local time `2026-10-05T03:07:33-06:00`. The user confirmed that the sync worked.
This validates one PC-to-watch sync after reboot, not repeated windows,
InfiniLink interoperability, or radio-off power consumption.

Version 0.2.6 extends both UI and radio deadlines from one to five minutes using
one duration passed from Go to the C bridge. Source inspection also found that
the stop path released HFXO, while NimBLE's RF-management-disabled configuration
requested it only once at initialization. Each new sync window now explicitly
reacquires HFXO before starting the host. This matches the observed restart
failure but still requires a dedicated repeated-window check. This change is
included in the installed 0.3.2 build. Sleep current remains unmeasured.

The 0.2.7 compatibility work adds the standard Battery Service (`0x180F`) and live
Battery Level characteristic (`0x2A19`) during the sync window. InfiniLink reads
that characteristic before it changes its UI from Connecting to Connected. The
candidate still intentionally has no ANCS client, Bluetooth Security Manager,
bond store, notifications, or background connection. In InfiniLink, enable
Developer Mode and turn **Developer → Force ANCS** off before connecting. The
default-on option passes Apple's `CBConnectPeripheralOptionRequiresANCS` when
connecting and is incompatible with goPine's bounded unauthenticated sync mode.
This work is included in the installed 0.3.2 build and still needs an on-phone
test. See the [roadmap](roadmap.md) for the remaining hardware checks.

## Bluetooth candidate: explicit Sync Time window

Swipe left → Time & Date → Sync Time. Only this action initializes/starts NimBLE.
The screen reminds InfiniLink users to turn **Force ANCS** off, connect to
InfiniTime, and confirm the received time on the watch. InfiniLink is the iOS
companion, not an Android requirement; the PC sender is an alternative. The name
matches InfiniLink's discovery filter, not the firmware identity: this is still
goPine, and it exposes no Bluetooth DFU service while syncing. A distinct, stable
random address separates goPine's GATT cache from InfiniTime recovery (factory
address with the low bit toggled).

The five-minute window (0.2.6; one minute in 0.2.5) keeps the display awake; the side button still sleeps and
cancels it. A received proposal is validated and shown with ACCEPT/BACK. No
write changes the clock without a fresh on-screen tap. A second sender cannot
replace a pending proposal. Cancel, Back, swipe-right, sleep, expiry, errors,
and receipt of a proposal close the radio window. The stack allows 200ms for
the ATT response before teardown, then bounds graceful disconnect to 500ms
before resetting the controller to standby. No background advertising,
pairing, bonding, scan, or reconnect is enabled. The MCU RTC used for stack
bookkeeping is not a Bluetooth transmission; sleep-current verification remains
required. The receiver is unauthenticated: check the displayed time carefully.

The GATT services are Current Time `0x1805`, with readable/writable `0x2A2B`,
and Battery `0x180F`, with readable/notifiable `0x2A19`. The battery value is
sampled when the sync window opens; notifications are declared for InfiniLink
compatibility but are unnecessary during the bounded session and are not sent.
Its write response acknowledges receipt, **not user acceptance**. The strict
ten-byte decoder remains available. A narrow compatibility adapter also accepts
InfiniLink's 9/10-byte packets with Sunday-based weekday and ambiguous fractional
tail, deriving weekday and discarding those sub-seconds. The first seven date/time
bytes remain strictly validated (2000–2099). Standard CTS fractions are preserved.
Time spent queued or awaiting confirmation is added on acceptance. Timezone/DST
are copied as displayed local calendar time, not continuously managed.

Compatibility was checked against InfiniLink source commit
`60abe2d2c67aff67726855374385a499d9353a94`:
[discovery](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/BLEManager.swift),
[time encoding](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/SetTime.swift).
App Store/TestFlight versions still need testing on a real phone.

### PC alternative

Install `bleak` in a Python virtual environment, then open Sync Time on the watch:

```sh
python3 scripts/sync-time.py --scan --adapter hci1
python3 scripts/sync-time.py --address WATCH_ADDRESS --adapter hci1 --wait 300
```

The sender can run before opening Sync Time. It waits up to five minutes by
default, retries discovery/connection, and connects immediately using the
discovered device without a second scan. Time is sampled just before sending.
It sends once: an uncertain write asks you to check the watch instead of blindly
retrying. This host wait does not extend an already installed firmware's window.

Use the address printed by the scan, not the recovery address. Omit `--adapter`
on non-Linux hosts. The tool only writes to the explicitly selected device after
checking its CTS service. It does not toggle adapters or update firmware.
Check the watch and tap ACCEPT. Only one PC/phone should connect at a time.

## Phone weather candidate (0.3.8)

The local candidate adds **Clock Tools → Weather**, current temperature,
conditions and high/low values, plus a five-day forecast. Tap the C/F unit at
the top right to change units. Weather and unit selection stay in RAM until
reboot; receiving weather never writes the settings journal.

Tap **UPDATE** before connecting the companion. This opens a 60-second window
advertised as InfiniTime, with CTS, Battery Service and the
[Simple Weather Service](https://github.com/InfiniTimeOrg/InfiniTime/blob/main/doc/SimpleWeatherService.md).
It accepts current v0/v1 and forecast v0 values. Long ATT writes are assembled
by NimBLE; the bridge copies the complete mbuf chain into two bounded latest-
record mailboxes. Malformed records leave the previous good cache intact.
The watch closes the window after receiving both valid records, or on Done,
Back, sleep, alarm interruption, expiry or radio failure. A partial successful
update remains cached. The last write gets the existing 200 ms ACK grace before
radio shutdown. Cached browsing never starts the radio.

The screen remains awake only during this explicit update window. This is a
foreground integration, not background weather synchronization. Only one peer
can connect. Existing time sync remains separate: CTS writes often sent by a
companion before weather are acknowledged during Weather Update, but are not
applied to the clock. Use Sync Time and ACCEPT to set time.

For **InfiniLink**, keep Developer → Force ANCS off. Its development source
discovers the weather characteristic, fetches WeatherKit data, and writes
current/forecast values with response. Weather fetches are rate-limited and
require phone location/network access; the foreground Weather screen is useful
for diagnosing fetch failures. App Store/TestFlight behavior must be checked
on the installed version. For **Gadgetbridge**, configure a weather provider
and send its weather update while the window is open. Companion compatibility
is not confirmed until a real exchange succeeds on each platform.

InfiniLink's source sends UTC seconds even though the protocol describes local
seconds. The watch consequently displays **receipt age**, not observation age.
It marks data out of date after 24 hours since receipt. Forecast rows represent
the following five days; labels use the watch's confirmed local date at receipt.
An unset/approximate clock displays Day 1–5 instead. Set the watch date correctly
for named weekday/date labels. Sunrise/sunset fields are accepted but not shown.
Unsupported location characters become question marks with the current fonts.

Source audit: InfiniLink commit `60abe2d2c67aff67726855374385a499d9353a94`,
[weather writer](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/BLEWriteManager.swift)
and [weather controller](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/Utils/WeatherController.swift).
The application code was inspected for interoperability; it is not copied into
the firmware. No new companion protocol or cloud service is required.

Host coverage includes packet lengths/versions, signed temperatures, bounded
forecasts, malformed-data recovery, cancellation/expiry, alarm interruption,
unit changes, date rollover and strip pixel equivalence. The C mailbox passes
ASan/UBSan; Go parser fuzzing checked over three million inputs. Phone exchange,
long-write behavior over the physical radio and current after disconnect still
need device tests. Version 0.3.7 remains installed for the battery comparison.

Both standalone and BLE OTA builds pass. The controlled BLE build uses 213,408
linked flash bytes, 6,604 more than 0.3.7. Static RAM reservation remains 19,316
bytes and the heap region remains 46,220 bytes; these are linker budgets, not
measured runtime headroom. The selected weather-update stack path is 3,840 bytes,
within the existing 8,192-byte task stack and 2,048-byte deeper-call reserve.
Opening Weather creates one bounded cache; cached rendering adds no compiler-
reported heap allocations. The radio's runtime allocation peak needs measurement.

The local `build/ota/gopine-dfu-0.3.8.zip` contains a 213,484-byte MCUboot image.
ZIP SHA-256: `c9e0bee2f9672b67c29a1fb366efaa71440cc15c6dab1711ebd957272bc96d48`.
It has not been uploaded to the watch.

## Phone connections and music candidate (0.3.9)

The current candidate renames Clock Tools to **Apps** and adds **Music** beside
Weather. Open Music and tap **LINK** (or its connection status) to select:

- **Off:** default after every boot; stops the connection and clears media state.
- **Connect 10 Min:** an explicit session which survives screen sleep and alarms,
  then expires even while asleep. Selecting it again starts a fresh ten minutes.
- **Stay Connected:** stays enabled until Off, reboot, a radio error, or opening
  explicit Sync Time, Weather Update, or Firmware Update. Those operations take
  over the radio and do not automatically restore the phone connection afterward.

Leaving the Music screen does not stop an opted-in session. Opening the screen
alone never starts Bluetooth. Policy and media state are RAM-only. There is one
unauthenticated peer; this increment does not implement pairing or notifications.

The independently implemented GATT service uses InfiniTime's music service UUID
`00000000-78fc-48fe-8e23-433b3a1942d0`, event notifications at suffix `00000001`,
and status/artist/track/album/position/length/track-count/speed/repeat/shuffle
characteristics at suffixes 2–12. Strings are capped at 40 bytes (long values end
in `...`); numeric writes have exact lengths, with four-byte values retained in
big-endian wire order. Complete mbuf chains are read. Non-ASCII display bytes
become `?`. Position and repeated unchanged metadata do not request redraws.

Controls emit InfiniTime play (0), pause (1), next (3), previous (4), volume up
(5), volume down (6), and metadata refresh (0xe0) events. They require an active
subscription. Play/pause waits for actual playback status and does not change
optimistically after a command. Track/artist text clears on disconnect; each
connection has a generation number so a queued gesture cannot cross to another
peer. The command is attempted once, with errors shown on screen, and never
replayed after reconnection. Connection handle zero is accepted. NimBLE owns the
notification mbuf on both success and failure.

A refresh hint is sent once when the companion subscribes, and again when the
user returns from LINK to Music. Tap the track/artist area to request another
refresh if the companion had not completed discovery. InfiniLink needs its music
control permission, volume control setting, and Developer → Force ANCS off.
Its Apple Music integration does not imply system-wide media support. Android
Gadgetbridge and the installed iPhone app both still need actual transfer tests.
The Battery Service initially reports the battery sample captured when the
session starts. Candidate 0.3.12 updates it from the existing awake/wake samples
and notifies subscribers only when the percentage changes. It adds no asleep
ADC polling, and sleeping phones see the last sampled watch battery value.

Weather received during a phone connection updates the existing bounded cache;
that does not terminate the connection. CTS writes are transport-acknowledged
without changing the clock, as in the standalone weather session.

Bluetooth now follows queued events, host callout deadlines, bounded session
expiry and final-ACK grace instead of fixed 20 ms service sleeps. The idle path
checks queues again with interrupts masked immediately before WFE, so an ISR
which already queued work cannot be missed. Controller radio/RTC interrupts wake
the CPU earlier as needed. NimBLE's RF manager now owns HFXO between events using
the same 1,500 µs startup allowance as pinned InfiniTime. Advertising begins at
100–150 ms intervals, slows after 30 seconds to 1–1.5 seconds, and uses the slow
interval immediately after a disconnected phone. Off still drains shutdown and
releases PHY/RNG/HFXO; no reconnect is attempted once disabled.

Sources inspected locally: pinned InfiniTime
[MusicService](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/components/ble/MusicService.cpp)
and [radio configuration](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/CMakeLists.txt),
and pinned InfiniLink
[MusicController](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/Utils/MusicController.swift).
No companion application code was copied.

Host coverage includes sleeping session expiry in the production loop, renewed
session deadlines, opt-in policy, mode transitions during asynchronous shutdown,
peer changes, command failure, weather reception, wire bounds, unchanged metadata,
malformed numeric writes, queue/callout deadlines, and strip pixel equivalence.
The C tests run under ASan/UBSan. These checks do not establish RF timing,
interoperability, or connected-idle current on the physical watch. Android-style
notifications are added in 0.3.11 below; bond storage, Apple ANCS and Apple Media
Service remain future work. Keep the
installed 0.3.7 battery comparison separate from this unflashed candidate.
Build sizes, resource budgets and the final package checksum are recorded in
[performance measurements](performance.md#phone-candidate-039-2026-10-09-not-installed).

## Notification inbox (candidate 0.3.11)

The local candidate adds **Apps → Inbox**. **LINK** opens the existing phone
connection choices; simply opening the inbox leaves Bluetooth off. In opted-in
phone mode, a companion can send InfiniTime ANS notifications even without a
music subscription. There is no additional polling timer or background radio
session. The watch still starts with Bluetooth off after a reboot.

- Retains the **four newest messages** in RAM, newest first. Tap a row to read
  and clear its unread marker; the clock shows a tappable unread count.
- Source/sender/title text and body are wrapped and paged with **MORE / TOP**
  or a left swipe. Unsupported Unicode becomes one `?` per decoded character;
  control characters become spaces. This firmware's font remains ASCII.
- **DISMISS** and **CLEAR** affect this watch only. ANS has no stable remote
  notification IDs, so edits, removals and phone dismissal are not synchronized.
  A viewed message that is evicted shows a replacement notice instead of a
  different message under the same controls. Calls have no answer/reject action.
- A new message gives a **150 ms** vibration, at most once every **five seconds**.
  The cutoff is scheduled even with the screen asleep. Messages leave the screen
  asleep and do not change the active page. Alarm/countdown vibration takes
  priority; suppressed message vibrations are not replayed later.
- **QUIET** suppresses message vibration while retaining messages and unread
  markers. Quiet mode and inbox contents are RAM-only and reset on reboot;
  disconnecting retains messages already received by the UI. No notification
  text enters the settings journal. The existing link is still unauthenticated
  and unencrypted; pairing/bonding and ANCS are not implemented.

The receiver uses service **0x1811**, New Alert **0x2a46**, and InfiniTime's
three-byte companion header followed by plain text or `title NUL body`.
Transport copies at most 103 bytes; larger writes end in `...`. Stored titles
have a 40-byte limit and bodies a 100-byte limit, subject to the combined wire
limit. A two-packet mailbox keeps the newest packets during bursts; each loop
update drains at most two. Connection changes discard undelivered mailbox text.
This is a best-effort inbox, not a reliable message archive.

The implementation was checked against pinned
[InfiniTime AlertNotificationService](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/components/ble/AlertNotificationService.cpp),
[Gadgetbridge's PineTime sender](https://github.com/Freeyourgadget/Gadgetbridge/blob/master/app/src/main/java/nodomain/freeyourgadget/gadgetbridge/service/devices/pinetime/PineTimeJFSupport.java)
and its [ANS encoder](https://github.com/Freeyourgadget/Gadgetbridge/blob/master/app/src/main/java/nodomain/freeyourgadget/gadgetbridge/service/btle/profiles/alertnotification/AlertNotificationProfile.java)
(GitHub mirror inspected 2026-10-09), plus pinned
[InfiniLink's app-message sender](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/BLEWriteManager.swift).
No companion implementation code was copied. InfiniLink-generated app messages
can use this service; **general iPhone notifications still require ANCS**.

Host tests cover parsing, malformed/long/Unicode packets, bounded replacement,
read/dismiss/clear, stable selection, text paging, frame pixels and zero redraw
allocations. The production event-loop tests cover screen-off vibration cutoff,
quiet mode, burst limits and simultaneous countdown alerts. The C mailbox runs
under ASan/UBSan. Both standalone and BLE builds are checked with unchanged
resource gates. **No real phone exchange, GATT rediscovery, ATT long-write,
receiver boot or current measurement has been performed for 0.3.11.**

## Firmware integration constraint

TinyGo's Nordic Bluetooth backend currently relies on SoftDevice. Our MCUboot
image starts at 0x8020 with RAM at 0x20000000; TinyGo 0.42.0's S132 target starts
at 0x26000 and reserves RAM through 0x200039c0. Simply importing the Bluetooth
package is therefore not a compatible change to this firmware layout.

The candidate uses a cooperative NimBLE port with the existing MCUboot layout.
NimBLE uses RADIO/TIMER0/RTC0/RNG and its reserved PPI channels; TinyGo's runtime
continues using RTC1. Radio interrupts stay in C, while host queues and UI work
run cooperatively between display strips and waits. C allocations use a bounded
8 KiB non-moving arena. Normal non-Bluetooth and provisioning targets are unchanged.
See [radio build and validation](../internal/ble_nimble/README.md). Do not overwrite
MCUboot or flash a standalone image to try this feature.

References:

- [InfiniTime time service](https://github.com/InfiniTimeOrg/InfiniTime/blob/main/src/components/ble/CurrentTimeService.cpp)
- [TinyGo Bluetooth Nordic requirements](https://github.com/tinygo-org/bluetooth#nordic-semiconductor)
- [Web Bluetooth capabilities and requirements](https://developer.chrome.com/docs/capabilities/bluetooth)

## Firmware update interaction (candidate 0.3.13)

Opening the direct-update screen stops time/weather sessions and sets phone mode
to Off. The update radio accepts only its firmware transfer, keeping the display
on until Cancel, an alarm, or the ten-minute inactivity expiry. Phone mode must
be enabled again afterward. Install uses the existing planned-reset clock
handoff; time spent rebooting or swapping is still approximate. No new sleep
polling or background battery sample is added. Hardware verification is pending.
