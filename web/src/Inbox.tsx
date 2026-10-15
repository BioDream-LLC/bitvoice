import { clsx } from 'clsx'
import { CheckCheck, Copy, Globe, Inbox as InboxIcon, MessageCircle, MessageSquare, RotateCcw, Save, Send, Settings2, UserRound } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  useInboxQuery, useInboxReplyMutation, useInboxSendTemplateMutation, useInboxSetMutation, useInboxTemplatesQuery, useInboxThreadQuery, useMeQuery, useSaveSettingsMutation, useSettingsQuery, useUsersQuery,
  type ApiErrorBody, type InboxConv, type InboxSettings, type WhatsAppTemplate,
} from './api'
import { attempt, useIsAdmin } from './Editor'
import { Empty, Field, ListInput, Page, problemFor, relTime, Toggle } from './ui'

const channels: Record<InboxConv['channel'], { label: string; colour: string }> = {
  webchat: { label: 'Web chat', colour: 'text-sky-300' },
  whatsapp: { label: 'WhatsApp', colour: 'text-emerald-300' },
  messenger: { label: 'Messenger', colour: 'text-violet-300' },
  sms: { label: 'Text', colour: 'text-amber-300' },
}

/** One inbox for every written conversation: the web chat, WhatsApp, Messenger and texts. */
export function Inbox() {
  const [state, setState] = useState<'open' | 'closed' | ''>('open')
  const { data } = useInboxQuery(state || undefined, { pollingInterval: 4000 })
  const [cur, setCur] = useState('')
  const [settings, setSettings] = useState(false)
  const admin = useIsAdmin()
  const list = data ?? []
  return (
    <Page icon={InboxIcon} title="Inbox" accent="from-sky-400 to-emerald-400"
      intro="Every written conversation in one place: the chat box on your website, WhatsApp, Facebook Messenger and texts. Anyone signed in can answer; the first to reply takes the conversation."
      actions={admin && <button className="btn" onClick={() => setSettings(!settings)}><Settings2 size={15} aria-hidden /> {settings ? 'Back to the inbox' : 'Channels'}</button>}>
      {settings ? <InboxSettingsCard /> : (
        <div className="grid gap-4 lg:grid-cols-[22rem_1fr]">
          <div className="card flex max-h-[70vh] flex-col overflow-hidden">
            <div className="flex gap-1 border-b border-slate-800 p-2" role="tablist" aria-label="Conversations">
              {(['open', 'closed', ''] as const).map((s) => (
                <button key={s} role="tab" aria-selected={state === s} className={clsx('rounded px-3 py-1 text-sm', state === s ? 'bg-slate-800 text-slate-100' : 'text-slate-400')} onClick={() => setState(s)}>{s === '' ? 'All' : s === 'open' ? 'Open' : 'Closed'}</button>
              ))}
            </div>
            {!list.length ? <div className="p-4"><Empty icon={InboxIcon} title="Nothing here">Conversations appear as people write.</Empty></div> : (
              <ul className="flex-1 divide-y divide-slate-800 overflow-auto" aria-label="Conversations list">
                {list.map((c) => (
                  <li key={c.id}>
                    <button className={clsx('w-full p-3 text-left hover:bg-slate-900', cur === c.id && 'bg-slate-900')} onClick={() => setCur(c.id)}>
                      <div className="flex items-center gap-2">
                        <span className={clsx('text-xs font-semibold', channels[c.channel].colour)}>{channels[c.channel].label}</span>
                        <span className="truncate text-sm text-slate-100">{c.name || c.contact}</span>
                        {c.unread > 0 && <span className="ml-auto rounded-full bg-sky-500 px-1.5 text-xs font-semibold text-slate-950">{c.unread}</span>}
                      </div>
                      <div className="mt-0.5 flex items-center gap-2 text-xs text-slate-500">
                        <span className="truncate">{c.preview}</span>
                        <span className="ml-auto shrink-0">{relTime(c.updated)}</span>
                      </div>
                      {c.assignee && <div className="text-xs text-slate-500">with <span className="text-slate-300">{c.assignee}</span></div>}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
          {cur ? <Thread id={cur} /> : <div className="card flex items-center justify-center p-10 text-sm text-slate-500">Choose a conversation.</div>}
        </div>
      )}
    </Page>
  )
}

function Thread({ id }: { id: string }) {
  const { data } = useInboxThreadQuery(id, { pollingInterval: 3000 })
  const { data: me } = useMeQuery()
  const { data: users } = useUsersQuery()
  const [reply, { isLoading }] = useInboxReplyMutation()
  const [setConv] = useInboxSetMutation()
  const [text, setText] = useState('')
  const end = useRef<HTMLDivElement>(null)
  const count = data?.messages.length ?? 0
  useEffect(() => { end.current?.scrollIntoView({ block: 'end' }) }, [count, id])
  useEffect(() => {
    if (data && data.conversation.unread > 0) void setConv({ id, read: true })
  }, [data, id, setConv])
  if (!data) return null
  const c = data.conversation
  const ch = channels[c.channel]
  const closed = c.state === 'closed'
  const send = async () => {
    const t = text.trim()
    if (!t) return
    try { await reply({ id, text: t }).unwrap(); setText('') } catch (e) { toast.error((e as { data?: { message?: string } }).data?.message ?? 'Not sent') }
  }
  return (
    <div className="card flex max-h-[70vh] flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-slate-800 p-3">
        <span className={clsx('text-xs font-semibold', ch.colour)}>{ch.label}</span>
        <span className="font-semibold text-slate-100">{c.name || c.contact}</span>
        <span className="font-mono text-xs text-slate-500">{c.channel === 'webchat' ? c.email : c.contact}</span>
        {c.page && <a className="flex items-center gap-1 text-xs text-sky-300 underline" href={c.page} target="_blank" rel="noreferrer"><Globe size={12} aria-hidden /> {new URL(c.page).pathname}</a>}
        {c.channel !== 'sms' && (
          <span className="ml-auto flex items-center gap-2">
            <label className="flex items-center gap-1 text-xs text-slate-400"><UserRound size={12} aria-hidden />
              <select aria-label="Answered by" className="input h-8 py-0 text-xs" value={c.assignee ?? ''} onChange={(e) => void attempt(() => setConv({ id, assignee: e.target.value }).unwrap(), 'Assigned')}>
                <option value="">Nobody yet</option>
                {users?.items.map((u) => <option key={u.username} value={u.username}>{u.name || u.username}</option>)}
              </select>
            </label>
            <button type="button" className="btn" onClick={() => void attempt(() => setConv({ id, state: closed ? 'open' : 'closed' }).unwrap(), closed ? 'Reopened' : 'Closed')}>
              {closed ? <><RotateCcw size={14} aria-hidden /> Reopen</> : <><CheckCheck size={14} className="text-emerald-300" aria-hidden /> Close</>}
            </button>
          </span>
        )}
      </div>
      <div className="flex-1 space-y-2 overflow-auto p-4" aria-live="polite">
        {data.messages.map((m) => (
          <div key={m.id} className={clsx('flex', m.in ? 'justify-start' : 'justify-end')}>
            <div className={clsx('max-w-[75%] rounded-xl px-3 py-2 text-sm', m.in ? 'bg-slate-800 text-slate-100' : 'bg-sky-700 text-white', m.status === 'failed' && 'ring-2 ring-rose-500')}>
              <p className="whitespace-pre-wrap break-words">{m.text}</p>
              <p className={clsx('mt-1 text-[11px]', m.in ? 'text-slate-400' : 'text-sky-100')}>{relTime(m.at)}{!m.in && m.by ? ` · ${m.by === 'auto' ? 'automatic' : m.by}` : ''}{m.status === 'failed' ? ` · not sent: ${m.error}` : ''}</p>
            </div>
          </div>
        ))}
        <div ref={end} />
      </div>
      {data.window === 'agent' && <p className="px-4 pb-1 text-xs text-amber-300">Their last message was over 24 hours ago: Messenger takes replies written by a person for 7 days (your Meta app needs the Human Agent permission).</p>}
      {data.window === 'shut' && <p className="px-4 pb-1 text-xs text-rose-300">Their last message was over 7 days ago: Messenger takes no reply until they write again.</p>}
      {data.optedOut && <p className="px-4 pb-1 text-xs text-rose-300">They replied STOP: no texts can be sent until they reply START.</p>}
      {data.window === 'template' ? <TemplateReply id={id} /> : data.window !== 'shut' && <form className="flex gap-2 border-t border-slate-800 p-3" onSubmit={(e) => { e.preventDefault(); void send() }}>
        <textarea aria-label="Reply" className="input h-16 flex-1 resize-none" value={text} placeholder={`Reply on ${ch.label}${me ? ` as ${me.name || me.username}` : ''}…`}
          onChange={(e) => setText(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); void send() } }} />
        <button type="submit" className="btn btn-primary self-end" disabled={isLoading || !text.trim()}><Send size={14} aria-hidden /> Send</button>
      </form>}
    </div>
  )
}

/** WhatsApp after 24 hours: an approved template, its values filled in, previewed as the customer reads it. */
function TemplateReply({ id }: { id: string }) {
  const { data, error, isFetching, refetch } = useInboxTemplatesQuery()
  const [send, { isLoading }] = useInboxSendTemplateMutation()
  const [pick, setPick] = useState('')
  const [vals, setVals] = useState<string[]>([])
  const list = data ?? []
  const tpl: WhatsAppTemplate | undefined = list.find((t) => `${t.name}|${t.language}` === pick)
  const fill = (s: string) => s.replace(/\{\{\s*(\d+)\s*\}\}/g, (m, n) => vals[Number(n) - 1] || m)
  const ready = tpl && tpl.usable && vals.length === tpl.params && vals.every((v) => v.trim())
  return (
    <div className="space-y-2 border-t border-slate-800 p-3">
      <p className="text-xs text-amber-300">Their last message was over 24 hours ago: WhatsApp takes only an approved template now.</p>
      {error ? <p className="text-xs text-rose-300">{(error as { data?: { message?: string } }).data?.message ?? 'The templates could not be read'} (give the business account ID under Channels).</p> : (
        <>
          {isFetching && !data && <p className="text-xs text-slate-400">Reading your templates from WhatsApp…</p>}
          {data && !data.length && <p className="text-xs text-amber-300">The account has no templates yet: make one in WhatsApp Manager. <button type="button" className="underline" onClick={() => void refetch()}>Look again</button></p>}
          <select aria-label="Template" className="input" value={pick} onChange={(e) => { setPick(e.target.value); const t = list.find((x) => `${x.name}|${x.language}` === e.target.value); setVals(Array(t?.params ?? 0).fill('')) }}>
            <option value="">Choose a template…</option>
            {list.map((t) => <option key={`${t.name}|${t.language}`} value={`${t.name}|${t.language}`} disabled={!t.usable}>{t.name} ({t.language}, {t.category.toLowerCase()}){t.usable ? '' : ` — ${t.why}`}</option>)}
          </select>
          {tpl && (
            <>
              {vals.map((v, i) => <input key={i} aria-label={`Value ${i + 1}`} className="input" placeholder={`{{${i + 1}}}`} value={v} onChange={(e) => setVals(vals.map((x, j) => (j === i ? e.target.value : x)))} />)}
              <div className="whitespace-pre-wrap rounded-lg bg-slate-800 p-2 text-sm text-slate-200" aria-label="Template preview">
                {tpl.header && <p className="font-semibold">{tpl.header}</p>}<p>{fill(tpl.body)}</p>{tpl.footer && <p className="text-xs text-slate-400">{tpl.footer}</p>}
              </div>
              <button type="button" className="btn btn-primary" disabled={!ready || isLoading}
                onClick={async () => { try { await send({ id, name: tpl.name, language: tpl.language, params: vals.map((v) => v.trim()) }).unwrap(); setPick(''); setVals([]) } catch (e) { toast.error((e as { data?: { message?: string } }).data?.message ?? 'Not sent') } }}>
                <Send size={14} aria-hidden /> Send template
              </button>
            </>
          )}
        </>
      )}
    </div>
  )
}

const defaults: InboxSettings = {
  greeting: '',
  webChat: { enabled: false, title: 'Chat with us', welcome: '', colour: '#0ea5e9', origins: [] },
  whatsApp: { enabled: false, phoneNumberId: '', token: '', appSecret: '', verifyToken: '' },
  messenger: { enabled: false, pageId: '', pageToken: '', appSecret: '', verifyToken: '' },
}

function Copyable({ text, label }: { text: string; label: string }) {
  return (
    <span className="flex items-center gap-1">
      <code className="break-all font-mono text-xs text-sky-300">{text}</code>
      <button type="button" className="btn btn-ghost px-1" aria-label={`Copy ${label}`} onClick={() => { void navigator.clipboard?.writeText(text); toast.success('Copied') }}><Copy size={12} aria-hidden /></button>
    </span>
  )
}

/** Settings for the inbox's channels. */
function InboxSettingsCard() {
  const { data } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const [edit, setEdit] = useState<InboxSettings | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!data) return null
  const s: InboxSettings = edit ?? { ...defaults, ...data.system.inbox, webChat: { ...defaults.webChat, ...data.system.inbox?.webChat },
    whatsApp: { ...defaults.whatsApp, ...data.system.inbox?.whatsApp }, messenger: { ...defaults.messenger, ...data.system.inbox?.messenger } }
  const set = (v: Partial<InboxSettings>) => setEdit({ ...s, ...v })
  const p = (f: string) => problemFor(err?.problems, 'system.inbox.' + f)
  const origin = location.origin
  const submit = async () => {
    const e = await attempt(() => save({ ...data, system: { ...data.system, inbox: s } }).unwrap(), 'Inbox channels saved')
    setErr(e)
    if (!e) setEdit(null)
  }
  return (
    <div className="space-y-4">
      <section className="card space-y-3 p-5" aria-labelledby="wc-h">
        <h2 id="wc-h" className="flex items-center gap-2 font-semibold text-slate-100"><MessageCircle size={17} className="text-sky-300" aria-hidden /> Chat box on your website</h2>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={s.webChat.enabled} onChange={(v) => set({ webChat: { ...s.webChat, enabled: v } })} label="Web chat" /> Web chat</label>
        {s.webChat.enabled && (
          <>
            <p className="text-sm text-slate-400">Put this in your site's pages, before &lt;/body&gt;:</p>
            <Copyable label="the chat box script" text={`<script src="${origin}/webchat.js" async></script>`} />
            <div className="grid gap-3 sm:grid-cols-[1fr_8rem]">
              <Field label="Title" htmlFor="wct" error={p('webChat.title')}><input id="wct" className="input" value={s.webChat.title} onChange={(e) => set({ webChat: { ...s.webChat, title: e.target.value } })} /></Field>
              <Field label="Colour" htmlFor="wcc" error={p('webChat.colour')}><input id="wcc" type="color" className="input h-10 p-1" value={s.webChat.colour} onChange={(e) => set({ webChat: { ...s.webChat, colour: e.target.value } })} /></Field>
            </div>
            <Field label="First line visitors see" htmlFor="wcw" error={p('webChat.welcome')}><input id="wcw" className="input" value={s.webChat.welcome} placeholder="Hello! How can we help?" onChange={(e) => set({ webChat: { ...s.webChat, welcome: e.target.value } })} /></Field>
            <Field label="Only on these sites" htmlFor="wco" error={p('webChat.origins')} hint="Press Enter after each, such as https://www.example.com. Empty lets the box run on any site.">
              <ListInput id="wco" mono value={s.webChat.origins ?? []} onChange={(v) => set({ webChat: { ...s.webChat, origins: v } })} placeholder="https://www.example.com" />
            </Field>
            <p className="text-xs text-slate-500">Visitors need to reach this server's address from the internet, with a certificate browsers trust (Let's Encrypt, in Settings).</p>
          </>
        )}
      </section>
      <section className="card space-y-3 p-5" aria-labelledby="wa-h">
        <h2 id="wa-h" className="flex items-center gap-2 font-semibold text-slate-100"><MessageSquare size={17} className="text-emerald-300" aria-hidden /> WhatsApp</h2>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={s.whatsApp.enabled} onChange={(v) => set({ whatsApp: { ...s.whatsApp, enabled: v } })} label="WhatsApp" /> WhatsApp Business (Meta's Cloud API)</label>
        {s.whatsApp.enabled && (
          <>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Phone number ID" htmlFor="wap" error={p('whatsApp.phoneNumberId')} hint="In WhatsApp Manager, API setup."><input id="wap" className="input font-mono" value={s.whatsApp.phoneNumberId} onChange={(e) => set({ whatsApp: { ...s.whatsApp, phoneNumberId: e.target.value.trim() } })} /></Field>
              <Field label="Business account ID" htmlFor="wab" error={p('whatsApp.businessAccountId')} hint="Also in API setup: lets the inbox list your message templates."><input id="wab" className="input font-mono" value={s.whatsApp.businessAccountId ?? ''} onChange={(e) => set({ whatsApp: { ...s.whatsApp, businessAccountId: e.target.value.trim() } })} /></Field>
              <Field label="Access token" htmlFor="wat" error={p('whatsApp.token')} hint="A system user's permanent token."><input id="wat" type="password" autoComplete="off" className="input font-mono" value={s.whatsApp.token} onChange={(e) => set({ whatsApp: { ...s.whatsApp, token: e.target.value.trim() } })} /></Field>
              <Field label="App secret" htmlFor="was" error={p('whatsApp.appSecret')}><input id="was" type="password" autoComplete="off" className="input font-mono" value={s.whatsApp.appSecret} onChange={(e) => set({ whatsApp: { ...s.whatsApp, appSecret: e.target.value.trim() } })} /></Field>
            </div>
            <WebhookHelp path="/hooks/whatsapp" verify={s.whatsApp.verifyToken} field="messages" />
          </>
        )}
      </section>
      <section className="card space-y-3 p-5" aria-labelledby="fb-h">
        <h2 id="fb-h" className="flex items-center gap-2 font-semibold text-slate-100"><MessageCircle size={17} className="text-violet-300" aria-hidden /> Facebook Messenger</h2>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={s.messenger.enabled} onChange={(v) => set({ messenger: { ...s.messenger, enabled: v } })} label="Messenger" /> Messenger, for a Facebook Page</label>
        {s.messenger.enabled && (
          <>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Page ID" htmlFor="fbp" error={p('messenger.pageId')}><input id="fbp" className="input font-mono" value={s.messenger.pageId} onChange={(e) => set({ messenger: { ...s.messenger, pageId: e.target.value.trim() } })} /></Field>
              <Field label="Page access token" htmlFor="fbt" error={p('messenger.pageToken')}><input id="fbt" type="password" autoComplete="off" className="input font-mono" value={s.messenger.pageToken} onChange={(e) => set({ messenger: { ...s.messenger, pageToken: e.target.value.trim() } })} /></Field>
              <Field label="App secret" htmlFor="fbs" error={p('messenger.appSecret')}><input id="fbs" type="password" autoComplete="off" className="input font-mono" value={s.messenger.appSecret} onChange={(e) => set({ messenger: { ...s.messenger, appSecret: e.target.value.trim() } })} /></Field>
            </div>
            <WebhookHelp path="/hooks/messenger" verify={s.messenger.verifyToken} field="messages, messaging_postbacks" />
          </>
        )}
      </section>
      <section className="card space-y-3 p-5">
        <Field label="Automatic greeting" htmlFor="ibg" error={p('greeting')} hint="Sent to the first message of every new conversation (web chat, WhatsApp, Messenger); empty sends none.">
          <input id="ibg" className="input" value={s.greeting} placeholder="Thanks for your message: we usually answer within a few minutes." onChange={(e) => set({ greeting: e.target.value })} />
        </Field>
      </section>
      <button type="button" className="btn btn-primary" disabled={isLoading || !edit} onClick={submit}><Save size={14} aria-hidden /> Save channels</button>
    </div>
  )
}

function WebhookHelp({ path, verify, field }: { path: string; verify: string; field: string }) {
  return (
    <div className="space-y-1 rounded-lg border border-slate-800 bg-slate-950/50 p-3 text-xs text-slate-400">
      <p>In the Meta app's webhook settings, give the callback address and the verify token, and subscribe to <span className="text-slate-200">{field}</span>:</p>
      <Copyable label="the callback address" text={`${location.origin}${path}`} />
      {verify ? <Copyable label="the verify token" text={verify} /> : <p className="text-amber-300">Save first: the verify token is made then.</p>}
    </div>
  )
}
