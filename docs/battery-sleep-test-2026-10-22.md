# goPine sleep battery test — October 22, 2026

Test started **October 22, 2026 at 10:04:27 PM MDT** (`America/Denver`, UTC−06:00).
UTC baseline: `2026-10-23T04:04:27Z`.

Starting battery: **100%**, reported by the user. The watch will remain asleep.
Bluetooth stays off, continuing the agreed sleep-test configuration. Firmware
under test is the locally prepared **0.3.35** candidate uploaded and verified
immediately before this test; see [the candidate record](releases/0.3.35.md).
The user has not separately confirmed INSTALL/boot/KEEP, so those remain
unconfirmed rather than inferred from the battery report.

Check-ins are scheduled for **9:00 AM, 3:00 PM, and 9:00 PM America/Denver** daily,
starting October 23, via the active chat automation `watch-sleep-battery-check-ins`.
Readings are user-reported; the check-ins do not connect to
Bluetooth or wake the watch remotely. Record any charging, extended screen use,
Bluetooth activation, restart, or firmware change alongside the reading.

| Reading time (America/Denver) | Elapsed | Battery | Notes |
| --- | --- | --- | --- |
| 2026-10-22 22:04:27 MDT | 0 h | 100% | User-reported baseline; watch entering sleep; Bluetooth off. |

For later readings, record the observation time rather than the reminder time.
If the user supplies no observation time, use the report time and label it.
Calculate elapsed time from the UTC baseline. Percentage-point loss per day is
a trend from the voltage gauge, not a calibrated full-discharge prediction.
Keep missed readings blank; do not invent values. Charging or firmware changes
end this comparison interval and need a new baseline.
