import { ChevronRight, CircleCheck, CircleX, FlaskConical, Play, TriangleAlert } from 'lucide-react'
import { useState } from 'react'
import { errorOf, useSimulateMutation, useTrunksQuery, type ApiErrorBody, type SimResult, type SimStep } from './api'
import { Field, problemFor } from './ui'

/** now gives the current local time as a datetime-local value. */
const now = () => {
  const d = new Date()
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset())
  return d.toISOString().slice(0, 16)
}

/** Simulator traces where a call would go, at a chosen time, without ringing anything. */
export function Simulator() {
  const { data: trunks } = useTrunksQuery()
  const [run, { isLoading }] = useSimulateMutation()
  const [v, setV] = useState({ from: '', to: '', trunk: '', at: now(), assumePhones: false })
  const [res, setRes] = useState<SimResult | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const p = (f: string) => problemFor(err?.problems, f)
  const go = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      setRes(await run(v).unwrap())
      setErr(null)
    } catch (x) {
      setErr(errorOf(x))
      setRes(null)
    }
  }
  return (
    <section className="card mb-6 space-y-4 p-5" aria-labelledby="sim-h">
      <h2 id="sim-h" className="flex items-center gap-2 font-semibold text-slate-100"><FlaskConical size={17} className="text-violet-300" aria-hidden /> Try a call</h2>
      <p className="text-sm text-slate-400">Pick who calls, the number they dial and when, and see exactly where the call would go: through hours, scripts, menus and the rest. Nothing rings.</p>
      <form noValidate className="grid items-start gap-3 sm:grid-cols-2 lg:grid-cols-5" onSubmit={go}>
        <Field label="Caller" htmlFor="simf" error={p('from')} hint="An extension, or an outside number.">
          <input id="simf" className="input font-mono" inputMode="tel" placeholder="+12125550123" value={v.from} onChange={(e) => { const x = e.target.value; setV((v0) => ({ ...v0, from: x })) }} />
        </Field>
        <Field label="Dials" htmlFor="simt" error={p('to')} hint="Your number, an extension or a code.">
          <input id="simt" className="input font-mono" inputMode="tel" placeholder="+12125550100" value={v.to} onChange={(e) => { const x = e.target.value; setV((v0) => ({ ...v0, to: x })) }} />
        </Field>
        <Field label="When" htmlFor="sima" error={p('at')} hint="In the system's time zone.">
          <input id="sima" type="datetime-local" className="input" value={v.at} onChange={(e) => { const x = e.target.value; setV((v0) => ({ ...v0, at: x })) }} />
        </Field>
        <Field label="Arrives on" htmlFor="simk" error={p('trunk')} hint="Calls from outside.">
          <select id="simk" className="input" value={v.trunk} onChange={(e) => { const x = e.target.value; setV((v0) => ({ ...v0, trunk: x })) }}>
            <option value="">Any trunk</option>
            {trunks?.items.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        </Field>
        <div className="flex flex-col gap-2 pt-6">
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={v.assumePhones} onChange={(e) => { const x = e.target.checked; setV((v0) => ({ ...v0, assumePhones: x })) }} />
            Assume every phone is connected
          </label>
          <button className="btn btn-primary w-fit" disabled={isLoading}><Play size={14} aria-hidden /> Try it</button>
        </div>
      </form>
      {err && !err.problems?.length && <p role="alert" className="text-sm text-rose-300">{err.message}</p>}
      {res && (
        <div className="space-y-3" aria-live="polite">
          <p className="text-sm text-slate-300">
            <span className="text-slate-500">{res.direction} call, {res.at}: </span>
            <span className={res.outcome.startsWith('Fails') || res.outcome.startsWith('Refused') ? 'text-rose-300' : 'text-emerald-300'}>{res.outcome}</span>
          </p>
          <ol aria-label="The path the call takes"><StepView st={res.start} /></ol>
        </div>
      )}
    </section>
  )
}

const icons = { ok: CircleCheck, warn: TriangleAlert, fail: CircleX }
const colours = { ok: 'text-emerald-300', warn: 'text-amber-300', fail: 'text-rose-300' }

function StepView({ st }: { st: SimStep }) {
  const Icon = icons[st.status] ?? CircleCheck
  return (
    <li className="space-y-2">
      <div className="flex items-start gap-2 rounded-lg border border-slate-800 bg-slate-950/50 p-3">
        <Icon size={16} className={`mt-0.5 shrink-0 ${colours[st.status]}`} aria-label={st.status === 'ok' ? 'Works' : st.status === 'warn' ? 'Look at this' : 'Fails here'} />
        <div className="min-w-0 text-sm">
          <p className="font-medium text-slate-100">{st.name} <span className="text-xs font-normal text-slate-500">{st.kind}</span></p>
          <p className="text-slate-300">{st.says}</p>
        </div>
      </div>
      {!!st.branches?.length && (
        <ol className="ml-4 space-y-2 border-l border-slate-800 pl-4">
          {st.branches.map((b, i) => (
            <li key={i}>
              {b.taken ? (
                <>
                  <p className="mb-1 flex items-center gap-1 text-xs font-medium text-sky-300"><ChevronRight size={12} aria-hidden /> {b.when} <span className="text-emerald-300">(this call)</span></p>
                  <ol><StepView st={b.step} /></ol>
                </>
              ) : (
                <details>
                  <summary className="cursor-pointer text-xs text-slate-400 hover:text-slate-200">{b.when}: {b.step.name}</summary>
                  <ol className="mt-1"><StepView st={b.step} /></ol>
                </details>
              )}
            </li>
          ))}
        </ol>
      )}
    </li>
  )
}
