const $ = (s) => document.querySelector(s);

const tokenInput = $("#adminToken");
const tokenBtn = $("#saveToken");
const commandBox = $("#commandBox");
const tlsModeSelect = $("#tlsMode");
const caPemInput = $("#caPem");
const probeBtn = $("#probeUpstream");

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
      .map(
        (a) => `
      <div class="item item-agent">
        <div class="item-main">
          <div class="title-row">
            <b>${a.name}</b>
            <span class="badge ${statusClass(a)}">${statusLabel(a)}</span>
          </div>
          <small>${a.host || "-"} | ${a.id}</small>
          <small>Last seen: ${a.last_seen || "-"}</small>
        </div>
        <button onclick="copyAgentInstall('${a.id}')">Copy Install Cmd</button>
        <button onclick="copyAgentUninstall()">Copy Uninstall Cmd</button>
        <button onclick="resetToken('${a.id}')">Reset Token</button>
        <button class="danger" onclick="delAgent('${a.id}')">Delete</button>
      </div>
    `
      )
      .join("") +
    "</div>";
}

function renderUpstreams() {
  const upstreams = safeArray(state.upstreams);
  const host = $("#upstreamList");
  host.innerHTML =
    '<div class="list">' +
    upstreams
      .map((u) => {
        const tls = u.tls_mode || (u.insecure_tls ? "insecure" : "strict");
        return `
      <div class="item">
        <div>
          <b>${u.name}</b>
          <small>${u.base_url} | tls=${tls}</small>
        </div>
        <button onclick="probeSavedUpstream('${u.id}')">Probe</button>
        <div></div>
        <button class="danger" onclick="delUpstream('${u.id}')">Delete</button>
      </div>
    `;
      })
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
        <button onclick="verifyRoute('${r.id}')">Verify</button>
        <div></div>
        <button class="danger" onclick="delRoute('${r.id}')">Delete</button>
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
    `<option value="">Select Agent</option>` +
    agents.map((a) => `<option value="${a.id}">${a.name}</option>`).join("");
  $("#bindUpstream").innerHTML =
    `<option value="">Select Emby</option>` +
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

window.verifyRoute = async (id) => {
  const data = await api(`/api/routes/verify?id=${encodeURIComponent(id)}`);
  alert(data.ok ? `Verify OK, status=${data.status}` : `Verify FAIL, status=${data.status || 0}, error=${data.error || "-"}`);
};

function upstreamPayloadFromForm(formData) {
  const scheme = String(formData.get("scheme") || "https").trim();
  const portRaw = Number(formData.get("port"));
  const port = Number.isFinite(portRaw) && portRaw > 0 ? portRaw : (scheme === "https" ? 443 : 80);
  return {
    name: String(formData.get("name") || "").trim(),
    scheme,
    host: String(formData.get("host") || "").trim(),
    port,
    path: String(formData.get("path") || "").trim(),
    tls_mode: String(formData.get("tls_mode") || "strict").trim(),
    ca_cert_pem: String(formData.get("ca_cert_pem") || "").trim(),
  };
}

async function doProbe(payload) {
  const data = await api("/api/upstreams/probe", {
    method: "POST",
    body: JSON.stringify(payload),
  });
  alert(data.ok ? `Probe OK: ${data.reason}` : `Probe FAIL: ${data.reason}`);
}

window.probeSavedUpstream = async (id) => {
  const u = safeArray(state.upstreams).find((x) => x.id === id);
  if (!u) return;
  await doProbe({
    base_url: u.base_url,
    tls_mode: u.tls_mode || (u.insecure_tls ? "insecure" : "strict"),
    ca_cert_pem: u.ca_cert_pem || "",
  });
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

probeBtn.addEventListener("click", async () => {
  const form = new FormData($("#upstreamForm"));
  await doProbe(upstreamPayloadFromForm(form));
});

$("#upstreamForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  await api("/api/upstreams", {
    method: "POST",
    body: JSON.stringify(upstreamPayloadFromForm(f)),
  });
  e.target.reset();
  caPemInput.style.display = "none";
  await loadState();
});

$("#bindForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  const agentID = String(f.get("agent_id") || "");
  const upstreamID = String(f.get("upstream_id") || "");
  const domain = String(f.get("domain") || "").trim();
  if (!agentID || !upstreamID) {
    alert("Please select Agent and Emby");
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

tlsModeSelect.addEventListener("change", () => {
  caPemInput.style.display = tlsModeSelect.value === "custom_ca" ? "block" : "none";
});

tokenBtn.addEventListener("click", () => {
  localStorage.setItem("admin_token", tokenInput.value.trim());
  loadState().catch((e) => alert(e.message || String(e)));
});

(async () => {
  tokenInput.value = localStorage.getItem("admin_token") || "";
  caPemInput.style.display = tlsModeSelect.value === "custom_ca" ? "block" : "none";
  await loadState();
})().catch((e) => alert(e.message || String(e)));
