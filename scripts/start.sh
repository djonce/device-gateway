#!/usr/bin/env bash
# One-click install & start: builds the image and runs Light Gateway.
# Requires Docker + Docker Compose. Run from anywhere inside the repo.
set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v docker >/dev/null 2>&1; then
  echo "ERROR: Docker is not installed. Install Docker Desktop / Docker Engine first: https://docs.docker.com/get-docker/" >&2
  exit 1
fi

echo "==> Building and starting Light Gateway (this builds the web UI + gateway on first run)..."
docker compose up -d --build

echo
echo "================================================================"
echo " Light Gateway is up:  http://localhost:7001"
echo " Admin console login:  admin / lightgateway"
echo " Device enroll key:    lightgateway-enroll"
echo
echo " CHANGE THESE for production: create a .env file with"
echo "   LIGHT_ADMIN_PASSWORD=... and LIGHT_PROVISION_KEY=..."
echo " then re-run this script."
echo
echo " Logs:  docker compose logs -f gateway"
echo " Stop:  docker compose down"
echo "================================================================"
