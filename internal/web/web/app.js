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
  jobs: [],
  currentJobId: '',
  activeTab: 'instances',
  timer: null,
  logBuffers: {},      // jobId -> { lines: [], next: 0 }
  loaded: {},          // account -> { instances: bool, volumes: bool }  缓存标记
  tasks: [],
  taskTemplates: [],
  taskFormIsNew: true,   // 当前打开的是否为「新建抢机任务」（决定是否自动预填 SSH 公钥）
  vcns: [],
  netSub: 'security',
  subnets: [],
  nsgs: [],
  vnics: [],
  privateIps: [],
  security: { ingress: [], egress: [] },
  limitServices: [],
  trafficInstances: [],
  update: null,
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
let loginLayer = null; // 登录框所在的弹窗层（与其它弹窗同一个栈）

const LOGIN_HTML =
  '<div class="login-head">' +
    '<div class="login-logo">☁️</div>' +
    '<h3 class="login-title">甲骨文实例管理</h3>' +
    '<div class="login-subtitle">请登录以继续访问</div>' +
  '</div>' +
  '<form class="login-form">' +
    '<label class="login-field"><span class="login-label">用户名</span>' +
      '<input class="login-user" type="text" autocomplete="username" required></label>' +
    '<label class="login-field"><span class="login-label">密码</span>' +
      '<input class="login-pass" type="password" autocomplete="current-password" required></label>' +
    '<div class="login-error hidden"></div>' +
    '<button type="submit" class="btn primary login-btn">登 录</button>' +
  '</form>';

function setLoginError(msg) {
  if (!loginLayer) return;
  const errEl = loginLayer.box.querySelector('.login-error');
  if (msg) {
    errEl.textContent = msg;
    errEl.classList.remove('hidden');
  } else {
    errEl.textContent = '';
    errEl.classList.add('hidden');
  }
}

function showLogin(errMsg) {
  if (loginLayer) {
    setLoginError(errMsg);
    return;
  }
  const layer = buildModal({ raw: LOGIN_HTML, boxClass: 'login-box' });
  const formEl = layer.box.querySelector('.login-form');
  const userEl = layer.box.querySelector('.login-user');
  const passEl = layer.box.querySelector('.login-pass');
  formEl.addEventListener('submit', function (e) {
    e.preventDefault();
    const user = userEl.value.trim();
    const pass = passEl.value;
    if (!user || !pass) {
      setLoginError('请输入用户名和密码');
      return;
    }
    setCred(user, pass);
    hideLogin();
    if (loginResolve) {
      const r = loginResolve;
      loginResolve = null;
      loginReject = null;
      r();
    }
  });
  // 登录框不可点背景关闭（必须登录才能继续）
  pushLayer(layer, null, false);
  loginLayer = layer;
  setLoginError(errMsg);
  userEl.focus();
}

function hideLogin() {
  if (loginLayer) {
    popLayer(loginLayer);
    loginLayer = null;
  }
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
    await promptLogin(); // 等登录
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

// retryRangeText 把抢机任务的重试间隔区间格式化为列表展示文本。
function retryRangeText(t) {
  const min = t.retryMin || 0;
  const max = t.retryMax || 0;
  if (min > 0 && max > 0) return min + '~' + max;
  if (min > 0) return String(min);
  if (max > 0) return String(max);
  return '默认';
}

let toastTimer = null;
function toast(msg, type) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast' + (type ? ' ' + type : '');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(function () { el.classList.add('hidden'); }, 4000);
}

/* ================================================================== 弹窗栈
 * 所有弹窗（详情/表单、确认框、输入框、登录框）统一入栈管理：
 *   - 后开的在上层，z-index 按栈深度内联计算（基准 50，每层 +10），无需手工维护；
 *   - closeModal() 关闭栈顶；每层自带 onClose 回调，多级叠加互不干扰；
 *   - 点击遮罩（.modal 本身）关闭该层，登录框除外。
 */
const MODAL_Z_BASE = 50;
const MODAL_Z_STEP = 10;
const modalStack = [];

function modalZ(depth) { return MODAL_Z_BASE + depth * MODAL_Z_STEP; }

// buildModal 构造一层弹窗 DOM（不入栈）。opts:
//   title     标题
//   html      正文 HTML
//   foot      正文之后的页脚元素（可选）
//   boxClass  追加到 .modal-box 的类名
//   closable  是否显示右上角 ✕（默认显示）
//   raw       直接作为 .modal-box 的 innerHTML（登录框这类自定义结构用）
function buildModal(opts) {
  opts = opts || {};
  const root = document.createElement('div');
  root.className = 'modal';
  const box = document.createElement('div');
  box.className = 'modal-box' + (opts.boxClass ? ' ' + opts.boxClass : '');
  let bodyEl = box;
  let closeBtn = null;
  let titleEl = null;
  if (opts.raw) {
    box.innerHTML = opts.raw;
  } else {
    const head = document.createElement('div');
    head.className = 'modal-head';
    titleEl = document.createElement('h3');
    titleEl.textContent = opts.title || '';
    head.appendChild(titleEl);
    if (opts.closable !== false) {
      closeBtn = document.createElement('button');
      closeBtn.type = 'button';
      closeBtn.className = 'btn ghost';
      closeBtn.textContent = '✕';
      head.appendChild(closeBtn);
    }
    bodyEl = document.createElement('div');
    bodyEl.className = 'modal-body';
    if (typeof opts.html === 'string') bodyEl.innerHTML = opts.html;
    box.appendChild(head);
    box.appendChild(bodyEl);
    if (opts.foot) box.appendChild(opts.foot);
  }
  root.appendChild(box);
  return { root: root, box: box, bodyEl: bodyEl, titleEl: titleEl, closeBtn: closeBtn, onClose: null };
}

// relayoutModals 按当前栈深度重排 z-index（中间层被弹出后仍保持连续）
function relayoutModals() {
  modalStack.forEach(function (layer, i) { layer.root.style.zIndex = String(modalZ(i)); });
}

// pushLayer 入栈并置于栈顶；dismissible=false 时点遮罩不关闭（如登录框）。
// onClose(result) 在该层关闭时调用一次。
function pushLayer(layer, onClose, dismissible) {
  layer.onClose = onClose || null;
  layer.root.style.zIndex = String(modalZ(modalStack.length));
  if (dismissible !== false) {
    layer.root.addEventListener('click', function (e) {
      if (e.target === layer.root) popLayer(layer, undefined);
    });
  }
  document.getElementById('modalRoot').appendChild(layer.root);
  modalStack.push(layer);
  return layer;
}

// popLayer 弹出指定层（默认栈顶）并触发其 onClose(result)
function popLayer(layer, result) {
  const target = layer || modalStack[modalStack.length - 1];
  if (!target) return null;
  const idx = modalStack.indexOf(target);
  if (idx < 0) return null;
  modalStack.splice(idx, 1);
  if (target.root.parentNode) target.root.parentNode.removeChild(target.root);
  const cb = target.onClose;
  target.onClose = null;
  relayoutModals();
  if (cb) cb(result);
  return target;
}

// modalTop 返回栈顶弹窗层
function modalTop() { return modalStack[modalStack.length - 1] || null; }

// openModal 打开一个内容弹窗（详情/表单），返回该层便于调用方继续绑定。
function openModal(title, html, opts) {
  opts = opts || {};
  const layer = buildModal({ title: title, html: html, boxClass: opts.boxClass, closable: opts.closable });
  if (layer.closeBtn) layer.closeBtn.addEventListener('click', function () { popLayer(layer); });
  return pushLayer(layer);
}

// closeModal 关闭栈顶弹窗。
function closeModal() {
  const top = modalTop();
  if (top) popLayer(top);
}

// setModalContent 原地更新指定层（默认栈顶）的标题与正文。
// 用于「先占位（加载中）、拿到数据后再填充」的刷新场景，避免连开两层。
function setModalContent(title, html, layer) {
  const target = layer || modalTop();
  if (!target) return null;
  if (target.titleEl && title != null) target.titleEl.textContent = title;
  if (target.bodyEl && html != null) target.bodyEl.innerHTML = html;
  return target;
}

/* ---- 自定义确认框 / 输入框（替换浏览器原生 confirm / prompt，同栈叠加） ---- */

function dialogFoot(okText, danger) {
  const foot = document.createElement('div');
  foot.className = 'modal-foot';
  const cancel = document.createElement('button');
  cancel.type = 'button';
  cancel.className = 'btn';
  cancel.textContent = '取消';
  const ok = document.createElement('button');
  ok.type = 'button';
  ok.className = 'btn ' + (danger ? 'danger' : 'primary');
  ok.textContent = okText || '确定';
  foot.appendChild(cancel);
  foot.appendChild(ok);
  return { foot: foot, cancel: cancel, ok: ok };
}

// uiConfirm 返回 Promise<boolean>（取消/点遮罩为 false）
function uiConfirm(message, opts) {
  opts = opts || {};
  return new Promise(function (resolve) {
    const b = dialogFoot(opts.okText, opts.danger);
    const layer = buildModal({
      title: opts.title || '确认操作',
      html: '<div class="dialog-message"></div>',
      boxClass: 'dialog-box', foot: b.foot, closable: false
    });
    layer.bodyEl.querySelector('.dialog-message').textContent = message;
    b.cancel.addEventListener('click', function () { popLayer(layer, false); });
    b.ok.addEventListener('click', function () { popLayer(layer, true); });
    pushLayer(layer, function (r) { resolve(r === true); });
    b.ok.focus();
  });
}

// uiPrompt 返回 Promise<string|null>，取消返回 null
function uiPrompt(message, defaultValue, opts) {
  opts = opts || {};
  return new Promise(function (resolve) {
    const b = dialogFoot(opts.okText, false);
    const layer = buildModal({
      title: opts.title || '请输入',
      html: '<div class="dialog-message"></div><input class="dialog-input" type="text">',
      boxClass: 'dialog-box', foot: b.foot, closable: false
    });
    layer.bodyEl.querySelector('.dialog-message').textContent = message;
    const input = layer.bodyEl.querySelector('.dialog-input');
    input.value = defaultValue || '';
    b.cancel.addEventListener('click', function () { popLayer(layer, null); });
    b.ok.addEventListener('click', function () { popLayer(layer, input.value.trim()); });
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); b.ok.click(); }
    });
    pushLayer(layer, function (r) { resolve(r == null ? null : r); });
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

/* ------------------------------------------------------------------ 版本 */
async function loadVersion() {
  const el = document.getElementById('appVersion');
  if (!el) return;
  try {
    const data = await api('/api/version');
    VersionInfo = { version: data.version || '', commit: data.commit || '', buildTime: data.buildTime || '' };
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
  const layer = openModal('实例详情', '<div class="hint">加载中...</div>');
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
        '<button class="btn" data-dact="ipv6" data-id="' + esc(d.id) + '">附加 IPv6</button>' +
        '<button class="btn" data-dact="500m" data-id="' + esc(d.id) + '">开启 500Mbps</button>' +
        '<button class="btn" data-dact="500m-off" data-id="' + esc(d.id) + '">关闭 500Mbps</button>' +
        '<button class="btn danger" data-dact="rescue" data-id="' + esc(d.id) + '">自动救援/缩盘</button>' +
      '</div>';
    setModalContent('实例详情 - ' + (d.displayName || ''), html, layer);
  } catch (e) {
    setModalContent('实例详情', '<div class="hint">加载失败: ' + esc(e.message) + '</div>', layer);
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

/* ------------------------------------------------------------------ 任务 */
function findJob(id) {
  return state.jobs.filter(function (j) { return j.id === id; })[0];
}

function statusLabel(job) {
  if (job.status === 'running' && job.stopping) return '停止中';
  return { running: '运行中', success: '成功', partial: '部分成功', failed: '失败', canceled: '已取消' }[job.status] || job.status;
}

let jobsLoading = false;
async function loadJobs() {
  // 定时轮询与手动刷新可能同时触发，加锁避免重复请求与重复渲染
  if (jobsLoading) return;
  jobsLoading = true;
  try {
    const data = await api('/api/jobs');
    state.jobs = (data.jobs || []).sort(function (a, b) {
      return new Date(b.started) - new Date(a.started);
    });
    const job = state.currentJobId ? findJob(state.currentJobId) : null;
    if (job) await fetchJobLogs(job);
    renderJobs();
  } catch (e) { /* 忽略轮询错误 */ }
  jobsLoading = false;
}

// fetchJobLogs 只拉取 since 之后的增量日志，避免每次轮询都传输全部日志。
// 同一任务同时只允许一个请求在途：否则并发的两个请求会读到相同的 since，
// 各自把同一段日志追加一次，导致日志重复。
async function fetchJobLogs(job) {
  let buf = state.logBuffers[job.id];
  if (!buf) { buf = state.logBuffers[job.id] = { lines: [], next: 0, inflight: null }; }
  if (buf.inflight) return buf.inflight;   // 复用进行中的请求

  const since = buf.next;
  buf.inflight = (async function () {
    try {
      const data = await api('/api/job?id=' + encodeURIComponent(job.id) + '&since=' + since);
      const d = data.job || {};
      if (typeof d.logFrom === 'number' && d.logFrom > buf.next) {
        buf.lines = [];   // 服务端已裁剪旧日志，本地缓存作废
      }
      if (d.logs && d.logs.length) { buf.lines = buf.lines.concat(d.logs); }
      if (typeof d.logNext === 'number') { buf.next = d.logNext; }
      if (buf.lines.length > 4000) { buf.lines = buf.lines.slice(buf.lines.length - 4000); }
      buf.detail = d;
    } catch (e) { /* 忽略轮询错误 */ }
    buf.inflight = null;
    return buf;
  })();
  return buf.inflight;
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

// jobCategory 把任务分为「抢机任务」与「操作类」两类，用于日志分类筛选。
function jobCategory(j) { return j.type === 'task' ? 'task' : 'action'; }

// filteredJobs 按 #jobFilter 的当前选择过滤任务列表。
function filteredJobs() {
  const el = document.getElementById('jobFilter');
  const f = el ? el.value : '';
  if (!f) return state.jobs;
  return state.jobs.filter(function (j) { return jobCategory(j) === f; });
}

function renderJobs() {
  const running = state.jobs.filter(function (j) { return j.status === 'running'; });
  const badge = document.getElementById('jobBadge');
  if (badge) {
    if (running.length > 0) {
      badge.textContent = running.length;
      badge.classList.remove('hidden');
    } else {
      badge.classList.add('hidden');
    }
  }

  const jobs = filteredJobs();
  const list = document.getElementById('jobsList');
  if (jobs.length === 0) {
    list.innerHTML = '<div class="empty">' + (state.jobs.length === 0 ? '暂无任务' : '当前分类下暂无任务') + '</div>';
  } else {
    list.innerHTML = jobs.map(function (j) {
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
  // 抢机任务（type=task）由任务管理器控制，停止 = 暂停（可在任务列表「恢复」）
  const isTask = job.type === 'task';
  const msg = isTask
    ? '确定暂停抢机任务 ' + job.id + ' ？已创建的实例不会被删除，可在任务列表点「恢复」继续。'
    : '确定停止任务 ' + job.id + ' ？已创建的实例不会被删除。';
  if (!(await uiConfirm(msg, { danger: true, okText: isTask ? '暂停' : '停止' }))) return;
  try {
    const data = await api('/api/job/cancel', { method: 'POST', body: { id: job.id } });
    toast(data.message || '已请求停止任务', 'success');
    await loadJobs();
    if (isTask) await loadTasks();
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

// 按「分类 + 状态」批量删除日志（空状态 = 删除该分类下全部已结束任务，运行中的始终保留）
async function deleteJobsByStatus() {
  const sel = document.getElementById('jobDeleteStatus');
  const status = sel.value;
  const statusLabelText = sel.options[sel.selectedIndex].textContent;
  const fEl = document.getElementById('jobFilter');
  const type = fEl ? fEl.value : '';
  const typeLabel = type === 'task' ? '抢机任务' : (type === 'action' ? '操作类任务' : '全部任务');
  const count = filteredJobs().filter(function (j) {
    return j.status !== 'running' && (status === '' || j.status === status);
  }).length;
  const hint = '确定删除 分类「' + typeLabel + '」/ 状态「' + statusLabelText + '」 的日志？' +
    '共 ' + count + ' 条，运行中的任务会保留，且不影响已创建的实例。';
  if (!(await uiConfirm(hint, { danger: true, okText: '删除' }))) return;
  try {
    const data = await api('/api/jobs/delete', { method: 'POST', body: { status: status, type: type } });
    const n = (data && typeof data.removed === 'number') ? data.removed : 0;
    const cur = findJob(state.currentJobId);
    if (!cur) state.currentJobId = '';
    toast('已删除 ' + n + ' 条日志', 'success');
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
var TAB_TITLES = {
  instances: '实例', volumes: '引导卷',
  tasks: '抢机任务', network: '网络', traffic: '流量', limits: '配额',
  tenant: '租户', settings: '设置'
};
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
  else if (name === 'tasks') loadTasks();
  else if (name === 'network') loadNetwork();
  else if (name === 'traffic') loadTrafficTab();
  else if (name === 'limits') loadLimitsTab();
  else if (name === 'tenant') loadTenantTab();
  else if (name === 'settings') loadSettings();
}

function reloadCurrentTab() { switchTab(state.activeTab); }

// 强制刷新当前 tab（跳过缓存），由"刷新"按钮触发
function forceReloadCurrentTab() {
  if (state.activeTab === 'instances') loadInstances(true);
  else if (state.activeTab === 'volumes') loadVolumes(true);
  else if (state.activeTab === 'tasks') loadTasks();
  else if (state.activeTab === 'network') { state.vcns = []; loadNetwork(); }
  else if (state.activeTab === 'traffic') loadTrafficData();
  else if (state.activeTab === 'limits') loadLimits();
  else if (state.activeTab === 'tenant') loadTenantTab();
  else if (state.activeTab === 'settings') loadSettings();
}

/* ------------------------------------------------------------------ 轮询 */
function startPolling() {
  if (state.timer) return;
  state.timer = setInterval(function () {
    const hasRunning = state.jobs.some(function (j) { return j.status === 'running'; });
    if (state.activeTab === 'tasks' || hasRunning) loadJobs();
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

  document.getElementById('exportIpsBtn').addEventListener('click', exportIps);
  document.getElementById('reloadVolumesBtn').addEventListener('click', function () { loadVolumes(true); });
  document.getElementById('reloadJobsBtn').addEventListener('click', loadJobs);
  document.getElementById('cancelJobBtn').addEventListener('click', cancelJob);
  document.getElementById('jobFilter').addEventListener('change', function () { renderJobs(); });

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

  // 弹窗内容里的按钮统一在容器上做事件委托（各层都挂在 #modalRoot 下）
  document.getElementById('modalRoot').addEventListener('click', function (e) {
    const tbtn = e.target.closest('button[data-tact]');
    if (tbtn) { taskAction(tbtn.dataset.tact, tbtn.dataset.id); return; }
    const nbtn = e.target.closest('button[data-nact]');
    if (nbtn) { handleNewAction(nbtn.dataset.nact, nbtn.dataset.id); return; }
    const ubtn = e.target.closest('button[data-useract]');
    if (ubtn) { userModalAction(ubtn.dataset.useract, ubtn); return; }
    const btn = e.target.closest('button[data-dact]');
    if (!btn) return;
    const act = btn.dataset.dact;
    if (act === 'ipv6' || act === '500m' || act === '500m-off' || act === 'rescue') {
      instanceDetailAction2(act, btn.dataset.id);
    } else {
      instanceDetailAction(act, btn.dataset.id);
    }
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
}

/* ================================================================== 抢机任务 */

async function loadTasks() {
  setTableLoading('tasksTable', 9);
  try {
    const data = await api('/api/tasks');
    state.tasks = data.tasks || [];
    renderTasks();
  } catch (e) {
    setTableMessage('tasksTable', 9, '加载失败：' + e.message);
  }
  await loadJobs();
}

function renderTasks() {
  const tbody = document.querySelector('#tasksTable tbody');
  if (!state.tasks.length) {
    tbody.innerHTML = '<tr><td colspan="9" class="empty">暂无抢机任务</td></tr>';
    return;
  }
  tbody.innerHTML = state.tasks.map(function (t) {
    const status = t.paused
      ? '<span class="status-tag status-canceled">已暂停</span>'
      : (t.createNumbers > 0 ? '<span class="status-tag status-running">运行中</span>'
                             : '<span class="status-tag status-success">已完成</span>');
    const spec = t.architecture === 'ARM' ? (t.ocpus + 'C/' + t.memory + 'G') : '1C/1G 免费';
    return '<tr>' +
      '<td>' + esc(t.tenantName || t.tenantId) + '</td>' +
      '<td class="mono">' + esc(t.region) + '</td>' +
      '<td>' + esc(t.architecture) + '</td>' +
      '<td class="mono">' + spec + '</td>' +
      '<td class="mono">' + t.disk + '</td>' +
      '<td class="mono">' + t.createNumbers + '</td>' +
      '<td class="mono">' + esc(retryRangeText(t)) + '</td>' +
      '<td>' + status + '</td>' +
      '<td class="actions">' +
        (t.paused
          ? '<button class="btn small" data-tact="resume" data-id="' + esc(t.id) + '">恢复</button>'
          : '<button class="btn small" data-tact="pause" data-id="' + esc(t.id) + '">暂停</button>') +
        '<button class="btn small" data-tact="edit" data-id="' + esc(t.id) + '">编辑</button>' +
        '<button class="btn small danger" data-tact="delete" data-id="' + esc(t.id) + '">删除</button>' +
      '</td></tr>';
  }).join('');
}

// accountRegion 返回指定租户（账号）配置里的 region。
function accountRegion(tenantId) {
  const a = state.accounts.filter(function (x) { return x.id === tenantId; })[0];
  return a ? (a.region || '') : '';
}

// 各操作系统的默认版本（与后端 task.NormalizeOS 保持一致）
const OS_DEFAULT_VERSION = {
  'Canonical Ubuntu': '20.04',
  'CentOS': '8',
  'Oracle Linux': '8'
};

// osDefaultVersion 返回「系统版本」留空时后端会使用的默认版本。
function osDefaultVersion(os) {
  return OS_DEFAULT_VERSION[os] || '20.04';
}

function taskForm(task) {
  const t = task || {};
  const isNew = !task;
  // 新建时浏览器会默认选中第一项，区域跟随该账号
  const curTenantId = t.tenantId || (state.accounts[0] && state.accounts[0].id) || '';
  const accounts = state.accounts.map(function (a) {
    return '<option value="' + esc(a.id) + '"' + (curTenantId === a.id ? ' selected' : '') + '>' + esc(a.name) + '</option>';
  }).join('');
  const osList = ['Canonical Ubuntu', 'CentOS', 'Oracle Linux'];
  const osOpts = osList.map(function (o) {
    return '<option value="' + esc(o) + '"' + (t.operationSystem === o ? ' selected' : '') + '>' + esc(o) + '</option>';
  }).join('');
  return '<div class="form-grid">' +
    '<label class="span3">从实例模版导入<select id="taskTemplate">' +
      '<option value="">（加载中…）</option></select>' +
      '<span class="hint">模版来自 oci-help.ini 的 [INSTANCE.*] 段，选择后自动填充下方参数。</span></label>' +
    '<label>租户<select id="taskTenant">' + accounts + '</select></label>' +
    '<label>区域<input id="taskRegion" value="' + esc(t.region || accountRegion(curTenantId)) + '" placeholder="留空用租户默认"></label>' +
    '<label>架构<select id="taskArch">' +
      '<option value="AMD"' + (t.architecture !== 'ARM' ? ' selected' : '') + '>AMD (免费 E2.1.Micro)</option>' +
      '<option value="ARM"' + (t.architecture === 'ARM' ? ' selected' : '') + '>ARM (A1.Flex)</option></select></label>' +
    '<label>OCPU (ARM)<input id="taskOcpus" type="number" step="0.5" value="' + (t.ocpus || 1) + '"></label>' +
    '<label>内存 GB (ARM)<input id="taskMemory" type="number" step="0.5" value="' + (t.memory || 6) + '"></label>' +
    '<label>磁盘 GB<input id="taskDisk" type="number" value="' + (t.disk || 47) + '"></label>' +
    '<label>创建数量<input id="taskCount" type="number" value="' + (t.createNumbers || 1) + '"></label>' +
    '<label>重试间隔最小(秒)<input id="taskRetryMin" type="number" min="0" value="' + (isNew ? 99 : (t.retryMin || '')) + '" placeholder="留空=默认 1 秒"></label>' +
    '<label>重试间隔最大(秒)<input id="taskRetryMax" type="number" min="0" value="' + (isNew ? 150 : (t.retryMax || '')) + '" placeholder="留空=同最小"></label>' +
    '<label>操作系统<select id="taskOs">' + osOpts + '</select></label>' +
    '<label>系统版本<input id="taskOsVer" value="' + esc(t.osVersion || '') + '" placeholder="留空默认 ' + esc(osDefaultVersion(t.operationSystem || 'Canonical Ubuntu')) + '"></label>' +
    '<label>实例名称<input id="taskName" value="' + esc(t.instanceName || '') + '"></label>' +
    '<label>可用性域<input id="taskAd" value="' + esc(t.availabilityDomain || '') + '" placeholder="建议留空=自动轮换所有可用性域"></label>' +
    '<label>Root 密码<input id="taskPwd" value="' + esc(t.rootPassword || '') + '" placeholder="留空=无公钥时自动生成"></label>' +
    '<label class="span2">SSH 公钥<input id="taskSsh" value="' + esc(t.sshPublicKey || '') + '">' +
      '<span class="hint" id="taskSshHint"></span></label>' +
    '</div>' +
    '<div class="modal-actions">' +
      '<button class="btn" data-nact="close">取消</button>' +
      '<button class="btn primary" data-nact="saveTask" data-id="' + esc(t.id || '') + '">保存并启动</button>' +
    '</div>';
}

function openTaskForm(id) {
  const t = id ? state.tasks.filter(function (x) { return x.id === id; })[0] : null;
  if (state.accounts.length === 0) { toast('请先在「设置」页添加账号', 'error'); return; }
  state.taskTemplates = [];
  // 只有新建任务才按架构自动预填 SSH 公钥；编辑时以任务自身存的值（含"空=不用 SSH"）为准
  state.taskFormIsNew = !t;
  openModal(t ? '编辑抢机任务' : '新建抢机任务', taskForm(t));
  bindTaskForm();
}

/* ---- 实例模版导入 ---- */

// bindTaskForm 绑定模版下拉与租户切换（表单为动态生成，需在 openModal 后调用）。
function bindTaskForm() {
  const tmplSel = document.getElementById('taskTemplate');
  if (!tmplSel) return;
  const tenantSel = document.getElementById('taskTenant');
  if (tenantSel) {
    tenantSel.addEventListener('change', function () {
      // 区域跟随所选账号
      const regionEl = document.getElementById('taskRegion');
      if (regionEl) regionEl.value = accountRegion(tenantSel.value);
      state.taskTemplates = [];
      loadTaskTemplates();
    });
  }
  // 切换架构 → SSH 公钥跟着换成该架构在 oci-help.ini 里配的那份
  const archSel = document.getElementById('taskArch');
  if (archSel) archSel.addEventListener('change', function () { fillTaskSshFromIni(); });
  // 切换操作系统 → 「系统版本」的占位提示跟着变（留空时后端用的就是它）
  const osSel = document.getElementById('taskOs');
  if (osSel) {
    osSel.addEventListener('change', function () {
      const verEl = document.getElementById('taskOsVer');
      if (verEl) verEl.placeholder = '留空默认 ' + osDefaultVersion(osSel.value);
    });
  }
  const sshEl = document.getElementById('taskSsh');
  if (sshEl) sshEl.addEventListener('input', updateSshHint);
  const pwdEl = document.getElementById('taskPwd');
  if (pwdEl) pwdEl.addEventListener('input', updateSshHint);
  tmplSel.addEventListener('change', function () { applyTaskTemplate(tmplSel.value); });
  loadTaskTemplates();
}

// templateForArch 按架构挑出对应的 INI 模版段：优先按 shape 匹配，其次按段名含 AMD/ARM。
function templateForArch(arch) {
  const list = state.taskTemplates || [];
  const wantArm = String(arch).toUpperCase() === 'ARM';
  const wantShape = wantArm ? 'A1.FLEX' : 'E2.1.MICRO';
  let byShape = null;
  let byName = null;
  list.forEach(function (t) {
    const shape = String(t.shape || '').toUpperCase();
    const name = String(t.name || '').toUpperCase();
    if (!byShape && shape.indexOf(wantShape) >= 0) byShape = t;
    if (byName) return;
    if (wantArm ? name.indexOf('ARM') >= 0 : name.indexOf('AMD') >= 0) byName = t;
  });
  return byShape || byName || null;
}

// fillTaskSshFromIni 按当前架构把 oci-help.ini 模版里的 ssh_authorized_key 预填到表单。
// 只在用户没动过这个框时回填（清空后即视为"本次不使用 SSH"，不再自动填回）。
function fillTaskSshFromIni() {
  const sshEl = document.getElementById('taskSsh');
  if (!sshEl) return;
  const archEl = document.getElementById('taskArch');
  const tpl = templateForArch(archEl ? archEl.value : 'AMD');
  const iniKey = tpl ? String(tpl.sshPublicKey || '').trim() : '';
  const auto = sshEl.dataset.autoSsh || '';
  if (sshEl.value === '' || sshEl.value === auto) {
    sshEl.value = iniKey;
    sshEl.dataset.autoSsh = iniKey;
  }
  updateSshHint();
}

// updateSshHint 说明当前会怎么登录（公钥 / 密码 / 自动生成密码）。
function updateSshHint() {
  const sshEl = document.getElementById('taskSsh');
  const hintEl = document.getElementById('taskSshHint');
  if (!sshEl || !hintEl) return;
  const archEl = document.getElementById('taskArch');
  const tpl = templateForArch(archEl ? archEl.value : 'AMD');
  const pwdEl = document.getElementById('taskPwd');
  const hasKey = sshEl.value.trim() !== '';
  const hasPwd = !!(pwdEl && pwdEl.value.trim() !== '');
  if (hasKey) {
    const from = tpl && String(tpl.sshPublicKey || '').trim() === sshEl.value.trim()
      ? '（已从 oci-help.ini 的 [' + tpl.name + '] 自动填入）'
      : '';
    hintEl.textContent = '使用 SSH 公钥登录' + from + '；清空此框则本次不使用 SSH 公钥。';
  } else if (hasPwd) {
    hintEl.textContent = '不使用 SSH 公钥，将用上面填写的 Root 密码登录。';
  } else {
    hintEl.textContent = '不使用 SSH 公钥、也未填 Root 密码 → 保存时会自动生成随机 root 密码。';
  }
}

// loadTaskTemplates 按当前租户读取 oci-help.ini 中的实例模版列表。
async function loadTaskTemplates() {
  const sel = document.getElementById('taskTemplate');
  if (!sel) return;
  const tenantId = document.getElementById('taskTenant').value;
  const acct = state.accounts.filter(function (a) { return a.id === tenantId; })[0];
  if (!acct) {
    sel.innerHTML = '<option value="">（无可用模版）</option>';
    return;
  }
  try {
    const d = await api('/api/templates?account=' + encodeURIComponent(acct.name));
    state.taskTemplates = d.templates || [];
  } catch (e) {
    state.taskTemplates = [];
    sel.innerHTML = '<option value="">（读取模版失败：' + esc(e.message) + '）</option>';
    updateSshHint();
    return;
  }
  if (!state.taskTemplates.length) {
    sel.innerHTML = '<option value="">（该账号无实例模版）</option>';
    updateSshHint();
    return;
  }
  sel.innerHTML = '<option value="">（不导入，手动填写）</option>' +
    state.taskTemplates.map(function (t) {
      return '<option value="' + esc(t.name) + '">' + esc(t.name) + '</option>';
    }).join('');
  // 模版到手后再按架构预填 SSH 公钥（仅新建任务；编辑时以任务自身的值为准）
  if (state.taskFormIsNew) fillTaskSshFromIni();
  else updateSshHint();
}

// applyTaskTemplate 把模版参数填充到表单（不覆盖模版未提供的项）。
function applyTaskTemplate(name) {
  if (!name) return;
  const tpl = (state.taskTemplates || []).filter(function (x) { return x.name === name; })[0];
  if (!tpl) return;
  const shape = (tpl.shape || '').toUpperCase();
  const isArm = shape.indexOf('A1') >= 0 || shape.indexOf('ARM') >= 0;
  setInput('taskArch', isArm ? 'ARM' : 'AMD');
  if (isArm) {
    if (tpl.ocpus > 0) setInput('taskOcpus', tpl.ocpus);
    if (tpl.memoryInGBs > 0) setInput('taskMemory', tpl.memoryInGBs);
  }
  if (tpl.bootVolumeSizeInGBs > 0) setInput('taskDisk', tpl.bootVolumeSizeInGBs);
  const count = tpl.sum > 0 ? tpl.sum : tpl.each;
  if (count > 0) setInput('taskCount', count);
  if (tpl.availabilityDomain) setInput('taskAd', tpl.availabilityDomain);
  if (tpl.operatingSystem) setSelectValue('taskOs', tpl.operatingSystem);
  if (tpl.operatingSystemVersion) setInput('taskOsVer', tpl.operatingSystemVersion);
  // 模版里的 SSH 公钥一并导入（记为"自动填"，切架构时仍可被替换）
  const sshEl = document.getElementById('taskSsh');
  if (sshEl && tpl.sshPublicKey) {
    sshEl.value = String(tpl.sshPublicKey).trim();
    sshEl.dataset.autoSsh = sshEl.value;
  }
  updateSshHint();
  toast('已导入模版「' + name + '」', 'success');
}

function setInput(id, v) {
  const el = document.getElementById(id);
  if (el) el.value = v;
}

// setSelectValue 设置 select 的值；模版里的名称与选项不完全一致时做一次模糊匹配。
function setSelectValue(id, v) {
  const el = document.getElementById(id);
  if (!el) return;
  const opts = Array.prototype.slice.call(el.options);
  if (opts.some(function (o) { return o.value === v; })) { el.value = v; return; }
  const lower = String(v).toLowerCase();
  const hit = opts.filter(function (o) {
    const key = o.value.toLowerCase().split(' ')[0];
    return key && lower.indexOf(key) >= 0;
  })[0];
  if (hit) el.value = hit.value;
}

async function saveTask(id) {
  const arch = document.getElementById('taskArch').value;
  const body = {
    id: id || undefined,
    tenantId: document.getElementById('taskTenant').value,
    region: document.getElementById('taskRegion').value.trim(),
    architecture: arch,
    ocpus: Number(document.getElementById('taskOcpus').value) || 1,
    memory: Number(document.getElementById('taskMemory').value) || 1,
    disk: Number(document.getElementById('taskDisk').value) || 47,
    createNumbers: Number(document.getElementById('taskCount').value) || 1,
    retryMin: Number(document.getElementById('taskRetryMin').value) || 0,
    retryMax: Number(document.getElementById('taskRetryMax').value) || 0,
    operationSystem: document.getElementById('taskOs').value,
    osVersion: document.getElementById('taskOsVer').value.trim(),
    instanceName: document.getElementById('taskName').value.trim(),
    availabilityDomain: document.getElementById('taskAd').value.trim(),
    rootPassword: document.getElementById('taskPwd').value,
    sshPublicKey: document.getElementById('taskSsh').value.trim()
  };
  try {
    const data = await api('/api/task/save', { method: 'POST', body: body });
    closeModal();
    await loadTasks();
    if (data && data.generatedRootPassword && data.task && data.task.rootPassword) {
      // 自动生成的密码必须让用户看到，否则等于没设
      openModal('已自动生成 Root 密码', '<div class="kv">' +
        '<div class="k">Root 密码</div><div class="mono">' + esc(data.task.rootPassword) + '</div>' +
        '</div><div class="hint">未填写 SSH 公钥、也未填 Root 密码，已自动生成该密码：' +
        '抢到实例后会随通知（Telegram）一起发送，也可在任务列表点「编辑」查看。</div>');
    } else {
      toast('抢机任务已保存并开始执行', 'success');
    }
  } catch (e) {
    toast('保存失败: ' + e.message, 'error');
  }
}

async function taskAction(action, id) {
  if (action === 'edit') { openTaskForm(id); return; }
  if (action === 'delete') {
    if (!(await uiConfirm('确定删除该抢机任务？已创建的实例不会被删除。', { danger: true, okText: '删除' }))) return;
  }
  if (action === 'pause' && !(await uiConfirm('确定暂停该抢机任务？'))) return;
  const path = { pause: '/api/task/pause', resume: '/api/task/resume', delete: '/api/task/delete' }[action];
  if (!path) return;
  try {
    const data = await api(path, { method: 'POST', body: { id: id } });
    toast(data.message || '操作成功', 'success');
    await loadTasks();
    // 暂停/恢复/删除都会影响任务日志列表（Job 注册表），一并刷新
    await loadJobs();
  } catch (e) {
    toast('操作失败: ' + e.message, 'error');
  }
}

/* ================================================================== 网络 */

async function loadNetwork() {
  const sel = document.getElementById('vcnSelect');
  if (!state.vcns.length) {
    try {
      const d = await api('/api/vcns?account=' + encodeURIComponent(state.account));
      state.vcns = d.vcns || [];
    } catch (e) {
      toast('加载 VCN 失败: ' + e.message, 'error');
      return;
    }
    sel.innerHTML = state.vcns.length
      ? state.vcns.map(function (v) {
          return '<option value="' + esc(v.id) + '">' + esc(v.displayName) + (v.ipv6Enabled ? ' [IPv6]' : '') + '</option>';
        }).join('')
      : '<option value="">（无 VCN）</option>';
  }
  await loadNetSub();
}

// loadNetSub 按当前子页加载对应数据。
async function loadNetSub() {
  if (state.netSub === 'security') return loadSecurityRules();
  if (state.netSub === 'subnet') return loadSubnets();
  if (state.netSub === 'nsg') return loadNsgs();
  if (state.netSub === 'vnic') return loadVnicTab();
}

function switchNetSub(name) {
  state.netSub = name;
  document.querySelectorAll('.subnav-btn').forEach(function (b) {
    b.classList.toggle('active', b.dataset.net === name);
  });
  document.querySelectorAll('.net-sub').forEach(function (p) {
    p.classList.toggle('active', p.id === 'net-' + name);
  });
  loadNetSub();
}

function currentVcn() {
  return document.getElementById('vcnSelect').value;
}

async function loadSecurityRules() {
  const vcnId = document.getElementById('vcnSelect').value;
  if (!vcnId) { setTableMessage('ingressTable', 5, '暂无 VCN'); setTableMessage('egressTable', 5, '暂无 VCN'); return; }
  setTableLoading('ingressTable', 5);
  setTableLoading('egressTable', 5);
  document.getElementById('networkHint').textContent = '';
  try {
    const d = await api('/api/security/rules?account=' + encodeURIComponent(state.account) + '&vcnId=' + encodeURIComponent(vcnId));
    state.security = { ingress: d.ingress || [], egress: d.egress || [] };
    renderSecurity();
  } catch (e) {
    setTableMessage('ingressTable', 5, '加载失败：' + e.message);
    setTableMessage('egressTable', 5, '加载失败：' + e.message);
  }
}

function portText(r) {
  if (r.protocol === 'all' || r.protocol === 'icmp') return '全部';
  if (r.portMin || r.portMax) return r.portMin + ' - ' + r.portMax;
  return '全部';
}

function renderSecurity() {
  const ing = document.querySelector('#ingressTable tbody');
  const egr = document.querySelector('#egressTable tbody');
  ing.innerHTML = state.security.ingress.length ? state.security.ingress.map(function (r, i) {
    return '<tr><td class="col-check"><input type="checkbox" data-rule="ingress" value="' + i + '"></td>' +
      '<td>' + esc(r.protocol) + '</td><td class="mono">' + esc(r.source) + '</td>' +
      '<td class="mono">' + portText(r) + '</td><td>' + esc(r.description) + '</td></tr>';
  }).join('') : '<tr><td colspan="5" class="empty">暂无入站规则</td></tr>';
  egr.innerHTML = state.security.egress.length ? state.security.egress.map(function (r, i) {
    return '<tr><td class="col-check"><input type="checkbox" data-rule="egress" value="' + i + '"></td>' +
      '<td>' + esc(r.protocol) + '</td><td class="mono">' + esc(r.destination) + '</td>' +
      '<td class="mono">' + portText(r) + '</td><td>' + esc(r.description) + '</td></tr>';
  }).join('') : '<tr><td colspan="5" class="empty">暂无出站规则</td></tr>';
}

async function releasePorts() {
  const vcnId = document.getElementById('vcnSelect').value;
  if (!vcnId) return;
  if (!(await uiConfirm('确定放行该 VCN 安全列表的所有端口与协议？', { danger: true, okText: '放行' }))) return;
  try {
    const d = await api('/api/security/release', { method: 'POST', body: { account: state.account, vcnId: vcnId } });
    toast(d.message || '已放行', 'success');
    await loadSecurityRules();
  } catch (e) {
    toast('放行失败: ' + e.message, 'error');
  }
}

function openRuleForm() {
  openModal('新增安全规则', '<div class="form-grid">' +
    '<label>方向<select id="ruleDir"><option value="ingress">入站</option><option value="egress">出站</option></select></label>' +
    '<label>协议<select id="ruleProto"><option value="all">全部</option><option value="tcp">TCP</option><option value="udp">UDP</option><option value="icmp">ICMP</option></select></label>' +
    '<label>来源 CIDR (入站)<input id="ruleSrc" value="0.0.0.0/0"></label>' +
    '<label>目标 CIDR (出站)<input id="ruleDst" value="0.0.0.0/0"></label>' +
    '<label>起始端口<input id="rulePortMin" type="number" placeholder="TCP/UDP 有效"></label>' +
    '<label>结束端口<input id="rulePortMax" type="number" placeholder="TCP/UDP 有效"></label>' +
    '<label class="span2">描述<input id="ruleDesc"></label>' +
    '</div><div class="modal-actions">' +
    '<button class="btn" data-nact="close">取消</button>' +
    '<button class="btn primary" data-nact="saveRule">添加</button></div>');
}

async function saveRule() {
  const vcnId = document.getElementById('vcnSelect').value;
  const body = {
    account: state.account, vcnId: vcnId,
    direction: document.getElementById('ruleDir').value,
    protocol: document.getElementById('ruleProto').value,
    source: document.getElementById('ruleSrc').value.trim(),
    destination: document.getElementById('ruleDst').value.trim(),
    portMin: Number(document.getElementById('rulePortMin').value) || 0,
    portMax: Number(document.getElementById('rulePortMax').value) || 0,
    description: document.getElementById('ruleDesc').value.trim()
  };
  try {
    await api('/api/security/rule/add', { method: 'POST', body: body });
    closeModal();
    toast('规则已添加', 'success');
    await loadSecurityRules();
  } catch (e) {
    toast('添加失败: ' + e.message, 'error');
  }
}

async function removeRules() {
  const vcnId = document.getElementById('vcnSelect').value;
  const ingress = [], egress = [];
  document.querySelectorAll('input[data-rule="ingress"]:checked').forEach(function (c) { ingress.push(Number(c.value)); });
  document.querySelectorAll('input[data-rule="egress"]:checked').forEach(function (c) { egress.push(Number(c.value)); });
  if (!ingress.length && !egress.length) { toast('请先勾选要删除的规则', 'error'); return; }
  if (!(await uiConfirm('确定删除勾选的 ' + (ingress.length + egress.length) + ' 条规则？', { danger: true, okText: '删除' }))) return;
  try {
    await api('/api/security/rule/remove', { method: 'POST', body: { account: state.account, vcnId: vcnId, ingress: ingress, egress: egress } });
    toast('规则已删除', 'success');
    await loadSecurityRules();
  } catch (e) {
    toast('删除失败: ' + e.message, 'error');
  }
}

/* ================================================================== 子网管理 */

async function loadSubnets() {
  const vcnId = currentVcn();
  if (!vcnId) { setTableMessage('subnetsTable', 7, '暂无 VCN'); return; }
  setTableLoading('subnetsTable', 7);
  try {
    const d = await api('/api/subnets?account=' + encodeURIComponent(state.account) + '&vcnId=' + encodeURIComponent(vcnId));
    state.subnets = d.subnets || [];
    renderSubnets();
  } catch (e) {
    setTableMessage('subnetsTable', 7, '加载失败：' + e.message);
  }
}

function renderSubnets() {
  const tbody = document.querySelector('#subnetsTable tbody');
  tbody.innerHTML = state.subnets.length ? state.subnets.map(function (sn) {
    const v6 = (sn.ipv6CidrBlocks || []).join(', ') || '-';
    return '<tr><td>' + esc(sn.displayName) + '</td>' +
      '<td class="mono">' + esc(sn.cidrBlock) + '</td>' +
      '<td class="mono">' + esc(v6) + '</td>' +
      '<td>' + esc(sn.lifecycleState) + '</td>' +
      '<td class="mono">' + esc(sn.availabilityDomain || '-') + '</td>' +
      '<td>' + (sn.prohibitInternetIngress ? '是' : '否') + '</td>' +
      '<td class="actions">' +
        '<button class="btn small" data-subact="edit" data-id="' + esc(sn.id) + '">编辑</button>' +
        '<button class="btn small danger" data-subact="delete" data-id="' + esc(sn.id) + '">删除</button>' +
      '</td></tr>';
  }).join('') : '<tr><td colspan="7" class="empty">暂无子网</td></tr>';
}

async function openSubnetForm(id) {
  const vcnId = currentVcn();
  if (!vcnId) { toast('请先选择 VCN', 'error'); return; }
  const sn = id ? state.subnets.filter(function (x) { return x.id === id; })[0] : null;
  let routeTables = [], securityLists = [];
  try {
    const rt = await api('/api/route-tables?account=' + encodeURIComponent(state.account) + '&vcnId=' + encodeURIComponent(vcnId));
    routeTables = rt.routeTables || [];
  } catch (e) { /* 忽略 */ }
  try {
    const sl = await api('/api/security-lists?account=' + encodeURIComponent(state.account) + '&vcnId=' + encodeURIComponent(vcnId));
    securityLists = sl.securityLists || [];
  } catch (e) { /* 忽略 */ }

  const rtOpts = '<option value="">（保持不变）</option>' + routeTables.map(function (x) {
    return '<option value="' + esc(x.id) + '"' + (sn && sn.routeTableId === x.id ? ' selected' : '') + '>' + esc(x.name || x.id) + '</option>';
  }).join('');
  const slOpts = securityLists.map(function (x) {
    const sel = sn && (sn.securityListIds || []).indexOf(x.id) >= 0;
    return '<option value="' + esc(x.id) + '"' + (sel ? ' selected' : '') + '>' + esc(x.name || x.id) + '</option>';
  }).join('');

  openModal(sn ? '编辑子网' : '新建子网', '<div class="form-grid">' +
    '<label>名称<input id="snName" value="' + esc(sn ? sn.displayName : '') + '"></label>' +
    (sn ? '' :
      '<label>CIDR<input id="snCidr" placeholder="10.0.1.0/24"></label>' +
      '<label>DNS 标签 (可选)<input id="snDns" placeholder="subnetdns"></label>' +
      '<label>可用性域 (可选)<input id="snAd"></label>' +
      '<label>禁止公网入站<select id="snProhibit"><option value="false">否</option><option value="true">是</option></select></label>') +
    '<label>路由表<select id="snRt">' + rtOpts + '</select></label>' +
    '<label class="span2">安全列表 (可多选，Ctrl/⌘ 多选)<select id="snSl" multiple size="4">' + slOpts + '</select></label>' +
    '</div><div class="modal-actions">' +
    '<button class="btn" data-nact="close">取消</button>' +
    '<button class="btn primary" data-nact="saveSubnet" data-id="' + esc(sn ? sn.id : '') + '">保存</button></div>');
}

async function saveSubnet(id) {
  const vcnId = currentVcn();
  const slSel = document.getElementById('snSl');
  const body = {
    account: state.account,
    vcnId: vcnId,
    subnetId: id || undefined,
    displayName: document.getElementById('snName').value.trim(),
    routeTableId: document.getElementById('snRt').value
  };
  if (slSel && slSel.options.length > 0) {
    body.securityListIds = Array.prototype.slice.call(slSel.selectedOptions).map(function (o) { return o.value; });
  }
  if (!id) {
    body.cidrBlock = document.getElementById('snCidr').value.trim();
    body.dnsLabel = document.getElementById('snDns').value.trim();
    body.availabilityDomain = document.getElementById('snAd').value.trim();
    body.prohibitInternetIngress = document.getElementById('snProhibit').value === 'true';
  }
  try {
    await api(id ? '/api/subnet/update' : '/api/subnet/create', { method: 'POST', body: body });
    closeModal();
    toast('子网已保存', 'success');
    await loadSubnets();
  } catch (e) {
    toast('保存失败: ' + e.message, 'error');
  }
}

async function subnetAction(action, id) {
  if (action === 'edit') { openSubnetForm(id); return; }
  if (action === 'delete') {
    if (!(await uiConfirm('确定删除该子网？子网内必须已无 VNIC 等资源，否则会失败。', { danger: true, okText: '删除' }))) return;
    try {
      const d = await api('/api/subnet/delete', { method: 'POST', body: { account: state.account, subnetId: id } });
      toast('已提交删除，任务: ' + d.jobId, 'success');
    } catch (e) { toast('删除失败: ' + e.message, 'error'); }
  }
}

/* ================================================================== 网络安全组 (NSG) */

async function loadNsgs() {
  const vcnId = currentVcn();
  const sel = document.getElementById('nsgSelect');
  if (!vcnId) { sel.innerHTML = ''; setTableMessage('nsgRulesTable', 6, '暂无 VCN'); return; }
  try {
    const d = await api('/api/nsgs?account=' + encodeURIComponent(state.account) + '&vcnId=' + encodeURIComponent(vcnId));
    state.nsgs = d.nsgs || [];
    sel.innerHTML = state.nsgs.length
      ? state.nsgs.map(function (g) { return '<option value="' + esc(g.id) + '">' + esc(g.displayName) + '</option>'; }).join('')
      : '<option value="">（无 NSG）</option>';
  } catch (e) {
    toast('加载 NSG 失败: ' + e.message, 'error');
    return;
  }
  await loadNsgRules();
}

async function loadNsgRules() {
  const nsgId = document.getElementById('nsgSelect').value;
  if (!nsgId) {
    setTableMessage('nsgRulesTable', 6, '暂无 NSG');
    document.getElementById('nsgVnics').textContent = '—';
    return;
  }
  setTableLoading('nsgRulesTable', 6);
  try {
    const d = await api('/api/nsg/rules?account=' + encodeURIComponent(state.account) + '&nsgId=' + encodeURIComponent(nsgId));
    renderNsgRules(d.rules || []);
  } catch (e) {
    setTableMessage('nsgRulesTable', 6, '加载失败：' + e.message);
  }
  try {
    const v = await api('/api/nsg/vnics?account=' + encodeURIComponent(state.account) + '&nsgId=' + encodeURIComponent(nsgId));
    const ids = v.vnicIds || [];
    document.getElementById('nsgVnics').textContent = ids.length ? ids.join(', ') : '（未绑定任何 VNIC）';
  } catch (e) {
    document.getElementById('nsgVnics').textContent = '获取失败：' + e.message;
  }
}

function renderNsgRules(rules) {
  const tbody = document.querySelector('#nsgRulesTable tbody');
  tbody.innerHTML = rules.length ? rules.map(function (r) {
    const peer = r.direction === 'egress' ? r.destination : r.source;
    const port = (r.protocol === 'all' || r.protocol === 'icmp') ? '全部'
      : ((r.portMin || r.portMax) ? (r.portMin + ' - ' + r.portMax) : '全部');
    return '<tr><td class="col-check"><input type="checkbox" data-nsgrule value="' + esc(r.id) + '"></td>' +
      '<td>' + esc(r.direction) + '</td><td>' + esc(r.protocol) + '</td>' +
      '<td class="mono">' + esc(peer) + '</td><td class="mono">' + port + '</td><td>' + esc(r.description) + '</td></tr>';
  }).join('') : '<tr><td colspan="6" class="empty">暂无规则</td></tr>';
}

function openNsgForm() {
  const vcnId = currentVcn();
  if (!vcnId) { toast('请先选择 VCN', 'error'); return; }
  openModal('新建网络安全组', '<div class="form-grid">' +
    '<label class="span2">名称<input id="nsgName" placeholder="nsg-web"></label>' +
    '</div><div class="modal-actions">' +
    '<button class="btn" data-nact="close">取消</button>' +
    '<button class="btn primary" data-nact="saveNsg">创建</button></div>');
}

async function saveNsg() {
  const body = { account: state.account, vcnId: currentVcn(), name: document.getElementById('nsgName').value.trim() };
  if (!body.name) { toast('请输入名称', 'error'); return; }
  try {
    await api('/api/nsg/create', { method: 'POST', body: body });
    closeModal();
    toast('NSG 已创建', 'success');
    await loadNsgs();
  } catch (e) { toast('创建失败: ' + e.message, 'error'); }
}

async function deleteNsg() {
  const nsgId = document.getElementById('nsgSelect').value;
  if (!nsgId) { toast('请先选择 NSG', 'error'); return; }
  if (!(await uiConfirm('确定删除该网络安全组？需先解除其与 VNIC 的绑定。', { danger: true, okText: '删除' }))) return;
  try {
    await api('/api/nsg/delete', { method: 'POST', body: { account: state.account, nsgId: nsgId } });
    toast('NSG 已删除', 'success');
    await loadNsgs();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

function openNsgRuleForm() {
  const nsgId = document.getElementById('nsgSelect').value;
  if (!nsgId) { toast('请先选择 NSG', 'error'); return; }
  openModal('新增 NSG 规则', '<div class="form-grid">' +
    '<label>方向<select id="nrDir"><option value="ingress">入站</option><option value="egress">出站</option></select></label>' +
    '<label>协议<select id="nrProto"><option value="all">全部</option><option value="tcp">TCP</option><option value="udp">UDP</option><option value="icmp">ICMP</option></select></label>' +
    '<label>来源 CIDR (入站)<input id="nrSrc" value="0.0.0.0/0"></label>' +
    '<label>目标 CIDR (出站)<input id="nrDst" value="0.0.0.0/0"></label>' +
    '<label>起始端口<input id="nrPortMin" type="number" placeholder="TCP/UDP 有效"></label>' +
    '<label>结束端口<input id="nrPortMax" type="number" placeholder="TCP/UDP 有效"></label>' +
    '<label class="span2">描述<input id="nrDesc"></label>' +
    '</div><div class="modal-actions">' +
    '<button class="btn" data-nact="close">取消</button>' +
    '<button class="btn primary" data-nact="saveNsgRule">添加</button></div>');
}

async function saveNsgRule() {
  const nsgId = document.getElementById('nsgSelect').value;
  const rule = {
    direction: document.getElementById('nrDir').value,
    protocol: document.getElementById('nrProto').value,
    source: document.getElementById('nrSrc').value.trim(),
    destination: document.getElementById('nrDst').value.trim(),
    portMin: Number(document.getElementById('nrPortMin').value) || 0,
    portMax: Number(document.getElementById('nrPortMax').value) || 0,
    description: document.getElementById('nrDesc').value.trim()
  };
  try {
    await api('/api/nsg/rule/add', { method: 'POST', body: { account: state.account, nsgId: nsgId, rule: rule } });
    closeModal();
    toast('规则已添加', 'success');
    await loadNsgRules();
  } catch (e) { toast('添加失败: ' + e.message, 'error'); }
}

async function removeNsgRules() {
  const nsgId = document.getElementById('nsgSelect').value;
  const ids = [];
  document.querySelectorAll('input[data-nsgrule]:checked').forEach(function (c) { ids.push(c.value); });
  if (!ids.length) { toast('请先勾选要删除的规则', 'error'); return; }
  if (!(await uiConfirm('确定删除勾选的 ' + ids.length + ' 条规则？', { danger: true, okText: '删除' }))) return;
  try {
    await api('/api/nsg/rule/remove', { method: 'POST', body: { account: state.account, nsgId: nsgId, ruleIds: ids } });
    toast('规则已删除', 'success');
    await loadNsgRules();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

/* ================================================================== VNIC 管理 */

async function loadVnicTab() {
  const sel = document.getElementById('vnicInstance');
  try {
    const d = await api('/api/instances?account=' + encodeURIComponent(state.account));
    state.trafficInstances = d.instances || [];
  } catch (e) { toast('加载实例失败: ' + e.message, 'error'); return; }
  sel.innerHTML = state.trafficInstances.length
    ? state.trafficInstances.map(function (i) {
        return '<option value="' + esc(i.id) + '">' + esc(i.displayName) + '</option>';
      }).join('')
    : '<option value="">（无实例）</option>';
  await loadVnics();
}

async function loadVnics() {
  const instanceId = document.getElementById('vnicInstance').value;
  if (!instanceId) { setTableMessage('vnicsTable', 7, '暂无实例'); return; }
  setTableLoading('vnicsTable', 7);
  document.getElementById('privateIpsBox').innerHTML = '<div class="hint">点击上方 VNIC 的「私有 IP」查看</div>';
  try {
    const d = await api('/api/vnics?account=' + encodeURIComponent(state.account) + '&instanceId=' + encodeURIComponent(instanceId));
    state.vnics = d.vnics || [];
    renderVnics();
  } catch (e) {
    setTableMessage('vnicsTable', 7, '加载失败：' + e.message);
  }
}

function renderVnics() {
  const tbody = document.querySelector('#vnicsTable tbody');
  tbody.innerHTML = state.vnics.length ? state.vnics.map(function (v) {
    return '<tr><td>' + esc(v.displayName) + '</td>' +
      '<td>' + (v.isPrimary ? '<span class="status-tag status-running">主</span>' : '辅助') + '</td>' +
      '<td class="mono">' + esc(v.privateIp) + '</td>' +
      '<td class="mono">' + esc(v.publicIp || '-') + '</td>' +
      '<td class="mono">' + esc(v.macAddress) + '</td>' +
      '<td class="mono">' + ((v.nsgIds || []).length) + '</td>' +
      '<td class="actions">' +
        '<button class="btn small" data-vnicact="ips" data-id="' + esc(v.id) + '">私有 IP</button>' +
        '<button class="btn small" data-vnicact="edit" data-id="' + esc(v.id) + '">编辑</button>' +
        (v.isPrimary ? '' :
          '<button class="btn small danger" data-vnicact="detach" data-id="' + esc(v.id) + '" data-att="' + esc(v.attachmentId) + '">分离</button>') +
      '</td></tr>';
  }).join('') : '<tr><td colspan="7" class="empty">暂无 VNIC</td></tr>';
}

async function openAttachVnicForm() {
  const instanceId = document.getElementById('vnicInstance').value;
  if (!instanceId) { toast('请先选择实例', 'error'); return; }
  const vcnId = currentVcn();
  if (!vcnId) { toast('请先在上方选择 VCN（用于挑选子网）', 'error'); return; }
  let subnets = [], nsgs = [];
  try {
    const s = await api('/api/subnets?account=' + encodeURIComponent(state.account) + '&vcnId=' + encodeURIComponent(vcnId));
    subnets = s.subnets || [];
  } catch (e) { /* 忽略 */ }
  try {
    const n = await api('/api/nsgs?account=' + encodeURIComponent(state.account) + '&vcnId=' + encodeURIComponent(vcnId));
    nsgs = n.nsgs || [];
  } catch (e) { /* 忽略 */ }
  if (!subnets.length) { toast('该 VCN 下没有可用子网', 'error'); return; }

  openModal('附加辅助 VNIC', '<div class="form-grid">' +
    '<label class="span2">子网<select id="vnicSubnet">' +
      subnets.map(function (s) {
        return '<option value="' + esc(s.id) + '">' + esc(s.displayName) + ' (' + esc(s.cidrBlock) + ')</option>';
      }).join('') + '</select></label>' +
    '<label>名称<input id="vnicName" placeholder="secondary-vnic"></label>' +
    '<label>主机名 (可选)<input id="vnicHost"></label>' +
    '<label>指定私网 IP (可选)<input id="vnicPriv" placeholder="10.0.1.50"></label>' +
    '<label>分配公网 IP<select id="vnicPub"><option value="false">否</option><option value="true">是</option></select></label>' +
    '<label class="span2">NSG (可多选)<select id="vnicNsg" multiple size="4">' +
      nsgs.map(function (g) { return '<option value="' + esc(g.id) + '">' + esc(g.displayName) + '</option>'; }).join('') +
    '</select></label>' +
    '</div><div class="modal-actions">' +
    '<button class="btn" data-nact="close">取消</button>' +
    '<button class="btn primary" data-nact="saveAttachVnic">附加</button></div>');
}

async function saveAttachVnic() {
  const instanceId = document.getElementById('vnicInstance').value;
  const nsgSel = document.getElementById('vnicNsg');
  const body = {
    account: state.account,
    instanceId: instanceId,
    subnetId: document.getElementById('vnicSubnet').value,
    displayName: document.getElementById('vnicName').value.trim(),
    hostnameLabel: document.getElementById('vnicHost').value.trim(),
    privateIp: document.getElementById('vnicPriv').value.trim(),
    assignPublicIp: document.getElementById('vnicPub').value === 'true',
    nsgIds: nsgSel ? Array.prototype.slice.call(nsgSel.selectedOptions).map(function (o) { return o.value; }) : []
  };
  try {
    const d = await api('/api/vnic/attach', { method: 'POST', body: body });
    closeModal();
    toast('已提交附加，任务: ' + d.jobId, 'success');
  } catch (e) { toast('附加失败: ' + e.message, 'error'); }
}

function openVnicEditForm(vnicId) {
  const v = state.vnics.filter(function (x) { return x.id === vnicId; })[0];
  if (!v) return;
  openModal('编辑 VNIC - ' + v.displayName, '<div class="form-grid">' +
    '<label>名称<input id="veName" value="' + esc(v.displayName) + '"></label>' +
    '<label>主机名<input id="veHost" value="' + esc(v.hostnameLabel || '') + '"></label>' +
    '<label>路由表 ID (可选)<input id="veRt" value="' + esc(v.routeTableId || '') + '" placeholder="ocid1.routetable..."></label>' +
    '<label>跳过源/目的检查<select id="veSkip">' +
      '<option value="">保持</option><option value="true"' + (v.skipSourceDestCheck ? ' selected' : '') + '>启用</option>' +
      '<option value="false"' + (!v.skipSourceDestCheck ? ' selected' : '') + '>禁用</option></select></label>' +
    '<label class="span2">NSG ID 列表 (逗号分隔，留空表示不改)<input id="veNsg" value="' + esc((v.nsgIds || []).join(',')) + '"></label>' +
    '</div><div class="modal-actions">' +
    '<button class="btn" data-nact="close">取消</button>' +
    '<button class="btn primary" data-nact="saveVnicEdit" data-id="' + esc(v.id) + '">保存</button></div>');
}

async function saveVnicEdit(vnicId) {
  const nsgRaw = document.getElementById('veNsg').value.trim();
  const skipRaw = document.getElementById('veSkip').value;
  const body = {
    account: state.account,
    vnicId: vnicId,
    displayName: document.getElementById('veName').value.trim(),
    hostnameLabel: document.getElementById('veHost').value.trim(),
    routeTableId: document.getElementById('veRt').value.trim()
  };
  if (nsgRaw !== '') {
    body.nsgIds = nsgRaw.split(',').map(function (x) { return x.trim(); }).filter(function (x) { return x; });
  }
  if (skipRaw !== '') body.skipSourceDestCheck = (skipRaw === 'true');
  try {
    await api('/api/vnic/update', { method: 'POST', body: body });
    closeModal();
    toast('VNIC 已更新', 'success');
    await loadVnics();
  } catch (e) { toast('更新失败: ' + e.message, 'error'); }
}

async function detachVnic(vnicId, attachmentId) {
  if (!attachmentId) { toast('缺少挂载 ID', 'error'); return; }
  if (!(await uiConfirm('确定分离该辅助 VNIC？分离会删除该 VNIC 及其公网 IP 关联。', { danger: true, okText: '分离' }))) return;
  try {
    const d = await api('/api/vnic/detach', { method: 'POST', body: { account: state.account, attachmentId: attachmentId } });
    toast('已提交分离，任务: ' + d.jobId, 'success');
  } catch (e) { toast('分离失败: ' + e.message, 'error'); }
}

async function loadPrivateIps(vnicId) {
  currentVnicIdValue = vnicId;
  const box = document.getElementById('privateIpsBox');
  box.innerHTML = '<div class="hint">加载中...</div>';
  try {
    const d = await api('/api/vnic/private-ips?account=' + encodeURIComponent(state.account) + '&vnicId=' + encodeURIComponent(vnicId));
    renderPrivateIps(vnicId, d.privateIps || []);
  } catch (e) {
    box.innerHTML = '<div class="hint">加载失败：' + esc(e.message) + '</div>';
  }
}

function renderPrivateIps(vnicId, ips) {
  const box = document.getElementById('privateIpsBox');
  const toolbar = '<div class="toolbar">' +
    '<button class="btn small primary" data-ipact="add" data-vnic="' + esc(vnicId) + '">新增辅助私有 IP</button></div>';
  if (!ips.length) { box.innerHTML = toolbar + '<div class="hint">暂无私有 IP</div>'; return; }
  const rows = ips.map(function (ip) {
    return '<tr><td class="mono">' + esc(ip.ipAddress) + '</td>' +
      '<td>' + (ip.isPrimary ? '主' : '辅助') + '</td>' +
      '<td class="mono">' + esc(ip.hostnameLabel || '-') + '</td>' +
      '<td class="mono">' + esc(ip.publicIp || '-') + '</td>' +
      '<td class="actions">' +
        (ip.publicIp
          ? '<button class="btn small danger" data-ipact="release" data-pub="' + esc(ip.publicIpId) + '">释放公网IP</button>'
          : '<button class="btn small" data-ipact="reserve" data-id="' + esc(ip.id) + '">保留公网IP</button>') +
        (ip.isPrimary ? '' :
          '<button class="btn small danger" data-ipact="delip" data-id="' + esc(ip.id) + '">删除</button>') +
      '</td></tr>';
  }).join('');
  box.innerHTML = toolbar + '<div class="table-wrap"><table><thead><tr>' +
    '<th>私有 IP</th><th>类型</th><th>主机名</th><th>公网 IP</th><th class="col-actions">操作</th>' +
    '</tr></thead><tbody>' + rows + '</tbody></table></div>';
}

async function privateIpAction(action, btn) {
  if (action === 'add') {
    const vnicId = btn.dataset.vnic;
    const ip = await uiPrompt('请输入要分配的私有 IP（留空自动分配）：', '', { title: '新增辅助私有 IP' });
    if (ip === null) return;
    try {
      await api('/api/vnic/private-ip/add', { method: 'POST', body: { account: state.account, vnicId: vnicId, ipAddress: ip.trim() } });
      toast('私有 IP 已新增', 'success');
      await loadPrivateIps(vnicId);
    } catch (e) { toast('新增失败: ' + e.message, 'error'); }
    return;
  }
  if (action === 'delip') {
    if (!(await uiConfirm('确定删除该辅助私有 IP？', { danger: true, okText: '删除' }))) return;
    try {
      await api('/api/vnic/private-ip/delete', { method: 'POST', body: { account: state.account, privateIpId: btn.dataset.id } });
      toast('私有 IP 已删除', 'success');
      await loadVnics();
      await loadPrivateIps(currentVnicId());
    } catch (e) { toast('删除失败: ' + e.message, 'error'); }
    return;
  }
  if (action === 'reserve') {
    try {
      const d = await api('/api/vnic/public-ip/reserve', { method: 'POST', body: { account: state.account, privateIpId: btn.dataset.id } });
      toast(d.message || '已保留公网 IP', 'success');
      await loadPrivateIps(currentVnicId());
    } catch (e) { toast('保留失败: ' + e.message, 'error'); }
    return;
  }
  if (action === 'release') {
    if (!(await uiConfirm('确定释放该公网 IP？', { danger: true, okText: '释放' }))) return;
    try {
      await api('/api/vnic/public-ip/release', { method: 'POST', body: { account: state.account, publicIpId: btn.dataset.pub } });
      toast('公网 IP 已释放', 'success');
      await loadPrivateIps(currentVnicId());
    } catch (e) { toast('释放失败: ' + e.message, 'error'); }
  }
}

// currentVnicId 记录当前展开私有 IP 的 VNIC。
var currentVnicIdValue = '';
function currentVnicId() { return currentVnicIdValue; }

/* ================================================================== 流量 */

async function loadTrafficTab() {
  const instSel = document.getElementById('trafficInstance');
  try {
    const d = await api('/api/instances?account=' + encodeURIComponent(state.account));
    state.trafficInstances = d.instances || [];
  } catch (e) { toast('加载实例失败: ' + e.message, 'error'); return; }
  instSel.innerHTML = state.trafficInstances.length
    ? state.trafficInstances.map(function (i) {
        return '<option value="' + esc(i.id) + '">' + esc(i.displayName) + '</option>';
      }).join('')
    : '<option value="">（无实例）</option>';
  await loadTrafficVnics();
}

async function loadTrafficVnics() {
  const instanceId = document.getElementById('trafficInstance').value;
  const vnicSel = document.getElementById('trafficVnic');
  if (!instanceId) { vnicSel.innerHTML = ''; return; }
  try {
    const d = await api('/api/traffic/vnics?account=' + encodeURIComponent(state.account) + '&instanceId=' + encodeURIComponent(instanceId));
    const vnics = d.vnics || [];
    vnicSel.innerHTML = vnics.length
      ? vnics.map(function (v) { return '<option value="' + esc(v.id) + '">' + esc(v.displayName || v.privateIp || v.id) + '</option>'; }).join('')
      : '<option value="">（无 VNIC）</option>';
  } catch (e) { toast('加载 VNIC 失败: ' + e.message, 'error'); }
  await loadTrafficData();
}

async function loadTrafficData() {
  const vnicId = document.getElementById('trafficVnic').value;
  const summary = document.getElementById('trafficSummary');
  const chart = document.getElementById('trafficChart');
  if (!vnicId) { summary.innerHTML = '<div class="hint">请选择实例与 VNIC</div>'; chart.innerHTML = ''; return; }
  const minutes = document.getElementById('trafficRange').value;
  summary.innerHTML = '<div class="hint">加载中...</div>';
  try {
    const d = await api('/api/traffic?account=' + encodeURIComponent(state.account) +
      '&vnicId=' + encodeURIComponent(vnicId) + '&minutes=' + encodeURIComponent(minutes));
    summary.innerHTML =
      '<div class="k">本月入站</div><div class="mono">' + esc(d.monthInText || '0 B') + '</div>' +
      '<div class="k">本月出站</div><div class="mono">' + esc(d.monthOutText || '0 B') + '</div>' +
      '<div class="k">采样点数</div><div class="mono">' + ((d.traffic && d.traffic.times) ? d.traffic.times.length : 0) + '</div>';
    renderTrafficChart(d.traffic);
  } catch (e) {
    summary.innerHTML = '<div class="hint">加载失败：' + esc(e.message) + '</div>';
    chart.innerHTML = '';
  }
}

function fmtBytes(v) {
  if (!v) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(2) + ' ' + u[i];
}

function renderTrafficChart(res) {
  const box = document.getElementById('trafficChart');
  const times = (res && res.times) || [];
  if (!times.length) { box.innerHTML = '<div class="hint">暂无数据（实例无流量或监控数据未上报）</div>'; return; }
  const W = 900, H = 220, P = 24;
  const all = res.inbound.concat(res.outbound);
  const max = Math.max.apply(null, all.concat([1]));
  function pts(arr) {
    return arr.map(function (v, i) {
      const x = P + (W - 2 * P) * (times.length === 1 ? 0.5 : i / (times.length - 1));
      const y = H - P - (H - 2 * P) * (v / max);
      return x.toFixed(1) + ',' + y.toFixed(1);
    }).join(' ');
  }
  box.innerHTML = '<svg viewBox="0 0 ' + W + ' ' + H + '" class="traffic-svg" preserveAspectRatio="none">' +
    '<polyline fill="none" stroke="var(--primary)" stroke-width="2" points="' + pts(res.inbound) + '"/>' +
    '<polyline fill="none" stroke="var(--danger)" stroke-width="2" points="' + pts(res.outbound) + '"/>' +
    '</svg>' +
    '<div class="traffic-legend"><span class="lg-in">■ 入站</span><span class="lg-out">■ 出站</span>' +
    '<span class="hint">峰值 ' + fmtBytes(max) + '</span></div>';
}

/* ================================================================== 配额 */

async function loadLimitsTab() {
  const sel = document.getElementById('limitService');
  if (!state.limitServices.length) {
    try {
      const d = await api('/api/limits/services?account=' + encodeURIComponent(state.account));
      state.limitServices = d.services || [];
    } catch (e) { toast('加载服务列表失败: ' + e.message, 'error'); return; }
    sel.innerHTML = '<option value="">全部服务</option>' +
      state.limitServices.map(function (s) { return '<option value="' + esc(s) + '">' + esc(s) + '</option>'; }).join('');
  }
  await loadLimits();
}

async function loadLimits() {
  const service = document.getElementById('limitService').value;
  setTableLoading('limitsTable', 7);
  document.getElementById('limitsHint').textContent = '';
  try {
    const d = await api('/api/limits?account=' + encodeURIComponent(state.account) + '&service=' + encodeURIComponent(service));
    const items = d.limits || [];
    const tbody = document.querySelector('#limitsTable tbody');
    tbody.innerHTML = items.length ? items.map(function (x) {
      const avail = x.hasAvailability ? x.available : '-';
      const used = x.hasAvailability ? x.used : '-';
      return '<tr><td>' + esc(x.serviceName) + '</td><td>' + esc(x.limitName) + '</td>' +
        '<td>' + esc(x.scopeType) + '</td><td class="mono">' + esc(x.availabilityDomain) + '</td>' +
        '<td class="mono">' + x.serviceLimit + '</td><td class="mono">' + used + '</td><td class="mono">' + avail + '</td></tr>';
    }).join('') : '<tr><td colspan="7" class="empty">暂无配额数据</td></tr>';
    document.getElementById('limitsHint').textContent = '共 ' + items.length + ' 条';
  } catch (e) {
    setTableMessage('limitsTable', 7, '加载失败：' + e.message);
  }
}

/* ================================================================== 租户 */

async function loadTenantTab() {
  await loadTenantInfo();
}

async function loadTenantConfigs() {
  setTableLoading('tenantCfgTable', 5);
  try {
    await refreshAccounts();
    renderTenantConfigs();
  } catch (e) {
    setTableMessage('tenantCfgTable', 5, '加载失败：' + e.message);
  }
}

function renderTenantConfigs() {
  const tbody = document.querySelector('#tenantCfgTable tbody');
  tbody.innerHTML = state.accounts.length ? state.accounts.map(function (a) {
    return '<tr><td>' + esc(a.name) + '</td><td class="mono">' + esc(a.region) + '</td>' +
      '<td class="mono">' + esc((a.tenancy || '').slice(-12)) + '</td>' +
      '<td class="mono">' + esc((a.user || '').slice(-12)) + '</td>' +
      '<td class="actions">' +
        '<button class="btn small" data-accact="edit" data-id="' + esc(a.id) + '">编辑</button>' +
        '<button class="btn small danger" data-accact="delete" data-id="' + esc(a.id) + '">删除</button>' +
      '</td></tr>';
  }).join('') : '<tr><td colspan="5" class="empty">暂无账号，点击「新增账号」添加</td></tr>';
}

async function refreshAccounts() {
  const prev = state.account;
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
  if (state.accounts.some(function (a) { return a.name === prev; })) state.account = prev;
  else if (state.accounts.length) state.account = state.accounts[0].name;
  else state.account = '';
  sel.value = state.account;
}

function openAccountForm(id) {
  const a = id ? state.accounts.filter(function (x) { return x.id === id; })[0] : null;
  const t = a || {};
  openModal(a ? '编辑账号' : '新增账号', '<div class="form-grid">' +
    '<label>账号名称<input id="accName" value="' + esc(t.name || '') + '"></label>' +
    '<label>区域 (region)<input id="accRegion" value="' + esc(t.region || '') + '" placeholder="ap-singapore-1"></label>' +
    '<label class="span2">User OCID<input id="accUser" value="' + esc(t.user || '') + '"></label>' +
    '<label class="span2">Tenancy OCID<input id="accTenancy" value="' + esc(t.tenancy || '') + '"></label>' +
    '<label class="span2">Fingerprint<input id="accFp" value="' + esc(t.fingerprint || '') + '"></label>' +
    '<label>私钥文件路径<input id="accKeyFile" value="' + esc(t.keyFile || '') + '" placeholder="留空则用下方粘贴内容生成"></label>' +
    '<label>私钥密码 (可选)<input id="accKeyPwd" type="password"></label>' +
    '<label class="span2">代理 (可选)<input id="accProxy" value="' + esc(t.proxy || '') + '" placeholder="socks5://127.0.0.1:7890"></label>' +
    '<label class="span2">粘贴私钥内容 (可选)<textarea id="accKeyContent" rows="4" placeholder="-----BEGIN PRIVATE KEY-----"></textarea></label>' +
    '</div><div class="modal-actions">' +
    '<button class="btn" data-nact="close">取消</button>' +
    '<button class="btn primary" data-nact="saveAccount" data-id="' + esc(t.id || '') + '">保存</button></div>');
}

async function saveAccount(id) {
  const body = {
    id: id || undefined,
    name: document.getElementById('accName').value.trim(),
    region: document.getElementById('accRegion').value.trim(),
    user: document.getElementById('accUser').value.trim(),
    tenancy: document.getElementById('accTenancy').value.trim(),
    fingerprint: document.getElementById('accFp').value.trim(),
    keyFile: document.getElementById('accKeyFile').value.trim(),
    keyPassword: document.getElementById('accKeyPwd').value,
    proxy: document.getElementById('accProxy').value.trim(),
    keyContent: document.getElementById('accKeyContent').value
  };
  try {
    await api('/api/tenants/save', { method: 'POST', body: body });
    closeModal();
    toast('账号已保存', 'success');
    await loadTenantConfigs();
  } catch (e) {
    toast('保存失败: ' + e.message, 'error');
  }
}

async function accountAction(action, id) {
  if (action === 'edit') { openAccountForm(id); return; }
  if (action === 'delete') {
    if (!(await uiConfirm('确定删除该账号？其抢机任务也会一并删除。', { danger: true, okText: '删除' }))) return;
    try {
      await api('/api/tenants/delete', { method: 'POST', body: { id: id } });
      toast('账号已删除', 'success');
      await loadTenantConfigs();
    } catch (e) { toast('删除失败: ' + e.message, 'error'); }
  }
}

async function loadTenantInfo() {
  const box = document.getElementById('tenantInfoBox');
  const tbody = document.querySelector('#tenantUsersTable tbody');
  if (!state.account) { box.innerHTML = '<div class="hint">请先在「设置」页添加账号</div>'; tbody.innerHTML = ''; return; }
  box.innerHTML = '<div class="hint">加载中...</div>';
  setTableLoading('tenantUsersTable', 6);
  try {
    const d = await api('/api/tenant?account=' + encodeURIComponent(state.account));
    const t = d.tenant || {};
    box.innerHTML =
      '<div class="k">租户名称</div><div>' + esc(t.name) + '</div>' +
      '<div class="k">套餐类型</div><div>' + esc(t.planType || '-') + '</div>' +
      '<div class="k">主区域</div><div class="mono">' + esc(t.homeRegion) + '</div>' +
      '<div class="k">已订阅区域</div><div class="mono">' + esc((t.regions || []).join(', ')) + '</div>';
    const users = t.users || [];
    tbody.innerHTML = users.length ? users.map(function (u) {
      return '<tr><td>' + esc(u.name) + '</td><td>' + esc(u.email) + '</td>' +
        '<td>' + esc(u.lifecycleState) + '</td><td>' + (u.isMfaActivated ? '已启用' : '未启用') + '</td>' +
        '<td class="mono">' + esc(u.timeCreated) + '</td>' +
        '<td class="actions">' +
          '<button class="btn small" data-useract="apikeys" data-id="' + esc(u.id) + '" data-name="' + esc(u.name) + '">API密钥</button>' +
          '<button class="btn small" data-useract="mfa" data-id="' + esc(u.id) + '" data-name="' + esc(u.name) + '">MFA</button>' +
          '<button class="btn small" data-useract="resetpwd" data-id="' + esc(u.id) + '" data-name="' + esc(u.name) + '">重置密码</button>' +
          '<button class="btn small" data-useract="edit" data-id="' + esc(u.id) + '" data-name="' + esc(u.name) + '">编辑</button>' +
          '<button class="btn small danger" data-useract="delete" data-id="' + esc(u.id) + '" data-name="' + esc(u.name) + '">删除</button>' +
        '</td></tr>';
    }).join('') : '<tr><td colspan="6" class="empty">暂无用户</td></tr>';
  } catch (e) {
    box.innerHTML = '<div class="hint">加载失败：' + esc(e.message) + '</div>';
    setTableMessage('tenantUsersTable', 6, '加载失败：' + e.message);
  }
}

async function userAction(action, userId, name) {
  if (action === 'delete') {
    if (!(await uiConfirm('确定删除用户 ' + name + ' ？该操作不可恢复。', { danger: true, okText: '删除' }))) return;
    try {
      await api('/api/tenant/user/delete', { method: 'POST', body: { account: state.account, userId: userId } });
      toast('用户已删除', 'success');
      await loadTenantInfo();
    } catch (e) { toast('删除失败: ' + e.message, 'error'); }
    return;
  }
  if (action === 'resetpwd') {
    if (!(await uiConfirm('确定重置用户 ' + name + ' 的控制台密码？', { okText: '重置' }))) return;
    try {
      const d = await api('/api/tenant/reset-password', { method: 'POST', body: { account: state.account, userId: userId } });
      openModal('新密码 - ' + name, '<div class="kv"><div class="k">新密码</div><div class="mono">' +
        esc(d.password) + '</div></div><div class="hint">请立即复制保存，关闭弹窗后无法再次查看。</div>');
    } catch (e) { toast('重置失败: ' + e.message, 'error'); }
    return;
  }
  if (action === 'edit') {
    const email = await uiPrompt('请输入新的邮箱（留空则不改）：', '', { title: '编辑用户 - ' + name });
    if (email === null) return;
    try {
      await api('/api/tenant/user/update', { method: 'POST', body: { account: state.account, userId: userId, email: email } });
      toast('用户信息已更新', 'success');
      await loadTenantInfo();
    } catch (e) { toast('更新失败: ' + e.message, 'error'); }
    return;
  }
  const path = action === 'apikeys' ? '/api/tenant/apikeys' : '/api/tenant/mfa';
  try {
    const d = await api(path + '?account=' + encodeURIComponent(state.account) + '&userId=' + encodeURIComponent(userId));
    if (action === 'apikeys') {
      const keys = d.apiKeys || [];
      openModal('API 密钥 - ' + name, '<div class="table-wrap"><table><thead><tr><th>指纹</th><th>状态</th><th>创建时间</th><th class="col-actions">操作</th></tr></thead><tbody>' +
        (keys.length ? keys.map(function (k) {
          return '<tr><td class="mono">' + esc(k.fingerprint) + '</td><td>' + esc(k.lifecycleState) + '</td>' +
            '<td class="mono">' + esc(k.timeCreated) + '</td>' +
            '<td class="actions"><button class="btn small danger" data-useract="delkey" data-id="' + esc(userId) + '" data-fp="' + esc(k.fingerprint) + '">删除</button></td></tr>';
        }).join('') : '<tr><td colspan="4" class="empty">暂无 API 密钥</td></tr>') + '</tbody></table></div>');
    } else {
      const devices = d.mfaDevices || [];
      openModal('MFA 设备 - ' + name, '<div class="table-wrap"><table><thead><tr><th>设备 ID</th><th>状态</th><th>创建时间</th><th class="col-actions">操作</th></tr></thead><tbody>' +
        (devices.length ? devices.map(function (m) {
          return '<tr><td class="mono">' + esc(m.id) + '</td><td>' + (m.isActivated ? '已激活' : '未激活') + '</td>' +
            '<td class="mono">' + esc(m.timeCreated) + '</td>' +
            '<td class="actions"><button class="btn small danger" data-useract="delmfa" data-id="' + esc(userId) + '" data-dev="' + esc(m.id) + '">删除</button></td></tr>';
        }).join('') : '<tr><td colspan="4" class="empty">暂无 MFA 设备</td></tr>') + '</tbody></table></div>');
    }
  } catch (e) { toast('加载失败: ' + e.message, 'error'); }
}

async function deleteApiKey(userId, fingerprint) {
  if (!(await uiConfirm('确定删除该 API 密钥？', { danger: true, okText: '删除' }))) return;
  try {
    await api('/api/tenant/apikey/delete', { method: 'POST', body: { account: state.account, userId: userId, fingerprint: fingerprint } });
    toast('API 密钥已删除', 'success');
    closeModal();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

async function deleteMfa(userId, deviceId) {
  if (!(await uiConfirm('确定删除该 MFA 设备？', { danger: true, okText: '删除' }))) return;
  try {
    await api('/api/tenant/mfa/delete', { method: 'POST', body: { account: state.account, userId: userId, deviceId: deviceId } });
    toast('MFA 设备已删除', 'success');
    closeModal();
  } catch (e) { toast('删除失败: ' + e.message, 'error'); }
}

/* ================================================================== 设置 / 更新 */

async function loadSettings() {
  await loadTenantConfigs();
  const box = document.getElementById('settingsVersion');
  box.innerHTML = '<div class="k">当前版本</div><div>' + esc(VersionInfo.version || 'dev') + '</div>' +
    '<div class="k">Commit</div><div class="mono">' + esc((VersionInfo.commit || '').slice(0, 12)) + '</div>' +
    '<div class="k">构建时间</div><div class="mono">' + esc(VersionInfo.buildTime || '') + '</div>' +
    '<div class="k">最新版本</div><div>' + esc((state.update && state.update.latest) || '未检查') + '</div>';
}

var VersionInfo = {};

async function checkUpdate() {
  const btn = document.getElementById('checkUpdateBtn');
  btn.disabled = true;
  try {
    const d = await api('/api/update/check');
    state.update = d.update || {};
    await loadSettings();
    const applyBtn = document.getElementById('applyUpdateBtn');
    applyBtn.disabled = !state.update.hasUpdate;
    toast(state.update.hasUpdate ? ('发现新版本 ' + state.update.latest) : '当前已是最新版本',
      state.update.hasUpdate ? 'success' : '');
  } catch (e) {
    toast('检查更新失败: ' + e.message, 'error');
  }
  btn.disabled = false;
}

async function applyUpdate() {
  if (!(await uiConfirm('确定更新到 ' + (state.update && state.update.latest) + ' ？服务会自动重启。', { okText: '更新' }))) return;
  try {
    const d = await api('/api/update/apply', { method: 'POST', body: {} });
    toast(d.message || '更新中，服务即将重启', 'success');
  } catch (e) {
    toast('更新失败: ' + e.message, 'error');
  }
}

/* ================================================================== 实例详情扩展操作 */

async function instanceDetailAction2(action, id) {
  try {
    if (action === 'ipv6') {
      if (!(await uiConfirm('确定为实例附加 IPv6 地址？'))) return;
      const d = await api('/api/ipv6/attach', { method: 'POST', body: { account: state.account, id: id } });
      toast('已提交，任务: ' + d.jobId, 'success');
    } else if (action === '500m') {
      if (!(await uiConfirm('确定开启免费 AMD 实例下行 500Mbps？将创建 NAT 网关与网络负载均衡器。'))) return;
      const d = await api('/api/instance/500m', { method: 'POST', body: { account: state.account, id: id, sshPort: 22 } });
      toast('已提交，任务: ' + d.jobId, 'success');
    } else if (action === '500m-off') {
      if (!(await uiConfirm('确定关闭下行 500Mbps？将删除 NAT 网关与网络负载均衡器。', { danger: true }))) return;
      const d = await api('/api/instance/500m/disable', { method: 'POST', body: { account: state.account, id: id } });
      toast('已提交，任务: ' + d.jobId, 'success');
    } else if (action === 'rescue') {
      const disk = await uiPrompt('请输入新的引导卷大小 (GB)：', '47', { title: '自动救援 / 缩小硬盘' });
      if (disk === null) return;
      if (!(await uiConfirm('自动救援会重建引导卷（原卷会先备份），确定继续？', { danger: true, okText: '继续' }))) return;
      const d = await api('/api/instance/rescue', { method: 'POST', body: { account: state.account, id: id, diskGB: Number(disk) || 47 } });
      toast('已提交，任务: ' + d.jobId, 'success');
    }
    closeModal();
    state.currentJobId = '';
    switchTab('tasks');
  } catch (e) {
    toast('操作失败: ' + e.message, 'error');
  }
}

function handleNewAction(name, id) {
  if (name === 'close') { closeModal(); return; }
  if (name === 'saveTask') { saveTask(id); return; }
  if (name === 'saveRule') { saveRule(); return; }
  if (name === 'saveAccount') { saveAccount(id); return; }
  if (name === 'saveSubnet') { saveSubnet(id); return; }
  if (name === 'saveNsg') { saveNsg(); return; }
  if (name === 'saveNsgRule') { saveNsgRule(); return; }
  if (name === 'saveAttachVnic') { saveAttachVnic(); return; }
  if (name === 'saveVnicEdit') { saveVnicEdit(id); return; }
}

function userModalAction(name, btn) {
  if (name === 'delkey') { deleteApiKey(btn.dataset.id, btn.dataset.fp); return; }
  if (name === 'delmfa') { deleteMfa(btn.dataset.id, btn.dataset.dev); return; }
}

function bindNewEvents() {
  const on = function (id, ev, fn) {
    const el = document.getElementById(id);
    if (el) el.addEventListener(ev, fn);
  };
  // 抢机任务
  on('newTaskBtn', 'click', function () { openTaskForm(''); });
  on('reloadTasksBtn', 'click', loadTasks);
  const tasksTbody = document.querySelector('#tasksTable tbody');
  if (tasksTbody) tasksTbody.addEventListener('click', function (e) {
    const b = e.target.closest('button[data-tact]');
    if (b) taskAction(b.dataset.tact, b.dataset.id);
  });
  // 网络
  on('vcnSelect', 'change', loadNetSub);
  on('reloadVcnsBtn', 'click', function () { state.vcns = []; loadNetwork(); });
  on('releasePortsBtn', 'click', releasePorts);
  on('addRuleBtn', 'click', openRuleForm);
  on('removeRulesBtn', 'click', removeRules);
  document.querySelectorAll('.subnav-btn').forEach(function (b) {
    b.addEventListener('click', function () { switchNetSub(b.dataset.net); });
  });
  // 子网
  on('newSubnetBtn', 'click', function () { openSubnetForm(''); });
  on('reloadSubnetsBtn', 'click', loadSubnets);
  const subnetTbody = document.querySelector('#subnetsTable tbody');
  if (subnetTbody) subnetTbody.addEventListener('click', function (e) {
    const b = e.target.closest('button[data-subact]');
    if (b) subnetAction(b.dataset.subact, b.dataset.id);
  });
  // NSG
  on('nsgSelect', 'change', loadNsgRules);
  on('newNsgBtn', 'click', openNsgForm);
  on('deleteNsgBtn', 'click', deleteNsg);
  on('reloadNsgsBtn', 'click', loadNsgs);
  on('addNsgRuleBtn', 'click', openNsgRuleForm);
  on('removeNsgRulesBtn', 'click', removeNsgRules);
  // VNIC
  on('vnicInstance', 'change', loadVnics);
  on('attachVnicBtn', 'click', openAttachVnicForm);
  on('reloadVnicsBtn', 'click', loadVnics);
  const vnicTbody = document.querySelector('#vnicsTable tbody');
  if (vnicTbody) vnicTbody.addEventListener('click', function (e) {
    const b = e.target.closest('button[data-vnicact]');
    if (!b) return;
    const act = b.dataset.vnicact;
    if (act === 'ips') loadPrivateIps(b.dataset.id);
    else if (act === 'edit') openVnicEditForm(b.dataset.id);
    else if (act === 'detach') detachVnic(b.dataset.id, b.dataset.att);
  });
  const ipBox = document.getElementById('privateIpsBox');
  if (ipBox) ipBox.addEventListener('click', function (e) {
    const b = e.target.closest('button[data-ipact]');
    if (b) privateIpAction(b.dataset.ipact, b);
  });
  // 流量
  on('trafficInstance', 'change', loadTrafficVnics);
  on('trafficVnic', 'change', loadTrafficData);
  on('trafficRange', 'change', loadTrafficData);
  on('reloadTrafficBtn', 'click', loadTrafficData);
  // 配额
  on('limitService', 'change', loadLimits);
  on('reloadLimitsBtn', 'click', loadLimits);
  // 租户
  on('newTenantBtn', 'click', function () { openAccountForm(''); });
  on('reloadTenantCfgBtn', 'click', loadTenantConfigs);
  on('reloadTenantInfoBtn', 'click', loadTenantInfo);
  const cfgTbody = document.querySelector('#tenantCfgTable tbody');
  if (cfgTbody) cfgTbody.addEventListener('click', function (e) {
    const b = e.target.closest('button[data-accact]');
    if (b) accountAction(b.dataset.accact, b.dataset.id);
  });
  const usersTbody = document.querySelector('#tenantUsersTable tbody');
  if (usersTbody) usersTbody.addEventListener('click', function (e) {
    const b = e.target.closest('button[data-useract]');
    if (b) userAction(b.dataset.useract, b.dataset.id, b.dataset.name);
  });
  // 设置
  on('checkUpdateBtn', 'click', checkUpdate);
  on('applyUpdateBtn', 'click', applyUpdate);
}

/* ================================================================== 启动 */
async function init() {
  bindEvents();
  bindNewEvents();
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
