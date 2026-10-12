// Builds docs/manual/index.html from src/manual.ts (the articles under Help), then the PDF with Chromium.
// Run with: make manual
import { chromium } from '@playwright/test'
const { manual } = await import(process.argv[2])
import { writeFileSync } from 'fs'
const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
const byRoute = Object.fromEntries(manual.filter((a) => a.route).map((a) => [a.route, a.id]))
function inline(s) {
  let out = '', last = 0
  for (const m of s.matchAll(/(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)]+\))/g)) {
    out += esc(s.slice(last, m.index)); const t = m[0]
    if (t.startsWith('`')) out += `<code>${esc(t.slice(1, -1))}</code>`
    else if (t.startsWith('**')) out += `<strong>${esc(t.slice(2, -2))}</strong>`
    else {
      const [, text, href] = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(t)
      if (href.startsWith('#/')) { const id = byRoute[href.slice(1).split('?')[0]]; out += id ? `<a href="#${id}">${esc(text)}</a>` : `<strong>${esc(text)}</strong>` }
      else if (href.startsWith('/')) out += `<code>${esc(href)}</code> (${esc(text)})`
      else out += `<a href="${esc(href)}">${esc(text)}</a>`
    }
    last = m.index + t.length
  }
  return out + esc(s.slice(last))
}
function md(text) {
  const lines = text.split('\n'), out = []
  for (let i = 0; i < lines.length;) {
    const l = lines[i]
    if (!l.trim()) { i++; continue }
    if (l.startsWith('```')) { const c = []; for (i++; i < lines.length && !lines[i].startsWith('```'); i++) c.push(lines[i]); i++; out.push(`<pre>${esc(c.join('\n'))}</pre>`); continue }
    if (l.startsWith('## ')) { out.push(`<h3>${inline(l.slice(3))}</h3>`); i++; continue }
    const b = /^(- |\d+\. )/
    if (b.test(l)) { const ol = /^\d/.test(l), it = []; for (; i < lines.length && b.test(lines[i]); i++) it.push(lines[i].replace(b, '')); out.push(`<${ol ? 'ol' : 'ul'}>${it.map((x) => `<li>${inline(x)}</li>`).join('')}</${ol ? 'ol' : 'ul'}>`); continue }
    const p = []; for (; i < lines.length && lines[i].trim() && !b.test(lines[i]) && !lines[i].startsWith('## ') && !lines[i].startsWith('```'); i++) p.push(lines[i])
    out.push(`<p>${inline(p.join(' '))}</p>`)
  }
  return out.join('\n')
}
const logo = `<svg viewBox="0 0 32 32" width="34" height="34" aria-hidden="true"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#38bdf8"/><stop offset="1" stop-color="#a78bfa"/></linearGradient></defs><rect width="32" height="32" rx="8" fill="#0b1220"/><path d="M7 17c0-5 4-9 9-9s9 4 9 9" fill="none" stroke="url(#g)" stroke-width="2.5" stroke-linecap="round"/><rect x="5" y="16" width="5" height="8" rx="2" fill="url(#g)"/><rect x="22" y="16" width="5" height="8" rx="2" fill="url(#g)"/><path d="M13 20h1.5l1.5-4 2 8 1.5-4H21" fill="none" stroke="#34d399" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`
const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>BitVoice manual</title><meta name="description" content="The BitVoice manual: the phone system, the 911 profile, the radio dispatch console and NG911 Core Services.">
<style>
:root{color-scheme:dark}*{box-sizing:border-box}body{margin:0;font:16px/1.6 system-ui,-apple-system,"Segoe UI",sans-serif;background:#060b16;color:#cbd5e1}
a{color:#7dd3fc}code{font:0.88em ui-monospace,SFMono-Regular,Menlo,monospace;background:#111a2e;color:#fcd34d;padding:1px 5px;border-radius:5px}
pre{background:#020617;color:#a7f3d0;padding:14px;border-radius:10px;overflow-x:auto;font:13px/1.5 ui-monospace,Menlo,monospace;border:1px solid #1e293b}
nav{position:fixed;top:0;left:0;bottom:0;width:280px;overflow-y:auto;background:#0b1220;border-right:1px solid #1e293b;padding:20px 14px}
nav .brand{display:flex;gap:10px;align-items:center;margin-bottom:6px}nav .brand b{font-size:20px;background:linear-gradient(90deg,#7dd3fc,#c4b5fd);-webkit-background-clip:text;background-clip:text;color:transparent}
nav input{width:100%;margin:12px 0;padding:8px 10px;border-radius:8px;border:1px solid #334155;background:#020617;color:#e2e8f0}
nav a{display:block;padding:4px 8px;border-radius:6px;color:#94a3b8;text-decoration:none;font-size:14px}nav a:hover{background:#111a2e;color:#e2e8f0}
main{margin-left:280px;max-width:900px;padding:32px 48px 80px}h1{font-size:40px;margin:0;background:linear-gradient(90deg,#7dd3fc,#c4b5fd);-webkit-background-clip:text;background-clip:text;color:transparent}
.lede{color:#94a3b8;font-size:18px}section{border-top:1px solid #1e293b;padding-top:18px;margin-top:28px}h2{color:#e2e8f0;font-size:24px;margin:0 0 6px}
h3{color:#c4b5fd;font-size:17px;margin:18px 0 4px}strong{color:#f1f5f9}.where{font-size:13px;color:#64748b}footer{margin-top:60px;color:#64748b;font-size:13px}
@media (max-width:900px){nav{position:static;width:auto;height:auto;max-height:40vh}main{margin:0;padding:20px}}
@media print{:root{color-scheme:light}body{background:#fff;color:#1e293b;font-size:11pt}nav{display:none}main{margin:0;padding:0;max-width:none}h1{color:#0c4a6e;-webkit-text-fill-color:#0c4a6e}
h2{color:#0c4a6e}h3{color:#5b21b6}strong{color:#0f172a}code{background:#f1f5f9;color:#9a3412}pre{background:#f8fafc;color:#14532d;border-color:#e2e8f0;white-space:pre-wrap}a{color:#0369a1}section{break-inside:auto}h2,h3{break-after:avoid}}
</style></head><body>
<nav aria-label="Contents"><div class="brand">${logo}<b>BitVoice</b></div><div class="where">Manual</div>
<input type="search" placeholder="Find a topic" aria-label="Find a topic" oninput="const q=this.value.toLowerCase();document.querySelectorAll('nav a[data-k]').forEach(a=>a.style.display=a.dataset.k.includes(q)?'':'none')">
${manual.map((a) => `<a href="#${a.id}" data-k="${esc((a.title + ' ' + a.keys).toLowerCase())}">${esc(a.title)}</a>`).join('\n')}</nav>
<main><h1>BitVoice manual</h1><p class="lede">The phone system, the 911 profile, the radio dispatch console and NG911 Core Services, set up in the web interface. The same articles are under <strong>Help</strong> in every BitVoice.</p>
${manual.map((a) => `<section id="${a.id}"><h2>${esc(a.title)}</h2>${a.route ? `<div class="where">In the web interface: ${esc(a.route)}</div>` : ''}\n${md(a.body)}</section>`).join('\n')}
<footer>BitVoice is a product of BioDream LLC, published under the Apache License 2.0.</footer></main></body></html>`
writeFileSync(process.argv[3], html)
const b = await chromium.launch(); const p = await b.newPage()
await p.goto('file://' + process.argv[3])
await p.pdf({ path: process.argv[4], format: 'Letter', printBackground: true, displayHeaderFooter: true, headerTemplate: '<span></span>',
  footerTemplate: '<div style="font-size:8px;width:100%;text-align:center;color:#64748b">BitVoice manual · BioDream LLC · <span class="pageNumber"></span> / <span class="totalPages"></span></div>',
  margin: { top: '0.6in', bottom: '0.7in', left: '0.7in', right: '0.7in' } })
await b.close()
console.log(manual.length, 'articles')
