# Browser firmware updater

The [goPine browser updater](https://distortions81.github.io/goPineTime/) transfers
firmware directly from a supported browser to the watch. It needs **goPine 0.3.22
or newer already on the watch**, with its in-app Firmware Update screen. InfiniTime
recovery uses a separate external DFU tool; older goPine needs the one-time
[transition or wired setup](ota.md#transition-from-0321).

Open the page over HTTPS in a browser that provides Web Bluetooth, such as Chrome
or Edge on a supported desktop or Android device. Browser support varies by
operating system; ordinary Safari on iPhone does not provide this API. The page
checks availability before connecting. See [Chrome's Web Bluetooth
documentation](https://developer.chrome.com/docs/capabilities/bluetooth) for
platform requirements, including Linux configuration.

On **Linux Chrome**, if a ZIP shows **Checked** but the button says **Bluetooth
unavailable**, open `chrome://flags/#enable-experimental-web-platform-features`,
set **Experimental Web Platform features** to **Enabled**, and relaunch Chrome.
Then reopen the updater; the latest release loads automatically. The page provides a
**Copy Linux setup address** button and links to Chrome's guide beside the
connection control. This setting exposes the Bluetooth API; it does not choose
or connect to a watch automatically.

1. Wait for the latest published firmware to download and show **Checked**, or
   choose an application DFU ZIP downloaded or built from this project. A manual
   selection is never replaced by a delayed release lookup. Loading firmware
   does not connect to a watch.
2. Keep the watch nearby and open **Settings →
   Firmware Update** on the watch. Hold the on-watch control until it is ready
   to connect. Stay in goPine; this flow does not use the recovery clock.
3. Use the page's connect control and choose **goPine Update** from the browser's
   Bluetooth chooser. Keep the page open and visible during transfer.
4. Wait for the watch to finish verification. Tap **INSTALL** on the watch,
   then **KEEP** after the new firmware boots.

Progress counts bytes acknowledged by the watch. If the connection drops, keep
the page open and reconnect to resume the same transfer. Reloading or closing
the page loses its transfer session; use Cancel/Retry on the watch before starting
a new session. Stopping the browser transfer does not install firmware. Installation
and KEEP remain physical watch actions, and the page cannot confirm a successful
boot on its own.

With receiver 0.3.28 or later, the sender negotiates a two-packet window and uses
writes without response when the browser exposes them. Earlier protocol-2 watches
use the existing transfer method. Progress still counts only flash-verified bytes;
the final result includes elapsed transfer time. Installing the faster receiver
uses the currently installed receiver's speed. Receiver 0.3.27 also allows two
saved phones and the updater to connect concurrently; older receivers may need
phone Bluetooth temporarily disabled while updating.

Transient Bluetooth requests and watch acknowledgement timeouts reconnect using
the same transfer session. Connection and service discovery requests allow up to
30 seconds, other Bluetooth requests 15 seconds, and retries stop after 90 seconds
without acknowledged progress. A timed-out native Bluetooth request must finish
closing before another starts; Resume and Start over stay disabled until then.
Error messages identify the connection stage and preserve the browser's error.
If reconnect messages change too quickly to read, use **Copy diagnostics**. The
page keeps the latest 20 connection events and the last failure, including the
selected package version and browser. A selectable text snapshot is available if
clipboard access is blocked. Pause/Resume retains the history; Start over clears
it. Diagnostics stay in page memory and are copied only when you ask; they exclude
watch identifiers, transfer session tokens, and firmware contents.

The page validates the application package, image hash, link addresses and size
before transfer. The watch also validates the completed image before offering
INSTALL. These checks detect corruption and incompatible package layouts; they
do not turn arbitrary downloaded files into authenticated releases.

## Hosting and release updates

The `Browser updater` workflow builds the static `web/` directory and deploys it
with GitHub Pages. In repository **Settings → Pages**, set **Source** to **GitHub
Actions**. See [GitHub's Pages workflow
documentation](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages).

The workflow runs after website changes on `main`, a manual dispatch, published
releases, and successful `OTA release` runs. The last trigger covers releases
published by `GITHUB_TOKEN`, which do not start another release-event workflow.
It always checks out the repository's default branch and downloads release assets
from the same repository; it does not execute artifacts from the triggering run.

`scripts/build_web.py` finds the highest stable version at least 0.3.22. It checks
the release's `build-info.json`, SHA-256, exact package version, BLE variant, image
length, and the source commit resolved from its Git tag. A malformed compatible
release fails the deployment instead of silently offering an older version. If
there are no compatible published releases, the site still works with a local ZIP.

The validated ZIP is copied into the Pages artifact and served from the same
origin as the page. This avoids requiring browser access to GitHub API credentials
or cross-origin release downloads. No tokens or build metadata secrets are shipped
to the browser.

The generated `firmware/latest.json` has this shape; `firmware` is `null` when no
compatible published release is available:

```json
{
  "schema": 1,
  "repository": "Distortions81/goPineTime",
  "firmware": {
    "version": "0.3.22",
    "tag": "v0.3.22",
    "commit": "<40-character source commit>",
    "package": "firmware/gopine-update-0.3.22.zip",
    "sha256": "<64-character package digest>",
    "size": 208880,
    "imageBytes": 277132,
    "releaseUrl": "https://github.com/Distortions81/goPineTime/releases/tag/v0.3.22",
    "publishedAt": "2026-10-09T12:00:00Z"
  }
}
```

Numbers above illustrate the schema, not a published release. `package` is
relative to the site root. A fresh build refuses to overwrite a nonempty output
directory, avoiding stale firmware files in a deployment.

## Local development

Python 3 and Node 22 are sufficient; there are no third-party build dependencies.

```sh
python3 scripts/test_build_web.py
node --test web/*.test.mjs
python3 scripts/build_web.py --offline --output /tmp/gopine-web-preview
python3 -m http.server 8080 --directory /tmp/gopine-web-preview
```

Open `http://localhost:8080`. Browsers treat localhost as a secure context for
development. Omit `--offline` to stage the latest checked release; an optional
`GH_TOKEN` environment variable raises the GitHub API rate limit. The build does
not flash or connect to a watch. Automated browser protocol tests use a fake GATT
receiver; a successful real transfer still needs hardware confirmation.
