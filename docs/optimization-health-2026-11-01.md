# Health and notification resource audit — 2026-11-01

Baseline: local 0.3.38+4 candidate, including clickable menus and iPhone dismissal
history. Optimized candidate: 0.3.38+5. Same TinyGo 0.42.0 BLE target and pinned
NimBLE dependency. These are build/static and host results, not measured watch
current, physical stack high-water marks or live free-heap measurements.

| Metric | Before | After |
| --- | ---: | ---: |
| MCUboot image | 308,444 B | 307,564 B |
| Main loop stack frame | 3,192 B | 2,328 B |
| Largest selected core stack path | 6,048 B | 5,184 B |
| Remaining task stack after 2,048 B deeper-call reserve | 96 B | 960 B |
| Health history object | 848 B | 592 B |
| Heap address-space region | 43,660 B | 43,660 B |

The permitted image size is 471,040 bytes (`ota.MaxImageSize`), leaving 163,476
bytes (159.6 KiB, 34.7%) for image growth. The heap region is an allocation pool,
not free RAM: it also contains runtime, UI, renderer and optional feature objects.
The task stack is 8,192 bytes. The stack checker covers selected paths with a
separate 2,048-byte reserve; it is not a full call-graph proof.

## Changes

- Periodic HR stops immediately after its estimator accepts a reading, with
  the existing 30-second timeout for unsuccessful attempts. The 5/15/30-minute
  start cadence is retained. Continuous tracking remains at 10 Hz.
- Heart-rate and pedometer setting changes restart only the affected sensor.
  In particular, changing HR mode no longer reloads the Bosch step firmware.
- Early-return sensor polling before the next deadline; off mode has no added
  sensor deadline. Periodic gaps and the hardware step counter let the MCU sleep.
- Unchanged BPM/step values do not invalidate Health frames. Graph frames use
  their existing history revision and clock-minute boundaries.
- Move transient render comparisons into a separate stack frame so their
  storage does not remain live beneath settings saves, radio and sensor calls.
- Normalize/order the 64 correlation inputs once; use bounded 32-bit sums in
  the inner loop and 64-bit covariance arithmetic only at the end of each lag.
  A golden test matches the previous arithmetic across 1,024 windows (656
  accepted, 368 rejected), including ring offsets, varied periods and noise.
- Store history minute stamps in uint32: comfortably within the watch's
  supported calendar, retaining all 64 slots and actual minute spacing.
- Remove temporary display strings/formatter escapes. Formatting happens only
  for visible strips and uses caller-owned fixed buffers. This covers Health,
  graph settings, diagnostic numeric rows and graph axis numbers.
- Extend the normal BLE build's allocation gate to these selected health hot
  paths. The TinyGo compiler reports zero allocations there. Initial health and
  history object creation remains intentional and bounded.

## Evidence

Host benchmarks (Ryzen 9 7950X; median of three runs; not on-watch timings):

| Benchmark | Before | After | Change |
| --- | ---: | ---: | ---: |
| Correlation estimate | 1,055 ns | 729 ns | 31% less time |
| Poll before sensor deadline | 13.59 ns | 5.53 ns | 59% less time |

Both benchmarks report 0 B/op and 0 allocations/op. Host tests also verify zero
allocations for steady sensor polling and all four new health/graph UI paths.
An initial audit caught 60 allocations per Health frame, 30 per graph-options
frame and 150 per diagnostic frame; these checks now pass at zero. A separate
TinyGo audit caught slice-buffer escapes missed by the host compiler; the final
normal build allocation report is empty for selected health hot paths.

Power-transition tests exercise early successful shutoff, periodic restart
cadence, independent sensors, failed-shutdown retry and update suspension.
Existing no-periodic-flash-write and notification allocation/replay tests pass.
All Go tests, vet, the resource-checker tests and the firmware build pass.
Five health/graph screenshots are byte-identical to the previous rendering.

Artifacts under ignored `build/ota/`: `optimization-health-baseline.json`,
`optimization-health-before.txt`, `optimization-health-after.txt`,
`gopine-resources-0.3.38+5.json`, `gopine-allocations-0.3.38+5.log` and the
`gopine-update-0.3.38+5.zip` candidate. Live battery/runtime improvements still
need a watch trial with comparable settings and traffic.
