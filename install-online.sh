#!/usr/bin/env bash
set -euo pipefail

# 用法：
# bash <(curl -fsSL https://raw.githubusercontent.com/charmingyi/-/codex/create-reverse-proxy-website-for-emby/install-online.sh)
# 可选环境变量：
#   INSTALL_DIR=/opt/emby-relay-hub
#   BRANCH=codex/create-reverse-proxy-website-for-emby
#   REPO_URL=https://github.com/charmingyi/-.git
#   HOST=0.0.0.0
#   PORT=8000

REPO_URL="${REPO_URL:-https://github.com/charmingyi/-.git}"
BRANCH="${BRANCH:-codex/create-reverse-proxy-website-for-emby}"
INSTALL_DIR="${INSTALL_DIR:-/opt/emby-relay-hub}"

SUDO_CMD=""
if [[ "${EUID}" -ne 0 ]]; then
  SUDO_CMD="sudo"
fi

info() { echo -e "\033[1;36m[INFO]\033[0m $*"; }
warn() { echo -e "\033[1;33m[WARN]\033[0m $*"; }

ensure_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令: $1" >&2
    exit 1
  fi
}

prepare_env() {
  ensure_cmd uname
  if [[ "$(uname -s)" != "Linux" ]]; then
    echo "当前脚本仅支持 Linux（推荐 Ubuntu/Debian）。" >&2
    exit 1
  fi

  ensure_cmd apt-get
  info "安装基础依赖（git/curl/python3/sudo）..."
  ${SUDO_CMD} apt-get update
  ${SUDO_CMD} apt-get install -y git curl python3 python3-venv python3-pip sudo
}

checkout_code() {
  info "拉取项目代码到: ${INSTALL_DIR}"
  ${SUDO_CMD} mkdir -p "${INSTALL_DIR}"
  ${SUDO_CMD} chown -R "$(id -un):$(id -gn)" "${INSTALL_DIR}"

  if [[ -d "${INSTALL_DIR}/.git" ]]; then
    git -C "${INSTALL_DIR}" fetch origin
    git -C "${INSTALL_DIR}" checkout "${BRANCH}"
    git -C "${INSTALL_DIR}" pull --ff-only origin "${BRANCH}"
  else
    rm -rf "${INSTALL_DIR:?}"/*
    git clone --depth 1 -b "${BRANCH}" "${REPO_URL}" "${INSTALL_DIR}"
  fi
}

deploy() {
  info "执行一键部署脚本 deploy.sh"
  cd "${INSTALL_DIR}"
  chmod +x deploy.sh
  HOST="${HOST:-0.0.0.0}" PORT="${PORT:-8000}" bash ./deploy.sh
}

print_done() {
  info "在线安装已完成 ✅"
  echo "默认端口: ${PORT:-8000}"
  echo "如果你修改了 PORT 环境变量，请按你的值访问。"
}

main() {
  prepare_env
  checkout_code
  deploy
  print_done
}

main "$@"
