#!/usr/bin/env bash
set -euo pipefail

APP_NAME="emby-agent"
BIN_PATH="/usr/local/bin/${APP_NAME}"
ENV_PATH="/etc/${APP_NAME}.env"
SERVICE_PATH="/etc/systemd/system/${APP_NAME}.service"

PANEL_URL="${PANEL_URL:-}"
AGENT_ID="${AGENT_ID:-}"
AGENT_TOKEN="${AGENT_TOKEN:-}"
LISTEN_ADDR="${LISTEN_ADDR:-:19073}"

need_root() {
  if [ "${EUID}" -ne 0 ]; then
    echo "Please run with sudo/root"
    exit 1
  fi
}

need_vars() {
  if [ -z "${PANEL_URL}" ] || [ -z "${AGENT_ID}" ] || [ -z "${AGENT_TOKEN}" ]; then
    echo "Missing env vars: PANEL_URL / AGENT_ID / AGENT_TOKEN"
    exit 1
  fi
}

detect_arch() {
  local arch
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    armv7l|armv6l) echo "arm" ;;
    *) echo "unsupported" ;;
  esac
}

download_bin() {
  local arch
  arch="$(detect_arch)"
  if [ "$arch" = "unsupported" ]; then
    echo "Unsupported arch: $(uname -m)"
    exit 1
  fi

  local url="${PANEL_URL%/}/downloads/agent-linux-${arch}"
  echo "Downloading: $url"
  curl -fsSL "$url" -o "$BIN_PATH"
  chmod +x "$BIN_PATH"
}

write_env() {
  cat > "$ENV_PATH" <<EOF
PANEL_URL=${PANEL_URL}
AGENT_ID=${AGENT_ID}
AGENT_TOKEN=${AGENT_TOKEN}
LISTEN_ADDR=${LISTEN_ADDR}
SYNC_INTERVAL=30
EOF
}

write_service() {
  cat > "$SERVICE_PATH" <<EOF
[Unit]
Description=Emby Relay Agent
After=network.target

[Service]
Type=simple
EnvironmentFile=${ENV_PATH}
ExecStart=${BIN_PATH}
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
}

install_agent() {
  need_root
  need_vars
  download_bin
  write_env
  write_service
  systemctl daemon-reload
  systemctl enable --now "$APP_NAME"
  echo "Installed and started: $APP_NAME"
}

uninstall_agent() {
  need_root
  systemctl disable --now "$APP_NAME" 2>/dev/null || true
  rm -f "$SERVICE_PATH" "$ENV_PATH" "$BIN_PATH"
  systemctl daemon-reload
  echo "Uninstalled: $APP_NAME"
}

update_agent() {
  need_root
  need_vars
  download_bin
  systemctl restart "$APP_NAME"
  echo "Updated and restarted: $APP_NAME"
}

status_agent() {
  systemctl status "$APP_NAME" --no-pager || true
}

logs_agent() {
  journalctl -u "$APP_NAME" -n 120 --no-pager || true
}

menu() {
  while true; do
    cat <<EOF

=== Emby Agent Menu ===
1) Install
2) Update
3) Uninstall
4) Status
5) Logs
0) Exit
EOF
    read -rp "Select: " c
    case "$c" in
      1) install_agent ;;
      2) update_agent ;;
      3) uninstall_agent ;;
      4) status_agent ;;
      5) logs_agent ;;
      0) exit 0 ;;
      *) echo "Invalid" ;;
    esac
  done
}

cmd="${1:-menu}"
case "$cmd" in
  install) install_agent ;;
  update) update_agent ;;
  uninstall) uninstall_agent ;;
  status) status_agent ;;
  logs) logs_agent ;;
  menu) menu ;;
  *) echo "Usage: [menu|install|update|uninstall|status|logs]"; exit 1 ;;
esac

