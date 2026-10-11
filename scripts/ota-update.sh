#!/usr/bin/env bash
# Prepare dependencies before asking the user to enter the watch connection window.
set -euo pipefail
cd "$(dirname "$0")/.."
# Optional, local-only toolchain/cache paths, like the caller's shell profile.
if [[ -f build/ota/environment.sh ]]; then
  source build/ota/environment.sh
fi
if [[ -n ${GOPINE_OTA_PYTHON:-} ]]; then
  python_bin=$GOPINE_OTA_PYTHON
else
  python_bin=build/ota/venv/bin/python
  if [[ ! -x $python_bin ]]; then
    python3 -m venv build/ota/venv
  fi
fi
if ! "$python_bin" -c 'import bleak' 2>/dev/null; then
  "$python_bin" -m pip install --disable-pip-version-check bleak==0.22.3
fi
exec "$python_bin" -u scripts/ota_update.py "$@"
