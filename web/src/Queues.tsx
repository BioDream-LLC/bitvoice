import { Clock, Crown, Headset, ListOrdered, LogIn, LogOut, Pause, PhoneIncoming, PhoneMissed, Play, Plus, Timer } from 'lucide-react'
import { toast } from 'sonner'
import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { errorOf, useQueueStatusQuery, useQueuesQuery, useSetAgentMutation, type AgentState, type Queue, type QueueStatus } from './api'
import { useOpenParam } from './useOpenParam'
import { Vips } from './Vips'
import { nextFree, useTakenNumbers } from './numbers'
import { useIsAdmin } from './Editor'
import { QueueEditor } from './QueueEditor'
import { Chip, Empty, Page, formatDuration } from './ui'
import { useNow } from './useNow'

export const agentColours: Record<AgentState, [string, string]> = {
  available: ['text-emerald-300', 'bg-emerald-400'],
  ringing: ['text-amber-300', 'bg-amber-400'],
  busy: ['text-sky-300', 'bg-sky-400'],
  'wrap-up': ['text-violet-300', 'bg-violet-400'],
  paused: ['text-orange-300', 'bg-orange-400'],
  away: ['text-violet-300', 'bg-violet-400'],
  'logged-out': ['text-slate-400', 'bg-slate-500'],
  offline: ['text-slate-500', 'bg-slate-600'],
}
const agentLabels: Record<AgentState, string> = {
  available: 'Available', ringing: 'Ringing', busy: 'On a call', 'wrap-up': 'Wrap-up', paused: 'Paused', away: 'Away', 'logged-out': 'Logged out', offline: 'No phone',
}

export const blankQueue: Queue = {
  id: '', number: '', name: '', agents: [], strategy: 'longest-idle', ringSeconds: 15, wrapUpSeconds: 10, maxWaitSeconds: 0, timeout: null,
}

export function Queues() {
  const admin = useIsAdmin()
  const { data } = useQueuesQuery()
  const { data: status } = useQueueStatusQuery()
  const [edit, setEdit] = useState<Queue | null>(null)
  useOpenParam(data?.items, setEdit)
  const [params, setParams] = useSearchParams()
  const taken = useTakenNumbers()
  const add = () => {
    setEdit({ ...blankQueue, number: nextFree(taken, 700) })
  }
  useEffect(() => { if (params.get('new') && admin) { add(); setParams({}, { replace: true }) } }, [params, admin]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <Page icon={ListOrdered} title="Queues" accent="from-orange-400 to-amber-400"
      intro="Callers wait their turn and go to the next free agent: support lines, reception, a contact centre."
      actions={admin && <button className="btn btn-primary" onClick={add}><Plus size={15} aria-hidden /> Add queue</button>}>
      {!data?.items.length ? <Empty icon={ListOrdered} title="No queues yet">A queue holds callers until someone is free, instead of ringing everyone at once.</Empty> : (
        <div className="grid gap-4 xl:grid-cols-2">
          {data.items.map((q) => (
            <Board key={q.id} q={q} s={status?.items.find((x) => x.number === q.number)} onEdit={() => setEdit(q)} />
          ))}
        </div>
      )}
      <Vips />
      {edit && <QueueEditor q={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function Board({ q, s, onEdit }: { q: Queue; s?: QueueStatus; onEdit: () => void }) {
  const now = useNow()
  const waiting = s?.waiting ?? []
  const longest = waiting.length ? Math.max(...waiting.map((w) => (now - Date.parse(w.since)) / 1000)) : 0
  const free = s?.agents.filter((a) => a.state === 'available').length ?? 0
  return (
    <section className="card p-5">
      <button className="flex w-full items-center gap-3 text-left" onClick={onEdit} aria-label={`Edit queue ${q.name}`}>
        <Chip kind="queue">{q.number}</Chip>
        <h2 className="text-lg font-semibold text-slate-100">{q.name}</h2>
        <span className="ml-auto text-xs text-slate-500">{q.strategy.replace('-', ' ')}</span>
      </button>
      <div className="mt-4 grid grid-cols-4 gap-3 text-center">
        <Stat icon={Clock} label="Waiting" value={String(waiting.length)} colour={waiting.length ? (free ? 'text-amber-300' : 'text-rose-300') : 'text-slate-400'} />
        <Stat icon={Timer} label="Longest wait" value={formatDuration(Math.round(longest))} colour={longest > 120 ? 'text-rose-300' : longest > 30 ? 'text-amber-300' : 'text-slate-300'} />
        <Stat icon={PhoneIncoming} label="Answered" value={String(s?.totals.answered ?? 0)} colour="text-emerald-300" />
        <Stat icon={PhoneMissed} label="Gave up" value={String((s?.totals.abandoned ?? 0) + (s?.totals.timedOut ?? 0))} colour={(s?.totals.abandoned ?? 0) ? 'text-rose-300' : 'text-slate-400'} />
      </div>
      {waiting.length > 0 && (
        <ol className="mt-4 space-y-1">
          {waiting.map((w, i) => (
            <li key={w.callId} className="flex items-center gap-3 rounded-lg bg-slate-900/60 px-3 py-1.5 text-sm">
              <span className="w-5 text-xs font-bold text-orange-300">{i + 1}</span>
              <span className="font-mono text-amber-300">{w.from}</span>
              {w.vip && <span className="inline-flex items-center gap-1 rounded bg-amber-400/15 px-1.5 text-xs font-semibold text-amber-300"><Crown size={11} aria-hidden /> VIP</span>}
              <span className="truncate text-slate-400">{w.name}</span>
              <span className="ml-auto font-mono text-xs tabular-nums text-slate-300">{formatDuration(Math.round((now - Date.parse(w.since)) / 1000))}</span>
            </li>
          ))}
        </ol>
      )}
      <ul className="mt-4 grid gap-2 sm:grid-cols-2">
        {s?.agents.map((a) => {
          const [text, dot] = agentColours[a.state]
          return (
            <li key={a.extension} className="flex items-center gap-2 rounded-lg border border-slate-800 px-3 py-2 text-sm">
              <Headset size={15} className={text} aria-hidden />
              <span className="font-mono text-sky-300">{a.extension}</span>
              <span className="truncate text-slate-300">{a.name}</span>
              <span className={`ml-auto flex items-center gap-1.5 text-xs ${text}`} title={a.reason}>
                <span className={`h-2 w-2 rounded-full ${dot} ${a.state === 'ringing' ? 'animate-pulse' : ''}`} aria-hidden />{agentLabels[a.state]}{a.reason ? `: ${a.reason}` : ''}
              </span>
              <AgentControls ext={a.extension} name={a.name} state={a.state} />
            </li>
          )
        })}
      </ul>
    </section>
  )
}

function Stat({ icon: Icon, label, value, colour }: { icon: typeof Clock; label: string; value: string; colour: string }) {
  return (
    <div className="rounded-lg bg-slate-900/60 p-2">
      <div className={`text-xl font-bold tabular-nums ${colour}`}>{value}</div>
      <div className="flex items-center justify-center gap-1 text-xs text-slate-500"><Icon size={11} aria-hidden />{label}</div>
    </div>
  )
}

/** Pause, resume, log in or out an agent: what the phone codes do, from the wallboard. */
function AgentControls({ ext, name, state }: { ext: string; name: string; state: AgentState }) {
  const admin = useIsAdmin()
  const [set, { isLoading }] = useSetAgentMutation()
  const [asking, setAsking] = useState(false)
  if (!admin || state === 'offline') return null
  const go = (v: { loggedOut: boolean; paused: boolean; reason?: string }) => set({ ext, ...v }).unwrap().catch((e) => toast.error(errorOf(e).message))
  const who = name || ext
  return (
    <span className="flex gap-1">
      {state === 'logged-out' ? (
        <button className="btn px-2 py-0.5 text-xs" disabled={isLoading} aria-label={`Log ${who} in`} onClick={() => go({ loggedOut: false, paused: false })}><LogIn size={12} aria-hidden /></button>
      ) : (
        <>
          {state === 'paused' ? (
            <button className="btn px-2 py-0.5 text-xs text-emerald-300" disabled={isLoading} aria-label={`Resume ${who}`} onClick={() => go({ loggedOut: false, paused: false })}><Play size={12} aria-hidden /></button>
          ) : (
            asking ? (
              <select autoFocus className="input py-0 text-xs" aria-label={`Why ${who} is pausing`} defaultValue=""
                onBlur={() => setAsking(false)}
                onChange={(e) => { if (e.target.value) { go({ loggedOut: false, paused: true, reason: e.target.value }); setAsking(false) } }}>
                <option value="" disabled>Why?</option>
                {['Break', 'Lunch', 'Meeting', 'Training', 'Paperwork', 'Other'].map((r) => <option key={r}>{r}</option>)}
              </select>
            ) : (
              <button className="btn px-2 py-0.5 text-xs text-orange-300" disabled={isLoading} aria-label={`Pause ${who}`} onClick={() => setAsking(true)}><Pause size={12} aria-hidden /></button>
            )
          )}
          <button className="btn px-2 py-0.5 text-xs" disabled={isLoading} aria-label={`Log ${who} out`} onClick={() => go({ loggedOut: true, paused: false })}><LogOut size={12} aria-hidden /></button>
        </>
      )}
    </span>
  )
}
