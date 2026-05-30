#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:7001}"

curl -sS "$BASE_URL/health"
echo

REGISTRATION="$(curl -sS -X POST "$BASE_URL/api/v1/devices/register" \
  -H 'Content-Type: application/json' \
  -d '{"id":"esp-smoke-001","name":"Smoke ESP","type":"esp","capabilities":[{"name":"sensor.read"},{"name":"gpio.write"}]}')"
echo "$REGISTRATION"
echo
TOKEN="$(echo "$REGISTRATION" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
if [ -z "$TOKEN" ]; then
  TOKEN="$(curl -sS -X POST "$BASE_URL/api/v1/devices/esp-smoke-001/token/reset" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
fi

curl -sS -X POST "$BASE_URL/api/v1/devices/esp-smoke-001/telemetry" \
  -H 'Content-Type: application/json' \
  -H "X-Device-Token: $TOKEN" \
  -d '{"key":"temperature","value":23.8,"unit":"celsius"}'
echo

COMMAND_ID="$(curl -sS -X POST "$BASE_URL/api/v1/devices/esp-smoke-001/commands" \
  -H 'Content-Type: application/json' \
  -d '{"type":"gpio.write","payload":{"pin":2,"value":true},"ttlSeconds":60}' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"

curl -sS "$BASE_URL/api/v1/devices/esp-smoke-001/commands/next" \
  -H "X-Device-Token: $TOKEN"
echo

curl -sS -X POST "$BASE_URL/api/v1/devices/esp-smoke-001/commands/$COMMAND_ID/ack" \
  -H 'Content-Type: application/json' \
  -H "X-Device-Token: $TOKEN" \
  -d '{"status":"succeeded","result":{"message":"ok"}}'
echo
