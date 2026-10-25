# PineTime BLE port

Compiled with `pinetime && bletime && !provision`. Uses the pinned InfiniTime
NimBLE host/controller with a cooperative TinyGo port. See the root
[README](../../README.md#build-and-first-install) for building and flashing.

## Services

- Two simultaneous phone links with passkey pairing and two persistent bonds.
- Standard Battery, Device Information, and Current Time services.
- InfiniTime music controls, Simple Weather receiver, and ANS notifications.
- ANCS client for iPhone notifications. Apple AMS and remote notification actions
  are not implemented. The radio remains a GAP peripheral.
- Native goPine protocol-2 firmware update service
  `00060000-78fc-48fe-8e23-433b3a1942d0`.

Phone feature writes require an authenticated, bonded, encrypted link with a
16-byte key. Battery and Device Information reads are public. Fixed mailboxes
copy incoming data for the Go loop; connection generations reject stale work.
Each phone has independent subscriptions, notification state, and pairing data.
Weather reception remains implemented while the Weather UI is disabled.

OTA control/data/status characteristics use suffixes `00060001`–`00060003`.
The receiver negotiates up to two 236-byte payloads at MTU 247, acknowledges only
flash-verified bytes, and resumes from the durable offset. Local INSTALL stages
a trial; KEEP confirms it after boot. See [the receiver](../ota/receiver.go) and
[Python sender](../../scripts/direct_upload.py) for the wire protocol.

## Runtime ownership

NimBLE owns RADIO, TIMER0, RTC0, RNG, and its PHY PPI channels. TinyGo owns RTC1;
input uses RTC2 and GPIOTE PORT. Touch/sensors share I2C1; LCD/flash share SPI0.
The port preserves existing vectors when installing its RAM interrupt table.
No Go callbacks run from radio interrupts.

Host callbacks defer controller work where needed to avoid reentrancy during
HCI waits. Shutdown drains disconnect work before resetting the controller,
disabling CCM/AAR and RNG, and releasing HFXO. With Bluetooth inactive, the
application stops pumping the host. Phone connections remain active during
screen sleep. OTA temporarily suspends ordinary feature processing.

Bonds and subscriptions use the internal `0x7d000` page as a CRC-protected
journal. Settings use separate pages. Interrupted appends retain the prior
record; power loss during bond-page reclamation can require pairing again.

## Tests

From the repository root, with the pinned dependency checked out:

```sh
python3 scripts/test-ble-port.py --infinitime build/deps/InfiniTime
python3 scripts/test-ble-host.py --infinitime build/deps/InfiniTime
python3 scripts/test-ble-phy.py --infinitime build/deps/InfiniTime \
  --tinygo-root "$(tinygo env TINYGOROOT)"
python3 scripts/test-runtime-rng.py --tinygo-root "$(tinygo env TINYGOROOT)"
```

The sanitizer harnesses exercise the pinned host, pairing, reconnects, bond
storage, ATT traffic, ANCS, update ownership, and peripheral shutdown. They do
not simulate radio timing or measure current. The BLE build also checks task
stack/heap-region budgets and allocation/RNG call sites.

For physical diagnostics, `scripts/pairing_harness.py --help` describes trace,
probe, and full pairing modes. Check its configured adapter/address before use;
full mode creates a real PC bond and requires the on-watch PIN.

NimBLE is Apache-2.0 licensed; retain the included LICENSE and NOTICE files.
Nordic and CMSIS headers come from TinyGo. The port does not copy companion-app
implementations.
