import { Building, ExternalLink, Link2, PhoneForwarded, PlugZap, Send, Sparkles, UserRound } from 'lucide-react'
import { useState } from 'react'
import { errorOf, useCallerQuery, useCrmTestMutation, type CallerCard as Card, type CRM } from './api'
import { Field, relTime } from './ui'

/** CRMSettings connects a customer system: a lookup for caller pop-ups, an "open in CRM" link and a call log webhook. */
export function CRMSettings({ value, onChange, error }: { value: CRM; onChange: (v: Partial<CRM>) => void; error: (f: string) => string | undefined }) {
  const [test, { isLoading }] = useCrmTestMutation()
  const [number, setNumber] = useState('')
  const [result, setResult] = useState<{ what: string; ok: boolean; message: string } | null>(null)
  const run = async (what: 'lookup' | 'log') => {
    try {
      const r = await test({ crm: value, number, what }).unwrap()
      setResult({ what, ...r })
    } catch (e) {
      const b = errorOf(e)
      setResult({ what, ok: false, message: b.problems?.[0]?.message ?? b.message })
    }
  }
  const shown = (what: string) => result?.what === what && (
    <p role="status" className={`text-sm ${result.ok ? 'text-emerald-300' : 'text-rose-300'}`}>{result.message}</p>
  )
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="crm-h">
      <h2 id="crm-h" className="flex items-center gap-2 font-semibold text-slate-100"><PlugZap size={17} className="text-teal-300" aria-hidden /> CRM</h2>
      <p className="text-sm text-slate-400">
        Connect your customer system, or anything with a web address. When a call rings, BitVoice asks it who the caller is and shows a
        <span className="text-sky-300"> caller card</span> on Live calls and the browser phone; every call can be <span className="text-emerald-300">logged</span> to it as it ends.
        Use <span className="font-mono text-amber-300">{'{number}'}</span> where the caller's number goes.
      </p>
      <div className="grid gap-4 md:grid-cols-2">
        <Field label="Lookup address" htmlFor="crml" error={error('system.crm.lookupUrl')} hint={'BitVoice asks it (GET). JSON {"name", "company", "url", "notes"}, or the CRM\'s own shape with the answer fields below; 404 or no match when unknown.'}>
          <input id="crml" className="input font-mono" placeholder="https://crm.example.com/api/contacts?phone={number}" value={value.lookupUrl} onChange={(e) => onChange({ lookupUrl: e.target.value.trim() })} />
        </Field>
        <Field label="Open in CRM link" htmlFor="crmo" error={error('system.crm.openUrl')} hint="A link on the caller card, such as the CRM's search page.">
          <input id="crmo" className="input font-mono" placeholder="https://crm.example.com/search?q={number}" value={value.openUrl} onChange={(e) => onChange({ openUrl: e.target.value.trim() })} />
        </Field>
        <Field label="Lookup header" htmlFor="crmh" error={error('system.crm.lookupHeader')} hint="Sent with each lookup, such as Authorization.">
          <input id="crmh" className="input font-mono" value={value.lookupHeader} onChange={(e) => onChange({ lookupHeader: e.target.value.trim() })} />
        </Field>
        <Field label="Lookup header value" htmlFor="crms" error={error('system.crm.lookupSecret')} hint="Such as Bearer and the CRM's API key. A vault reference works too.">
          <input id="crms" className="input font-mono" type="password" autoComplete="off" value={value.lookupSecret} onChange={(e) => onChange({ lookupSecret: e.target.value })} />
        </Field>
      </div>
      <details className="rounded-lg border border-slate-800 p-3" open={Object.values(value.fields ?? {}).some(Boolean)}>
        <summary className="cursor-pointer text-sm font-medium text-slate-200">Answer fields <span className="font-normal text-slate-500">for a CRM that answers in its own shape</span></summary>
        <p className="mt-2 text-sm text-slate-400">
          Most CRMs answer a search with a <span className="text-sky-300">list of matches</span>. Say where each part is as a dotted path; a number steps into the list, so
          <span className="font-mono text-amber-300"> list.0.name</span> is the first match's name. The link may be a web address with paths in braces, such as
          <span className="font-mono text-amber-300"> {'https://crm.example.com/#Contact/view/{list.0.id}'}</span>. Empty fields read BitVoice's own shape.
        </p>
        <div className="mt-3 grid gap-4 md:grid-cols-2">
          {([['name', 'Name field', 'list.0.name'], ['company', 'Company field', 'list.0.accountName'], ['notes', 'Notes field', 'list.0.description'], ['url', 'Link to the contact', 'https://crm.example.com/#Contact/view/{list.0.id}']] as const).map(([k, label, ph]) => (
            <Field key={k} label={label} htmlFor={`crmf-${k}`} error={error(`system.crm.fields.${k}`)}>
              <input id={`crmf-${k}`} className="input font-mono" placeholder={ph} value={value.fields?.[k] ?? ''}
                onChange={(e) => onChange({ fields: { name: '', company: '', notes: '', url: '', ...value.fields, [k]: e.target.value.trim() } })} />
            </Field>
          ))}
        </div>
      </details>
      <div className="flex flex-wrap items-end gap-2">
        <Field label="Number to try" htmlFor="crmn"><input id="crmn" className="input w-48 font-mono" inputMode="tel" value={number} onChange={(e) => setNumber(e.target.value)} /></Field>
        <button type="button" className="btn" disabled={isLoading || !number.trim()} onClick={() => run('lookup')}><UserRound size={14} className="text-sky-300" aria-hidden /> Look it up</button>
      </div>
      {shown('lookup')}
      <div className="grid gap-4 border-t border-slate-800 pt-4 md:grid-cols-2">
        <Field label="Call log address" htmlFor="crmw" error={error('system.crm.logUrl')} hint="Every call is sent here (POST, JSON) when it ends, again with its summary once transcribed, and each callback when booked.">
          <input id="crmw" className="input font-mono" placeholder="https://crm.example.com/hooks/bitvoice" value={value.logUrl} onChange={(e) => onChange({ logUrl: e.target.value.trim() })} />
        </Field>
        <Field label="Signing secret" htmlFor="crmk" error={error('system.crm.logSecret')} hint="X-BitVoice-Signature is sha256= and the HMAC of timestamp, a dot and the body, so the receiver can check it.">
          <input id="crmk" className="input font-mono" type="password" autoComplete="off" value={value.logSecret} onChange={(e) => onChange({ logSecret: e.target.value })} />
        </Field>
      </div>
      <button type="button" className="btn" disabled={isLoading} onClick={() => run('log')}><Send size={14} className="text-emerald-300" aria-hidden /> Send a test event</button>
      {shown('log')}
    </section>
  )
}

/** CallerCard shows who is calling: the CRM's answer, the open link, past calls and waiting callbacks. */
export function CallerCard({ number, compact }: { number: string; compact?: boolean }) {
  const { data } = useCallerQuery(number, { skip: !number })
  if (!data) return null
  return <CallerCardView card={data} compact={compact} />
}

export function CallerCardView({ card, compact }: { card: Card; compact?: boolean }) {
  const c = card.contact
  return (
    <div className="space-y-1.5 rounded-lg border border-sky-900/60 bg-sky-950/30 p-3 text-sm" aria-label={`About ${c?.name || card.number}`}>
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <UserRound size={14} className="text-sky-300" aria-hidden />
        <span className="font-semibold text-slate-100">{c?.name || 'Not known'}</span>
        {c?.company && <span className="flex items-center gap-1 text-slate-300"><Building size={13} className="text-slate-500" aria-hidden />{c.company}</span>}
        {c?.source === 'extension' && <span className="text-xs text-slate-500">extension</span>}
      </p>
      {c?.notes && <p className="text-amber-200">{c.notes}</p>}
      {card.lookupError && <p className="text-xs text-rose-300">CRM lookup failed: {card.lookupError}</p>}
      <p className="text-xs text-slate-400">
        {card.calls ? <>{card.calls} {card.calls === 1 ? 'call' : 'calls'} in 90 days, last {relTime(card.lastCall)}</> : 'First call in 90 days'}
        {!compact && <> · <a className="text-sky-300 underline" href={`#/timeline?number=${encodeURIComponent(card.number)}`}>Timeline</a></>}
      </p>
      {card.callbacks.length > 0 && (
        <p className="flex items-center gap-1.5 text-emerald-300"><PhoneForwarded size={13} aria-hidden />
          Waiting for a call back{card.callbacks[0].reason ? `: ${card.callbacks[0].reason}` : ''} {!compact && <a className="underline" href="#/callbacks">Callbacks</a>}
        </p>
      )}
      {!compact && card.lastSummary && <p className="flex gap-1.5 text-slate-300"><Sparkles size={13} className="mt-0.5 shrink-0 text-violet-300" aria-hidden /><span><span className="text-slate-500">Last call: </span>{card.lastSummary}</span></p>}
      {(c?.url || card.openUrl) && (
        <p className="flex flex-wrap gap-3 text-xs">
          {c?.url && <a className="flex items-center gap-1 text-sky-300 hover:underline" href={c.url} target="_blank" rel="noopener noreferrer"><Link2 size={12} aria-hidden /> Contact page</a>}
          {card.openUrl && <a className="flex items-center gap-1 text-sky-300 hover:underline" href={card.openUrl} target="_blank" rel="noopener noreferrer"><ExternalLink size={12} aria-hidden /> Open in CRM</a>}
        </p>
      )}
    </div>
  )
}
