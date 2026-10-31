import { clsx } from 'clsx'
import { ArrowRightLeft, BookUser, Clock, Delete, LogOut, MessageSquareText, Mic, MicOff, Pause, Phone, PhoneIncoming, PhoneMissed, PhoneOff, PhoneOutgoing, Play, Trash2, Users, Video, Voicemail } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
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
  const { phone, calls, error, open, close, dial, answer, hangup, dtmf, mute, transfer, hold } = phoneApi
  // A video room this phone opened, in its own window, asks it to call the room's sound (VideoRoom's dialRoom).
  useEffect(() => {
    if (!phone) return
    const ch = new BroadcastChannel('bitvoice-phone')
    ch.onmessage = (e) => { const m = e.data as { phone?: string; dial?: string }; if (m.dial && m.phone === phone.id) void dial(m.dial) }
    return () => ch.close()
  }, [phone, dial])
  const [number, setNumber] = useState('')
  const [ext, setExt] = useState(extensions?.[0]?.number ?? '')
  const [pw, setPw] = useState('')
  const [tab, setTab] = useState<'keypad' | 'recents' | 'voicemail' | 'team' | 'chat'>('keypad')
  // A chat notification tapped in the mobile app opens the Chat tab.
  useEffect(() => {
    const on = () => setTab('chat')
    window.addEventListener('bitvoice:chat', on)
    return () => window.removeEventListener('bitvoice:chat', on)
  }, [])
  // A second call can be placed while the first is on hold.
  const live = calls.filter((c) => c.state !== 'ended')
  const active = live.find((c) => (c.state === 'connected' && !c.held) || c.state === 'calling' || c.state === 'ringing' || c.state === 'incoming')

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
      {calls.map((c) => (
        <CallCard key={c.id} c={c} phoneId={phone?.id} answer={answer} hangup={hangup} mute={mute} hold={hold}
          transfer={transfer} canTransfer={live.length === 1} />
      ))}

      <div className="flex gap-1 rounded-lg bg-slate-900 p-1 text-sm" role="tablist" aria-label="Phone">
        {([['keypad', 'Keypad', Phone], ['recents', 'Recents', Clock], ['voicemail', 'Voicemail', Voicemail], ['team', 'Team', Users], ['chat', 'Chat', MessageSquareText]] as const).map(([k, title, Icon]) => (
          <button key={k} role="tab" aria-selected={tab === k} onClick={() => setTab(k)}
            className={clsx('flex flex-1 items-center justify-center gap-1 rounded-md px-2 py-1.5', tab === k ? 'bg-slate-700 text-slate-100' : 'text-slate-400 hover:text-slate-200')}>
            <Icon size={14} aria-hidden /> <span className="hidden sm:inline">{title}</span>
          </button>
        ))}
      </div>

      {tab === 'keypad' && (
        <>
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
              <Phone size={16} aria-hidden /> {live.length ? 'Call (holds the first)' : 'Call'}
            </button>
          )}
          {!active && <Directory phoneId={phone.id} onPick={(n) => setNumber(n)} />}
          {!active && <VideoRoomButton phoneId={phone.id} />}
        </>
      )}
      {tab === 'recents' && <Recents phoneId={phone.id} onCall={(n) => { setTab('keypad'); setNumber(n); if (!active) dial(n) }} />}
      {tab === 'voicemail' && <Voicemails phoneId={phone.id} onCall={(n) => { setTab('keypad'); dial(n) }} />}
      {tab === 'team' && <Team phoneId={phone.id} onCall={(n) => { setTab('keypad'); dial(n) }}
        onTransfer={active?.state === 'connected' ? (n) => transfer(active.id, n) : undefined} />}
      {tab === 'chat' && <ChatTab phoneId={phone.id} onCall={(n) => { setTab('keypad'); dial(n) }} />}
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

function CallCard({ c, phoneId, answer, hangup, mute, hold, transfer, canTransfer }: {
  c: PhoneCall; phoneId?: string; answer: (id: string) => void; hangup: (id: string) => void
  mute: (id: string, on: boolean) => void; hold: (id: string, on: boolean) => void
  transfer: (id: string, to: string) => void; canTransfer: boolean
}) {
  const now = useNow()
  const [xfer, setXfer] = useState(false)
  const [to, setTo] = useState('')
  const label = c.state === 'connected' && c.reconnecting ? 'Reconnecting…'
    : c.state === 'connected' && c.held ? 'On hold'
      : { incoming: 'Incoming call', calling: 'Calling…', ringing: 'Ringing…', connected: formatDuration(Math.round((now - c.since) / 1000)), ended: c.reason ?? 'Call ended' }[c.state]
  const Icon = c.direction === 'in' ? PhoneIncoming : PhoneOutgoing
  return (
    <div className={clsx('card p-3', c.state === 'incoming' && 'ringing border-emerald-600', c.held && 'opacity-80')}>
      <div className="flex items-center gap-2">
        <Icon size={16} className={c.state === 'ended' ? 'text-slate-500' : c.held ? 'text-amber-300' : 'text-emerald-300'} aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-amber-300">{c.number}{c.name && <span className="ml-2 font-sans text-slate-300">{c.name}</span>}</div>
          <div className={clsx('text-xs', c.reconnecting && c.state === 'connected' ? 'text-amber-300' : c.held ? 'text-amber-300' : c.state === 'connected' ? 'font-mono tabular-nums text-emerald-300' : 'text-slate-400')}
            role={c.reconnecting ? 'status' : undefined}>{label}</div>
          {c.briefing && <p className="mt-1 rounded bg-violet-500/10 px-2 py-1 text-xs text-violet-200" role="note" aria-label="Briefing"><span className="font-semibold text-violet-300">Briefing: </span>{c.briefing}</p>}
        </div>
        {c.state === 'incoming' && (
          <button className="btn border-emerald-600 bg-emerald-600 text-white" onClick={() => answer(c.id)}><Phone size={14} aria-hidden /> Answer</button>
        )}
        {c.state === 'connected' && (
          <>
            <button className="btn px-2" aria-label={c.held ? 'Take off hold' : 'Hold'} aria-pressed={!!c.held} title={c.held ? 'Take off hold' : 'Hold'} onClick={() => hold(c.id, !c.held)}>
              {c.held ? <Play size={14} className="text-amber-300" aria-hidden /> : <Pause size={14} aria-hidden />}
            </button>
            <button className="btn px-2" aria-label={c.muted ? 'Unmute' : 'Mute'} aria-pressed={!!c.muted} onClick={() => mute(c.id, !c.muted)}>
              {c.muted ? <MicOff size={14} className="text-rose-300" aria-hidden /> : <Mic size={14} aria-hidden />}
            </button>
            <button className="btn px-2" aria-label="Transfer" aria-pressed={xfer} title="Transfer" disabled={!canTransfer} onClick={() => setXfer((v) => !v)}>
              <ArrowRightLeft size={14} className={xfer ? 'text-sky-300' : undefined} aria-hidden />
            </button>
          </>
        )}
        {c.state !== 'ended' && (
          <button className="btn border-rose-700 bg-rose-700 text-white" aria-label={c.state === 'incoming' ? 'Decline' : 'Hang up'} onClick={() => hangup(c.id)}><PhoneOff size={14} aria-hidden /></button>
        )}
      </div>
      {xfer && c.state === 'connected' && (
        <form className="mt-2 flex gap-2" onSubmit={(e) => { e.preventDefault(); if (to) { transfer(c.id, to); setXfer(false); setTo('') } }}>
          <input className="input flex-1 font-mono" aria-label="Transfer to" placeholder="Extension or number" autoFocus value={to}
            onChange={(e) => setTo(e.target.value.replace(/[^\d*#+]/g, ''))} />
          <button className="btn border-sky-600 bg-sky-600 text-white" disabled={!to}>Transfer</button>
        </form>
      )}
      {c.transcript && c.transcript.length > 0 && (
        <section aria-label="Live transcript" className="mt-2 rounded-lg border border-slate-800 p-2">
          <h4 className="flex items-center gap-1.5 text-xs font-semibold text-slate-300"><MessageSquareText size={12} className="text-sky-300" aria-hidden /> Live transcript <span className="font-normal text-slate-500">written by AI</span></h4>
          <ol className="mt-1 max-h-40 space-y-0.5 overflow-y-auto text-xs">
            {c.transcript.map((l, i) => (
              <li key={i}><span className={clsx('font-semibold', l.side === 'caller' ? 'text-amber-300' : 'text-sky-300')}>{l.speaker}:</span> <span className="text-slate-200">{l.text}</span></li>
            ))}
          </ol>
        </section>
      )}
      {c.direction === 'in' && c.state !== 'ended' && phoneId && <div className="mt-2"><PhoneCaller phoneId={phoneId} number={c.number} /></div>}
    </div>
  )
}

interface Recent { number: string; name?: string; direction: 'in' | 'out'; when: string; missed: boolean; seconds: number }

/** Recents: the extension's recent calls, tap to call back. */
function Recents({ phoneId, onCall }: { phoneId: string; onCall: (number: string) => void }) {
  const [items, setItems] = useState<Recent[] | null>(null)
  useEffect(() => {
    fetch(`./api/phone/${phoneId}/recents`, { headers: { 'X-BitVoice': '1' } }).then((r) => (r.ok ? r.json() : { items: [] })).then((j) => setItems(j.items)).catch(() => setItems([]))
  }, [phoneId])
  if (items === null) return <p className="py-6 text-center text-sm text-slate-500">Loading…</p>
  if (!items.length) return <p className="py-6 text-center text-sm text-slate-500">No calls yet.</p>
  return (
    <ul className="max-h-72 space-y-1 overflow-y-auto" aria-label="Recent calls">
      {items.map((e, i) => {
        const Icon = e.missed ? PhoneMissed : e.direction === 'in' ? PhoneIncoming : PhoneOutgoing
        return (
          <li key={i}>
            <button className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-slate-800" onClick={() => onCall(e.number)}>
              <Icon size={15} className={e.missed ? 'text-rose-400' : e.direction === 'in' ? 'text-emerald-300' : 'text-sky-300'} aria-hidden />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm text-slate-200">{e.name || e.number}</span>
                {e.name && <span className="block truncate font-mono text-xs text-slate-500">{e.number}</span>}
              </span>
              <time className="text-xs text-slate-500" dateTime={e.when}>{new Date(e.when).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</time>
            </button>
          </li>
        )
      })}
    </ul>
  )
}

interface VM { id: string; from: string; fromName?: string; at: string; seconds: number; heard: boolean; transcript?: string }

/** Visual voicemail: the extension's messages, each with its transcript, play, call back and delete. */
function Voicemails({ phoneId, onCall }: { phoneId: string; onCall: (number: string) => void }) {
  const [items, setItems] = useState<VM[] | null>(null)
  const reload = () => fetch(`./api/phone/${phoneId}/voicemail`, { headers: { 'X-BitVoice': '1' } }).then((r) => (r.ok ? r.json() : { items: [] })).then((j) => setItems(j.items)).catch(() => setItems([]))
  useEffect(() => { reload() }, [phoneId]) // eslint-disable-line react-hooks/exhaustive-deps
  const del = async (id: string) => { await fetch(`./api/phone/${phoneId}/voicemail/${id}`, { method: 'DELETE', headers: { 'X-BitVoice': '1' } }); reload() }
  const heard = (id: string) => fetch(`./api/phone/${phoneId}/voicemail/${id}/heard`, { method: 'POST', headers: { 'X-BitVoice': '1' } })
  if (items === null) return <p className="py-6 text-center text-sm text-slate-500">Loading…</p>
  if (!items.length) return <p className="py-6 text-center text-sm text-slate-500">No voicemail.</p>
  return (
    <ul className="max-h-80 space-y-2 overflow-y-auto" aria-label="Voicemail">
      {items.map((v) => (
        <li key={v.id} className={clsx('card p-2.5', !v.heard && 'border-sky-700')}>
          <div className="flex items-center gap-2">
            {!v.heard && <span className="h-2 w-2 rounded-full bg-sky-400" aria-label="New" />}
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm text-slate-200">{v.fromName || v.from}</span>
              <span className="block font-mono text-xs text-slate-500">{v.from} · {formatDuration(v.seconds)} · {new Date(v.at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</span>
            </span>
            <button className="btn px-2" aria-label={`Call ${v.from} back`} title="Call back" onClick={() => onCall(v.from)}><Phone size={14} className="text-emerald-300" aria-hidden /></button>
            <button className="btn px-2" aria-label="Delete" title="Delete" onClick={() => del(v.id)}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
          </div>
          {v.transcript && <p className="mt-1.5 rounded bg-slate-800/60 px-2 py-1 text-xs text-slate-300">{v.transcript}</p>}
          <audio className="mt-1.5 w-full" controls preload="none" onPlay={() => !v.heard && heard(v.id)}
            src={`./api/phone/${phoneId}/voicemail/${v.id}/audio`} aria-label={`Message from ${v.fromName || v.from}`} />
        </li>
      ))}
    </ul>
  )
}

interface Mate { name: string; number: string; state: 'free' | 'busy' | 'ringing' | 'away' }

/** Team: colleagues' presence (busy lamp). Call them, or transfer the current call to them. */
function Team({ phoneId, onCall, onTransfer }: { phoneId: string; onCall: (number: string) => void; onTransfer?: (number: string) => void }) {
  const [items, setItems] = useState<Mate[] | null>(null)
  useEffect(() => {
    let live = true
    const load = () => fetch(`./api/phone/${phoneId}/presence`, { headers: { 'X-BitVoice': '1' } }).then((r) => (r.ok ? r.json() : { items: [] })).then((j) => live && setItems(j.items)).catch(() => {})
    load()
    const t = setInterval(load, 4000) // the busy lamp keeps up
    return () => { live = false; clearInterval(t) }
  }, [phoneId])
  const dot = { free: 'bg-emerald-400', busy: 'bg-rose-400', ringing: 'bg-amber-400 animate-pulse', away: 'bg-slate-500' }
  const word = { free: 'Free', busy: 'On a call', ringing: 'Ringing', away: 'Away' }
  if (items === null) return <p className="py-6 text-center text-sm text-slate-500">Loading…</p>
  if (!items.length) return <p className="py-6 text-center text-sm text-slate-500">No colleagues.</p>
  return (
    <ul className="max-h-72 space-y-1 overflow-y-auto" aria-label="Team">
      {items.map((m) => (
        <li key={m.number} className="flex items-center gap-2 rounded px-2 py-1.5 hover:bg-slate-800">
          <span className={clsx('h-2.5 w-2.5 rounded-full', dot[m.state])} aria-hidden />
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm text-slate-200">{m.name}</span>
            <span className="block font-mono text-xs text-slate-500">{m.number} · {word[m.state]}</span>
          </span>
          {onTransfer
            ? <button className="btn px-2 py-1 text-xs" onClick={() => onTransfer(m.number)}><ArrowRightLeft size={13} className="text-sky-300" aria-hidden /> Transfer</button>
            : <button className="btn px-2 py-1" aria-label={`Call ${m.name}`} onClick={() => onCall(m.number)}><Phone size={13} className="text-emerald-300" aria-hidden /></button>}
        </li>
      ))}
    </ul>
  )
}

interface ChatMsg { id?: string; from: string; text: string; at: string }
interface ChatPerson { extension: string; name: string; department?: string; state: string; reason?: string; status?: string; unread: number }
interface ChatRoom { id: string; key: string; name: string; members: number; unread: number }

/** Chat: the staff chat (colleagues' presence and direct messages, and rooms), inside the phone. */
function ChatTab({ phoneId, onCall }: { phoneId: string; onCall: (number: string) => void }) {
  const [overview, setOverview] = useState<{ me: string; people: ChatPerson[]; rooms: ChatRoom[] } | null>(null)
  const [error, setError] = useState('')
  const [open, setOpen] = useState<{ key: string; name: string } | null>(null)
  const load = () => fetch(`./api/phone/${phoneId}/chat`, { headers: { 'X-BitVoice': '1' } })
    .then((r) => r.json().then((j: { message?: string } & Record<string, unknown>) => ({ ok: r.ok, j }))).then(({ ok, j }) => { if (ok) setOverview(j as never); else setError(j.message ?? 'chat is off') }).catch(() => {})
  useEffect(() => {
    if (open) return
    load()
    const t = setInterval(load, 4000)
    return () => clearInterval(t)
  }, [phoneId, open]) // eslint-disable-line react-hooks/exhaustive-deps
  if (error) return <p className="py-6 text-center text-sm text-amber-300">{error}</p>
  if (open) return <ChatConversation phoneId={phoneId} conv={open} me={overview?.me ?? ''} onCall={onCall} onBack={() => setOpen(null)} />
  if (overview === null) return <p className="py-6 text-center text-sm text-slate-500">Loading…</p>
  const dot: Record<string, string> = { free: 'bg-emerald-400', busy: 'bg-rose-400', ringing: 'bg-amber-400', away: 'bg-slate-500', offline: 'bg-slate-700' }
  return (
    <div className="max-h-80 space-y-1 overflow-y-auto" aria-label="Chat">
      {overview.rooms.map((rm) => (
        <button key={rm.key} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-slate-800" onClick={() => setOpen({ key: rm.key, name: rm.name })}>
          <Users size={15} className="text-slate-400" aria-hidden />
          <span className="min-w-0 flex-1 truncate text-sm text-slate-200">{rm.name} <span className="text-xs text-slate-500">({rm.members})</span></span>
          {rm.unread > 0 && <span className="rounded-full bg-sky-500 px-1.5 text-xs font-semibold text-white">{rm.unread}</span>}
        </button>
      ))}
      {overview.people.map((p) => (
        <button key={p.extension} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-slate-800"
          onClick={() => setOpen({ key: 'd-' + [overview.me, p.extension].sort().join('-'), name: p.name })}>
          <span className={clsx('h-2.5 w-2.5 rounded-full', dot[p.state] ?? 'bg-slate-500')} aria-hidden />
          <span className="min-w-0 flex-1"><span className="block truncate text-sm text-slate-200">{p.name}</span>
            {p.status && <span className="block truncate text-xs text-slate-500">{p.status}</span>}</span>
          {p.unread > 0 && <span className="rounded-full bg-sky-500 px-1.5 text-xs font-semibold text-white">{p.unread}</span>}
        </button>
      ))}
      {overview.rooms.length === 0 && overview.people.length === 0 && <p className="py-6 text-center text-sm text-slate-500">No colleagues.</p>}
    </div>
  )
}

/** One chat conversation inside the phone: its messages, with a box to send. */
function ChatConversation({ phoneId, conv, me, onCall, onBack }: { phoneId: string; conv: { key: string; name: string }; me: string; onCall: (number: string) => void; onBack: () => void }) {
  const [msgs, setMsgs] = useState<ChatMsg[]>([])
  const [text, setText] = useState('')
  const end = useRef<HTMLDivElement>(null)
  const direct = conv.key.startsWith('d-')
  const other = direct ? conv.key.slice(2).split('-').find((n) => n !== me) ?? '' : ''
  const load = () => fetch(`./api/phone/${phoneId}/chat/${conv.key}`, { headers: { 'X-BitVoice': '1' } })
    .then((r) => (r.ok ? r.json() : { items: [] })).then((j) => setMsgs(j.items)).catch(() => {})
  useEffect(() => { load(); const t = setInterval(load, 3000); return () => clearInterval(t) }, [phoneId, conv.key]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { end.current?.scrollIntoView() }, [msgs])
  const send = async () => {
    const t = text.trim()
    if (!t) return
    setText('')
    await fetch(`./api/phone/${phoneId}/chat/${conv.key}`, { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify({ text: t }) }).catch(() => {})
    load()
  }
  return (
    <div className="flex h-80 flex-col">
      <div className="flex items-center gap-2 border-b border-slate-800 pb-2">
        <button className="btn px-2 py-1" aria-label="Back to chats" onClick={onBack}>Back</button>
        <span className="flex-1 truncate text-sm font-semibold text-slate-200">{conv.name}</span>
        {direct && other && <button className="btn px-2 py-1" aria-label={`Call ${conv.name}`} onClick={() => onCall(other)}><Phone size={13} className="text-emerald-300" aria-hidden /></button>}
      </div>
      <ol className="flex-1 space-y-1.5 overflow-y-auto py-2 text-sm" aria-label="Messages">
        {msgs.map((m, i) => (
          <li key={m.id ?? i} className={clsx('max-w-[85%] rounded-lg px-2.5 py-1.5', m.from === me ? 'ml-auto bg-sky-600 text-white' : 'bg-slate-800 text-slate-200')}>
            {!direct && m.from !== me && <span className="block text-xs font-semibold text-sky-300">{m.from}</span>}
            {m.text}
          </li>
        ))}
        <div ref={end} />
      </ol>
      <form className="flex gap-2 pt-1" onSubmit={(e) => { e.preventDefault(); send() }}>
        <input className="input flex-1" aria-label="Message" placeholder="Type a message" value={text} onChange={(e) => setText(e.target.value)} />
        <button className="btn border-sky-600 bg-sky-600 text-white" disabled={!text.trim()}>Send</button>
      </form>
    </div>
  )
}

/** A video call: pick a BitVoice SFU video room (its cameras and screen sharing) and open it in its own
 * window, so the phone keeps running. Rooms are conference numbers set to a video room in Conferences. */
function VideoRoomButton({ phoneId }: { phoneId: string }) {
  const [open, setOpen] = useState(false)
  const [rooms, setRooms] = useState<{ number: string; name: string; hasPin: boolean; inRoom: number }[] | null>(null)
  const [other, setOther] = useState('')
  useEffect(() => {
    if (!open) return
    let live = true
    const load = () => fetch(`./api/phone/${phoneId}/video-rooms`, { headers: { 'X-BitVoice': '1' } }).then((r) => (r.ok ? r.json() : { items: [] })).then((j) => live && setRooms(j.items)).catch(() => {})
    load()
    const t = setInterval(load, 5000)
    return () => { live = false; clearInterval(t) }
  }, [open, phoneId])
  const join = (n: string) => { window.open(`#/video-room/${encodeURIComponent(n)}?phone=${encodeURIComponent(phoneId)}`, '_blank', 'noopener'); setOpen(false) }
  if (!open) return <button className="btn w-full justify-center" onClick={() => setOpen(true)}><Video size={15} className="text-fuchsia-300" aria-hidden /> Join a video room</button>
  return (
    <div className="space-y-2" role="region" aria-label="Video rooms">
      {rooms === null && <p className="text-sm text-slate-500">Loading…</p>}
      {rooms && rooms.length > 0 && (
        <ul className="max-h-48 space-y-1 overflow-y-auto">
          {rooms.map((rm) => (
            <li key={rm.number}>
              <button className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-slate-800" onClick={() => join(rm.number)}>
                <Video size={15} className="text-fuchsia-300" aria-hidden />
                <span className="min-w-0 flex-1"><span className="block truncate text-sm text-slate-200">{rm.name || `Room ${rm.number}`}</span>
                  <span className="block font-mono text-xs text-slate-500">{rm.number}{rm.hasPin && ' · PIN'}</span></span>
                {rm.inRoom > 0 && <span className="rounded-full bg-fuchsia-600/30 px-1.5 text-xs text-fuchsia-200">{rm.inRoom} in</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
      {rooms && rooms.length === 0 && <p className="text-sm text-slate-500">No video rooms set up. Add one in Conferences, or enter a number.</p>}
      <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); if (other) join(other) }}>
        <input className="input flex-1 font-mono" aria-label="Video room number" placeholder="Room number" inputMode="numeric" value={other} onChange={(e) => setOther(e.target.value.replace(/\D/g, ''))} />
        <button className="btn border-fuchsia-600 bg-fuchsia-600 text-white" disabled={!other}><Video size={14} aria-hidden /> Join</button>
      </form>
      <button className="btn w-full justify-center" onClick={() => setOpen(false)}>Close</button>
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
