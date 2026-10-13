// Builds docs/manual/index.html from src/manual.ts (the articles under Help) and src/figures.ts, then the
// PDF with Chromium. Run with: make manual
import { chromium } from '@playwright/test'
import { writeFileSync } from 'fs'
const { manual, parts } = await import(process.argv[2])
const { figures } = await import(process.argv[2].replace(/manual\.mjs$/, 'figures.mjs'))

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
const byId = Object.fromEntries(manual.map((a) => [a.id, a]))

// Chapters are numbered within the order of the parts; figures across the whole manual.
const order = parts.flatMap((p) => p.ids)
const missing = manual.filter((a) => !order.includes(a.id)).map((a) => a.id)
if (missing.length) throw new Error('articles in no part: ' + missing.join(', '))
const number = Object.fromEntries(order.map((id, i) => [id, i + 1]))
// A link to a screen points at the chapter about that screen: the first in reading order with its route.
const byRoute = {}
for (const id of order) { const r = byId[id].route; if (r && !(r in byRoute)) byRoute[r] = id }
let figureNo = 0

function inline(s) {
  let out = '', last = 0
  for (const m of s.matchAll(/(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)]+\))/g)) {
    out += esc(s.slice(last, m.index)); const t = m[0]
    if (t.startsWith('`')) out += `<code>${esc(t.slice(1, -1))}</code>`
    else if (t.startsWith('**')) out += `<strong>${esc(t.slice(2, -2))}</strong>`
    else {
      const [, text, href] = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(t)
      if (href.startsWith('#/')) {
        const id = byRoute[href.slice(1).split('?')[0]]
        out += id ? `<a href="#${id}">${esc(text)}</a><span class="ref"> (chapter ${number[id]})</span>` : `<strong>${esc(text)}</strong>`
      } else if (href.startsWith('#')) {
        const id = href.slice(1)
        out += byId[id] ? `<a href="#${id}">${esc(text)}</a><span class="ref"> (chapter ${number[id]})</span>` : esc(text)
      } else if (href.startsWith('/')) out += `<code>${esc(href)}</code> (${esc(text)})`
      else out += `<a href="${esc(href)}">${esc(text)}</a>`
    }
    last = m.index + t.length
  }
  return out + esc(s.slice(last))
}

const special = (l) => /^(## |### |```|> |\||::figure )/.test(l)
function md(text) {
  const lines = text.split('\n'), out = []
  for (let i = 0; i < lines.length;) {
    const l = lines[i]
    if (!l.trim()) { i++; continue }
    if (l.startsWith('```')) { const c = []; for (i++; i < lines.length && !lines[i].startsWith('```'); i++) c.push(lines[i]); i++; out.push(`<pre>${esc(c.join('\n'))}</pre>`); continue }
    if (l.startsWith('::figure ')) {
      const f = figures[l.slice(9).trim()]
      if (!f) throw new Error('no figure ' + l.slice(9))
      figureNo++
      out.push(`<figure>${f.svg}<figcaption><b>Figure ${figureNo}.</b> ${esc(f.title)}</figcaption></figure>`)
      i++; continue
    }
    if (l.startsWith('> ')) { const n = []; for (; i < lines.length && lines[i].startsWith('> '); i++) n.push(lines[i].slice(2)); out.push(`<aside>${inline(n.join(' '))}</aside>`); continue }
    if (l.startsWith('|')) {
      const rows = []
      for (; i < lines.length && lines[i].startsWith('|'); i++) {
        const cells = lines[i].replace(/^\|/, '').replace(/\|$/, '').split('|').map((c) => c.trim())
        if (!cells.every((c) => /^:?-+:?$/.test(c))) rows.push(cells)
      }
      const [head, ...body] = rows
      out.push(`<table><thead><tr>${head.map((c) => `<th>${inline(c)}</th>`).join('')}</tr></thead><tbody>${body.map((r) => `<tr>${r.map((c) => `<td>${inline(c)}</td>`).join('')}</tr>`).join('')}</tbody></table>`)
      continue
    }
    if (l.startsWith('### ')) { out.push(`<h4>${inline(l.slice(4))}</h4>`); i++; continue }
    if (l.startsWith('## ')) { out.push(`<h3>${inline(l.slice(3))}</h3>`); i++; continue }
    const b = /^(- |\d+\. )/
    if (b.test(l)) { const ol = /^\d/.test(l), it = []; for (; i < lines.length && b.test(lines[i]); i++) it.push(lines[i].replace(b, '')); out.push(`<${ol ? 'ol' : 'ul'}>${it.map((x) => `<li>${inline(x)}</li>`).join('')}</${ol ? 'ol' : 'ul'}>`); continue }
    const p = []; for (; i < lines.length && lines[i].trim() && !b.test(lines[i]) && !special(lines[i]); i++) p.push(lines[i])
    out.push(`<p>${inline(p.join(' '))}</p>`)
  }
  return out.join('\n')
}

const logo = `<svg viewBox="0 0 32 32" width="34" height="34" aria-hidden="true"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#38bdf8"/><stop offset="1" stop-color="#a78bfa"/></linearGradient></defs><rect width="32" height="32" rx="8" fill="#0b1220"/><path d="M7 17c0-5 4-9 9-9s9 4 9 9" fill="none" stroke="url(#g)" stroke-width="2.5" stroke-linecap="round"/><rect x="5" y="16" width="5" height="8" rx="2" fill="url(#g)"/><rect x="22" y="16" width="5" height="8" rx="2" fill="url(#g)"/><path d="M13 20h1.5l1.5-4 2 8 1.5-4H21" fill="none" stroke="#34d399" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`
// Its own gradient id: the menu's logo is hidden in print, and a gradient inside hidden SVG draws nothing.
const bigLogo = logo.replace('width="34" height="34"', 'width="96" height="96"').replaceAll('id="g"', 'id="g2"').replaceAll('url(#g)', 'url(#g2)')

const words = manual.reduce((n, a) => n + a.body.split(/\s+/).length, 0)
const chapters = parts.map((p, pi) => `<section class="part" id="part-${pi + 1}"><div class="partno">Part ${pi + 1}</div><h1 class="parttitle">${esc(p.title)}</h1><p class="lede">${inline(p.intro)}</p></section>\n` +
  p.ids.map((id) => {
    const a = byId[id]
    return `<section class="chapter" id="${a.id}"><div class="chapno">Chapter ${number[id]}</div><h2>${esc(a.title)}</h2>${a.route ? `<div class="where">In the web interface: <code>${esc(a.route)}</code></div>` : ''}\n${md(a.body)}</section>`
  }).join('\n')).join('\n')

const toc = parts.map((p, pi) => `<li class="tp"><a href="#part-${pi + 1}">Part ${pi + 1}. ${esc(p.title)}</a><ol>${p.ids.map((id) => `<li><a href="#${id}"><span>${number[id]}.</span> ${esc(byId[id].title)}</a></li>`).join('')}</ol></li>`).join('')

const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>BitVoice manual</title><meta name="description" content="The BitVoice manual: the phone system, conference rooms, feature codes, hosted organisations, the 911 profile, the radio dispatch console and NG911 Core Services.">
<style>
:root{color-scheme:dark}*{box-sizing:border-box}body{margin:0;font:16px/1.65 system-ui,-apple-system,"Segoe UI",sans-serif;background:#060b16;color:#cbd5e1}
a{color:#7dd3fc}code{font:0.88em ui-monospace,SFMono-Regular,Menlo,monospace;background:#111a2e;color:#fcd34d;padding:1px 5px;border-radius:5px}
pre{background:#020617;color:#a7f3d0;padding:14px;border-radius:10px;overflow-x:auto;font:13px/1.5 ui-monospace,Menlo,monospace;border:1px solid #1e293b}
nav{position:fixed;top:0;left:0;bottom:0;width:300px;overflow-y:auto;background:#0b1220;border-right:1px solid #1e293b;padding:20px 14px}
nav .brand{display:flex;gap:10px;align-items:center;margin-bottom:6px}nav .brand b{font-size:20px;background:linear-gradient(90deg,#7dd3fc,#c4b5fd);-webkit-background-clip:text;background-clip:text;color:transparent}
nav input{width:100%;margin:12px 0;padding:8px 10px;border-radius:8px;border:1px solid #334155;background:#020617;color:#e2e8f0}
nav .np{margin:14px 6px 4px;font-size:11px;letter-spacing:.08em;text-transform:uppercase;color:#a78bfa}
nav a{display:block;padding:3px 8px;border-radius:6px;color:#94a3b8;text-decoration:none;font-size:14px}nav a:hover{background:#111a2e;color:#e2e8f0}
main{margin-left:300px;max-width:960px;padding:32px 48px 80px}
.title{min-height:70vh;display:flex;flex-direction:column;justify-content:center;gap:14px}
.title h1{font-size:56px;margin:0;background:linear-gradient(90deg,#7dd3fc,#c4b5fd);-webkit-background-clip:text;background-clip:text;color:transparent}
.title .sub{font-size:22px;color:#e2e8f0;max-width:640px}.title .meta{color:#64748b;font-size:14px}
.lede{color:#94a3b8;font-size:18px}.toc{columns:2;column-gap:40px}.toc ol{list-style:none;padding-left:0;margin:4px 0 14px}.toc li.tp{break-inside:avoid}.toc>li>a{font-weight:600;color:#c4b5fd;text-decoration:none}
.toc ol a{color:#cbd5e1;text-decoration:none;font-size:14px}.toc ol span{color:#64748b;display:inline-block;width:2.2em}
section.part{border-top:2px solid #6d28d9;margin-top:56px;padding-top:24px}.partno{color:#a78bfa;letter-spacing:.1em;text-transform:uppercase;font-size:13px}
.parttitle{font-size:34px;margin:4px 0;color:#e2e8f0}
section.chapter{border-top:1px solid #1e293b;padding-top:18px;margin-top:32px}.chapno{color:#64748b;font-size:12px;letter-spacing:.08em;text-transform:uppercase}
h2{color:#e2e8f0;font-size:26px;margin:2px 0 6px}h3{color:#c4b5fd;font-size:18px;margin:22px 0 6px}h4{color:#7dd3fc;font-size:15px;margin:16px 0 4px}
strong{color:#f1f5f9}.where{font-size:13px;color:#64748b}.ref{color:#64748b;font-size:.85em}
figure{margin:20px 0}figure svg{width:100%;height:auto;display:block}figcaption{text-align:center;color:#94a3b8;font-size:13px;margin-top:6px}
aside{border-left:4px solid #fbbf24;background:#1f1705;color:#fde68a;padding:10px 14px;border-radius:0 8px 8px 0;margin:14px 0}
table{border-collapse:collapse;width:100%;margin:12px 0;font-size:14px}th{text-align:left;color:#94a3b8;font-weight:600;border-bottom:1px solid #334155;padding:6px 10px 6px 0}td{border-bottom:1px solid #1e293b;padding:6px 10px 6px 0;vertical-align:top}
footer{margin-top:60px;color:#64748b;font-size:13px}
@media (max-width:900px){nav{position:static;width:auto;height:auto;max-height:40vh}main{margin:0;padding:20px}.toc{columns:1}}
@media print{:root{color-scheme:light}body{background:#fff;color:#1e293b;font-size:10.5pt}nav{display:none}main{margin:0;padding:0;max-width:none}
.title{min-height:9in;break-after:page}.title h1{color:#0c4a6e;-webkit-text-fill-color:#0c4a6e}.title .sub{color:#0f172a}
.contents{break-after:page}.toc ol a,.toc>li>a{color:#0f172a}section.part{break-before:page;border-top-color:#6d28d9}.parttitle{color:#0f172a}
h2{color:#0c4a6e}h3{color:#5b21b6}h4{color:#0369a1}strong{color:#0f172a}code{background:#f1f5f9;color:#9a3412}pre{background:#f8fafc;color:#14532d;border-color:#e2e8f0;white-space:pre-wrap}
a{color:#0369a1;text-decoration:none}aside{background:#fffbeb;color:#78350f}th{color:#334155;border-color:#cbd5e1}td{border-color:#e2e8f0}figcaption{color:#475569}
section.chapter{break-inside:auto}h2,h3,h4{break-after:avoid}figure,table,aside,pre{break-inside:avoid}}
</style></head><body>
<nav aria-label="Contents"><div class="brand">${logo}<b>BitVoice</b></div><div class="where">Manual</div>
<input type="search" placeholder="Find a topic" aria-label="Find a topic" oninput="const q=this.value.toLowerCase();document.querySelectorAll('nav a[data-k]').forEach(a=>a.style.display=a.dataset.k.includes(q)?'':'none')">
${parts.map((p) => `<div class="np">${esc(p.title)}</div>` + p.ids.map((id) => { const a = byId[id]; return `<a href="#${a.id}" data-k="${esc((a.title + ' ' + a.keys).toLowerCase())}">${number[id]}. ${esc(a.title)}</a>` }).join('')).join('\n')}</nav>
<main>
<section class="title">${bigLogo}<h1>BitVoice manual</h1><div class="sub">The phone system, conference rooms and staff portal, hosted organisations, the 911 profile, the radio dispatch console and NG911 Core Services, all set up in the web interface.</div>
<div class="meta">${order.length} chapters · ${figureNo || '—'} figures · ${Math.round(words / 1000)} thousand words · BioDream LLC</div></section>
<section class="contents"><h2>Contents</h2><ol class="toc">${toc}</ol></section>
${chapters}
<footer>BitVoice is a product of BioDream LLC, published under the Apache License 2.0. The same chapters are under <strong>Help</strong> in every BitVoice.</footer></main></body></html>`

// The figure count on the title page is known only once the chapters are drawn.
writeFileSync(process.argv[3], html.replace(/<div class="meta">\d+ chapters · [^·]+ figures/, `<div class="meta">${order.length} chapters · ${figureNo} figures`))
const b = await chromium.launch(); const p = await b.newPage()
await p.goto('file://' + process.argv[3])
await p.pdf({ path: process.argv[4], format: 'Letter', printBackground: true, displayHeaderFooter: true, headerTemplate: '<span></span>',
  footerTemplate: '<div style="font-size:8px;width:100%;text-align:center;color:#64748b">BitVoice manual · BioDream LLC · <span class="pageNumber"></span> / <span class="totalPages"></span></div>',
  margin: { top: '0.6in', bottom: '0.7in', left: '0.7in', right: '0.7in' } })
await b.close()
console.log(order.length, 'chapters,', figureNo, 'figures,', words, 'words')
