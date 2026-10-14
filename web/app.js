'use strict';

/* ------------------------------------------------------------------ 主题切换 */
function initTheme() {
  const saved = localStorage.getItem('oci-help-theme');
  if (saved) {
    document.documentElement.setAttribute('data-theme', saved);
  }
}
function toggleTheme() {
  const root = document.documentElement;
  const current = root.getAttribute('data-theme');
  const isDarkPreferred = window.matchMedia('(prefers-color-scheme: dark)').matches;
  let next;

  if (current === 'dark') {
    next = 'light';
  } else if (current === 'light') {
    next = 'dark';
  } else {
    // 跟随系统时，切换到相反主题
    next = isDarkPreferred ? 'light' : 'dark';
  }
  root.setAttribute('data-theme', next);
  localStorage.setItem('oci-help-theme', next);
}
initTheme();

/* ------------------------------------------------------------------ 状态 */
const state = {
  account: '',
  accounts: [],
  instances: [],
  volumes: [],
  templates: [],
  jobs: [],
  currentJobId: '',
  activeTab: 'instances',
  timer: null,
  logBuffers: {},      // jobId -> { lines: [], next: 0 }
  loaded: {},          // account -> { instances: bool, volumes: bool }  缓存标记
};

/* ------------------------------------------------------------------ 路径前缀 */
// 服务端可能配置了访问路径（[web] path=/xxx），此时接口与资源都在该前缀下。
// 从当前文档 URL 推导前缀，兼容根路径部署（BASE 为空串）。
var BASE = (function () {
  var p = window.location.pathname;
  if (p.slice(-11) === '/index.html') p = p.slice(0, -11);
  if (p.slice(-1) === '/') p = p.slice(0, -1);
  return p; // '' 或 '/my-secret'
})();

/* ------------------------------------------------------------------ 鉴权 */
// 凭证存 sessionStorage：关闭标签页即失效，刷新不丢。
function getCred() {
  return sessionStorage.getItem('oci-help-cred') || '';
}
function setCred(user, pass) {
  const s = user + ':' + pass;
  sessionStorage.setItem('oci-help-cred', btoa(unescape(encodeURIComponent(s))));
}
function clearCred() {
  sessionStorage.removeItem('oci-help-cred');
}

let loginResolve = null;
let loginReject = null;
let loginVisible = false;

function showLogin(errMsg) {
  const modal = document.getElementById('loginModal');
  const errEl = document.getElementById('loginError');
  modal.classList.remove('hidden');
  loginVisible = true;
  if (errMsg) {
    errEl.textContent = errMsg;
    errEl.classList.remove('hidden');
  } else {
    errEl.classList.add('hidden');
  }
  document.getElementById('loginUser').focus();
}
function hideLogin() {
  document.getElementById('loginModal').classList.add('hidden');
  document.getElementById('loginPass').value = '';
  loginVisible = false;
}

// promptLogin 返回 Promise，等用户提交后才 resolve；在别处取消登录则 reject。
function promptLogin(errMsg) {
  return new Promise(function (resolve, reject) {
    loginResolve = resolve;
    loginReject = reject;
    showLogin(errMsg);
  });
}

/* ------------------------------------------------------------------ 基础工具 */
async function api(path, options, _retried) {
  const opts = Object.assign({ method: 'GET', headers: {} }, options || {});
  if (opts.body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(opts.body);
  }
  const cred = getCred();
  if (cred) {
    opts.headers['Authorization'] = 'Basic ' + cred;
  }
  const res = await fetch(BASE + path, opts);

  // 401：触发登录弹窗，成功后重试
  if (res.status === 401) {
    if (_retried) {
      // 已经重试过还 401：说明凭证无效，清空并给提示
      clearCred();
      throw new Error('用户名或密码错误');
    }
    clearCred();
    await promptLogin(res.status === 401 ? undefined : undefined); // 等登录
    return api(path, options, true);  // 用新凭证重试
  }

  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch (e) { data = { error: text }; }
  if (!res.ok) throw new Error(data.error || ('HTTP ' + res.status));
  return data;
}

function esc(v) {
  return String(v == null ? '' : v)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

let toastTimer = null;
function toast(msg, type) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast' + (type ? ' ' + type : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(function () { el.classList.add('hidden'); }, 4000);
}

function openModal(title, html) {
  document.getElementById('modalTitle').textContent = title;
  document.getElementById('modalBody').innerHTML = html;
  document.getElementById('modal').classList.remove('hidden');
}
function closeModal() {
  document.getElementById('modal').classList.add('hidden');
}

/* ---- 自定义确认框 / 输入框（替换浏览器原生 confirm / prompt） ---- */
var dialogResolve = null;

function closeDialog(result) {
  document.getElementById('dialogModal').classList.add('hidden');
  var r = dialogResolve;
  dialogResolve = null;
  if (r) r(result);
}

// uiConfirm 返回 Promise<boolean>
function uiConfirm(message, opts) {
  opts = opts || {};
  return new Promise(function (resolve) {
    dialogResolve = resolve;
    document.getElementById('dialogTitle').textContent = opts.title || '确认操作';
    document.getElementById('dialogMessage').textContent = message;
    var input = document.getElementById('dialogInput');
    input.classList.add('hidden');
    input.value = '';
    var ok = document.getElementById('dialogOk');
    ok.textContent = opts.okText || '确定';
    ok.className = 'btn ' + (opts.danger ? 'danger' : 'primary');
    document.getElementById('dialogModal').classList.remove('hidden');
    ok.focus();
  });
}

// uiPrompt 返回 Promise<string|null>，取消返回 null
function uiPrompt(message, defaultValue, opts) {
  opts = opts || {};
  return new Promise(function (resolve) {
    dialogResolve = resolve;
    document.getElementById('dialogTitle').textContent = opts.title || '请输入';
    document.getElementById('dialogMessage').textContent = message;
    var input = document.getElementById('dialogInput');
    input.classList.remove('hidden');
    input.value = defaultValue || '';
    var ok = document.getElementById('dialogOk');
    ok.textContent = opts.okText || '确定';
    ok.className = 'btn primary';
    document.getElementById('dialogModal').classList.remove('hidden');
    input.focus();
    input.select();
  });
}

function stateClass(raw) {
  if (!raw) return 'state-pending';
  if (raw === 'RUNNING') return 'state-running';
  if (raw === 'STOPPED' || raw === 'STOPPING') return 'state-stopped';
  if (raw.indexOf('TERMINAT') === 0) return 'state-dead';
  return 'state-pending';
}

function numOrNull(id) {
  const v = document.getElementById(id).value.trim();
  if (v === '') return null;
  const n = Number(v);
  return isNaN(n) ? null : n;
}

/* ------------------------------------------------------------------ 版本 */
async function loadVersion() {
  const el = document.getElementById('appVersion');
  if (!el) return;
  try {
    const data = await api('/api/version');
    const v = (data.version || '').trim();
    if (!v) return;
    el.textContent = v;
    // 悬停显示 commit 与构建时间
    const extra = [];
    if (data.commit) extra.push('commit ' + String(data.commit).slice(0, 7));
    if (data.buildTime) extra.push(data.buildTime);
    if (extra.length) el.title = extra.join(' · ');
  } catch (e) { /* 版本信息获取失败不影响主流程 */ }
}

/* ------------------------------------------------------------------ 账号 */
async function loadAccounts() {
  const data = await api('/api/accounts');
  state.accounts = data.accounts || [];
  const sel = document.getElementById('accountSelect');
  sel.innerHTML = '';
  state.accounts.forEach(function (a) {
    const opt = document.createElement('option');
    opt.value = a.name;
    opt.textContent = a.name + (a.region ? ' (' + a.region + ')' : '');
    sel.appendChild(opt);
  });
  if (state.accounts.length > 0) {
    state.account = state.accounts[0].name;
    sel.value = state.account;
  }
}

/* ------------------------------------------------------------------ 实例 */
// 数据加载期间显示占位行，避免误显示「暂无实例 / 暂无引导卷」
function setTableLoading(tableId, colspan) {
  const tbody = document.querySelector('#' + tableId + ' tbody');
  if (tbody) tbody.innerHTML = '<tr><td colspan="' + colspan + '" class="empty loading">加载中…</td></tr>';
}
function setTableMessage(tableId, colspan, msg) {
  const tbody = document.querySelector('#' + tableId + ' tbody');
  if (tbody) tbody.innerHTML = '<tr><td colspan="' + colspan + '" class="empty">' + esc(msg) + '</td></tr>';
}

function loadedFlag(account) {
  if (!state.loaded[account]) state.loaded[account] = { instances: false, volumes: false };
  return state.loaded[account];
}

async function loadInstances(force) {
  const flag = loadedFlag(state.account);
  if (!force && flag.instances) {
    // 已有缓存数据，直接渲染，不重复请求
    renderInstances();
    document.getElementById('instancesHint').textContent =
      '共 ' + state.instances.length + ' 个实例（点击 ⟳ 刷新）';
    return;
  }
  const hint = document.getElementById('instancesHint');
  hint.textContent = '加载中...';
  setTableLoading('instancesTable', 5);
  try {
    const data = await api('/api/instances?account=' + encodeURIComponent(state.account));
    state.instances = data.instances || [];
    flag.instances = true;
    renderInstances();
    hint.textContent = '共 ' + state.instances.length + ' 个实例（点击 ⟳ 刷新）';
  } catch (e) {
    hint.textContent = '';
    setTableMessage('instancesTable', 5, '加载失败：' + e.message);
    toast('加载实例失败: ' + e.message, 'error');
  }
}

function renderInstances() {
  const tbody = document.querySelector('#instancesTable tbody');
  if (state.instances.length === 0) {
    tbody.innerHTML = '<tr><td colspan="5" class="empty">暂无实例</td></tr>';
    return;
  }
  tbody.innerHTML = state.instances.map(function (ins) {
    const ips = (ins.publicIps || []).join(', ') || '-';
    const cfg = esc(ins.shape) + (ins.ocpus ? ' · ' + ins.ocpus + 'C/' + ins.memoryInGBs + 'G' : '');
    return '<tr>' +
      '<td>' + esc(ins.displayName) + '</td>' +
      '<td><span class="state ' + stateClass(ins.stateRaw) + '">' + esc(ins.state) + '</span></td>' +
      '<td class="mono">' + cfg + '</td>' +
      '<td class="mono">' + esc(ips) + '</td>' +
      '<td class="actions">' +
        '<button class="btn small" data-act="detail" data-id="' + esc(ins.id) + '">详情</button>' +
        '<button class="btn small" data-act="start" data-id="' + esc(ins.id) + '" data-name="' + esc(ins.displayName) + '">启动</button>' +
        '<button class="btn small" data-act="stop" data-id="' + esc(ins.id) + '" data-name="' + esc(ins.displayName) + '">停止</button>' +
        '<button class="btn small" data-act="restart" data-id="' + esc(ins.id) + '" data-name="' + esc(ins.displayName) + '">重启</button>' +
        '<button class="btn small danger" data-act="terminate" data-id="' + esc(ins.id) + '" data-name="' + esc(ins.displayName) + '">终止</button>' +
      '</td></tr>';
  }).join('');
}

async function instanceAction(id, action, name) {
  if (action === 'terminate' &&
      !(await uiConfirm('确定终止实例 ' + (name || id) + ' ？该操作不可恢复。', { danger: true, okText: '终止' }))) return;
  try {
    const data = await api('/api/instance/action', { method: 'POST', body: { account: state.account, id: id, action: action } });
    toast(data.message || '操作成功', 'success');
    // 操作成功后标记缓存失效，确保下次加载拿到最新数据
    const flag = loadedFlag(state.account);
    flag.instances = false;
    setTimeout(function () { loadInstances(true); }, 1200);
  } catch (e) {
    toast('操作失败: ' + e.message, 'error');
  }
}

async function bulkAction(action) {
  const labels = { start: '启动', stop: '停止', restart: '重启', terminate: '终止' };
  const label = labels[action] || action;
  if (!(await uiConfirm('确定' + label + '当前账号下的全部实例？', { danger: action === 'terminate' }))) return;
  try {
    const data = await api('/api/instances/action-all', { method: 'POST', body: { account: state.account, action: action } });
    const results = data.results || [];
    const failed = results.filter(function (r) { return !r.ok; });
    toast(label + '完成：成功 ' + (results.length - failed.length) + '，失败 ' + failed.length,
      failed.length ? 'error' : 'success');
    const flag = loadedFlag(state.account);
    flag.instances = false;
    setTimeout(function () { loadInstances(true); }, 1200);
  } catch (e) {
    toast(label + '失败: ' + e.message, 'error');
  }
}

async function showInstanceDetail(id) {
  openModal('实例详情', '<div class="hint">加载中...</div>');
  try {
    const data = await api('/api/instance?account=' + encodeURIComponent(state.account) + '&id=' + encodeURIComponent(id));
    const d = data.instance;
    const plugins = (d.plugins || []).map(function (p) { return esc(p.name) + ': ' + esc(p.desiredState); }).join('<br>') || '-';
    const html =
      '<div class="kv">' +
        '<div class="k">名称</div><div>' + esc(d.displayName) + '</div>' +
        '<div class="k">状态</div><div><span class="state ' + stateClass(d.stateRaw) + '">' + esc(d.state) + '</span></div>' +
        '<div class="k">公共IP</div><div class="mono">' + (esc((d.publicIps || []).join(', ')) || '-') + '</div>' +
        '<div class="k">私有IP</div><div class="mono">' + (esc((d.privateIps || []).join(', ')) || '-') + '</div>' +
        '<div class="k">可用性域</div><div>' + esc(d.availabilityDomain) + '</div>' +
        '<div class="k">配置</div><div class="mono">' + esc(d.shape) + '</div>' +
        '<div class="k">OCPU / 内存</div><div class="mono">' + d.ocpus + ' C / ' + d.memoryInGBs + ' GB</div>' +
        '<div class="k">网络带宽</div><div class="mono">' + d.networkingBandwidthInGbps + ' Gbps</div>' +
        '<div class="k">监控插件已禁用</div><div>' + d.isMonitoringDisabled + '</div>' +
        '<div class="k">管理插件已禁用</div><div>' + d.isManagementDisabled + '</div>' +
        '<div class="k">所有插件已禁用</div><div>' + d.areAllPluginsDisabled + '</div>' +
        '<div class="k">插件配置</div><div>' + plugins + '</div>' +
      '</div>' +
      '<div class="modal-actions">' +
        '<button class="btn" data-dact="rename" data-id="' + esc(d.id) + '">修改名称</button>' +
        '<button class="btn" data-dact="resize" data-id="' + esc(d.id) + '">升级/降级</button>' +
        '<button class="btn" data-dact="agent-on" data-id="' + esc(d.id) + '">启用监控/管理插件</button>' +
        '<button class="btn" data-dact="agent-off" data-id="' + esc(d.id) + '">禁用监控/管理插件</button>' +
        '<button class="btn" data-dact="newip" data-id="' + esc(d.id) + '">更换公共IP</button>' +
      '</div>';
    openModal('实例详情 - ' + (d.displayName || ''), html);
  } catch (e) {
    openModal('实例详情', '<div class="hint">加载失败: ' + esc(e.message) + '</div>');
  }
}

async function instanceDetailAction(action, id) {
  try {
    if (action === 'rename') {
      const name = await uiPrompt('请输入新的实例名称：', '', { title: '修改名称' });
      if (!name) return;
      await api('/api/instance/update', { method: 'POST', body: { account: state.account, id: id, displayName: name } });
      toast('修改名称成功', 'success');
    } else if (action === 'resize') {
      const ocpus = await uiPrompt('请输入 CPU 个数（例如 1）：', '', { title: '升级 / 降级' });
      if (ocpus === null) return;
      const mem = await uiPrompt('请输入内存大小 GB（例如 6）：', '', { title: '升级 / 降级' });
      if (mem === null) return;
      await api('/api/instance/update', { method: 'POST', body: { account: state.account, id: id, ocpus: Number(ocpus), memoryInGBs: Number(mem) } });
      toast('升级/降级请求已提交', 'success');
    } else if (action === 'agent-on' || action === 'agent-off') {
      const disable = action === 'agent-off';
      if (!(await uiConfirm(disable ? '确定禁用监控与管理插件？' : '确定启用监控与管理插件？'))) return;
      await api('/api/instance/update', { method: 'POST', body: { account: state.account, id: id, agentDisable: disable } });
      toast('插件配置已提交', 'success');
    } else if (action === 'newip') {
      if (!(await uiConfirm('将删除当前公共IP并创建新的公共IP，确定继续？', { danger: true }))) return;
      const data = await api('/api/instance/public-ip', { method: 'POST', body: { account: state.account, id: id } });
      toast('更换成功，新IP: ' + (data.ip || ''), 'success');
    }
    closeModal();
    setTimeout(loadInstances, 1500);
  } catch (e) {
    toast('操作失败: ' + e.message, 'error');
  }
}

async function exportIps() {
  try {
    const data = await api('/api/export-ips', { method: 'POST', body: { account: state.account } });
    const blob = new Blob([data.content || ''], { type: 'text/plain;charset=utf-8' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = data.filename || 'IPs.txt';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(a.href);
    toast('已导出 ' + (data.filename || ''), 'success');
  } catch (e) {
    toast('导出失败: ' + e.message, 'error');
  }
}

/* ------------------------------------------------------------------ 引导卷 */
async function loadVolumes(force) {
  const flag = loadedFlag(state.account);
  if (!force && flag.volumes) {
    renderVolumes();
    document.getElementById('volumesHint').textContent =
      '共 ' + state.volumes.length + ' 个引导卷（点击 ⟳ 刷新）';
    return;
  }
  const hint = document.getElementById('volumesHint');
  hint.textContent = '加载中...';
  setTableLoading('volumesTable', 6);
  try {
    const data = await api('/api/boot-volumes?account=' + encodeURIComponent(state.account));
    state.volumes = data.bootVolumes || [];
    flag.volumes = true;
    renderVolumes();
    hint.textContent = '共 ' + state.volumes.length + ' 个引导卷（点击 ⟳ 刷新）';
  } catch (e) {
    hint.textContent = '';
    setTableMessage('volumesTable', 6, '加载失败：' + e.message);
    toast('加载引导卷失败: ' + e.message, 'error');
  }
}

function renderVolumes() {
  const tbody = document.querySelector('#volumesTable tbody');
  if (state.volumes.length === 0) {
    tbody.innerHTML = '<tr><td colspan="6" class="empty">暂无引导卷</td></tr>';
    return;
  }
  tbody.innerHTML = state.volumes.map(function (v) {
    return '<tr>' +
      '<td>' + esc(v.displayName) + '</td>' +
      '<td>' + esc(v.state) + '</td>' +
      '<td class="mono">' + v.sizeInGBs + '</td>' +
      '<td class="mono">' + v.vpusPerGB + '</td>' +
      '<td class="mono">' + esc(v.availabilityDomain) + '</td>' +
      '<td class="actions">' +
        '<button class="btn small" data-vact="detail" data-id="' + esc(v.id) + '">详情</button>' +
        '<button class="btn small" data-vact="perf" data-id="' + esc(v.id) + '">改性能</button>' +
        '<button class="btn small" data-vact="size" data-id="' + esc(v.id) + '">改大小</button>' +
        '<button class="btn small" data-vact="detach" data-id="' + esc(v.id) + '">分离</button>' +
        '<button class="btn small danger" data-vact="delete" data-id="' + esc(v.id) + '">终止</button>' +
      '</td></tr>';
  }).join('');
}

async function volumeAction(action, id) {
  try {
    if (action === 'perf') {
      const v = await uiPrompt('输入 1（均衡，VPU 10）或 2（性能较高，VPU 20）：', '', { title: '修改引导卷性能' });
      if (v !== '1' && v !== '2') return;
      await api('/api/boot-volume/update', { method: 'POST', body: { account: state.account, id: id, vpusPerGB: v === '1' ? 10 : 20 } });
      toast('修改性能请求已提交', 'success');
    } else if (action === 'size') {
      const v = await uiPrompt('请输入新的引导卷大小 (GB)：', '', { title: '修改引导卷大小' });
      if (!v) return;
      await api('/api/boot-volume/update', { method: 'POST', body: { account: state.account, id: id, sizeInGBs: Number(v) } });
      toast('修改大小请求已提交', 'success');
    } else if (action === 'detach') {
      if (!(await uiConfirm('确定分离该引导卷？'))) return;
      await api('/api/boot-volume/detach', { method: 'POST', body: { account: state.account, id: id } });
      toast('分离请求已提交', 'success');
    } else if (action === 'delete') {
      if (!(await uiConfirm('确定终止该引导卷？该操作不可恢复。', { danger: true, okText: '终止' }))) return;
      await api('/api/boot-volume/delete', { method: 'POST', body: { account: state.account, id: id } });
      toast('终止请求已提交', 'success');
    } else if (action === 'detail') {
      const data = await api('/api/boot-volume?account=' + encodeURIComponent(state.account) + '&id=' + encodeURIComponent(id));
      const bv = data.bootVolume;
      const atts = (data.attachments || []).map(function (a) {
        return esc(a.instanceName || a.instanceId) + ' (' + esc(a.state) + ')';
      }).join('<br>') || '未附加';
      openModal('引导卷详情 - ' + (bv.displayName || ''), '<div class="kv">' +
        '<div class="k">名称</div><div>' + esc(bv.displayName) + '</div>' +
        '<div class="k">状态</div><div>' + esc(bv.state) + '</div>' +
        '<div class="k">可用性域</div><div>' + esc(bv.availabilityDomain) + '</div>' +
        '<div class="k">大小(GB)</div><div>' + bv.sizeInGBs + '</div>' +
        '<div class="k">性能(VPU)</div><div>' + bv.vpusPerGB + '</div>' +
        '<div class="k">附加的实例</div><div>' + atts + '</div>' +
      '</div>');
      return;
    }
    // 操作成功后标记缓存失效，下次加载获取最新数据
    const flag = loadedFlag(state.account);
    flag.volumes = false;
    setTimeout(function () { loadVolumes(true); }, 1500);
  } catch (e) {
    toast('操作失败: ' + e.message, 'error');
  }
}

/* ------------------------------------------------------------------ 创建实例 */
async function loadTemplates() {
  const sel = document.getElementById('templateSelect');
  sel.innerHTML = '';
  try {
    const data = await api('/api/templates?account=' + encodeURIComponent(state.account));
    state.templates = data.templates || [];
    if (state.templates.length === 0) {
      sel.innerHTML = '<option value="">（无可用模版）</option>';
      document.getElementById('templatePreview').innerHTML = '<div class="hint">当前账号未配置实例模版（[INSTANCE.xxx]）。</div>';
      return;
    }
    state.templates.forEach(function (t) {
      const opt = document.createElement('option');
      opt.value = t.name;
      opt.textContent = t.name + ' — ' + t.shape;
      sel.appendChild(opt);
    });
    renderTemplatePreview();
  } catch (e) {
    toast('加载模版失败: ' + e.message, 'error');
  }
}

function renderTemplatePreview() {
  const name = document.getElementById('templateSelect').value;
  const t = state.templates.filter(function (x) { return x.name === name; })[0];
  const box = document.getElementById('templatePreview');
  if (!t) { box.innerHTML = ''; return; }
  box.innerHTML = '<h3>模版详情</h3><div class="kv">' +
    '<div class="k">名称</div><div>' + esc(t.name) + '</div>' +
    '<div class="k">配置</div><div class="mono">' + esc(t.shape) + '</div>' +
    '<div class="k">OCPU / 内存</div><div class="mono">' + t.ocpus + ' C / ' + t.memoryInGBs + ' GB</div>' +
    '<div class="k">引导卷(GB)</div><div>' + t.bootVolumeSizeInGBs + '</div>' +
    '<div class="k">创建个数 (sum)</div><div>' + t.sum + '</div>' +
    '<div class="k">每个域创建 (each)</div><div>' + t.each + '</div>' +
    '<div class="k">重试次数</div><div>' + t.retry + '</div>' +
    '<div class="k">可用性域</div><div>' + (esc(t.availabilityDomain) || '自动') + '</div>' +
    '<div class="k">操作系统</div><div>' + esc(t.operatingSystem) + ' ' + esc(t.operatingSystemVersion) + '</div>' +
  '</div>';
}

async function launch() {
  const template = document.getElementById('templateSelect').value;
  if (!template) { toast('请选择实例模版', 'error'); return; }
  if (!(await uiConfirm('确定按模版「' + template + '」创建实例？任务将在后台运行。'))) return;
  const body = { account: state.account, template: template };
  const sum = numOrNull('launchSum'); if (sum !== null) body.sum = sum;
  const each = numOrNull('launchEach'); if (each !== null) body.each = each;
  const retry = numOrNull('launchRetry'); if (retry !== null) body.retry = retry;
  const ocpus = numOrNull('launchOcpus'); if (ocpus !== null) body.ocpus = ocpus;
  const memory = numOrNull('launchMemory'); if (memory !== null) body.memoryInGBs = memory;
  const boot = numOrNull('launchBoot'); if (boot !== null) body.bootVolumeSizeInGBs = boot;
  const ad = document.getElementById('launchAd').value.trim(); if (ad) body.availabilityDomain = ad;
  const name = document.getElementById('launchName').value.trim(); if (name) body.instanceDisplayName = name;
  const ssh = document.getElementById('launchSsh').value.trim(); if (ssh) body.sshPublicKey = ssh;
  try {
    const data = await api('/api/launch', { method: 'POST', body: body });
    toast('任务已创建: ' + data.jobId, 'success');
    switchTab('jobs');
    state.currentJobId = data.jobId;
    await loadJobs();
  } catch (e) {
    toast('创建失败: ' + e.message, 'error');
  }
}

/* ------------------------------------------------------------------ 任务 */
function findJob(id) {
  return state.jobs.filter(function (j) { return j.id === id; })[0];
}

function statusLabel(job) {
  if (job.status === 'running' && job.stopping) return '停止中';
  return { running: '运行中', success: '成功', partial: '部分成功', failed: '失败', canceled: '已取消' }[job.status] || job.status;
}

async function loadJobs() {
  try {
    const data = await api('/api/jobs');
    state.jobs = (data.jobs || []).sort(function (a, b) {
      return new Date(b.started) - new Date(a.started);
    });
    const job = state.currentJobId ? findJob(state.currentJobId) : null;
    if (job) await fetchJobLogs(job);
    renderJobs();
  } catch (e) { /* 忽略轮询错误 */ }
}

// fetchJobLogs 只拉取 since 之后的增量日志，避免每次轮询都传输全部日志。
async function fetchJobLogs(job) {
  let buf = state.logBuffers[job.id];
  if (!buf) { buf = state.logBuffers[job.id] = { lines: [], next: 0 }; }
  try {
    const data = await api('/api/job?id=' + encodeURIComponent(job.id) + '&since=' + buf.next);
    const d = data.job || {};
    if (typeof d.logFrom === 'number' && d.logFrom > buf.next) {
      buf.lines = [];   // 服务端已裁剪旧日志，本地缓存作废
    }
    if (d.logs && d.logs.length) { buf.lines = buf.lines.concat(d.logs); }
    if (typeof d.logNext === 'number') { buf.next = d.logNext; }
    if (buf.lines.length > 4000) { buf.lines = buf.lines.slice(buf.lines.length - 4000); }
    buf.detail = d;
  } catch (e) { /* 忽略轮询错误 */ }
  return buf;
}

// 删除图标（垃圾桶），用 currentColor 以便随按钮状态变色
var TRASH_ICON =
  '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" ' +
  'stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
  '<path d="M3 6h18"/>' +
  '<path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2"/>' +
  '<path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>' +
  '<line x1="10" y1="11" x2="10" y2="17"/>' +
  '<line x1="14" y1="11" x2="14" y2="17"/>' +
  '</svg>';

function renderJobs() {
  const running = state.jobs.filter(function (j) { return j.status === 'running'; });
  const badge = document.getElementById('jobBadge');
  if (running.length > 0) {
    badge.textContent = running.length;
    badge.classList.remove('hidden');
  } else {
    badge.classList.add('hidden');
  }

  const list = document.getElementById('jobsList');
  if (state.jobs.length === 0) {
    list.innerHTML = '<div class="empty">暂无任务</div>';
  } else {
    list.innerHTML = state.jobs.map(function (j) {
      const active = j.id === state.currentJobId ? ' active' : '';
      // 非运行中的任务可删除
      const del = j.status === 'running' ? '' :
        '<button class="job-del" data-del-job="' + esc(j.id) + '" title="删除任务" aria-label="删除任务">' +
        TRASH_ICON + '</button>';
      return '<div class="job-item' + active + '" data-job="' + esc(j.id) + '">' +
        '<div class="job-id">' + esc(j.id) + '</div>' +
        '<div class="job-meta"><span>' + esc(j.account) + '</span>' +
        '<span><span class="status-tag status-' + esc(j.status) + '">' + esc(statusLabel(j)) + '</span>' + del + '</span></div>' +
      '</div>';
    }).join('');
  }

  const cancelBtn = document.getElementById('cancelJobBtn');
  const job = state.currentJobId ? findJob(state.currentJobId) : null;
  if (job) {
    renderJobDetail(job);
    cancelBtn.disabled = !(job.status === 'running' && !job.stopping);
  } else {
    cancelBtn.disabled = true;
    // 未选中任务（含被删除）时清空详情面板
    document.getElementById('jobDetailHeader').textContent = '选择左侧任务查看详情';
    document.getElementById('jobProgressBar').style.width = '0%';
    document.getElementById('jobLogs').textContent = '';
  }
}

function renderJobDetail(job) {
  const buf = state.logBuffers[job.id] || { lines: [] };
  document.getElementById('jobDetailHeader').innerHTML =
    '<strong>' + esc(job.id) + '</strong> · ' + esc(job.account) + ' · ' + esc(statusLabel(job)) +
    (job.stage ? ' · ' + esc(job.stage) + ' ' + job.done + '/' + job.total : '') +
    (job.error ? '<br><span style="color:var(--danger)">' + esc(job.error) + '</span>' : '');
  const pct = job.total > 0 ? Math.min(100, Math.round(job.done / job.total * 100)) : (job.status === 'running' ? 5 : 100);
  document.getElementById('jobProgressBar').style.width = pct + '%';
  const logs = document.getElementById('jobLogs');
  logs.textContent = buf.lines.join('\n');
  logs.scrollTop = logs.scrollHeight;
}

async function cancelJob() {
  const job = state.currentJobId ? findJob(state.currentJobId) : null;
  if (!job) return;
  if (!(await uiConfirm('确定停止任务 ' + job.id + ' ？已创建的实例不会被删除。', { danger: true, okText: '停止' }))) return;
  try {
    const data = await api('/api/job/cancel', { method: 'POST', body: { id: job.id } });
    toast(data.message || '已请求停止任务', 'success');
    await loadJobs();
  } catch (e) {
    toast('停止失败: ' + e.message, 'error');
  }
}

// 删除单个任务（仅非运行中可删）
async function deleteJob(id) {
  const job = findJob(id);
  if (!job || job.status === 'running') return;
  if (!(await uiConfirm('确定删除任务 ' + id + ' ？仅删除列表记录，不影响已创建的实例。', { danger: true, okText: '删除' }))) return;
  try {
    await api('/api/job/delete', { method: 'POST', body: { id: id } });
    if (state.currentJobId === id) state.currentJobId = '';
    delete state.logBuffers[id];
    toast('任务已删除', 'success');
    await loadJobs();
  } catch (e) {
    toast('删除失败: ' + e.message, 'error');
  }
}

// 按状态批量删除（空状态 = 删除全部已结束任务，保留运行中）
async function deleteJobsByStatus() {
  const sel = document.getElementById('jobDeleteStatus');
  const status = sel.value;
  const label = sel.options[sel.selectedIndex].textContent;
  const hint = status === '' ? '确定删除全部已结束的任务（运行中的任务会保留）？' : '确定删除状态为「' + label + '」的任务？';
  if (!(await uiConfirm(hint, { danger: true, okText: '删除' }))) return;
  try {
    const data = await api('/api/jobs/delete', { method: 'POST', body: { status: status } });
    const n = (data && typeof data.removed === 'number') ? data.removed : 0;
    const cur = findJob(state.currentJobId);
    if (!cur) state.currentJobId = '';
    toast('已删除 ' + n + ' 个任务', 'success');
    await loadJobs();
  } catch (e) {
    toast('删除失败: ' + e.message, 'error');
  }
}

async function selectJob(id) {
  state.currentJobId = id;
  const job = findJob(id);
  if (job) await fetchJobLogs(job);
  renderJobs();
}

/* ------------------------------------------------------------------ 页签切换 */
var TAB_TITLES = { instances: '实例', volumes: '引导卷', create: '创建实例', jobs: '任务' };
function switchTab(name) {
  state.activeTab = name;
  document.querySelectorAll('.tab').forEach(function (t) {
    t.classList.toggle('active', t.dataset.tab === name);
  });
  var titleEl = document.getElementById('topbarTitle');
  if (titleEl) titleEl.textContent = TAB_TITLES[name] || '';
  document.querySelectorAll('.panel').forEach(function (p) {
    p.classList.toggle('active', p.id === 'tab-' + name);
  });
  if (name === 'instances') loadInstances();
  else if (name === 'volumes') loadVolumes();
  else if (name === 'create') loadTemplates();
  else if (name === 'jobs') loadJobs();
}

function reloadCurrentTab() { switchTab(state.activeTab); }

// 强制刷新当前 tab（跳过缓存），由"刷新"按钮触发
function forceReloadCurrentTab() {
  if (state.activeTab === 'instances') loadInstances(true);
  else if (state.activeTab === 'volumes') loadVolumes(true);
  else if (state.activeTab === 'create') loadTemplates();
  else if (state.activeTab === 'jobs') loadJobs();
}

/* ------------------------------------------------------------------ 轮询 */
function startPolling() {
  if (state.timer) return;
  state.timer = setInterval(function () {
    const hasRunning = state.jobs.some(function (j) { return j.status === 'running'; });
    if (state.activeTab === 'jobs' || hasRunning) loadJobs();
  }, 3000);
}

/* ------------------------------------------------------------------ 事件绑定 */
function bindEvents() {
  document.querySelectorAll('.tab').forEach(function (t) {
    t.addEventListener('click', function () { switchTab(t.dataset.tab); });
  });

  document.getElementById('accountSelect').addEventListener('change', function (e) {
    const oldAccount = state.account;
    state.account = e.target.value;
    state.currentJobId = '';
    state.logBuffers = {};

    // 切换账号后立即清空旧账号的数据展示，并显示加载占位（避免误显示「暂无数据」）
    state.instances = [];
    state.volumes = [];
    setTableLoading('instancesTable', 5);
    setTableLoading('volumesTable', 6);
    document.getElementById('instancesHint').textContent = '';
    document.getElementById('volumesHint').textContent = '';

    // 标记旧账号缓存过期（下次切回时会强制刷新）
    if (oldAccount && state.loaded[oldAccount]) {
      state.loaded[oldAccount] = { instances: false, volumes: false };
    }

    reloadCurrentTab();
  });

  document.getElementById('refreshBtn').addEventListener('click', forceReloadCurrentTab);
  document.getElementById('themeBtn').addEventListener('click', toggleTheme);
  document.getElementById('modalClose').addEventListener('click', closeModal);
  document.getElementById('modal').addEventListener('click', function (e) {
    if (e.target.id === 'modal') closeModal();
  });

  // 登录表单
  document.getElementById('loginForm').addEventListener('submit', function (e) {
    e.preventDefault();
    const user = document.getElementById('loginUser').value.trim();
    const pass = document.getElementById('loginPass').value;
    if (!user || !pass) {
      const errEl = document.getElementById('loginError');
      errEl.textContent = '请输入用户名和密码';
      errEl.classList.remove('hidden');
      return;
    }
    setCred(user, pass);
    hideLogin();
    if (loginResolve) {
      loginResolve();
      loginResolve = null;
      loginReject = null;
    }
  });

  document.getElementById('exportIpsBtn').addEventListener('click', exportIps);
  document.getElementById('reloadVolumesBtn').addEventListener('click', function () { loadVolumes(true); });
  document.getElementById('reloadJobsBtn').addEventListener('click', loadJobs);
  document.getElementById('cancelJobBtn').addEventListener('click', cancelJob);
  document.getElementById('launchBtn').addEventListener('click', launch);
  document.getElementById('templateSelect').addEventListener('change', renderTemplatePreview);

  document.querySelectorAll('[data-bulk]').forEach(function (b) {
    b.addEventListener('click', function () { bulkAction(b.dataset.bulk); });
  });

  document.querySelector('#instancesTable tbody').addEventListener('click', function (e) {
    const btn = e.target.closest('button[data-act]');
    if (!btn) return;
    if (btn.dataset.act === 'detail') showInstanceDetail(btn.dataset.id);
    else instanceAction(btn.dataset.id, btn.dataset.act, btn.dataset.name);
  });

  document.querySelector('#volumesTable tbody').addEventListener('click', function (e) {
    const btn = e.target.closest('button[data-vact]');
    if (!btn) return;
    volumeAction(btn.dataset.vact, btn.dataset.id);
  });

  document.getElementById('modalBody').addEventListener('click', function (e) {
    const btn = e.target.closest('button[data-dact]');
    if (!btn) return;
    instanceDetailAction(btn.dataset.dact, btn.dataset.id);
  });

  document.getElementById('jobsList').addEventListener('click', function (e) {
    const delBtn = e.target.closest('button[data-del-job]');
    if (delBtn) {
      e.stopPropagation();
      deleteJob(delBtn.dataset.delJob);
      return;
    }
    const item = e.target.closest('.job-item');
    if (!item) return;
    selectJob(item.dataset.job);
  });

  document.getElementById('deleteJobsBtn').addEventListener('click', deleteJobsByStatus);

  // 通用对话框按钮
  document.getElementById('dialogOk').addEventListener('click', function () {
    const input = document.getElementById('dialogInput');
    const val = input.classList.contains('hidden') ? true : input.value.trim();
    closeDialog(val);
  });
  document.getElementById('dialogCancel').addEventListener('click', function () {
    closeDialog(document.getElementById('dialogInput').classList.contains('hidden') ? false : null);
  });
  document.getElementById('dialogModal').addEventListener('click', function (e) {
    if (e.target.id === 'dialogModal') {
      closeDialog(document.getElementById('dialogInput').classList.contains('hidden') ? false : null);
    }
  });
  document.getElementById('dialogInput').addEventListener('keydown', function (e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      document.getElementById('dialogOk').click();
    }
  });
}

/* ------------------------------------------------------------------ 启动 */
async function init() {
  bindEvents();
  loadVersion();   // 不阻塞，版本号尽快显示
  try {
    await loadAccounts();
  } catch (e) {
    toast('加载账号失败: ' + e.message, 'error');
    return;
  }
  if (!state.account) {
    toast('未配置任何甲骨文账号', 'error');
    return;
  }
  await loadInstances();
  startPolling();
}

document.addEventListener('DOMContentLoaded', init);
