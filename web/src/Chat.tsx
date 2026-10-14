import { clsx } from 'clsx'
import { Hash, MessageCircle, Plus, Send, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  useChatListQuery, useChatMessagesQuery, useExtensionsQuery, useSendChatMutation, useSetChatStatusMutation,
  type ChatMessage, type SystemSettings,
} from './api'
import { Field, ListInput, Toggle } from './ui'

const stateLook: Record<string, [string, string]> = {
  available: ['bg-emerald-400', 'Available'],
  'on-call': ['bg-rose-400', 'On a call'],
  ringing: ['bg-amber-400', 'Ringing'],
  away: ['bg-violet-400', 'Away'],
  offline: ['bg-slate-600', 'Offline'],
}

/** The staff portal's chat: colleagues with their presence, direct conversations and team rooms. */
export function PortalChat() {
  const { data, refetch, isError } = useChatListQuery()
  const [open, setOpen] = useState('')
  const [status, setStatus] = useState<string | null>(null)
  const [saveStatus] = useSetChatStatusMutation()
  const [filter, setFilter] = useState('')
  // Live: a message or a colleague's change refreshes what is shown.
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const es = new EventSource('./api/me/events')
    const bump = () => { refetch(); setTick((t) => t + 1) }
    es.addEventListener('chat.message', bump)
    for (const e of ['presence.changed', 'call.started', 'call.ended', 'registrations.changed']) es.addEventListener(e, () => refetch())
    return () => es.close()
  }, [refetch])
  if (isError) return <p className="text-sm text-amber-300">Chat is switched off for this phone system.</p>
  if (!data) return null
  // As the server names a conversation between two people: the shorter number first, then in order.
  const keyOf = (ext: string) => {
    let [a, b] = [data.me, ext]
    if (a.length > b.length || (a.length === b.length && a > b)) [a, b] = [b, a]
    return `d-${a}-${b}`
  }
  const people = data.people.filter((p) => !filter || `${p.name} ${p.extension} ${p.department ?? ''}`.toLowerCase().includes(filter.toLowerCase()))
  const title = open.startsWith('r-') ? data.rooms.find((r) => r.key === open)?.name : data.people.find((p) => keyOf(p.extension) === open)?.name
  return (
    <div className="grid gap-4 md:grid-cols-[18rem_1fr]">
      <aside className="card space-y-3 p-3" aria-label="Colleagues">
        <Field label="Your status" htmlFor="cs" hint="What colleagues see beside your name.">
          <input id="cs" className="input" maxLength={80} placeholder="At lunch, back at 2" value={status ?? data.status ?? ''} onChange={(e) => setStatus(e.target.value)}
            onBlur={() => status !== null && saveStatus(status).unwrap().then(() => setStatus(null)).catch(() => toast.error('Status not saved'))} />
        </Field>
        {data.rooms.length > 0 && (
          <div>
            <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-400">Rooms</h3>
            {data.rooms.map((r) => (
              <button key={r.key} type="button" onClick={() => setOpen(r.key)} aria-current={open === r.key || undefined}
                className={clsx('flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm', open === r.key ? 'bg-sky-950 text-slate-100' : 'text-slate-300 hover:bg-slate-900')}>
                <Hash size={14} className="text-sky-300" aria-hidden /> {r.name}
                {r.unread > 0 && <span className="ml-auto rounded-full bg-sky-500 px-1.5 text-xs text-white">{r.unread}</span>}
              </button>
            ))}
          </div>
        )}
        <div>
          <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-400">People</h3>
          <input className="input mb-1 py-1 text-sm" aria-label="Find a colleague" placeholder="Find…" value={filter} onChange={(e) => setFilter(e.target.value)} />
          <ul className="max-h-96 overflow-y-auto">
            {people.map((p) => {
              const [dot, label] = stateLook[p.state] ?? stateLook.offline
              const key = keyOf(p.extension)
              return (
                <li key={p.extension}>
                  <button type="button" onClick={() => setOpen(key)} aria-current={open === key || undefined}
                    className={clsx('flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm', open === key ? 'bg-sky-950 text-slate-100' : 'text-slate-300 hover:bg-slate-900')}>
                    <span className={clsx('h-2.5 w-2.5 shrink-0 rounded-full', dot)} title={label} aria-label={label} role="img" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate">{p.name} <span className="font-mono text-xs text-sky-300">{p.extension}</span></span>
                      {(p.status || p.reason) && <span className="block truncate text-xs text-slate-500">{p.status || p.reason}</span>}
                    </span>
                    {p.unread > 0 && <span className="rounded-full bg-sky-500 px-1.5 text-xs text-white">{p.unread}</span>}
                  </button>
                </li>
              )
            })}
          </ul>
        </div>
      </aside>
      {open ? <Conversation key={open} chatKey={open} title={title ?? ''} me={data.me} tick={tick} names={Object.fromEntries(data.people.map((p) => [p.extension, p.name]))} />
        : <div className="card grid place-items-center p-10 text-sm text-slate-500"><MessageCircle size={28} className="mb-2 text-sky-300" aria-hidden />Choose a colleague or a room.</div>}
    </div>
  )
}

function Conversation({ chatKey, title, me, names, tick }: { chatKey: string; title: string; me: string; names: Record<string, string>; tick: number }) {
  const { data, refetch } = useChatMessagesQuery(chatKey)
  const [send, { isLoading }] = useSendChatMutation()
  const [text, setText] = useState('')
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => { if (tick) refetch() }, [tick, refetch])
  useEffect(() => { end.current?.scrollIntoView({ block: 'end' }) }, [data?.items.length])
  const go = async () => {
    const t = text.trim()
    if (!t) return
    try { await send({ key: chatKey, text: t }).unwrap(); setText('') } catch { toast.error('The message was not sent') }
  }
  const items: ChatMessage[] = data?.items ?? []
  return (
    <section className="card flex h-[32rem] flex-col" aria-label={`Conversation with ${title}`}>
      <h2 className="border-b border-slate-800 px-4 py-3 font-semibold text-slate-100">{title}</h2>
      <div className="flex-1 space-y-2 overflow-y-auto p-4" aria-live="polite">
        {!items.length && <p className="text-sm text-slate-500">No messages yet.</p>}
        {items.map((m) => (
          <div key={m.id} className={clsx('max-w-[80%] rounded-lg px-3 py-2 text-sm', m.from === me ? 'ml-auto bg-sky-900/60 text-slate-100' : 'bg-slate-800 text-slate-200')}>
            {m.from !== me && chatKey.startsWith('r-') && <div className="text-xs font-semibold text-teal-300">{names[m.from] ?? m.from}</div>}
            <div className="whitespace-pre-wrap break-words">{m.text}</div>
            <div className="text-right text-[10px] text-slate-500">{new Date(m.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</div>
          </div>
        ))}
        <div ref={end} />
      </div>
      <form className="flex gap-2 border-t border-slate-800 p-3" onSubmit={(e) => { e.preventDefault(); go() }}>
        <textarea className="input flex-1 resize-none" rows={1} maxLength={4000} aria-label="Message" placeholder="Write a message…" value={text}
          onChange={(e) => setText(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); go() } }} />
        <button className="btn btn-primary" disabled={isLoading || !text.trim()} aria-label="Send"><Send size={15} aria-hidden /></button>
      </form>
    </section>
  )
}

/** Settings: chat on or off, how long messages are kept, and the team rooms. */
export function ChatSettings({ chat, onChange, p }: {
  chat: SystemSettings['chat']; onChange: (v: NonNullable<SystemSettings['chat']>) => void; p: (f: string) => string | undefined
}) {
  const { data: exts } = useExtensionsQuery()
  const c = useMemo(() => ({ off: false, keepDays: 0, rooms: [], ...chat }), [chat])
  const rooms = c.rooms ?? []
  const setRoom = (i: number, v: Partial<(typeof rooms)[number]>) => onChange({ ...c, rooms: rooms.map((r, j) => (j === i ? { ...r, ...v } : r)) })
  return (
    <section className="card space-y-4 p-5" aria-labelledby="chat-h">
      <h2 id="chat-h" className="flex items-center gap-2 font-semibold text-slate-100"><MessageCircle size={17} className="text-sky-300" aria-hidden /> Staff chat</h2>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!c.off} onChange={(v) => onChange({ ...c, off: !v })} label="Staff chat" />
        Chat and presence in the staff portal <span className="text-xs text-slate-500">(people see who is on a call, away or offline, and message each other)</span></label>
      {!c.off && (
        <>
          <Field label="Keep messages (days)" htmlFor="ckd" error={p('system.chat.keepDays')} hint="0 keeps the newest thousand of each conversation.">
            <input id="ckd" type="number" min={0} max={3650} className="input w-32" value={c.keepDays || ''} onChange={(e) => onChange({ ...c, keepDays: Number(e.target.value) })} />
          </Field>
          <div className="space-y-2">
            <span className="label">Team rooms</span>
            {rooms.map((r, i) => (
              <div key={r.id || i} className="grid gap-2 rounded-md border border-slate-800 p-2 sm:grid-cols-[12rem_1fr_auto]">
                <input className="input" aria-label={`Room ${i + 1} name`} value={r.name} onChange={(e) => setRoom(i, { name: e.target.value })} placeholder="Front desk" />
                <ListInput id={`crm${i}`} mono value={r.members ?? []} onChange={(v) => setRoom(i, { members: v })} placeholder="Everyone (or list extensions)" invalid={!!p(`system.chat.rooms[${i}].members`)} />
                <button type="button" className="btn" aria-label={`Remove room ${r.name || i + 1}`} onClick={() => onChange({ ...c, rooms: rooms.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
                {(p(`system.chat.rooms[${i}].name`) || p(`system.chat.rooms[${i}].members`)) && <p className="text-sm text-rose-300 sm:col-span-3">{p(`system.chat.rooms[${i}].name`) ?? p(`system.chat.rooms[${i}].members`)}</p>}
              </div>
            ))}
            <button type="button" className="btn" onClick={() => onChange({ ...c, rooms: [...rooms, { id: '', name: '', members: [] }] })}><Plus size={14} aria-hidden /> Add a room</button>
            {exts && <p className="text-xs text-slate-500">Members are extension numbers; a room with none is for everyone.</p>}
          </div>
        </>
      )}
    </section>
  )
}
