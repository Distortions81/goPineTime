# PineTime phone radio

This port is compiled only with `pinetime && bletime && !provision`.
It provides two simultaneous phone connections, authenticated passkey pairing
and two saved device bonds,
CTS, standard Battery and Device Information services for InfiniLink compatibility,
bounded time/weather/media state, InfiniTime-compatible music controls, an ANS
notification receiver, an iOS ANCS client, and a custom direct-update receiver. Apple AMS and
Nordic Legacy DFU inside goPine remain unimplemented; legacy DFU clients use
InfiniTime recovery.

Current source targets **0.3.28**. The [pairing harness](../../docs/pairing-harness.md)
runs bounded physical checks on hci1 and reports phase-specific failures. The
retained 0.3.23 watch trace shows repeated authentication failures, including
successful encryption callbacks without a fully authenticated/bonded connection.
New non-secret trace flags distinguish those conditions. Existing unusable saved
keys show **Pairing needs reset** in INFO and remain intact until explicit reset.

The candidate prevents weak/incomplete pairing keys from occupying the phone
slot, keeps controller work moving through touch/LCD/flash settling delays, and
retries initial ANCS resource failures with bounded deadlines. Due ANCS timers
now return control from the input wait to the host pump. These reproduce in
automated tests; physical InfiniLink pairing is not yet established. The app keeps
two paired-device connections for time, weather, music and notifications. See
[phone setup](../../docs/pairing.md) and [candidate notes](../../docs/releases/0.3.27.md).

The fixed 87-byte internal snapshot distinguishes disconnected, securing,
authenticated without music, and music-ready states. Shared Phone status does
not depend on a music subscription; media commands still require one. Music
errors are local to that screen instead of replacing the connection status.

All incoming phone-feature writes require an encrypted, authenticated
connection with a 16-byte key. Automatic CTS uses a fixed ten-byte mailbox with
connection, generation, and receipt time. Permissions are checked both before
queuing and when consuming; stale, disconnected, or unauthenticated data cannot
become a trusted clock update. Go validates the date and adjusts for delivery
age. Resent time within two seconds of an already synchronized clock does not
cause another clock edit. The separate explicit five-minute time session retains
its on-watch proposal and confirmation flow.

The port exposes InfiniTime Simple Weather Service
`00050000-78fc-48fe-8e23-433b3a1942d0`, with writable characteristic `00050001`.
The candidate's Weather screen uses the shared Phone connection, which
accepts weather throughout the session. The transport also retains its legacy
explicit 60-second weather window.
Two fixed mailboxes hold the latest current/forecast record (maximum 53 bytes
each). Complete mbuf chains are copied; transport lengths and versions are
checked before queuing, and Go validates records before replacing the cache.
CTS writes during the legacy window receive a transport ACK but do not change time,
create a time proposal or terminate weather transfer. Both valid records end the
standalone weather session (not a phone connection); the final write ACK retains the existing 200 ms shutdown grace.

ANS `0x1811` exposes writable New Alert `0x2a46`, using
InfiniTime's three-byte companion header and at most 100 bytes of text.
Only authenticated phone mode accepts notifications. Each device has a two-slot, 103-byte-per-slot
mailbox that copies complete mbuf chains and keeps the newest two packets on
overflow. Disconnect or lost trust clears only that device; Stop/Start clears both. Oversized writes are truncated
with an ellipsis; undersized writes are rejected. The Go loop drains at most two
packets per update and validates/sanitizes them into its four-entry RAM inbox.
Mailbox work participates in the existing interrupt/deadline wake path, adding
no periodic poll. No notification response/call-control characteristic is exposed.
See [inbox behavior](../../docs/time.md#notification-inbox-candidate-0311).

The ANCS GATT client is separate from that ANS server. It discovers the iPhone's
Generic Attribute service and subscribes to Service Changed, then discovers ANCS,
subscribes to Data Source before Notification Source, and reads title/message
attributes through Control Point. Only authenticated, bonded 16-byte encrypted
links can start discovery or supply content. Procedures advance from the shallow
host poll after callbacks unwind; epochs reject callbacks from replaced sessions.
Each of two ANCS clients has four queued UIDs, a 151-byte fragmented response,
and two 147-byte output records. The Go bridge appends a source-slot byte; the
inbox matches remote UIDs within that source and clears only that source on
disconnect. Connection/epoch checks reject delayed callbacks after handle reuse. Invalid UID errors skip vanished messages; malformed/timeout
streams stop until service change or reconnect to prevent stale attributes being
assigned to a later UID. Missing/unauthorized services retry at 30-second
deadlines up to three times, then wait for Service Changed or reconnect.
No ANCS notification actions or Apple AMS media client are implemented.
The radio remains a GAP peripheral. `port/config.h` explicitly enables the four
required GATT client discovery/write operations, whose upstream defaults follow
the disabled central radio role. Without these overrides, service discovery
returns `BLE_HS_ENOTSUP`; the client added in 0.3.18 could not deliver ANCS.
See [Apple's ANCS specification](https://developer.apple.com/library/archive/documentation/CoreBluetooth/Reference/AppleNotificationCenterServiceSpecification/Specification/Specification.html).

## Direct firmware update

An explicit watch-side hold opens a separate ten-minute inactivity window.
Advertising names the watch `goPine Update` and exposes service
`00060000-78fc-48fe-8e23-433b3a1942d0`. Control/data/status characteristics have
suffixes `00060001`, `00060002`, and `00060003`. Two fixed 236-byte payload slots
queue writes for the Go loop, retaining a slot until flash readback is published.
CONTROL exposes the optional `[1,2,1,0]` capability read; DATA supports both Write
Requests and Write Commands. Old senders remain supported. New senders negotiate
the two-packet window and acknowledge only durable cumulative offsets. The explicit
OTA owner requests a 7.5–15 ms connection interval once, with benign rejection;
spectators and normal phone connections are not tuned. Preferred ATT MTU is 247; the fixed eight
128-byte mbuf blocks and four shared ACL credits remain unchanged. A measured
5 KiB C arena holds host startup allocations, with over 2 KiB spare above the
64-bit sanitizer harness peak of 3,056 bytes. Smaller negotiated MTUs
use smaller chunks. Byte counts are acknowledged only after flash readback.

Only the inactive application slot is written. Image validation streams through
MCUboot header/vector/SHA-256 checks. No BLE operation activates the image;
local Install rechecks power and integrity, then writes trial boot magic last.
Cancel invalidates an uncertain commit before returning to Settings. A running
trial cannot open update mode. Update screens temporarily take ownership of the
radio; enabled phone auto-connect resumes after leaving them. See
[protocol and usage](../../docs/ota.md#update-inside-gopine-candidate-0313).

## Reproducible candidate build

Requires TinyGo **0.42.0**, Go from go.mod, Python 3, clang and llvm-ar. Start at
the repository root. Supply a clean checkout at the pinned InfiniTime commit;
the NimBLE tree is tracked there, so no full SDK or FreeRTOS build is needed.

```sh
git clone https://github.com/InfiniTimeOrg/InfiniTime.git build/deps/InfiniTime
git -C build/deps/InfiniTime checkout --detach 6c119eb52206b580b556b41633dddc1e1b66a8da
tinygo build -target=./targets/pinetime-gopine.json -o build/plain-check.elf .
# The plain build initializes TinyGo's generated libc headers.
INFINITIME_SOURCE=build/deps/InfiniTime bash scripts/build-ota.sh --ble 0.3.18
```

`TINYGO=/path/to/tinygo` and `TZ=your/timezone` are supported. The script rebuilds
the archive on every BLE package build, checks the source revision/cleanliness
and TinyGo version, and atomically replaces the archive only on success.
The resulting `build/ota/gopine-dfu-0.3.18.zip` is for **existing MCUboot OTA only**.
Never use a standalone/bootloader image for this test. The ordinary build does
not include this experimental radio backend and will report Bluetooth unavailable.

NimBLE is Apache-2.0 licensed; preserve its upstream LICENSE/NOTICE when
redistributing the linked firmware (copies in this folder). Nordic/ARM headers
come from TinyGo's bundled MDK/CMSIS, not an unpinned SDK download. This port's
small compatibility shims do not copy InfiniLink's GPL implementation.

## Ownership and shutdown

- NimBLE owns RADIO, TIMER0, RTC0, RNG and the PPI channels configured in its
  nRF52 PHY. The TinyGo runtime owns RTC1; the input driver owns RTC2 and GPIOTE PORT;
  board sensors/touch share I2C1, and the LCD/flash share SPI0. GPIOTE IN
  channels must remain disabled. There are no Go callbacks from radio interrupts.
- IRQ handlers are installed in a 256-byte-aligned RAM vector table copied
  from the current application's VTOR. Unrelated vectors, including the input
  driver's previously installed PORT handler, are preserved.
- Host queues, callouts and blocking HCI acknowledgments are serviced by the
  cooperative NPL pump. A busy queue cannot recursively dispatch itself. In
  0.3.15, HCI semaphore waits service only the controller; host events and host
  callouts stay queued until the HCI caller returns. This prevents disconnect
  dispatch from reentering termination before its outstanding count is updated.
  Stop completion defers controller/host reset until the outer poll returns
  from host dispatch.
- The host starts for an explicit foreground session or enabled phone auto-connect
  after a confirmed boot. Shutdown disables advertising,
  terminates the peer, resets the controller scheduler, disables the PHY/RNG,
  and releases the radio HFXO request. It allows 200ms to send a time-write ACK
  and at most 500ms for graceful disconnect. RTC0 bookkeeping remains available
  for subsequent starts. Off never reconnects. Enabled phone mode advertises at 20–30 ms intervals
  for the bounded 30-second setup window, then 1–1.5 seconds. Failed connections
  retain the remaining fast window; opening/waking Phone renews it.
- Pending settings saves temporarily stop the shared phone session. Internal
  flash programming waits until both the controller and its asynchronous
  disconnect are idle; auto-connect resumes after the save. Opt-in is saved before
  first starting BLE when storage and fresh power checks allow it. Saves are
  coalesced, and shutdown progress supplies wakeups instead of a polling loop.
  Hardware testing must verify these transitions with real phone traffic.
- This HFXO policy depends on the audited TinyGo PineTime runtime/SPI driver not
  owning another HFXO request. Re-audit if adding another crystal consumer.
  NimBLE RF management uses InfiniTime's 1,500 µs
  startup allowance. The RF manager now owns requests around controller events;
  the old unconditional session-start HFXO request is removed. Verify this on
  hardware before assuming any connected-idle power improvement.
- The CPU waits on queue/callout deadlines and IRQs. The final interrupt-masked
  WFE handoff rechecks NPL queues so already-dispatched IRQ work cannot be lost.
  This retains the single Go application task assumption.
- The unused accelerometer and heart-rate sensor remain powered down. This
  candidate adds no motion notifications or sensor polling to drive companion
  weather refresh; InfiniLink background weather is consequently limited.
- The pinned host's stop counter survives a disconnect timeout. `port/stop.c`
  wraps that implementation to reset the count on each new stop; completed
  shutdown also clears stale host GAP records after resetting the controller.
- Invariant failure resets the application; MCUboot and recovery are unchanged.
  Check initial boot before KEEP. Settings require KEEP, so radio tests then
  rely on the existing recovery path rather than automatic trial rollback.
- The pinned GATT start consumes service definitions and reallocates its ATT
  pool. Starting it again without clearing the old list leaves invalid entries
  and no registered services. `port/gatts.c` registers the fixed database once
  and preserves ATT memory, service entries and CCCD pools across subsequent
  host starts. Per-connection CCCDs are still released on disconnect through
  normal upstream cleanup. Resource counts and handles remain stable.
  This defect reproduced on the second host start in the integration test.

## Verification and limits

Host Go tests cover saved opt-in, shared navigation, authenticated phone-time
handling, repeated sessions, cancellation, sleep, expiry, bad payloads, delayed
approval, InfiniLink calendar compatibility, settings-save shutdown and resume,
and strip-rendering equivalence. Python tests cover the PC wire format, immediate
connection using the discovered device, and bounded retries before writing only.
C arena, NPL queue/callout deadlines, phone permissions/time mailboxes, and bounded
weather/music mailboxes run under
address/undefined-behavior sanitizers. These
do **not** simulate the nRF radio or prove event deadlines on the watch.

The resource checker enforces the 8 KiB task stack, selected call-path budgets,
heap-region floor, and 2,880-byte display strip. See [performance](../../docs/performance.md)
for versioned measurements. Runtime stack/heap peaks and current still need
hardware measurements; successful builds are not proof of headroom under traffic.

The local 0.3.18 BLE build, race-enabled Go tests, eleven C sanitizer suites, and
real-host integration test passed. The latter uses a simulated HCI controller
with the real pinned host, production service/allocator/NPL/GATT/security code:
100 update discovery cycles and ten phone public-key/passkey/update handoffs.
The 0.3.20 extension completes 20-round passkey pairing, DHKey checks, LTK
encryption, identity distribution, pre-pairing CCCD persistence under the peer's
permanent identity, flash-journal reload and authenticated bonded reconnect.
A bad confirm verifies failure reporting. Trusted time/weather/music/ANS writes
are exercised before and after eight private-address reconnects. The test now
runs actual host startup, including controller reset and identity-key restore;
a rejected restore retains the bond for retry. Reconnects add no flash writes.
The peer server in `tests/host_ancs.h` answers actual GATT client ATT requests:
Data-before-Source subscriptions, immediate notifications, fragmented attributes,
removals, Service Changed confirmation, disappearance/republication, and a
denied-sharing retry preserving battery/weather are exercised across reconnects.
This test failed with the previous disabled GATT client configuration.
`testdata/infinilink_wire.json` supplies shared source-derived synthetic
calendar/weather/music/alert values. The test script generates a temporary C
header from them; the real-host test checks exact mailbox bytes and outbound
music control notifications, including stale-generation rejection. Go's
`companion_wire_test.go` separately checks those values reach the clock, cache,
music and inbox through one phone connection. They are not captured iOS traffic.
Controller events execute inside ECC service points while host callbacks stay
deferred. Firmware and tests
use the same generated crypto source from `scripts/ble_crypto_sources.py`:
a fixed hook at each scalar bit and unsigned conversions for two upstream AES
byte shifts that would otherwise fail UBSan. Cryptographic inputs and math are
unchanged; no debug keys are enabled and vendor source files remain intact.
Run `python3 scripts/test-ble-host.py --infinitime build/deps/InfiniTime`.
It does not test the nRF controller/IRQs, an actual iOS bond or Go stacks.
The updater defers full host processing to the shallow main-loop pump as well.
Pending host work must make HasUpdate true: merely preventing WFE does not
return the input wait to that pump. The 0.3.20 regression clears application
snapshots, queues real HCI connection/ATT/SMP packets, runs only the controller,
and verifies this handoff before dispatching the host. The earlier wake policy
fails that regression. NPL tests also cover due host timers and IRQ mask state.
Recovery transfer, receiver validation, boot and KEEP are confirmed; this
candidate has not been published. Before
calling the new phone behavior ready for normal use, test on the watch:

1. No advertisement with phone mode Off and no explicit foreground session;
   enable auto-connect and verify its setting survives a confirmed reboot.
2. Valid time from the paired phone applies automatically; invalid or
   unauthenticated time must not change the clock.
3. With iOS notification sharing allowed, InfiniLink leaves Connecting, reads
   the live battery level and firmware revision, pairs, and sends usable time
   and music data. Record the installed companion version. Weather requires
   a newer companion; classic 1.0.2 has no weather sender or Force ANCS setting.
4. Preserve screen-off phone connections and resume after settings
   saves/update cancellation. Verify Off survives reboot and releases the radio.
5. Touch, clock progression and display remain responsive during BLE traffic.
6. Confirm/reboot, then OTA entry/rollback still work; check memory/IRQ stack use
   and measure sleep current with Bluetooth Off, connected, and out of range.

The PC sender delivered time successfully after a watch reboot on 2026-10-05.
That earlier check and the confirmed 0.3.18 boot do not validate 0.3.18 phone
reconnects, background delivery, or the previously reported connected shutdown
crash. Those remain on-device checks.


## Pairing and phone permissions

LE Secure Connections only, display-only passkey authentication, bonding,
controller encryption and privacy are enabled. TinyCrypt AES/CMAC/P-256 sources
come from the same pinned vendor tree. Battery reads immediately return the
public one-byte percentage, matching InfiniTime. InfiniLink's newer development
branch needs that value to finish its connection screen. Classic 1.0.2 instead
requires a nonempty Firmware Revision (`0x2A26`) before showing Home. The public
Device Information service reports `PineTime`, `PINE64`, and `goPine ` followed
by the actual build version; its handles are appended after existing services.
Battery and Device Information data
does not prove pairing or encryption. Automatic CTS and incoming phone-feature writes
require authentication and bonding; an ATT security error lets the central
secure the link and retry its operation. Battery reads during update mode and
local notification reads use the same reference behavior.
Two bonds and eight CCCDs are retained; repeat pairing and capacity exhaustion
do not silently replace keys. Phone → INFO → FORGET ALL stops the radio before
clearing both pairings. Cancelling a displayed PIN disconnects only its device.

The pinned security manager omits the fresh-bond result flag and updates the
peer identity only after its encryption callback. `port/sm.c` wraps that exact
source without modifying the vendor checkout. It forwards encryption callbacks
once, then saves pre-pairing CCCDs after both key stores succeed for the same
authenticated peer identity. This avoids saving subscriptions under a temporary
private address. Failure and restored-encryption paths keep their upstream
behavior. The sanitizer regression runs the pinned key-exchange, result, GAP,
and GATT paths through first pairing and subscription restoration. The full
host test executes the pinned cryptography with a simulated central; it does
not execute the nRF radio or an iOS pairing exchange.

`port/privacy.c` performs a checked controller identity-key restore in the sync
callback. The pinned host already attempts restoration before that callback,
but logs and discards failures. The checked replay first removes any installed
peer entry: the real controller rejects duplicate insertion with `0x12`, which
previously prevented advertising after a bonded restart. The harness now models
that rejection. Address resolution is explicitly re-enabled because the host's
cached local IRK can survive stop/start and skip that step. This replay does not
write the flash journal. Controller failures prevent advertising and survive in
Phone INFO; the existing saved pairing remains available for a later retry.

The existing upstream RAM store supplies key lookup semantics. A separate
512-byte snapshot journal occupies only internal page 0x7d000. Records contain
CRC-32 and a final commit word; interrupted appends retain the previous record.
Keys and CCCDs save within one second of the first dirty write, or during
completed shutdown;
identical snapshots cause no writes. The deadline is active only for dirty data.
Reclamation happens before advertising, with battery at least 20%, and saves the
current state again. Power loss during this single-page erase can lose the bond,
requiring Forget on both devices; it cannot damage settings or either firmware
slot. Store schema 2 uses the pinned ARM compiler's structure layout, with
compile-time assertions for its 464-byte payload and the older 240-byte schema-1
payload. Schema-1 records load without writes and migrate on the next append.
All 128 interrupted-write boundaries are tested for that first migration and
for later records containing two saved devices.

C ASan/UBSan tests exercise host-queue deferral, all journal write cutoffs, key/
CCCD reload and Forget. The archive build emits build/ble/stack-usage.txt for C
frame inspection; this is not a whole-program stack proof. Candidate on-device
pairing, RPA reconnect, reboot persistence, and connected shutdown remain unverified.
See [test procedure](../../docs/pairing.md). ANCS host tests cover discovery,
subscription order, all short response split points, full-size fragmented text,
burst bounds, vanished UIDs, session reset/stale callbacks, and deadline wrap.
