import { Ban, Bot, Check, Database, FlaskConical, Lightbulb, Plus, ShieldCheck, ShieldQuestion, ShieldX, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useSaveSettingsMutation, useSettingsQuery, useSpamSuggestionsQuery, useTestSpamDatabaseMutation, type Reputation, type Screening as ScreeningSettings, type ScreenNumber } from './api'
import { attempt, useIsAdmin } from './Editor'
import { DestinationPicker } from './Extensions'
import { Field } from './ui'

const noDatabase: Reputation = { url: '', username: '', password: '', field: '', spamAt: 1, spamText: '', action: 'label', timeoutMs: 1500, cacheHours: 24 }

/** Nomorobo's spam score through Twilio Lookup (the Nomorobo add-on installed in the Twilio account). */
const nomoroboViaTwilio: Partial<Reputation> = {
  url: 'https://lookups.twilio.com/v1/PhoneNumbers/{e164}?AddOns=nomorobo_spamscore',
  field: 'add_ons.results.nomorobo_spamscore.result.score', spamAt: 1, spamText: '',
}

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
  const list = (name: 'blocked' | 'allowed' | 'dismissed', title: string, colour: string) => {
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
      <UnknownCallers value={value} onChange={onChange} error={error} />
      <SpamDatabase value={value.reputation ?? noDatabase} onChange={(r) => onChange({ reputation: { ...(value.reputation ?? noDatabase), ...r } })} error={error} />
      <Suggestions />
      {(value.dismissed?.length ?? 0) > 0 && list('dismissed', 'Not spam (never suggested again)', 'text-slate-300')}
      <Field label="STI certificate authorities (optional)" htmlFor="scrr" error={error('system.screening.stiRoots')} hint="PEM certificates a caller's signing certificate must chain to. Empty: the signature is checked, not who issued it.">
        <textarea id="scrr" rows={3} className="input font-mono text-xs" value={value.stiRoots ?? ''} onChange={(e) => onChange({ stiRoots: e.target.value })} placeholder="-----BEGIN CERTIFICATE-----" />
      </Field>
    </section>
  )
}


/** UnknownCallers: what happens to callers BitVoice does not know: ring, the robot test, or straight somewhere. */
function UnknownCallers({ value, onChange, error }: { value: ScreeningSettings; onChange: (v: Partial<ScreeningSettings>) => void; error: (f: string) => string | undefined }) {
  const mode = value.unknown || 'ring'
  return (
    <div className="space-y-3 rounded-lg border border-slate-800 p-4">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-sky-300"><Bot size={15} aria-hidden /> Callers BitVoice does not know</h3>
      <p className="text-xs text-slate-400">
        A caller is <span className="text-emerald-300">known</span> when the number is always allowed, in the contacts, was called from here in the last year,
        or passed the robot test in the last 90 days. Everyone else is unknown. Emergency calls, and every call within an hour of an emergency call, ring as usual.
      </p>
      <div className="grid gap-4 md:grid-cols-2">
        <Field label="Unknown callers" htmlFor="scrunknown" error={error('system.screening.unknown')}
          hint={mode === 'test' ? 'They hear "To be connected, please press" and a digit (it changes each call). Robocallers do not press it.' : mode === 'send' ? 'Nothing rings: they go straight to the place below.' : 'They ring like everyone else.'}>
          <select id="scrunknown" className="input" value={mode} onChange={(e) => onChange({ unknown: e.target.value as ScreeningSettings['unknown'] })}>
            <option value="ring">Ring as usual</option>
            <option value="test">Robot test first (press a key)</option>
            <option value="send">Send somewhere else (voicemail, say)</option>
          </select>
        </Field>
        {mode !== 'ring' && (
          <DestinationPicker label={mode === 'send' ? 'Unknown callers go to' : 'Callers who fail the test go to'} value={value.unknownTo ?? null}
            onChange={(d) => onChange({ unknownTo: d })} required={mode === 'send'} noSchedules emptyLabel={mode === 'send' ? undefined : 'Goodbye (hang up)'}
            error={error('system.screening.unknownTo')} />
        )}
      </div>
      {mode === 'test' && <p className="text-xs text-amber-300">The test needs its words recorded: in Sounds, the screening prompts and the numbers 1 to 9. Until they are, those calls ring as usual and Health says so.</p>}
    </div>
  )
}

/** SpamDatabase: a paid reputation service asked about each unknown caller. */
function SpamDatabase({ value, onChange, error }: { value: Reputation; onChange: (v: Partial<Reputation>) => void; error: (f: string) => string | undefined }) {
  const admin = useIsAdmin()
  const [number, setNumber] = useState('')
  const [test, { isLoading }] = useTestSpamDatabaseMutation()
  const [result, setResult] = useState<string | null>(null)
  const f = (k: string) => error('system.screening.reputation.' + k)
  const on = value.url !== ''
  return (
    <div className="space-y-3 rounded-lg border border-slate-800 p-4">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-violet-300"><Database size={15} aria-hidden /> Spam database</h3>
      <p className="text-xs text-slate-400">
        BitVoice can ask a reputation service about each unknown caller before the call rings: Nomorobo through Twilio Lookup, or any service that answers in JSON.
        These services <span className="text-amber-300">charge per lookup</span>; known callers are never looked up, and each answer is kept for a day.
        If the service is slow or fails, the call rings as usual.
      </p>
      <div className="flex flex-wrap gap-2">
        <button type="button" className="btn" onClick={() => onChange(nomoroboViaTwilio)}><Database size={14} className="text-violet-300" aria-hidden /> Use Nomorobo through Twilio</button>
        {on && <button type="button" className="btn-ghost" onClick={() => onChange({ ...noDatabase })}><Trash2 size={14} className="text-rose-300" aria-hidden /> Switch off</button>}
      </div>
      <Field label="Address" htmlFor="repurl" error={f('url')} hint="https, with {e164} (+12125550142) or {number} (as the provider sent it) where the caller's number goes. Empty: off.">
        <input id="repurl" className="input font-mono text-xs" value={value.url} placeholder="https://lookups.twilio.com/v1/PhoneNumbers/{e164}?AddOns=nomorobo_spamscore" onChange={(e) => onChange({ url: e.target.value })} />
      </Field>
      {on && (
        <>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Username" htmlFor="repuser" hint="For Twilio, the account SID."><input id="repuser" className="input" autoComplete="off" value={value.username} onChange={(e) => onChange({ username: e.target.value })} /></Field>
            <Field label="Password" htmlFor="reppass" hint="For Twilio, the auth token."><input id="reppass" type="password" className="input" autoComplete="new-password" value={value.password} onChange={(e) => onChange({ password: e.target.value })} /></Field>
          </div>
          <div className="grid gap-4 md:grid-cols-3">
            <Field label="Verdict field" htmlFor="repfield" error={f('field')} hint="Where the answer says it, a dotted path."><input id="repfield" className="input font-mono text-xs" value={value.field} onChange={(e) => onChange({ field: e.target.value })} /></Field>
            <Field label="Spam at score" htmlFor="repat" error={f('spamAt')} hint="A number this high or higher is spam (true always is)."><input id="repat" type="number" step="any" className="input w-28" value={value.spamAt} onChange={(e) => onChange({ spamAt: Number(e.target.value) })} /></Field>
            <Field label="Or spam when the text is" htmlFor="reptext" hint="For services that answer in words, such as BLOCK."><input id="reptext" className="input w-36" value={value.spamText} onChange={(e) => onChange({ spamText: e.target.value })} /></Field>
          </div>
          <div className="grid gap-4 md:grid-cols-3">
            <Field label="When it says spam" htmlFor="repact" error={f('action')}>
              <select id="repact" className="input" value={value.action || 'label'} onChange={(e) => onChange({ action: e.target.value as Reputation['action'] })}>
                <option value="label">Ring, labelled</option><option value="test">Robot test first</option><option value="block">Refuse</option>
              </select>
            </Field>
            <Field label="Wait at most (ms)" htmlFor="reptime" error={f('timeoutMs')} hint="100 to 3000."><input id="reptime" type="number" className="input w-28" value={value.timeoutMs} onChange={(e) => onChange({ timeoutMs: Number(e.target.value) })} /></Field>
            <Field label="Keep answers (hours)" htmlFor="repcache" error={f('cacheHours')} hint="0 for a day; -1 never."><input id="repcache" type="number" className="input w-28" value={value.cacheHours} onChange={(e) => onChange({ cacheHours: Number(e.target.value) })} /></Field>
          </div>
          {admin && (
            <div className="flex flex-wrap items-end gap-2">
              <Field label="Try a number (as saved)" htmlFor="reptry"><input id="reptry" className="input w-44 font-mono" placeholder="+12125550142" value={number} onChange={(e) => setNumber(e.target.value)} /></Field>
              <button type="button" className="btn" disabled={!number || isLoading} onClick={async () => {
                const r: { ok: boolean; spam?: boolean; said?: string; error?: string } = await test(number).unwrap().catch(() => ({ ok: false, error: 'The test could not be run.' }))
                setResult(r.ok ? `${r.spam ? 'Spam' : 'Not spam'}: ${r.said || 'not listed'}` : `It did not work: ${r.error}`)
              }}><FlaskConical size={14} className="text-amber-300" aria-hidden /> Test</button>
              {result && <p role="status" className={`text-sm ${result.startsWith('Spam') ? 'text-rose-300' : result.startsWith('Not') ? 'text-emerald-300' : 'text-amber-300'}`}>{result}</p>}
            </div>
          )}
        </>
      )}
    </div>
  )
}

/** Suggestions: numbers the call history says may be spam, for a person to block or dismiss. Nothing is blocked without one. */
function Suggestions() {
  const admin = useIsAdmin()
  const { data } = useSpamSuggestionsQuery()
  const { data: settings } = useSettingsQuery()
  const [save] = useSaveSettingsMutation()
  const items = data?.items ?? []
  const change = (number: string, list: 'blocked' | 'dismissed', note: string, done: string) => attempt(async () => {
    if (!settings) return
    const sc = settings.system.screening
    return save({ ...settings, system: { ...settings.system, screening: { ...sc, [list]: [...(sc[list] ?? []), { number, note }] } } }).unwrap()
  }, done)
  return (
    <div className="space-y-3 rounded-lg border border-slate-800 p-4">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-amber-300"><Lightbulb size={15} aria-hidden /> Suggested from your calls</h3>
      <p className="text-xs text-slate-400">
        Numbers from the last 30 days that call again and again and are over within seconds (three calls or more, nearly all under 10 seconds), or that failed the robot test twice,
        and never had a real conversation. Known numbers are left out. Nothing is blocked until you say so.
      </p>
      {items.length === 0 ? <p className="text-sm text-slate-500">No suggestions.</p> : (
        <ul className="divide-y divide-slate-800">
          {items.map((x) => (
            <li key={x.number} className="flex flex-wrap items-center gap-3 py-2">
              <span className="font-mono text-sm text-slate-100">{x.number}</span>
              {x.name && <span className="text-sm text-slate-300">{x.name}</span>}
              <span className="text-xs text-slate-400">{x.why}</span>
              {admin && (
                <span className="ml-auto flex gap-2">
                  <button type="button" className="btn px-2 py-1" aria-label={`Block ${x.number}`} onClick={() => change(x.number, 'blocked', 'Suggested: ' + x.why, `${x.number} is blocked`)}><Ban size={13} className="text-rose-300" aria-hidden /> Block</button>
                  <button type="button" className="btn-ghost px-2 py-1" aria-label={`${x.number} is not spam`} onClick={() => change(x.number, 'dismissed', 'Not spam', `${x.number} will not be suggested again`)}><Check size={13} className="text-emerald-300" aria-hidden /> Not spam</button>
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
