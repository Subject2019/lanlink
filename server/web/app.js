// LanLink 前端：零依赖，直接操作 DOM，事件靠 SSE 推送。
(() => {
  const $ = (id) => document.getElementById(id);
  const state = { me: null, messages: [], devices: [], info: {}, es: null, uploading: false };

  // 页面可能挂在根路径，也可能挂在反代前缀（/app/lanlink/）下，
  // 用脚本自身的地址推导出基路径，所有请求都基于它，避免写死绝对路径。
  const BASE = new URL('.', document.currentScript?.src || location.href).pathname;
  const url = (p) => BASE + String(p).replace(/^\/+/, '');

  // ---------- 工具 ----------
  const api = async (path, opts = {}) => {
    const res = await fetch(url(path), { credentials: 'same-origin', ...opts });
    if (res.status === 401) {
      showJoin();
      throw new Error('unauthorized');
    }
    if (!res.ok) {
      let msg = '请求失败';
      try {
        msg = (await res.json()).error || msg;
      } catch {}
      throw new Error(msg);
    }
    return res.status === 204 ? null : res.json();
  };

  const fmtSize = (n) => {
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
    return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
  };
  const fmtTime = (ts) => {
    const d = new Date(ts);
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  };
  let toastTimer;
  const toast = (msg) => {
    const el = $('toast');
    el.textContent = msg;
    el.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (el.hidden = true), 2200);
  };
  const platform = () => {
    const ua = navigator.userAgent;
    if (/Android/i.test(ua)) return 'Android';
    if (/iPhone|iPad/i.test(ua)) return 'iOS';
    if (/Windows/i.test(ua)) return 'Windows';
    if (/Mac OS X/i.test(ua)) return 'Mac';
    return 'Linux';
  };

  // ---------- 分享到其他应用 ----------
  const copyText = async (text, okMsg) => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        const ta = document.createElement('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        ta.remove();
      }
      toast(okMsg);
    } catch {
      toast('无法分享，请长按复制');
    }
  };

  // 安卓 App 走系统分享面板；普通浏览器降级到 Web Share API / 复制链接
  const shareMessage = async (m) => {
    const native = window.LanLinkNative;
    if (m.kind === 'file' && m.file) {
      const href = new URL(
        url(`api/files/${m.id}?n=${encodeURIComponent(m.file.name)}`),
        location.href
      ).href;
      if (native && native.shareFile) return native.shareFile(href, m.file.name, m.file.mime || '');
      if (navigator.share && navigator.canShare) {
        try {
          toast('正在准备文件…');
          const res = await fetch(href, { credentials: 'same-origin' });
          if (!res.ok) throw new Error('fetch failed');
          const blob = await res.blob();
          const file = new File([blob], m.file.name, {
            type: m.file.mime || blob.type || 'application/octet-stream',
          });
          if (navigator.canShare({ files: [file] })) {
            await navigator.share({ files: [file], title: m.file.name });
            return;
          }
        } catch {}
      }
      return copyText(href, '已复制文件链接');
    }

    const text = m.text || '';
    if (!text) return;
    if (native && native.shareText) return native.shareText(text);
    if (navigator.share) {
      try {
        await navigator.share({ text, title: '来自 LanLink' });
        return;
      } catch {}
    }
    return copyText(text, '已复制文本');
  };

  // ---------- 渲染 ----------
  const atBottom = () => {
    const l = $('list');
    return l.scrollHeight - l.scrollTop - l.clientHeight < 80;
  };
  const scrollDown = () => {
    const l = $('list');
    l.scrollTop = l.scrollHeight;
  };

  function messageNode(m) {
    const row = document.createElement('div');
    row.className = 'row' + (m.fromId === state.me?.id ? ' mine' : '');
    row.dataset.id = m.id;

    const meta = document.createElement('div');
    meta.className = 'meta';
    meta.textContent = `${m.fromName} · ${fmtTime(m.ts)}`;
    row.appendChild(meta);

    if (m.kind === 'file' && m.file) {
      const a = document.createElement('a');
      a.className = 'bubble file';
      // n= 参数让安卓客户端的下载器能拿到真实文件名
      a.href = url(`api/files/${m.id}?n=${encodeURIComponent(m.file.name)}`);
      a.setAttribute('download', m.file.name);
      const icon = document.createElement('span');
      icon.className = 'ficon';
      icon.textContent = '📄';
      const box = document.createElement('span');
      const name = document.createElement('div');
      name.className = 'fname';
      name.textContent = m.file.name;
      const size = document.createElement('div');
      size.className = 'fsize';
      size.textContent = fmtSize(m.file.size);
      box.append(name, size);
      const dl = document.createElement('span');
      dl.className = 'dl';
      dl.textContent = '下载';
      a.append(icon, box, dl);
      row.appendChild(a);
    } else {
      const b = document.createElement('div');
      b.className = 'bubble';
      b.textContent = m.text;
      row.appendChild(b);
    }

    const share = document.createElement('button');
    share.type = 'button';
    share.className = 'share-btn';
    share.textContent = '↗ 分享';
    share.title = '分享到其他应用';
    share.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      shareMessage(m);
    });
    row.appendChild(share);
    return row;
  }

  function appendMessage(m, force) {
    if (state.messages.some((x) => x.id === m.id)) return;
    state.messages.push(m);
    const stick = force || atBottom();
    $('list').appendChild(messageNode(m));
    if (stick) scrollDown();
  }

  function renderAll() {
    const list = $('list');
    list.textContent = '';
    for (const m of state.messages) list.appendChild(messageNode(m));
    scrollDown();
  }

  function renderDevices() {
    const online = state.devices.filter((d) => d.online);
    $('onlineBadge').textContent = String(online.length);
    const ul = $('deviceList');
    ul.textContent = '';
    for (const d of state.devices) {
      const li = document.createElement('li');
      const dot = document.createElement('span');
      dot.className = 'dot' + (d.online ? ' on' : '');
      const name = document.createElement('span');
      name.textContent = d.name + (d.id === state.me?.id ? '（我）' : '');
      const p = document.createElement('span');
      p.className = 'p';
      p.textContent = d.platform || '';
      li.append(dot, name, p);
      ul.appendChild(li);
    }
    $('renameInput').value = state.me?.name || '';
    $('meName').textContent = `${state.me?.name || ''} · ${online.length} 台在线`;
  }

  // ---------- 事件流 ----------
  function connect() {
    state.es?.close();
    const es = new EventSource(url('api/events'));
    state.es = es;
    es.onmessage = (e) => {
      let ev;
      try {
        ev = JSON.parse(e.data);
      } catch {
        return;
      }
      if (ev.type === 'message') appendMessage(ev.data, true);
      else if (ev.type === 'devices') {
        state.devices = ev.data || [];
        renderDevices();
      } else if (ev.type === 'delete') {
        const el = $('list').querySelector(`[data-id="${ev.data.id}"]`);
        el?.remove();
        state.messages = state.messages.filter((m) => m.id !== ev.data.id);
      }
    };
    es.onerror = () => {
      // EventSource 会自动重连，这里只做一次降级提示
      if (es.readyState === EventSource.CLOSED) setTimeout(connect, 3000);
    };
  }

  // ---------- 加载 ----------
  async function loadInfo() {
    try {
      const res = await fetch(url('api/info'));
      state.info = await res.json();
    } catch {
      state.info = {};
    }
  }

  async function boot() {
    try {
      const data = await api('/api/state');
      state.me = data.me;
      state.devices = data.devices || [];
      state.messages = data.messages || [];
      $('join').hidden = true;
      $('app').hidden = false;
      renderAll();
      renderDevices();
      connect();
      // 登录态已就绪，让原生补发之前暂存的系统分享
      if (window.LanLinkNative && window.LanLinkNative.onReady) {
        window.LanLinkNative.onReady();
      }
    } catch (err) {
      if (err.message !== 'unauthorized') toast(err.message);
    }
  }

  function showJoin() {
    $('app').hidden = true;
    $('join').hidden = false;
    const c = new URLSearchParams(location.search).get('c');
    if (c) $('joinCode').value = c;
    if (state.info?.open) {
      $('codeRow').hidden = true;
    }
    if (!$('joinName').value) $('joinName').value = platform() + ' 设备';
    const needCode = !state.info?.open && !$('joinCode').value;
    (needCode ? $('joinCode') : $('joinName')).focus();
  }

  // ---------- 发送 ----------
  async function sendText() {
    const ta = $('text');
    const text = ta.value.trim();
    if (!text) return;
    ta.value = '';
    ta.style.height = 'auto';
    try {
      await api('/api/messages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text }),
      });
    } catch (err) {
      toast(err.message);
      ta.value = text;
    }
  }

  function uploadFile(file, onProgress) {
    return new Promise((resolve) => {
      const xhr = new XMLHttpRequest();
      xhr.open('PUT', url('api/files?name=' + encodeURIComponent(file.name)));
      xhr.setRequestHeader('Content-Type', 'application/octet-stream');
      xhr.setRequestHeader('X-File-Type', file.type || '');
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded);
      };
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            appendMessage(JSON.parse(xhr.responseText), true);
          } catch {}
        } else {
          // 服务端会给出「存储空间不足」等具体原因，别只显示文件名
          let msg = '';
          try {
            msg = JSON.parse(xhr.responseText).error || '';
          } catch {}
          toast(msg ? `上传失败：${msg}` : `上传失败：${file.name}`);
        }
        resolve();
      };
      xhr.onerror = () => {
        toast(`上传失败：${file.name}`);
        resolve();
      };
      xhr.send(file);
    });
  }

  // 一次选择的文件并发上传，进度条按所有文件的总字节数汇总
  async function sendFiles(fileList) {
    const list = Array.from(fileList || []);
    if (!list.length) return;
    if (state.uploading) return toast('正在上传，请稍候');
    state.uploading = true;

    const total = list.reduce((sum, f) => sum + f.size, 0) || 1;
    const loaded = list.map(() => 0);
    const wrap = $('progressWrap');
    wrap.hidden = false;
    const update = () => {
      const done = loaded.reduce((a, b) => a + b, 0);
      const pct = Math.min(100, Math.round((done / total) * 100));
      $('progressBar').style.width = pct + '%';
      $('progressText').textContent = `${list.length} 个文件 · ${pct}%`;
    };
    update();

    await Promise.all(
      list.map((file, i) =>
        uploadFile(file, (bytes) => {
          loaded[i] = bytes;
          update();
        })
      )
    );

    state.uploading = false;
    wrap.hidden = true;
    $('progressBar').style.width = '0';
  }

  // ---------- 交互绑定 ----------
  $('btnJoin').addEventListener('click', async () => {
    const name = $('joinName').value.trim();
    const code = $('joinCode').value.trim();
    if (!state.info?.open && !code) return ($('joinErr').textContent = '请输入加入码');
    if (!name) return ($('joinErr').textContent = '请输入设备名称');
    $('btnJoin').disabled = true;
    try {
      await api('/api/join', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ code, name, platform: platform() }),
      });
      history.replaceState(null, '', BASE);
      $('joinErr').textContent = '';
      await boot();
    } catch (err) {
      $('joinErr').textContent = err.message;
    } finally {
      $('btnJoin').disabled = false;
    }
  });

  $('joinCode').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') $('btnJoin').click();
  });

  $('composer').addEventListener('submit', (e) => {
    e.preventDefault();
    sendText();
  });

  const ta = $('text');
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      sendText();
    }
  });
  ta.addEventListener('input', () => {
    ta.style.height = 'auto';
    ta.style.height = Math.min(ta.scrollHeight, 140) + 'px';
  });
  ta.addEventListener('paste', (e) => {
    const files = e.clipboardData?.files;
    if (files && files.length) {
      e.preventDefault();
      sendFiles(files);
    }
  });

  $('btnFile').addEventListener('click', () => {
    // 安卓原生壳接管多选，绕开 WebView 只回传第一个文件的老问题
    if (window.LanLinkNative && window.LanLinkNative.pickFiles) {
      window.LanLinkNative.pickFiles();
    } else {
      $('file').click();
    }
  });
  $('file').addEventListener('change', (e) => {
    sendFiles(e.target.files);
    e.target.value = '';
  });

  // 拖拽上传
  let dragDepth = 0;
  window.addEventListener('dragenter', (e) => {
    if (![...(e.dataTransfer?.types || [])].includes('Files')) return;
    dragDepth++;
    $('drop').hidden = false;
  });
  window.addEventListener('dragover', (e) => e.preventDefault());
  window.addEventListener('dragleave', () => {
    dragDepth = Math.max(0, dragDepth - 1);
    if (!dragDepth) $('drop').hidden = true;
  });
  window.addEventListener('drop', (e) => {
    e.preventDefault();
    dragDepth = 0;
    $('drop').hidden = true;
    sendFiles(e.dataTransfer?.files);
  });

  // 设备抽屉
  if (window.LanLinkNative && window.LanLinkNative.openSettings) {
    $('btnNativeSettings').hidden = false;
    $('btnNativeSettings').addEventListener('click', () => window.LanLinkNative.openSettings());
  }
  if (window.LanLinkNative && window.LanLinkNative.shareServer) {
    $('btnShareApp').hidden = false;
    $('btnShareApp').addEventListener('click', () => window.LanLinkNative.shareServer());
  }
  $('btnDevices').addEventListener('click', () => ($('devices').hidden = false));
  $('btnCloseDevices').addEventListener('click', () => ($('devices').hidden = true));
  $('devices').addEventListener('click', (e) => {
    if (e.target === $('devices')) $('devices').hidden = true;
  });
  $('btnRename').addEventListener('click', async () => {
    const name = $('renameInput').value.trim();
    if (!name) return;
    try {
      await api('/api/device', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      state.me.name = name;
      renderDevices();
      toast('已保存');
    } catch (err) {
      toast(err.message);
    }
  });
  $('btnReload').addEventListener('click', () => boot());

  loadInfo().then(boot);
})();
