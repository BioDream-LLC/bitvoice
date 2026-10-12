import { clsx } from 'clsx'
import { CheckCircle2, Circle, CircleDot, Copy, Info, MessageSquare, MessageSquarePlus, Plus, Save, Send, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  errorOf, useConversationsQuery, useDirectoryQuery, useExtensionsQuery, useMarkReadMutation, useSaveSettingsMutation, useSendTextMutation, useSettingsQuery, useTextingKindsQuery, useThreadQuery,
  type ApiErrorBody, type Texting,
} from './api'
import { useIsAdmin } from './Editor'
import { TimelineLink } from './Timeline'
import { useDraft } from './useDraft'
import { Empty, Field, Page, StaleBanner, problemFor, relTime } from './ui'

const parts = (t: string) => {
  const wide = [...t].some((c) => c.charCodeAt(0) > 0x7e)
  const [one, many] = wide ? [70, 67] : [160, 153]
  const n = [...t].length
  return n <= one ? 1 : Math.ceil(n / many)
}

/** Texts: conversations by text message on the business numbers, and their setup. */
export function Texts() {
  const admin = useIsAdmin()
  const { data: convs } = useConversationsQuery()
  const { data: settings } = useSettingsQuery()
  const { data: dir } = useDirectoryQuery()
  const numbers = settings?.system.texting?.numbers ?? []
  const [open, setOpen] = useState<{ our: string; other: string } | null>(null)
  const [composing, setComposing] = useState(false)
  const nameOf = (n: string) => dir?.items.find((c) => c.numbers.some((x) => x.number.replace(/\D/g, '').endsWith(n.replace(/\D/g, '').slice(-10))))?.name
  return (
    <Page icon={MessageSquare} title="Texts" accent="from-sky-300 to-violet-300"
      intro="Text messages to and from your business numbers, through your texting provider. Replies come in here, and to the people each number names by email."
      actions={admin && numbers.length > 0 && <button className="btn btn-primary" onClick={() => { setComposing(true); setOpen(null) }}><MessageSquarePlus size={15} aria-hidden /> New text</button>}>
      {numbers.length === 0 ? (
        <Empty icon={MessageSquare} title="No texting numbers yet">{admin ? 'Add your texting provider and numbers below.' : 'An administrator can set up texting.'}</Empty>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[20rem_1fr]">
          <ul className="card max-h-[32rem] divide-y divide-slate-800 overflow-y-auto" aria-label="Conversations">
            {!convs?.items.length && <li className="p-4 text-sm text-slate-500">No texts yet.</li>}
            {convs?.items.map((c) => (
              <li key={c.our + c.other}>
                <button className={clsx('w-full p-3 text-left hover:bg-slate-800/60', open?.other === c.other && open?.our === c.our && 'bg-slate-800/80')} onClick={() => { setOpen({ our: c.our, other: c.other }); setComposing(false) }}>
                  <div className="flex items-center gap-2">
                    <span className="truncate font-medium text-slate-100">{nameOf(c.other) ?? c.other}</span>
                    {c.unread > 0 && <span className="rounded-full bg-sky-500 px-1.5 text-xs font-bold text-white" aria-label={`${c.unread} unread`}>{c.unread}</span>}
                    <span className="ml-auto text-xs text-slate-500">{relTime(c.last.at)}</span>
                  </div>
                  <div className="truncate text-sm text-slate-400">{c.last.direction === 'out' && <span className="text-slate-500">You: </span>}{c.last.body}</div>
                  {numbers.length > 1 && <div className="text-xs text-slate-500">on {c.our}</div>}
                </button>
              </li>
            ))}
          </ul>
          {composing ? <Compose numbers={numbers.map((n) => n.number)} onSent={(our, other) => { setComposing(false); setOpen({ our, other }) }} />
            : open ? <Thread our={open.our} other={open.other} name={nameOf(open.other)} admin={admin} />
              : <div className="card grid place-items-center p-10 text-sm text-slate-500">Choose a conversation.</div>}
        </div>
      )}
      {admin && <TextingSetup />}
    </Page>
  )
}

function Thread({ our, other, name, admin }: { our: string; other: string; name?: string; admin: boolean }) {
  const { data } = useThreadQuery({ our, other })
  const [markRead] = useMarkReadMutation()
  const [send, { isLoading }] = useSendTextMutation()
  const [text, setText] = useState('')
  const end = useRef<HTMLDivElement>(null)
  const count = data?.items.length ?? 0
  useEffect(() => { if (count) { markRead({ our, other }); end.current?.scrollIntoView({ block: 'end' }) } }, [count, our, other, markRead])
  const doSend = async () => {
    try { await send({ from: our, to: other, text }).unwrap(); setText('') }
    catch (e) { const b = errorOf(e); toast.error(b.problems?.[0]?.message ?? b.message) }
  }
  return (
    <section className="card flex max-h-[32rem] flex-col" aria-label={`Texts with ${name ?? other}`}>
      <header className="flex flex-wrap items-center gap-2 border-b border-slate-800 p-3"><span className="font-semibold text-slate-100">{name ?? other}</span> <span className="font-mono text-sm text-sky-300">{other}</span> <span className="text-xs text-slate-500">on {our}</span> <span className="ml-auto"><TimelineLink number={other} /></span></header>
      <div className="flex-1 space-y-2 overflow-y-auto p-3" role="log" aria-live="polite">
        {data?.items.map((t) => (
          <div key={t.id} className={clsx('max-w-[80%] rounded-lg px-3 py-2 text-sm', t.direction === 'out' ? 'ml-auto bg-sky-600/30 text-sky-50' : 'bg-slate-800 text-slate-100')}>
            <p className="whitespace-pre-wrap">{t.body}</p>
            <p className={clsx('mt-1 text-xs', t.status === 'failed' ? 'text-rose-300' : 'text-slate-400')}>{new Date(t.at).toLocaleString()}{t.by && ` · ${t.by}`}{t.status === 'failed' && ` · not sent: ${t.error}`}</p>
          </div>
        ))}
        <div ref={end} />
      </div>
      {data?.optedOut ? <p className="border-t border-slate-800 p-3 text-sm text-amber-300">They replied STOP: no texts can be sent to them until they reply START.</p> : admin && (
        <form className="flex items-end gap-2 border-t border-slate-800 p-3" onSubmit={(e) => { e.preventDefault(); if (text.trim()) doSend() }}>
          <div className="flex-1">
            <textarea className="input" rows={2} aria-label="Reply" value={text} onChange={(e) => setText(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); if (text.trim()) doSend() } }} />
            <p className="text-xs text-slate-500">{text ? `${[...text].length} characters, ${parts(text)} text${parts(text) > 1 ? 's' : ''}` : 'Enter sends; Shift+Enter for a new line.'}</p>
          </div>
          <button className="btn btn-primary" disabled={isLoading || !text.trim()}><Send size={14} aria-hidden /> Send</button>
        </form>
      )}
    </section>
  )
}

function Compose({ numbers, onSent }: { numbers: string[]; onSent: (our: string, other: string) => void }) {
  const [send, { isLoading }] = useSendTextMutation()
  const [from, setFrom] = useState(numbers[0] ?? '')
  const [to, setTo] = useState('')
  const [text, setText] = useState('')
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const p = (f: string) => problemFor(err?.problems, f)
  const go = async () => {
    try { const t = await send({ from, to, text }).unwrap(); setErr(null); onSent(t.our, t.other) }
    catch (e) { const b = errorOf(e); if (b.problems?.length) setErr(b); else toast.error(b.message) }
  }
  return (
    <form className="card space-y-3 p-4" onSubmit={(e) => { e.preventDefault(); go() }} aria-label="New text">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="From" htmlFor="txf" error={p('from')}><select id="txf" className="input" value={from} onChange={(e) => setFrom(e.target.value)}>{numbers.map((n) => <option key={n}>{n}</option>)}</select></Field>
        <Field label="To" htmlFor="txt" error={p('to')}><input id="txt" className="input font-mono" inputMode="tel" placeholder="+12125550100" value={to} onChange={(e) => setTo(e.target.value)} /></Field>
      </div>
      <Field label="Message" htmlFor="txm" error={p('text')} hint={text ? `${[...text].length} characters, ${parts(text)} text${parts(text) > 1 ? 's' : ''}` : 'Texts may only go to people who agreed to get them.'}>
        <textarea id="txm" className="input" rows={4} value={text} onChange={(e) => setText(e.target.value)} />
      </Field>
      <button className="btn btn-primary" disabled={isLoading}><Send size={14} aria-hidden /> Send</button>
    </form>
  )
}

const step = (v: string) => v === 'approved' ? <CheckCircle2 size={15} className="text-emerald-300" aria-hidden /> : v === 'submitted' ? <CircleDot size={15} className="text-amber-300" aria-hidden /> : <Circle size={15} className="text-slate-500" aria-hidden />

function TextingSetup() {
  const { data } = useSettingsQuery()
  const { data: exts } = useExtensionsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const blank: Texting = { providers: [], numbers: [], tenDlc: { brand: 'not-started', campaign: 'not-started', useCase: '', sample: '' } }
  const { draft, setDraft, reset, stale } = useDraft<Texting>(data ? (data.system.texting ?? blank) : undefined)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const providers = draft?.providers ?? []
  const nums = draft?.numbers ?? []
  const origin = useMemo(() => location.origin, [])
  const { data: kinds = [] } = useTextingKindsQuery()
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.texting.' + f)
  const set = (v: Partial<Texting>) => setDraft((d) => ({ ...d, ...v }))
  const doSave = async () => {
    try { const s = await save({ ...data, system: { ...data.system, texting: draft } }).unwrap(); reset(s.system.texting); setErr(null); toast.success('Texting saved') }
    catch (e) { setErr((e as { data?: ApiErrorBody }).data ?? { message: 'Texting could not be saved.' }) }
  }
  const dlc = draft.tenDlc ?? blank.tenDlc
  const sampleOk = /stop/i.test(dlc.sample ?? '')
  return (
    <section className="card mt-6 space-y-5 p-5" aria-labelledby="txs-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="txs-h" className="flex items-center gap-2 font-semibold text-slate-100"><MessageSquare size={17} className="text-sky-300" aria-hidden /> Texting setup</h2>
      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-violet-300">Providers</h3>
        {providers.map((v, i) => {
          const hook = v.id && v.hookSecret ? `${origin}/hooks/sms/${v.id}/${v.hookSecret}` : ''
          const k = kinds.find((x) => x.id === v.kind)
          const setP = (x: Partial<typeof v>) => set({ providers: providers.map((y, j) => (j === i ? { ...y, ...x } : y)) })
          return (
            <div key={i} className="space-y-3 rounded-lg border border-slate-800 p-3">
              <div className="grid gap-3 md:grid-cols-4">
                <Field label="Name" htmlFor={`txpn${i}`} error={p(`providers[${i}].name`)}><input id={`txpn${i}`} className="input" value={v.name} onChange={(e) => setP({ name: e.target.value })} /></Field>
                <Field label="Provider" htmlFor={`txpk${i}`} error={p(`providers[${i}].kind`)}>
                  <select id={`txpk${i}`} className="input" value={v.kind} onChange={(e) => {
                    const nk = kinds.find((x) => x.id === e.target.value)
                    // A new provider takes the provider's name unless the name was changed by hand.
                    const named = !v.name || kinds.some((x) => x.name === v.name)
                    setP({ kind: e.target.value, ...(named && nk ? { name: nk.name } : {}) })
                  }}>
                    {kinds.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                  </select>
                </Field>
                {k?.username && <Field label={k.username} htmlFor={`txpu${i}`} error={p(`providers[${i}].username`)}><input id={`txpu${i}`} className="input" autoComplete="off" value={v.username} onChange={(e) => setP({ username: e.target.value })} /></Field>}
                <Field label={k?.key ?? 'API key'} htmlFor={`txpa${i}`} error={p(`providers[${i}].key`)}><input id={`txpa${i}`} className="input font-mono" type="password" autoComplete="off" value={v.key} onChange={(e) => setP({ key: e.target.value })} /></Field>
                {k?.account && <Field label={k.account} htmlFor={`txpc${i}`} error={p(`providers[${i}].account`)}><input id={`txpc${i}`} className="input font-mono" value={v.account ?? ''} onChange={(e) => setP({ account: e.target.value.trim() })} /></Field>}
                {k?.app && <Field label={k.app} htmlFor={`txpp${i}`} error={p(`providers[${i}].app`)}><input id={`txpp${i}`} className="input font-mono" value={v.app ?? ''} onChange={(e) => setP({ app: e.target.value.trim() })} /></Field>}
              </div>
              {k && <p className="text-xs text-slate-400"><Info size={12} className="mr-1 inline text-sky-300" aria-hidden />{k.help}</p>}
              <Field label={k?.baseRequired ? k.base ?? 'Address' : 'API address (optional)'} htmlFor={`txpb${i}`} error={p(`providers[${i}].baseUrl`)}
                hint={k?.baseRequired ? undefined : `Only for ${k?.name ?? 'the provider'}'s sandbox or a test server; empty for the real service.`}>
                <input id={`txpb${i}`} className="input font-mono" value={v.baseUrl ?? ''} onChange={(e) => setP({ baseUrl: e.target.value.trim() })} />
              </Field>
              {hook ? (
                <div className="text-sm">
                  <span className="text-slate-400">Received texts: in {k?.name ?? 'the provider'}, send received messages to </span>
                  <span className="break-all font-mono text-xs text-emerald-300">{hook}</span>
                  <button type="button" className="btn-ghost ml-1 p-1" aria-label="Copy the callback address" onClick={() => { navigator.clipboard?.writeText(hook); toast.success('Copied') }}><Copy size={13} aria-hidden /></button>
                  <p className="text-xs text-slate-500">The address holds a secret: keep it private. BitVoice must be reachable from the internet at this address.</p>
                </div>
              ) : <p className="text-xs text-slate-500">Save to get the address {k?.name ?? 'the provider'} sends received texts to.</p>}
              <button type="button" className="btn" onClick={() => set({ providers: providers.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /> Remove this provider</button>
            </div>
          )
        })}
        <button type="button" className="btn" onClick={() => set({ providers: [...providers, { id: '', name: 'TextMagic', kind: 'textmagic', username: '', key: '', hookSecret: '' }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a provider</button>
      </div>
      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-sky-300">Numbers</h3>
        {nums.map((n, i) => {
          const setN = (x: Partial<typeof n>) => set({ numbers: nums.map((y, j) => (j === i ? { ...y, ...x } : y)) })
          return (
            <div key={i} className="grid items-start gap-3 md:grid-cols-[12rem_1fr_12rem_1fr_auto]">
              <Field label="Number" htmlFor={`txnn${i}`} error={p(`numbers[${i}].number`)}><input id={`txnn${i}`} className="input font-mono" placeholder="+12125550100" value={n.number} onChange={(e) => setN({ number: e.target.value })} /></Field>
              <Field label="Name" htmlFor={`txnm${i}`}><input id={`txnm${i}`} className="input" placeholder="Main line" value={n.name} onChange={(e) => setN({ name: e.target.value })} /></Field>
              <Field label="Carried by" htmlFor={`txnp${i}`} error={p(`numbers[${i}].provider`)}>
                <select id={`txnp${i}`} className="input" value={n.provider} onChange={(e) => setN({ provider: e.target.value })}>
                  <option value="">Choose</option>{providers.filter((v) => v.id).map((v) => <option key={v.id} value={v.id}>{v.name}</option>)}
                </select>
              </Field>
              <Field label="Tell these extensions" htmlFor={`txnx${i}`} error={p(`numbers[${i}].notify`)} hint="By email, of each text received.">
                <select id={`txnx${i}`} className="input" multiple size={3} value={n.notify ?? []} onChange={(e) => setN({ notify: [...e.target.selectedOptions].map((o) => o.value) })}>
                  {exts?.items.map((x) => <option key={x.number} value={x.number}>{x.number} {x.name}</option>)}
                </select>
              </Field>
              <button type="button" className="btn-ghost mt-6 p-2" aria-label={`Remove number ${i + 1}`} onClick={() => set({ numbers: nums.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
            </div>
          )
        })}
        <button type="button" className="btn" disabled={!providers.some((v) => v.id)} onClick={() => set({ numbers: [...nums, { number: '', provider: providers.find((v) => v.id)?.id ?? '', name: '', notify: [] }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a number</button>
        {!providers.some((v) => v.id) && <p className="text-xs text-slate-500">Save a provider first.</p>}
      </div>
      <div className="space-y-3 rounded-lg border border-slate-800 p-4">
        <h3 className="text-sm font-semibold text-amber-300">US 10DLC registration</h3>
        <p className="text-sm text-slate-400">US carriers block texts from ordinary (10-digit) business numbers that are not registered. With your texting provider, register your <b className="text-slate-200">brand</b> (the business: legal name, tax number, address) and then a <b className="text-slate-200">campaign</b> (what you text people about, with samples), and wait for approval. Track it here.</p>
        <ol className="space-y-2 text-sm">
          {(['brand', 'campaign'] as const).map((k) => (
            <li key={k} className="flex flex-wrap items-center gap-3">
              {step(dlc[k])}<span className="w-24 capitalize text-slate-200">{k}</span>
              <select aria-label={`10DLC ${k}`} className="input w-40" value={dlc[k]} onChange={(e) => set({ tenDlc: { ...dlc, [k]: e.target.value } })}>
                <option value="not-started">Not started</option><option value="submitted">Submitted</option><option value="approved">Approved</option>
              </select>
            </li>
          ))}
        </ol>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="What you text about" htmlFor="dlcu" hint="Customer care, appointment reminders, delivery updates…"><input id="dlcu" className="input" value={dlc.useCase} onChange={(e) => set({ tenDlc: { ...dlc, useCase: e.target.value } })} /></Field>
          <Field label="A sample message" htmlFor="dlcs" hint={dlc.sample && !sampleOk ? <span className="text-amber-300">Carriers expect the opt-out words: add “Reply STOP to opt out.”</span> : 'Name your business and say how to opt out.'}>
            <input id="dlcs" className="input" value={dlc.sample} placeholder="Acme Dental: your appointment is tomorrow at 9. Reply STOP to opt out." onChange={(e) => set({ tenDlc: { ...dlc, sample: e.target.value } })} />
          </Field>
        </div>
        <p className="text-xs text-slate-500">BitVoice honours STOP and START replies itself: after STOP, nothing more is sent to that person from that number until they reply START. Toll-free numbers need toll-free verification instead.</p>
      </div>
      {err && !err.problems?.length && <p className="text-sm text-rose-300">{err.message}</p>}
      <button type="button" className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={14} aria-hidden /> Save texting</button>
    </section>
  )
}
