package plugin

// tokenHelperHTML is a self-contained Freebuff/Codebuff CLI login helper page.
// Served under CPA /v0/resource/plugins/freebuff/ (management resource).
// It talks to management routes under /v0/management/plugins/freebuff/...
// which require the management API key (browser can use stored key when same-origin).
const tokenHelperHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
<title>Freebuff Token</title>
<style>
:root { --bg:#0f1419; --card:#1a2332; --text:#e7ecf3; --muted:#8b9bb4; --accent:#5b9fd4; --ok:#3ecf8e; --err:#f07178; --border:#2a3548; }
* { box-sizing: border-box; }
body { margin:0; font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; background:var(--bg); color:var(--text); min-height:100vh; }
main { max-width:560px; margin:0 auto; padding:32px 20px 48px; }
h1 { font-size:1.4rem; margin:0 0 4px; }
.sub { color:var(--muted); margin:0 0 24px; font-size:.92rem; }
.card { background:var(--card); border:1px solid var(--border); border-radius:12px; padding:20px; }
.row { display:flex; gap:10px; flex-wrap:wrap; margin-bottom:14px; }
label { display:block; font-size:.8rem; color:var(--muted); margin-bottom:6px; }
select, input, button, textarea { font:inherit; }
select, input, textarea { width:100%; background:#0d1218; color:var(--text); border:1px solid var(--border); border-radius:8px; padding:10px 12px; }
button { cursor:pointer; border:none; border-radius:8px; padding:10px 16px; background:var(--accent); color:#fff; font-weight:600; }
button.secondary { background:transparent; border:1px solid var(--border); color:var(--text); }
button:disabled { opacity:.5; cursor:not-allowed; }
.modes { display:flex; gap:8px; }
.modes button { flex:1; background:#0d1218; border:1px solid var(--border); color:var(--text); }
.modes button.active { border-color:var(--accent); color:var(--accent); background:rgba(91,159,212,.12); }
.area { margin-top:16px; padding-top:16px; border-top:1px solid var(--border); }
.hidden { display:none !important; }
.status { color:var(--muted); font-size:.9rem; }
.ok { color:var(--ok); }
.err { color:var(--err); }
.token { word-break:break-all; background:#0d1218; border:1px solid var(--border); border-radius:8px; padding:12px; font-family:ui-monospace, SFMono-Regular, Menlo, monospace; font-size:.82rem; }
.meta { font-size:.85rem; color:var(--muted); line-height:1.5; }
a { color:var(--accent); }
.tip { font-size:.8rem; color:var(--muted); margin-top:10px; }
</style>
</head>
<body>
<main>
  <h1>Freebuff Token</h1>
  <p class="sub">CLI 扫码登录获取 Codebuff Freebuff Bearer token，可直接写入 CPA 凭据。</p>
  <section class="card">
    <label>平台</label>
    <div class="modes" id="modes">
      <button type="button" class="active" data-mode="freebuff">Freebuff</button>
      <button type="button" data-mode="codebuff">Codebuff</button>
    </div>
    <div style="height:14px"></div>
    <div class="row">
      <button type="button" id="startBtn">开始认证</button>
      <button type="button" class="secondary" id="verifyBtn" disabled>验证 Token</button>
    </div>
    <div id="loginArea" class="area hidden">
      <p class="status">请在浏览器完成登录：</p>
      <p><a id="loginLink" href="#" target="_blank" rel="noopener">打开登录页 →</a></p>
      <p class="tip">登录后此页会自动轮询；也可稍后点「开始认证」重试。</p>
    </div>
    <div id="statusArea" class="area hidden">
      <p id="statusText" class="status">等待登录…</p>
    </div>
    <div id="resultArea" class="area hidden">
      <p class="ok">已拿到 token</p>
      <p class="meta" id="userMeta"></p>
      <label>Token</label>
      <div class="token" id="tokenDisplay"></div>
      <div class="row" style="margin-top:12px">
        <button type="button" class="secondary" id="copyBtn">复制 Token</button>
        <button type="button" class="secondary" id="copyJSONBtn">复制 freebuff.json</button>
      </div>
      <p id="actionTip" class="tip"></p>
    </div>
    <div class="area">
      <label>手动粘贴 Token 验证</label>
      <input id="manualToken" placeholder="Bearer token…"/>
    </div>
  </section>
</main>
<script>
const $ = id => document.getElementById(id);
let mode = 'freebuff';
let currentToken = '';
let pollTimer = null;

// Resource pages may read management key from same-origin localStorage (CPA UI).
function mgmtHeaders() {
  const h = { 'Content-Type': 'application/json' };
  try {
    const key = localStorage.getItem('managementKey')
      || localStorage.getItem('management-key')
      || localStorage.getItem('cpa-management-key')
      || '';
    if (key) h['Authorization'] = 'Bearer ' + key;
  } catch (_) {}
  return h;
}

function mgmtBase() {
  // Prefer absolute management path used by CPA host.
  return '/v0/management/plugins/freebuff';
}

document.querySelectorAll('#modes button').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('#modes button').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    mode = btn.dataset.mode;
  });
});

function show(id, on) { $(id).classList.toggle('hidden', !on); }
function setStatus(text, cls) {
  show('statusArea', true);
  const el = $('statusText');
  el.textContent = text;
  el.className = 'status' + (cls ? ' ' + cls : '');
}

$('startBtn').onclick = async () => {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  show('resultArea', false);
  show('loginArea', false);
  setStatus('请求登录码…');
  $('startBtn').disabled = true;
  try {
    const resp = await fetch(mgmtBase() + '/login/start', {
      method: 'POST',
      headers: mgmtHeaders(),
      body: JSON.stringify({ mode }),
    });
    const data = await resp.json();
    if (!resp.ok || data.error) throw new Error(data.error || data.message || ('HTTP ' + resp.status));
    $('loginLink').href = data.login_url;
    show('loginArea', true);
    setStatus('等待浏览器登录…');
    const state = data.state;
    let attempt = 0;
    pollTimer = setInterval(async () => {
      attempt++;
      try {
        const pr = await fetch(mgmtBase() + '/login/poll', {
          method: 'POST',
          headers: mgmtHeaders(),
          body: JSON.stringify({ state }),
        });
        const pd = await pr.json();
        if (pd.status === 'pending') {
          setStatus('等待登录… 第 ' + attempt + ' 次');
          return;
        }
        if (pd.status === 'error') {
          clearInterval(pollTimer); pollTimer = null;
          setStatus(pd.message || '登录失败', 'err');
          return;
        }
        if (pd.status === 'success' && pd.token) {
          clearInterval(pollTimer); pollTimer = null;
          currentToken = pd.token;
          $('tokenDisplay').textContent = currentToken;
          $('userMeta').textContent = [pd.name, pd.email, pd.id, pd.mode].filter(Boolean).join(' · ');
          show('resultArea', true);
          show('loginArea', false);
          setStatus('登录成功', 'ok');
          $('verifyBtn').disabled = false;
        }
      } catch (e) {
        setStatus('轮询错误: ' + e.message, 'err');
      }
    }, 2000);
  } catch (e) {
    setStatus(e.message, 'err');
  } finally {
    $('startBtn').disabled = false;
  }
};

async function verify(token) {
  const resp = await fetch(mgmtBase() + '/login/verify', {
    method: 'POST',
    headers: mgmtHeaders(),
    body: JSON.stringify({ token }),
  });
  return resp.json();
}

$('verifyBtn').onclick = async () => {
  const token = currentToken || $('manualToken').value.trim();
  if (!token) { $('actionTip').textContent = '没有 token'; return; }
  $('actionTip').textContent = '验证中…';
  try {
    const r = await verify(token);
    $('actionTip').textContent = (r.ok ? '✓ ' : '✗ ') + (r.info || JSON.stringify(r));
    $('actionTip').className = 'tip ' + (r.ok ? 'ok' : 'err');
  } catch (e) {
    $('actionTip').textContent = e.message;
  }
};

$('copyBtn').onclick = async () => {
  if (!currentToken) return;
  await navigator.clipboard.writeText(currentToken);
  $('actionTip').textContent = 'Token 已复制';
};

$('copyJSONBtn').onclick = async () => {
  if (!currentToken) return;
  const json = JSON.stringify({ token: currentToken }, null, 2);
  await navigator.clipboard.writeText(json);
  $('actionTip').textContent = 'freebuff.json 内容已复制，可保存到 CPA auth 目录';
};
</script>
</body>
</html>
`
