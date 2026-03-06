#!/usr/bin/env bash
set -euo pipefail

APP_NAME="emby-panel"
INSTALL_DIR="/opt/${APP_NAME}"
BIN_PATH="/usr/local/bin/${APP_NAME}"
ENV_PATH="/etc/${APP_NAME}.env"
SERVICE_PATH="/etc/systemd/system/${APP_NAME}.service"

REPO_OWNER="${REPO_OWNER:-charmingyi}"
REPO_NAME="${REPO_NAME:--}"
REPO_BRANCH="${REPO_BRANCH:-codex/create-reverse-proxy-website-for-emby-kzecse}"

PANEL_LISTEN="${PANEL_LISTEN:-:18473}"
PANEL_PUBLIC_URL="${PANEL_PUBLIC_URL:-}"
ADMIN_TOKEN="${ADMIN_TOKEN:-}"

GO_VERSION="${GO_VERSION:-1.22.12}"
GO_INSTALL_DIR="/usr/local"

need_root() {
  if [ "${EUID}" -ne 0 ]; then
    echo "Please run as root, e.g. sudo bash deploy.sh"
    exit 1
  fi
}

append_path_profile() {
  local profile_file
  profile_file="/etc/profile.d/go-path.sh"
  if [ ! -f "${profile_file}" ] || ! grep -q '/usr/local/go/bin' "${profile_file}"; then
    cat > "${profile_file}" <<'EOF'
export PATH=/usr/local/go/bin:$PATH
EOF
  fi
  export PATH=/usr/local/go/bin:$PATH
}

pkg_install() {
  local pkg="$1"
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -y >/dev/null 2>&1 || true
    DEBIAN_FRONTEND=noninteractive apt-get install -y "${pkg}" >/dev/null 2>&1 || true
    return 0
  fi
  if command -v dnf >/dev/null 2>&1; then
    dnf install -y "${pkg}" >/dev/null 2>&1 || true
    return 0
  fi
  if command -v yum >/dev/null 2>&1; then
    yum install -y "${pkg}" >/dev/null 2>&1 || true
    return 0
  fi
  return 1
}

ensure_base_tools() {
  command -v curl >/dev/null 2>&1 || pkg_install curl
  command -v tar >/dev/null 2>&1 || pkg_install tar

  if ! command -v curl >/dev/null 2>&1; then
    echo "Missing curl and auto-install failed."
    exit 1
  fi
  if ! command -v tar >/dev/null 2>&1; then
    echo "Missing tar and auto-install failed."
    exit 1
  fi

  pkg_install ca-certificates >/dev/null 2>&1 || true
  update-ca-certificates >/dev/null 2>&1 || true
}

go_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    armv7l|armv6l|armhf) echo "armv6l" ;;
    *) echo "unsupported" ;;
  esac
}

install_go() {
  local arch go_tgz_url tmp_dir

  arch="$(go_arch)"
  if [ "${arch}" = "unsupported" ]; then
    echo "Unsupported CPU arch: $(uname -m)"
    exit 1
  fi

  tmp_dir="$(mktemp -d)"
  go_tgz_url="https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz"
  echo "Installing Go ${GO_VERSION} (${arch})..."
  curl -fL -s "${go_tgz_url}" -o "${tmp_dir}/go.tgz"
  rm -rf "${GO_INSTALL_DIR}/go"
  tar -C "${GO_INSTALL_DIR}" -xzf "${tmp_dir}/go.tgz"
  append_path_profile

  if ! command -v go >/dev/null 2>&1; then
    echo "Go install failed."
    exit 1
  fi
}

ensure_go() {
  append_path_profile
  if command -v go >/dev/null 2>&1; then
    echo "Detected $(go version)"
    return 0
  fi
  install_go
}

download_source() {
  local tmp_dir archive_url
  tmp_dir="$(mktemp -d)"
  archive_url="https://codeload.github.com/${REPO_OWNER}/${REPO_NAME}/tar.gz/refs/heads/${REPO_BRANCH}"

  echo "Downloading source: ${archive_url}"
  curl -fL -s "${archive_url}" -o "${tmp_dir}/src.tar.gz"
  tar -xzf "${tmp_dir}/src.tar.gz" -C "${tmp_dir}"

  SRC_DIR="$(find "${tmp_dir}" -mindepth 1 -maxdepth 1 -type d | head -n1)"
  if [ -z "${SRC_DIR}" ] || [ ! -f "${SRC_DIR}/go.mod" ]; then
    echo "Invalid source archive. Check REPO_OWNER / REPO_NAME / REPO_BRANCH."
    exit 1
  fi
}

build_panel() {
  mkdir -p "${INSTALL_DIR}"
  rm -rf "${INSTALL_DIR}/src"
  cp -r "${SRC_DIR}" "${INSTALL_DIR}/src"
  cd "${INSTALL_DIR}/src"
  CGO_ENABLED=0 go build -o "${BIN_PATH}" ./cmd/panel
}

write_env() {
  if [ -z "${PANEL_PUBLIC_URL}" ]; then
    local host_ip
    host_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    if [ -z "${host_ip}" ]; then
      host_ip="127.0.0.1"
    fi
    PANEL_PUBLIC_URL="http://${host_ip}${PANEL_LISTEN}"
  fi

  cat > "${ENV_PATH}" <<EOF
PANEL_LISTEN=${PANEL_LISTEN}
PANEL_PUBLIC_URL=${PANEL_PUBLIC_URL}
ADMIN_TOKEN=${ADMIN_TOKEN}
PANEL_DATA=${INSTALL_DIR}/data/panel.json
EOF
}

write_service() {
  cat > "${SERVICE_PATH}" <<EOF
[Unit]
Description=Emby Relay Hub Panel
After=network.target

[Service]
Type=simple
EnvironmentFile=${ENV_PATH}
WorkingDirectory=${INSTALL_DIR}/src
ExecStart=${BIN_PATH}
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
}

install_panel() {
  need_root
  ensure_base_tools
  ensure_go
  download_source
  build_panel
  mkdir -p "${INSTALL_DIR}/data"
  write_env
  write_service
  systemctl daemon-reload
  systemctl enable --now "${APP_NAME}"
  echo "Installed."
  echo "Panel URL: ${PANEL_PUBLIC_URL}"
}

update_panel() {
  need_root
  ensure_base_tools
  ensure_go
  download_source
  build_panel
  write_env
  write_service
  systemctl daemon-reload
  systemctl restart "${APP_NAME}"
  echo "Updated."
  echo "Panel URL: ${PANEL_PUBLIC_URL}"
}

uninstall_panel() {
  need_root
  systemctl disable --now "${APP_NAME}" 2>/dev/null || true
  rm -f "${SERVICE_PATH}" "${ENV_PATH}" "${BIN_PATH}"
  rm -rf "${INSTALL_DIR}"
  systemctl daemon-reload
  echo "Uninstalled ${APP_NAME}."
}

status_panel() {
  systemctl status "${APP_NAME}" --no-pager || true
}

logs_panel() {
  journalctl -u "${APP_NAME}" -n 120 --no-pager || true
}

menu() {
  while true; do
    cat <<EOF

=== Emby Panel Menu ===
1) Install
2) Update
3) Uninstall
4) Status
5) Logs
0) Exit
EOF
    read -rp "Select: " c
    case "${c}" in
      1) install_panel ;;
      2) update_panel ;;
      3) uninstall_panel ;;
      4) status_panel ;;
      5) logs_panel ;;
      0) exit 0 ;;
      *) echo "Invalid choice." ;;
    esac
  done
}

cmd="${1:-menu}"
case "${cmd}" in
  menu) menu ;;
  install) install_panel ;;
  update) update_panel ;;
  uninstall) uninstall_panel ;;
  status) status_panel ;;
  logs) logs_panel ;;
  *) echo "Usage: [menu|install|update|uninstall|status|logs]"; exit 1 ;;
esac
