// Shared building blocks: coloured entity chips, status badges, page frames, form fields, tables.
// Colour carries meaning everywhere (the Perfuse way): each kind of thing has one colour wherever it
// appears, and every coloured item also has an icon or text so colour is never the only signal.
import { clsx } from 'clsx'
import {
  AlertTriangle, ArrowDownLeft, ArrowUpRight, ArrowLeftRight, CheckCircle2, CircleDot, Hash, Info,
  CircleHelp, ListOrdered, Phone, PhoneCall, Server, ShieldAlert, Siren, User, Users, X, XCircle, Loader2, type LucideIcon,
} from 'lucide-react'
import { useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import { Link, useLocation } from 'react-router-dom'
import type { Problem } from './api'
import { articleFor } from './manual'

export const kinds = {
  extension: { icon: Phone, text: 'text-sky-300', bg: 'bg-sky-950/60 border-sky-800/60' },
  ringgroup: { icon: Users, text: 'text-violet-300', bg: 'bg-violet-950/60 border-violet-800/60' },
  queue: { icon: ListOrdered, text: 'text-orange-300', bg: 'bg-orange-950/60 border-orange-800/60' },
  trunk: { icon: Server, text: 'text-teal-300', bg: 'bg-teal-950/60 border-teal-800/60' },
  number: { icon: Hash, text: 'text-amber-300', bg: 'bg-amber-950/60 border-amber-800/60' },
  user: { icon: User, text: 'text-fuchsia-300', bg: 'bg-fuchsia-950/60 border-fuchsia-800/60' },
  emergency: { icon: Siren, text: 'text-rose-300', bg: 'bg-rose-950/60 border-rose-800/60' },
} satisfies Record<string, { icon: LucideIcon; text: string; bg: string }>

export type Kind = keyof typeof kinds

/** A coloured, iconed reference to a thing: an extension, a trunk, a phone number. */
export function Chip({ kind, children, title }: { kind: Kind; children: ReactNode; title?: string }) {
  const k = kinds[kind]
  const Icon = k.icon
  return (
    <span title={title} className={clsx('inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-xs font-semibold', k.text, k.bg)}>
      <Icon size={12} aria-hidden /> {children}
    </span>
  )
}

const statuses = {
  ok: { icon: CheckCircle2, cls: 'text-emerald-300 bg-emerald-950/60 border-emerald-800/60' },
  warn: { icon: AlertTriangle, cls: 'text-amber-300 bg-amber-950/60 border-amber-800/60' },
  fail: { icon: XCircle, cls: 'text-rose-300 bg-rose-950/60 border-rose-800/60' },
  info: { icon: Info, cls: 'text-sky-300 bg-sky-950/60 border-sky-800/60' },
  idle: { icon: CircleDot, cls: 'text-slate-400 bg-slate-900 border-slate-700' },
}
export type Status = keyof typeof statuses

export function Badge({ status, children }: { status: Status; children: ReactNode }) {
  const s = statuses[status]
  const Icon = s.icon
  return (
    <span className={clsx('inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-semibold', s.cls)}>
      <Icon size={12} aria-hidden /> {children}
    </span>
  )
}

export function directionIcon(d: string) {
  switch (d) {
    case 'inbound': return { icon: ArrowDownLeft, cls: 'text-emerald-300', label: 'Inbound' }
    case 'outbound': return { icon: ArrowUpRight, cls: 'text-sky-300', label: 'Outbound' }
    case 'emergency': return { icon: Siren, cls: 'text-rose-300', label: 'Emergency' }
    case 'test': return { icon: PhoneCall, cls: 'text-violet-300', label: 'Test' }
    default: return { icon: ArrowLeftRight, cls: 'text-slate-300', label: 'Internal' }
  }
}

export function Direction({ d }: { d: string }) {
  const x = directionIcon(d)
  const Icon = x.icon
  return <span className={clsx('inline-flex items-center gap-1 text-xs font-semibold', x.cls)}><Icon size={14} aria-hidden />{x.label}</span>
}

/** A page with a gradient-iconed title, a one-line explanation and actions. */
/** Opens this screen's page of the manual, when it has one. */
function HelpLink() {
  const { pathname } = useLocation()
  const a = articleFor(pathname)
  if (!a || pathname === '/help') return null
  return (
    <Link to={`/help?a=${a.id}`} className="btn text-slate-300" aria-label={`Help: ${a.title}`} title={`Help: ${a.title}`}>
      <CircleHelp size={15} className="text-sky-300" aria-hidden /> Help
    </Link>
  )
}

export function Page({ icon: Icon, title, intro, actions, children, accent = 'from-sky-400 to-violet-400' }: {
  icon: LucideIcon; title: string; intro?: ReactNode; actions?: ReactNode; children: ReactNode; accent?: string
}) {
  return (
    <div className="rise mx-auto max-w-7xl p-6">
      <header className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          <span className={clsx('grid h-11 w-11 place-items-center rounded-xl bg-gradient-to-br text-slate-950 shadow-lg', accent)}>
            <Icon size={22} aria-hidden />
          </span>
          <div>
            <h1 className="text-2xl font-bold text-slate-50">{title}</h1>
            {intro && <p className="mt-1 max-w-2xl text-sm text-slate-400">{intro}</p>}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {actions}
          <HelpLink />
        </div>
      </header>
      {children}
    </div>
  )
}

export function Field({ label, hint, error, children, htmlFor }: {
  label: string; hint?: ReactNode; error?: string; children: ReactNode; htmlFor?: string
}) {
  return (
    <div className={error ? 'field-error' : undefined}>
      <label className="label" htmlFor={htmlFor}>{label}</label>
      {children}
      {error ? <p className="mt-1 text-xs font-medium text-rose-300" role="alert">{error}</p>
        : hint ? <p className="mt-1 text-xs text-slate-500">{hint}</p> : null}
    </div>
  )
}

/** Maps API field problems ("extensions[3].number") onto form fields ("number"). */
export function problemFor(problems: Problem[] | undefined, field: string): string | undefined {
  // Problems on other items the save touched (other:menus[0].number) never mark a field of this editor.
  const own = problems?.filter((p) => !p.field.startsWith('other:'))
  return own?.find((p) => p.field === field || p.field.endsWith('.' + field))?.message
    // A list field (patterns) also shows the first problem of any of its items (patterns[2]).
    ?? own?.find((p) => p.field.startsWith(field + '[') || p.field.includes('.' + field + '['))?.message
}

export function ErrorBox({ message, problems, fields }: { message?: string; problems?: Problem[]; fields?: boolean }) {
  if (!message) return null
  return (
    <div role="alert" className="flex gap-2 rounded-lg border border-rose-800/60 bg-rose-950/60 p-3 text-sm text-rose-200">
      <ShieldAlert size={18} className="mt-0.5 shrink-0" aria-hidden />
      <div>
        <p className="font-semibold">{message}</p>
        {problems && problems.length > 0 && (
          <ul className="mt-1 list-disc pl-4 text-rose-300">{problems.map((p, i) => {
            const other = p.field.startsWith('other:')
            return <li key={p.field + i}>{(fields || other) && <span className="mr-2 font-mono text-xs text-sky-300">{other ? p.field.slice(6) : p.field}</span>}{p.message}</li>
          })}</ul>
        )}
      </div>
    </div>
  )
}

export function Empty({ icon: Icon, title, children }: { icon: LucideIcon; title: string; children?: ReactNode }) {
  return (
    <div className="card flex flex-col items-center gap-3 p-10 text-center">
      <span className="grid h-14 w-14 place-items-center rounded-2xl bg-slate-800 text-sky-300"><Icon size={28} aria-hidden /></span>
      <h2 className="text-lg font-semibold text-slate-100">{title}</h2>
      {children && <div className="max-w-md text-sm text-slate-400">{children}</div>}
    </div>
  )
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return <span className="inline-flex items-center gap-2 text-sm text-slate-400"><Loader2 size={16} className="animate-spin" aria-hidden />{label}</span>
}

export function Toggle({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button type="button" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}
      className={clsx('relative h-6 w-11 shrink-0 rounded-full border transition-colors',
        checked ? 'border-emerald-500 bg-emerald-500/80' : 'border-slate-600 bg-slate-800')}>
      <span className={clsx('absolute top-0.5 h-4.5 w-4.5 rounded-full bg-white shadow transition-all', checked ? 'left-5.5' : 'left-0.5')} />
    </button>
  )
}

export function formatDuration(seconds: number) {
  const s = Math.max(0, Math.floor(seconds))
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(r).padStart(2, '0')}` : `${m}:${String(r).padStart(2, '0')}`
}

export function relTime(iso?: string) {
  if (!iso) return ''
  const d = (Date.now() - new Date(iso).getTime()) / 1000
  if (d < 60) return 'just now'
  if (d < 3600) return `${Math.floor(d / 60)} min ago`
  if (d < 86400) return `${Math.floor(d / 3600)} h ago`
  return new Date(iso).toLocaleDateString()
}

export function mosStatus(mos: number): Status {
  if (!mos) return 'idle'
  if (mos >= 4) return 'ok'
  if (mos >= 3.5) return 'warn'
  return 'fail'
}

/** Says that someone else saved this page while it was being edited. */
export function StaleBanner({ onReload }: { onReload: () => void }) {
  return (
    <div role="status" className="mb-4 flex items-center gap-3 rounded-lg border border-amber-700/60 bg-amber-950/40 p-3 text-sm text-amber-200">
      <ShieldAlert size={16} className="shrink-0" aria-hidden />
      <span>These settings were changed somewhere else while you were editing. Saving now would overwrite that change.</span>
      <button type="button" className="btn ml-auto shrink-0" onClick={onReload}>Load the new version</button>
    </div>
  )
}

/** Arrow keys for a role="radiogroup" of buttons, as the ARIA pattern expects: move and choose together.
 * Spread onto each radio with its index: {...radioKeys(i, ids, set)}. Only the chosen one is tabbable. */
export function radioKeys<T>(i: number, ids: readonly T[], current: T, set: (v: T) => void) {
  return {
    tabIndex: ids[i] === current ? 0 : -1,
    onKeyDown: (e: ReactKeyboardEvent<HTMLElement>) => {
      const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
      if (!step) return
      e.preventDefault()
      const j = (i + step + ids.length) % ids.length
      set(ids[j])
      const group = (e.currentTarget as HTMLElement).closest('[role=radiogroup]')
      ;(group?.querySelectorAll<HTMLElement>('[role=radio]')[j])?.focus()
    },
  }
}

/**
 * A list of short values shown as chips: type one and press Enter (or a comma) to add it, Backspace in the
 * empty box removes the last, and pasting several lines adds them all. Values are trimmed and kept unique.
 */
export function ListInput({ id, value, onChange, placeholder, mono, invalid }: {
  id: string; value: string[]; onChange: (v: string[]) => void; placeholder?: string; mono?: boolean; invalid?: boolean
}) {
  const [draft, setDraft] = useState('')
  const add = (raw: string) => {
    const items = raw.split(/[\n,]/).map((s) => s.trim()).filter(Boolean)
    if (!items.length) return
    onChange([...value, ...items.filter((x, i) => !value.includes(x) && items.indexOf(x) === i)])
    setDraft('')
  }
  return (
    <div className={clsx('input flex min-h-10 flex-wrap items-center gap-1.5 py-1.5', invalid && 'border-rose-700')}
      onClick={(e) => (e.currentTarget.querySelector('input') as HTMLInputElement | null)?.focus()}>
      {value.map((v) => (
        <span key={v} className={clsx('inline-flex items-center gap-1 rounded-md bg-slate-800 px-2 py-0.5 text-sm text-slate-200', mono && 'font-mono')}>
          {v}
          <button type="button" className="text-slate-400 hover:text-rose-300" aria-label={`Remove ${v}`} onClick={() => onChange(value.filter((x) => x !== v))}><X size={13} aria-hidden /></button>
        </span>
      ))}
      <input id={id} className={clsx('min-w-32 flex-1 bg-transparent outline-none', mono && 'font-mono')} value={draft} placeholder={value.length ? undefined : placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); add(draft) }
          if (e.key === 'Backspace' && !draft && value.length) onChange(value.slice(0, -1))
        }}
        onPaste={(e) => { const t = e.clipboardData.getData('text'); if (/[\n,]/.test(t)) { e.preventDefault(); add(draft + t) } }}
        onBlur={() => add(draft)} />
    </div>
  )
}
