import { AlertTriangle, Building2, Crosshair, ExternalLink, FileJson, MapPin, Phone, RefreshCw, Smartphone, User } from 'lucide-react'
import { toast } from 'sonner'
import { errorOf, useRebidMutation, type Call, type Civic, type Location } from './api'
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
        <Phone size={16} className={answered ? 'text-emerald-300' : 'animate-pulse text-rose-300'} aria-hidden />
        <span className="font-mono text-lg font-semibold text-sky-200">{callback}</span>
        {call.fromName && call.fromName !== callback && <span className="text-slate-300">{call.fromName}</span>}
        <Badge status={answered ? 'ok' : 'fail'}>{answered ? `Taken by ${em.takenBy} ${em.takenName ?? ''} ${relTime(em.takenAt!)}` : held ? `Waiting for a call-taker since ${relTime(call.start)}` : `Ringing since ${relTime(call.start)}`}</Badge>
        {em.esinet && <Badge status="info">ESInet</Badge>}
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

      <div className="flex flex-wrap gap-x-6 gap-y-1 text-sm text-slate-400">
        {add.providers?.map((p, i) => <span key={i}>Provider: <span className="text-slate-200">{p.name}</span>{p.contact && <> · <span className="font-mono">{p.contact}</span></>}</span>)}
        {add.service && <span>Service: <span className="text-slate-200">{[add.service.types?.join('/'), add.service.environment, add.service.mobility].filter(Boolean).join(', ')}</span></span>}
        {add.device && <span className="inline-flex items-center gap-1"><Smartphone size={13} aria-hidden /> <span className="text-slate-200">{[add.device.maker, add.device.model, add.device.class].filter(Boolean).join(' ')}</span></span>}
        {add.subscriber && <span className="inline-flex items-center gap-1"><User size={13} aria-hidden /> <span className="text-slate-200">{add.subscriber.name}</span>{add.subscriber.address && ` · ${add.subscriber.address}`}</span>}
      </div>
      {add.comments?.map((c, i) => <p key={i} className="text-sm text-amber-200">Note: {c}</p>)}
      {em.problems?.map((p, i) => <p key={i} className="flex items-center gap-2 text-xs text-amber-300"><AlertTriangle size={12} aria-hidden />{p}</p>)}

      <footer className="flex flex-wrap items-center gap-2">
        {em.locationUri && <button className="btn" disabled={isLoading} onClick={doRebid}><RefreshCw size={14} className="text-sky-300" aria-hidden /> {isLoading ? 'Fetching…' : 'Rebid location'}</button>}
        {em.rebid && <span className="text-xs text-slate-500">fetched again {relTime(em.rebid)}</span>}
        <a className="btn" href={`/api/calls/${call.id}/eido`} target="_blank" rel="noreferrer"><FileJson size={14} className="text-violet-300" aria-hidden /> EIDO</a>
      </footer>
    </article>
  )
}
