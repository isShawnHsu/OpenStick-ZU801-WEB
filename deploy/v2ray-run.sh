#!/bin/sh
# Start v2ray with the syntax that matches the installed major version.
# v2ray 5.x / xray use `run -c`, v2ray 4.x uses `-config`.
set -e

BIN="${V2RAY_BIN:-/usr/local/bin/v2ray}"
CONF="${1:-/etc/v2ray/config.json}"

if [ ! -x "$BIN" ]; then
    echo "v2ray-run: $BIN not found or not executable" >&2
    exit 1
fi

VER=$("$BIN" version 2>/dev/null | head -1 || true)
case "$VER" in
    *"V2Ray 4"*)
        exec "$BIN" -config "$CONF"
        ;;
esac
exec "$BIN" run -c "$CONF"
