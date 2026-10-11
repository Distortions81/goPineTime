# Automated Bluetooth pairing checks

Run the Linux harness with the prepared Bluetooth environment:

```sh
build/ota/venv/bin/python -u scripts/pairing_harness.py --mode trace
build/ota/venv/bin/python -u scripts/pairing_harness.py --mode probe --cycles 3
```

The harness, legacy probe and Linux uploader share a nonblocking per-target
lock. A second goPine client exits before opening Bluetooth while the first
holds the watch, including during disconnect cleanup. Other targets remain
available. This coordinates goPine tools; it does not lock out desktop Bluetooth
settings or other applications.

Use trace mode on the OTA page, including when this PC already has a bond. It
sends no firmware and never calls Pair. Firmware through 0.3.25 protects Phone
mode Battery reads, so BlueZ's Battery plugin can request security automatically;
use OTA for those diagnostic reads. The newer public Battery read does not
establish a secure link. Phone failures remain in the watch's RAM ring until reboot or
replacement by newer events. A partial or failed read fails the trace check.

Probe mode needs **Settings → Phone → CONNECT PHONE**, with the phone app closed
and no existing PC bond. It uses only **hci1 / C9:9E:15:7A:69:B4**, requests real
Secure Connections pairing through its own KeyboardDisplay agent, waits for the
central's PIN request, and deliberately rejects before entering a PIN or saving
a bond. Discovery, pairing, trace reads, and cleanup have separate deadlines.
Each attempt records its failing phase and timings in `build/ota/*.jsonl`.
Known disconnect and timeout failures also have fixed `error_code` values;
arbitrary library exception messages are omitted so PINs and peer data cannot
leak into the log. A service-discovery disconnect identifies where the attempt
stopped, but does not identify whether a saved key or radio timing caused it.
The process exits nonzero unless every requested attempt and cleanup succeeds.
It never selects another adapter, replaces the default agent, deletes an existing
bond, or falls back to recovery.

Probe starts Pair through its own agent before connecting for GATT discovery.
It skips a Battery pre-read, which can invoke the desktop's default agent on
older firmware with protected Battery access.

For a complete physical bond and repeated encrypted reconnects:

```sh
build/ota/venv/bin/python -u scripts/pairing_harness.py --mode full --cycles 3
```

This mode asks for the displayed six-digit PIN once, through hidden standard
input. It then automates the remaining checks. The PIN is never a command-line
argument, trace field, or readable GATT value. It leaves the test PC bond in
place. On the 0.3.27 candidate the iPhone can occupy the second saved slot.
On older single-bond firmware, use the watch's confirmed Forget action and forget
the watch on the PC before pairing the iPhone again. The candidate's **FORGET ALL**
action clears both saved devices, so it is not a routine test cleanup step. Fully unattended first pairing would
need an independent way to observe the screen; production PIN authentication is
not bypassed for testing.

Full mode requires the 0.3.24 security metadata, including a fresh authenticated,
bonded, 16-byte encrypted-link record on each reconnect. A successful Battery
read proves neither encryption nor authentication on firmware with public
Battery access; the security metadata remains mandatory.

Full mode also checks discovered UUIDs and characteristic properties against
the contracts in `testdata/infinilink_wire.json`. The classic 1.0.2 source,
with **Home, Charts, Settings** tabs, keeps its
[Home view on Connecting](https://github.com/InfiniTimeOrg/InfiniLink/blob/d9f13a5d785b25b30870eb6ea99d4716b554a9b0/InfiniLink/View%20Components/Home%20View/WelcomeView.swift#L17)
until it receives a nonempty UTF-8 Firmware Revision (`0x2A26`). Full mode
requires that characteristic to be readable and reads it on every connection;
missing, empty, invalid UTF-8, or failed reads fail the cycle even if Battery
works. The fixture records the exact classic source paths and SHA-256 hashes.
It pins historical source with marketing version 1.0.2, without claiming to
identify the exact installed App Store binary.

The separate newer
[InfiniLink characteristic callbacks](https://github.com/InfiniTimeOrg/InfiniLink/blob/60abe2d2c67aff67726855374385a499d9353a94/InfiniLink/BLE/BLECharacteristicHandler.swift)
and writer remain the source for the supported Battery, CTS, music, weather
and alert contract and packet fixtures. That newer app uses Battery receipt
for its Connected label. The harness reads Battery exactly once, then
subscribes, matching both generations' per-characteristic startup order. A
failed read or subscription fails the cycle; no read retry hides that failure.
An immediate notification is not required after the successful initial read.
The harness does not assume an order between different service discovery
callbacks, require unsupported sensors/filesystem/DFU, or write time and feature
values unless the corresponding option was given. This is a companion contract
check through BlueZ; it does not run CoreBluetooth or the installed iPhone app.

`--write-features` additionally sends the source-derived weather/music/alert
fixtures; `--write-time` explicitly sets the watch to the computer's local time.
These flags require full mode. Feature write acknowledgements prove transport
acceptance, not the watch's rendered output. Default tests do not replace those
values. Linux results do not establish InfiniLink/iOS interoperability or Apple
notification delivery.

The firmware trace format remains version 1. It contains no PIN, keys, nonces,
addresses, or notification content. The 0.3.24 candidate adds:

- Event 13: link security, with encrypted/authenticated/bonded in bits 0–2 and
  key size in the high byte.
- Events 14/15: all saved/current-peer key metadata. Low/high bytes describe
  our/peer records; bits 0–4 mean present, LTK present, authenticated, Secure
  Connections, and a 16-byte key. These are flags, never key material.
- Header byte 3: current saved local-key metadata (zero on older firmware or
  when no local key record exists).

These distinguish successful encryption from a usable authenticated bond.
OTA encryption no longer overwrites the preceding Phone diagnostic result.

The development firmware also records event 16 (`ltk_restore_lookup`) and
event 17 (`pair_request_rejected`). Their result is a NimBLE status only;
event 16 with `BLE_HS_ENOENT` (5) means the central requested a saved encryption
key the watch could not find. Event 17 covers early request rejection that the
pinned host does not report through its encryption callback. Neither event
contains a key, peer address or pairing packet. These events are absent from
the already-transferred 0.3.24 image.
Event 18 (`startup_failure`) records the status when identity/privacy setup
fails before advertising, including the former duplicate-identity error
`0x0212`. It also contains no key material.

The repeatable tests without a watch are:

```sh
python3 scripts/test_pairing_harness.py
python3 scripts/test_pairing_probe.py
python3 scripts/test_direct_upload.py
python3 scripts/test_ble_session.py
python3 scripts/test-ble-port.py --infinitime build/deps/InfiniTime
python3 scripts/test-ble-host.py --infinitime build/deps/InfiniTime
go test -race ./...
go vet ./...
```

The harness tests inject discovery failures, timeouts, bad/partial traces,
unexpected pairing success, and cleanup errors. Sender tests cover an existing
PC connection without advertisements; process tests keep competing goPine
clients excluded through asynchronous cleanup. The C suites execute the pinned
host, production cryptography, full 20-round passkey exchange, early rejection
of unsupported pairing requests, failed pairing,
identity distribution, bond persistence, private-address reconnects, ATT feature
traffic, ANCS, and repeated host restarts under sanitizers. Go tests exercise the
shared phone UI and controller service during hardware settling. Simulated HCI
and modeled timing do not execute the physical nRF radio.

The classic-app host regression discovers DIS and reads its public revision over
ATT. It checks that a subscribed Battery snapshot follows that read response
once per connection, giving classic InfiniLink a publication after its plain
firmware field changes. A separate cold-process test reloads a saved bond,
preserves its Service Changed indication through an unrelated OTA connection,
restores encryption, acknowledges cache invalidation, and discovers the added
DIS without changing either phone key. Middle-CCCD deletion is checked across
journal reload because corrupted subscriptions can prevent that migration.

The multi-device regression uses actual HCI connection handles and requires
advertising before each simulated connection. It performs two full authenticated
Secure Connections pairings, stores both keys, reloads the journal and restores
encryption in the opposite order. Per-device ATT MTUs, subscriptions, feature
mailboxes, same-UID Apple notifications, command generations, pairing
cancellation and disconnect ordering are checked. A separate three-connection
OTA case connects both saved phones before the PC, then exercises sender
ownership, status-only spectators, rejected cross-device writes, disconnects,
resume and bounded candidate deadlines. These tests run the pinned host and
cryptography, not the nRF radio or iOS CoreBluetooth/SwiftUI.
