#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "run as root to install the systemd service" >&2
  exit 1
fi

GATEWAY_URL="${LIGHT_AGENT_GATEWAY:-http://127.0.0.1:7001}"
DEVICE_ID="${LIGHT_AGENT_ID:-$(hostname)}"
DEVICE_NAME="${LIGHT_AGENT_NAME:-$(hostname)}"
INSTALL_DIR="${LIGHT_AGENT_INSTALL_DIR:-/opt/light-gateway}"
SERVICE_FILE="/etc/systemd/system/light-gateway-agent.service"

mkdir -p "$INSTALL_DIR"
install -m 0755 ./light-agent "$INSTALL_DIR/light-agent"
mkdir -p /var/lib/light-gateway

cat > "$SERVICE_FILE" <<SERVICE
[Unit]
Description=Light Gateway Linux Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=LIGHT_AGENT_GATEWAY=$GATEWAY_URL
Environment=LIGHT_AGENT_ID=$DEVICE_ID
Environment=LIGHT_AGENT_NAME=$DEVICE_NAME
Environment=LIGHT_AGENT_TOKEN_FILE=/var/lib/light-gateway/agent-token
Environment=LIGHT_AGENT_ALLOWED_COMMANDS=uptime,df,free,uname,whoami,pwd,date
ExecStart=$INSTALL_DIR/light-agent
Restart=always
RestartSec=5
User=root

[Install]
WantedBy=multi-user.target
SERVICE

systemctl daemon-reload
systemctl enable light-gateway-agent
systemctl restart light-gateway-agent
systemctl status light-gateway-agent --no-pager
