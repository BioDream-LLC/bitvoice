import { clsx } from 'clsx'
import { BookUser, Delete, LogOut, Mic, MicOff, Phone, PhoneIncoming, PhoneOff, PhoneOutgoing } from 'lucide-react'
import { useEffect, useState } from 'react'
import { type CallerCard } from './api'
import { CallerCardView } from './Crm'
import { formatDuration } from './ui'
import { useNow } from './useNow'
import { usePhone, type PhoneCall } from './usePhone'

const keys = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '*', '0', '#']

/** The dialler: number, keypad, calls. Signs in as an extension first. */
export function Softphone({ phoneApi, extensions }: {
  phoneApi: ReturnType<typeof usePhone>
  extensions?: { number: string; name: string }[] // an administrator picks one; others type it with a password
}) {
  const { phone, calls, error, open, close, dial, answer, hangup, dtmf, mute } = phoneApi
  const [number, setNumber] = useState('')
  const [ext, setExt] = useState(extensions?.[0]?.number ?? '')
  const [pw, setPw] = useState('')
  const active = calls.find((c) => c.state !== 'ended' && c.state !== 'incoming')

  if (!phone) {
    return (
      <form className="space-y-3" onSubmit={(e) => { e.preventDefault(); open(ext, pw) }}>
        <p className="text-sm text-slate-400">Use this browser as a phone for an extension. Calls ring here as well as on its other phones.</p>
        {extensions ? (
          <select className="input" aria-label="Extension" value={ext} onChange={(e) => setExt(e.target.value)}>
            {extensions.map((x) => <option key={x.number} value={x.number}>{x.number} {x.name}</option>)}
          </select>
        ) : (
          <>
            <input className="input font-mono" aria-label="Extension" placeholder="Extension" inputMode="numeric" value={ext} onChange={(e) => setExt(e.target.value)} />
            <input className="input" type="password" aria-label="SIP password" placeholder="SIP password" value={pw} onChange={(e) => setPw(e.target.value)} />
          </>
        )}
        {error && <p className="text-sm text-rose-300">{error}</p>}
        <button className="btn btn-primary w-full justify-center" disabled={!ext}><Phone size={15} aria-hidden /> Sign in as {ext || '…'}</button>
      </form>
    )
  }
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-sm">
        <span className="live-dot" aria-hidden />
        <span className="text-slate-300">Extension <span className="font-mono text-sky-300">{phone.extension}</span> {phone.name}</span>
        <button className="btn ml-auto px-2 py-1" aria-label="Sign the browser phone out" title="Sign out" onClick={close}><LogOut size={13} aria-hidden /></button>
      </div>
      {error && <p className="text-xs text-amber-300">{error}</p>}
      {calls.map((c) => <CallCard key={c.id} c={c} phoneId={phone?.id} answer={answer} hangup={hangup} mute={mute} />)}
      <div className="flex gap-2">
        <input className="input flex-1 font-mono text-lg text-amber-300" aria-label="Number to call" placeholder="Number" value={number}
          onChange={(e) => setNumber(e.target.value.replace(/[^\d*#+]/g, ''))} onKeyDown={(e) => { if (e.key === 'Enter' && !active) dial(number) }} />
        <button className="btn px-2" aria-label="Delete a digit" onClick={() => setNumber((n) => n.slice(0, -1))}><Delete size={16} aria-hidden /></button>
      </div>
      <div className="grid grid-cols-3 gap-2" role="group" aria-label="Keypad">
        {keys.map((k) => (
          <button key={k} className="h-11 rounded-lg bg-slate-800 font-mono text-lg font-semibold text-slate-100 hover:bg-slate-700"
            onClick={() => (active?.state === 'connected' ? dtmf(active.id, k) : setNumber((n) => n + k))}>{k}</button>
        ))}
      </div>
      {!active && (
        <button className="btn w-full justify-center border-emerald-600 bg-emerald-600 py-2.5 text-white hover:bg-emerald-500" disabled={!number} onClick={() => dial(number)}>
          <Phone size={16} aria-hidden /> Call
        </button>
      )}
      {!active && <Directory phoneId={phone.id} onPick={(n) => setNumber(n)} />}
    </div>
  )
}

interface Entry { name: string; number: string; company?: string; group: string }

/** Directory: find someone in the company directory (extensions and contacts) and put their number in. */
function Directory({ phoneId, onPick }: { phoneId: string; onPick: (number: string) => void }) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const [items, setItems] = useState<Entry[] | null>(null)
  useEffect(() => {
    if (!open || items) return
    fetch(`./api/phone/${phoneId}/directory`, { headers: { 'X-BitVoice': '1' } }).then((r) => (r.ok ? r.json() : { items: [] })).then((j) => setItems(j.items)).catch(() => setItems([]))
  }, [open, items, phoneId])
  const w = q.trim().toLowerCase()
  const shown = (items ?? []).filter((e) => !w || `${e.name} ${e.company ?? ''} ${e.number}`.toLowerCase().includes(w)).slice(0, 50)
  if (!open) return <button className="btn w-full justify-center" onClick={() => setOpen(true)}><BookUser size={15} className="text-teal-300" aria-hidden /> Contacts</button>
  return (
    <div className="space-y-2" role="region" aria-label="Directory">
      <input type="search" className="input" aria-label="Find in the directory" placeholder="Find a name or number" autoFocus value={q} onChange={(e) => setQ(e.target.value)} />
      <ul className="max-h-56 space-y-1 overflow-y-auto">
        {items === null && <li className="text-sm text-slate-500">Loading…</li>}
        {items && shown.length === 0 && <li className="text-sm text-slate-500">Nobody matches.</li>}
        {shown.map((e, i) => (
          <li key={i}>
            <button className="flex w-full items-center justify-between gap-2 rounded px-2 py-1 text-left text-sm hover:bg-slate-800" onClick={() => { onPick(e.number); setOpen(false); setQ('') }}>
              <span className="truncate text-slate-200">{e.name}{e.company && <span className="text-xs text-slate-500"> {e.company}</span>}</span>
              <span className={clsx('font-mono text-xs', e.group === 'Extensions' ? 'text-sky-300' : 'text-emerald-300')}>{e.number}</span>
            </button>
          </li>
        ))}
      </ul>
      <button className="btn w-full justify-center" onClick={() => setOpen(false)}>Close the directory</button>
    </div>
  )
}

function CallCard({ c, phoneId, answer, hangup, mute }: {
  c: PhoneCall; phoneId?: string; answer: (id: string) => void; hangup: (id: string) => void; mute: (id: string, on: boolean) => void
}) {
  const now = useNow()
  const label = { incoming: 'Incoming call', calling: 'Calling…', ringing: 'Ringing…', connected: formatDuration(Math.round((now - c.since) / 1000)), ended: c.reason ?? 'Call ended' }[c.state]
  const Icon = c.direction === 'in' ? PhoneIncoming : PhoneOutgoing
  return (
    <div className={clsx('card p-3', c.state === 'incoming' && 'ringing border-emerald-600')}>
      <div className="flex items-center gap-2">
        <Icon size={16} className={c.state === 'ended' ? 'text-slate-500' : 'text-emerald-300'} aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-amber-300">{c.number}{c.name && <span className="ml-2 font-sans text-slate-300">{c.name}</span>}</div>
          <div className={clsx('text-xs', c.state === 'connected' ? 'font-mono tabular-nums text-emerald-300' : 'text-slate-400')}>{label}</div>
          {c.briefing && <p className="mt-1 rounded bg-violet-500/10 px-2 py-1 text-xs text-violet-200" role="note" aria-label="Briefing"><span className="font-semibold text-violet-300">Briefing: </span>{c.briefing}</p>}
        </div>
        {c.state === 'incoming' && (
          <button className="btn border-emerald-600 bg-emerald-600 text-white" onClick={() => answer(c.id)}><Phone size={14} aria-hidden /> Answer</button>
        )}
        {c.state === 'connected' && (
          <button className="btn px-2" aria-label={c.muted ? 'Unmute' : 'Mute'} aria-pressed={!!c.muted} onClick={() => mute(c.id, !c.muted)}>
            {c.muted ? <MicOff size={14} className="text-rose-300" aria-hidden /> : <Mic size={14} aria-hidden />}
          </button>
        )}
        {c.state !== 'ended' && (
          <button className="btn border-rose-700 bg-rose-700 text-white" aria-label={c.state === 'incoming' ? 'Decline' : 'Hang up'} onClick={() => hangup(c.id)}><PhoneOff size={14} aria-hidden /></button>
        )}
      </div>
      {c.direction === 'in' && c.state !== 'ended' && phoneId && <div className="mt-2"><PhoneCaller phoneId={phoneId} number={c.number} /></div>}
    </div>
  )
}

/** PhoneCaller is the caller pop-up on the browser phone, through the phone's own session. */
function PhoneCaller({ phoneId, number }: { phoneId: string; number: string }) {
  const [card, setCard] = useState<CallerCard | null>(null)
  useEffect(() => {
    if (!number) return
    let live = true
    fetch(`./api/phone/${phoneId}/caller?number=${encodeURIComponent(number)}`, { headers: { 'X-BitVoice': '1' } })
      .then((r) => (r.ok ? r.json() : null)).then((d) => { if (live) setCard(d) }).catch(() => {})
    return () => { live = false }
  }, [phoneId, number])
  return card ? <CallerCardView card={card} compact /> : null
}
