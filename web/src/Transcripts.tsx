import { CheckSquare, FileText, Gauge, Loader2, MessageSquarePlus, RefreshCw, Search, Sparkles, Trash2, TriangleAlert } from 'lucide-react'
import { useEffect, useState } from 'react'
import { errorOf, useAddCoachingMutation, useDeleteCoachingMutation, useMakeInsightMutation, useMakeTranscriptMutation, useSearchTranscriptsQuery, useSettingsQuery, useTranscriptQuery, type CallTranscript } from './api'
import { Drawer, useIsAdmin } from './Editor'
import { formatDuration } from './ui'
import { toast } from 'sonner'

const who = (t: CallTranscript, side: 'caller' | 'callee') =>
  side === 'caller' ? t.fromName || t.from || 'Caller' : t.toName || t.to || 'Other side'

/** TranscriptButton opens a recorded call's transcript, summary and action items. */
export function TranscriptButton({ name, label }: { name: string; label: string }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button className="btn px-2 py-1" aria-label={`Transcript of ${label}`} title="Transcript, summary and action items"
        onClick={(e) => { e.stopPropagation(); setOpen(true) }}>
        <FileText size={14} className="text-violet-300" aria-hidden />
      </button>
      {open && <TranscriptDrawer name={name} label={label} onClose={() => setOpen(false)} />}
    </>
  )
}

function TranscriptDrawer({ name, label, onClose }: { name: string; label: string; onClose: () => void }) {
  const admin = useIsAdmin()
  const ai = useSettingsQuery().data?.system.ai
  const { data: t, error, isLoading } = useTranscriptQuery(name)
  const [make, { isLoading: making }] = useMakeTranscriptMutation()
  const none = !!error && (error as { status?: number }).status === 404
  const go = async () => {
    try { await make(name).unwrap(); toast.success('Transcribing: it appears here in a minute or two') } catch (e) { toast.error(errorOf(e).message) }
  }
  const canMake = admin && ai?.enabled && t?.status !== 'pending'
  return (
    <Drawer open onClose={onClose} title={`Transcript: ${label}`} icon={<FileText size={18} className="text-violet-300" aria-hidden />}
      footer={canMake ? <button type="button" className="btn" disabled={making} onClick={go}><RefreshCw size={14} className="text-violet-300" aria-hidden /> {t ? 'Transcribe again' : 'Transcribe this call'}</button> : undefined}>
      {isLoading ? <p className="text-sm text-slate-400">Loading…</p>
        : none ? (
          <p className="text-sm text-slate-400">
            This call has no transcript yet. {!ai?.enabled ? <>Switch on AI in <a className="text-sky-300 hover:underline" href="#/settings">Settings</a> to transcribe calls.</>
              : ai.calls ? 'It may have been recorded before transcripts were switched on.' : <>Transcribe it below, or switch on <span className="text-slate-200">Transcribe recorded calls</span> in Settings for every recorded call.</>}
          </p>
        ) : t && <TranscriptBody t={t} />}
    </Drawer>
  )
}

function TranscriptBody({ t }: { t: CallTranscript }) {
  if (t.status === 'pending') return <p role="status" className="flex items-center gap-2 text-sm text-amber-300"><Loader2 size={15} className="animate-spin" aria-hidden /> Transcribing this call…</p>
  if (t.status === 'failed') return <p role="alert" className="flex items-start gap-2 text-sm text-rose-300"><TriangleAlert size={15} className="mt-0.5 shrink-0" aria-hidden /> It could not be transcribed: {t.error}</p>
  return (
    <div className="space-y-5">
      {t.summary && (
        <section aria-labelledby="tr-sum">
          <h3 id="tr-sum" className="mb-1 flex items-center gap-1.5 text-sm font-semibold text-slate-200"><Sparkles size={14} className="text-violet-300" aria-hidden /> Summary</h3>
          <p className="text-sm text-slate-300">{t.summary}</p>
        </section>
      )}
      <section aria-labelledby="tr-act">
        <h3 id="tr-act" className="mb-1 flex items-center gap-1.5 text-sm font-semibold text-slate-200"><CheckSquare size={14} className="text-emerald-300" aria-hidden /> Action items</h3>
        {t.actionItems.length ? <ul className="list-disc space-y-1 pl-5 text-sm text-emerald-200">{t.actionItems.map((a, i) => <li key={i}>{a}</li>)}</ul>
          : <p className="text-sm text-slate-500">None.</p>}
      </section>
      {t.error && <p className="text-xs text-amber-300">{t.error}</p>}
      <InsightSection t={t} />
      <section aria-labelledby="tr-lines">
        <h3 id="tr-lines" className="mb-2 text-sm font-semibold text-slate-200">Who said what</h3>
        {t.lines.length === 0 ? <p className="text-sm text-slate-500">Nobody could be heard on the recording.</p> : (
          <ol className="space-y-2">
            {t.lines.map((l, i) => (
              <li key={i} className={`flex gap-3 text-sm ${l.side === 'caller' ? '' : 'flex-row-reverse text-right'}`}>
                <span className="w-12 shrink-0 pt-0.5 font-mono text-xs tabular-nums text-slate-500">{formatDuration(Math.round(l.second))}</span>
                <span className={`rounded-lg px-3 py-1.5 ${l.side === 'caller' ? 'bg-sky-950/60 text-sky-100' : 'bg-amber-950/50 text-amber-100'}`}>
                  <span className={`block text-xs font-medium ${l.side === 'caller' ? 'text-sky-300' : 'text-amber-300'}`}>{who(t, l.side)}</span>
                  {l.text}
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>
    </div>
  )
}

/** TranscriptSearch finds recorded calls by what was said in them. */
export function TranscriptSearch() {
  const [text, setText] = useState('')
  const [q, setQ] = useState('')
  useEffect(() => { const id = setTimeout(() => setQ(text.trim()), 350); return () => clearTimeout(id) }, [text])
  const { data, isFetching } = useSearchTranscriptsQuery(q, { skip: q.length < 2 })
  return (
    <section className="card mb-6 space-y-3 p-4" aria-labelledby="ts-h">
      <h2 id="ts-h" className="flex items-center gap-2 text-sm font-semibold text-slate-200"><Search size={15} className="text-violet-300" aria-hidden /> Search what was said</h2>
      <input className="input" type="search" placeholder="Words from a recorded call, such as boiler quote" aria-label="Search what was said" value={text} onChange={(e) => setText(e.target.value)} />
      {q.length >= 2 && (
        isFetching && !data ? <p className="text-sm text-slate-400">Searching…</p>
          : !data?.items.length ? <p className="text-sm text-slate-400">No transcribed call mentions <span className="text-slate-200">{q}</span>. Only recorded calls with a transcript can be searched.</p>
            : (
              <ul className="divide-y divide-slate-800" aria-label="Calls that mention it">
                {data.items.map((t) => (
                  <li key={t.recording} className="flex items-start gap-3 py-2">
                    <div className="min-w-0 flex-1">
                      <p className="text-sm text-slate-200">
                        <span className="text-xs text-slate-500">{new Date(t.at).toLocaleString()}</span>{' '}
                        <span className="font-mono text-sky-300">{t.from}</span> {t.fromName && <span className="text-slate-400">{t.fromName}</span>} to <span className="font-mono text-amber-300">{t.to}</span> {t.toName && <span className="text-slate-400">{t.toName}</span>}
                      </p>
                      <p className="truncate text-sm text-slate-400">{snippet(t, q)}</p>
                    </div>
                    <TranscriptButton name={t.recording} label={`${t.fromName || t.from} to ${t.toName || t.to}`} />
                  </li>
                ))}
              </ul>
            )
      )}
    </section>
  )
}

/** snippet is the first line that holds a searched word, or the summary. */
function snippet(t: CallTranscript, q: string) {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean)
  const l = t.lines.find((x) => words.some((w) => x.text.toLowerCase().includes(w)))
  return l ? `${who(t, l.side)}: ${l.text}` : t.summary
}

const obsLook = { 'went-well': ['text-emerald-300', 'Went well'], 'look-at': ['text-amber-300', 'Look at'], unanswered: ['text-rose-300', 'Not answered'], promise: ['text-sky-300', 'Promised'] } as const
const upsetMood = (m?: string) => m === 'frustrated' || m === 'angry' || m === 'upset'

/** InsightSection: how the call went, from the sound and the AI's notes, as evidence for a supervisor. */
function InsightSection({ t }: { t: CallTranscript }) {
  const admin = useIsAdmin()
  const [make, { isLoading }] = useMakeInsightMutation()
  const k = t.talk
  const talked = k ? k.callerTalk + k.calleeTalk : 0
  const share = k && talked ? Math.round((100 * k.calleeTalk) / talked) : 0
  return (
    <section aria-labelledby="tr-ins" className="space-y-2">
      <h3 id="tr-ins" className="flex items-center gap-1.5 text-sm font-semibold text-slate-200"><Gauge size={14} className="text-sky-300" aria-hidden /> How it went</h3>
      {k ? (
        <>
          <div className="flex h-3 overflow-hidden rounded bg-slate-800" role="img" aria-label={`${t.fromName || t.from} talked ${100 - share}%, ${t.toName || t.to} ${share}%`}>
            <div className="bg-sky-400" style={{ width: `${100 - share}%` }} /><div className="bg-amber-400" style={{ width: `${share}%` }} />
          </div>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-3">
            <div><dt className="text-slate-500">Talk</dt><dd className="text-slate-200"><span className="text-sky-300">{100 - share}%</span> / <span className="text-amber-300">{share}%</span></dd></div>
            <div><dt className="text-slate-500">Longest stretch</dt><dd className="text-slate-200">{formatDuration(Math.round(k.longestCaller))} / {formatDuration(Math.round(k.longestCallee))}</dd></div>
            <div><dt className="text-slate-500">Talked over the other</dt><dd className="text-slate-200">{k.callerCutIn} / {k.calleeCutIn}</dd></div>
            <div><dt className="text-slate-500">Both at once</dt><dd className="text-slate-200">{formatDuration(Math.round(k.overlap))}</dd></div>
            <div><dt className="text-slate-500">Quiet</dt><dd className={k.longestSilence >= 30 ? 'text-amber-300' : 'text-slate-200'}>{formatDuration(Math.round(k.silence))}, longest {formatDuration(Math.round(k.longestSilence))}</dd></div>
          </dl>
        </>
      ) : <p className="text-sm text-slate-500">No measures yet.</p>}
      {t.insight && (
        <>
          {t.insight.moodStart && <p className="text-sm text-slate-300">The caller seemed <span className={upsetMood(t.insight.moodStart) ? 'text-rose-300' : 'text-slate-100'}>{t.insight.moodStart}</span> at the start and <span className={upsetMood(t.insight.moodEnd) ? 'text-rose-300' : 'text-emerald-300'}>{t.insight.moodEnd}</span> at the end.</p>}
          {t.insight.observations.length > 0 && (
            <ul className="space-y-1.5 text-sm">
              {t.insight.observations.map((o, i) => (
                <li key={i} className="flex gap-2">
                  <span className="w-12 shrink-0 font-mono text-xs tabular-nums text-slate-500">{formatDuration(Math.round(o.second))}</span>
                  <span><span className={`mr-1 text-xs font-semibold ${obsLook[o.kind][0]}`}>{obsLook[o.kind][1]}:</span><span className="text-slate-200">{o.note}</span>
                    {o.quote && <span className="block text-xs italic text-slate-400">“{o.quote}”</span>}</span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      <p className="text-xs text-slate-500">Evidence to listen to, not a score: the measures come from the sound, the notes from the AI reading the transcript.</p>
      {admin && <button type="button" className="btn" disabled={isLoading} onClick={() => make(t.recording).unwrap().catch((e) => toast.error(errorOf(e).problems?.[0]?.message ?? errorOf(e).message))}>
        <Sparkles size={14} className="text-violet-300" aria-hidden /> {t.insight ? 'Look again' : 'Look at this call'}</button>}
      <Coaching t={t} />
    </section>
  )
}

/** "m:ss" or plain seconds to seconds; NaN when it is neither. */
function parseMoment(s: string) {
  const m = s.trim().match(/^(?:(\d+):)?(\d+)$/)
  return m ? Number(m[1] ?? 0) * 60 + Number(m[2]) : NaN
}

/** Coaching: supervisors' own notes on moments of the call, written by administrators, read by everyone who sees the transcript. */
function Coaching({ t }: { t: CallTranscript }) {
  const admin = useIsAdmin()
  const [add, { isLoading }] = useAddCoachingMutation()
  const [del] = useDeleteCoachingMutation()
  const [at, setAt] = useState('')
  const [note, setNote] = useState('')
  const [err, setErr] = useState<{ field?: string; message: string } | null>(null)
  const notes = t.coaching ?? []
  if (!admin && !notes.length) return null
  const save = () => {
    const second = at.trim() ? parseMoment(at) : 0
    if (Number.isNaN(second)) { setErr({ field: 'second', message: 'write the moment as m:ss, such as 1:05' }); return }
    add({ name: t.recording, second, note }).unwrap()
      .then(() => { setAt(''); setNote(''); setErr(null); toast.success('Note added') })
      .catch((x) => { const p = errorOf(x).problems?.[0]; setErr({ field: p?.field, message: p?.message ?? errorOf(x).message }) })
  }
  return (
    <div className="space-y-2 border-t border-slate-800 pt-2" aria-labelledby="tr-coach">
      <h4 id="tr-coach" className="flex items-center gap-1.5 text-sm font-semibold text-slate-200"><MessageSquarePlus size={14} className="text-violet-300" aria-hidden /> Coaching notes</h4>
      {notes.length > 0 ? (
        <ul className="space-y-1.5 text-sm">
          {notes.map((n) => (
            <li key={n.id} className="flex items-start gap-2">
              <span className="w-12 shrink-0 font-mono text-xs tabular-nums text-slate-500">{formatDuration(Math.round(n.second))}</span>
              <span className="flex-1"><span className="text-slate-200">{n.note}</span>
                <span className="block text-xs text-slate-500">{n.by}, {new Date(n.at).toLocaleString()}</span></span>
              {admin && <button type="button" className="btn btn-ghost p-1" aria-label={`Remove the note at ${formatDuration(Math.round(n.second))}`}
                onClick={() => del({ name: t.recording, id: n.id }).unwrap().catch((x) => toast.error(errorOf(x).message))}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>}
            </li>
          ))}
        </ul>
      ) : <p className="text-xs text-slate-500">No notes yet. Write what to keep doing or try differently, at the moment it happened.</p>}
      {admin && (
        // The drawer is a form already: this is a group, saved by its button or Enter in its fields.
        <div role="group" aria-label="New coaching note" className="flex flex-wrap items-start gap-2"
          onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); save() } }}>
          <label className="flex flex-col text-xs text-slate-400">Moment
            <input className={`input w-20 font-mono ${err?.field === 'second' ? 'border-rose-400' : ''}`} placeholder="0:00" value={at} onChange={(e) => setAt(e.target.value)}
              aria-invalid={err?.field === 'second'} aria-describedby={err ? 'coach-err' : undefined} /></label>
          <label className="flex min-w-48 flex-1 flex-col text-xs text-slate-400">Coaching note
            <input className={`input ${err?.field === 'note' ? 'border-rose-400' : ''}`} maxLength={1000} value={note} onChange={(e) => setNote(e.target.value)}
              aria-invalid={err?.field === 'note'} aria-describedby={err ? 'coach-err' : undefined} /></label>
          <button type="button" className="btn btn-primary mt-4" disabled={isLoading} onClick={save}>Add note</button>
          {err && <p id="coach-err" role="alert" className="w-full text-xs text-rose-300">{err.message}</p>}
        </div>
      )}
    </div>
  )
}
