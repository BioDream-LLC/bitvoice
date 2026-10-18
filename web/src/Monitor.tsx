import { Reasons } from './Judges'
import { CDRCsvFiles } from './Outputs'
import { createColumnHelper } from '@tanstack/react-table'
import { ArrowRight, BookOpen, Disc, Download, Gauge, Lock, Stethoscope, Waypoints } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useCdrQuery, useHealthQuery, useTraceQuery, type CDR, type TraceEntry } from './api'
import { Chart } from './Chart'
import { TranscriptButton, TranscriptSearch } from './Transcripts'
import { Table, useIsAdmin } from './Editor'
import { AttestBadge, BlockButton } from './Screening'
import { TimelineLink } from './Timeline'
import { Badge, Direction, Empty, Page, formatDuration, mosStatus, type Status } from './ui'

const col = createColumnHelper<CDR>()
const dispo: Record<string, [Status, string]> = {
  answered: ['ok', 'Answered'], 'no-answer': ['warn', 'No answer'], busy: ['warn', 'Busy'],
  cancelled: ['idle', 'Caller hung up'], failed: ['fail', 'Failed'], blocked: ['fail', 'Blocked'],
}

export function History() {
  const admin = useIsAdmin()
  const [direction, setDirection] = useState('')
  const { data } = useCdrQuery(direction ? { direction } : {})
  const items = data?.items ?? []
  const pie = useMemo(() => {
    const c: Record<string, number> = {}
    for (const r of items) c[r.disposition] = (c[r.disposition] ?? 0) + 1
    return Object.entries(c).map(([k, v]) => ({ name: dispo[k]?.[1] ?? k, value: v }))
  }, [items])
  const columns = [
    col.accessor('start', { header: 'When', cell: (c) => <span className="whitespace-nowrap text-xs text-slate-300">{new Date(c.getValue()).toLocaleString()}</span> }),
    col.accessor('direction', { header: 'Type', cell: (c) => <Direction d={c.getValue()} /> }),
    col.accessor('from', { header: 'From', cell: (c) => <span className="inline-flex flex-wrap items-center gap-1"><span className="font-mono text-sky-300">{c.getValue()}</span> <span className="text-xs text-slate-500">{c.row.original.fromName}</span>
      <AttestBadge attest={c.row.original.attest} verified={c.row.original.verified} />{admin && c.row.original.direction === 'inbound' && <BlockButton number={c.getValue()} />}{c.row.original.direction === 'inbound' && <TimelineLink number={c.getValue()} />}</span> }),
    col.accessor('to', { header: 'To', cell: (c) => <span><span className="font-mono text-amber-300">{c.getValue()}</span> {c.row.original.signed && <span className="rounded bg-emerald-500/15 px-1 text-xs text-emerald-300" title="Caller ID signed by this system (STIR/SHAKEN)">signed {c.row.original.signed}</span>} {(() => { const n = c.row.original.answeredBy || c.row.original.toName; return n && n !== c.getValue() ? <span className="text-xs text-slate-500">{n}</span> : null })()}</span> }),
    col.accessor('disposition', { header: 'Result', cell: (c) => {
      const d = dispo[c.getValue()] ?? ['idle', c.getValue()]
      const cause = c.row.original.cause
      return <span className="flex flex-col items-start gap-0.5"><Badge status={d[0]}>{d[1]}</Badge>{c.getValue() !== 'answered' && cause && <span className="text-xs text-slate-500">{cause}</span>}<Reasons items={c.row.original.why} label="How it was routed" /></span>
    } }),
    col.accessor('billsec', { header: 'Talk time', cell: (c) => <span className="flex flex-col items-start gap-0.5"><span className="font-mono text-xs tabular-nums text-emerald-300">{c.row.original.answer ? formatDuration(c.getValue()) : '—'}</span>
      {c.row.original.charged && <span className="text-xs text-amber-200" title="What the phone company said this call cost (advice of charge)">charged {c.row.original.charged}</span>}</span> }),
    col.accessor('recording', { header: () => <span className="sr-only">Recording</span>, enableSorting: false, cell: (c) => {
      const r = c.row.original
      const paused = r.paused?.map((s) => `${formatDuration(s.from)}–${formatDuration(s.to)}`).join(', ')
      return (
        <span className="inline-flex items-center gap-1.5">
          {r.held && <span title="Under a legal hold: kept and cannot be deleted" className="text-amber-300"><Lock size={13} aria-hidden /><span className="sr-only">Under a legal hold</span></span>}
          {c.getValue() && <RecordingButton name={c.getValue()!} />}
          {c.getValue() && <TranscriptButton name={c.getValue()!} label={`${r.fromName || r.from} to ${r.answeredBy || r.toName || r.to}`} />}
          {paused && <span className="text-xs text-amber-300" title={`Recording paused ${paused}`}>paused {paused}</span>}
        </span>
      )
    } }),
    col.accessor('mos', { header: 'Quality', cell: (c) => c.getValue() ? <Badge status={mosStatus(c.getValue())}><Gauge size={11} aria-hidden />{c.getValue().toFixed(1)}</Badge> : null }),
  ]
  const csv = () => {
    const head = 'start,direction,from,fromName,to,answeredBy,disposition,talkSeconds,trunk,cause\n'
    // Caller names come from outside callers: a leading = + - @ would run as a formula in a spreadsheet.
    const esc = (s: unknown) => { let v = String(s ?? ''); if (/^[=+\-@\t\r]/.test(v)) v = "'" + v; return `"${v.replace(/"/g, '""')}"` }
    const body = items.map((r) => [r.start, r.direction, r.from, r.fromName, r.to, r.answeredBy, r.disposition, r.billsec, r.trunk, r.cause].map(esc).join(',')).join('\n')
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([head + body], { type: 'text/csv' }))
    a.download = 'bitvoice-calls.csv'
    a.click()
    setTimeout(() => URL.revokeObjectURL(a.href), 10000)
  }
  return (
    <Page icon={BookOpen} title="Call history" accent="from-fuchsia-400 to-violet-400" intro="Every call, with who answered, how long and how it sounded."
      actions={<>
        <select className="input w-40" aria-label="Call type" value={direction} onChange={(e) => setDirection(e.target.value)}>
          <option value="">All calls</option><option value="inbound">Inbound</option><option value="outbound">Outbound</option>
          <option value="internal">Internal</option><option value="emergency">Emergency</option>
        </select>
        <button className="btn" onClick={csv} disabled={!items.length}><Download size={15} className="text-emerald-300" aria-hidden /> CSV</button>
      </>}>
      <TranscriptSearch />
      {items.length > 0 && (
        <div className="card mb-6 p-4">
          <Chart height={180} label="Calls by result" option={{
            tooltip: { trigger: 'item' }, legend: { orient: 'vertical', right: 10, top: 'center', textStyle: { color: 'inherit' } },
            color: ['#34d399', '#fbbf24', '#fb923c', '#94a3b8', '#fb7185'],
            series: [{ type: 'pie', radius: ['55%', '80%'], center: ['30%', '50%'], label: { show: false }, data: pie, itemStyle: { borderColor: 'transparent', borderWidth: 2 } }],
          }} />
        </div>
      )}
      <Table data={items} columns={columns} empty={<Empty icon={BookOpen} title="No calls yet">Calls appear here as soon as they end.</Empty>} />
      <CDRCsvFiles />
    </Page>
  )
}

export function Health() {
  const { data } = useHealthQuery()
  return (
    <Page icon={Stethoscope} title="Health" accent="from-emerald-400 to-sky-400" intro="BitVoice checks itself and tells you in plain words what to fix.">
      <ul className="space-y-3">
        {data?.items.map((c) => (
          <li key={c.id} className="card flex items-start gap-4 p-4">
            <Badge status={c.status}>{c.status === 'ok' ? 'OK' : c.status === 'warn' ? 'Check' : 'Problem'}</Badge>
            <div className="flex-1">
              <div className="font-semibold text-slate-100">{c.title}</div>
              {c.detail && <div className="text-sm text-slate-400">{c.detail}</div>}
            </div>
            {c.fix && <Link to={c.fix} className="btn" aria-label={`Fix it: ${c.title}`}>Fix it <ArrowRight size={14} aria-hidden /></Link>}
          </li>
        ))}
      </ul>
    </Page>
  )
}

export function Trace() {
  const [q, setQ] = useState('')
  const [query, setQuery] = useState('')
  useEffect(() => { const t = setTimeout(() => setQuery(q.trim()), 250); return () => clearTimeout(t) }, [q])
  const [selKey, setSelKey] = useState('')
  const { data, refetch } = useTraceQuery(query)
  const items = data?.items ?? []
  const keyOf = (e: TraceEntry) => `${e.at}|${e.dir}|${e.callId}|${e.summary}`
  // Keep the chosen message across refreshes, which bring new objects.
  const sel = items.find((e) => keyOf(e) === selKey) ?? null
  // Ladder for the selected call: every message with the same Call-ID, oldest first.
  const ladder = useMemo(() => sel ? items.filter((e) => e.callId === sel.callId).reverse() : [], [sel, items])
  return (
    <Page icon={Waypoints} title="SIP trace" accent="from-orange-400 to-amber-400"
      intro="The most recent SIP messages, for working out why a call or phone misbehaves. Passwords are hidden."
      actions={<>
        <input className="input w-64" placeholder="Filter by number, address, Call-ID…" aria-label="Filter trace" value={q} onChange={(e) => setQ(e.target.value)} />
        <button className="btn" onClick={() => refetch()}>Refresh</button>
      </>}>
      {!items.length ? (query
        ? <Empty icon={Waypoints} title={`No message matches “${query}”`}>Try part of a number, an address or a Call-ID.</Empty>
        : <Empty icon={Waypoints} title="No messages captured yet">Make a call or register a phone, then refresh.</Empty>) : (
        <div className="grid gap-4 lg:grid-cols-[1fr_1.2fr]">
          <ul className="card max-h-[70vh] divide-y divide-slate-800/70 overflow-auto">
            {items.map((e, i) => (
              <li key={i}>
                <button className={`flex w-full items-center gap-2 px-3 py-2 text-left text-xs hover:bg-slate-900 ${sel && keyOf(sel) === keyOf(e) ? 'bg-slate-900' : ''}`} aria-pressed={!!sel && keyOf(sel) === keyOf(e)} onClick={() => setSelKey(keyOf(e))}>
                  <span className={e.dir === 'in' ? 'text-emerald-300' : 'text-sky-300'} aria-hidden>{e.dir === 'in' ? '←' : '→'}</span>
                  <span className="sr-only">{e.dir === 'in' ? 'Received' : 'Sent'}</span>
                  <span className={`font-semibold ${/^[45]/.test(e.summary) ? 'text-rose-300' : /^[12]/.test(e.summary) ? 'text-emerald-300' : 'text-amber-300'}`}>{e.summary}</span>
                  <span className="ml-auto font-mono text-slate-500">{e.peer}</span>
                  <span className="font-mono text-slate-600">{new Date(e.at).toLocaleTimeString()}</span>
                </button>
              </li>
            ))}
          </ul>
          <div className="space-y-4">
            {sel ? (
              <>
                <div className="card p-4">
                  <h2 className="label">Call flow</h2>
                  <ol className="space-y-1 font-mono text-xs">
                    {ladder.map((e, i) => (
                      <li key={i} className={`flex items-center gap-2 ${e.dir === 'in' ? '' : 'flex-row-reverse text-right'}`}>
                        <span className="w-28 truncate text-slate-500">{e.peer}</span>
                        <span className={`flex-1 border-b ${e.dir === 'in' ? 'border-emerald-700' : 'border-sky-700'} pb-0.5 ${/^[45]/.test(e.summary) ? 'text-rose-300' : 'text-slate-200'}`}>
                          {e.dir === 'in' ? '' : '◀ '}{e.summary}{e.dir === 'in' ? ' ▶' : ''}
                        </span>
                        <span className="w-16 text-slate-500">BitVoice</span>
                      </li>
                    ))}
                  </ol>
                </div>
                <pre className="card max-h-[50vh] overflow-auto p-4 font-mono text-xs leading-relaxed text-slate-300">{colourSip(sel.message)}</pre>
              </>
            ) : <p className="text-sm text-slate-500">Choose a message to see it and its call flow.</p>}
          </div>
        </div>
      )}
    </Page>
  )
}

export function RecordingButton({ name }: { name: string }) {
  const [open, setOpen] = useState(false)
  if (open) return <audio autoPlay controls className="h-8 w-56" src={`./api/recordings/${name}`} aria-label="Call recording" onClick={(e) => e.stopPropagation()} />
  return (
    <button className="btn px-2 py-1" aria-label="Play recording" title="Play recording" onClick={(e) => { e.stopPropagation(); setOpen(true) }}>
      <Disc size={14} className="text-rose-300" aria-hidden />
    </button>
  )
}

function colourSip(text: string) {
  return text.split('\r\n').map((line, i) => {
    if (i === 0) return <div key={i} className="font-bold text-amber-300">{line}</div>
    const c = line.indexOf(':')
    if (c > 0 && /^[A-Za-z-]+$/.test(line.slice(0, c))) {
      return <div key={i}><span className="text-sky-300">{line.slice(0, c)}</span>:<span className="text-slate-200">{line.slice(c + 1)}</span></div>
    }
    return <div key={i} className="text-violet-300">{line || '\u00a0'}</div>
  })
}
