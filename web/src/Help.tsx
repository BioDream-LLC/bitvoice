import { BookOpenText, Search } from 'lucide-react'
import { Fragment, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { manual, type Article } from './manual'
import { Page } from './ui'

/** Inline markdown: `code`, **bold** and [text](#/route) links. Everything else is plain text. */
function inline(s: string, key = 0): ReactNode[] {
  const out: ReactNode[] = []
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)]+\))/g
  let last = 0
  for (const m of s.matchAll(re)) {
    if (m.index! > last) out.push(s.slice(last, m.index))
    const t = m[0]
    if (t.startsWith('`')) out.push(<code key={key++} className="rounded bg-slate-800 px-1 font-mono text-[0.9em] text-amber-200">{t.slice(1, -1)}</code>)
    else if (t.startsWith('**')) out.push(<strong key={key++} className="font-semibold text-slate-100">{t.slice(2, -2)}</strong>)
    else {
      const [, text, href] = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(t)!
      out.push(href.startsWith('#/')
        ? <Link key={key++} to={href.slice(1)} className="text-sky-300 underline underline-offset-2">{text}</Link>
        : <a key={key++} href={href} target="_blank" rel="noreferrer" className="text-sky-300 underline underline-offset-2">{text}</a>)
    }
    last = m.index! + t.length
  }
  if (last < s.length) out.push(s.slice(last))
  return out
}

/** Block markdown: ## headings, paragraphs, - and 1. lists, ``` code blocks. */
export function Markdown({ text }: { text: string }) {
  const blocks: ReactNode[] = []
  const lines = text.split('\n')
  for (let i = 0; i < lines.length;) {
    const l = lines[i]
    if (!l.trim()) { i++; continue }
    if (l.startsWith('```')) {
      const code: string[] = []
      for (i++; i < lines.length && !lines[i].startsWith('```'); i++) code.push(lines[i])
      i++
      blocks.push(<pre key={i} className="overflow-x-auto rounded-lg bg-slate-950 p-3 font-mono text-xs text-emerald-200">{code.join('\n')}</pre>)
      continue
    }
    if (l.startsWith('## ')) { blocks.push(<h3 key={i} className="pt-2 text-sm font-semibold text-violet-300">{inline(l.slice(3))}</h3>); i++; continue }
    const bullet = /^(- |\d+\. )/
    if (bullet.test(l)) {
      const ordered = /^\d/.test(l)
      const items: string[] = []
      for (; i < lines.length && bullet.test(lines[i]); i++) items.push(lines[i].replace(bullet, ''))
      const List = ordered ? 'ol' : 'ul'
      blocks.push(<List key={i} className={`${ordered ? 'list-decimal' : 'list-disc'} space-y-1 pl-5`}>{items.map((it, j) => <li key={j}>{inline(it)}</li>)}</List>)
      continue
    }
    const para: string[] = []
    for (; i < lines.length && lines[i].trim() && !bullet.test(lines[i]) && !lines[i].startsWith('## ') && !lines[i].startsWith('```'); i++) para.push(lines[i])
    blocks.push(<p key={i}>{inline(para.join(' '))}</p>)
  }
  return <div className="space-y-3 text-sm leading-relaxed text-slate-300">{blocks.map((b, i) => <Fragment key={i}>{b}</Fragment>)}</div>
}

const plain = (a: Article) => `${a.title} ${a.keys} ${a.body}`.toLowerCase().replace(/[`*#[\]()]/g, ' ')

export function Help() {
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState('')
  const id = params.get('a') ?? 'start'
  const found = useMemo(() => {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean)
    if (!words.length) return manual
    return manual
      .map((a) => ({ a, score: words.every((w) => plain(a).includes(w)) ? words.reduce((s, w) => s + (a.title.toLowerCase().includes(w) ? 3 : a.keys.includes(w) ? 2 : 1), 0) : 0 }))
      .filter((x) => x.score > 0).sort((x, y) => y.score - x.score).map((x) => x.a)
  }, [q])
  const current = manual.find((a) => a.id === id) ?? manual[0]
  useEffect(() => { document.getElementById('help-article')?.focus() }, [id])
  return (
    <Page icon={BookOpenText} title="Help" accent="from-sky-300 to-emerald-300" intro="How BitVoice works, screen by screen. Every screen's Help link opens its page here.">
      <div className="grid gap-6 md:grid-cols-[16rem_1fr]">
        <nav aria-label="Manual" className="space-y-3">
          <label className="relative block">
            <span className="sr-only">Search the manual</span>
            <Search size={15} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-slate-500" aria-hidden />
            <input type="search" className="input pl-9" placeholder="Search the manual" value={q} onChange={(e) => setQ(e.target.value)} />
          </label>
          {found.length === 0 ? <p className="px-2 text-sm text-slate-500">Nothing matches “{q}”.</p> : (
            <ul className="space-y-0.5">
              {found.map((a) => (
                <li key={a.id}>
                  <button className={`w-full rounded-lg px-3 py-1.5 text-left text-sm ${a.id === current.id ? 'bg-slate-800 text-sky-200' : 'text-slate-300 hover:bg-slate-900'}`}
                    aria-current={a.id === current.id ? 'page' : undefined} onClick={() => setParams({ a: a.id })}>{a.title}</button>
                </li>
              ))}
            </ul>
          )}
        </nav>
        <article id="help-article" tabIndex={-1} className="card p-6 outline-none" aria-labelledby="help-title">
          <h2 id="help-title" className="mb-4 text-lg font-semibold text-slate-100">{current.title}</h2>
          <Markdown text={current.body} />
          {current.route && <p className="mt-6 text-sm"><Link to={current.route} className="text-sky-300 underline">Go to {current.title}</Link></p>}
        </article>
      </div>
    </Page>
  )
}
