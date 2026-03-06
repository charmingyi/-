# Emby Relay Hub

A control panel + lightweight agent to reverse proxy multiple Emby servers through optimized relay nodes.

## Features
- Manage multiple agents
- Manage multiple Emby upstream servers
- Route binding: `agent + path prefix -> upstream`
- Agent auto-sync config and hot reload routes
- Agent menu script: install / update / uninstall / status / logs
- Agent build targets: `linux/amd64`, `linux/arm64`, `linux/arm`

## Default Ports (randomized)
- Panel: `18473`
- Agent: `19073`

Env overrides:
- `PANEL_LISTEN` (default `:18473`)
- `PANEL_PUBLIC_URL` (default `http://127.0.0.1:18473`)
- `LISTEN_ADDR` for agent (default `:19073`)

## One-line Deploy (as requested)
```bash
bash <(curl -L -s https://raw.githubusercontent.com/charmingyi/-/codex/create-reverse-proxy-website-for-emby-kzecse/deploy.sh)
```

Optional custom repo/branch:
```bash
REPO_OWNER=myuser REPO_NAME=myrepo REPO_BRANCH=main bash <(curl -L -s https://raw.githubusercontent.com/myuser/myrepo/main/deploy.sh)
```

## Local Dev Run
```bash
go run ./cmd/panel
```

Open: `http://<server-ip>:18473`

## Build Release Binaries
```bash
bash build-release.sh
```

Outputs:
- `releases/panel-linux-amd64`
- `releases/agent-linux-amd64`
- `releases/agent-linux-arm64`
- `releases/agent-linux-arm`

## Agent Connect Command
After creating an agent in panel UI, copy the generated command from page.

Command shape:
```bash
curl -fsSL http://<panel>/install/agent-menu.sh | sudo env PANEL_URL='http://<panel>' AGENT_ID='...' AGENT_TOKEN='...' bash -s -- menu
```

## Route Example
Rule: `/hk-emby -> https://origin-emby.example.com`

User access:
- `http://<agent-ip>:19073/hk-emby/...`
