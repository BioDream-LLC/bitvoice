import { Calculator, Coins, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { usePriceMutation, useSettingsQuery, type Rate, type Trunk } from './api'
import { Field } from './ui'

const blankRate: Rate = { prefix: '', name: '', perMinute: 0, setup: 0, increment: 60, minimum: 0 }

const num = (s: string) => {
  const v = Number(s.replace(',', '.'))
  return Number.isFinite(v) && v >= 0 ? v : 0
}

/** RatesEditor prices calls through a trunk: rates by the number's first digits, and calls in by the minute. */
export function RatesEditor({ t, onChange, p }: { t: Trunk; onChange: (v: Partial<Trunk>) => void; p: (f: string) => string | undefined }) {
  const { data: settings } = useSettingsQuery()
  const cur = settings?.system.costs?.currency || 'USD'
  const rates = t.rates ?? []
  const setRate = (i: number, v: Partial<Rate>) => onChange({ rates: rates.map((r, j) => (j === i ? { ...r, ...v } : r)) })
  const [price, { data: tried }] = usePriceMutation()
  const [number, setNumber] = useState('')
  const [secs, setSecs] = useState('90')
  return (
    <details className="card p-4" open={rates.length > 0 || rates.some((_, i) => p(`rates[${i}].prefix`) || p(`rates[${i}].perMinute`)) || undefined}>
      <summary className="flex cursor-pointer items-center gap-2 text-sm font-semibold text-slate-300"><Coins size={14} className="text-amber-300" aria-hidden /> Call prices</summary>
      <div className="mt-4 space-y-4">
        <p className="text-sm text-slate-400">
          What this provider charges, so <span className="text-amber-300">Costs</span> can show spend by department, person and trunk. The longest
          matching start of the number (as sent to the provider, digits only) wins; leave the start empty for every other number. Prices are in {cur}.
        </p>
        {rates.length > 0 && (
          <table className="w-full text-sm" aria-label="Call prices">
            <thead className="text-left text-xs text-slate-500">
              <tr><th className="pb-1 font-normal">Number starts</th><th className="pb-1 font-normal">Name</th><th className="pb-1 font-normal">Per minute</th><th className="pb-1 font-normal">Per call</th><th className="pb-1 font-normal">Step (s)</th><th className="pb-1 font-normal">Minimum (s)</th><th /></tr>
            </thead>
            <tbody>
              {rates.map((r, i) => {
                const err = p(`rates[${i}].prefix`) || p(`rates[${i}].perMinute`) || p(`rates[${i}].increment`) || p(`rates[${i}].minimum`)
                return (
                  <tr key={`${rates.length}-${i}`} className="align-top">
                    <td className="pr-2 pb-2"><input aria-label={`Rate ${i + 1}: number starts`} className="input w-24 font-mono" inputMode="numeric" placeholder="any" value={r.prefix} onChange={(e) => setRate(i, { prefix: e.target.value.trim() })} />
                      {err && <p className="field-error mt-1 text-xs text-rose-300">{err}</p>}</td>
                    <td className="pr-2 pb-2"><input aria-label={`Rate ${i + 1}: name`} className="input" placeholder="UK mobiles" value={r.name} onChange={(e) => setRate(i, { name: e.target.value })} /></td>
                    <td className="pr-2 pb-2"><input aria-label={`Rate ${i + 1}: per minute`} className="input w-24 font-mono" inputMode="decimal" defaultValue={r.perMinute} onChange={(e) => setRate(i, { perMinute: num(e.target.value) })} /></td>
                    <td className="pr-2 pb-2"><input aria-label={`Rate ${i + 1}: per call`} className="input w-20 font-mono" inputMode="decimal" defaultValue={r.setup} onChange={(e) => setRate(i, { setup: num(e.target.value) })} /></td>
                    <td className="pr-2 pb-2"><input aria-label={`Rate ${i + 1}: billing step in seconds`} className="input w-16 font-mono" inputMode="numeric" value={r.increment || ''} placeholder="60" onChange={(e) => setRate(i, { increment: Math.trunc(num(e.target.value)) })} /></td>
                    <td className="pr-2 pb-2"><input aria-label={`Rate ${i + 1}: minimum seconds`} className="input w-16 font-mono" inputMode="numeric" value={r.minimum || ''} placeholder="0" onChange={(e) => setRate(i, { minimum: Math.trunc(num(e.target.value)) })} /></td>
                    <td className="pb-2"><button type="button" className="btn-ghost p-2" aria-label={`Remove rate ${i + 1}`} onClick={() => onChange({ rates: rates.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button></td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
        <button type="button" className="btn" onClick={() => onChange({ rates: [...rates, { ...blankRate }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a price</button>
        <Field label={`Calls in, per minute (${cur})`} htmlFor="tin" error={p('inboundPerMinute')} hint="For toll-free numbers the provider charges you to receive. 0 when calls in are free.">
          <input id="tin" className="input w-32 font-mono" inputMode="decimal" defaultValue={t.inboundPerMinute ?? 0} onChange={(e) => onChange({ inboundPerMinute: num(e.target.value) })} />
        </Field>
        <div className="flex flex-wrap items-end gap-2 border-t border-slate-800 pt-4">
          <Field label="Try a number" htmlFor="tpn"><input id="tpn" className="input w-44 font-mono" inputMode="tel" placeholder="442071234567" value={number} onChange={(e) => setNumber(e.target.value)} /></Field>
          <Field label="Seconds" htmlFor="tps"><input id="tps" className="input w-20 font-mono" inputMode="numeric" value={secs} onChange={(e) => setSecs(e.target.value)} /></Field>
          <button type="button" className="btn" disabled={!number.trim()} onClick={() => price({ trunk: t, number, seconds: Math.trunc(num(secs)) })}><Calculator size={14} className="text-sky-300" aria-hidden /> What would it cost</button>
        </div>
        {tried && (
          <p role="status" className={`text-sm ${tried.found ? 'text-emerald-300' : 'text-amber-300'}`}>
            {tried.found && tried.rate ? `${tried.rate.name || (tried.rate.prefix ? 'Starting ' + tried.rate.prefix : 'Every other number')}: ${cur} ${tried.cost?.toFixed(4)}` : 'No price covers this number, so its calls are not costed. Add a price with an empty start to cover every number.'}
          </p>
        )}
      </div>
    </details>
  )
}
