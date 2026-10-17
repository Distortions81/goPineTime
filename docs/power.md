# Screen-off power audit

“Off” here means display sleep, not nRF System OFF: the RTC, alarms, countdowns,
and side-button recovery continue working. These changes have host-test and
firmware-build coverage; battery life and sleep current still need measurement
on a watch.

## Bluetooth-off investigation (2026-10-16)

The user reported 91% remaining after 17 hours asleep with Bluetooth turned
off, compared with a remembered runtime near 40 days. If the starting reading
was 100%, linear extrapolation gives 7.9 days. The voltage gauge and unknown
installed version prevent treating that as a measured full-discharge runtime.

The shutdown audit found a reproducible omission in the pinned nRF52 driver
integration. `ble_phy_disable()` stops RADIO, TIMER0 and the event-triggering
PPI routes, but retains encryption state for the next connection event.
`ble_ll_reset()` ultimately calls `ble_phy_init()`, which also leaves the shared
CCM/AAR `ENABLE` register untouched. After encrypted traffic it can retain
CCM's enabled value (2); after private-address resolution it can retain AAR's
enabled value (3). Clearing the resolving list does not disable that peripheral.

The local fix uses `port/power.h` at final shutdown and startup-failure exits.
After stopping the PHY, it calls `ble_phy_encrypt_disable()` before stopping
RNG and releasing HFXO. This stops CCM, clears its software encryption flag,
and writes zero to the shared CCM/AAR `ENABLE` register. On nRF52832 both
peripherals share the `STOP` and `ENABLE` addresses; Nordic documents their
[shared resources and enable register](https://docs.nordicsemi.com/r/bundle/ps_nrf52832/page/ccm.html).
The next connection can enable encryption normally. This does not disable
encryption between active connection events or change pairing requirements.

Other audited paths already stop the controller scheduler and RF-manager
timer, cancel connection and privacy timers, shut down TIMER0, stop RNG, and
release the high-frequency crystal. RTC0 remains running for NimBLE timekeeping
and its overflow handling. After shutdown, the bridge ignores inactive software
callouts and stops pumping the host/controller. This audit found no evidence of
ordinary advertising continuing with Bluetooth off.

Validation adds `scripts/test-ble-phy.py`, which executes the pinned PHY's actual
initialization, disable and encryption routines against mapped memory using
Nordic's real register layouts. Unrelated ARM packet-copy assembly, NVIC/ISR
installation and RNG stop are stubbed for this focused host test. The old sequence reproduces
the retained CCM/AAR enable values; the new sequence clears them through repeated
restart cycles. Existing host tests had empty PHY shutdown stubs and could not
detect this. The integration harness now checks the cleanup ordering and an
hour of simulated idle time after shutdown, including after encrypted phone
traffic and consumption of the one-time notification-clear records. These tests
do not emulate DMA, electrical behavior or interrupt timing. The PHY and host
integration tests pass ASan/UBSan; `go test ./...`, `go vet ./...`, the BLE
archive build and TinyGo firmware link/resource gate pass. The audit ELF and
resource report are `build/ble/shutdown-check.elf` and
`build/ble/shutdown-resources.json`; this is not a release package or an upload.

**This proves incomplete peripheral cleanup, not the cause or magnitude of the
reported drain.** No sleep-current measurement or on-watch validation of this
fix has occurred. Version 0.3.34 was subsequently uploaded in-app on 2026-10-16;
the watch verified the image, and the user confirmed installation, boot and
KEEP on 2026-10-16. They plan to leave Bluetooth off and check the battery on
2026-10-17; battery impact remains unmeasured.
See [the transfer record](releases/0.3.34.md). Compare Bluetooth-never-started sleep with sleep after an
encrypted connection and Turn off, using the same firmware and settings.

## Current display controls

Candidate 0.3.33 displays a compact percentage and battery/charging icon at the
top right on every screen, including pairing, update, and charging overlays.
It uses existing power readings and redraw scheduling; it adds no battery polls
or screen-awake requirement. The Settings battery footer is removed. Low battery
uses the warning color; external power uses the accent color, with a bolt only
while actively charging.

**Settings → Display** groups the saved theme, brightness, touch-wake, and rotation
controls. Theme toggles between Dark and Light. Brightness cycles through High,
Low, and Medium; existing settings start at Dark and High. All three backlight
channels still turn off during screen sleep. These choices do not change the
15-second screen timeout or add background polling. Their physical brightness
and battery impact still need watch measurements.

Candidate 0.3.32 explains battery use before changing brightness or enabling
touch wake, Bluetooth, or notification buzz. The proposal is applied only with
**Confirm**; Cancel, swipe-back, focus cancellation, or screen sleep discards it.
Disabling touch wake/Bluetooth or choosing Quiet applies immediately. Existing
settings are retained and reconnecting an already enabled radio does not ask
again. Quiet mutes vibration but retains notification reception. Text describes
the cause of power use without unmeasured battery-life estimates. Theme and
rotation do not show a battery confirmation.

## Charger wake screen (0.3.14, boot/KEEP confirmed)

GPIO P0.12 (active-low charging) and P0.19 (active-low external power) now use
the existing low-power PORT/SENSE interrupt handler on both edges. Each handler
acknowledges its latched source and arms the opposite level. The normal wait
loop consumes charger changes separately from button/touch changes, then waits
for 150 ms of stable state before emitting one power event. Contact bounce and
short pulses do not repeatedly wake the display; stable charger state leaves
no additional timer deadline. External power absent takes precedence over a
charging signal that is still settling on unplug.

A power event wakes the LCD, refreshes the battery sample once, and opens a
full-screen notice. Awake sampling remains at five seconds, and the notice uses
the normal 15-second display timeout. It never keeps the display on for the
whole charge. Sleep has no new ADC polling. Tap/swipe dismisses the notice and
consumes that gesture; the underlying editor/page is preserved. Alarm, trial,
firmware transfer and active time/weather update screens retain priority.

Statuses are Charging, Unplugged, and Charging stopped (external power still
present); the last becomes Charged when the voltage estimate reaches 100%.
External power alone is not taken as proof of full charge. There is no ETA:
voltage-derived percentage under charging load is not a calibrated time-to-full
measurement. The user explicitly allowed omitting an unreliable ETA.

Host tests cover debounce, GPIO state precedence, a 12-hour sleep without ADC
polls, charger wake/unplug wake, alarm priority, update-prompt expiry, safe
notice dismissal and zero-allocation strip rendering. The GPIO ISR frame is
48 bytes in the BLE build and passes the resource gate. Physical charging edges,
contact bounce, return to sleep, and current consumption remain hardware checks.

## Local 0.3.9 phone power changes

The unflashed phone candidate adds opt-in screen-off connections. Bluetooth still
defaults to Off on reboot. A ten-minute session expires while asleep; Stay
Connected lasts until disabled. Music/weather traffic does not wake the display.

Two changes address connected overhead: NimBLE RF management now releases HFXO
between scheduled events (using InfiniTime's 1,500 µs startup allowance), and
hardware waits use NPL queue/callout deadlines instead of a fixed 20 ms interval.
Queues are rechecked with interrupts masked at the WFE handoff to close the
IRQ-before-sleep race. Initial advertising slows after 30 seconds, and link loss
uses 1–1.5-second intervals. Repeated metadata and position updates do not redraw.

Host/build checks pass, but crystal timing, connected/disconnected-idle current,
and shutdown current **still need on-watch measurements**. The device remains on
0.3.7 for its battery comparison. See [phone details](time.md#phone-connections-and-music-candidate-039).

## Drain report and InfiniTime-style candidate (2026-10-09)

The user reported approximately 30% remaining after 30 hours on 0.3.4, with
Touch to wake Off and the watch asleep throughout. The cause is **not proven**.
The changed voltage curve prevents a direct percentage comparison across older
versions, but does not establish that this is merely an estimation difference.

Candidate **0.3.6** replaces the previous sleep implementation with the same
basic input/sleep model used by InfiniTime: low-power GPIO PORT interrupts and
a tickless CPU wait. The intermediate 0.3.5+1 candidate rolled back to 20 ms
button polling for comparison; 0.3.6 supersedes that candidate.

### Input and CPU wait

- Button and touch share a real GPIOTE PORT handler. All high-accuracy GPIOTE
  IN channels are disabled. InfiniTime likewise sets `hi_accuracy=false` in
  its [input configuration](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/systemtask/SystemTask.cpp).
- BUTTON_OUT stays high, as in InfiniTime. A button press wakes immediately;
  a 20 ms stable-release debounce prevents contact bounce from toggling twice.
  Latched short pulses survive until the handler processes them. GPIO SENSE is
  rearmed to the opposite level so a held button or touch IRQ cannot cause an
  interrupt storm. LDETECT retains pulses; SENSE is disabled before clearing
  LATCH because an active SENSE condition prevents clearing it.
- TinyGo's built-in handler only services IN channels. A dedicated PORT handler
  is installed in a 256-byte-aligned RAM vector table, preserving other vectors.
  NimBLE later copies the current table and preserves this handler. Do not use
  `machine.Pin.SetInterrupt` alongside this driver. The ISR only acknowledges
  hardware and records flags; it does not allocate, call buses, or run UI code.
- Like InfiniTime's [FreeRTOS tickless port](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/FreeRTOS/port_cmsis_systick.c),
  the wait masks interrupts globally during the check/WFE handoff and uses
  SEVONPEND. Pending interrupts wake the CPU and are dispatched when the mask
  is restored. GPIOTE stays enabled in the NVIC; there is no temporary GPIOTE
  masking or button-circuit pulsing as in 0.3.4.
- RTC2 supplies a one-shot deadline. RTC0 remains NimBLE's and RTC1 remains
  TinyGo's. The deadline is the earliest application, display timeout, debounce,
  vibration, or watchdog deadline. With a normal inherited watchdog and no
  other work, idle wakes once per second; shorter watchdog timeouts use one
  quarter of their reload period. Without a running watchdog, a four-minute
  cap bounds RTC wrap handling. Holding the physical button stops watchdog
  feeding for recovery.
- Active touch contact uses 2 ms tracking, and an active/draining BLE session
  uses 20 ms cooperative runtime sleeps in 0.3.6/0.3.7 (replaced in local 0.3.9). Neither interval applies to an idle
  sleeping watch with touch wake Off and radio stopped. The direct wait assumes
  the current single Go application task; adding background goroutines requires
  scheduler integration.

InfiniTime's system task also runs housekeeping every 100 ms for motion,
watchdog service and time persistence. goPine has no motion sampling, so that
period is not copied. Alarms, countdowns and pending saves retain their own
wake deadlines. System ON sleep keeps RTCs and interrupts available; this is
not System OFF.

### Peripheral findings

The comparison found missing LCD/SPI pin shutdown and confirmed the LCD timing
guard needed by the earlier candidate:

- After LCD SLPIN, wait for settling, disable/reset SPIM0, and disconnect SCK,
  MOSI, MISO and the LCD D/C digital input buffers. Fully restore SPI and D/C
  before wake. InfiniTime does this in
  [SpiMaster::Sleep](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/drivers/SpiMaster.cpp)
  and [St7789::Sleep](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/drivers/St7789.cpp).
  Chip selects stay high and LCD reset stays deasserted.
- Guard SLPIN for 125 ms after SLPOUT, and allow at least 5 ms of settling after
  either command (with rounding margin), following InfiniTime. TinyGo's driver
  does not enforce the wake-to-sleep guard. This matters for rapid toggles but
  is a weak explanation for a watch left asleep throughout the reported run.
- Retain Nordic's SPIM POWER reset/readback sequence from candidate 0.3.5.
  Clearing ENABLE alone misses
  [nRF52832 anomaly 89](https://docs.nordicsemi.com/r/bundle/errata_nRF52832_Rev2/page/ERR/nRF52832/Rev2/latest/anomaly_832_89.html),
  which documents 400–450 µA excess current with SPIM/TWIM EasyDMA and GPIOTE.
  Only SPIM0 is reset; board I2C uses TWI1, not TWIM0.
- Existing touch deep sleep, per-transaction I2C gating, external flash sleep,
  and unused sensor shutdown already cover the corresponding InfiniTime paths.
  TinyGo's internal I2C pull-ups remain configured; InfiniTime uses external
  pull-ups. Idle lines are high, so this is not evidence of a continuous drain.
- InfiniTime clears pending FPU exceptions before WFE. This target uses software
  floating point and TinyGo disables the FPU at startup, so its FPSCR access
  sequence is not applicable here.

These are source/specification findings, not current measurements. In
particular, the LCD timing gap predates 0.3.4 and SPI disable was added in 0.3.3.
They do not prove why 0.3.4 appeared worse. Measure sleep current after ordinary
sleep, rapid toggles, and Bluetooth sync before claiming the regression fixed.

## Touch to wake

Settings → Display has a **Touch Wake: Off/On** toggle in 0.3.15 (directly under Settings in 0.3.14). It defaults to Off, including
when loading existing settings. Back remains available through the top-left
arrow or a right swipe. Touch works normally while awake. With the setting On,
the waking contact is consumed instead of activating a control.

With the setting Off, screen sleep resets the CST816S and writes `0x03` to
register `0xA5`, following [InfiniTime's touch driver](https://github.com/InfiniTimeOrg/InfiniTime/blob/main/src/drivers/Cst816s.cpp).
GPIO SENSE for touch is disabled, its latch and software flags are cleared, and
queued contact state is discarded. No GPIOTE IN channel is used.
Wake resets and configures touch again. Sleep writes have bounded
retries; failure leaves reset asserted and interrupts off until wake retries
initialization. A touch fault does not prevent button or timer wake.

The saved flag occupies previously reserved byte 144 in the existing
CRC-protected journal record. Old records contain zero there, so upgrades
preserve alarms/timers and select Off. Older firmware rejects records with
Touch to wake enabled and may restore an earlier snapshot, or defaults if no
compatible snapshot remains. Select Off and allow the save to finish before
downgrading to a firmware version without this setting if current state matters.

## Other activity

| Component | Audited behavior / change |
| --- | --- |
| LCD/backlight | Switches all three backlight channels off. LCD sleep respects a 125 ms wake guard. SPIM0 is disabled and power-cycled (anomaly 89); SPI and D/C pins are disconnected until wake. |
| Touch I2C | TinyGo leaves TWI1 enabled after transfers. Now enabled only for a transaction and disabled afterward, including errors. No touch polling or reads while asleep with touch wake Off. |
| Battery | Samples every five seconds awake in candidate 0.3.7, with no periodic samples while asleep; button/touch/alert wake refreshes immediately. Pending settings writes take a fresh sample only at the write boundary. SAADC is disabled between completed conversions. |
| Application loop | Sleep skips rendering, stopwatch/countdown animation, minute redraw deadlines, update-page expiry redraws, and closed time-sync-page polls. Timer and alarm deadlines still take precedence. Pending settings retain their two-second save deadline independently of battery sampling. |
| Side button/watchdog | Low-power PORT interrupts with BUTTON_OUT high. RTC2 waits until work is due or watchdog service is needed (normally 1 s). Held presses inhibit feeding. Active contact uses 2 ms tracking; local 0.3.9 BLE uses queue/callout deadlines and IRQ wakeups. |
| Bluetooth | Starts for explicit time/weather sync or opt-in phone mode in local 0.3.9. Sleep cancels standalone time/weather windows; phone sessions survive sleep. Off, expiry or error stop the session; existing teardown disables radio/RNG and releases HFXO. Continue pumping through asynchronous disconnect, then skip the pump once the host is stopped. RTC0 remains allocated after first use; no changes to its ownership. Verify current returns to baseline after repeated connection windows. |
| External flash/UART/regulator | Already sends external flash deep-power-down at boot, disables UART0, and enables DC/DC. |
| Vibration | Already off outside bounded alert pulses; sleep dismissal and error exits turn it off. |
| Motion/heart-rate sensors | At boot, disable HRS3300 LED drive and clear its enable bit. Recognize BMA421/425 IDs (0x11/0x13), soft reset to discard inherited features/step counting, disable acceleration, enable advanced power save, and disable FIFO self-wakeup. No recurring sensor reads. Transfers are bounded by the TinyGo TWI timeout; attempt both sensors independently and allow the watch to boot if one is absent/unresponsive. |

During idle sleep the application has no periodic refresh or ADC deadline.
Necessary alarm/countdown/snooze and pending-save deadlines still apply.
A low-power settings-write rejection defers saving until another event instead
of creating an ADC retry timer. Unused heart-rate/motion sensors receive
shutdown commands once at boot; they are never enabled for measurement or polled.

Sensor register sequences follow the pinned InfiniTime
[HRS3300 driver](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/drivers/Hrs3300.cpp)
and [BMA driver/API](https://github.com/InfiniTimeOrg/InfiniTime/tree/6c119eb52206b580b556b41633dddc1e1b66a8da/src/drivers/Bma421_C).
GPIO reference: [Nordic GPIOTE PORT](https://docs.nordicsemi.com/r/bundle/ps_nrf52832/page/gpiote.html).

## Awake work in candidate 0.3.7

The optimization pass reduces recurring work without changing brightness or
timer/contact responsiveness:

- Battery sampling changes from 1 s to 5 s: 80% fewer scheduled ADC reads.
  A 15-second awake-loop test sees three samples (boot, 5 s, 10 s). Wake and
  alert samples and pre-write safety checks remain immediate. Charging and
  battery indications can lag up to five seconds while already awake.
- Stopwatch/countdown ticks and hold feedback rasterize and checksum only
  their changing horizontal bands. Page/control changes and display wake still
  repaint fully; unchanged display strips still skip SPI transfer.
- Settings comparisons no longer allocate a 288-byte temporary each loop on
  TinyGo, reducing allocation/GC work both awake and during idle wakeups.
- CRC lookup data moves to flash, avoiding the standard implementation's 8 KiB
  runtime heap table and 1 KiB static RAM table. This buys memory at the cost of
  slower full-frame checksums in host tests; watch CPU/current effects need
  measurement. See the [measured resource and CPU tradeoffs](performance.md).

These changes do not establish a battery-life gain or resolve the reported
sleep-drain regression. Version 0.3.7 was uploaded on 2026-10-09; the user
confirmed boot and tapped KEEP. A useful device
comparison includes a static clock, running stopwatch, repeated page changes,
and ordinary sleep with identical brightness, timeout and radio history.

## Battery voltage curve

Use the six-point fit from Finlay Davidson's measurements on **three PineTimes**,
adopted by InfiniTime in
[commit 8b0d8889](https://github.com/InfiniTimeOrg/InfiniTime/commit/8b0d888952bb3cfbf587ab20d5096f2e578a6107).
The [methodology and CSV datasets](https://gist.github.com/FintasticMan/72d3aa1fc5c7cb9739f45b81197836c7)
include both intensive and light-load runs. This is more appropriate to the
watch's listed 170–180 mAh, 3.8 V LiPo than a generic lithium curve.

| Battery voltage | Estimated remaining |
| --- | --- |
| 3.500 V | 0% |
| 3.616 V | 3% |
| 3.723 V | 22% |
| 3.776 V | 48% |
| 3.979 V | 79% |
| 4.180 V | 100% |

Interpolate linearly between points, clamping outside the range. These are
empirical estimation points, not charging limits or a cutoff controller. The
study reconstructed voltages from InfiniTime's former linear percentage reports
and fitted normalized remaining runtime under constant loads; it did not measure
charge with a coulomb counter. It is not calibrated to this individual watch,
cell age, temperature, or goPine's load. The percentage may change after this
update without any change in actual energy. Keep the existing voltage conversion
and smoothing; judge power savings using controlled runtime/current comparisons.

An [older 2019 PineTime dataset](https://forum.pine64.org/showthread.php?tid=8147)
was also reviewed. It used a lit screen, maximum backlight and an attached
debugger, with a substantially lower measured full voltage. It is not used for
the new estimator.

## Validation

Candidate 0.3.6 passes `go test ./...`, `go vet ./...`, the resource-checker
regression tests, the standalone PineTime build, and
`scripts/build-ota.sh --ble 0.3.6` with TinyGo 0.42.0. The linked OTA image is
215,252 bytes with a 44,712-byte heap region and a 40-byte PORT ISR frame.
The resource checker enforces vector-table alignment and the ISR frame budget.
Disassembly inspection verifies the RAM vector installation references the
PORT handler with its Thumb bit set, and that the handler does not allocate.
These checks cover both standalone and MCUboot builds; they do not execute the
physical interrupt controller. On 2026-10-09, the complete 0.3.6 OTA image was
uploaded through recovery and passed receiver validation. An activation/reset
command was sent, but its acknowledgement was unavailable. User confirmation
of boot and KEEP is pending; do not retry the transfer solely for the missing ACK.
Package SHA-256: `f8a10b28252ae6770fc9f9a8f82814c0ac9586f28e9e146c5150b9ddda605a47`.
Transfer log: `build/ota/upload-0.3.6-20261009-163346-145348.log`.

Host tests cover default/toggle/repaint, both persisted choices, old-record
compatibility, malformed flag rejection, touch sleep command/retries, SDL
button and touch wake, consumed wake taps, zero periodic battery samples through 12 hours of sleep,
timely power-checked saves without low-power retry loops, sensor failures/unknown IDs, LCD wake/sleep timing, and
existing sleeping alarm/countdown/snooze behavior. New input tests model short
button/touch pulses, held inputs, rearm races, contact bounce and RTC wrap;
scheduling tests cover alarm/refresh, screen timeout, debounce, vibration and
radio deadlines with no periodic idle button poll. These do not simulate
the physical buses, GPIO interrupt hardware, or current consumption.

On a watch, compare sleep current with touch wake Off and On; test repeated
button wake, the first touch after wake, brief presses and held-button recovery,
alarm/countdown wake and dismissal, reboot persistence, and sleep after time
sync. Measure with the debugger disconnected. Confirm charging indications
refresh on wake and touch reinitializes reliably after many sleep cycles before
claiming a battery-life improvement.

The 0.3.4 candidate was installed by OTA on 2026-10-07;
the user confirmed boot and tapped KEEP. Sleep current has not been measured.
The replacement sleep implementation, now installed in 0.3.7, still needs sleep-current/runtime comparison
with and without prior Bluetooth sync; the reported regression is not yet
confirmed resolved.


## Screen orientation (candidate 0.3.15)

Settings → Display → Flip Screen rotates the LCD 180 degrees using ST7789 MADCTL
and maps touch coordinates and swipe directions to match. It defaults to Off,
is saved in journal byte 145, and survives sleep and reboot. Rotation is sent
only when the setting changes (including boot restoration), with a complete
repaint; there is no per-pixel rotation or extra sleep polling on the watch.
A setting change discards the current contact and queued touch events.
Older firmware rejects snapshots with this reserved byte set. Turn Flip Screen
Off and wait for saving before downgrading if the latest settings must carry back.
