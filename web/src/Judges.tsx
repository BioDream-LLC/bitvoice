import { BookCheck, ChevronRight, CircleCheck, CircleX, Gavel, Info, Plus, Radar, RefreshCw, ShieldAlert, Trash2, TriangleAlert, Waypoints } from 'lucide-react'
import { useState } from 'react'
import {
  useExtensionsQuery, useFraudDecisionsQuery, usePinSetsQuery, usePlanCheckQuery, useRulebooksQuery, useSchedulesQuery,
  type FraudDecision, type FraudGuard, type PlanFinding, type Rulebook, type TrunkJudging,
} from './api'
import { Badge, Empty, Field, Spinner, Toggle } from './ui'

/** Reasons shows a judge's reasons, one per line, the decision first. */
export function Reasons({ items, label = 'Why' }: { items?: string[]; label?: string }) {
  const [open, setOpen] = useState(false)
  if (!items?.length) return null
  return (
    <div className="text-xs">
      <button type="button" className="inline-flex items-center gap-1 text-sky-300 hover:underline" aria-expanded={open} onClick={() => setOpen(!open)}>
        <ChevronRight size={12} className={open ? 'rotate-90 transition-transform' : 'transition-transform'} aria-hidden /> {label}
      </button>
      {open && (
        <ul className="mt-1 space-y-0.5 border-l border-slate-700 pl-3 text-slate-400" aria-label={label}>
          {items.map((r, i) => <li key={i} className={r.startsWith('Not ') ? 'text-amber-200/80' : r.startsWith('Not measured') ? 'text-amber-300' : ''}>{r}</li>)}
        </ul>
      )}
    </div>
  )
}

const sevIcon = { error: CircleX, warning: TriangleAlert, info: Info }
const sevCls = { error: 'text-rose-300', warning: 'text-amber-300', info: 'text-sky-300' }

/** FindingList shows what the call-plan checker found, each with the situations that show it. */
export function FindingList({ items, onTry }: { items: PlanFinding[]; onTry?: (f: PlanFinding, i: number) => void }) {
  return (
    <ul className="space-y-2" aria-label="Findings">
      {items.map((f, n) => {
        const Icon = sevIcon[f.severity]
        return (
          <li key={n} className="rounded-lg border border-slate-800 bg-slate-950/50 p-3">
            <div className="flex items-start gap-2">
              <Icon size={16} className={`mt-0.5 shrink-0 ${sevCls[f.severity]}`} aria-label={f.severity} />
              <div className="min-w-0 flex-1 space-y-1">
                <p className="text-sm"><span className={`font-semibold ${sevCls[f.severity]}`}>{f.title}</span>{f.where.name && <span className="text-slate-500"> · {f.where.name}</span>}</p>
                <p className="text-sm text-slate-300">{f.says}</p>
                {f.situations.filter((s) => s.from).map((s, i) => (
                  <div key={i} className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-slate-500">
                    <span className="text-slate-400">{s.when}</span>
                    {Object.entries(s.toggles ?? {}).filter(([, on]) => on).map(([id]) => <span key={id} className="rounded bg-violet-500/15 px-1.5 text-violet-300">toggle {id} switched over</span>)}
                    <span className="font-mono text-slate-400">{s.path.join(' → ')}</span>
                    {onTry && s.at && <button type="button" className="btn-ghost px-1.5 py-0.5 text-sky-300" onClick={() => onTry(f, i)}>Try this call</button>}
                  </div>
                ))}
                {f.count > f.situations.length && <p className="text-xs text-slate-500">…in {f.count} situations in all.</p>}
              </div>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

/** PlanCheck tries every situation the call plan can be in and lists what goes wrong. */
export function PlanCheck() {
  const { data, isFetching, refetch } = usePlanCheckQuery(undefined, { refetchOnMountOrArgChange: true })
  const tryIt = (f: PlanFinding, i: number) => {
    const s = f.situations[i]
    window.dispatchEvent(new CustomEvent('bv-simulate', { detail: { from: s.from, to: s.to, trunk: s.trunk ?? '', at: s.at.slice(0, 16), toggles: s.toggles } }))
    document.getElementById('sim-h')?.scrollIntoView({ behavior: 'smooth' })
  }
  return (
    <section className="card mb-6 space-y-4 p-5" aria-labelledby="pc-h">
      <div className="flex flex-wrap items-center gap-3">
        <h2 id="pc-h" className="flex items-center gap-2 font-semibold text-slate-100"><Radar size={17} className="text-emerald-300" aria-hidden /> Check every situation</h2>
        <button className="btn ml-auto" onClick={() => refetch()} disabled={isFetching}>{isFetching ? <Spinner label="Checking" /> : <RefreshCw size={14} aria-hidden />} Check again</button>
      </div>
      <p className="text-sm text-slate-400">
        BitVoice tries every situation your call plan can be in, not a sample: every combination of open and closed your schedules reach in the coming year,
        every combination of day/night toggles, every incoming number, every key a caller can press and every no-answer, and every extension dialling the emergency numbers.
        It lists calls that end nowhere, loops, things nothing routes to, and emergency calls that rest on one trunk.
      </p>
      {!data ? <Spinner label="Checking every situation" /> : (
        <>
          <p className="flex flex-wrap items-center gap-2 text-sm" aria-live="polite">
            <Badge status={data.errors ? 'fail' : data.warnings ? 'warn' : 'ok'}>{data.errors ? `${data.errors} to fix` : data.warnings ? `${data.warnings} to look at` : 'Nothing to fix'}</Badge>
            <span className="text-slate-400">{data.situations.toLocaleString()} calls tried across {data.times} combinations of hours{data.truncated ? ' (stopped early: a very large plan)' : ''}.</span>
          </p>
          {data.findings.length > 0 && <FindingList items={data.findings} onTry={tryIt} />}
        </>
      )}
    </section>
  )
}

const actionStatus = { allow: 'ok', alert: 'warn', challenge: 'warn', block: 'fail' } as const
const actionText = { allow: 'Let through', alert: 'Let through, reported', challenge: 'Asked for a PIN', block: 'Refused' }

/** FraudDecisions lists the calls out of the country the toll-fraud guard judged, with its reasons. */
export function FraudDecisions() {
  const { data } = useFraudDecisionsQuery(undefined, { pollingInterval: 30000 })
  return (
    <section className="mb-8" aria-labelledby="fd-h">
      <h2 id="fd-h" className="mb-3 flex items-center gap-2 text-lg font-semibold text-slate-100"><ShieldAlert size={18} className="text-amber-300" aria-hidden /> Calls abroad the toll-fraud guard judged</h2>
      {!data?.items.length ? <Empty icon={ShieldAlert} title="None yet">Switch on the toll-fraud guard in Settings; every call out of the country is then judged before it is placed.</Empty> : (
        <ul className="space-y-2">
          {data.items.map((d: FraudDecision, i) => (
            <li key={i} className="card space-y-1 p-3 text-sm">
              <div className="flex flex-wrap items-center gap-2">
                <Badge status={actionStatus[d.action]}>{actionText[d.action]}</Badge>
                <span className="font-mono text-sky-300">{d.ext}</span><span className="text-slate-500">→</span><span className="font-mono text-violet-300">{d.number}</span>
                <span className="text-xs text-slate-500">+{d.country}{d.outside ? ' · placed from outside' : ''} · {new Date(d.at).toLocaleString()}</span>
              </div>
              <Reasons items={d.reasons} />
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/** FraudGuardSettings edits the toll-fraud guard. */
export function FraudGuardSettings({ value, onChange, p }: { value?: FraudGuard; onChange: (v: FraudGuard) => void; p: (f: string) => string | undefined }) {
  const { data: schedules } = useSchedulesQuery()
  const { data: pins } = usePinSetsQuery()
  const { data: exts } = useExtensionsQuery()
  const g: FraudGuard = { on: false, schedule: '', burst: 0, allowedCountries: [], pinSet: '', exceptions: [], ...value }
  const set = (v: Partial<FraudGuard>) => onChange({ ...g, ...v })
  const ex = g.exceptions ?? []
  const setEx = (i: number, v: Partial<FraudGuard['exceptions'][number]>) => set({ exceptions: ex.map((x, j) => (j === i ? { ...x, ...v } : x)) })
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="fg-h">
      <h2 id="fg-h" className="flex items-center gap-2 font-semibold text-slate-100"><Gavel size={17} className="text-amber-300" aria-hidden /> Toll-fraud guard</h2>
      <p className="text-sm text-slate-400">
        Judges every call out of the country before it is placed, and says why: a first call to a country in 90 days is reported; outside business hours or placed from outside (a dial-in, a forward) it needs a PIN;
        many calls abroad in ten minutes, or a first call abroad while someone is guessing passwords, is refused. Emergency calls are never stopped. When something cannot be measured, the guard asks for a PIN rather than assume the call is safe.
      </p>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={g.on} onChange={(v) => set({ on: v })} label="Toll-fraud guard" /> Judge calls out of the country</label>
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label="Business hours" htmlFor="fgs" error={p('security.fraudGuard.schedule')} hint="Outside them, a first call abroad needs a PIN. None leaves hours out.">
          <select id="fgs" className="input" value={g.schedule} onChange={(e) => set({ schedule: e.target.value })}>
            <option value="">None</option>{schedules?.items.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </Field>
        <Field label="PIN asked for" htmlFor="fgp" error={p('security.fraudGuard.pinSet')} hint="With none, a call that needs a PIN is refused.">
          <select id="fgp" className="input" value={g.pinSet} onChange={(e) => set({ pinSet: e.target.value })}>
            <option value="">None</option>{pins?.items.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </Field>
        <Field label="Calls abroad in 10 minutes" htmlFor="fgb" error={p('security.fraudGuard.burst')} hint="More are refused from one extension. 0 means 5.">
          <input id="fgb" type="number" min={0} className="input w-28" value={g.burst} onChange={(e) => set({ burst: Number(e.target.value) })} />
        </Field>
      </div>
      <Field label="Countries always allowed" htmlFor="fgc" error={p('security.fraudGuard.allowedCountries')} hint="Calling codes, separated by commas: 44 for the UK, 1876 for Jamaica.">
        <input id="fgc" className="input font-mono" value={(g.allowedCountries ?? []).join(', ')} onChange={(e) => set({ allowedCountries: e.target.value.split(/[,\s]+/).filter(Boolean) })} placeholder="44, 33" />
      </Field>
      <div className="space-y-2">
        <h3 className="text-sm font-semibold text-emerald-300">Exceptions</h3>
        <p className="text-xs text-slate-500">An extension excused until a day (travel, a project abroad). Many calls in a short time and an attack are never excused: a stolen password looks approved too.</p>
        {ex.map((x, i) => (
          <div key={i} className="flex flex-wrap items-start gap-2">
            <div>
              <select aria-label={`Exception ${i + 1}: extension`} className="input w-48" value={x.ext} onChange={(e) => setEx(i, { ext: e.target.value })}>
                <option value="">Choose…</option>{exts?.items.map((e) => <option key={e.id} value={e.number}>{e.number} {e.name}</option>)}
              </select>
              {p(`security.fraudGuard.exceptions[${i}].ext`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`security.fraudGuard.exceptions[${i}].ext`)}</p>}
            </div>
            <div>
              <input aria-label={`Exception ${i + 1}: until`} type="date" className="input w-40" value={x.until} onChange={(e) => setEx(i, { until: e.target.value })} />
              {p(`security.fraudGuard.exceptions[${i}].until`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`security.fraudGuard.exceptions[${i}].until`)}</p>}
            </div>
            <input aria-label={`Exception ${i + 1}: note`} className="input w-56" placeholder="Why" value={x.note} onChange={(e) => setEx(i, { note: e.target.value })} />
            <button type="button" className="btn-ghost p-2" aria-label={`Remove exception ${i + 1}`} onClick={() => set({ exceptions: ex.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
          </div>
        ))}
        <button type="button" className="btn" onClick={() => set({ exceptions: [...ex, { ext: '', until: '', note: '' }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add an exception</button>
      </div>
    </section>
  )
}

/** TrunkJudgingSettings edits the trunk judge. */
export function TrunkJudgingSettings({ value, onChange, p }: { value?: TrunkJudging; onChange: (v: TrunkJudging) => void; p: (f: string) => string | undefined }) {
  const j: TrunkJudging = { off: false, failures: 0, windowMinutes: 0, ...value }
  const set = (v: Partial<TrunkJudging>) => onChange({ ...j, ...v })
  return (
    <section className="card space-y-4 p-5" aria-labelledby="tj-h">
      <h2 id="tj-h" className="flex items-center gap-2 font-semibold text-slate-100"><Waypoints size={17} className="text-sky-300" aria-hidden /> Trunk health judge</h2>
      <p className="text-sm text-slate-400">
        Every 15 seconds, decides whether each trunk is used in its place or tried after the others: a provider that stops answering, or keeps refusing calls, moves behind the others while another trunk works.
        It does not trust a failing keep-alive while calls still get through, and when every trunk fails at once it blames this system's own link and moves nothing. A trunk is never taken out altogether.
      </p>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!j.off} onChange={(v) => set({ off: !v })} label="Trunk health judge" /> Judge trunks</label>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Failed calls in a row" htmlFor="tjf" error={p('system.trunkJudging.failures')} hint="Failing on the provider's side, before it counts as refusing calls. 0 means 3.">
          <input id="tjf" type="number" min={0} className="input w-28" value={j.failures} onChange={(e) => set({ failures: Number(e.target.value) })} />
        </Field>
        <Field label="Within (minutes)" htmlFor="tjw" error={p('system.trunkJudging.windowMinutes')} hint="0 means 10.">
          <input id="tjw" type="number" min={0} className="input w-28" value={j.windowMinutes} onChange={(e) => set({ windowMinutes: Number(e.target.value) })} />
        </Field>
      </div>
    </section>
  )
}

const kindCls = { fact: 'text-sky-300', conclusion: 'text-violet-300', action: 'text-emerald-300' }

/** Rulebooks shows the judges' rulebooks, each checked against every combination of its facts. */
export function Rulebooks() {
  const { data } = useRulebooksQuery()
  return (
    <section className="mb-8" aria-labelledby="rb-h">
      <h2 id="rb-h" className="mb-1 flex items-center gap-2 text-lg font-semibold text-slate-100"><BookCheck size={18} className="text-emerald-300" aria-hidden /> How BitVoice decides</h2>
      <p className="mb-3 text-sm text-slate-400">The rulebooks BitVoice's judges use. Every answer comes with its reasons, "cannot determine" is an answer, and something not measured is never taken for safety. Each rulebook is checked in every combination of its facts before it is used.</p>
      <div className="grid gap-3 lg:grid-cols-2">
        {data?.items.map((b: Rulebook) => (
          <details key={b.id} className="card p-4 text-sm">
            <summary className="cursor-pointer list-none">
              <span className="font-semibold text-slate-100">{b.name}</span>{' '}
              {(b.check.problems ?? []).length === 0
                ? <span className="inline-flex items-center gap-1 text-xs text-emerald-300"><CircleCheck size={12} aria-hidden /> checked in all {b.check.situations.toLocaleString()} situations</span>
                : <span className="text-xs text-rose-300">{b.check.problems?.length} problems</span>}
              <p className="mt-1 text-slate-400">{b.about}</p>
            </summary>
            <ul className="mt-3 space-y-1">
              {b.rules.map((r) => (
                <li key={r.name}><span className={`font-semibold ${kindCls[r.kind]}`}>{r.kind}</span> <span className="text-slate-300">{r.text}</span>
                  {r.does && <span className="text-xs text-slate-500"> → {b.choices.find((c) => c.does === r.does)?.text}</span>}
                  {b.attacks.filter((a) => a.from === r.name).length > 0 && <span className="text-xs text-amber-300/80"> (overrules: {b.attacks.filter((a) => a.from === r.name).map((a) => b.rules.find((x) => x.name === a.to)?.text).join('; ')})</span>}
                </li>
              ))}
            </ul>
            <p className="mt-2 text-xs text-slate-500">When it cannot decide: {b.choices.find((c) => c.does === b.cautious)?.text}. When nothing applies: {b.choices.find((c) => c.does === b.default)?.text}.</p>
          </details>
        ))}
      </div>
    </section>
  )
}
