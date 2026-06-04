#!/usr/bin/env bash
# One-click flash for Light Gateway ESP32 firmwares.
#
# Usage:
#   scripts/flash.sh <light|clock|gps|voice|wifi-agent> [PORT] [--monitor]
#
# Examples:
#   scripts/flash.sh light                 # auto-detect the serial port
#   scripts/flash.sh gps /dev/ttyUSB0      # explicit port
#   scripts/flash.sh voice --monitor       # flash, then open serial monitor @115200
#
# Requires: arduino-cli (https://arduino.github.io/arduino-cli/). The ESP32 core
# is installed automatically on first run. All firmware libraries ship with the
# core, so nothing else is needed.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ESP32_INDEX="https://espressif.github.io/arduino-esp32/package_esp32_index.json"
BAUD=115200

die() { echo "ERROR: $*" >&2; exit 1; }

usage() {
  cat <<'EOF'
One-click flash for Light Gateway ESP32 firmwares.

Usage:
  scripts/flash.sh <light|clock|gps|voice|wifi-agent> [PORT] [--monitor]

Examples:
  scripts/flash.sh light                 # auto-detect the serial port
  scripts/flash.sh light /dev/cu.usbmodem2101
  scripts/flash.sh gps /dev/ttyUSB0
  scripts/flash.sh voice --monitor       # flash, then open serial monitor @115200

Boards: light=ESP32-C3 (with ST7789 TFT) · clock/gps=ESP32 · voice=ESP32-S3.
Requires arduino-cli; the ESP32 core and any required libraries are installed
automatically on first run.
EOF
  exit "${1:-0}"
}

# ---- parse args -------------------------------------------------------------
TARGET=""; PORT=""; MONITOR=0
for arg in "$@"; do
  case "$arg" in
    -h|--help) usage 0 ;;
    --monitor) MONITOR=1 ;;
    *) if [ -z "$TARGET" ]; then TARGET="$arg"; elif [ -z "$PORT" ]; then PORT="$arg"; else die "unexpected argument: $arg"; fi ;;
  esac
done
[ -n "$TARGET" ] || usage 1

# ---- map target -> firmware dir + FQBN + required libraries ----------------
# light is an ESP32-C3 SuperMini with an ST7789 TFT (needs Adafruit libs +
# the huge_app partition because the binary is large). The others are bare.
LIBS=()
case "$TARGET" in
  light)      DIR="esp32-light";       FQBN="esp32:esp32:esp32c3:PartitionScheme=huge_app"
              LIBS=("Adafruit GFX Library" "Adafruit ST7735 and ST7789 Library") ;;
  clock)      DIR="esp32-clock";       FQBN="esp32:esp32:esp32" ;;
  gps)        DIR="esp32-gps";         FQBN="esp32:esp32:esp32" ;;
  voice)      DIR="esp32-voice";       FQBN="esp32:esp32:esp32s3:CDCOnBoot=cdc" ;;
  wifi-agent) DIR="esp32-wifi-agent";  FQBN="esp32:esp32:esp32" ;;
  *) die "unknown target '$TARGET' (use: light | clock | gps | voice | wifi-agent)" ;;
esac

SKETCH="$REPO_ROOT/firmware/$DIR"
[ -d "$SKETCH" ] || die "firmware folder not found: $SKETCH"

# ---- prerequisites ----------------------------------------------------------
command -v arduino-cli >/dev/null 2>&1 || die "arduino-cli not installed. macOS: brew install arduino-cli — or: curl -fsSL https://raw.githubusercontent.com/arduino/arduino-cli/master/install.sh | sh"

if ! arduino-cli core list 2>/dev/null | grep -q '^esp32:esp32'; then
  echo "==> Installing ESP32 core (one-time)..."
  arduino-cli config init --overwrite >/dev/null 2>&1 || true
  arduino-cli config add board_manager.additional_urls "$ESP32_INDEX" >/dev/null 2>&1 || \
    arduino-cli config set board_manager.additional_urls "$ESP32_INDEX" >/dev/null 2>&1 || true
  arduino-cli core update-index
  arduino-cli core install esp32:esp32
fi

# ---- required libraries (light needs Adafruit GFX + ST7789) ----------------
if [ "${#LIBS[@]}" -gt 0 ]; then
  echo "==> Ensuring libraries: ${LIBS[*]}"
  for lib in "${LIBS[@]}"; do
    arduino-cli lib install "$lib"
  done
fi

# ---- resolve port -----------------------------------------------------------
if [ -z "$PORT" ]; then
  echo "==> Auto-detecting serial port..."
  PORT="$(arduino-cli board list 2>/dev/null | awk 'NR>1 && $1 ~ /^\/dev\/|^COM/ {print $1; exit}')"
  [ -n "$PORT" ] || die "no serial port found. Plug the board in, install the CH340/CP210x driver, then pass the port explicitly: scripts/flash.sh $TARGET <PORT>"
  echo "    using $PORT"
fi

# ---- compile + upload -------------------------------------------------------
echo "==> Compiling $DIR ($FQBN)..."
arduino-cli compile --fqbn "$FQBN" "$SKETCH"

echo "==> Uploading to $PORT..."
if ! arduino-cli upload --fqbn "$FQBN" -p "$PORT" "$SKETCH"; then
  die "upload failed. Try: hold BOOT, tap RST/EN, release BOOT, then re-run. (Linux perms: sudo usermod -aG dialout \$USER, then re-login.)"
fi

echo
echo "================================================================"
echo " Flashed $TARGET -> $PORT"
echo " First boot opens Wi-Fi hotspot 'LightGateway-<id>'."
echo " Connect to it, open http://192.168.4.1, set Wi-Fi + gateway URL"
echo " (the gateway host's LAN IP, e.g. http://192.168.x.x:7001)."
echo "================================================================"

# ---- optional serial monitor ------------------------------------------------
if [ "$MONITOR" -eq 1 ]; then
  echo "==> Opening serial monitor @${BAUD} (Ctrl-C to exit)..."
  exec arduino-cli monitor -p "$PORT" -c baudrate=$BAUD
fi
