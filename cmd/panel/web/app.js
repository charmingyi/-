const $ = (s) => document.querySelector(s);

const tokenInput = $("#adminToken");
const tokenBtn = $("#saveToken");
const commandBox = $("#commandBox");

let state = { agents: [], upstreams: [], routes: [] };

function safeArray(v) {
  return Array.isArray(v) ? v : [];
}

function headers() {
  const token = localStorage.getItem("admin_token") || "";
  const h = { "Content-Type": "application/json" };
  if (token) h["X-Admin-Token"] = token;
  return h;
}

async function api(path, options = {}) {
  const resp = await fetch(path, {
    ...options,
    headers: { ...headers(), ...(options.headers || {}) },
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
  return data;
}

function panelBase() {
  return `${window.location.protocol}//${window.location.host}`;
}

function cmdInstall(agent) {
  const base = panelBase();
  return `sudo env PANEL_URL='${base}' AGENT_ID='${agent.id}' AGENT_TOKEN='${agent.token}' bash -c 'tmp=$(mktemp); curl -fsSL ${base}/install/agent-menu.sh -o "$tmp"; bash "$tmp" menu; rm -f "$tmp"'`;
}

function cmdUninstall() {
  const base = panelBase();
  return `sudo bash -c 'tmp=$(mktemp); curl -fsSL ${base}/install/agent-menu.sh -o "$tmp"; bash "$tmp" uninstall; rm -f "$tmp"'`;
}

async function copyCommand(text) {
  commandBox.value = text;
  try {
    await navigator.clipboard.writeText(text);
  } catch (_e) {}
}

function statusLabel(agent) {
  if (agent.online) return "ONLINE";
  if (agent.last_seen) return "OFFLINE";
  return "NEVER";
}

function statusClass(agent) {
  if (agent.online) return "online";
  if (agent.last_seen) return "offline";
  return "never";
}

function renderAgents() {
  const agents = safeArray(state.agents);
  const host = $("#agentList");

  host.innerHTML =
    '<div class="list">' +
    agents
      .map((a) => {
        return `
          <div class="item item-agent">
            <div class="item-main">
              <div class="title-row">
                <b>${a.name}</b>
                <span class="badge ${statusClass(a)}">${statusLabel(a)}</span>
              </div>
              <small>${a.host || "-"} | ${a.id}</small>
              <small>Last seen: ${a.last_seen || "-"}</small>
            </div>
            <button onclick="copyAgentInstall('${a.id}')">复制对接命令</button>
            <button onclick="copyAgentUninstall()">复制卸载命令</button>
            <button onclick="resetToken('${a.id}')">重置Token</button>
            <button class="danger" onclick="delAgent('${a.id}')">删除</button>
          </div>
        `;
      })
      .join("") +
    "</div>";
}

function renderUpstreams() {
  const upstreams = safeArray(state.upstreams);
  const host = $("#upstreamList");
  host.innerHTML =
    '<div class="list">' +
    upstreams
      .map(
        (u) => `
      <div class="item">
        <div>
          <b>${u.name}</b>
          <small>${u.base_url}</small>
        </div>
        <div></div><div></div>
        <button class="danger" onclick="delUpstream('${u.id}')">删除</button>
      </div>
    `
      )
      .join("") +
    "</div>";
}

function renderRoutes() {
  const routes = safeArray(state.routes);
  const agents = safeArray(state.agents);
  const upstreams = safeArray(state.upstreams);
  const aMap = Object.fromEntries(agents.map((a) => [a.id, a.name]));
  const uMap = Object.fromEntries(upstreams.map((u) => [u.id, u.name]));
  const host = $("#routeList");

  host.innerHTML =
    '<div class="list">' +
    routes
      .map(
        (r) => `
      <div class="item">
        <div>
          <b>${aMap[r.agent_id] || r.agent_id} -> ${uMap[r.upstream_id] || r.upstream_id}</b>
          <small>${r.domain ? `Domain: ${r.domain}` : `Path: ${r.path_prefix}`}</small>
        </div>
        <div></div><div></div>
        <button class="danger" onclick="delRoute('${r.id}')">删除</button>
      </div>
    `
      )
      .join("") +
    "</div>";
}

function renderBinds() {
  const agents = safeArray(state.agents);
  const upstreams = safeArray(state.upstreams);
  $("#bindAgent").innerHTML =
    `<option value="">选择 Agent</option>` +
    agents.map((a) => `<option value="${a.id}">${a.name}</option>`).join("");
  $("#bindUpstream").innerHTML =
    `<option value="">选择 Emby</option>` +
    upstreams.map((u) => `<option value="${u.id}">${u.name}</option>`).join("");
}

function render() {
  renderAgents();
  renderUpstreams();
  renderRoutes();
  renderBinds();
}

async function loadState() {
  const s = await api("/api/state");
  state = {
    agents: safeArray(s.agents),
    upstreams: safeArray(s.upstreams),
    routes: safeArray(s.routes),
  };
  render();
}

window.copyAgentInstall = async (id) => {
  const agent = safeArray(state.agents).find((a) => a.id === id);
  if (!agent) return;
  await copyCommand(cmdInstall(agent));
};

window.copyAgentUninstall = async () => {
  await copyCommand(cmdUninstall());
};

window.delAgent = async (id) => {
  if (!confirm("Delete this agent?")) return;
  await api(`/api/agents/${id}`, { method: "DELETE" });
  await loadState();
};

window.resetToken = async (id) => {
  await api(`/api/agents/${id}/reset-token`, { method: "POST" });
  await loadState();
};

window.delUpstream = async (id) => {
  if (!confirm("Delete this upstream?")) return;
  await api(`/api/upstreams/${id}`, { method: "DELETE" });
  await loadState();
};

window.delRoute = async (id) => {
  if (!confirm("Delete this binding?")) return;
  await api(`/api/routes/${id}`, { method: "DELETE" });
  await loadState();
};

$("#agentForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  await api("/api/agents", {
    method: "POST",
    body: JSON.stringify({
      name: String(f.get("name") || "").trim(),
      host: String(f.get("host") || "").trim(),
    }),
  });
  e.target.reset();
  await loadState();
});

$("#upstreamForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  const scheme = String(f.get("scheme") || "https").trim();
  const portRaw = Number(f.get("port"));
  const port = Number.isFinite(portRaw) && portRaw > 0 ? portRaw : (scheme === "https" ? 443 : 80);
  await api("/api/upstreams", {
    method: "POST",
    body: JSON.stringify({
      name: String(f.get("name") || "").trim(),
      scheme,
      host: String(f.get("host") || "").trim(),
      port,
    }),
  });
  e.target.reset();
  await loadState();
});

$("#bindForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  const agentID = String(f.get("agent_id") || "");
  const upstreamID = String(f.get("upstream_id") || "");
  const domain = String(f.get("domain") || "").trim();
  if (!agentID || !upstreamID) {
    alert("请先选择 Agent 和 Emby");
    return;
  }
  await api("/api/routes", {
    method: "POST",
    body: JSON.stringify({
      name: "",
      domain,
      path_prefix: domain ? "/" : "",
      agent_id: agentID,
      upstream_id: upstreamID,
    }),
  });
  e.target.reset();
  await loadState();
});

tokenBtn.addEventListener("click", () => {
  localStorage.setItem("admin_token", tokenInput.value.trim());
  loadState().catch((e) => alert(e.message || String(e)));
});

(async () => {
  tokenInput.value = localStorage.getItem("admin_token") || "";
  await loadState();
})().catch((e) => alert(e.message || String(e)));
