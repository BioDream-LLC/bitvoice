import { BellRing, Coins, Plus, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { useCostsQuery, useSaveSettingsMutation, useSettingsQuery, type ApiErrorBody, type Budget, type CostLine, type Costs as CostSettings } from './api'
import { Chart } from './Chart'
import { useIsAdmin } from './Editor'
import { useDraft } from './useDraft'
import { Empty, Field, Page, StaleBanner, problemFor } from './ui'

const thisMonth = () => new Date().toISOString().slice(0, 7)
const amount = (cur: string, v: number) => `${cur} ${v.toFixed(2)}`
const blank: CostSettings = { currency: '', budgets: [], hourlyLimit: 0, alertEmail: '' }

/** Costs: what calls cost this month by department, person and trunk, budgets, and the runaway-spend alarm. */
export function Costs() {
  const admin = useIsAdmin()
  const [month, setMonth] = useState(thisMonth())
  const { data } = useCostsQuery(month)
  const cur = data?.currency ?? 'USD'
  return (
    <Page icon={Coins} title="Costs" accent="from-amber-300 to-emerald-300"
      intro="What calls cost, from each trunk's prices: by department, person and trunk, against budgets, with an alarm when calls out suddenly cost much more than usual."
      actions={<input type="month" aria-label="Month" className="input w-44" value={month} max={thisMonth()} onChange={(e) => e.target.value && setMonth(e.target.value)} />}>
      {data && (
        <div className="space-y-6">
          <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
            <p><span className="text-sm text-slate-500">Spent in {month}: </span><span className="font-mono text-2xl text-amber-200">{amount(cur, data.total)}</span></p>
            <p className="text-sm text-slate-400">{data.calls} calls with a cost</p>
            {data.unpriced > 0 && <p className="text-sm text-amber-300">{data.unpriced} answered calls out had no price: add prices on <Link className="underline" to="/trunks">Trunks</Link>.</p>}
          </div>
          {data.budgets.length > 0 && (
            <ul className="grid gap-3 md:grid-cols-2" aria-label="Budgets">
              {data.budgets.map((b) => {
                const colour = b.percent >= 100 ? 'bg-rose-400' : b.percent >= 80 ? 'bg-amber-400' : 'bg-emerald-400'
                const text = b.percent >= 100 ? 'text-rose-300' : b.percent >= 80 ? 'text-amber-300' : 'text-emerald-300'
                return (
                  <li key={b.id} className="card p-4">
                    <div className="flex justify-between text-sm"><span className="text-slate-200">{b.department || 'All calls'}</span>
                      <span className={text}>{amount(cur, b.spent)} of {amount(cur, b.monthly)} ({b.percent}%)</span></div>
                    <div className="mt-2 h-2 overflow-hidden rounded bg-slate-800" role="progressbar" aria-label={`${b.department || 'All calls'} budget used`} aria-valuenow={Math.min(100, b.percent)} aria-valuemin={0} aria-valuemax={100}>
                      <div className={`h-full ${colour}`} style={{ width: `${Math.min(100, b.percent)}%` }} />
                    </div>
                  </li>
                )
              })}
            </ul>
          )}
          {data.calls === 0 ? <Empty icon={Coins} title="Nothing spent this month">Costs appear once calls go through a trunk with prices (Trunks, a trunk, Call prices).</Empty> : (
            <>
              <section className="card p-4" aria-label="Spend by day">
                <Chart height={180} label={`Spend by day in ${month}`} option={{
                  grid: { left: 50, right: 10, top: 10, bottom: 24 },
                  tooltip: { trigger: 'axis', valueFormatter: (v: number) => amount(cur, v) },
                  xAxis: { type: 'category', data: data.days.map((d) => d.key.slice(8)) },
                  yAxis: { type: 'value' },
                  series: [{ type: 'bar', data: data.days.map((d) => d.cost), itemStyle: { color: '#fcd34d' } }],
                }} />
              </section>
              <div className="grid gap-4 lg:grid-cols-3">
                <Lines title="By department" lines={data.departments} cur={cur} />
                <Lines title="By person" lines={data.people} cur={cur} />
                <Lines title="By trunk" lines={data.trunks} cur={cur} />
              </div>
              <section className="card p-4">
                <h2 className="mb-2 font-semibold text-slate-100">Most expensive calls</h2>
                <table className="w-full text-sm">
                  <thead className="text-left text-xs text-slate-500"><tr><th className="font-normal">When</th><th className="font-normal">From</th><th className="font-normal">To</th><th className="font-normal">Price</th><th className="font-normal">Minutes</th><th className="text-right font-normal">Cost</th></tr></thead>
                  <tbody>
                    {data.top.map((c) => (
                      <tr key={c.id} className="border-t border-slate-800">
                        <td className="py-1 text-slate-400">{new Date(c.start).toLocaleString()}</td>
                        <td className="text-slate-200">{c.fromName || c.from}</td>
                        <td className="font-mono text-sky-300">{c.to}</td>
                        <td className="text-slate-400">{c.rate}{c.charged && <span className="block text-xs text-amber-200">the phone company said {c.charged}</span>}</td>
                        <td className="font-mono text-slate-300">{(c.billsec / 60).toFixed(1)}</td>
                        <td className="text-right font-mono text-amber-200">{amount(cur, c.cost ?? 0)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </section>
            </>
          )}
          {admin && <CostSettingsCard />}
        </div>
      )}
    </Page>
  )
}

function Lines({ title, lines, cur }: { title: string; lines: CostLine[]; cur: string }) {
  return (
    <section className="card p-4">
      <h2 className="mb-2 font-semibold text-slate-100">{title}</h2>
      <ul className="space-y-1 text-sm">
        {lines.map((l) => (
          <li key={l.key} className="flex justify-between gap-2">
            <span className="truncate text-slate-300">{l.name} <span className="text-xs text-slate-500">{l.calls} calls, {l.minutes} min</span></span>
            <span className="font-mono text-amber-200">{amount(cur, l.cost)}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}

function CostSettingsCard() {
  const { data } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const { draft, setDraft, reset, stale } = useDraft<CostSettings>(data ? (data.system.costs ?? blank) : undefined)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.costs.' + f)
  const set = (v: Partial<CostSettings>) => setDraft((d) => ({ ...d, ...v }))
  const budgets = draft.budgets ?? []
  const setBudget = (i: number, v: Partial<Budget>) => set({ budgets: budgets.map((b, j) => (j === i ? { ...b, ...v } : b)) })
  const doSave = async () => {
    try { const s = await save({ ...data, system: { ...data.system, costs: draft } }).unwrap(); reset(s.system.costs); setErr(null); toast.success('Cost settings saved') }
    catch (e) { setErr((e as { data?: ApiErrorBody }).data ?? { message: 'The cost settings could not be saved.' }) }
  }
  const num = (s: string) => Math.max(0, Number(s.replace(',', '.')) || 0)
  return (
    <section className="card space-y-4 p-5" aria-labelledby="cost-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="cost-h" className="flex items-center gap-2 font-semibold text-slate-100"><BellRing size={17} className="text-amber-300" aria-hidden /> Budgets and alarms</h2>
      <p className="text-sm text-slate-400">A budget sends an alert (in BitVoice, and by email when <Link className="text-sky-300 underline" to="/settings">email</Link> is set up) at 80% and again at 100% of a month's spend. The alarm catches calls out that suddenly cost much more than usual, the usual sign of toll fraud.</p>
      <div className="grid gap-4 md:grid-cols-3">
        <Field label="Currency" htmlFor="ccur" error={p('currency')} hint="Three letters: USD, EUR, GBP. Prices on trunks are in it.">
          <input id="ccur" className="input w-28 font-mono uppercase" maxLength={3} placeholder="USD" value={draft.currency} onChange={(e) => set({ currency: e.target.value.toUpperCase() })} />
        </Field>
        <Field label="Alarm: calls out cost more than this in an hour" htmlFor="chl" error={p('hourlyLimit')} hint="0 for no alarm.">
          <input id="chl" className="input w-32 font-mono" inputMode="decimal" defaultValue={draft.hourlyLimit} onChange={(e) => set({ hourlyLimit: num(e.target.value) })} />
        </Field>
        <Field label="Send alerts to" htmlFor="cae" error={p('alertEmail')} hint="Besides any budget's own address.">
          <input id="cae" type="email" className="input" value={draft.alertEmail} onChange={(e) => set({ alertEmail: e.target.value.trim() })} />
        </Field>
      </div>
      {budgets.length > 0 && (
        <table className="w-full text-sm" aria-label="Budgets to set">
          <thead className="text-left text-xs text-slate-500"><tr><th className="font-normal">Department</th><th className="font-normal">Each month</th><th className="font-normal">Email</th><th /></tr></thead>
          <tbody>
            {budgets.map((b, i) => (
              <tr key={b.id || `new-${i}`} className="align-top">
                <td className="pr-2 pb-2"><input aria-label={`Budget ${i + 1}: department`} className="input" placeholder="Every call" value={b.department} onChange={(e) => setBudget(i, { department: e.target.value })} />
                  {p(`budgets[${i}].department`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`budgets[${i}].department`)}</p>}</td>
                <td className="pr-2 pb-2"><input aria-label={`Budget ${i + 1}: each month`} className="input w-32 font-mono" inputMode="decimal" defaultValue={b.monthly || ''} onChange={(e) => setBudget(i, { monthly: num(e.target.value) })} />
                  {p(`budgets[${i}].monthly`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`budgets[${i}].monthly`)}</p>}</td>
                <td className="pr-2 pb-2"><input aria-label={`Budget ${i + 1}: email`} type="email" className="input" value={b.email} onChange={(e) => setBudget(i, { email: e.target.value.trim() })} />
                  {p(`budgets[${i}].email`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`budgets[${i}].email`)}</p>}</td>
                <td className="pb-2"><button type="button" className="btn-ghost p-2" aria-label={`Remove budget ${i + 1}`} onClick={() => set({ budgets: budgets.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="flex flex-wrap gap-2">
        <button type="button" className="btn" onClick={() => set({ budgets: [...budgets, { id: '', department: '', monthly: 0, email: '' }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a budget</button>
        <button type="button" className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={14} aria-hidden /> Save</button>
      </div>
      {err && !err.problems?.length && <p className="text-sm text-rose-300">{err.message}</p>}
    </section>
  )
}
