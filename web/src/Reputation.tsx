import { BadgeCheck, Plus, Save, ShieldAlert, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useReputationQuery, useSaveSettingsMutation, useSettingsQuery, type ApiErrorBody, type Brand, type NumberReputation } from './api'
import { useIsAdmin } from './Editor'
import { useDraft } from './useDraft'
import { Badge, Empty, Page, StaleBanner, Toggle, problemFor, type Status } from './ui'

const looks: Record<NumberReputation['status'], [Status, string]> = {
  labelled: ['fail', 'Labelled'], watch: ['warn', 'Watch it'], good: ['ok', 'Looks fine'], few: ['idle', 'Too few calls'],
}

/** Caller ID reputation: how each outgoing number is received, and how it presents itself. */
export function Reputation() {
  const [days, setDays] = useState(30)
  const { data } = useReputationQuery(days)
  return (
    <Page icon={BadgeCheck} title="Caller ID reputation" accent="from-emerald-400 to-sky-400"
      intro="Whether the numbers you call out from may be shown as “spam likely”, judged from your own calls: how often they are answered, how fast people hang up, and refusals that carriers send when a number is labelled."
      actions={<select aria-label="Time" className="input w-auto" value={days} onChange={(e) => setDays(+e.target.value)}>
        <option value={7}>Last 7 days</option><option value={30}>Last 30 days</option><option value={90}>Last 90 days</option></select>}>
      {!data ? null : data.numbers.length === 0 ? <Empty icon={BadgeCheck} title="No calls out yet">Each number you call out from appears here once it has been used.</Empty> : (
        <ul className="grid gap-3 lg:grid-cols-2" aria-label="Outgoing numbers">
          {data.numbers.map((n) => <NumberCard key={n.number} n={n} />)}
        </ul>
      )}
      <Brands />
      <section className="card mt-6 space-y-2 p-5 text-sm text-slate-300" aria-labelledby="rep-fix">
        <h2 id="rep-fix" className="flex items-center gap-2 font-semibold text-slate-100"><ShieldAlert size={16} className="text-amber-300" aria-hidden /> If a number is labelled</h2>
        <ul className="list-disc space-y-1 pl-5">
          <li>Register it on the carriers' free redress sites (in the US: the Free Caller Registry covers AT&amp;T, T-Mobile and Verizon's analytics).</li>
          <li>Make sure your provider signs your calls with full (A) attestation.</li>
          <li>Slow down: spread calls over more numbers, keep each under about 100 a day, and stop calling people who never answer.</li>
          <li>Leave voicemails and let people call back; answered call-backs mend a number's name.</li>
        </ul>
      </section>
    </Page>
  )
}

function NumberCard({ n }: { n: NumberReputation }) {
  const [st, word] = looks[n.status]
  const rate = n.answerRate < 20 ? 'text-rose-300' : n.answerRate < 35 ? 'text-amber-300' : 'text-emerald-300'
  return (
    <li className="card space-y-2 p-4">
      <div className="flex items-center gap-2">
        <h2 className="flex-1 font-mono text-slate-100">{n.number}{n.brand && <span className="ml-2 font-sans text-sm text-slate-400">{n.brand}</span>}</h2>
        <Badge status={st}>{word}</Badge>
      </div>
      <dl className="grid grid-cols-4 gap-2 text-sm">
        <div><dt className="text-xs text-slate-500">Calls</dt><dd className="text-slate-200">{n.calls}</dd></div>
        <div><dt className="text-xs text-slate-500">Answered</dt><dd className={rate}>{n.answerRate}%{n.prevRate >= 0 && <span className="ml-1 text-xs text-slate-500">was {n.prevRate}%</span>}</dd></div>
        <div><dt className="text-xs text-slate-500">Hung up at once</dt><dd className={n.shortRate >= 40 ? 'text-amber-300' : 'text-slate-200'}>{n.short}</dd></div>
        <div><dt className="text-xs text-slate-500">Refused as spam</dt><dd className={n.rejected + n.unwanted ? 'text-rose-300' : 'text-slate-200'}>{n.rejected + n.unwanted}</dd></div>
      </dl>
      <p className="text-sm text-slate-300">{n.why.join(' ')}</p>
    </li>
  )
}

/** Brands: the name, logo and reason each outgoing number shows, and whether to watch it. */
function Brands() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const { draft, setDraft, reset, stale } = useDraft<Brand[]>(data ? (data.system.brands ?? []) : undefined)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.brands' + f)
  const set = (i: number, v: Partial<Brand>) => setDraft((d) => d.map((x, j) => (j === i ? { ...x, ...v } : x)))
  const doSave = async () => {
    try { const s = await save({ ...data, system: { ...data.system, brands: draft } }).unwrap(); reset(s.system.brands ?? []); setErr(null); toast.success('Numbers saved') }
    catch (e) { setErr((e as { data?: ApiErrorBody }).data ?? { message: 'The numbers could not be saved.' }) }
  }
  const field = (i: number, f: keyof Brand, label: string, ph: string, cls: string, mono = false) => (
    <div>
      <input aria-label={`Number ${i + 1}: ${label}`} className={`input ${cls} ${mono ? 'font-mono' : ''}`} placeholder={ph} value={draft[i][f] as string} disabled={!admin} onChange={(e) => set(i, { [f]: e.target.value })} />
      {p(`[${i}].${f}`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`[${i}].${f}`)}</p>}
    </div>
  )
  return (
    <section className="card mt-6 space-y-3 p-5" aria-labelledby="brand-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="brand-h" className="flex items-center gap-2 font-semibold text-slate-100"><BadgeCheck size={17} className="text-emerald-300" aria-hidden /> Your outgoing numbers</h2>
      <p className="text-sm text-slate-400">The name is sent as the caller's name. The logo and reason go out as rich call data on trunks with <span className="text-slate-300">Send branded caller ID</span> on; carriers that support it show them on the called phone. <span className="text-slate-300">Watch</span> puts a warning on Health when a number starts to look labelled. The name people see on most phones also depends on the caller name (CNAM) your provider registers: ask them to set it.</p>
      {draft.length > 0 && (
        <ul className="space-y-2" aria-label="Outgoing numbers to brand">
          {draft.map((b, i) => (
            <li key={i} className="flex flex-wrap items-start gap-2">
              {field(i, 'number', 'number', '+12125550100', 'w-44', true)}
              {field(i, 'name', 'name', 'Acme Dental', 'w-44')}
              {field(i, 'reason', 'reason', 'Appointment reminder', 'w-52')}
              {field(i, 'logo', 'logo', 'https://…/logo.png', 'w-60', true)}
              <label className="flex items-center gap-2 pt-2 text-sm text-slate-300"><Toggle checked={b.watch} onChange={(v) => set(i, { watch: v })} label={`Number ${i + 1}: watch`} /> Watch</label>
              {admin && <button type="button" className="btn-ghost p-2" aria-label={`Remove number ${i + 1}`} onClick={() => setDraft((d) => d.filter((_, j) => j !== i))}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>}
            </li>
          ))}
        </ul>
      )}
      {admin && (
        <div className="flex gap-2">
          <button type="button" className="btn" onClick={() => setDraft((d) => [...d, { number: '', name: '', logo: '', reason: '', watch: true }])}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a number</button>
          <button type="button" className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={14} aria-hidden /> Save</button>
        </div>
      )}
    </section>
  )
}
