#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
bootstrap=false
target=./targets/pinetime-mcuboot.json
if [[ ${1:-} == --ble ]]; then
  target=./targets/pinetime-mcuboot-ble.json
  shift
fi
if [[ ${1:-} == --bootstrap ]]; then
  bootstrap=true
  shift
fi
if [[ $# != 1 || ! $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+(\+[0-9]+)?$ ]]; then
  printf 'Usage: bash scripts/build-ota.sh [--ble | --bootstrap] major.minor.revision[+build]\n' >&2
  exit 2
fi
version=$1
tinygo_bin=${TINYGO:-tinygo}
if [[ $target == *-ble.json ]]; then
  if $bootstrap; then
    printf 'Bluetooth candidates are OTA-only; do not combine --ble and --bootstrap.\n' >&2
    exit 2
  fi
  python3 scripts/build-ble.py \
    --infinitime "${INFINITIME_SOURCE:-build/deps/InfiniTime}" \
    --tinygo-root "$("$tinygo_bin" env TINYGOROOT)"
fi
firmware_time=${FIRMWARE_TIME:-$(date +%H:%M:%S)}
firmware_date=${FIRMWARE_DATE:-$(date +%Y-%m-%d)}
pack_args=()
output=build/ota

# Fetch only explicit upstream releases. Recheck cached downloads every time;
# never execute unverified downloads or silently substitute a newer recovery.
fetch() {
  local url=$1 path=$2 digest=$3
  if [[ ! -f $path ]]; then
    curl --fail --location --retry 3 --proto '=https' --tlsv1.2 "$url" -o "$path.download"
    printf '%s  %s\n' "$digest" "$path.download" | sha256sum --check --status
    mv "$path.download" "$path"
  fi
  printf '%s  %s\n' "$digest" "$path" | sha256sum --check --status
}

if $bootstrap; then
  output=build/bootstrap
  mkdir -p build/assets
  fetch \
    https://github.com/InfiniTimeOrg/pinetime-mcuboot-bootloader/releases/download/1.0.1/bootloader-1.0.1.bin \
    build/assets/bootloader-1.0.1.bin \
    4d25ea801c7859069881a4e601cd25f7598ad16114b2a806be401865d255d72a
  fetch \
    https://github.com/InfiniTimeOrg/InfiniTime/releases/download/0.14.1/spinor.bin \
    internal/recoveryasset/recovery.bin \
    3bf992adb44282f384b1944387abaea2a141afd24498e1688280e842247536dc
  pack_args=(-bootloader build/assets/bootloader-1.0.1.bin)
fi

mkdir -p "$output"
if $bootstrap; then
  # First run this standalone helper to provision external recovery and clear
  # stale pending updates. Only then install the bootloader+app HEX below.
  "$tinygo_bin" build -target=./targets/pinetime-gopine.json -tags=provision \
    -ldflags="-X main.firmwareTime=$firmware_time -X main.firmwareDate=$firmware_date -X main.firmwareVersion=$version" \
    -o "$output/gopine-recovery-setup-$version.elf" .
  "$tinygo_bin" build -target=./targets/pinetime-gopine.json -tags=provision \
    -ldflags="-X main.firmwareTime=$firmware_time -X main.firmwareDate=$firmware_date -X main.firmwareVersion=$version" \
    -o "$output/gopine-recovery-setup-$version.hex" .
fi
allocation_args=()
if [[ $target == *-ble.json ]]; then
  allocation_args=(-print-allocs='(ota.*Receiver|ota.ValidateWithScratch|notifications.*Inbox)')
fi
if ! "$tinygo_bin" build -target="$target" "${allocation_args[@]}" \
  -ldflags="-X main.firmwareTime=$firmware_time -X main.firmwareDate=$firmware_date -X main.firmwareVersion=$version" \
  -o "$output/gopine-$version.elf" . > "$output/gopine-allocations-$version.log" 2>&1; then
  cat "$output/gopine-allocations-$version.log" >&2
  exit 1
fi
if [[ $target == *-ble.json ]]; then
  # A successful link does not bound TinyGo's indirect/recursive task stack.
  python3 scripts/check-resources.py "$output/gopine-$version.elf" \
    --ble-stack-usage build/ble/stack-usage.txt \
    --allocations "$output/gopine-allocations-$version.log" \
    --report "$output/gopine-resources-$version.json"
fi
go run ./cmd/otapack -elf "$output/gopine-$version.elf" -version "$version" \
  -out "$output" "${pack_args[@]}"
