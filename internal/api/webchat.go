package api

// webChatJS is the chat box a site puts in its pages with a script tag. It needs nothing else: it
// draws a button and a panel in its own shadow root (so the site's styles and its own never mix),
// talks to BitVoice across sites (CORS, with the visitor's key in Authorization), and keeps the
// conversation in the visitor's browser so it survives moving between pages.
const webChatJS = `(() => {
  "use strict";
  const me = document.currentScript;
  if (!me || window.__bitvoiceChat) return;
  window.__bitvoiceChat = true;
  const base = new URL(me.src).origin;
  const key = "bitvoice-chat:" + base;
  let state = null;
  try { state = JSON.parse(localStorage.getItem(key) || "null"); } catch (e) { state = null; }
  let last = 0, timer = 0, cfg = { title: "Chat with us", welcome: "", colour: "#0ea5e9" };
  const host = document.createElement("div");
  host.setAttribute("data-bitvoice-chat", "");
  const root = host.attachShadow({ mode: "open" });
  root.innerHTML = '<style>' +
    ':host{all:initial}*{box-sizing:border-box;font-family:system-ui,-apple-system,"Segoe UI",sans-serif}' +
    '.btn{position:fixed;right:20px;bottom:20px;width:56px;height:56px;border-radius:50%;border:0;color:#fff;cursor:pointer;box-shadow:0 6px 20px #0004;z-index:2147483646}' +
    '.btn svg{width:26px;height:26px}' +
    '.box{position:fixed;right:20px;bottom:88px;width:340px;max-width:calc(100vw - 40px);height:460px;max-height:calc(100vh - 120px);background:#fff;color:#0f172a;border-radius:14px;box-shadow:0 12px 40px #0005;display:none;flex-direction:column;overflow:hidden;z-index:2147483647}' +
    '.box.open{display:flex}.head{padding:12px 14px;color:#fff;font-weight:600;display:flex;justify-content:space-between;align-items:center}' +
    '.head button{background:none;border:0;color:#fff;font-size:20px;cursor:pointer}' +
    '.msgs{flex:1;overflow:auto;padding:12px;display:flex;flex-direction:column;gap:6px;background:#f8fafc}' +
    '.m{max-width:80%;padding:8px 11px;border-radius:12px;font-size:14px;line-height:1.35;white-space:pre-wrap;word-wrap:break-word}' +
    '.in{align-self:flex-end;color:#fff}.out{align-self:flex-start;background:#e2e8f0}.note{align-self:center;color:#64748b;font-size:12px}' +
    'form{display:flex;flex-direction:column;gap:6px;padding:10px;border-top:1px solid #e2e8f0}' +
    'input,textarea{width:100%;border:1px solid #cbd5e1;border-radius:8px;padding:8px;font-size:14px;color:#0f172a;background:#fff}' +
    'textarea{resize:none;height:56px}.send{border:0;border-radius:8px;padding:8px;color:#fff;font-weight:600;cursor:pointer}' +
    '</style>' +
    '<button class="btn" aria-label="Chat"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg></button>' +
    '<div class="box" role="dialog" aria-label="Chat"><div class="head"><span class="t"></span><button aria-label="Close">×</button></div>' +
    '<div class="msgs" aria-live="polite"></div>' +
    '<form><input class="name" placeholder="Your name" maxlength="80" aria-label="Your name">' +
    '<textarea class="text" placeholder="Write a message…" maxlength="4000" aria-label="Message"></textarea>' +
    '<button class="send" type="submit">Send</button></form></div>';
  const $ = (s) => root.querySelector(s);
  const btn = $(".btn"), box = $(".box"), msgs = $(".msgs"), form = $("form"), name = $(".name"), text = $(".text");
  const colour = (c) => { btn.style.background = c; $(".head").style.background = c; $(".send").style.background = c; };
  const add = (cls, t) => { const d = document.createElement("div"); d.className = "m " + cls; d.textContent = t; if (cls === "in") d.style.background = cfg.colour; msgs.appendChild(d); msgs.scrollTop = msgs.scrollHeight; };
  const save = () => { try { localStorage.setItem(key, JSON.stringify(state)); } catch (e) {} };
  const api = (path, opts) => fetch(base + "/api/webchat" + path, Object.assign({ mode: "cors", credentials: "omit" }, opts));
  async function poll() {
    if (!state) return;
    try {
      const r = await api("/" + state.id + "/messages?after=" + last, { headers: { Authorization: "Bearer " + state.token } });
      if (r.status === 404) { state = null; save(); return; }
      const j = await r.json();
      for (const m of j.messages) { if (m.at > last) last = m.at; add(m.in ? "in" : "out", m.text); }
      if (j.closed && !box.dataset.closed) { box.dataset.closed = "1"; add("note", "This chat was closed. Write again any time."); }
    } catch (e) {}
  }
  function start() { clearInterval(timer); poll(); timer = setInterval(poll, box.classList.contains("open") ? 2500 : 15000); }
  btn.onclick = () => { box.classList.toggle("open"); if (box.classList.contains("open")) { name.style.display = state ? "none" : ""; text.focus(); } start(); };
  $(".head button").onclick = () => { box.classList.remove("open"); start(); };
  form.onsubmit = async (e) => {
    e.preventDefault();
    const t = text.value.trim();
    if (!t) return;
    text.value = "";
    delete box.dataset.closed;
    try {
      if (!state) {
        const r = await api("/start", { method: "POST", headers: { "Content-Type": "application/json", "X-BitVoice": "1" }, body: JSON.stringify({ name: name.value.trim(), text: t, page: location.href }) });
        if (!r.ok) { add("note", "The message could not be sent. Please try again later."); return; }
        state = await r.json(); save(); name.style.display = "none";
      } else {
        const r = await api("/" + state.id + "/messages", { method: "POST", headers: { "Content-Type": "application/json", Authorization: "Bearer " + state.token }, body: JSON.stringify({ text: t }) });
        if (!r.ok) { add("note", "The message could not be sent."); return; }
      }
      poll();
    } catch (e) { add("note", "No connection: the message was not sent."); }
  };
  api("/config").then((r) => r.ok ? r.json() : null).then((c) => {
    if (!c) return;
    cfg = c; colour(c.colour); $(".t").textContent = c.title;
    if (c.welcome) add("out", c.welcome);
    document.body.appendChild(host);
    if (state) start();
  }).catch(() => {});
})();
`
