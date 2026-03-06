# Emby Relay Hub

一个用于 Emby 多源站反代分发的控制面板，配套轻量 Agent，适合把非优化线路源站通过优化机中转出去。

## 一键部署（执行后直接进入菜单）
直接在服务器执行：

```bash
bash <(curl -L -s https://raw.githubusercontent.com/charmingyi/-/codex/create-reverse-proxy-website-for-emby-kzecse/deploy.sh)
```

会进入菜单，可选：
- `1) Install`
- `2) Update`
- `3) Uninstall`
- `4) Status`
- `5) Logs`

脚本会自动处理：
- 自动安装基础依赖：`curl`、`tar`、`ca-certificates`（缺失时）
- 自动安装 Go（默认 `1.22.12`，可通过 `GO_VERSION` 覆盖）
- 自动下载仓库源码并编译 Panel
- 自动写入并启动 `systemd` 服务

## 核心功能
- Agent 管理：新增、删除、重置 Token
- Emby 源站管理：新增、删除
- 路由规则：`Agent + Path Prefix -> 指定 Emby 源站`
- Agent 自动拉取配置并热更新
- 面板可生成 Agent 一键接入命令
- Agent 菜单脚本支持：安装 / 更新 / 卸载 / 状态 / 日志

## 默认端口（随机化）
- Panel 默认端口：`18473`
- Agent 默认端口：`19073`

可通过环境变量覆盖：
- `PANEL_LISTEN`，默认 `:18473`
- `PANEL_PUBLIC_URL`，默认自动按服务器 IP 生成
- `ADMIN_TOKEN`，默认空（建议配置）
- `GO_VERSION`，默认 `1.22.12`
- `LISTEN_ADDR`（Agent），默认 `:19073`

## 可选：指定仓库或分支
```bash
REPO_OWNER=myuser REPO_NAME=myrepo REPO_BRANCH=main bash <(curl -L -s https://raw.githubusercontent.com/myuser/myrepo/main/deploy.sh)
```

## 本地开发运行
```bash
go run ./cmd/panel
```

访问地址：
- `http://<你的服务器IP>:18473`

## 构建发布
```bash
bash build-release.sh
```

构建产物：
- `releases/panel-linux-amd64`
- `releases/agent-linux-amd64`
- `releases/agent-linux-arm64`
- `releases/agent-linux-arm`

## Agent 接入方式
在面板创建 Agent 后，页面会生成一条接入命令，复制到 Agent 机器执行。

命令形态：
```bash
curl -fsSL http://<panel>/install/agent-menu.sh | sudo env PANEL_URL='http://<panel>' AGENT_ID='...' AGENT_TOKEN='...' bash -s -- menu
```

## 路由访问示例
规则：
- `/hk-emby -> https://origin-emby.example.com`

用户访问：
- `http://<agent-ip>:19073/hk-emby/...`
