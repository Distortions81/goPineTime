# Watch updates

When the user asks to update or flash the watch, assume they have already put
it in **goPine Settings → Firmware Update**, showing **Ready to connect**.
Start the prepared uploader immediately. Do not ask an initial readiness or
mode question, request another confirmation, or run a preliminary scan that
delays the connection. The request authorizes the firmware transfer.

Use **goPine's in-app update**. The maintained uploader supports OTA protocol 2
(goPine 0.3.22 and newer); legacy recovery upload tools have been removed.
Emergency bootloader recovery and rollback remain available. Recovery uses
external compatible DFU tools and official InfiniTime packages, not native
protocol-2 goPine packages. Do not enter recovery for a normal update.

The configured watch uses the TP-Link USB adapter **hci1**. goPine's address
is **C9:9E:15:7A:69:B4**; recovery uses **C9:9E:15:7A:69:B5**. Do not enable
the antenna-less MediaTek adapter. Start the already prepared, validated package
with the appropriate mode:

```sh
build/ota/venv/bin/python -u scripts/ota_update.py VERSION \
  --package build/ota/gopine-update-VERSION.zip \
  --adapter hci1 --address C9:9E:15:7A:69:B4 --ready
```

For the first transition from 0.3.21 to the current 0.3.23 candidate, the old in-app sender is prepared
locally. Start it immediately when the user requests that update:

```sh
bash build/ota/v1-transition/run-0.3.23.sh
```

This one-time transition is still an in-app upload with INSTALL and KEEP. Its
preserved sender is not a compatibility path in the maintained updater.
If the package still needs preparation, finish that work before asking the
user to reopen a short connection window. Ask for a restart or mode clarification
only after an actual failure or conflicting evidence. Describe what failed and
whether any firmware was sent. Never retry recovery activation after successful
receiver validation merely because its reset acknowledgement is missing.

In-app uploads require the user to tap **INSTALL**, then **KEEP** after boot.
Recovery uploads activate automatically and require **KEEP** after boot.
After a verified upload, give a brief result and any necessary INSTALL/KEEP
instructions, then continue or finish. **Do not ask whether boot succeeded or
KEEP was tapped after every update.** The user will report any update failure.
Record transfer validation separately from user-confirmed boot and KEEP;
only mark boot/KEEP confirmed when the user volunteers that information. Do not
turn an unconfirmed boot into another question or block subsequent work on it.
