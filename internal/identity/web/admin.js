const $ = id => document.getElementById(id);
const node = (tag, text, cls) => { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; };
const titles = { overview: ['运营概览', '统一网站的注册用户、登录活动与账号管理。'], users: ['用户管理', '查看固定玩家 ID、注册来源及最近登录，管理账号访问权限。'], online: ['网站在线', '查看正在访问游戏客户端网站的已登录用户。'], servers: ['已登记服务器', '私人游戏服务器的身份接入资料。'] };
const state = { view: 'overview', csrf: '', user: null, page: 1, q: '', status: 'all', refreshing: false, selected: null };
const newConnectionId = () => Array.from(crypto.getRandomValues(new Uint8Array(16)), value => value.toString(16).padStart(2, '0')).join('');
let presenceConnection = '', presenceActive = false;
async function heartbeat() {
  if (document.hidden || !state.user) return;
  if (!presenceActive) { presenceConnection = newConnectionId(); presenceActive = true; }
  try { await request('/api/auth/heartbeat', { connectionId: presenceConnection, online: true }); }
  catch (error) { if (error.status === 401) showLogin(); }
}
function leaveWebsite() {
  if (!presenceActive) return;
  const payload = JSON.stringify({ connectionId: presenceConnection, online: false }); presenceActive = false;
  if (!navigator.sendBeacon('/api/auth/heartbeat', new Blob([payload], { type: 'application/json' }))) fetch('/api/auth/heartbeat', { method: 'POST', credentials: 'same-origin', headers: { 'content-type': 'application/json' }, body: payload, keepalive: true }).catch(() => {});
}
const formatTime = value => value ? new Date(value * 1000).toLocaleString('zh-CN', { hour12: false }) : '暂无记录';
const ipText = value => value || '未知（旧账号未记录）';

async function request(path, data) {
  const options = { credentials: 'same-origin', cache: 'no-store' };
  if (data !== undefined) Object.assign(options, { method: 'POST', headers: { 'content-type': 'application/json', 'X-Mcraft-CSRF': state.csrf }, body: JSON.stringify(data) });
  const response = await fetch(path, options);
  const result = await response.json();
  if (!response.ok) { const error = new Error(result.error || '无法完成请求'); error.status = response.status; throw error; }
  return result;
}
function notice(text, error = false) { $('message').hidden = !text; $('message').className = 'notice' + (error ? ' error' : ''); $('message').textContent = text; }
function button(text, action, cls = 'secondary') { const b = node('button', text, cls); b.type = 'button'; b.addEventListener('click', action); return b; }
function cell(main, sub, mono = false) { const td = node('td'); td.append(node('div', main, 'cell-main')); if (sub !== undefined) td.append(node('div', sub, 'cell-sub' + (mono ? ' mono' : ''))); return td; }
function badge(text, type = '') { return node('span', text, 'badge' + (type ? ' ' + type : '')); }
function statusCell(user) { const td = node('td'); td.append(badge(user.disabled ? '已禁用' : user.online ? '在线' : '正常', user.disabled ? 'disabled' : user.online ? '' : 'offline')); if (user.protected) td.append(badge('管理员', 'protected')); return td; }
function websiteActivity(user) { return (user.locations || []).filter(p => p.location === 'website'); }
function connectionCount(user) { return websiteActivity(user).reduce((n, p) => n + p.connections, 0); }
function lastActivity(user) { return Math.max(0, ...websiteActivity(user).map(p => p.lastSeenAt)); }
function locationText(locations) { return (locations || []).some(p => p.location === 'website') ? '游戏客户端网站' : '当前离线'; }
function panel(title, description, count) { const section = node('section', undefined, 'panel'), heading = node('div', undefined, 'panel-heading'), text = node('div'); text.append(node('h2', title)); if (description) text.append(node('p', description)); heading.append(text); if (count !== undefined) heading.append(node('span', count + ' 个', 'count')); section.append(heading); return section; }
function table(headers, rows) { const wrap = node('div', undefined, 'table-wrap'), table = node('table'), head = node('thead'), row = node('tr'), body = node('tbody'); for (const title of headers) row.append(node('th', title)); head.append(row); table.append(head, body); for (const r of rows) body.append(r); wrap.append(table); return wrap; }
function empty(text) { return node('div', text, 'empty'); }
function accountRow(user, compact = false) {
  const row = node('tr'); row.append(cell(user.username, user.name || '尚未设置名字'), cell(user.id.slice(0, 12) + '…', user.id, true));
  if (!compact) row.append(cell(formatTime(user.createdAt), ipText(user.registrationIp)), cell(formatTime(user.lastLoginAt), ipText(user.lastLoginIp)));
  else row.append(cell(formatTime(user.createdAt), ipText(user.registrationIp)));
  row.append(statusCell(user)); const actions = node('td'); actions.append(button('查看详情', () => showDetail(user.id), 'text-button')); row.append(actions); return row;
}
async function overview() {
  const [stats, users, online] = await Promise.all([request('/api/admin/stats'), request('/api/admin/users?pageSize=6'), request('/api/admin/online')]);
  const fragment = document.createDocumentFragment(), cards = node('div', undefined, 'stats-grid');
  for (const [label, value, hint, symbol] of [['注册用户', stats.accounts, '最近 24 小时新增 ' + stats.registeredToday + ' 人', '◎'], ['网站在线', stats.online, '已登录访客 · 按固定 ID 去重', '◉'], ['已登记服务器', stats.servers, '私人服务器身份接入资料', '▤'], ['有效登录会话', stats.sessions, '已禁用账号 ' + stats.disabled + ' 人', '◇']]) { const card = node('div', undefined, 'stat-card'), labelNode = node('div', label, 'stat-label'); labelNode.append(node('span', symbol)); card.append(labelNode, node('div', value.toLocaleString(), 'stat-value'), node('div', hint, 'stat-hint')); cards.append(card); }
  fragment.append(cards);
  const live = panel('网站在线活动', '已登录用户的页面活动', online.total);
  live.append(online.users.length ? table(['账号 / 玩家', '固定玩家 ID', '活动页面数', '最近活动'], online.users.slice(0, 6).map(u => { const tr = node('tr'); tr.append(cell(u.username, u.name), cell(u.id, undefined, true), cell(connectionCount(u).toString()), cell(formatTime(lastActivity(u)))); return tr; })) : empty('当前没有在线用户'));
  fragment.append(live);
  const recent = panel('最近注册', '旧账号没有记录的来源信息显示为未知', users.total); recent.append(users.users.length ? table(['账号 / 玩家', '固定玩家 ID', '注册时间 / IP', '状态', ''], users.users.map(u => accountRow(u, true))) : empty('暂无注册账号')); fragment.append(recent);
  fragment.append(node('p', '在线状态来自已登录用户的网站页面活动，异常断网后会在约 90 秒内过期。', 'fine-print'));
  $('view').replaceChildren(fragment);
}
async function users() {
  const data = await request('/api/admin/users?' + new URLSearchParams({ q: state.q, status: state.status, page: state.page, pageSize: 25 })); state.page = data.page;
  const section = panel('全部用户', '账号身份跨服务器保持一致，游戏存档由各私人服务器保存', data.total), filter = node('form', undefined, 'filter-bar'), search = node('input', undefined, 'search-input'), select = node('select'); search.placeholder = '搜索账号、玩家名字或固定 ID'; search.maxLength = 256; search.value = state.q; search.setAttribute('aria-label', '搜索用户');
  for (const [value, label] of [['all', '全部状态'], ['enabled', '正常账号'], ['disabled', '已禁用'], ['online', '当前在线']]) { const option = node('option', label); option.value = value; select.append(option); } select.value = state.status; select.setAttribute('aria-label', '用户状态');
  const submit = node('button', '搜索', 'primary'); submit.type = 'submit'; filter.append(search, select, submit); filter.addEventListener('submit', e => { e.preventDefault(); state.q = search.value.trim(); state.status = select.value; state.page = 1; refresh(); }); section.append(filter);
  section.append(data.users.length ? table(['账号 / 玩家', '固定玩家 ID', '注册时间 / IP', '最近登录 / IP', '状态', '操作'], data.users.map(u => accountRow(u))) : empty('没有匹配的用户'));
  const pagination = node('div', undefined, 'pagination'), controls = node('div', undefined, 'pagination-controls'), previous = button('上一页', () => { state.page--; refresh(); }), next = button('下一页', () => { state.page++; refresh(); }); previous.disabled = data.page <= 1; next.disabled = data.page >= data.pages; controls.append(previous, node('span', data.page + ' / ' + data.pages), next); pagination.append(node('span', '共 ' + data.total + ' 个用户 · 每页 25 个'), controls); section.append(pagination); $('view').replaceChildren(section);
}
async function online() {
  const data = await request('/api/admin/online'), section = panel('网站在线用户', '每 15 秒自动更新 · 同一账号的多个活动页面合并展示', data.total);
  section.append(data.users.length ? table(['账号 / 玩家', '固定玩家 ID', '活动页面数', '最近活动', ''], data.users.map(u => { const tr = node('tr'); tr.append(cell(u.username, u.name), cell(u.id, undefined, true), cell(connectionCount(u).toString()), cell(formatTime(lastActivity(u)))); const action = node('td'); action.append(button('查看详情', () => showDetail(u.id), 'text-button')); tr.append(action); return tr; })) : empty('当前没有在线用户'));
  $('view').replaceChildren(section, node('p', '首页、本地游戏、线上游戏以及账号页面都属于网站活动。关闭页面会结束该页面活动，异常断网后最多保留约 90 秒；有效登录会话数量不等于在线人数。', 'fine-print'));
}
async function servers() {
  const data = await request('/api/admin/servers'), section = panel('已登记游戏服务器', '核心服务统一验证身份，各服务器独立运行世界和插件', data.total);
  section.append(data.servers.length ? table(['服务器 / 固定 ID', '接入地址 / 回调', '服主'], data.servers.map(server => { const tr = node('tr'), address = node('td'), link = node('a', server.frontendUrl, 'server-link'), callback = node('div', server.callbackUrl, 'cell-sub'); link.href = server.frontendUrl; link.target = '_blank'; link.rel = 'noopener noreferrer'; address.append(link, callback); tr.append(cell(server.name, server.id, true), address, cell(server.owner?.username || '配置登记', server.owner?.name || server.ownerId || '服务端配置')); return tr; })) : empty('尚未登记游戏服务器'));
  $('view').replaceChildren(section, node('p', '服务器由用户下载服务端并自行部署，通过核心账号中心登记回调地址。核心后台不创建、托管或合并它们的世界。', 'fine-print'));
}
async function refresh(silent = false) {
  if (state.refreshing || !state.user) return;
  state.refreshing = true; $('refresh').disabled = true;
  if (!silent) notice('');
  const view = state.view;
  try { await ({ overview, users, online, servers })[view](); $('updated').textContent = '更新于 ' + new Date().toLocaleTimeString('zh-CN', { hour12: false }); }
  catch (error) { if (error.status === 401) showLogin(); else notice(error.message, true); }
  finally { state.refreshing = false; $('refresh').disabled = false; if (view !== state.view && state.user) refresh(); }
}
function navigate(view) { state.view = view; $('view-title').textContent = titles[view][0]; $('breadcrumb-view').textContent = titles[view][0]; $('view-description').textContent = titles[view][1]; document.querySelectorAll('[data-view]').forEach(b => { b.classList.toggle('active', b.dataset.view === view); b.setAttribute('aria-current', b.dataset.view === view ? 'page' : 'false'); }); if (!state.refreshing) $('view').replaceChildren(node('div', '正在读取数据……', 'loading')); refresh(); }
function detailItem(list, label, value, mono = false) { list.append(node('dt', label), node('dd', value, mono ? 'mono' : undefined)); }
async function showDetail(id) {
  const dialog = $('detail-dialog'); $('dialog-title').textContent = '用户详情'; $('dialog-content').replaceChildren(node('p', '正在读取……', 'dialog-description')); if (!dialog.open) dialog.showModal();
  try { const { user } = await request('/api/admin/users/' + encodeURIComponent(id)); state.selected = user; const list = node('dl', undefined, 'details'); detailItem(list, '账号', user.username); detailItem(list, '玩家名字', user.name || '尚未设置'); detailItem(list, '固定玩家 ID', user.id, true); detailItem(list, '注册时间', formatTime(user.createdAt)); detailItem(list, '注册 IP', ipText(user.registrationIp), true); detailItem(list, '最近登录', formatTime(user.lastLoginAt)); detailItem(list, '登录 IP', ipText(user.lastLoginIp), true); detailItem(list, '账号状态', user.disabled ? '已禁用' : '正常'); detailItem(list, '有效会话', user.sessionCount.toString()); detailItem(list, '当前活动', locationText(user.locations)); detailItem(list, '核心权限', user.protected ? '核心账号管理员（受保护）' : '普通用户'); const actions = node('div', undefined, 'dialog-actions'), status = button(user.disabled ? '恢复账号' : '禁用账号', () => confirmMutation(user, 'status'), user.disabled ? 'primary' : 'danger'), revoke = button('撤销全部登录会话', () => confirmMutation(user, 'revoke')); status.disabled = user.protected && !user.disabled; revoke.disabled = user.protected; actions.append(status, revoke); $('dialog-content').replaceChildren(list, actions); }
  catch (error) { $('dialog-content').replaceChildren(node('p', error.message, 'error')); }
}
function confirmMutation(user, kind) {
  const disable = !user.disabled, title = kind === 'status' ? disable ? '禁用账号' : '恢复账号' : '撤销登录会话'; $('dialog-title').textContent = title;
  const description = node('p', kind === 'revoke' ? `撤销 ${user.username} 的全部核心登录会话。该用户需要重新登录，游戏服务器会在身份校验时断开旧身份。` : disable ? `禁用 ${user.username} 后，将撤销全部核心会话并阻止再次登录。恢复账号后不会恢复旧会话。` : `恢复 ${user.username} 的登录权限。原固定 ID 与各服务器存档保持关联。`, 'dialog-description'), actions = node('div', undefined, 'dialog-actions'), error = node('p', '', 'dialog-error'), submit = button('确认' + title, async () => { submit.disabled = true; cancel.disabled = true; try { const result = await request('/api/admin/users/' + encodeURIComponent(user.id) + (kind === 'status' ? '/status' : '/sessions/revoke'), kind === 'status' ? { disabled: disable } : {}); $('detail-dialog').close(); notice(result.auditWarning ? title + '已生效，但审计结果写入异常，请检查服务端日志。' : title + '已完成。'); await refresh(true); } catch (e) { error.textContent = e.message; submit.disabled = false; cancel.disabled = false; } }, kind === 'revoke' || disable ? 'danger' : 'primary'), cancel = button('返回详情', () => showDetail(user.id)); actions.append(cancel, submit); $('dialog-content').replaceChildren(description, actions, error);
}
function showLogin(message = '') { leaveWebsite(); state.user = null; state.csrf = ''; $('console').hidden = true; $('login-panel').hidden = false; $('login-error').textContent = message; }
async function boot() {
  try { const me = await request('/api/admin/me'); state.user = me.user; state.csrf = me.csrfToken; $('login-panel').hidden = true; $('console').hidden = false; $('admin-name').textContent = (me.user.name || me.user.username) + ' · 核心管理员'; $('issuer').textContent = me.issuer; await heartbeat(); navigate(state.view); }
  catch (error) { showLogin(error.status === 401 ? '' : error.message); }
}
$('login-form').addEventListener('submit', async e => { e.preventDefault(); const form = e.currentTarget, submit = form.querySelector('button'), password = form.elements.password; submit.disabled = true; $('login-error').textContent = ''; try { await request('/api/auth/login', { username: form.elements.username.value.trim(), password: password.value }); password.value = ''; await boot(); } catch (error) { $('login-error').textContent = error.message; } finally { password.value = ''; submit.disabled = false; } });
document.querySelectorAll('[data-view]').forEach(b => b.addEventListener('click', () => navigate(b.dataset.view)));
$('refresh').addEventListener('click', () => refresh()); $('dialog-close').addEventListener('click', () => $('detail-dialog').close());
$('logout').addEventListener('click', async () => { try { await request('/api/auth/logout', {}); $('detail-dialog').close(); showLogin(); } catch (error) { notice(error.message, true); } });
setInterval(() => { if (!document.hidden && state.user && (state.view === 'online' || state.view === 'overview')) refresh(true); }, 15000);
setInterval(heartbeat, 30000);
document.addEventListener('visibilitychange', () => document.hidden ? leaveWebsite() : heartbeat());
window.addEventListener('pagehide', leaveWebsite);
window.addEventListener('pageshow', heartbeat);
await boot();
