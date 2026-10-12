import { clsx } from 'clsx'
import { CalendarClock, History, MessageSquare, PhoneIncoming, PhoneOutgoing, StickyNote, Trash2, Voicemail } from 'lucide-react'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { errorOf, useAddNoteMutation, useDeleteNoteMutation, useTimelineQuery, type TimelineItem } from './api'
import { attempt, useIsAdmin } from './Editor'
import { RecordingButton } from './Monitor'
import { TranscriptButton } from './Transcripts'
import { Empty, Page, relTime } from './ui'

const looks: Record<TimelineItem['kind'], { icon: typeof History; colour: string }> = {
  call: { icon: PhoneIncoming, colour: 'text-emerald-300' }, voicemail: { icon: Voicemail, colour: 'text-violet-300' },
  text: { icon: MessageSquare, colour: 'text-sky-300' }, callback: { icon: CalendarClock, colour: 'text-amber-300' }, note: { icon: StickyNote, colour: 'text-orange-300' },
}

/** TimelineLink opens a number's timeline. */
export function TimelineLink({ number, label = 'Timeline' }: { number: string; label?: string }) {
  if (!number || number.replace(/\D/g, '').length < 3) return null
  return <Link to={`/timeline?number=${encodeURIComponent(number)}`} className="inline-flex items-center gap-1 text-xs text-sky-300 underline" aria-label={`${label} for ${number}`}><History size={12} aria-hidden />{label}</Link>
}

/** Timeline: everything about one customer, newest first, with shared notes. */
export function Timeline() {
  const admin = useIsAdmin()
  const [params, setParams] = useSearchParams()
  const number = params.get('number') ?? ''
  const [q, setQ] = useState(number)
  const { data, isFetching } = useTimelineQuery(number, { skip: number.replace(/\D/g, '').length < 3 })
  const [add, { isLoading }] = useAddNoteMutation()
  const [del] = useDeleteNoteMutation()
  const [note, setNote] = useState('')
  const [err, setErr] = useState('')
  const save = async () => {
    try { await add({ number, text: note }).unwrap(); setNote(''); setErr('') } catch (e) { const b = errorOf(e); setErr(b.problems?.[0]?.message ?? b.message) }
  }
  return (
    <Page icon={History} title="Timeline" accent="from-sky-300 to-emerald-300"
      intro="Everything with one customer in one place: calls with their summaries, recordings, voicemails, texts, callbacks and the team's notes.">
      <form className="mb-4 flex gap-2" onSubmit={(e) => { e.preventDefault(); setParams({ number: q.trim() }) }}>
        <input type="search" className="input max-w-xs font-mono" aria-label="Phone number" placeholder="+12125550100" value={q} onChange={(e) => setQ(e.target.value)} />
        <button className="btn">Show</button>
      </form>
      {!number ? <Empty icon={History} title="Whose timeline?">Enter a phone number, or open one from a caller card, Contacts, Call history or Texts.</Empty> : data && (
        <div className="grid gap-4 lg:grid-cols-[1fr_20rem]">
          <section aria-label={`Timeline for ${number}`}>
            <h2 className="mb-3 text-lg font-semibold text-slate-100">{data.contact?.name ?? number} {data.contact && <span className="font-mono text-sm text-sky-300">{number}</span>}
              {data.contact?.company && <span className="ml-2 text-sm text-slate-400">{data.contact.company}</span>}</h2>
            {data.items.length === 0 ? <p className="text-sm text-slate-500">{isFetching ? 'Looking…' : 'Nothing with this number in the last year.'}</p> : (
              <ol className="relative space-y-3 border-l border-slate-800 pl-5">
                {data.items.map((it) => {
                  const L = looks[it.kind]
                  const Icon = it.kind === 'call' && it.direction === 'outbound' ? PhoneOutgoing : L.icon
                  return (
                    <li key={it.kind + it.id} className="relative">
                      <span className={clsx('absolute -left-[1.85rem] top-1 grid h-5 w-5 place-items-center rounded-full bg-slate-900', L.colour)}><Icon size={13} aria-hidden /></span>
                      <div className="card p-3">
                        <div className="flex flex-wrap items-center gap-2 text-sm">
                          <span className="text-slate-100">{it.title}</span>
                          {it.kind === 'callback' && it.status && <span className={clsx('rounded px-1.5 text-xs', it.status === 'open' ? 'bg-amber-500/15 text-amber-300' : 'bg-slate-700 text-slate-300')}>{it.status}</span>}
                          <span className="ml-auto text-xs text-slate-500" title={new Date(it.at).toLocaleString()}>{relTime(it.at)}</span>
                        </div>
                        {it.detail && <p className={clsx('mt-1 whitespace-pre-wrap text-sm', it.kind === 'note' ? 'text-orange-100' : 'text-slate-300')}>{it.detail}</p>}
                        <div className="mt-1 flex items-center gap-2">
                          {it.recording && <RecordingButton name={it.recording} />}
                          {it.recording && <TranscriptButton name={it.recording} label={it.title} />}
                          {it.kind === 'voicemail' && <Link to="/voicemail" className="text-xs text-violet-300 underline">Voicemail</Link>}
                          {it.kind === 'text' && <Link to="/texts" className="text-xs text-sky-300 underline">Texts</Link>}
                          {it.kind === 'note' && admin && <button className="btn-ghost ml-auto p-1" aria-label="Remove this note" onClick={() => attempt(() => del(it.id).unwrap(), 'Note removed')}><Trash2 size={13} className="text-rose-300" aria-hidden /></button>}
                        </div>
                      </div>
                    </li>
                  )
                })}
              </ol>
            )}
          </section>
          {admin && (
            <form className="card h-fit space-y-2 p-4" onSubmit={(e) => { e.preventDefault(); save() }} aria-label="Add a note">
              <label htmlFor="tln" className="label">Add a note for the team</label>
              <textarea id="tln" rows={4} className="input" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Prefers a call after 3 pm." />
              {err && <p className="field-error text-sm text-rose-300">{err}</p>}
              <button className="btn btn-primary" disabled={isLoading || !note.trim()}><StickyNote size={14} aria-hidden /> Add note</button>
            </form>
          )}
        </div>
      )}
    </Page>
  )
}
