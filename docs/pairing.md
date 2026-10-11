# Phone connection and pairing — 0.3.27 candidate

On 2026-10-11 the user reports that Bluetooth works but InfiniLink never displays
the watch. Their **Home, Charts, Settings** interface matches the classic 1.0.2
source, rather than the `rebuild` branch used for the earlier Battery audit.
Classic 1.0.2 requires a nonempty firmware revision from `0x2A26` to leave its
Connecting screen. goPine omitted this characteristic. The
[0.3.27 candidate](releases/0.3.27.md) adds standard, public Device Information
with the actual goPine version. The installed app's numeric version and physical
completion on this candidate remain unconfirmed.

The rebuilt two-device/three-OTA-link 0.3.27 package transferred completely and
was verified by the watch on 2026-10-11. The saved pairing was not erased by the
uploader. INSTALL and KEEP were instructed; boot, KEEP and physical InfiniLink
completion have not been reported. See the candidate notes for automated
coverage, limits and the successful transfer log.

The user now reports notification delivery following the 0.3.25 update, while
InfiniLink remains **Connecting** with an empty device list. Pairing was started
inside InfiniLink. The [0.3.26 candidate](releases/0.3.26.md) restores the reference
InfiniTime Battery read: its first response is available before encryption.
The newer InfiniLink development branch makes one initial read and waits for a Battery value to complete its
connection screen; our previous extra encryption gate could block that value.
The regression follows the app's read/subscription sequence and fails against
the old gate. The watch received and verified 0.3.26 through the in-app updater
on 2026-10-10 with its saved phone pairing retained; the successful resumed
transfer is logged in `build/ota/upload-0.3.26-retry2.log`. INSTALL, boot and KEEP
have not been reported. Physical InfiniLink completion is not yet confirmed.

The [0.3.25 candidate](releases/0.3.25.md) fixes a reproduced bonded-restart
failure. NimBLE restored saved peer identities before goPine inserted them
again; the real controller rejected the duplicates and advertising never
started. The harness now models that rejection, and the corrected startup
passes repeated bonded reconnects. A settings-save restart after pairing can
trigger this path. The user has now seen a PIN and a brief **Connected** state
after clearing both saved pairings, followed by **Retrying** and no completed
InfiniLink connection. The watch received and verified 0.3.25 through the in-app
updater on 2026-10-10 after the watch bond was cleared to restore OTA access.
INSTALL, boot and KEEP have not been reported. Forget the old iPhone Bluetooth
entry before pairing again, since the watch's key was cleared. Physical
InfiniLink success remains unverified.

The 0.3.24 candidate adds an [automated hardware pairing harness](pairing-harness.md),
rejects unsupported pairing requests and unusable bond storage, keeps controller work moving during
hardware settling delays, and repairs ANCS retries/deadline wakeups. Existing
unusable keys remain available for explicit **RESET PAIRING** in Phone → INFO;
they are no longer mislabeled as a saved working phone. Physical iPhone pairing
is still unverified. The watch received and verified the 0.3.24 image through
the in-app updater on 2026-10-10 after reopening **Ready to connect**. INSTALL,
boot and KEEP have not been reported. See [0.3.24 notes](releases/0.3.24.md).

After that transfer, the user reported that InfiniLink pairing still fails.
Two diagnostic attempts, including one immediately after the user reopened
**Ready to connect**, disconnected during service discovery. BlueZ saw
**goPine Update** and still had a saved PC bond. With the user's permission,
removing only that PC bond restored diagnostic access: the harness read a
complete 188-byte trace in OTA mode and completed cleanup successfully. No
firmware was sent. The trace began with unauthenticated saved keys (`0x1b1b`),
then showed an empty store after a later Phone restart. Further connections
ended with remote authentication failure (`0x205`) before any recorded PIN or
encryption event. This is consistent with a central retaining an old pairing
after the watch's key was reset; it does not prove which central or request
caused the failure. The next requested physical check is forgetting the watch
in iPhone Bluetooth settings and pairing again. Evidence:
`build/ota/pairing-retained-0.3.24.json` and
`build/ota/pairing-harness-20261010-215658-430182.jsonl`.

A later successful OTA-mode diagnostic read on 2026-10-10 recovered 0.3.23's
retained trace (`build/ota/pairing-retained-0.3.23.json`). It shows repeated
HCI authentication failures (`0x205`), including successful encryption callbacks
that did not reach the required authenticated/bonded state. This narrows the
failure beyond the earlier discovery results; the old trace does not identify
which security flag was missing. New non-secret metadata distinguishes them.

The user reports continuing physical failure on 0.3.21: the PIN prompt usually
does not appear, and code entry sometimes fails to complete. INFO shows
**No saved phone**. Earlier simulated test passes did not establish a working
watch/iPhone connection.

0.3.23 reproduces and fixes one retry defect: DISCONNECT unconditionally selected
1–1.5-second advertising even inside the first 30-second fast window. CONNECT
PHONE did not renew advertising when the radio was already running. Opening or
waking Phone and tapping CONNECT now renew a bounded 30-second fast window in
place; an active connection/security exchange is preserved. Phone fast intervals
now match the reference 20–30 ms range. Retries stay fast through that window.
They return to slow advertising after expiry, avoiding indefinite fast retries.
A regression test fails under the original disconnect policy and passes with
this implementation. This establishes the retry defect, not the cause of every
reported missing prompt or failed bond.

First pairing follows InfiniTime's central-triggered sequence: the phone requests
security for ANCS or receives an ATT authentication error from protected phone
feature writes. Battery is immediately readable as in InfiniTime. goPine does
not immediately send its own SMP Security Request
for a new peer during connection establishment. Existing bonds still support
peripheral-requested restoration. Secure Connections, MITM/passkey association,
key size, identity exchange and bond requirements are unchanged. Whether the
previous proactive request contributed to iOS failure is not established.

The host harness now retains all four controller TX slots while discovering the
music service and receives the fragmented SMP public key concurrently. The PIN
stage succeeds with the existing buffers, so no buffer-size workaround is added.

A 24-record RAM trace retains connect outcomes, security requests, passkey action
and injection outcomes, encryption outcomes, disconnects, protocol progress,
host resets, advertising errors and explicit stops. It survives automatic retries
and a transition to OTA, but clears on reboot. Failed establishment and local
security/injection failures now also reach INFO's numeric diagnostics. The trace
contains no PIN, keys, nonces, addresses or companion data. Read its format-1
characteristic `00070001-78fc-48fe-8e23-433b3a1942d0` with:

```sh
build/ota/venv/bin/python -u scripts/pairing_harness.py --mode trace
```

For a physical pre-PIN test, close the phone app temporarily, put the watch in
Settings → Phone → CONNECT PHONE, then run:

```sh
build/ota/venv/bin/python -u scripts/pairing_harness.py --mode probe --cycles 3
```

The selected hci1 central discovers the watch and requests real SMP pairing
through its own KeyboardDisplay BlueZ agent before any Battery read
or GATT service discovery. On RequestPasskey it records the milestone and rejects
after a short display interval, before
entering any PIN or saving a PC bond. It refuses an existing PC bond and does not
change the desktop's default agent. This verifies physical progression to the
central's PIN request, not the watch's rendered screen, full bonding, or iOS.
JSONL logs are saved under build/ota. A later iPhone failure can be inspected in
OTA mode without a new phone-mode connection replacing the last attempt.

The 0.3.23 transfer was verified on 2026-10-10. Three physical hci1 probe runs each
failed to find the watch address in three attempts, before connecting or reaching
SMP. The adapter was powered and a separate scan detected other nearby BLE
devices but no watch. Logs are recorded in the [release notes](releases/0.3.23.md).
The raw packet monitor lacked permission and produced no usable capture. A later
OTA-mode read recovered the trace described above; physical pairing remains
unresolved.

After the 0.3.20 transfer, the user reported entering the phone's code but never
completing pairing. INFO showed **No saved phone**. That establishes an
incomplete bond, not which security phase failed. The 0.3.21 build
shows **Waiting for phone code**, the number of completed code-check rounds,
key verification, encryption, or identity exchange on the PIN screen. INFO
retains the last observed stage and S/D errors after failure. The 0.3.21 transfer was verified on 2026-10-10, but the user reports that
pairing still fails and the PIN usually never appears. A separate reproduced permission mismatch is fixed: authenticated,
encrypted feature writes no longer return an ATT authentication error while
identity exchange is pending. Their data stays queued until bonding completes;
failed security clears it. Connected status, ANCS and outgoing music controls
continue to require the completed bond. This preserves the reference NimBLE
distinction between ATT authentication and bond completion, and does not accept
pre-encryption data. Its role in the physical stall remains unconfirmed.

The host test now feeds incoming HCI ACL fragments of at most 27 bytes, matching
the controller's disabled data-length extension. The full passkey exchange
still passes, including reassembly of the 65-byte public key. It also confirms
Battery reads succeed after encryption but before identity distribution, so
requiring a saved bond for that read is not the cause. A 31-second stalled-code
test times out, clears the PIN and retains the stage. These are simulated host
checks; physical radio/encryption timing remains untested.

**0.3.19 booted successfully and was confirmed with KEEP on 2026-10-10.
Phone pairing remains unreliable. On the preceding 0.3.17, the user reported a crash before the code appeared
on the first attempt and incomplete pairing after code entry on the second.
Its unified phone connection, automatic time sync, and
companion behavior still need the on-device checks below.**
The local 0.3.18 candidate adds ANCS, moves host processing out of screen drawing
and feature packet frames, and defers passkey injection until its callback
unwinds. Investigation also reproduced a GATT restart defect: the second host
start reused ATT memory without resetting its old list or restoring service
definitions. The corrected port passes 100 real-host discovery cycles and ten
interleaved phone public-key/passkey exchanges. It is installed; these
tests do not establish that the physical watch crash is resolved.

On installed 0.3.18, InfiniLink stalled at Connecting and the watch showed
Securing connection without a code. INFO showed a saved phone. Clearing the
saved pairing enabled a brief code and iPhone prompt, but pairing did not
complete and the watch slept quickly. The installed 0.3.19 candidate
keeps Phone setup visible for up to two minutes and defers settings saves during
that window and while the link is securing. After successful authentication,
normal screen sleep resumes. The side button still sleeps immediately; leaving
Phone also permits normal sleep without closing the shared connection.

The user reported that 0.3.19 still slept quickly after waking Phone and still
lost the code/iPhone prompt. Investigation reproduced a missing wake signal:
controller-only pumping could queue HCI/SMP host events without returning the
input wait to the main loop. The local 0.3.20 candidate exposes that pending
host work through HasUpdate, retaining shallow callback dispatch, and renews
the setup window when waking Phone or returning from INFO. The real-host test
now asserts wake readiness before dispatching raw connection, ATT and SMP
packets. It fails with the previous wake policy and passes with this fix.
The direct uploader sent 0.3.20 and the watch verified it on 2026-10-10.
INSTALL and boot/KEEP have not been reported; physical pairing remains
unvalidated. The latest user-confirmed installed baseline is 0.3.19.

The reference also gives its controller task higher priority than the host.
goPine's key generation/ECDH originally ran without servicing controller events;
connection-event completion needs that task work to schedule the next radio event.
The candidate now pumps only controller work at each scalar-multiplication bit,
never recursively dispatching the host. The same generated source is used in
firmware and host tests; the pinned vendor checkout remains unchanged.

The real-host test now completes all 20 passkey rounds, verifies DHKey checks
and the derived LTK, enables encryption, distributes identity keys, saves
pre-pairing subscriptions under the permanent phone identity, reloads the flash
journal, and restores the authenticated connection and subscriptions. An invalid
confirm reproduces a failed pairing and verifies the error survives disconnect.
Controller events queued during key calculation execute while host callbacks
remain deferred. This is simulated HCI/flash testing, not physical iOS validation.

InfiniTime's `NimbleController::RestoreBond` restores peer keys after host sync,
installing identity keys in the controller as well as host memory. goPine's
journal reload omitted that controller step. Reset also disabled address
resolution, which the pinned host could leave disabled on subsequent starts.
The stronger test uses real host startup and reproduces a saved phone losing
its identity after a private-address change. The candidate restores controller
identity keys and enables resolution before advertising. Eight address changes
now preserve the authenticated bond and subscriptions with no additional flash
writes. A rejected controller restore reports an error and retains the bond for
retry. These results still use a simulated controller, not an actual iPhone.

The ANCS client added in 0.3.18 could not actually discover the phone's services:
NimBLE's client discovery/write defaults follow its central radio role, which
goPine disables. Real ATT/HCI testing reproduced `BLE_HS_ENOTSUP`; the earlier
client tests stubbed these operations. The 0.3.20 candidate explicitly enables
only the required GATT client operations on the existing peripheral connection.
The real-host test now receives immediate subscription-time notifications,
fragmented text and removals, confirms Service Changed indications, and
rediscovers ANCS after disappearance/republication. A denied-sharing retry leaves
the authenticated battery/weather connection usable. Physical iOS authorization
and delivery remain unverified.

`testdata/infinilink_wire.json` contains synthetic payloads derived from the
audited companion's calendar, weather, music and app-alert encoders. The real
host test sends those payloads over ATT and checks exact mailbox bytes, including
v0/v1 current weather and a valid five-day forecast. It also verifies outbound
music notifications and rejection of commands from an old connection. The Go
controller test applies the same packets to the clock, weather cache, music and
inbox without creating feature sessions. This replaces an earlier transport
weather fixture whose zero timestamp would fail the Go decoder. The stages are
tested separately; neither fixture set is a physical phone capture.

**Phone → INFO** now retains the last security/disconnect error with short text
and numeric `S`/`D` codes. A new connection attempt does not erase it; successful
security or Forget All clears it. This allows a short-lived prompt to be
diagnosed without trying to read transient status text.

On 2026-10-09, the user confirmed that 0.3.16 still crashes at **Weather →
Update → Done**, specifically after InfiniLink connects. The 0.3.17 candidate
removes that separate session from normal navigation and waits for physical
radio shutdown before settings saves. These changes address the reported path;
the user's confirmation does not establish the underlying crash cause or prove
the candidate fixes it.

## Two paired devices for phone features

Open **Settings → Phone → CONNECT PHONE**. The 0.3.27 candidate retains up to
two paired devices and accepts authenticated time, weather, music and supported
notifications from both at once. Keep **AUTO CONNECT: ON** to allow reconnection.
Browsing apps, returning to the clock and sleeping the screen leave both links
available. New pairing is serialized so only one device owns the displayed PIN.
Saved devices may restore encryption concurrently.

The auto-connect preference is saved with watch settings and restored after a
confirmed boot. New installations and older settings default to Bluetooth Off.
The watch advertises so the companion can reconnect; the phone remains the
Bluetooth central and decides when to connect. This setting does not force a
suspended or stopped phone app to run. Firmware Update temporarily takes over
the radio. **BLUETOOTH OFF** disables automatic reconnection while retaining the
saved bonds. Cancelling the displayed pairing disconnects only that new device.
**FORGET ALL** turns auto-connect off and clears both saved pairings after the
radio stops; it requires the on-watch confirmation.

During a trusted, encrypted phone connection, valid Current Time Service writes
update the clock automatically. They no longer require a separate Sync Time
session and ACCEPT tap. Time and timezone/DST changes still depend on the
companion sending a fresh local time; the watch does not independently poll a
time server. Weather packets update the cache without closing the connection.
Music controls and metadata use the existing InfiniTime music service. The most
recent device to send accepted metadata becomes the Music screen source; a
command carries its connection generation so it cannot be sent to a replacement
connection. Apple notification IDs are scoped to their source device, and
disconnecting one device clears only its Apple notifications.

Phone status follows authentication, independently of music support. A trusted
phone can show **Connected** and send time/weather without subscribing to music
controls. Media errors stay on the Music screen. **INFO** shows saved pairing
details; it does not start a separate pairing session.

## iPhone / InfiniLink

1. Use InfiniLink's normal connection mode. Allow notification sharing when iOS
   asks. The newer development app has a **Force ANCS** option; classic 1.0.2
   does not have that setting.
2. Open **Settings → Phone** on the watch and tap **CONNECT PHONE**. In InfiniLink, select the
   watch advertised as **InfiniTime**. goPine uses a different Bluetooth identity
   from the InfiniTime recovery clock.
3. Enter the six-digit code shown on the watch. New peers start security through
   the phone’s ANCS request or protected feature access; saved peers may restore
   encryption without another PIN. Check that InfiniLink receives
   battery data and **Phone → INFO** shows the saved-device count (up to two).
4. Allow Apple Music permissions, and weather/location permissions if the
   installed companion supports weather.
   Leave auto-connect enabled on the watch. Subsequent connections should reuse
   the saved bond instead of requesting another code.

Classic InfiniLink 1.0.2 remembers the peripheral when it connects, but its Home
screen still says Connecting until Device Information supplies a nonempty
Firmware Revision. The newer `rebuild` branch instead uses receipt of Battery
for its connection label. Both conditions are covered by the harness; public
metadata alone does not prove an authenticated pairing. goPine reports its
actual firmware as `goPine 0.3.27`, with the version supplied by the build.

Classic 1.0.2 supports time, Battery, app-generated alerts, and Apple Music.
It has no weather sender. Its steps and heart-rate charts require sensors and
characteristics that goPine does not currently expose; these are separate from
the Home connection condition.

The audited newer `rebuild` source writes time when it discovers the Current Time
characteristic. Its device screen starts a weather fetch when the weather
characteristic changes; successful fetches push current weather and forecast.
Location updates can also start a fetch. Fetches are normally throttled to one
per two minutes, so a quick reconnect can skip a fetch without resending cached
data. These are companion behaviors, not watch-side refresh requests.

**Background weather is limited by the companion version.** The audited `rebuild` branch's
periodic weather check is called from its motion-notification handler. goPine
keeps its unused accelerometer powered down and does not emit motion data, so
holding a connection alone does not provide that periodic trigger. Battery,
music, and step callbacks do not substitute for it. Use InfiniLink in the
foreground and its location/weather settings to refresh when needed. The watch
continues receiving any real weather pushes while connected and shows receipt
age; it does not claim a guaranteed refresh interval.

InfiniLink's music integration controls Apple Music. It does not establish
system-wide media control. Its app-generated alerts can use the shared phone
connection. The 0.3.20 ANCS client is intended to receive iPhone notifications directly,
retrieving title/message text and applying updates/removals in Inbox. Silent
and pre-existing notifications do not buzz. Apple identifiers and messages are
cleared when their ANCS session ends; Android messages are preserved.
Dismiss clears a local message, without dismissing it on the iPhone or answering
calls. Notification content depends on iOS sharing and preview permissions.

## Android / Gadgetbridge

Use Gadgetbridge's PineTime/InfiniTime integration for time, music controls,
battery, and supported notification forwarding. Enable its time synchronization
option if automatic clock setting is wanted. The audited initialization path
sends Current Time and Local Time, subscribes to music events, and reads and
subscribes to battery data. Permissions and the phone's background restrictions
still affect delivery and reconnection.

**Unmodified Gadgetbridge currently blocks weather for goPine's version.** Its
PineTime integration initializes firmware-version fields to zero and refuses to
send weather for firmware major versions below 1. Versions 1.8–1.13 use its old
weather protocol; versions 1.14+ use the Simple Weather Service implemented by
goPine. Exposing the same weather UUID is therefore insufficient. Reporting
goPine's actual 0.3.x firmware version would still hit that gate. Android weather
needs companion capability detection or explicit goPine support; the watch does
not pretend to be an InfiniTime 1.x release.

Gadgetbridge also requires a configured external weather provider, such as
Breezy Weather, once that compatibility issue is resolved. Weather is pushed
from that provider through Gadgetbridge; the Simple Weather Service has no
watch-to-phone refresh command. Its music handler accepts play, pause, next,
previous, and volume events, but ignores the `0xe0` metadata refresh hint used by
goPine. Metadata still depends on the companion's music updates.

## On-device acceptance

1. Enable Phone once and complete pairing with InfiniLink and iOS notification
   sharing allowed. Record the actual companion version. Confirm Home leaves
   Connecting, automatic time setting, battery display, and Apple Music controls.
   Test current weather and forecast with a companion version that implements
   weather; classic 1.0.2 cannot send it.
   Deliver a real iPhone notification, modify/remove it, and reconnect. Check
   silent/pre-existing notifications do not vibrate and stale session IDs clear.
2. Switch among Phone, Music, Weather, and the clock; let the screen sleep.
   Confirm the shared connection remains available and incoming weather does
   not disconnect it. Check the weather receipt age instead of assuming that a
   connection implies fresh weather. Repeatedly enter and leave Weather after
   InfiniLink connects, then exercise BLUETOOTH OFF and a settings-save shutdown
   while connected to check the previously reported exit/crash path.
3. Leave range and return, lock the phone, then reboot the confirmed watch.
   Confirm the saved setting and bond allow reconnection without another code.
   Repeat after the phone rotates its private address. Test time received during
   pairing as well as after an already-bonded reconnect.
4. Verify **BLUETOOTH OFF** survives reboot and releases the radio. Test the
   Firmware Update handoff and return to normal phone use after canceling it.
5. Test pairing cancellation, incorrect code, timeout, removed phone bond,
   radio failure, and low battery. **Phone → INFO → FORGET ALL → TAP TO
   CONFIRM** stops Bluetooth and erases both saved pairings; also forget the watch
   in each device's Bluetooth settings before pairing again.
6. Measure screen-off consumption with auto-connect off, connected, and waiting
   out of range. A successful connection does not establish battery endurance.

## Storage and radio behavior

Two devices' keys and up to eight characteristic subscriptions persist in the
internal bond journal at `0x7d000..0x7dfff`, separate from MCUboot scratch and
clock/settings storage. Schema 2 expands the snapshot from 240 to 464 bytes,
inside the same 512-byte journal record. Existing schema-1 keys and subscriptions
are loaded without an erase and migrate on the next committed append. Writes use a CRC and a final commit word, are coalesced
for up to one second from the first change, and flush at shutdown. Avoid resetting while **Saving pairing...**
is shown. Interrupted appends preserve the previous record. Reclamation occurs
with BLE off and at least 20% battery; power loss during that erase can require
pairing again. Storage errors are displayed, and keys are not logged.

Subscriptions created before first pairing completes are saved only after the
phone's learned identity and both keys are stored. This corrects a pinned-stack
omission while avoiding subscriptions tied to the temporary private address
used for initial pairing. Reconnect restoration still needs the physical-phone
checks above, even though the upstream host-path regression passes locally.

The connected-radio shutdown repair defers host work during HCI waits and resets
the controller after disconnect callbacks unwind. Version 0.3.16 additionally
allows ordinary unencrypted Battery reads during the explicit firmware-update
session, avoiding Linux's automatic pairing probe. Since 0.3.26 Battery is also
public in Phone mode, matching InfiniTime. Phone feature writes and authenticated
connection status still require security; public metadata does not replace a
saved phone bond.

## Source audit

The implementation uses protocol facts from these sources; companion code is
not copied into the firmware. Source behavior is distinct from compatibility
with every released App Store, TestFlight, or Gadgetbridge build.

- Classic InfiniLink 1.0.2 source `d9f13a5d785b25b30870eb6ea99d4716b554a9b0`:
  [Home readiness](https://github.com/InfiniTimeOrg/InfiniLink/blob/d9f13a5d785b25b30870eb6ea99d4716b554a9b0/InfiniLink/View%20Components/Home%20View/WelcomeView.swift),
  [Device Information reads](https://github.com/InfiniTimeOrg/InfiniLink/blob/d9f13a5d785b25b30870eb6ea99d4716b554a9b0/InfiniLink/BLE/DeviceInfo.swift).
- InfiniLink `rebuild` development branch `60abe2d2c67aff67726855374385a499d9353a94`:
  [connection and restoration](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/BLEManager.swift),
  [time, battery, music and motion callbacks](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/BLECharacteristicHandler.swift),
  [calendar encoder](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/SetTime.swift),
  [weather, music and alert encoders](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/BLEWriteManager.swift),
  [foreground weather trigger](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/Core/DeviceView.swift),
  [weather refresh policy](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/Utils/WeatherController.swift),
  [location handling](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/Utils/LocationManager.swift),
  [music handling](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/Utils/MusicController.swift).
- Gadgetbridge `8449c3e99498c81535177db68cbf7b98a2e5ae68`:
  [PineTime initialization, music events, and weather version gate](https://codeberg.org/Freeyourgadget/Gadgetbridge/src/commit/8449c3e99498c81535177db68cbf7b98a2e5ae68/app/src/main/java/nodomain/freeyourgadget/gadgetbridge/service/devices/pinetime/PineTimeJFSupport.java),
  [weather provider integration](https://gadgetbridge.org/internals/development/weather-support/).
- InfiniTime `6c119eb52206b580b556b41633dddc1e1b66a8da`:
  [Simple Weather Service](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/doc/SimpleWeatherService.md).
- Apple: [Core Bluetooth background processing and restoration](https://developer.apple.com/library/archive/documentation/NetworkingInternetWeb/Conceptual/CoreBluetooth_concepts/CoreBluetoothBackgroundProcessingForIOSApps/PerformingTasksWhileYourAppIsInTheBackground.html).
