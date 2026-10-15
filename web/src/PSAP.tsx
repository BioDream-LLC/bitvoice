import { ScrollText, Save, Siren, Timer } from 'lucide-react'
import { useMemo, useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useCallsQuery, usePsapLogQuery, usePsapReportQuery, useSaveSettingsMutation, useSettingsQuery, type ApiErrorBody, type PSAPSettings } from './api'
import { useIsAdmin } from './Editor'
import { EmergencyCard } from './EmergencyCard'
import { useDraft } from './useDraft'
import { Badge, Empty, Field, Page, StaleBanner, problemFor } from './ui'

const blankPSAP: PSAPSettings = { agencyId: '', elementId: '', aliAddress: '', eidoUrl: '', eidoToken: '', answerTarget: 15, ttyOff: false }

export function PSAP() {
  const admin = useIsAdmin()
  const { data: calls } = useCallsQuery(undefined, { pollingInterval: 2000 })
  const live = (calls?.items ?? []).filter((c) => c.emergency)
  const [tab, setTab] = useState<'report' | 'log'>('report')
  return (
    <Page icon={Siren} title="911 calls" accent="from-rose-400 to-amber-400"
      intro="The 911 profile: calls from the emergency services network (ESInet) with the caller's location and the network's data, the answer-time report, the NENA i3 log, and what CAD is sent when a call is answered.">
      <section aria-labelledby="psl-h" className="space-y-3">
        <h2 id="psl-h" className="text-sm font-semibold text-rose-300">Now</h2>
        {!live.length ? <Empty icon={Siren} title="No 911 calls now">Calls from a trunk marked ESInet, or that bring a location, appear here as they ring.</Empty>
          : live.map((c) => <EmergencyCard key={c.id} call={c} />)}
      </section>
      <div className="mt-6 flex gap-2" role="tablist" aria-label="Reports">
        <button role="tab" aria-selected={tab === 'report'} className={tab === 'report' ? 'btn btn-primary' : 'btn'} onClick={() => setTab('report')}><Timer size={14} aria-hidden /> Answer times</button>
        <button role="tab" aria-selected={tab === 'log'} className={tab === 'log' ? 'btn btn-primary' : 'btn'} onClick={() => setTab('log')}><ScrollText size={14} aria-hidden /> i3 log</button>
      </div>
      {tab === 'report' ? <Report /> : <I3Log />}
      {admin && <PSAPSetup />}
    </Page>
  )
}

const iso = (d: Date) => d.toISOString().slice(0, 10)

function Report() {
  const [from, setFrom] = useState(iso(new Date(Date.now() - 6 * 864e5)))
  const [to, setTo] = useState(iso(new Date()))
  const { data: r } = usePsapReportQuery({ from, to })
  const labels = ['0-10 s', '10-15 s', '15-20 s', '20-40 s', 'over 40 s']
  const most = Math.max(1, ...(r?.buckets ?? [1]))
  return (
    <section className="card mt-3 space-y-4 p-5" aria-label="Answer times">
      <div className="flex flex-wrap items-end gap-3">
        <Field label="From" htmlFor="psf"><input id="psf" type="date" className="input" value={from} onChange={(e) => setFrom(e.target.value)} /></Field>
        <Field label="To" htmlFor="pst"><input id="pst" type="date" className="input" value={to} onChange={(e) => setTo(e.target.value)} /></Field>
      </div>
      {!r || !r.calls ? <p className="text-sm text-slate-400">No 911 calls in these days.</p> : <>
        <div className="grid gap-3 sm:grid-cols-4">
          <Stat label={`Answered within ${r.target} s`} value={`${r.withinTarget.toFixed(1)}%`} status={r.withinTarget >= 90 ? 'ok' : 'fail'} note="goal 90%" />
          <Stat label="Within 20 s" value={`${r.within20.toFixed(1)}%`} status={r.within20 >= 95 ? 'ok' : 'warn'} note="goal 95%" />
          <Stat label="Average to answer" value={`${r.average.toFixed(1)} s`} note={`longest ${Math.round(r.longest)} s`} />
          <Stat label="Calls" value={String(r.calls)} note={`${r.answered} answered, ${r.abandoned} abandoned`} status={r.abandoned ? 'warn' : 'ok'} />
        </div>
        <div className="space-y-1" aria-label="Calls by time to answer">
          {r.buckets.map((n, i) => (
            <div key={i} className="flex items-center gap-3 text-sm">
              <span className="w-20 text-slate-400">{labels[i]}</span>
              <div className="h-3 flex-1 rounded bg-slate-800"><div className={`h-3 rounded ${i < 2 ? 'bg-emerald-500' : i < 3 ? 'bg-amber-500' : 'bg-rose-500'}`} style={{ width: `${(100 * n) / most}%` }} /></div>
              <span className="w-10 text-right font-mono text-slate-300">{n}</span>
            </div>
          ))}
        </div>
        {r.byTaker.length > 0 && (
          <table className="w-full text-sm"><thead className="text-left text-xs text-slate-500"><tr><th className="py-1">Call-taker</th><th>Calls</th><th>Average to answer</th></tr></thead>
            <tbody>{r.byTaker.map((t) => <tr key={t.ext} className="border-t border-slate-800"><td className="py-1"><span className="font-mono text-amber-300">{t.ext}</span> {t.name}</td><td>{t.calls}</td><td>{t.average.toFixed(1)} s</td></tr>)}</tbody></table>
        )}
      </>}
    </section>
  )
}

function Stat({ label, value, note, status }: { label: string; value: string; note?: string; status?: 'ok' | 'warn' | 'fail' }) {
  const color = status === 'ok' ? 'text-emerald-300' : status === 'warn' ? 'text-amber-300' : status === 'fail' ? 'text-rose-300' : 'text-sky-200'
  return <div className="rounded-lg border border-slate-800 p-3"><div className="text-xs text-slate-500">{label}</div><div className={`text-2xl font-semibold ${color}`}>{value}</div>{note && <div className="text-xs text-slate-500">{note}</div>}</div>
}

function I3Log() {
  const [day, setDay] = useState(iso(new Date()))
  const { data } = usePsapLogQuery(day, { pollingInterval: 10000 })
  const days = useMemo(() => Array.from(new Set([iso(new Date()), ...(data?.days ?? [])])), [data])
  return (
    <section className="card mt-3 space-y-3 p-5" aria-label="i3 log">
      <Field label="Day (UTC)" htmlFor="psd"><select id="psd" className="input w-48" value={day} onChange={(e) => setDay(e.target.value)}>{days.map((d) => <option key={d}>{d}</option>)}</select></Field>
      {!data?.items.length ? <p className="text-sm text-slate-400">No events on this day.</p> : (
        <table className="w-full text-sm"><thead className="text-left text-xs text-slate-500"><tr><th className="py-1">Time</th><th>Event</th><th>Call</th><th>From</th><th>Agent</th></tr></thead>
          <tbody>{data.items.map((e, i) => (
            <tr key={i} className="border-t border-slate-800">
              <td className="py-1 font-mono text-xs text-slate-400">{new Date(e.timestamp).toLocaleTimeString()}</td>
              <td><Badge status={e.logEventType.startsWith('Eido') && e.extension?.ok === false ? 'fail' : 'info'}>{e.logEventType.replace(/LogEvent$/, '')}</Badge></td>
              <td className="font-mono text-xs text-slate-500">{e.callId?.split(':')[4]}</td>
              <td className="font-mono text-sky-300">{e.from}</td>
              <td className="text-slate-300">{e.agencyAgentId}</td>
            </tr>))}</tbody></table>
      )}
    </section>
  )
}

function PSAPSetup() {
  const { data } = useSettingsQuery()
  const server = useMemo(() => (data ? { ...blankPSAP, ...(data.system.psap ?? {}) } : undefined), [data])
  const { draft, setDraft, reset, stale } = useDraft<PSAPSettings>(server)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [save, { isLoading }] = useSaveSettingsMutation()
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.psap.' + f)
  const set = (v: Partial<PSAPSettings>) => setDraft((d) => ({ ...d, ...v }))
  const doSave = async () => {
    try { const s = await save({ ...data, system: { ...data.system, psap: draft } }).unwrap(); reset({ ...blankPSAP, ...(s.system.psap ?? {}) }); setErr(null); toast.success('911 settings saved') }
    catch (e) { setErr(errorOf(e)) }
  }
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="pss-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="pss-h" className="font-semibold text-slate-100">911 settings</h2>
      <p className="text-sm text-slate-400">Mark the trunk from your ESInet provider as <span className="text-slate-200">ESInet</span> (Trunks), and send its calls to your call-takers' queue or ring group in Call routing.</p>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="Agency ID" htmlFor="psa" error={p('agencyId')} hint="Your agency's domain, as i3 names agencies."><input id="psa" className="input" value={draft.agencyId} placeholder="psap.county.example.gov" onChange={(e) => set({ agencyId: e.target.value.trim() })} /></Field>
        <Field label="Element ID" htmlFor="pse" hint="This system's name in the i3 log; blank uses the server's name."><input id="pse" className="input" value={draft.elementId} onChange={(e) => set({ elementId: e.target.value.trim() })} /></Field>
        <Field label="Answer goal (seconds)" htmlFor="psg" error={p('answerTarget')} hint="NENA: 90% of calls within 15 s."><input id="psg" className="input font-mono" inputMode="numeric" value={draft.answerTarget || ''} onChange={(e) => set({ answerTarget: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
      </div>
      <label className="flex items-center gap-2 text-sm text-slate-300">
        <input type="checkbox" checked={!draft.ttyOff} onChange={(e) => set({ ttyOff: !e.target.checked })} />
        Listen for TTYs on 911 calls <span className="text-xs text-slate-500">(deaf callers' text telephones: their text shows on the call, and replies go back as TTY tones)</span>
      </label>
      <h3 className="text-sm font-semibold text-violet-300">CAD</h3>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="ALI spill to" htmlFor="psl" error={p('aliAddress')} hint="CAD's address and port: the ALI text goes there over TCP when a call is answered."><input id="psl" className="input font-mono" value={draft.aliAddress} placeholder="10.0.0.20:4000" onChange={(e) => set({ aliAddress: e.target.value.trim() })} /></Field>
        <Field label="EIDO to" htmlFor="psu" error={p('eidoUrl')} hint="CAD's address for EIDOs (NENA-STA-024), sent by HTTPS POST."><input id="psu" className="input font-mono" value={draft.eidoUrl} placeholder="https://cad.example.gov/eido" onChange={(e) => set({ eidoUrl: e.target.value.trim() })} /></Field>
        <Field label="EIDO token" htmlFor="pst2" hint="Sent as a bearer token, if CAD asks for one. Blank keeps the saved one."><input id="pst2" type="password" autoComplete="off" className="input font-mono" value={draft.eidoToken} onChange={(e) => set({ eidoToken: e.target.value })} /></Field>
      </div>
      <div className="flex justify-end"><button className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={15} aria-hidden /> Save</button></div>
    </section>
  )
}
