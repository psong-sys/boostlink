#!/usr/bin/env bash
# Boost Link server installer. Run as root on Ubuntu/Debian: bash install.sh
# Re-running it updates the program and keeps your server.json.
set -euo pipefail
cd "$(dirname "$0")"
[ "$(id -u)" = 0 ] || { echo "Run as root (sudo bash install.sh)"; exit 1; }

case "$(uname -m)" in
  x86_64|amd64) BIN=exvs-online-linux-amd64 ;;
  aarch64|arm64) BIN=exvs-online-linux-arm64 ;;
  *) echo "Unsupported CPU $(uname -m)"; exit 1 ;;
esac

DIR=/opt/boost-link
id boostlink >/dev/null 2>&1 || useradd --system --home-dir "$DIR" --shell /usr/sbin/nologin boostlink
mkdir -p "$DIR"
install -m 0755 "$BIN" "$DIR/exvs-online"
if [ ! -f "$DIR/server.json" ]; then
  install -m 0644 server.json "$DIR/server.json"
  echo "Installed default config at $DIR/server.json"
else
  echo "Kept your existing $DIR/server.json"
fi
chown -R boostlink:boostlink "$DIR"

cat > /etc/systemd/system/boost-link.service <<UNIT
[Unit]
Description=Boost Link matchmaking server
After=network-online.target
Wants=network-online.target

[Service]
User=boostlink
WorkingDirectory=$DIR
ExecStart=$DIR/exvs-online -config $DIR/server.json
Restart=always
RestartSec=3
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$DIR
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT

# Open the ports if ufw is in use (most cloud images leave it off; provider firewalls need the same ports).
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  ufw allow 28090/tcp
  ufw allow 3478:3479/udp
  ufw allow 3480/udp
  ufw allow 49160:49200/udp
fi

if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable boost-link >/dev/null 2>&1
  systemctl restart boost-link
  sleep 2
  systemctl --no-pager --lines=5 status boost-link || true
else
  echo "systemd isn't running here; start it by hand: $DIR/exvs-online -config $DIR/server.json"
fi

IP=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')
echo
echo "Boost Link server is set up."
echo "  Players type this as the host IP in the launcher (Join): $IP"
echo "  Live status page: http://$IP:28090/status"
echo "  Settings: $DIR/server.json, then: systemctl restart boost-link"
echo "  Logs: journalctl -u boost-link -f"
