# Emby Relay Hub

## 0. 一键部署（推荐）

在 Ubuntu/Debian 上可直接执行：

```bash
bash deploy.sh
```

脚本会自动：

- 安装 Python/venv/pip（apt）
- 创建虚拟环境并安装依赖
- 生成并启动 `systemd` 服务（`emby-relay-hub`）

一个用于管理 **Emby 反代线路** 的网站：

- 可以添加多个 `Agent`（你手里的优化服务器，部署轻量 relay 服务）。
- 可以添加多个 Emby 源站（非优化线路服务器）。
- 可以创建 `Route`，把某个 Agent 绑定到某个源站。
- 通过统一入口 `/proxy/<slug>/...` 进行访问。

## 1. 安装依赖

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
```

## 2. 启动管理面板（Hub）

```bash
uvicorn app:app --host 0.0.0.0 --port 8000
```

打开：`http://127.0.0.1:8000`

## 3. 在优化服务器启动轻量 Agent

复制 `agent/relay_agent.py` 到你的优化机，然后启动：

```bash
uvicorn relay_agent:app --host 0.0.0.0 --port 9000
```

将文件中的 `AGENT_TOKEN` 改成安全随机值，并在 Hub 中录入同样 token。

## 4. 使用方式

1. 添加 Agent（例如 `http://优化机IP:9000`）。
2. 添加 Emby 源站（例如 `http://源站IP:8096`）。
3. 创建 Route（例如 slug=`main`）。
4. 访问示例：
   - `http://你的Hub:8000/proxy/main/web/index.html`

## 说明

- 这个版本是最小可用版本，方便你先跑通。
- 生产环境建议配合 Nginx/Caddy、HTTPS、鉴权、日志、速率限制。
