import { FindingList } from './Judges'
import { useRevisionCheckQuery } from './api'
import { ChevronDown, ChevronRight, RotateCcw } from 'lucide-react'
import { useState } from 'react'
import type { ApiErrorBody } from './api'
import { ErrorBox } from './ui'

export interface Change { path: string; before: string; after: string }

async function diff(rev: number, against: 'current' | 'previous'): Promise<Change[]> {
  const r = await fetch(`./api/history/${rev}/diff?against=${against}`, { headers: { 'X-BitVoice': '1' } })
  if (!r.ok) throw new Error((await r.json().catch(() => ({}))).message ?? `HTTP ${r.status}`)
  return (await r.json()).items ?? []
}

/** A list of setting changes: path, then before → after, in meaningful colours. */
export function ChangeList({ items }: { items: Change[] }) {
  if (!items.length) return <p className="text-sm text-slate-500">No setting differs.</p>
  return (
    <ul className="max-h-80 space-y-1 overflow-auto font-mono text-xs">
      {items.map((c) => (
        <li key={c.path} className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)] gap-3">
          <span className="truncate text-sky-300" title={c.path}>{c.path}</span>
          <span className="truncate">
            {c.before ? <span className="text-rose-300 line-through decoration-rose-500/60">{c.before}</span> : <span className="text-slate-600">(none)</span>}
            <span className="px-1.5 text-slate-500" aria-hidden>→</span><span className="sr-only">becomes</span>
            {c.after ? <span className="text-emerald-300">{c.after}</span> : <span className="text-slate-600">(removed)</span>}
          </span>
        </li>
      ))}
    </ul>
  )
}

/** "What changed" for one revision, loaded when opened. */
export function WhatChanged({ rev }: { rev: number }) {
  const [open, setOpen] = useState(false)
  const [items, setItems] = useState<Change[] | null>(null)
  const [error, setError] = useState('')
  const toggle = async () => {
    setOpen(!open)
    if (!open && !items) diff(rev, 'previous').then(setItems, (e) => setError((e as Error).message))
  }
  return (
    <div className="mt-2">
      <button type="button" className="flex items-center gap-1 text-xs text-slate-400 hover:text-slate-200" aria-expanded={open} onClick={toggle}>
        {open ? <ChevronDown size={14} aria-hidden /> : <ChevronRight size={14} aria-hidden />} What changed
      </button>
      {open && <div className="mt-2">{error ? <p className="text-sm text-rose-300">{error}</p> : items ? <ChangeList items={items} /> : <p className="text-sm text-slate-500">Loading…</p>}</div>}
    </div>
  )
}

/** Restore with a look first: the button shows what would change and asks before replacing anything. */
export function RestoreButton({ rev, onRestore }: { rev: number; onRestore: () => Promise<ApiErrorBody | null> }) {
  const [items, setItems] = useState<Change[] | null>(null)
  const [error, setError] = useState('')
  const [refused, setRefused] = useState<ApiErrorBody | null>(null)
  if (!items) {
    return (
      <span className="ml-auto flex items-center gap-2">
        {error && <span className="text-xs text-rose-300">{error}</span>}
        <button type="button" className="btn" aria-label={`Restore revision ${rev}`}
          onClick={() => diff(rev, 'current').then(setItems, (e) => setError((e as Error).message))}>
          <RotateCcw size={14} className="text-amber-300" aria-hidden /> Restore this
        </button>
      </span>
    )
  }
  return (
    <div className="mt-3 w-full rounded-lg border border-amber-700/50 bg-amber-950/30 p-3" role="group" aria-label={`Restore revision ${rev}`}>
      <p className="mb-2 text-sm text-amber-200">Restoring revision {rev} makes {items.length} change{items.length === 1 ? '' : 's'} to the system as it is now:</p>
      <ChangeList items={items} />
      <RestoreCheck rev={rev} />
      {refused && <div className="mt-3"><ErrorBox message={`Revision ${rev} cannot be restored as it is: ${refused.message}`} problems={refused.problems} fields /></div>}
      <div className="mt-3 flex gap-2">
        <button type="button" className="btn btn-primary" disabled={!items.length} onClick={async () => { const e = await onRestore(); setRefused(e); if (!e) setItems(null) }}>
          <RotateCcw size={14} aria-hidden /> Yes, restore revision {rev}
        </button>
        <button type="button" className="btn" onClick={() => { setItems(null); setRefused(null) }}>Cancel</button>
      </div>
    </div>
  )
}

/** RestoreCheck says what restoring a revision would break, checked in every situation. */
function RestoreCheck({ rev }: { rev: number }) {
  const { data } = useRevisionCheckQuery(rev)
  if (!data?.items.length) return null
  return (
    <div className="mt-3 space-y-2" role="alert">
      <p className="text-sm font-semibold text-amber-300">Checked in every situation, restoring it would cause:</p>
      <FindingList items={data.items} />
    </div>
  )
}
