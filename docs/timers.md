# Clock tools and persistence (0.3.0)

Swipe right from the clock to open **Apps**. Swipe left opens **Settings**.
Apps groups Alarms, Stopwatch, Timer, Music, and Messages; Weather is temporarily
disabled. The Timer button opens the countdown controls described below.
Back or swipe-right returns to the parent screen. Sleep discards an unfinished
alarm or countdown edit. Stopwatch and countdown continue while navigating or
while the screen is off.
Labels and warnings use 18px or larger fonts. Editor units are shortened to fit
the available columns instead of using tiny text.

## Controls

- **Stopwatch:** Start, Pause, Resume, Lap while running, Reset while paused.
  The visible stopwatch refreshes at tenths of a second. Hidden/sleeping views
  do not request those extra refreshes.
- **Countdown:** Set Duration while paused, then Start/Pause/Resume or Reset.
  Durations range from one second to 23:59:59. The display rounds remaining
  seconds upward, and the alarm fires at the actual deadline.
- **Alarms:** Five slots, selected with Prev/Next. Edit the time, choose Once,
  Daily, or Weekdays, then Save to enable. On/Off toggles the selected slot.
  Editing or disabling clears that slot's outstanding snooze.
- **Alerts:** The display wakes and the motor pulses for 200ms each second.
  Dismiss, swipe-right, or the side button stops it. Alarm alerts also offer
  Snooze 5 Min. An alert stops after one minute; simultaneous alerts are queued.
  An alert cancels time sync, editor drafts, and an armed firmware-update hold.
  Once firmware update mode opens, alerts are deferred until it closes; an
  overdue alarm or countdown cannot interrupt a transfer or verification.

Calendar alarms follow displayed local time. A clock change reschedules them
from the new time without ringing every skipped alarm. There is no timezone or
automatic daylight-saving database. Countdown, stopwatch, and snooze use runtime
durations during a session, so ordinary clock edits do not move them.

## Reboots and an unset clock

MCUboot builds save the format choice, alarm definitions, countdown preset,
stopwatch state/lap, countdown state, snoozes, and pending/active alerts. Running
operations include calendar timestamps plus duration fallbacks. On reboot they
resume against the best available clock. Planned resets already hand off an
approximate calendar; elapsed time spent rebooting or in recovery is still
unknown until a later time setting or sync.

`watchClock.initialized` explicitly says whether a usable calendar exists.
`approximate` separately describes uncertainty. A full valid build date/time
or retained handoff gives an initialized but approximate clock. A missing date,
invalid calendar, or epoch-zero clock is uninitialized. In that state:

- Calendar alarms remain unarmed and show a time/date setup hint.
- Saved stopwatch, countdown, and snooze durations resume without interpreting
  epoch zero as the current calendar. Timers started now work normally.
- Original timestamps survive another save/reboot. Once time is set or synced,
  unchanged restored operations can reconcile against those timestamps.

The first accepted time setting/sync reconciles restored operations that still
have their original state. A subsequent pause, reset, dismissal, or edit wins;
sync cannot undo it or resurrect a delivered countdown. Operations created
without any calendar keep their running durations when the clock is initialized.
A known expired countdown produces one alert on restore. A saved one-shot alarm
is disabled after delivery. Repeating alarms schedule their next occurrence.

This is best-effort timing, not a battery-backed RTC. Build-time seeds can be
stale; approximate restores may over/underestimate elapsed time until sync.
An interrupted edit can lose the most recent unsaved changes.

## Flash storage

Clock and tool state share the existing two internal flash pages at
0x7e000–0x7ffff. The image/trailer, bootloader scratch, spare page, and InfiniTime
external filesystem are unchanged. Each committed snapshot is versioned and
CRC-protected. Rotation writes the other page before reclaiming the previous
authority. The 0.2.x clock-only format migrates on the first save; no valid old
snapshot is erased before its replacement has been verified.

Old-format headers are then retired to prevent older firmware from pairing a
stale anchor with reused retention sequence bits. Downgrading to pre-0.3.0
therefore loses access to this state and may require clock setup. The data is
not silently rewritten into an older format.

Writes happen after changes settle for two seconds, and immediately before a
planned reset or normal simulator exit. Running clocks and timers do not cause
periodic writes. Low charge defers writing (at least 20% or external power is
required). Background saves also wait for active radio sessions, ringing alerts,
and unconfirmed builds to finish.
Settings shows pending/temporary/error status. An uncertain write stops further
saves until reboot reopens the journal. Standalone/provisioning builds do not
have a persistent backend because their firmware may occupy those pages.

For a persistent SDL simulator, run:

```sh
GOPINE_SIM_STORAGE=/tmp/gopine-watch.flash go run .
```

The file is an 8 KiB simulated NOR arena. Existing files with an incompatible
size or unrecognized contents are left untouched. Without this option the
simulator's state is temporary. Space simulates the physical side button;
motor pulses appear in the window title.

## Verification

The production loop accepts a clock source and hardware adapters. `loop_test.go`
feeds scripted input and advances a virtual clock through real scheduling,
rendering, sleep/wake, timer delivery, and radio cancellation. `settings_test.go`
covers timestamp recovery, no-calendar startup, repeated reboot anchors,
resynchronization, power gating, and the shared planned-reset handoff.

```sh
go test ./...
go vet ./...
go test -run 'TestScript|TestRuntime|TestUnsetClock|TestTimersCreatedBefore' .
GOPINE_TEST_SCREENSHOTS=/tmp/gopine-screens go test -run TestDesktopControlsAndRendering .
```

Flash tests inject power cuts at every word boundary during appends, page
rotation, and migration; recovery must yield an entire old or new snapshot.
Rendering tests compare strip output with direct pixels for every tool screen.

The 2026-10-05 candidate passed the full Go suite with the race detector,
`go vet`, module checks, SDL rendering/input checks, PC sender tests, and the C
port's address/undefined-behavior sanitizers. Standalone, MCUboot, BLE MCUboot,
and provisioning/bootstrap builds all succeeded. The BLE MCUboot image is
325,724 bytes; the packaged ZIP's SHA-256 is
`f1d27a0c09483d0af3faedc7cf6df3e9628b9146cd7b57f84e67a90211f68f73`.

The new motor pulses, sleep wake-up, and state restoration still need an on-watch
smoke test after installing the candidate: start a short countdown, sleep it,
observe the wake/pulse/dismissal, snooze an alarm, and check a running timer and
format choice after a planned reboot and sync. Host simulations and successful
firmware builds do not establish the physical motor behavior or timing accuracy.

### Failed 0.3.0 hardware trial and resource audit

The 2026-10-05 Bluetooth transfer passed receiver validation, but the user
reported returning to InfiniTime without observing goPine. This is a failed
installation/boot trial, not a successful runtime smoke test. The exact reset
cause has not been captured.

Inspection of the uploaded ELF found:

- Image: 325,724 bytes, within the 471,040-byte image limit.
- RAM reserved below `_heap_start`: 23,232 bytes, including the 2,048-byte
  system stack and alignment. The heap region is 42,304 bytes; this is not a
  measurement of free runtime heap.
- Main task stack: 8,192 bytes, allocated from that heap region. The display
  strip additionally needs 5,760 heap bytes, apart from other objects/GC costs.
- ARM prologue frames: runtime goroutine wrapper 48 bytes, `main.run` 4,272,
  `watchLoop.run` 2,616, settings `flush` 1,704, journal `save` 1,328. These
  simultaneously active frames total at least 9,968 bytes on a settings-save
  path, excluding deeper calls. The save callback tail-calls `flush`, so its
  own 660-byte frame is **not** included in that total.
- Wrapper + `main.run` + loop + `tickTimers` already total 8,024 bytes before
  the timer function's further calls. The configured stack has very little
  headroom even before saving settings.

TinyGo's `-print-stacks` reports an unbounded/recursive path through
`runtime.nilPanic`, so it falls back to the configured stack; a successful build
does not prove that budget is adequate. The stack deficiency needs correction
and another resource audit before retrying the hardware trial. It is a plausible
explanation for failure, not a captured on-device fault diagnosis.

### 0.3.1 resource correction

Initialization now returns before entering the event loop, releasing its
temporary stack frame. The input handler and runtime snapshot conversion are
kept out of their callers' frames. Settings keys are computed once per save.
Journal scans reuse their record buffer and decode in place rather than
allocating a record-sized copy for every slot. An allocation regression covers
both the 510-slot legacy scan and the 50-slot current scan.

In the rebuilt BLE ELF, `main.run` uses 104 bytes, the loop 2,256 bytes, and
settings `flush` 1,032 bytes. Static RAM reservation and the 8,192-byte task
stack are unchanged. The resource checker conservatively includes the largest
application callback frame (even when tail-called) and reports 5,432 bytes for
the checked settings-save core path, versus 10,632 for 0.3.0 with that same
accounting. It requires another 2,048 bytes to remain for deeper calls/runtime
work. These selected-path budgets are not exhaustive worst-case stack bounds.

The 313,124-byte 0.3.1 candidate passed the full race-enabled Go suite, vet,
the new initialization-lifetime/KEEP/persistence test, seven resource-checker
tests, and the BLE build's ELF budget gate. Its ZIP SHA-256 is
`49c2d36be740c04ac969478c9c5e6033ed4a9fe5f44c1fd4831985073c91d820`.
On 2026-10-05, the user confirmed pressing KEEP after the 0.3.1 upload and
reported that a short countdown woke the sleeping watch and vibrated. Alarm
snooze and running-timer/settings persistence across reboot and resync still
need dedicated on-watch checks. Successful boot does not establish that the
stack deficiency was the exact cause of the previous trial's failure.
