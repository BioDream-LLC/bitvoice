import { Crown, Plus, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useSaveSettingsMutation, useSettingsQuery, type ApiErrorBody, type VIP } from './api'
import { useIsAdmin } from './Editor'
import { useDraft } from './useDraft'
import { StaleBanner, problemFor } from './ui'

/** VIPs: callers who go to the front of every queue. */
export function Vips() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const { draft, setDraft, reset, stale } = useDraft<VIP[]>(data ? (data.system.vips ?? []) : undefined)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.vips' + f)
  const set = (i: number, v: Partial<VIP>) => setDraft((d) => d.map((x, j) => (j === i ? { ...x, ...v } : x)))
  const doSave = async () => {
    try { const s = await save({ ...data, system: { ...data.system, vips: draft } }).unwrap(); reset(s.system.vips ?? []); setErr(null); toast.success('VIPs saved') }
    catch (e) { setErr((e as { data?: ApiErrorBody }).data ?? { message: 'The VIPs could not be saved.' }) }
  }
  return (
    <section className="card mt-6 space-y-3 p-5" aria-labelledby="vip-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="vip-h" className="flex items-center gap-2 font-semibold text-slate-100"><Crown size={17} className="text-amber-300" aria-hidden /> VIP callers</h2>
      <p className="text-sm text-slate-400">These callers go to the front of every queue, after any VIP already waiting. A number matches however it is written.</p>
      {draft.length > 0 && (
        <ul className="space-y-2" aria-label="VIP callers">
          {draft.map((v, i) => (
            <li key={i} className="flex flex-wrap items-start gap-2">
              <div>
                <input aria-label={`VIP ${i + 1}: number`} className="input w-48 font-mono" inputMode="tel" placeholder="+12125550100" value={v.number} disabled={!admin} onChange={(e) => set(i, { number: e.target.value })} />
                {p(`[${i}].number`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`[${i}].number`)}</p>}
              </div>
              <input aria-label={`VIP ${i + 1}: who`} className="input w-56" placeholder="Acme Corp, head office" value={v.name} disabled={!admin} onChange={(e) => set(i, { name: e.target.value })} />
              {admin && <button type="button" className="btn-ghost p-2" aria-label={`Remove VIP ${i + 1}`} onClick={() => setDraft((d) => d.filter((_, j) => j !== i))}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>}
            </li>
          ))}
        </ul>
      )}
      {admin && (
        <div className="flex gap-2">
          <button type="button" className="btn" onClick={() => setDraft((d) => [...d, { number: '', name: '' }])}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a VIP</button>
          <button type="button" className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={14} aria-hidden /> Save</button>
        </div>
      )}
    </section>
  )
}
