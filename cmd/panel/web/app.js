const $ = (s) => document.querySelector(s);
const tokenInput = $('#adminToken');
const tokenBtn = $('#saveToken');
const commandBox = $('#commandBox');

let state = { agents: [], upstreams: [], routes: [] };

const getHeaders = () => {
  const t = localStorage.getItem('admin_token') || '';
  return t ? { 'Content-Type': 'application/json', 'X-Admin-Token': t } : { 'Content-Type': 'application/json' };
};

function msg(e) {
  alert(e?.error || e?.message || String(e));
}

async function api(path, opt = {}) {
  const resp = await fetch(path, { ...opt, headers: { ...getHeaders(), ...(opt.headers || {}) } });
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw data;
  return data;
}

function render() {
  const agentList = $('#agentList');
  const upstreamList = $('#upstreamList');
  const routeList = $('#routeList');

  agentList.innerHTML = '<div class="list">' + state.agents.map(a => `
    <div class="item">
      <div><b>${a.name}</b><small>${a.id} | ${a.host || '未填写地址'}</small></div>
      <button onclick="genCmd('${a.id}')">命令</button>
      <button onclick="resetToken('${a.id}')">重置Token</button>
      <button class="danger" onclick="delAgent('${a.id}')">删除</button>
    </div>`).join('') + '</div>';

  upstreamList.innerHTML = '<div class="list">' + state.upstreams.map(u => `
    <div class="item">
      <div><b>${u.name}</b><small>${u.base_url}</small></div>
      <div></div><div></div>
      <button class="danger" onclick="delUpstream('${u.id}')">删除</button>
    </div>`).join('') + '</div>';

  const aName = Object.fromEntries(state.agents.map(a => [a.id, a.name]));
  const uName = Object.fromEntries(state.upstreams.map(u => [u.id, u.name]));
  routeList.innerHTML = '<div class="list">' + state.routes.map(r => `
    <div class="item">
      <div><b>${r.name}</b><small>${r.path_prefix} -> ${uName[r.upstream_id] || r.upstream_id} @ ${aName[r.agent_id] || r.agent_id}</small></div>
      <div></div><div></div>
      <button class="danger" onclick="delRoute('${r.id}')">删除</button>
    </div>`).join('') + '</div>';

  const agentOpts = state.agents.map(a => `<option value="${a.id}">${a.name} (${a.id})</option>`).join('');
  $('#routeAgent').innerHTML = agentOpts;
  $('#cmdAgent').innerHTML = '<option value="">选择 Agent</option>' + agentOpts;
  $('#routeUpstream').innerHTML = state.upstreams.map(u => `<option value="${u.id}">${u.name}</option>`).join('');
}

async function loadState() {
  state = await api('/api/state');
  render();
}

window.delAgent = async (id) => { if (confirm('删除 Agent?')) { await api('/api/agents/' + id, { method: 'DELETE' }); await loadState(); } };
window.resetToken = async (id) => { await api('/api/agents/' + id + '/reset-token', { method: 'POST' }); await loadState(); };
window.delUpstream = async (id) => { if (confirm('删除源站?')) { await api('/api/upstreams/' + id, { method: 'DELETE' }); await loadState(); } };
window.delRoute = async (id) => { if (confirm('删除规则?')) { await api('/api/routes/' + id, { method: 'DELETE' }); await loadState(); } };

window.genCmd = async (id) => {
  const data = await api('/api/agent/install-command?id=' + encodeURIComponent(id));
  commandBox.value = data.command;
  commandBox.select();
};

$('#genCmd').addEventListener('click', async () => {
  const id = $('#cmdAgent').value;
  if (!id) return;
  await window.genCmd(id);
});

$('#agentForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  await api('/api/agents', { method: 'POST', body: JSON.stringify(Object.fromEntries(f.entries())) });
  e.target.reset();
  await loadState();
});

$('#upstreamForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  await api('/api/upstreams', { method: 'POST', body: JSON.stringify(Object.fromEntries(f.entries())) });
  e.target.reset();
  await loadState();
});

$('#routeForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const f = new FormData(e.target);
  await api('/api/routes', { method: 'POST', body: JSON.stringify(Object.fromEntries(f.entries())) });
  e.target.reset();
  await loadState();
});

tokenBtn.addEventListener('click', () => {
  localStorage.setItem('admin_token', tokenInput.value.trim());
  loadState().catch(msg);
});

(async () => {
  tokenInput.value = localStorage.getItem('admin_token') || '';
  await loadState();
})().catch(msg);

