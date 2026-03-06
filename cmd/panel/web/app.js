const $ = (s) => document.querySelector(s);

const tokenInput = $("#adminToken");
const tokenBtn = $("#saveToken");
const commandBox = $("#commandBox");

let state = { agents: [], upstreams: [], routes: [] };

function safeArray(v) {
  return Array.isArray(v) ? v : [];
}

function getHeaders() {
  const token = localStorage.getItem("admin_token") || "";
  const headers = { "Content-Type": "application/json" };
  if (token) headers["X-Admin-Token"] = token;
  return headers;
}

function showError(e) {
  alert(e?.error || e?.message || String(e));
}

async function api(path, opt = {}) {
  const resp = await fetch(path, {
    ...opt,
    headers: { ...getHeaders(), ...(opt.headers || {}) },
  });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw data;
  return data;
}

function panelBase() {
  return `${window.location.protocol}//${window.location.host}`;
}

function agentMenuCommand(agent) {
  const base = panelBase();
  return `curl -fsSL ${base}/install/agent-menu.sh | sudo env PANEL_URL='${base}' AGENT_ID='${agent.id}' AGENT_TOKEN='${agent.token}' bash -s -- menu`;
}

function agentUninstallCommand() {
  const base = panelBase();
  return `curl -fsSL ${base}/install/agent-menu.sh | sudo bash -s -- uninstall`;
}

async function copyText(text) {
  await navigator.clipboard.writeText(text);
  commandBox.value = text;
}

function renderAgents() {
  const list = $("#agentList");
  const agents = safeArray(state.agents);

  list.innerHTML =
    '<div class="list">' +
    agents
      .map((a) => {
        const menuCmd = agentMenuCommand(a);
        const uninstallCmd = agentUninstallCommand();
        return `
          <div class="item item-agent">
            <div class="item-main">
              <b>${a.name}</b>
              <small>${a.id} | ${a.host || "未填写地址"}</small>
              <div class="cmd-row">
                <label>对接菜单命令</label>
                <code>${menuCmd}</code>
              </div>
              <div class="cmd-row">
                <label>卸载命令</label>
                <code>${uninstallCmd}</code>
              </div>
            </div>
            <button onclick="copyAgentMenuCmd('${a.id}')">复制对接命令</button>
            <button onclick="copyAgentUninstallCmd('${a.id}')">复制卸载命令</button>
            <button onclick="resetToken('${a.id}')">重置Token</button>
            <button class="danger" onclick="delAgent('${a.id}')">删除</button>
          </div>
        `;
      })
      .join("") +
    "</div>";
}

function renderUpstreams() {
  const list = $("#upstreamList");
  const upstreams = safeArray(state.upstreams);

  list.innerHTML =
    '<div class="list">' +
    upstreams
      .map(
        (u) => `
          <div class="item">
            <div><b>${u.name}</b><small>${u.base_url}</small></div>
            <div></div><div></div>
            <button class="danger" onclick="delUpstream('${u.id}')">删除</button>
          </div>
        `
      )
      .join("") +
    "</div>";
}

function renderRoutes() {
  const list = $("#routeList");
  const routes = safeArray(state.routes);
  const agents = safeArray(state.agents);
  const upstreams = safeArray(state.upstreams);

  const agentName = Object.fromEntries(agents.map((a) => [a.id, a.name]));
  const upstreamName = Object.fromEntries(upstreams.map((u) => [u.id, u.name]));

  list.innerHTML =
    '<div class="list">' +
    routes
      .map(
        (r) => `
          <div class="item">
            <div>
              <b>${r.name}</b>
              <small>${r.path_prefix} -> ${upstreamName[r.upstream_id] || r.upstream_id} @ ${agentName[r.agent_id] || r.agent_id}</small>
            </div>
            <div></div><div></div>
            <button class="danger" onclick="delRoute('${r.id}')">删除</button>
          </div>
        `
      )
      .join("") +
    "</div>";
}

function renderSelects() {
  const agents = safeArray(state.agents);
  const upstreams = safeArray(state.upstreams);
  $("#routeAgent").innerHTML = agents
    .map((a) => `<option value="${a.id}">${a.name} (${a.id})</option>`)
    .join("");
  $("#routeUpstream").innerHTML = upstreams
    .map((u) => `<option value="${u.id}">${u.name}</option>`)
    .join("");
}

function render() {
  state.agents = safeArray(state.agents);
  state.upstreams = safeArray(state.upstreams);
  state.routes = safeArray(state.routes);
  renderAgents();
  renderUpstreams();
  renderRoutes();
  renderSelects();
}

async function loadState() {
  const data = await api("/api/state");
  state = {
    agents: safeArray(data.agents),
    upstreams: safeArray(data.upstreams),
    routes: safeArray(data.routes),
  };
  render();
}

window.delAgent = async (id) => {
  if (!confirm("确认删除 Agent？")) return;
  await api(`/api/agents/${id}`, { method: "DELETE" });
  await loadState();
};

window.resetToken = async (id) => {
  await api(`/api/agents/${id}/reset-token`, { method: "POST" });
  await loadState();
};

window.delUpstream = async (id) => {
  if (!confirm("确认删除源站？")) return;
  await api(`/api/upstreams/${id}`, { method: "DELETE" });
  await loadState();
};

window.delRoute = async (id) => {
  if (!confirm("确认删除规则？")) return;
  await api(`/api/routes/${id}`, { method: "DELETE" });
  await loadState();
};

window.copyAgentMenuCmd = async (id) => {
  const agent = safeArray(state.agents).find((a) => a.id === id);
  if (!agent) return;
  await copyText(agentMenuCommand(agent));
};

window.copyAgentUninstallCmd = async (id) => {
  const _agent = safeArray(state.agents).find((a) => a.id === id);
  if (!_agent) return;
  await copyText(agentUninstallCommand());
};

$("#agentForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const form = new FormData(e.target);
  await api("/api/agents", {
    method: "POST",
    body: JSON.stringify(Object.fromEntries(form.entries())),
  });
  e.target.reset();
  await loadState();
});

$("#upstreamForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const form = new FormData(e.target);
  await api("/api/upstreams", {
    method: "POST",
    body: JSON.stringify(Object.fromEntries(form.entries())),
  });
  e.target.reset();
  await loadState();
});

$("#routeForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const form = new FormData(e.target);
  await api("/api/routes", {
    method: "POST",
    body: JSON.stringify(Object.fromEntries(form.entries())),
  });
  e.target.reset();
  await loadState();
});

tokenBtn.addEventListener("click", () => {
  localStorage.setItem("admin_token", tokenInput.value.trim());
  loadState().catch(showError);
});

(async () => {
  tokenInput.value = localStorage.getItem("admin_token") || "";
  await loadState();
})().catch(showError);
