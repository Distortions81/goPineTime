# goPine

goPine is firmware for the **PineTime smartwatch**, written in Go with
[TinyGo](https://tinygo.org/). It combines a readable digital watch face with
alarms, timers, and optional Bluetooth phone features.

[Update your watch](https://distortions81.github.io/goPineTime/) ·
[Download firmware](https://github.com/Distortions81/goPineTime/releases/latest) ·
[Release notes](docs/releases/)

## Features

- 12/24-hour clock with manual date and time settings.
- Five alarms with snooze, a stopwatch with laps, and a countdown timer.
- Battery indicator and a charging screen.
- Light and dark themes, three brightness levels, screen rotation, and optional
  tap to wake.
- Saved settings, alarms, and timer state.
- Optional phone time sync, notifications, and music controls over Bluetooth,
  with up to two saved paired devices.
- Firmware updates over Bluetooth, confirmed on the watch.

Phone features depend on your companion app and its permissions. See the
[phone setup guide](docs/pairing.md) for compatibility and limitations.
The Weather app is currently disabled. Battery percentage is a voltage-based
estimate; battery runtime is still being evaluated.

## Using the watch

Swipe **right** from the clock to open **Apps**, or **left** to open **Settings**.
Press the side button to wake the display or turn it off. The screen sleeps
after 15 seconds; alarms and countdown alerts wake it automatically.

Bluetooth and tap to wake are off by default. Enable them in
**Settings → Bluetooth** and **Settings → Display**. Set the clock manually in
**Settings → Time & Date**, or sync it through a paired phone.

## Updating

The [browser updater](https://distortions81.github.io/goPineTime/) works with
**goPine 0.3.22 or newer already installed** and a browser that supports Web
Bluetooth.

1. Open the updater and wait for the firmware to show **Checked**.
2. On the watch, open **Settings → Firmware Update** and hold the control until
   it shows **Ready to connect**. Use at least 20% battery or connect the charger.
3. Connect to **goPine Update** in the browser and keep the page open during transfer.
4. After verification, tap **INSTALL** on the watch, then **KEEP** after reboot.

For browser compatibility and troubleshooting, see the
[updater guide](docs/web-updater.md). Installing from InfiniTime, older goPine,
or a standalone build requires the appropriate
[initial setup or migration](docs/ota.md). Native goPine update packages cannot
be installed through InfiniTime recovery.

## Try it on your desktop

With Go 1.27.1 and SDL2 development libraries installed, run:

```sh
go run .
```

Use the mouse for taps and swipes, and Space for the side button.

For firmware builds, recovery, and technical details, see the
[OTA guide](docs/ota.md), [power notes](docs/power.md), and
[performance notes](docs/performance.md).
