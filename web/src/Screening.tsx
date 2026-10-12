import { Ban, Plus, ShieldCheck, ShieldQuestion, ShieldX, Trash2 } from 'lucide-react'
import { useSaveSettingsMutation, useSettingsQuery, type Screening as ScreeningSettings, type ScreenNumber } from './api'
import { attempt } from './Editor'
import { Field } from './ui'

/** AttestBadge shows how far a caller ID can be trusted: verified (A/B/C) or failed. */
export function AttestBadge({ attest, verified }: { attest?: string; verified?: string }) {
  if (!attest && !verified) return null
  if (verified === 'failed') return <span title="The caller ID failed verification: it may be spoofed" className="inline-flex items-center gap-1 rounded bg-rose-500/15 px-1.5 text-xs font-semibold text-rose-300"><ShieldX size={11} aria-hidden /> Not verified</span>
  if (attest === 'A' || (verified === 'passed' && !attest)) return <span title="Verified: the provider vouches the caller may use this number (STIR/SHAKEN A)" className="inline-flex items-center gap-1 rounded bg-emerald-500/15 px-1.5 text-xs font-semibold text-emerald-300"><ShieldCheck size={11} aria-hidden /> Verified</span>
  return <span title={attest === 'B' ? 'Partly verified: the provider knows the caller, not that they own the number (B)' : 'Passed on only: the provider cannot vouch for the number (C)'}
    className="inline-flex items-center gap-1 rounded bg-amber-500/15 px-1.5 text-xs font-semibold text-amber-300"><ShieldQuestion size={11} aria-hidden /> {attest === 'B' ? 'Partly verified' : 'Unverified'}</span>
}

/** BlockButton adds a caller's number to the block list. */
export function BlockButton({ number }: { number: string }) {
  const { data } = useSettingsQuery()
  const [save] = useSaveSettingsMutation()
  if (!data || !number || data.system.screening?.blocked?.some((b) => b.number === number)) return null
  const block = () => attempt(async () => {
    const sc = data.system.screening
    return save({ ...data, system: { ...data.system, screening: { ...sc, blocked: [...(sc.blocked ?? []), { number, note: 'Blocked from call history' }] } } }).unwrap()
  }, `${number} is blocked`)
  return <button className="btn-ghost p-1" title={`Block ${number}`} aria-label={`Block ${number}`} onClick={block}><Ban size={13} className="text-rose-300" aria-hidden /></button>
}

/** ScreeningSettings: what to do with spoofed, unverified and withheld numbers, and the block and allow lists. */
export function ScreeningSettings({ value, onChange, error }: { value: ScreeningSettings; onChange: (v: Partial<ScreeningSettings>) => void; error: (f: string) => string | undefined }) {
  const choice = (id: string, label: string, v: string, opts: [string, string][], set: (v: string) => void, hint: string) => (
    <Field label={label} htmlFor={id} error={error('system.screening.' + id.slice(3))} hint={hint}>
      <select id={id} className="input" value={v} onChange={(e) => set(e.target.value)}>{opts.map(([o, l]) => <option key={o} value={o}>{l}</option>)}</select>
    </Field>
  )
  const list = (name: 'blocked' | 'allowed', title: string, colour: string) => {
    const items: ScreenNumber[] = value[name] ?? []
    const set = (i: number, v: Partial<ScreenNumber>) => onChange({ [name]: items.map((x, j) => (j === i ? { ...x, ...v } : x)) })
    return (
      <div className="space-y-2">
        <h3 className={`text-sm font-semibold ${colour}`}>{title}</h3>
        {items.map((n, i) => (
          <div key={i} className="flex flex-wrap items-start gap-2">
            <div>
              <input aria-label={`${title} ${i + 1}: number`} className="input w-44 font-mono" placeholder="+1900*" value={n.number} onChange={(e) => set(i, { number: e.target.value })} />
              {error(`system.screening.${name}[${i}].number`) && <p className="field-error mt-1 text-xs text-rose-300">{error(`system.screening.${name}[${i}].number`)}</p>}
            </div>
            <input aria-label={`${title} ${i + 1}: note`} className="input w-56" placeholder="Why" value={n.note} onChange={(e) => set(i, { note: e.target.value })} />
            <button type="button" className="btn-ghost p-2" aria-label={`Remove ${title.toLowerCase()} ${i + 1}`} onClick={() => onChange({ [name]: items.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
          </div>
        ))}
        <button type="button" className="btn" onClick={() => onChange({ [name]: [...items, { number: '', note: '' }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add</button>
      </div>
    )
  }
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="scr-h">
      <h2 id="scr-h" className="flex items-center gap-2 font-semibold text-slate-100"><ShieldCheck size={17} className="text-emerald-300" aria-hidden /> Spam protection</h2>
      <p className="text-sm text-slate-400">
        Providers in the US and Canada vouch for caller IDs (<span className="text-emerald-300">STIR/SHAKEN</span>): A when they know the caller may use the number, B when they know the caller only, C when they just passed the call on.
        BitVoice reads their verdict, or checks the signed Identity header itself, and shows it on Live calls and Call history. A labelled call rings with the label before the caller's name.
        Within an hour of an emergency call nothing is refused, in case it is the emergency service calling back.
      </p>
      <div className="grid gap-4 md:grid-cols-3">
        {choice('scrfailed', 'Caller ID fails verification', value.failed || 'label', [['allow', 'Ring as usual'], ['label', 'Ring, labelled'], ['block', 'Refuse']], (v) => onChange({ failed: v as ScreeningSettings['failed'] }), 'The number was probably spoofed.')}
        {choice('scrunverified', 'Not verified (or only C)', value.unverified || 'allow', [['allow', 'Ring as usual'], ['label', 'Ring, labelled']], (v) => onChange({ unverified: v as ScreeningSettings['unverified'] }), 'Many genuine calls from abroad are not verified.')}
        {choice('scranonymous', 'Withheld numbers', value.anonymous || 'allow', [['allow', 'Ring as usual'], ['label', 'Ring, labelled'], ['block', 'Refuse']], (v) => onChange({ anonymous: v as ScreeningSettings['anonymous'] }), 'Callers who hide their number.')}
      </div>
      <Field label="Label" htmlFor="scrl" error={error('system.screening.label')} hint="Shown before a labelled caller's name; Spam? when empty.">
        <input id="scrl" className="input w-40" maxLength={20} placeholder="Spam?" value={value.label ?? ''} onChange={(e) => onChange({ label: e.target.value })} />
      </Field>
      <div className="grid gap-6 md:grid-cols-2">
        {list('blocked', 'Blocked numbers', 'text-rose-300')}
        {list('allowed', 'Always allowed', 'text-emerald-300')}
      </div>
      <p className="text-xs text-slate-500">A number, or its first digits and * for a range (+1900* is every US premium-rate number). Always allowed beats everything else.</p>
      <Field label="STI certificate authorities (optional)" htmlFor="scrr" error={error('system.screening.stiRoots')} hint="PEM certificates a caller's signing certificate must chain to. Empty: the signature is checked, not who issued it.">
        <textarea id="scrr" rows={3} className="input font-mono text-xs" value={value.stiRoots ?? ''} onChange={(e) => onChange({ stiRoots: e.target.value })} placeholder="-----BEGIN CERTIFICATE-----" />
      </Field>
    </section>
  )
}

