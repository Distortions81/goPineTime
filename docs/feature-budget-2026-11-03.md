# Daily features and device budgets — 2026-11-03

Implemented on `codex/budgeted-daily-features`: quick controls, persistent manual
quiet and scheduled quiet hours, a 32-point RAM battery-voltage history, and
measurement-first sleep/typical-use runtime estimates.
Daily step persistence and new heart-rate modes remain deferred for the reasons
below. The watch received and verified 0.3.38+16 on November 3. INSTALL/KEEP and
the full-charge unplug baseline are awaiting user confirmation; no measured
sleep or typical-use runtime has been established yet.

## Fresh measurements

Baseline is `dc834f7`, built as 0.3.38+8. The candidate is 0.3.38+16. Both use
TinyGo 0.42.0, the BLE MCUboot target, the pinned InfiniTime dependency, and the
same firmware date/time seed. These are ARM ELF/compiler and host-test results,
not measured watch free heap, stack high-water marks, CPU time, or current draw.

| Metric | Baseline | Candidate |
| --- | ---: | ---: |
| Packaged MCUboot image | 307,892 B | 314,612 B |
| Image allowance | 471,040 B | 471,040 B |
| Unused image allowance | 163,148 B | 156,428 B |
| Heap address-space region | 43,660 B | 43,660 B |
| Task stack | 8,192 B | 8,192 B |
| Main loop stack frame | 2,328 B | 2,336 B |
| Largest checked core path | 5,184 B | 5,224 B |
| Deeper-call/interrupt reserve | 2,048 B | 2,048 B |
| Margin after that reserve | 960 B | 920 B |
| GPIO interrupt frame | 48 B | 48 B |
| UI object | 680 B | 696 B |
| Settings persistence object | 640 B | 640 B |
| Journal object | 208 B | 208 B |
| Notification object, when used | 1,528 B | 1,520 B |
| Battery history + usage object | none | 364 B |
| Embedded usage model (included above) | none | 88 B |

The image grows by 6,720 bytes (6.6 KiB). The added owned live objects total
380 raw bytes: 364 for the ring/usage state and 16 for the UI. When the inbox
exists, its object saves eight bytes. Allocator rounding and GC metadata are additional;
the unchanged heap region is an allocation pool, not a free-memory reading.
The 8 KiB task stack is also allocated from that pool.

The initial addition enlarged the permanent loop frame by 96 bytes. Moving the
existing power sampling and history timestamp into a separate non-inlined
`samplePower` call reduced it to 2,320 bytes. The measurement hooks bring the final
frame to 2,336 bytes, eight above baseline. The checker includes both new paths.
No stack size, radio buffers, estimator windows, notification capacity, or
display-strip size were reduced to make the features fit.

## Enforced budgets

The normal BLE build now rejects:

- Images above 320 KiB, deliberately below the physical 460 KiB allowance.
  This leaves 140 KiB outside the working growth budget. The candidate has
  13,068 bytes remaining inside that budget.
- Checked stack paths leaving less than 512 bytes beyond the existing 2 KiB
  reserve. The task stack remains 8 KiB. This is a selected-path regression
  check, not a complete bound for indirect calls, interrupts or recursion.
- UI, settings persistence, and battery-history/usage objects above 704, 656, and
  384 bytes respectively; the embedded usage model is also capped at 96 bytes.
  Missing expected DWARF sizes fail the check. These object caps allow at most
  408 raw bytes of new owned state versus baseline, below a 512-byte working limit.
- Heap regions below the existing 40 KiB minimum, selected steady-state
  allocation regressions, or the previously banned generic formatter,
  heavyweight checksum and runtime RNG dependencies.

## CPU, power and writes

Quick controls use existing fonts and the strip renderer. Brightness and touch
controls share their handlers with Display settings. Returning from confirmation
preserves the source screen. Vertical swipes use the existing touch decoder and
mailbox, including rotation and stale-contact suppression; there is no new poller.

Quiet-hour decisions use constant-time civil-hour comparisons at notification
receipt and motor service. They add no boundary timer. A 150 ms pulse crossing
the start hour is shortened using its existing motor deadline. Manual quiet works
without a clock; schedules wait for a trusted clock. No backlog buzzes at the end.
Both notification transports retain messages, while alarms keep their own motor
policy. The two quiet-preference bytes occupy the journal's final reserved bytes:
160-byte records, the two existing pages, and coalesced user-edit saves remain
unchanged. Time passing, scheduled transitions, and voltage samples never write
flash or force a BLE disconnect for a save.

The battery ring only receives readings already taken at boot, wake/power events,
and the existing five-second awake polling deadline. Each sample is eight bytes;
insertion and coalescing are O(1). Drawing visits at most 32 points in visible
strips. Runtime-minute stamps are independent of civil-time edits. The plot uses
a fixed 3.4–4.3 V scale and leaves long sleep gaps disconnected. Markers capture
observed charger transitions and brightness, touch wake, Bluetooth, tracking,
manual quiet and schedule-enable changes; they do not measure individual loads.

Selected TinyGo draw/quiet/insertion paths and host steady-state tests report no
allocations. First-use history allocation is intentional and bounded. Three host
benchmark runs on a Ryzen 9 7950X had medians of 9.36 ns for a coalesced battery
sample including usage accounting and 6.56 ns for a scheduled-quiet decision,
both 0 B/op. These are host regression aids, not Cortex-M4 CPU timings or
battery-life estimates.

## Sleep and typical-use learning

The user requested two estimates: remaining time if the watch stays asleep, and
days per charge under their observed normal use. The 88-byte usage state is
embedded in the existing history allocation. It observes existing display
sleep/wake events and power samples, not an added polling task. Screen-off
intervals include the normal configured BLE/sensor background work; this is not
a CPU-deep-sleep current measurement.

Sleep spans must be at least four hours, have at most 30 seconds of screen-on
time, and show at least five percentage points of drop with declining voltage.
The model waits 30 minutes after boot/unplug or configuration changes, uses the
15–90% region, rejects invalid ADC values, and excludes firmware-update activity.
It needs at least three independent spans, 24 hours of accepted sleep and 15
points of total decline before displaying a range. Background touch, Bluetooth,
sensor and notification policy/schedule changes reset sleep learning; brightness
changes discard the current span without changing the learned asleep-load model.

Typical use requires two observed near-full unplugs (at least 90%) followed by
discharge to 25% or less, at least 60 points of drop per cycle, and at least eight
hours per cycle. Unknown unplug times at boot do not manufacture cycles. The
model records at most one result per charge and excludes cycles interrupted by
firmware-update activity or invalid ADC readings. Average screen-on minutes/day
comes from those cycles when present, and otherwise the observed time since boot.

Sleep remaining scales the observed seconds-per-percentage-point range by the
current percentage above a 10% reserve. Typical full-charge use projects the
cycle rates onto 90 usable percentage points. Endpoint tolerance of +/-2 points
widens each observed rate range, but does not bound systematic voltage-to-capacity
error or future changes in traffic/use. These are explicitly rough, experimental
estimates, not statistical confidence intervals or guarantees. No actual watch
runtime estimate has been established during this work. Counters are fixed size;
after 255 observations, accumulated weights halve without allocating storage.

For a first sleep trial, keep the chosen background settings fixed. After at
least 30 minutes unplugged and once below 90%, wake briefly to record a rested
baseline, leave the screen asleep for 8–12 hours, and wake briefly again. Repeat
until enough qualifying drop and spans exist; do not keep waking it to inspect
progress. Typical-use learning needs keeping this build through two deep charge
cycles. Everything currently resets on reboot; persistence remains a separate
storage/migration task. The previews use synthetic test data, not live measurements.

## Remaining feasibility decisions

| Proposal | Decision and limit |
| --- | --- |
| Daily steps, goal and seven daily totals | Feasible as bounded tracking state; aim for at most 128 B for the counter/history itself, then measure journal and temporary-buffer costs separately. No per-step writes or new minute wakeups. Implement durable storage as its own reviewed slice. |
| Durable step history | The current settings record has no reserved bytes remaining. A larger/versioned journal needs migration and interrupted-write tests, new stack budgets, and an explicit write/loss policy. External storage would also need an audited partition and shared-SPI ownership. Do not reuse OTA, recovery or bond regions. |
| Battery history/learning across reboots | Defer persistence until the storage design is reviewed. Initial estimates are experimental, gated by sufficient observed sleep spans or deep cycles, with no writes or sleeping ADC polls. They are not measured energy use or guaranteed runtime. |
| On-demand heart rate | Bounded measurement can reuse the existing health state, 64-sample estimator and 30-second window. Defer new behavior until the unresolved optical acquisition is characterized using wrist captures; synthetic estimator tests do not prove a working sensor. See `heart-rate.md`. |
| Larger HR buffers, continuous tracking or a new signal-processing pipeline | No budget assigned. First establish why current acquisition is failing, then compare captured data offline before adding work or RAM on the watch. |

Old settings records still load with scheduling/manual quiet off. Firmware predating
these fields rejects snapshots with nonzero new preference bytes and may fall back
to an older compatible record on a manual downgrade. This is not a guarantee that
new preferences or the latest settings survive a downgrade.

## Validation and artifacts

Passing checks: all Go tests and vet, the 25 resource-checker tests, the five
release-asset tests, and the BLE firmware build/resource/allocation gate. New tests
cover every start/end hour and minute in two runtime zones, civil-clock offsets,
unset/approximate clocks, both notification transports, alarm delivery, pulse
cutoff at the quiet boundary, persistence and interrupted writes at every word,
ring eviction/coalescing, charging transitions, gesture rotation/cancellation,
confirmation origin, overlays, and zero allocations. Existing long-sleep tests
still observe only the boot sample during 12 hours asleep and one sample on wake.
New screens were rendered and inspected in dark and light themes. Usage previews
use synthetic data, not watch measurements. Usage tests also cover long-sleep
accounting without added wakeups, minimum evidence, screen-on contamination,
voltage rebound/invalid ADC, charger/update exclusion, configuration changes,
stale timestamps, reserve handling, independent cycles, and bounded counters.

Ignored local artifacts:

- `build/feature-budget/baseline-resources.json` and baseline build/audit logs.
- `build/ota/gopine-resources-0.3.38+16.json`, allocation log, ELF and update ZIP.
- `build/feature-budget/screens/` for simulator screenshots.
- `build/battery-data/20261103-090255/` for the frozen source snapshot, firmware
  identity, transfer evidence, and pending trial baseline/readings.

Rebuild using the pinned toolchain (set `TINYGO` if the shell default differs):

```sh
FIRMWARE_TIME=00:00:00 FIRMWARE_DATE=2026-11-03 \
TINYGO=/path/to/tinygo-0.42.0/bin/tinygo \
bash scripts/build-ota.sh --ble 0.3.38+16
```

Before release, exercise the controls and quiet hours on the watch with an active
phone connection, alarms/countdown, sleep/wake, charging and rotation. Check that
settings saves reconnect correctly and no unexpected resets occur. Physical
stack/heap margins and comparable idle current remain unmeasured; a passing link
and these static budgets do not establish them.
