import { clsx } from 'clsx'
import {
  BellRing, CheckCheck, Headphones, KeyRound, Link2, Lock, LockOpen, MapPin, Mic, Phone, PhoneOff, Plus, Radio as RadioIcon, Save, Search, Siren, Trash2, Volume2, VolumeX,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { toast } from 'sonner'
import {
  errorOf, useAckEmergencyMutation, useAlertToneMutation, useHangUpMutation, useMonitorMutation, usePatchMutation, usePttMutation, useRadioLocationsQuery, useRadioLogQuery, useRadioQuery, useSimulSelectMutation,
  useSaveSettingsMutation, useSettingsQuery, useUnitCallMutation,
  type ApiErrorBody, type ConsoleState, type P25Key, type RadioSettings,
} from './api'
import { useIsAdmin } from './Editor'
import { useDraft } from './useDraft'
import { Empty, Field, Page, StaleBanner, Toggle, problemFor } from './ui'

const colors = ['sky', 'rose', 'amber', 'emerald', 'violet', 'teal']
const border: Record<string, string> = {
  sky: 'border-sky-700', rose: 'border-rose-700', amber: 'border-amber-700', emerald: 'border-emerald-700', violet: 'border-violet-700', teal: 'border-teal-700',
}
const toneNames: Record<string, string> = { steady: 'Alert 1 (steady)', warble: 'Alert 2 (warble)', pulsed: 'Alert 3 (pulsed)', beep: 'Beep' }
const algNames: Record<string, string> = { aes256: 'AES-256', des: 'DES-OFB', tdes: 'Triple DES' }
const keyDigits: Record<string, number> = { aes256: 64, des: 16, tdes: 48 }

type Position = ConsoleState['positions'][0]

export function RadioConsole() {
  const admin = useIsAdmin()
  const { data: settings } = useSettingsQuery()
  const { data: st } = useRadioQuery(undefined, { pollingInterval: 400 })
  const number = settings?.system.radio?.console
  const p25 = !!settings?.system.radio?.p25?.wacn
  const [mine, setMine] = useState('')
  const pos = st?.positions.find((p) => p.call === mine) ?? (st?.positions.length === 1 ? st.positions[0] : undefined)
  return (
    <Page icon={RadioIcon} title="Radio console" accent="from-amber-400 to-rose-400"
      intro="The radio dispatch console: radio channels through radio-over-IP gateways and P25 talkgroups over the ISSI, on the same headset as your phone calls. Join with your phone or the browser phone, hold a channel's button to talk on it, send alert tones, patch channels, and call a radio privately.">
      {!number ? <Empty icon={RadioIcon} title="The console is off">Set a console number and add channels below.</Empty> : (
        <section className="card flex flex-wrap items-center gap-3 p-4" aria-label="Your position">
          <Headphones size={18} className="text-sky-300" aria-hidden />
          {pos ? <span className="text-slate-200">On the console: <span className="font-mono text-amber-300">{pos.ext}</span> {pos.name}</span>
            : <span className="text-slate-400">Dial <span className="font-mono text-amber-300">{number}</span> from your phone to join, or</span>}
          {!pos && <button className="btn btn-primary" onClick={() => window.dispatchEvent(new CustomEvent('bitvoice:dial', { detail: number }))}><Headphones size={15} aria-hidden /> Join with the browser phone</button>}
          {(st?.positions.length ?? 0) > 1 && (
            <Field label="Control the position of" htmlFor="rpos"><select id="rpos" className="input" value={pos?.call ?? ''} onChange={(e) => setMine(e.target.value)}>
              <option value="">Choose…</option>{st!.positions.map((p) => <option key={p.call} value={p.call}>{p.ext} {p.name}</option>)}</select></Field>
          )}
          {pos && p25 && admin && <CallRadio pos={pos} />}
        </section>
      )}
      {st && <Channels st={st} pos={pos} admin={admin} />}
      <RadioPositions />
      {number && <RadioLog />}
      {admin && <RadioSetup />}
    </Page>
  )
}

/** A private (unit-to-unit) call to a radio. */
function CallRadio({ pos }: { pos: Position }) {
  const [unit, setUnit] = useState('')
  const [emergency, setEmergency] = useState(false)
  const [call, { isLoading }] = useUnitCallMutation()
  const go = async () => {
    try { await call({ call: pos.call, unit: Number(unit), emergency }).unwrap(); toast.success(`Calling unit ${unit}`); setUnit('') }
    catch (e) { toast.error(errorOf(e).message) }
  }
  return (
    <form className="ml-auto flex flex-wrap items-end gap-2" onSubmit={(e) => { e.preventDefault(); go() }} aria-label="Call a radio">
      <Field label="Call a radio (unit ID)" htmlFor="rcu"><input id="rcu" className="input w-36 font-mono" inputMode="numeric" value={unit} placeholder="197" onChange={(e) => setUnit(e.target.value.replace(/\D/g, ''))} /></Field>
      <Toggle checked={emergency} onChange={setEmergency} label="Emergency" />
      <button className="btn" type="submit" disabled={!unit || isLoading}><Phone size={14} className="text-emerald-300" aria-hidden /> Call</button>
    </form>
  )
}

function Channels({ st, pos, admin }: { st: ConsoleState; pos?: Position; admin: boolean }) {
  const [ptt] = usePttMutation()
  const [monitor] = useMonitorMutation()
  const [patch] = usePatchMutation()
  const [alert] = useAlertToneMutation()
  const [hangUp] = useHangUpMutation()
  const [simul] = useSimulSelectMutation()
  const [ack] = useAckEmergencyMutation()
  const [patching, setPatching] = useState<string | null>(null)
  const [picked, setPicked] = useState<string[]>([]) // channels chosen for simulselect
  if (!st.channels.length) return null
  const simulKey = (on: boolean) => {
    if (!pos) return toast.info('Join the console first')
    simul({ call: pos.call, channels: picked, on }).unwrap().catch((e) => toast.error(errorOf(e).message))
  }
  const doAck = (id: string) => ack(id).unwrap().then(() => toast.success('Emergency acknowledged')).catch((e) => toast.error(errorOf(e).message))
  const heard = (id: string) => !pos || !pos.monitor.length || pos.monitor.includes(id)
  const key = (channel: string, on: boolean) => {
    if (!pos) return toast.info('Join the console first')
    ptt({ call: pos.call, channel, on }).unwrap().catch((e) => toast.error(errorOf(e).message))
  }
  const sendTone = (channel: string, tone: string) => {
    if (!pos || !tone) return
    alert({ call: pos.call, channel, tone }).unwrap().catch((e) => toast.error(errorOf(e).message))
  }
  const toggleMonitor = (id: string) => {
    if (!pos) return
    const all = st.channels.map((c) => c.id)
    const now = pos.monitor.length ? pos.monitor : all
    const next = now.includes(id) ? now.filter((x) => x !== id) : [...now, id]
    monitor({ call: pos.call, channels: next.length === all.length ? null : next })
  }
  const clickPatch = async (id: string) => {
    if (!patching) return setPatching(id)
    if (patching === id) return setPatching(null)
    const on = !st.channels.find((c) => c.id === patching)?.patched.includes(id)
    try { await patch({ a: patching, b: id, on }).unwrap(); toast.success(on ? 'Patched' : 'Patch ended') } catch (e) { toast.error(errorOf(e).message) }
    setPatching(null)
  }
  const nameOf = (id: string) => st.channels.find((c) => c.id === id)?.name ?? id
  const simulOn = picked.length > 1 && picked.every((id) => pos?.ptt.includes(id))
  return (
    <>
    {admin && pos && (
      <section aria-label="Simulselect" className="card mt-6 flex flex-wrap items-center gap-3 p-3">
        <RadioIcon size={16} className="text-rose-300" aria-hidden />
        <span className="text-sm text-slate-300">Simulselect: tick channels below, then hold to talk on all of them at once.</span>
        <button className={clsx('btn ml-auto justify-center px-6', simulOn && 'bg-rose-600 text-white')} disabled={picked.length < 2} aria-pressed={simulOn} aria-label={`Talk on ${picked.length} channels`}
          onPointerDown={(e) => { e.currentTarget.setPointerCapture(e.pointerId); simulKey(true) }} onPointerUp={() => simulKey(false)} onPointerCancel={() => simulKey(false)}
          onKeyDown={(e) => { if ((e.key === ' ' || e.key === 'Enter') && !e.repeat) { e.preventDefault(); simulKey(true) } }}
          onKeyUp={(e) => { if (e.key === ' ' || e.key === 'Enter') simulKey(false) }}>
          <Mic size={16} aria-hidden /> {simulOn ? 'Talking on all' : `Hold to talk on ${picked.length || 'the ticked'} channels`}
        </button>
      </section>
    )}
    <section className="mt-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-3" aria-label="Channels">
      {st.channels.map((c) => {
        const mineTx = pos?.ptt.includes(c.id)
        const emergency = c.emergency || c.talker?.includes('EMERGENCY') || !!c.emergencyUnit
        return (
          <article key={c.id} aria-label={c.name} className={clsx('card space-y-3 border-2 p-4', emergency ? 'border-rose-500' : border[c.color] ?? 'border-slate-800',
            mineTx && 'ring-2 ring-rose-500', patching === c.id && 'ring-2 ring-violet-400')}>
            <header className="flex flex-wrap items-center gap-2">
              <span className={clsx('h-3 w-3 rounded-full', c.receive ? 'bg-emerald-400 shadow-[0_0_8px] shadow-emerald-400' : 'bg-slate-700')} aria-label={c.receive ? 'receiving' : 'quiet'} />
              <h3 className="font-semibold text-slate-100">{c.private ? `Private call: unit ${c.unit}` : c.name}</h3>
              {c.kind === 'p25' && !c.private && <span className={clsx('rounded px-1.5 text-[10px] font-semibold', c.linked ? 'bg-emerald-900/50 text-emerald-200' : 'bg-rose-900/50 text-rose-200')}>{c.linked ? 'P25' : 'P25 down'}</span>}
              {c.private && <span className="rounded bg-violet-900/60 px-1.5 text-[10px] font-semibold text-violet-200">{c.incoming ? 'called in' : c.linked ? 'connected' : 'calling…'}</span>}
              {emergency && <span className="flex items-center gap-1 rounded bg-rose-700 px-1.5 text-[10px] font-bold text-white"><Siren size={11} aria-hidden /> EMERGENCY</span>}
              {c.txEncryption && <span className="flex items-center gap-1 rounded bg-sky-900/60 px-1.5 text-[10px] font-semibold text-sky-200" title="Dispatchers transmit encrypted"><Lock size={10} aria-hidden /> TX {c.txEncryption}</span>}
              {c.txBy && <span className="ml-auto rounded bg-rose-900/60 px-1.5 text-xs font-semibold text-rose-200">{c.tone ? 'tone' : c.waiting ? 'asking' : 'TX'} {c.txBy}</span>}
              {admin && pos && !c.private && (
                <label className="ml-auto flex items-center gap-1 text-xs text-slate-400">
                  <input type="checkbox" checked={picked.includes(c.id)} onChange={(e) => setPicked((p) => e.target.checked ? [...p, c.id] : p.filter((x) => x !== c.id))} aria-label={`Simulselect ${c.name}`} /> select
                </label>
              )}
            </header>
            {c.emergencyUnit && (
              <div className="flex flex-wrap items-center gap-2 rounded-lg bg-rose-950/70 p-2 text-sm" role="alert">
                <Siren size={14} className="animate-pulse text-rose-300" aria-hidden />
                <span className="font-semibold text-rose-200">Emergency from {c.emergencyAlias || `unit ${c.emergencyUnit}`}</span>
                {c.emergencyAt && <span className="text-xs text-rose-300/80">since {new Date(c.emergencyAt).toLocaleTimeString()}</span>}
                {admin && <button className="btn ml-auto py-1" onClick={() => doAck(c.id)}><CheckCheck size={14} className="text-emerald-300" aria-hidden /> Acknowledge</button>}
              </div>
            )}
            {!c.emergencyUnit && c.ackBy && c.ackAt && <p className="text-xs text-slate-500">Emergency acknowledged by {c.ackBy} at {new Date(c.ackAt).toLocaleTimeString()}</p>}
            {c.talker && (
              <p className={clsx('flex items-center gap-1 text-xs', emergency ? 'font-semibold text-rose-300' : 'text-emerald-300')}>
                {c.talkerName ? <><span className="font-semibold">{c.talkerName}</span> <span className="font-mono text-slate-500">({c.talker.replace(' EMERGENCY', '')})</span></> : <>Unit {c.talker.replace(' EMERGENCY', '')}</>}
                {c.encryption && (c.noKey
                  ? <span className="ml-1 flex items-center gap-1 text-amber-300"><LockOpen size={11} aria-hidden /> {c.encryption}, no key: muted</span>
                  : <span className="ml-1 flex items-center gap-1 text-sky-300"><Lock size={11} aria-hidden /> {c.encryption}</span>)}
              </p>
            )}
            {c.notice && <p className="text-xs text-amber-300">{c.notice}</p>}
            {c.problem && <p className="text-xs text-rose-300">{c.problem}</p>}
            {c.patched.length > 0 && <p className="flex items-center gap-1 text-xs text-violet-300"><Link2 size={12} aria-hidden /> patched to {c.patched.map(nameOf).join(', ')}</p>}
            <button className={clsx('btn w-full justify-center py-4 text-base', mineTx ? 'bg-rose-600 text-white' : '')} disabled={!pos || !admin || (!!c.txBy && !mineTx)}
              aria-pressed={!!mineTx} aria-label={`Talk on ${c.name}`}
              onPointerDown={(e) => { e.currentTarget.setPointerCapture(e.pointerId); key(c.id, true) }} onPointerUp={() => key(c.id, false)} onPointerCancel={() => key(c.id, false)}
              onKeyDown={(e) => { if ((e.key === ' ' || e.key === 'Enter') && !e.repeat) { e.preventDefault(); key(c.id, true) } }}
              onKeyUp={(e) => { if (e.key === ' ' || e.key === 'Enter') key(c.id, false) }}>
              <Mic size={18} aria-hidden /> {mineTx ? (c.waiting ? 'Asking for the channel…' : 'Talking') : 'Hold to talk'}
            </button>
            <div className="flex flex-wrap gap-2">
              <button className="btn flex-1 justify-center" disabled={!pos} aria-pressed={heard(c.id)} onClick={() => toggleMonitor(c.id)}>
                {heard(c.id) ? <><Volume2 size={14} className="text-emerald-300" aria-hidden /> Hearing</> : <><VolumeX size={14} className="text-slate-500" aria-hidden /> Muted</>}
              </button>
              {admin && !c.private && <button className="btn flex-1 justify-center" aria-pressed={patching === c.id} onClick={() => clickPatch(c.id)}>
                <Link2 size={14} className="text-violet-300" aria-hidden /> {patching && patching !== c.id ? (c.patched.includes(patching) ? 'Unpatch' : 'Patch here') : 'Patch'}
              </button>}
              {admin && c.private && <button className="btn flex-1 justify-center" onClick={() => hangUp({ channel: c.id }).unwrap().catch((e) => toast.error(errorOf(e).message))}>
                <PhoneOff size={14} className="text-rose-300" aria-hidden /> Hang up
              </button>}
            </div>
            {admin && pos && (
              <div className="flex items-center gap-2">
                <BellRing size={14} className="text-amber-300" aria-hidden />
                <select className="input flex-1" aria-label={`Alert tone on ${c.name}`} value="" disabled={!!c.txBy && !mineTx}
                  onChange={(e) => sendTone(c.id, e.target.value)}>
                  <option value="">Send an alert tone…</option>
                  {Object.keys(st.tones ?? {}).sort().map((t) => <option key={t} value={t} title={st.tones[t]}>{toneNames[t] ?? t}</option>)}
                </select>
              </div>
            )}
          </article>
        )
      })}
    </section>
    </>
  )
}

const ago = (at: string) => {
  const s = Math.max(0, Math.round((Date.now() - new Date(at).getTime()) / 1000))
  return s < 60 ? `${s} s ago` : s < 3600 ? `${Math.round(s / 60)} min ago` : `${Math.round(s / 3600)} h ago`
}

/** Where the radios are, as the radio data gateway reports them. Shown once a report has come. */
function RadioPositions() {
  const { data } = useRadioLocationsQuery(undefined, { pollingInterval: 5000 })
  const items = data?.items ?? []
  if (!items.length) return null
  return (
    <section className="card mt-6 space-y-3 p-5" aria-label="Radio positions">
      <h2 className="flex items-center gap-2 font-semibold text-slate-100"><MapPin size={16} className="text-rose-300" aria-hidden /> Radio positions</h2>
      <table className="w-full text-sm">
        <thead className="text-left text-xs text-slate-500"><tr><th className="py-1">Radio</th><th>Position</th><th>Speed</th><th>Reported</th><th /></tr></thead>
        <tbody>{items.map((f) => {
          const stale = Date.now() - new Date(f.at).getTime() > 10 * 60_000
          return (
            <tr key={f.unit} className="border-t border-slate-800">
              <td className="py-1">{f.alias ? <span className="font-semibold text-emerald-200">{f.alias}</span> : <span className="text-slate-300">Unit</span>} <span className="font-mono text-xs text-slate-500">{f.unit}</span></td>
              <td className="font-mono text-sky-200">{f.lat.toFixed(5)}, {f.lon.toFixed(5)}{f.accuracy ? <span className="ml-1 text-xs text-slate-500">±{Math.round(f.accuracy)} m</span> : null}</td>
              <td className="text-slate-300">{f.speed ? `${Math.round(f.speed)} km/h` : '–'}{f.heading != null && f.speed ? <span className="ml-1 text-xs text-slate-500">{Math.round(f.heading)}°</span> : null}</td>
              <td className={stale ? 'text-amber-300' : 'text-slate-400'}>{ago(f.at)}</td>
              <td><a className="btn py-0.5" href={`https://www.openstreetmap.org/?mlat=${f.lat}&mlon=${f.lon}#map=16/${f.lat}/${f.lon}`} target="_blank" rel="noreferrer" aria-label={`Map of ${f.alias || `unit ${f.unit}`}`}><MapPin size={12} aria-hidden /> Map</a></td>
            </tr>
          )
        })}</tbody>
      </table>
    </section>
  )
}

/** Every transmission on the console, heard or sent, with its recording. */
function RadioLog() {
  const [q, setQ] = useState('')
  const { data } = useRadioLogQuery({ q })
  const [play, setPlay] = useState('')
  const items = data?.items ?? []
  return (
    <section className="card mt-6 space-y-3 p-5" aria-labelledby="rl-h">
      <div className="flex flex-wrap items-center gap-3">
        <h2 id="rl-h" className="font-semibold text-slate-100">Radio log</h2>
        <label className="relative ml-auto">
          <span className="sr-only">Find a unit, dispatcher or channel</span>
          <Search size={14} className="absolute left-2 top-2.5 text-slate-500" aria-hidden />
          <input className="input w-64 pl-7" placeholder="Unit, dispatcher, channel" value={q} onChange={(e) => setQ(e.target.value)} />
        </label>
      </div>
      {!items.length ? <p className="text-sm text-slate-400">No transmissions yet. Turn on <span className="text-slate-200">Record every transmission</span> in the console setup to keep them.</p> : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-slate-400"><tr><th className="py-1 pr-3">When</th><th className="pr-3">Channel</th><th className="pr-3">Who</th><th className="pr-3">Length</th><th className="pr-3">Notes</th><th>Recording</th></tr></thead>
            <tbody>
              {items.map((x) => (
                <tr key={x.id} className="border-t border-slate-800">
                  <td className="py-1.5 pr-3 font-mono text-xs text-slate-300">{new Date(x.start).toLocaleString()}</td>
                  <td className="pr-3 text-slate-200">{x.channelName}</td>
                  <td className="pr-3">{x.direction === 'rx'
                    ? <span className="text-emerald-300">{x.unitName ? `${x.unitName} (${x.unit})` : x.unit ? `Unit ${x.unit}` : 'A radio'}</span>
                    : <span className="text-rose-300">Dispatcher {x.dispatcher ?? ''}</span>}</td>
                  <td className="pr-3 font-mono text-xs text-slate-300">{x.seconds.toFixed(1)} s</td>
                  <td className="pr-3 text-xs">
                    {x.emergency && <span className="mr-2 font-semibold text-rose-300">emergency</span>}
                    {x.tone && <span className="mr-2 text-amber-300">{toneNames[x.tone] ?? x.tone}</span>}
                    {x.encryption && <span className="mr-2 text-sky-300">{x.encryption}</span>}
                    {x.private && <span className="text-violet-300">private call</span>}
                  </td>
                  <td>{x.recording && (play === x.id
                    ? <audio autoPlay controls className="h-8 w-56" src={`./api/recordings/${x.recording}`} aria-label={`Recording of ${x.channelName} at ${new Date(x.start).toLocaleTimeString()}`} />
                    : <button className="btn py-1 text-xs" onClick={() => setPlay(x.id)}>Play</button>)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

const blankP25: RadioSettings['p25'] = { wacn: '', system: '', rfss: 0, unit: 0, sipPort: 5062, console: true, priority: 4, keys: [], unitHome: '', unitHomeRfss: 0, unitTxKey: '' }
const blankRadio: RadioSettings = { console: '', channels: [], p25: blankP25, record: false, cadUrl: '', cadSecret: '', aliases: [] }

function fromServer(r?: Partial<RadioSettings>): RadioSettings {
  return {
    ...blankRadio, ...(r ?? {}),
    channels: (r?.channels ?? []).map((c) => ({ ...c, kind: c.kind || 'roip', txKey: c.txKey ?? '' })),
    p25: { ...blankP25, ...(r?.p25 ?? {}), keys: r?.p25?.keys ?? [] },
    aliases: r?.aliases ?? [],
  }
}

function RadioSetup() {
  const { data } = useSettingsQuery()
  const server = useMemo(() => (data ? fromServer(data.system.radio) : undefined), [data])
  const { draft, setDraft, reset, stale } = useDraft<RadioSettings>(server)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [save, { isLoading }] = useSaveSettingsMutation()
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.radio.' + f)
  const setCh = (i: number, v: Partial<RadioSettings['channels'][0]>) => setDraft((d) => ({ ...d, channels: d.channels.map((c, j) => (j === i ? { ...c, ...v } : c)) }))
  const nextPort = () => Math.max(45998, ...draft.channels.map((c) => c.port)) + 2
  const doSave = async () => {
    try { const s = await save({ ...data, system: { ...data.system, radio: draft } }).unwrap(); reset(fromServer(s.system.radio)); setErr(null); toast.success('Radio console saved') }
    catch (e) { setErr(errorOf(e)) }
  }
  const usesP25 = draft.channels.some((c) => c.kind === 'p25') || !!draft.p25.unitHome || !!draft.p25.wacn
  const keys = draft.p25.keys
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="rs-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="rs-h" className="font-semibold text-slate-100">Console setup</h2>
      <Field label="Console number" htmlFor="rcn" error={p('console')} hint="Dispatchers dial it to join. Empty turns the console off.">
        <input id="rcn" className="input w-40 font-mono text-amber-300" value={draft.console} placeholder="800" onChange={(e) => setDraft((d) => ({ ...d, console: e.target.value.replace(/[^0-9*#]/g, '') }))} />
      </Field>
      <h3 className="text-sm font-semibold text-violet-300">Channels</h3>
      <p className="text-sm text-slate-400">A channel is either a port on a radio-over-IP gateway (JPS, Omnitronics, Cisco, Telex and others; set the gateway to send the channel's audio as RTP, G.711 µ-law, to this server at the port below and to key the radio on voice), or a P25 talkgroup, joined over the ISSI with the RFSS that is home to it (the port below is for its voice).</p>
      {draft.channels.map((c, i) => (
        <div key={i} className="space-y-3 rounded-lg border border-slate-800 p-3">
          <div className="grid items-end gap-3 md:grid-cols-[1fr_12rem_8rem_8rem_auto]">
            <Field label="Name" htmlFor={`rcnm${i}`} error={p(`channels[${i}].name`)}><input id={`rcnm${i}`} className="input" value={c.name} onChange={(e) => setCh(i, { name: e.target.value })} /></Field>
            <Field label="Reached by" htmlFor={`rck${i}`} error={p(`channels[${i}].kind`)}>
              <select id={`rck${i}`} className="input" value={c.kind} onChange={(e) => setCh(i, { kind: e.target.value as 'roip' | 'p25' })}>
                <option value="roip">A radio-over-IP gateway</option><option value="p25">A P25 talkgroup (ISSI)</option>
              </select>
            </Field>
            <Field label="Port here" htmlFor={`rcp${i}`} error={p(`channels[${i}].port`)}><input id={`rcp${i}`} className="input font-mono" inputMode="numeric" value={c.port || ''} onChange={(e) => setCh(i, { port: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
            <Field label="Colour" htmlFor={`rcc${i}`}><select id={`rcc${i}`} className="input" value={c.color} onChange={(e) => setCh(i, { color: e.target.value })}>{colors.map((x) => <option key={x}>{x}</option>)}</select></Field>
            <button className="btn-ghost p-2" aria-label={`Remove channel ${c.name || i + 1}`} onClick={() => setDraft((d) => ({ ...d, channels: d.channels.filter((_, j) => j !== i) }))}><Trash2 size={15} className="text-rose-300" aria-hidden /></button>
          </div>
          {c.kind === 'p25' ? (
            <div className="grid gap-3 md:grid-cols-4">
              <Field label="Talkgroup ID" htmlFor={`rcg${i}`} error={p(`channels[${i}].group`)} hint="Decimal, 1 to 65535."><input id={`rcg${i}`} className="input font-mono" inputMode="numeric" value={c.group || ''} onChange={(e) => setCh(i, { group: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
              <Field label="Home RFSS" htmlFor={`rch${i}`} error={p(`channels[${i}].home`)} hint="The SIP address of the RFSS that is home to the talkgroup."><input id={`rch${i}`} className="input font-mono" value={c.home} placeholder="10.0.2.25:5060" onChange={(e) => setCh(i, { home: e.target.value.trim() })} /></Field>
              <Field label="Home RFSS ID" htmlFor={`rchi${i}`} error={p(`channels[${i}].homeRfss`)}><input id={`rchi${i}`} className="input font-mono" inputMode="numeric" value={c.homeRfss || ''} onChange={(e) => setCh(i, { homeRfss: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
              <Field label="Transmit" htmlFor={`rctk${i}`} error={p(`channels[${i}].txKey`)} hint="Encrypted with a key below, or clear.">
                <select id={`rctk${i}`} className="input" value={c.txKey} onChange={(e) => setCh(i, { txKey: e.target.value })}>
                  <option value="">Clear</option>{keys.map((k) => <option key={k.id || k.name} value={k.id} disabled={!k.id}>{k.name || 'Unnamed key'} ({algNames[k.alg]}{k.id ? '' : ', save first'})</option>)}
                </select>
              </Field>
            </div>
          ) : (
            <Field label="Gateway" htmlFor={`rcg${i}`} error={p(`channels[${i}].gateway`)}><input id={`rcg${i}`} className="input font-mono" value={c.gateway} placeholder="10.0.0.40:5000" onChange={(e) => setCh(i, { gateway: e.target.value.trim() })} /></Field>
          )}
        </div>
      ))}
      <div className="flex justify-start">
        <button className="btn" onClick={() => setDraft((d) => ({ ...d, channels: [...d.channels, { id: '', name: '', port: nextPort(), gateway: '', color: colors[d.channels.length % colors.length], kind: 'roip', group: 0, home: '', homeRfss: 0, txKey: '' }] }))}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a channel</button>
      </div>
      {usesP25 && <P25Identity v={draft.p25} set={(v) => setDraft((d) => ({ ...d, p25: { ...d.p25, ...v } }))} p={p} />}
      {usesP25 && <P25Keys keys={keys} set={(k) => setDraft((d) => ({ ...d, p25: { ...d.p25, keys: k } }))} p={p} />}
      <fieldset className="space-y-3 rounded-lg border border-slate-800 p-3">
        <legend className="px-1 text-sm font-semibold text-violet-300">Radio names</legend>
        <p className="text-sm text-slate-400">Name radios by their P25 unit ID: the console, the radio log and CAD show "Engine 5" rather than a number.</p>
        {draft.aliases.map((a, i) => (
          <div key={i} className="grid items-end gap-3 md:grid-cols-[10rem_1fr_auto]">
            <Field label="Unit ID" htmlFor={`rau${i}`} error={p(`aliases[${i}].unit`)}><input id={`rau${i}`} className="input font-mono" inputMode="numeric" value={a.unit || ''} onChange={(e) => setDraft((d) => ({ ...d, aliases: d.aliases.map((x, j) => j === i ? { ...x, unit: Number(e.target.value.replace(/\D/g, '')) || 0 } : x) }))} /></Field>
            <Field label="Name" htmlFor={`ran${i}`} error={p(`aliases[${i}].name`)}><input id={`ran${i}`} className="input" value={a.name} onChange={(e) => setDraft((d) => ({ ...d, aliases: d.aliases.map((x, j) => j === i ? { ...x, name: e.target.value } : x) }))} /></Field>
            <button className="btn-ghost p-2" aria-label={`Remove the name for unit ${a.unit || i + 1}`} onClick={() => setDraft((d) => ({ ...d, aliases: d.aliases.filter((_, j) => j !== i) }))}><Trash2 size={15} className="text-rose-300" aria-hidden /></button>
          </div>
        ))}
        <button className="btn" onClick={() => setDraft((d) => ({ ...d, aliases: [...d.aliases, { unit: 0, name: '' }] }))}><Plus size={14} className="text-emerald-300" aria-hidden /> Name a radio</button>
      </fieldset>
      <fieldset className="space-y-3 rounded-lg border border-slate-800 p-3">
        <legend className="px-1 text-sm font-semibold text-violet-300">Recording and CAD</legend>
        <Toggle checked={draft.record} onChange={(c) => setDraft((d) => ({ ...d, record: c }))} label="Record every transmission (heard and sent) in the radio log" />
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Send radio events to CAD at" htmlFor="rcad" error={p('cadUrl')} hint="Who is talking where, emergencies and each transmission, POSTed as JSON. Empty sends nothing.">
            <input id="rcad" className="input font-mono" value={draft.cadUrl} placeholder="https://cad.example.gov/radio" onChange={(e) => setDraft((d) => ({ ...d, cadUrl: e.target.value.trim() }))} />
          </Field>
          <Field label="CAD's shared secret" htmlFor="rcads" hint="Sent as a bearer token. Leave empty to keep the current one.">
            <input id="rcads" className="input font-mono" type="password" autoComplete="off" value={draft.cadSecret} onChange={(e) => setDraft((d) => ({ ...d, cadSecret: e.target.value }))} />
          </Field>
        </div>
      </fieldset>
      <div className="flex justify-end">
        <button className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={15} aria-hidden /> Save</button>
      </div>
    </section>
  )
}

/** The console's identity on the P25 system, and where private calls go. */
function P25Identity({ v, set, p }: { v: RadioSettings['p25']; set: (v: Partial<RadioSettings['p25']>) => void; p: (f: string) => string | undefined }) {
  const hex = (s: string, n: number) => s.replace(/[^0-9a-fA-F]/g, '').slice(0, n).toUpperCase()
  return (
    <fieldset className="space-y-3 rounded-lg border border-violet-900/60 p-3">
      <legend className="px-1 text-sm font-semibold text-violet-300">This console on the P25 system</legend>
      <p className="text-sm text-slate-400">Talkgroup channels join group calls over the ISSI (TIA-102.BACA) as a console subsystem: ask your P25 system's administrator for these numbers, and give them this server's address and the SIP port below.</p>
      <div className="grid gap-3 md:grid-cols-4">
        <Field label="WACN" htmlFor="p25w" error={p('p25.wacn')} hint="5 hex digits."><input id="p25w" className="input font-mono" value={v.wacn} placeholder="BEE07" onChange={(e) => set({ wacn: hex(e.target.value, 5) })} /></Field>
        <Field label="System ID" htmlFor="p25s" error={p('p25.system')} hint="3 hex digits."><input id="p25s" className="input font-mono" value={v.system} placeholder="AD5" onChange={(e) => set({ system: hex(e.target.value, 3) })} /></Field>
        <Field label="This console's RFSS ID" htmlFor="p25r" error={p('p25.rfss')}><input id="p25r" className="input font-mono" inputMode="numeric" value={v.rfss || ''} onChange={(e) => set({ rfss: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
        <Field label="Dispatchers transmit as unit" htmlFor="p25u" error={p('p25.unit')} hint="Radios call the console on this unit ID too."><input id="p25u" className="input font-mono" inputMode="numeric" value={v.unit || ''} onChange={(e) => set({ unit: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
        <Field label="SIP port for the ISSI" htmlFor="p25p" error={p('p25.sipPort')}><input id="p25p" className="input font-mono" inputMode="numeric" value={v.sipPort || ''} onChange={(e) => set({ sipPort: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
        <Field label="Transmit priority" htmlFor="p25q" error={p('p25.priority')} hint="1 (lowest) to 4.">
          <select id="p25q" className="input" value={v.priority || 4} onChange={(e) => set({ priority: Number(e.target.value) })}>{[4, 3, 2, 1].map((x) => <option key={x}>{x}</option>)}</select>
        </Field>
        <div className="flex items-end md:col-span-2"><Toggle checked={v.console} onChange={(c) => set({ console: c })} label="Send the CSSI console PTT word with each request" /></div>
      </div>
      <h4 className="text-sm font-semibold text-slate-200">Private calls</h4>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="The radios' home RFSS" htmlFor="p25uh" error={p('p25.unitHome')} hint="Where private calls go. Empty: the first talkgroup's home.">
          <input id="p25uh" className="input font-mono" value={v.unitHome} placeholder="10.0.2.25:5060" onChange={(e) => set({ unitHome: e.target.value.trim() })} />
        </Field>
        <Field label="Its RFSS ID" htmlFor="p25uhi" error={p('p25.unitHomeRfss')}><input id="p25uhi" className="input font-mono" inputMode="numeric" value={v.unitHomeRfss || ''} onChange={(e) => set({ unitHomeRfss: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
        <Field label="Transmit private calls" htmlFor="p25utk" error={p('p25.unitTxKey')}>
          <select id="p25utk" className="input" value={v.unitTxKey} onChange={(e) => set({ unitTxKey: e.target.value })}>
            <option value="">Clear</option>{v.keys.filter((k) => k.id).map((k) => <option key={k.id} value={k.id}>{k.name || 'Unnamed key'} ({algNames[k.alg]})</option>)}
          </select>
        </Field>
      </div>
    </fieldset>
  )
}

/** The traffic keys the console decrypts with (and channels may transmit with). Values are never shown again. */
function P25Keys({ keys, set, p }: { keys: P25Key[]; set: (k: P25Key[]) => void; p: (f: string) => string | undefined }) {
  const edit = (i: number, v: Partial<P25Key>) => set(keys.map((k, j) => (j === i ? { ...k, ...v } : k)))
  return (
    <fieldset className="space-y-3 rounded-lg border border-sky-900/60 p-3">
      <legend className="flex items-center gap-1 px-1 text-sm font-semibold text-sky-300"><KeyRound size={14} aria-hidden /> Encryption keys</legend>
      <p className="text-sm text-slate-400">Traffic keys for encrypted talkgroups (TIA-102.AAAD): the console hears an encrypted transmission when it has the key with the transmission's algorithm and key ID, and mutes it otherwise. A key is never shown again once saved; leave it empty to keep it.</p>
      {keys.map((k, i) => (
        <div key={i} className="grid items-end gap-3 md:grid-cols-[1fr_10rem_8rem_2fr_auto]">
          <Field label="Name" htmlFor={`pkn${i}`} error={p(`p25.keys[${i}].name`)}><input id={`pkn${i}`} className="input" value={k.name} placeholder="Fire tac" onChange={(e) => edit(i, { name: e.target.value })} /></Field>
          <Field label="Algorithm" htmlFor={`pka${i}`} error={p(`p25.keys[${i}].alg`)}>
            <select id={`pka${i}`} className="input" value={k.alg} onChange={(e) => edit(i, { alg: e.target.value as P25Key['alg'], key: '' })}>
              {Object.entries(algNames).map(([v, n]) => <option key={v} value={v}>{n}</option>)}
            </select>
          </Field>
          <Field label="Key ID" htmlFor={`pki${i}`} error={p(`p25.keys[${i}].keyId`)}><input id={`pki${i}`} className="input font-mono" inputMode="numeric" value={k.keyId} onChange={(e) => edit(i, { keyId: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
          <Field label="Key" htmlFor={`pkk${i}`} error={p(`p25.keys[${i}].key`)} hint={`${keyDigits[k.alg]} hex digits.${k.id ? ' Leave empty to keep the saved key.' : ''}`}>
            <input id={`pkk${i}`} className="input font-mono" type="password" autoComplete="off" value={k.key} placeholder={k.id ? '(saved)' : ''} onChange={(e) => edit(i, { key: e.target.value.replace(/[^0-9a-fA-F]/g, '') })} />
          </Field>
          <button className="btn-ghost p-2" aria-label={`Remove key ${k.name || i + 1}`} onClick={() => set(keys.filter((_, j) => j !== i))}><Trash2 size={15} className="text-rose-300" aria-hidden /></button>
        </div>
      ))}
      <button className="btn" onClick={() => set([...keys, { id: '', name: '', alg: 'aes256', keyId: 0, key: '' }])}><Plus size={14} className="text-sky-300" aria-hidden /> Add a key</button>
    </fieldset>
  )
}
