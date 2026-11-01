# Heart-rate investigation, 2026-11-01

Two watch trials ran the LED without returning BPM. Synthetic estimator tests
passed, but do not establish hardware acquisition or real optical accuracy.
The cause is not established yet; do not label this diagnostic build a fix.

## Existing implementations checked

- InfiniTime at our pinned `6c119eb52206b580b556b41633dddc1e1b66a8da`:
  [HRS3300 driver](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/drivers/Hrs3300.cpp),
  [acquisition task](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/heartratetask/HeartRateTask.cpp),
  and [PPG processing](https://github.com/InfiniTimeOrg/InfiniTime/blob/6c119eb52206b580b556b41633dddc1e1b66a8da/src/components/heartrate/Ppg.cpp).
  It uses 100 ms acquisition, a 64-sample FFT-based pipeline, ambient-light
  checks, and a startup settling delay. Register settings are ENABLE 0x50/0xD0,
  PDRIVER 0x2F, RES 0x77 and HGAIN 0x00. Our active configuration matches these.
- Wasp-OS at `9128cdb675e52ee4899fec1dc0858bc01c28180f`:
  [driver](https://github.com/wasp-os/wasp-os/blob/9128cdb675e52ee4899fec1dc0858bc01c28180f/wasp/drivers/hrs3300.py),
  [PPG processing](https://github.com/wasp-os/wasp-os/blob/9128cdb675e52ee4899fec1dc0858bc01c28180f/wasp/ppg.py),
  [heart application/debug logging](https://github.com/wasp-os/wasp-os/blob/9128cdb675e52ee4899fec1dc0858bc01c28180f/apps/heart.py).
  It uses different ADC/LED settings, 24 Hz acquisition, band filtering and
  waveform comparison. Its debug path saves raw HRS values for analysis.
  Do not mix its sampling settings with InfiniTime's timing without retesting.
- [Rust HRS3300 driver](https://github.com/eldruin/hrs3300-rs/blob/master/src/device_impl.rs)
  exposes device identity, oscillator enable, gain and both raw channels.
- [Manufacturer datasheet hosted by Pine64](https://files.pine64.org/doc/datasheet/pinetime/HRS3300%20Heart%20Rate%20Sensor.pdf):
  device ID 0x21; HEN and oscillator enable are independent; data registers
  auto-increment. Gain/current and reserved-bit behavior must be distinguished.

## Diagnostic changes

- Verify device ID and documented configuration bits after startup. Ignore
  reserved bits during validation but display their actual readback values.
- Add InfiniTime's 100 ms disabled settling interval and input configuration
  for the unused sensor IRQ; keep active current and gain unchanged.
- Read HRS and ambient light in one 0x08–0x0F transaction.
- Keep a bounded 64-sample raw waveform, ADC extrema, total samples, invalid
  samples, long gaps, longest gap and accepted BPM count, independent of the
  estimator's own buffers. No diagnostic flash writes or background radio.
- Open the diagnostic screen by tapping the BPM line; it uses the shared back
  button and does not itself enable tracking or keep the screen awake.

## Next physical check

Enable periodic tracking while wearing the watch, then tap its BPM line. After
the attempt, inspect ADC range, sample count, largest gap and register readback.
Roughly 300 samples over 30 seconds with approximately 100 ms gaps indicates
that scheduling and reads are occurring; this alone does not prove pulse quality.
Zero/constant/saturated data, setup mismatch or large gaps identify different
failure paths. A clean changing waveform with no accepted BPM calls for replaying
captured wrist data against the reference signal-processing pipeline.
