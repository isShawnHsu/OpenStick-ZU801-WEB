#!/bin/sh
# Turn off the eMMC activity LED at boot.
#
# Some OpenStick kernels reboot when the mmc0 LED trigger is detached. This
# script is guarded: it records an attempt before touching the trigger, and if
# the next boot finds an unfinished attempt, it disables itself instead of
# risking a reboot loop.
set -eu

STATE_DIR=/var/lib/uz801d
ATTEMPT="$STATE_DIR/emmc-led-off.attempting"
DISABLED="$STATE_DIR/emmc-led-off.disabled"
LOG=/var/log/uz801d-emmc-led.log

mkdir -p "$STATE_DIR"

if [ -e "$DISABLED" ]; then
  echo "$(date -Is) disabled marker present; skipping" >>"$LOG"
  exit 0
fi

if [ -e "$ATTEMPT" ]; then
  mv "$ATTEMPT" "$DISABLED"
  echo "$(date -Is) previous attempt did not finish; disabling to avoid a reboot loop" >>"$LOG"
  exit 0
fi

LEDS=""
for d in /sys/class/leds/mmc*; do
  [ -e "$d/trigger" ] || continue
  LEDS="$LEDS $d"
done
[ -n "$LEDS" ] || exit 0

boot_id=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || echo unknown)
echo "$boot_id $(date -Is)" >"$ATTEMPT"
sync

{
  echo "$(date -Is) attempt boot=$boot_id"
  for d in $LEDS; do
    echo none >"$d/trigger"
    echo 0 >"$d/brightness"
  done
  sync
  sleep 10
  echo "$(date -Is) survived; eMMC LED off"
} >>"$LOG" 2>&1

rm -f "$ATTEMPT"
sync
