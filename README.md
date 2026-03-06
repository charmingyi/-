# Emby Relay Hub（小白可用）

这是一个用于管理 **Emby 反代线路** 的网站：

- 你可以添加多个 `Agent`（优化线路服务器）。
- 你可以添加多个 Emby 源站（你现有的 Emby 服务器）。
- 你可以创建 `Route`（把某个 Agent 绑定到某个源站）。
- 最终通过统一入口 `/proxy/<slug>/...` 访问。

---

## 一、默认端口是多少？

- **默认端口是 `8000`**（Hub 管理后台 + 反代入口都在这个端口）。
- 默认监听 `0.0.0.0`，即所有网卡。
- 示例访问地址：`http://你的服务器IP:8000`

如果你要改端口，例如改成 `9001`：

```bash
PORT=9001 bash deploy.sh
```

---

## 二、空白环境一键安装（推荐）

> 适用于 Ubuntu/Debian 新机器。

直接执行（和你给的 `bash <(curl -L -s ...)` 用法一致）：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/charmingyi/-/codex/create-reverse-proxy-website-for-emby/install-online.sh)
```

这个脚本会自动：

1. 安装基础依赖（git/curl/python3/venv/pip/sudo）
2. 拉取本仓库代码到 `/opt/emby-relay-hub`
3. 执行 `deploy.sh` 自动部署
4. 创建并启动 systemd 服务：`emby-relay-hub`

### 可选：自定义安装目录/分支/端口

```bash
INSTALL_DIR=/data/emby-relay \
BRANCH=codex/create-reverse-proxy-website-for-emby \
PORT=9001 \
HOST=0.0.0.0 \
bash <(curl -fsSL https://raw.githubusercontent.com/charmingyi/-/codex/create-reverse-proxy-website-for-emby/install-online.sh)
```

---

## 三、本地仓库一键部署（你已在项目目录时）

```bash
bash deploy.sh
```

`deploy.sh` 会自动：

- 安装 Python 运行依赖
- 创建虚拟环境 `.venv`
- 安装 `requirements.txt`
- 注册并启动 systemd 服务 `emby-relay-hub`

### 常用运维命令

```bash
sudo systemctl status emby-relay-hub --no-pager
sudo systemctl restart emby-relay-hub
sudo journalctl -u emby-relay-hub -f
```

---

## 四、在优化机部署轻量 Agent

把 `agent/relay_agent.py` 放到优化线路服务器，然后运行：

```bash
uvicorn relay_agent:app --host 0.0.0.0 --port 9000
```

请先修改 `relay_agent.py` 里的：

- `AGENT_TOKEN = "change-me"`

改成你自己的强随机字符串，并在 Hub 页面添加 Agent 时填相同 Token。

---

## 五、页面怎么用（新手步骤）

1. 打开 `http://你的服务器IP:8000`
2. 在“新增 Agent”中填写优化机地址（如 `http://1.2.3.4:9000`）和 Token
3. 在“新增 Emby 源站”中填写源站地址（如 `http://emby-home:8096`）
4. 在“创建反代入口”中绑定 Agent + 源站，并设置 slug（如 `main`）
5. 用生成的入口访问：
   - `http://你的HubIP:8000/proxy/main/web/index.html`

---

## 六、注意事项（建议）

- 建议给 Hub 前面加 Nginx/Caddy，并开启 HTTPS。
- 建议配置防火墙，仅放通你需要的端口。
- 生产环境建议增加访问鉴权和日志告警。
