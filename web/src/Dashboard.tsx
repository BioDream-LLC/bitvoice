import { clsx } from 'clsx'
import {
  Activity, AlertTriangle, Disc, DoorOpen, Headphones, Pause, Play, ArrowRight, Gauge, LayoutDashboard, Phone, PhoneCall, PhoneOff, Server, ShieldAlert, Wifi, Lock, LockOpen, Printer, Video,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { toast } from 'sonner'
import { EmergencySwitch } from './EmergencyMode'
import { CallerCard } from './Crm'
import { Link } from 'react-router-dom'
import { errorOf, useExtensionsQuery, useSetSupervisionMutation, useSuperviseMutation, useCallsQuery, useCdrQuery, useHangupMutation, useOpenDoorMutation, usePauseRecordingMutation, useHealthQuery, useMeQuery, useSettingsQuery, useStatsQuery, useSystemQuery, type Call } from './api'
import { Chart } from './Chart'
import { AttestBadge } from './Screening'
import { Copyable } from './Setup'
import { Badge, Chip, Direction, Page, formatDuration, mosStatus, radioKeys } from './ui'
import { useNow } from './useNow'

export function Dashboard() {
  const { data: stats } = useStatsQuery()
  const { data: sys } = useSystemQuery()
  // A server behind a cloud's NAT is reached at its public address, not the private one it sees on its own card.
  const { data: settings } = useSettingsQuery()
  const reachAt = settings?.system.publicAddress || sys?.addresses?.[0]
  const { data: health } = useHealthQuery()
  const { data: calls } = useCallsQuery()
  const { data: cdr } = useCdrQuery({})
  // Worst first, and emergency calling above everything else of the same weight.
  const rank = (c: { id: string; status: string }) => (c.status === 'fail' ? 0 : 2) + (c.id === '911' ? 0 : 1)
  const problems = [...(health?.items.filter((c) => c.status !== 'ok') ?? [])].sort((a, b) => rank(a) - rank(b))

  const hourly = useMemo(() => {
    const counts = new Array(24).fill(0)
    const missed = new Array(24).fill(0)
    const since = Date.now() - 24 * 3600 * 1000
    for (const r of cdr?.items ?? []) {
      const t = new Date(r.start).getTime()
      if (t < since) continue
      const h = new Date(t).getHours()
      if (r.disposition === 'answered') counts[h]++
      else missed[h]++
    }
    return { counts, missed }
  }, [cdr])

  const tiles = [
    { label: 'Calls now', value: stats?.calls ?? 0, icon: PhoneCall, color: 'text-emerald-300', bg: 'from-emerald-500/20', to: '/calls' },
    { label: 'Phones online', value: `${stats?.online ?? 0} / ${stats?.extensions ?? 0}`, icon: Wifi, color: 'text-sky-300', bg: 'from-sky-500/20', to: '/extensions' },
    { label: 'Trunks up', value: `${stats?.trunksUp ?? 0} / ${stats?.trunks ?? 0}`, icon: Server, color: 'text-teal-300', bg: 'from-teal-500/20', to: '/trunks' },
    { label: 'Blocked addresses', value: stats?.banned ?? 0, icon: ShieldAlert, color: 'text-rose-300', bg: 'from-rose-500/20', to: '/security' },
  ]

  return (
    <Page icon={LayoutDashboard} title={sys?.name || 'Dashboard'}
      intro={<>Your phone system at a glance. Phones connect to <Copyable text={`${reachAt ?? '…'}${sys && sys.sipPort !== 5060 ? ':' + sys.sipPort : ''}`} /></>}>
      <div className="mb-4"><EmergencySwitch /></div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {tiles.map((t) => (
          <Link key={t.label} to={t.to} className={clsx('card group bg-gradient-to-br to-transparent p-5 hover:border-slate-700', t.bg)}>
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium text-slate-400">{t.label}</span>
              <t.icon size={20} className={t.color} aria-hidden />
            </div>
            <div className={clsx('mt-2 text-3xl font-bold tabular-nums', t.color)}>{t.value}</div>
          </Link>
        ))}
      </div>

      {problems.length > 0 && (
        <div className="card mt-6 border-amber-800/60 p-4">
          <h2 className="mb-2 flex items-center gap-2 font-semibold text-amber-200"><AlertTriangle size={18} aria-hidden /> Needs attention</h2>
          <ul className="space-y-2">
            {problems.slice(0, 4).map((p) => (
              <li key={p.id} className="flex items-start gap-3 text-sm">
                <Badge status={p.status}>{p.status === 'fail' ? 'Problem' : 'Check'}</Badge>
                <span className="flex-1"><span className="font-medium text-slate-100">{p.title}.</span> <span className="text-slate-400">{p.detail}</span></span>
                {p.fix && <Link to={p.fix} aria-label={`Fix: ${p.title}`} className="inline-flex items-center gap-1 text-sky-300 hover:underline">Fix <ArrowRight size={14} aria-hidden /></Link>}
              </li>
            ))}
          </ul>
          {problems.length > 4 && <Link to="/health" className="mt-3 inline-flex items-center gap-1 text-sm text-sky-300 hover:underline">{problems.length - 4} more on the Health page <ArrowRight size={14} aria-hidden /></Link>}
        </div>
      )}

      <div className="mt-6 grid gap-6 lg:grid-cols-3">
        <section className="card p-5 lg:col-span-2">
          <h2 className="mb-2 flex items-center gap-2 font-semibold text-slate-100"><Activity size={18} className="text-violet-300" aria-hidden /> Calls in the last 24 hours</h2>
          <Chart label="Answered and missed calls per hour" option={{
            tooltip: { trigger: 'axis' },
            legend: { data: ['Answered', 'Missed'], top: 0, right: 0, textStyle: { color: 'inherit' } },
            xAxis: { type: 'category', data: Array.from({ length: 24 }, (_, i) => `${i}:00`), axisLine: { lineStyle: { opacity: 0.3 } } },
            yAxis: { type: 'value', minInterval: 1, splitLine: { lineStyle: { opacity: 0.15 } } },
            series: [
              { name: 'Answered', type: 'bar', stack: 'c', data: hourly.counts, itemStyle: { color: '#34d399', borderRadius: [3, 3, 0, 0] } },
              { name: 'Missed', type: 'bar', stack: 'c', data: hourly.missed, itemStyle: { color: '#fb7185', borderRadius: [3, 3, 0, 0] } },
            ],
          }} />
        </section>
        <section className="card p-5">
          <h2 className="mb-3 flex items-center gap-2 font-semibold text-slate-100"><PhoneCall size={18} className="text-emerald-300" aria-hidden /> Happening now</h2>
          {calls?.items.length ? <ul className="space-y-2">{calls.items.slice(0, 6).map((c) => <MiniCall key={c.id} c={c} />)}</ul>
            : <p className="text-sm text-slate-500"><PhoneOff size={16} className="mr-1.5 inline align-[-3px]" aria-hidden />No calls right now. Dial <span className="font-mono text-violet-300">*43</span> from a phone to test audio.</p>}
        </section>
      </div>
    </Page>
  )
}

function MiniCall({ c }: { c: Call }) {
  const now = useNow()
  const since = new Date(c.answer || c.start).getTime()
  return (
    <li className="flex items-center gap-2 rounded-lg bg-slate-950/60 p-2 text-sm">
      <Phone size={15} className={clsx(c.state === 'ringing' ? 'ringing text-amber-300' : 'text-emerald-300')} aria-hidden />
      <span className="font-mono text-sky-300">{c.from}</span>
      <ArrowRight size={12} className="text-slate-600" aria-hidden />
      <span className="truncate font-mono text-amber-300">{c.to}</span>
      <span className="ml-auto font-mono text-xs tabular-nums text-slate-400">{formatDuration((now - since) / 1000)}</span>
    </li>
  )
}

export function CallsPage() {
  // Calls on other servers do not send events here, so the list also refreshes every few seconds.
  const { data, isLoading } = useCallsQuery(undefined, { pollingInterval: 3000 })
  return (
    <Page icon={PhoneCall} title="Live calls" accent="from-emerald-400 to-teal-400"
      intro="Every call in progress, updated the moment anything changes, with audio quality for each side.">
      {isLoading ? null : !data?.items.length && !data?.elsewhere?.length ? (
        <div className="card flex flex-col items-center gap-2 p-10 text-center">
          <PhoneOff size={32} className="text-slate-600" aria-hidden />
          <p className="font-semibold text-slate-200">No calls in progress</p>
          <p className="text-sm text-slate-500">Dial <span className="font-mono text-violet-300">*43</span> from any phone for an echo test.</p>
        </div>
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">{data.items.map((c) => <CallCard key={c.id} c={c} />)}</div>
      )}
      {!!data?.elsewhere?.length && (
        <section className="mt-6" aria-labelledby="elsewhere-h">
          <h2 id="elsewhere-h" className="mb-2 text-sm font-semibold text-slate-300">On the other servers</h2>
          <ul className="card divide-y divide-slate-800" aria-label="Calls on the other servers">
            {data.elsewhere.map((c) => (
              <li key={c.server + c.id} className="flex flex-wrap items-center gap-3 px-4 py-2 text-sm">
                <span className="text-slate-200">{c.fromName || c.from}</span><span className="text-slate-500">to</span><span className="text-amber-300">{c.toName || c.to}</span>
                <span className="text-xs text-slate-500">{c.state}</span>
                <span className="ml-auto rounded bg-sky-500/15 px-1.5 text-xs text-sky-200">{c.server}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </Page>
  )
}

function CallCard({ c }: { c: Call }) {
  const now = useNow()
  const since = new Date(c.answer || c.start).getTime()
  return (
    <article className="card rise p-5">
      <div className="flex items-center justify-between">
        <Direction d={c.direction} />
        <Badge status={c.state === 'answered' ? 'ok' : c.state === 'held' || c.state === 'parked' ? 'warn' : 'info'}>
          {c.state === 'answered' ? 'Talking' : c.state === 'held' ? 'On hold' : c.state === 'parked' ? c.toName : 'Ringing'}
        </Badge>
      </div>
      <div className="mt-3 flex items-center gap-3">
        <div className="min-w-0">
          <div className="font-mono text-lg font-bold text-sky-300">{c.from}</div>
          <div className="flex items-center gap-1.5 truncate text-sm text-slate-400">{c.fromName}{c.screen && <AttestBadge attest={c.screen.attest} verified={c.screen.verified} />}</div>
        </div>
        <ArrowRight className="shrink-0 text-slate-600" aria-hidden />
        <div className="min-w-0">
          <div className="font-mono text-lg font-bold text-amber-300">{c.to}</div>
          <div className="truncate text-sm text-slate-400">{c.toName}</div>
        </div>
        <div className="ml-auto text-right">
          <div className="font-mono text-2xl tabular-nums text-slate-100">{formatDuration((now - since) / 1000)}</div>
          {c.trunk && <Chip kind="trunk">{c.trunk}</Chip>}
        </div>
      </div>
      {c.briefing && <p className="mt-3 rounded bg-violet-500/10 px-3 py-2 text-sm text-violet-200" role="note"><span className="font-semibold text-violet-300">Briefing: </span>{c.briefing}</p>}
      {c.direction === 'inbound' && <div className="mt-3"><CallerCard number={c.from} /></div>}
      <div className="mt-4 grid gap-2 sm:grid-cols-2">
        {c.legs.map((l, i) => (
          <div key={i} className="rounded-lg bg-slate-950/60 p-3 text-xs">
            <div className="flex items-center justify-between">
              <span className="flex min-w-0 items-center gap-1.5 truncate font-semibold text-slate-200">
                {l.secure ? <Lock size={11} className="shrink-0 text-emerald-300" aria-label="Encrypted media" /> : <LockOpen size={11} className="shrink-0 text-slate-600" aria-label="Unencrypted media" />}
                {l.party || '—'}
                {l.transport && <span className={`text-[10px] font-normal ${l.transport === 'TLS' ? 'text-emerald-300' : 'text-slate-500'}`}>{l.transport}</span>}
                {l.video && <span className="flex items-center gap-0.5 text-[10px] font-normal text-sky-300"><Video size={11} aria-hidden /> video</span>}
                {l.fax && <span className="flex items-center gap-0.5 text-[10px] font-normal text-violet-300"><Printer size={11} aria-hidden /> fax (T.38)</span>}
              </span>
              <Badge status={mosStatus(l.media.mos)}><Gauge size={11} aria-hidden /> {l.media.mos ? l.media.mos.toFixed(1) : '—'}</Badge>
            </div>
            <div className="mt-1 grid grid-cols-3 gap-1 text-slate-500">
              <span>Codec <span className="text-violet-300">{l.codec || '—'}</span></span>
              <span>Lost <span className={l.media.lost ? 'text-rose-300' : 'text-emerald-300'}>{l.media.lost}</span></span>
              <span>Jitter <span className="text-sky-300">{l.media.jitterMs.toFixed(1)} ms</span></span>
            </div>
          </div>
        ))}
      </div>
      <div className="flex flex-wrap gap-2">
        {c.state !== 'ringing' && c.state !== 'parked' && c.legs.length >= 2 && <SuperviseControl call={c} />}
        {c.recording && <RecordingButton id={c.id} paused={c.recording === 'paused'} />}
        {c.door && <DoorButton id={c.id} />}
        <HangupButton id={c.id} />
      </div>
    </article>
  )
}

const modes = [
  { id: 'listen', label: 'Listen', hint: 'Heard by nobody' },
  { id: 'whisper', label: 'Whisper', hint: 'Heard by the agent only' },
  { id: 'barge', label: 'Barge', hint: 'Heard by both' },
] as const

/** Join a call from a supervisor's phone: listen, whisper or barge, and switch between them. */
function SuperviseControl({ call }: { call: Call }) {
  const { data: me } = useMeQuery()
  const { data: exts } = useExtensionsQuery()
  const [supervise, { isLoading }] = useSuperviseMutation()
  const [setMode] = useSetSupervisionMutation()
  const [open, setOpen] = useState(false)
  const [sup, setSup] = useState(() => localStorage.getItem('bitvoice.supervisor') ?? '')
  const [mode, setM] = useState<'listen' | 'whisper' | 'barge'>('listen')
  if (me?.role !== 'admin') return null
  if (call.supervision) {
    return (
      <div className="mt-4 flex items-center gap-1" role="radiogroup" aria-label="Supervision">
        <Headphones size={14} className="text-violet-300" aria-hidden />
        {modes.map((m, i) => (
          <button key={m.id} role="radio" aria-checked={call.supervision === m.id} title={m.hint}
            {...radioKeys(i, modes.map((x) => x.id), call.supervision as 'listen', (v) => setMode({ id: call.id, mode: v }))}
            className={clsx('rounded-md px-2 py-1 text-xs', call.supervision === m.id ? 'bg-violet-900 text-violet-100' : 'text-slate-400 hover:bg-slate-800')}
            onClick={() => setMode({ id: call.id, mode: m.id }).unwrap().catch((e) => toast.error(errorOf(e).message))}>{m.label}</button>
        ))}
      </div>
    )
  }
  if (!open) return <button className="btn mt-4 text-violet-300" onClick={() => setOpen(true)}><Headphones size={15} aria-hidden /> Supervise</button>
  const parties = [call.from, call.to]
  return (
    <form className="mt-4 flex flex-wrap items-end gap-2" onSubmit={async (e) => {
      e.preventDefault()
      localStorage.setItem('bitvoice.supervisor', sup)
      try { await supervise({ id: call.id, supervisor: sup, mode }).unwrap(); toast.success(`Ringing ${sup}: answer to join`); setOpen(false) } catch (err) { toast.error(errorOf(err).message) }
    }}>
      <label className="text-xs text-slate-400">From phone
        <select className="input mt-1 py-1 text-sm" value={sup} onChange={(e) => setSup(e.target.value)} required>
          <option value="">Choose</option>
          {exts?.items.filter((x) => !parties.includes(x.number)).map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
        </select>
      </label>
      <label className="text-xs text-slate-400">As
        <select className="input mt-1 py-1 text-sm" value={mode} onChange={(e) => setM(e.target.value as typeof mode)}>
          {modes.map((m) => <option key={m.id} value={m.id}>{m.label}: {m.hint.toLowerCase()}</option>)}
        </select>
      </label>
      <button className="btn btn-primary" disabled={isLoading || !sup}>Ring me</button>
      <button type="button" className="btn" onClick={() => setOpen(false)}>Cancel</button>
    </form>
  )
}

function RecordingButton({ id, paused }: { id: string; paused: boolean }) {
  const { data: me } = useMeQuery()
  const [pause, { isLoading }] = usePauseRecordingMutation()
  const label = paused ? 'Resume recording' : 'Pause recording'
  const btn = (
    <button className={clsx('btn mt-4', paused ? 'text-amber-300' : 'text-rose-300')} disabled={isLoading || me?.role !== 'admin'} aria-pressed={paused}
      onClick={async () => { try { await pause({ id, paused: !paused }).unwrap() } catch (e) { toast.error(errorOf(e).message) } }}>
      {paused ? <Play size={15} aria-hidden /> : <Pause size={15} aria-hidden />} {label}
    </button>
  )
  return (
    <span className="inline-flex items-center gap-2">
      <span className={clsx('mt-4 inline-flex items-center gap-1 text-xs', paused ? 'text-amber-300' : 'text-rose-300')} role="status">
        <Disc size={12} className={paused ? '' : 'animate-pulse'} aria-hidden /> {paused ? 'Recording paused' : 'Recording'}
      </span>
      {me?.role === 'admin' && btn}
    </span>
  )
}

function DoorButton({ id }: { id: string }) {
  const { data: me } = useMeQuery()
  const [open, { isLoading }] = useOpenDoorMutation()
  if (me?.role !== 'admin') return null
  return (
    <button className="btn mt-4 text-amber-300" disabled={isLoading} onClick={async () => {
      try { await open(id).unwrap(); toast.success('Door opened') } catch (e) { toast.error(errorOf(e).message) }
    }}><DoorOpen size={15} aria-hidden /> Open door</button>
  )
}

function HangupButton({ id }: { id: string }) {
  const { data: me } = useMeQuery()
  const [hangup] = useHangupMutation()
  if (me?.role !== 'admin') return null
  return <button className="btn btn-danger mt-4" onClick={() => hangup(id)}><PhoneOff size={15} aria-hidden /> End call</button>
}
