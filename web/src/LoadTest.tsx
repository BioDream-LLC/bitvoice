import { Gauge, Play, Square, Timer } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { errorOf, useExtensionsQuery, useLoadTestQuery, useMenusQuery, useQueuesQuery, useRingGroupsQuery, useSettingsQuery,
  useStartLoadTestMutation, useStopLoadTestMutation, type ApiErrorBody, type LoadRun } from './api'
import { Chart } from './Chart'
import { useIsAdmin } from './Editor'
import { Badge, ErrorBox, Field, Page, problemFor, relTime } from './ui'

const blank = { from: '', to: '', answerAs: '', calls: 20, rate: 2, concurrent: 5, holdSeconds: 10 }

function Kpi({ label, value, color, hint }: { label: string; value: string | number; color: string; hint?: string }) {
  return (
    <div className="card p-4" title={hint}>
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className={`mt-1 font-mono text-2xl font-semibold ${color}`}>{value}</div>
    </div>
  )
}

/** The built-in call generator: real calls into BitVoice, for load and regression tests. */
export function LoadTest() {
  const admin = useIsAdmin()
  const { data, refetch } = useLoadTestQuery()
  const run = data?.run
  const running = !!run?.running
  useEffect(() => {
    if (!running) return
    const t = setInterval(refetch, 1000)
    return () => clearInterval(t)
  }, [running, refetch])
  const { data: exts } = useExtensionsQuery()
  const { data: groups } = useRingGroupsQuery()
  const { data: queues } = useQueuesQuery()
  const { data: menus } = useMenusQuery()
  const { data: settings } = useSettingsQuery()
  const [start, { isLoading: starting }] = useStartLoadTestMutation()
  const [stop] = useStopLoadTestMutation()
  const [f, setF] = useState(blank)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const set = (v: Partial<typeof blank>) => setF((f0) => ({ ...f0, ...v }))
  // Active calls over time for the chart, sampled at each refresh of a running test.
  const [series, setSeries] = useState<[number, number][]>([])
  const startedAt = useRef('')
  useEffect(() => {
    if (!run) return
    if (startedAt.current !== run.started) { startedAt.current = run.started; setSeries([]) }
    if (run.running) setSeries((s) => [...s.slice(-600), [Math.round((Date.now() - Date.parse(run.started)) / 1000), run.active]])
  }, [run])

  const targets = [
    settings ? { n: settings.system.echoNumber, label: 'echo test' } : null,
    settings ? { n: settings.system.voicemailNumber, label: 'voicemail' } : null,
    ...(exts?.items ?? []).map((x) => ({ n: x.number, label: x.name })),
    ...(groups?.items ?? []).map((g) => ({ n: g.number, label: 'ring group ' + g.name })),
    ...(queues?.items ?? []).map((q) => ({ n: q.number, label: 'queue ' + q.name })),
    ...(menus?.items ?? []).filter((m) => m.number).map((m) => ({ n: m.number, label: 'menu ' + m.name })),
  ].filter((x): x is { n: string; label: string } => !!x && !!x.n)
  const p = (k: string) => problemFor(err?.problems, k)
  const go = async () => {
    setErr(null)
    try { await start(f).unwrap() } catch (e) { setErr(errorOf(e)) }
  }
  const pct = run ? Math.round((100 * (run.answered + run.failed)) / Math.max(1, run.plan.calls)) : 0

  return (
    <Page icon={Gauge} title="Call generator" accent="from-orange-300 to-rose-300"
      intro="Places real calls into BitVoice from this computer, with real audio, to see how many it carries and how well, or to check after a change that calls still go where they should. It only calls numbers inside BitVoice, never trunks or emergency numbers.">
      <div className="grid gap-6 lg:grid-cols-[22rem_1fr]">
        <form className="card space-y-3 p-5" onSubmit={(e) => { e.preventDefault(); go() }} aria-labelledby="lg-h">
          <h2 id="lg-h" className="font-semibold text-slate-100">Test</h2>
          {err && <ErrorBox message={err.message} />}
          <fieldset disabled={!admin || running} className="space-y-3">
            <Field label="Calls come from" htmlFor="lgf" error={p('from')} hint="Its own phones are not rung; its limit on calls at once does not apply.">
              <select id="lgf" className="input" value={f.from} onChange={(e) => set({ from: e.target.value })}>
                <option value="">Choose an extension</option>
                {exts?.items.map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
              </select>
            </Field>
            <Field label="Number to call" htmlFor="lgt" error={p('to')}>
              <input id="lgt" className="input font-mono" list="lgt-list" value={f.to} onChange={(e) => set({ to: e.target.value.trim() })} placeholder={settings?.system.echoNumber} />
              <datalist id="lgt-list">{targets.map((x) => <option key={x.n} value={x.n}>{x.label}</option>)}</datalist>
            </Field>
            <Field label="Answer as" htmlFor="lga" error={p('answerAs')} hint="The generator registers as this extension and answers when it rings, so calls to people can be tested without them.">
              <select id="lga" className="input" value={f.answerAs} onChange={(e) => set({ answerAs: e.target.value })}>
                <option value="">Nobody (the number answers by itself)</option>
                {exts?.items.filter((x) => x.number !== f.from).map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
              </select>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Calls" htmlFor="lgn" error={p('calls')}><input id="lgn" type="number" min={1} max={data?.limits.calls} className="input" value={f.calls} onChange={(e) => set({ calls: Number(e.target.value) })} /></Field>
              <Field label="At once" htmlFor="lgc" error={p('concurrent')}><input id="lgc" type="number" min={1} max={data?.limits.concurrent} className="input" value={f.concurrent} onChange={(e) => set({ concurrent: Number(e.target.value) })} /></Field>
              <Field label="New per second" htmlFor="lgr" error={p('rate')}><input id="lgr" type="number" min={0.1} step={0.1} max={data?.limits.rate} className="input" value={f.rate} onChange={(e) => set({ rate: Number(e.target.value) })} /></Field>
              <Field label="Each lasts (s)" htmlFor="lgh" error={p('holdSeconds')}><input id="lgh" type="number" min={0} max={data?.limits.holdSeconds} className="input" value={f.holdSeconds} onChange={(e) => set({ holdSeconds: Number(e.target.value) })} /></Field>
            </div>
          </fieldset>
          {admin && (running
            ? <button type="button" className="btn w-full justify-center text-rose-300" onClick={() => stop()}><Square size={15} aria-hidden /> Stop</button>
            : <button className="btn btn-primary w-full justify-center" disabled={starting}><Play size={15} aria-hidden /> Start</button>)}
        </form>
        <div className="space-y-4">
          {!run ? <div className="card p-8 text-center text-slate-500">No test has run since BitVoice started.</div> : <Results run={run} pct={pct} series={series} />}
        </div>
      </div>
    </Page>
  )
}

function Results({ run, pct, series }: { run: LoadRun; pct: number; series: [number, number][] }) {
  const failures = Object.entries(run.failures).sort((a, b) => b[1] - a[1])
  const mosColor = run.mos >= 4 ? 'text-emerald-300' : run.mos >= 3.6 ? 'text-amber-300' : 'text-rose-300'
  return (
    <>
      <div className="card flex flex-wrap items-center gap-3 p-4" role="status" aria-live="polite">
        <Badge status={run.running ? 'info' : run.failed ? 'warn' : 'ok'}>{run.running ? 'Running' : run.stopped ? 'Stopped' : 'Finished'}</Badge>
        <span className="text-sm text-slate-300">{run.plan.calls} calls from <span className="font-mono text-sky-300">{run.plan.from}</span> to <span className="font-mono text-sky-300">{run.plan.to}</span>{run.plan.answerAs && <> answered as <span className="font-mono text-sky-300">{run.plan.answerAs}</span></>}, {run.plan.concurrent} at once, {run.plan.rate}/s, {run.plan.holdSeconds} s each</span>
        <span className="ml-auto text-xs text-slate-500">started {relTime(run.started)}</span>
        <div className="h-2 w-full overflow-hidden rounded-full bg-slate-800" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} aria-label="Calls done">
          <div className="h-full bg-gradient-to-r from-sky-500 to-emerald-400 transition-all" style={{ width: `${pct}%` }} />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Kpi label="Answered" value={run.answered} color="text-emerald-300" />
        <Kpi label="Failed" value={run.failed} color={run.failed ? 'text-rose-300' : 'text-slate-400'} />
        <Kpi label="In progress" value={run.active} color="text-sky-300" />
        <Kpi label="Audio score" value={run.mos ? run.mos.toFixed(2) : '–'} color={mosColor} hint="Estimated MOS from the audio lost: 4.4 is perfect G.711, under 3.6 people notice." />
      </div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Kpi label="Answer time, median" value={`${run.setupMs.p50.toFixed(0)} ms`} color="text-violet-300" />
        <Kpi label="Answer time, 95%" value={`${run.setupMs.p95.toFixed(0)} ms`} color="text-violet-300" />
        <Kpi label="Audio lost" value={`${run.lossPct.toFixed(1)} %`} color={run.lossPct > 1 ? 'text-amber-300' : 'text-emerald-300'} hint={`${run.received} of ${run.sent} packets came back`} />
        <Kpi label="Packets sent" value={run.sent} color="text-slate-300" />
      </div>
      {series.length > 1 && (
        <div className="card p-4">
          <h3 className="mb-1 flex items-center gap-2 text-sm text-slate-300"><Timer size={14} aria-hidden /> Calls in progress</h3>
          <Chart label="Calls in progress over time" height={180} option={{
            tooltip: { trigger: 'axis' }, xAxis: { type: 'value', name: 's', min: 0 }, yAxis: { type: 'value', minInterval: 1 },
            series: [{ type: 'line', data: series, showSymbol: false, smooth: true, areaStyle: { opacity: 0.15 }, color: '#38bdf8' }],
          }} />
        </div>
      )}
      {failures.length > 0 && (
        <div className="card p-4">
          <h3 className="mb-2 text-sm font-medium text-rose-300">Why calls failed</h3>
          <table className="w-full text-sm"><thead className="sr-only"><tr><th>Reason</th><th>Calls</th></tr></thead>
            <tbody>{failures.map(([k, v]) => <tr key={k} className="border-t border-slate-800"><td className="py-1.5 font-mono text-slate-200">{k}</td><td className="py-1.5 text-right font-mono text-rose-300">{v}</td></tr>)}</tbody>
          </table>
        </div>
      )}
    </>
  )
}
