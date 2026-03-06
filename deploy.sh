#!/usr/bin/env bash
set -euo pipefail

APP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENV_DIR="${APP_DIR}/.venv"
SERVICE_NAME="emby-relay-hub"
PYTHON_BIN="python3"
HOST="0.0.0.0"
PORT="8000"

if [[ "${EUID}" -eq 0 ]]; then
  RUNNER_USER="${SUDO_USER:-root}"
else
  RUNNER_USER="$(id -un)"
fi

SUDO_CMD=""
if [[ "${EUID}" -ne 0 ]]; then
  SUDO_CMD="sudo"
fi

info() { echo -e "\033[1;36m[INFO]\033[0m $*"; }
warn() { echo -e "\033[1;33m[WARN]\033[0m $*"; }

ensure_command() {
  local cmd="$1"
  local hint="$2"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "缺少命令: ${cmd}. ${hint}" >&2
    exit 1
  fi
}

install_system_packages() {
  ensure_command apt-get "请手动安装 python3 / python3-venv / pip"
  info "安装系统依赖..."
  ${SUDO_CMD} apt-get update
  ${SUDO_CMD} apt-get install -y python3 python3-venv python3-pip
}

setup_venv() {
  info "创建虚拟环境: ${VENV_DIR}"
  "${PYTHON_BIN}" -m venv "${VENV_DIR}"

  info "安装 Python 依赖"
  "${VENV_DIR}/bin/pip" install --upgrade pip
  "${VENV_DIR}/bin/pip" install -r "${APP_DIR}/requirements.txt"
}

create_systemd_service() {
  ensure_command systemctl "你的系统可能不是 systemd，请手动运行 uvicorn"

  local service_file="/etc/systemd/system/${SERVICE_NAME}.service"
  info "写入 systemd 服务: ${service_file}"

  ${SUDO_CMD} bash -c "cat > '${service_file}'" <<SERVICE
[Unit]
Description=Emby Relay Hub Service
After=network.target

[Service]
Type=simple
User=${RUNNER_USER}
WorkingDirectory=${APP_DIR}
ExecStart=${VENV_DIR}/bin/uvicorn app:app --host ${HOST} --port ${PORT}
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
SERVICE

  ${SUDO_CMD} systemctl daemon-reload
  ${SUDO_CMD} systemctl enable "${SERVICE_NAME}"
  ${SUDO_CMD} systemctl restart "${SERVICE_NAME}"
}

print_summary() {
  info "部署完成 ✅"
  echo "----------------------------------------"
  echo "服务名: ${SERVICE_NAME}"
  echo "访问地址: http://<你的服务器IP>:${PORT}"
  echo "查看状态: ${SUDO_CMD} systemctl status ${SERVICE_NAME} --no-pager"
  echo "查看日志: ${SUDO_CMD} journalctl -u ${SERVICE_NAME} -f"
  echo "----------------------------------------"
  warn "请确保防火墙放行 ${PORT} 端口。"
}

main() {
  ensure_command "${PYTHON_BIN}" "请安装 Python 3"

  if [[ "$(uname -s)" == "Linux" ]]; then
    install_system_packages
  else
    warn "非 Linux 系统，跳过 apt 安装。"
  fi

  setup_venv

  if command -v systemctl >/dev/null 2>&1; then
    create_systemd_service
  else
    warn "未检测到 systemd，已完成依赖安装。请手动启动:"
    echo "${VENV_DIR}/bin/uvicorn app:app --host ${HOST} --port ${PORT}"
  fi

  print_summary
}

main "$@"
