import { AlertTriangle, Building2, Crosshair, ExternalLink, FileJson, Hand, Keyboard, MapPin, MessageSquareText, Phone, PhoneOff, RefreshCw, Send, Smartphone, User } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useEndTextCallMutation, useRebidMutation, useSendCallTextMutation, useTakeTextCallMutation, type Call, type Civic, type Location, type TextLine, type TextVia } from './api'
import { LazyMap as MapView } from './LazyMap'
import { Badge, relTime } from './ui'

export function streetLine(c: Civic) {
  return [`${c.number ?? ''}${c.numberSuffix ?? ''}`, c.preDir, c.street, c.streetType, c.postDir].filter(Boolean).join(' ')
}

export function placeLine(c: Civic) {
  return [[c.building && `Bldg ${c.building}`, c.unit && `Unit ${c.unit}`, c.floor && `Floor ${c.floor}`, c.room && `Room ${c.room}`, c.location].filter(Boolean).join(', '),
    [c.city, c.state, c.postalCode].filter(Boolean).join(' ')].filter(Boolean).join(' · ')
}

/** How good a position is, in words a call-taker uses. */
function accuracy(l: Location) {
  if (!l.radius) return { text: 'no uncertainty given', status: 'warn' as const }
  const r = Math.round(l.radius)
  return { text: `within ${r} m${l.confidence ? ` (${l.confidence}% sure)` : ''}`, status: r <= 50 ? 'ok' as const : r <= 300 ? 'warn' as const : 'fail' as const }
}

/** One 911 call: who, where, what the network said, and what the call-taker can do. */
export function EmergencyCard({ call }: { call: Call }) {
  const em = call.emergency!
  const loc = em.location
  const [rebid, { isLoading }] = useRebidMutation()
  const add = em.additional ?? {}
  // A queue holding the caller is not an answer: only a call-taker picking up is.
  const answered = !!em.takenBy
  const held = !!call.answer && !answered
  const doRebid = async () => {
    try { await rebid(call.id).unwrap(); toast.success('Location fetched again') }
    catch (e) { toast.error('Rebid failed', { description: errorOf(e).message }) }
  }
  const acc = loc && loc.lat != null ? accuracy(loc) : null
  const callback = call.from
  return (
    <article className={`card space-y-3 border-l-4 p-4 ${answered ? 'border-l-emerald-500' : 'border-l-rose-500'}`} aria-label={`911 call from ${callback}`}>
      <header className="flex flex-wrap items-center gap-2">
        {em.textOnly ? <MessageSquareText size={16} className={answered ? 'text-emerald-300' : 'animate-pulse text-rose-300'} aria-hidden />
          : <Phone size={16} className={answered ? 'text-emerald-300' : 'animate-pulse text-rose-300'} aria-hidden />}
        <span className="font-mono text-lg font-semibold text-sky-200">{callback}</span>
        {call.fromName && call.fromName !== callback && <span className="text-slate-300">{call.fromName}</span>}
        <Badge status={answered ? 'ok' : 'fail'}>{answered ? `Taken by ${em.takenBy} ${em.takenName ?? ''} ${relTime(em.takenAt!)}` : held || em.textOnly ? `Waiting for a call-taker since ${relTime(call.start)}` : `Ringing since ${relTime(call.start)}`}</Badge>
        {em.esinet && <Badge status="info">ESInet</Badge>}
        {em.textOnly && <Badge status="warn">Text to 911</Badge>}
        {em.service && em.service !== 'urn:service:sos' && <Badge status="info">{em.service.replace('urn:service:', '')}</Badge>}
      </header>

      {!loc ? (
        <p className="flex items-center gap-2 text-sm text-amber-300"><AlertTriangle size={14} aria-hidden /> No location came with this call. Ask the caller where they are.</p>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          {loc.civic && (
            <div className="rounded-lg border border-slate-800 p-3">
              <div className="flex items-center gap-2 text-xs text-slate-500"><Building2 size={13} aria-hidden /> Address</div>
              <div className="mt-1 text-lg font-semibold text-amber-200">{streetLine(loc.civic) || '(no street)'}</div>
              <div className="text-sm text-slate-300">{placeLine(loc.civic)}</div>
              {loc.civic.landmark && <div className="text-sm text-slate-400">near {loc.civic.landmark}</div>}
              {loc.civic.name && <div className="text-sm text-slate-400">{loc.civic.name}</div>}
            </div>
          )}
          {loc.lat != null && loc.lon != null && (
            <div className="rounded-lg border border-slate-800 p-3">
              <div className="flex items-center gap-2 text-xs text-slate-500"><Crosshair size={13} aria-hidden /> Position{loc.method && ` (${loc.method})`}</div>
              <div className="mt-1 font-mono text-lg text-sky-200">{loc.lat.toFixed(6)}, {loc.lon.toFixed(6)}</div>
              {acc && <Badge status={acc.status}>{acc.text}</Badge>}
              <div className="mt-2 flex flex-wrap gap-2 text-sm">
                <a className="btn" href={`https://www.openstreetmap.org/?mlat=${loc.lat}&mlon=${loc.lon}#map=18/${loc.lat}/${loc.lon}`} target="_blank" rel="noreferrer"><MapPin size={14} className="text-rose-300" aria-hidden /> Map <ExternalLink size={12} aria-hidden /></a>
              </div>
            </div>
          )}
        </div>
      )}

      {loc && loc.lat != null && loc.lon != null && <MapView label={`Map of the caller's position, ${loc.lat.toFixed(5)}, ${loc.lon.toFixed(5)}`} points={[{ id: call.id, lat: loc.lat, lon: loc.lon, radius: loc.radius, label: callback }]} />}

      <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm text-slate-400">
        {add.providers?.map((p, i) => <span key={i}>Provider: <span className="text-slate-200">{p.name}</span>{p.contact && <> · <span className="font-mono">{p.contact}</span></>}</span>)}
        {add.service && <span>Service: <span className="text-slate-200">{[add.service.types?.join('/'), add.service.environment, add.service.mobility].filter(Boolean).join(', ')}</span></span>}
        {add.device && <span className="inline-flex items-center gap-1"><Smartphone size={13} aria-hidden /> <span className="text-slate-200">{[add.device.maker, add.device.model, add.device.class].filter(Boolean).join(' ')}</span></span>}
        {add.subscriber && <span className="inline-flex items-center gap-1"><User size={13} aria-hidden /> <span className="text-slate-200">{add.subscriber.name}</span>{add.subscriber.address && ` · ${add.subscriber.address}`}</span>}
      </div>
      {add.comments?.map((c, i) => <p key={i} className="text-sm text-amber-200">Note: {c}</p>)}
      {em.problems?.map((p, i) => <p key={i} className="flex items-center gap-2 text-xs text-amber-300"><AlertTriangle size={12} aria-hidden />{p}</p>)}

      <Conversation call={call} answered={answered} />

      <footer className="flex flex-wrap items-center gap-2">
        {em.textOnly && <TextControls id={call.id} taken={answered} />}
        {em.locationUri && <button className="btn" disabled={isLoading} onClick={doRebid}><RefreshCw size={14} className="text-sky-300" aria-hidden /> {isLoading ? 'Fetching…' : 'Rebid location'}</button>}
        {em.rebid && <span className="text-xs text-slate-500">fetched again {relTime(em.rebid)}</span>}
        <a className="btn" href={`/api/calls/${call.id}/eido`} target="_blank" rel="noreferrer"><FileJson size={14} className="text-violet-300" aria-hidden /> EIDO</a>
      </footer>
    </article>
  )
}

const viaName: Record<TextVia, string> = { tty: 'TTY', rtt: 'Real-time text', msrp: 'Text to 911', sip: 'Text to 911' }

/** The call's text conversation (a TTY on the line, real-time text, or text to 911) and the reply box.
 * A call with no text yet still offers TTY, for a silent caller who may be using one. */
function Conversation({ call, answered }: { call: Call; answered: boolean }) {
  const em = call.emergency!
  const lines: TextLine[] = em.text ?? []
  const vias = em.textVia ?? []
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState('')
  const [via, setVia] = useState<TextVia | ''>('')
  const [send, { isLoading }] = useSendCallTextMutation()
  const has = lines.length > 0
  if (!has && !open) {
    return answered ? <button className="btn self-start" onClick={() => setOpen(true)}><Keyboard size={14} className="text-teal-300" aria-hidden /> Reply by TTY</button> : null
  }
  const choices: TextVia[] = vias.length ? vias : ['tty']
  const chosen = (via || choices[0]) as TextVia
  const doSend = async () => {
    if (!draft.trim()) return
    try { await send({ id: call.id, via: chosen, text: draft }).unwrap(); setDraft('') }
    catch (e) { toast.error('Text not sent', { description: errorOf(e).message }) }
  }
  return (
    <section className="rounded-lg border border-teal-900/60 bg-slate-900/40 p-3" aria-label="Text conversation">
      <div className="mb-2 flex items-center gap-2 text-xs text-teal-300"><MessageSquareText size={13} aria-hidden /> Text {vias.map((v) => <Badge key={v} status="info">{viaName[v]}</Badge>)}</div>
      <ol className="max-h-60 space-y-1 overflow-y-auto text-sm" aria-live="polite">
        {lines.map((l, i) => (
          <li key={i} className={l.from === 'caller' ? 'text-amber-100' : 'text-sky-200'}>
            <span className="mr-2 font-mono text-xs text-slate-500">{new Date(l.at).toLocaleTimeString()}</span>
            <span className={`mr-1 text-xs font-semibold ${l.from === 'caller' ? 'text-amber-300' : 'text-sky-300'}`}>{l.from === 'caller' ? 'Caller' : (l.name || 'Call-taker')}:</span>
            <span className="font-mono">{l.text}</span>{l.open && <span className="ml-1 animate-pulse text-amber-300" aria-label="typing">▌</span>}
          </li>
        ))}
        {!has && <li className="text-slate-500">No text yet. A TTY caller types GA (go ahead) when done; reply in capitals, ending with GA.</li>}
      </ol>
      {answered && (
        <div className="mt-2 flex gap-2">
          {choices.length > 1 && (
            <select aria-label="Send by" className="input w-40" value={chosen} onChange={(e) => setVia(e.target.value as TextVia)}>{choices.map((v) => <option key={v} value={v}>{viaName[v]}</option>)}</select>
          )}
          <input aria-label="Reply" className="input flex-1 font-mono" value={draft} maxLength={4000} placeholder={chosen === 'tty' ? 'WHAT IS YOUR ADDRESS? GA' : 'Type a reply'}
            onChange={(e) => setDraft(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') doSend() }} />
          <button className="btn btn-primary" disabled={isLoading || !draft.trim()} onClick={doSend}><Send size={14} aria-hidden /> Send</button>
        </div>
      )}
    </section>
  )
}

/** Take or end a text-to-911 conversation: there is no phone to pick up, so it is taken here. */
function TextControls({ id, taken }: { id: string; taken: boolean }) {
  const [take, { isLoading: taking }] = useTakeTextCallMutation()
  const [end, { isLoading: ending }] = useEndTextCallMutation()
  const run = async (f: () => Promise<unknown>, ok: string) => {
    try { await f(); toast.success(ok) } catch (e) { toast.error('Could not do that', { description: errorOf(e).message }) }
  }
  return taken
    ? <button className="btn" disabled={ending} onClick={() => run(() => end(id).unwrap(), 'Conversation ended')}><PhoneOff size={14} className="text-rose-300" aria-hidden /> End conversation</button>
    : <button className="btn btn-primary" disabled={taking} onClick={() => run(() => take(id).unwrap(), 'Conversation taken')}><Hand size={14} aria-hidden /> Take this text</button>
}
