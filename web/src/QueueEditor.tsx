import { clsx } from 'clsx'
import { ArrowDown, ArrowUp, ListOrdered } from 'lucide-react'
import { useState } from 'react'
import {
  useCreateQueueMutation, useDeleteQueueMutation, useExtensionsQuery, usePromptsQuery, useSettingsQuery, useUpdateQueueMutation,
  type ApiErrorBody, type Queue,
} from './api'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { Field, Toggle, problemFor } from './ui'

const strategies = [
  ['longest-idle', 'Longest idle', 'The agent free the longest gets the next call. Fair to agents.'],
  ['round-robin', 'Round robin', 'Agents take turns in list order, carrying on from the last one.'],
  ['ring-all', 'Ring everyone', 'Every free agent rings; first to answer gets it.'],
  ['fewest-calls', 'Fewest calls', 'The agent who took the fewest calls today.'],
  ['linear', 'In order', 'Always from the top of the list: the first free agent.'],
  ['random', 'Random', 'Any free agent, by chance.'],
  ['weighted-random', 'Weighted random', 'By chance, agents with a lower penalty much more often.'],
] as const

export function QueueEditor({ q: initial, onClose }: { q: Queue; onClose: () => void }) {
  const admin = useIsAdmin()
  const [q, setQ] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: exts } = useExtensionsQuery()
  const { data: settings } = useSettingsQuery()
  const { data: langs } = usePromptsQuery()
  const [create] = useCreateQueueMutation()
  const [update] = useUpdateQueueMutation()
  const [remove] = useDeleteQueueMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const toggle = (n: string) => setQ({ ...q, agents: q.agents.includes(n) ? q.agents.filter((a) => a !== n) : [...q.agents, n] })
  const move = (i: number, by: number) => {
    const a = [...q.agents]
    const [x] = a.splice(i, 1)
    a.splice(i + by, 0, x)
    setQ({ ...q, agents: a })
  }
  const setPenalty = (n: string, v: number) => {
    const pen = { ...(q.penalties ?? {}) }
    if (v > 0) pen[n] = v
    else delete pen[n]
    setQ({ ...q, penalties: pen })
  }
  const name = (n: string) => exts?.items.find((x) => x.number === n)?.name ?? ''
  const save = async () => {
    const e = await attempt(() => (isNew ? create(q) : update(q)).unwrap(), `Queue ${q.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  const timeoutKey = q.timeout?.type === 'voicemail' ? `voicemail:${q.timeout.target}` : 'hangup'
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New queue' : q.name} icon={<ListOrdered className="text-orange-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Queue removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="qn" error={p('number')}><input id="qn" className="input font-mono text-orange-300" value={q.number} onChange={(e) => { const v = e.target.value.replace(/\D/g, ''); setQ((q0) => ({ ...q0, number: v })) }} /></Field>
          <Field label="Name" htmlFor="qm" error={p('name')}><input id="qm" className="input" autoFocus={isNew} value={q.name} onChange={(e) => { const v = e.target.value; setQ((q0) => ({ ...q0, name: v })) }} placeholder="Support" /></Field>
        </div>
        <div>
          <span className="label">Who gets the next caller</span>
          <div className="grid gap-2 sm:grid-cols-4">
            {strategies.map(([id, t, d]) => (
              <button type="button" key={id} aria-pressed={q.strategy === id} onClick={() => setQ({ ...q, strategy: id })}
                className={clsx('card p-3 text-left', q.strategy === id && 'ring-2 ring-orange-400')}>
                <div className="text-sm font-semibold text-orange-300">{t}</div>
                <div className="text-xs text-slate-500">{d}</div>
              </button>
            ))}
          </div>
        </div>
        <Field label="Agents" error={p('agents')}>
          <div className="grid gap-1.5 sm:grid-cols-2">
            {exts?.items.map((x) => {
              const on = q.agents.includes(x.number)
              return (
                <button type="button" key={x.id} aria-pressed={on} onClick={() => toggle(x.number)}
                  className={clsx('flex items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm',
                    on ? 'border-sky-600 bg-sky-950/60 text-slate-100' : 'border-slate-800 text-slate-400 hover:border-slate-700')}>
                  <span className="font-mono text-sky-300">{x.number}</span> <span className="truncate">{x.name}</span>
                  {on && <span className="ml-auto text-xs text-orange-300">#{q.agents.indexOf(x.number) + 1}</span>}
                </button>
              )
            })}
          </div>
        </Field>
        {q.agents.length > 1 && (
          <div>
            <span className="label">Order and penalties</span>
            <p className="mb-2 text-xs text-slate-500">Order is used by In order and Round robin. A lower penalty is offered calls first; an agent with a higher one
              only when no one with a lower one is free (0 when empty).</p>
            <ol className="space-y-1">
              {q.agents.map((n, i) => (
                <li key={n} className="flex items-center gap-2 text-sm">
                  <span className="w-6 text-right font-mono text-xs text-orange-300">{i + 1}</span>
                  <span className="font-mono text-sky-300">{n}</span><span className="truncate text-slate-300">{name(n)}</span>
                  <span className="ml-auto flex items-center gap-1">
                    <button type="button" className="btn px-2 py-1" aria-label={`Move ${n} up`} disabled={i === 0} onClick={() => move(i, -1)}><ArrowUp size={12} aria-hidden /></button>
                    <button type="button" className="btn px-2 py-1" aria-label={`Move ${n} down`} disabled={i === q.agents.length - 1} onClick={() => move(i, 1)}><ArrowDown size={12} aria-hidden /></button>
                    <label className="flex items-center gap-1 text-xs text-slate-400">Penalty
                      <input type="number" min={0} max={100} className="input w-16 py-1" aria-label={`Penalty of ${n}`} value={q.penalties?.[n] || ''}
                        onChange={(e) => setPenalty(n, Number(e.target.value))} />
                    </label>
                  </span>
                </li>
              ))}
            </ol>
            {p('penalties') && <p className="mt-1 text-sm text-rose-300">{p('penalties')}</p>}
          </div>
        )}
        <div className="grid grid-cols-3 gap-3">
          <Field label="Ring each agent (s)" htmlFor="qr" error={p('ringSeconds')}><input id="qr" type="number" min={5} max={120} className="input" value={q.ringSeconds} onChange={(e) => { const v = Number(e.target.value); setQ((q0) => ({ ...q0, ringSeconds: v })) }} /></Field>
          <Field label="Wrap-up (s)" htmlFor="qw" error={p('wrapUpSeconds')} hint="Breathing room after each call."><input id="qw" type="number" min={0} max={600} className="input" value={q.wrapUpSeconds} onChange={(e) => { const v = Number(e.target.value); setQ((q0) => ({ ...q0, wrapUpSeconds: v })) }} /></Field>
          <Field label="Longest wait (s)" htmlFor="qx" error={p('maxWaitSeconds')} hint="0: until the caller hangs up."><input id="qx" type="number" min={0} max={7200} className="input" value={q.maxWaitSeconds} onChange={(e) => { const v = Number(e.target.value); setQ((q0) => ({ ...q0, maxWaitSeconds: v })) }} /></Field>
        </div>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={q.sticky ?? false} onChange={(v) => setQ((q0) => ({ ...q0, sticky: v }))} label="Sticky" />
          <span>Returning callers ring the agent who last answered them first <span className="text-xs text-slate-500">(if free; then the queue goes on as usual)</span></span></label>
        {q.sticky && (
          <Field label="Remember for (days)" htmlFor="qsd" error={p('stickyDays')} hint="30 when empty.">
            <input id="qsd" type="number" min={0} max={365} className="input w-28" value={q.stickyDays || ''} onChange={(e) => { const v = Number(e.target.value); setQ((q0) => ({ ...q0, stickyDays: v })) }} />
          </Field>
        )}
        <div className="grid grid-cols-3 gap-3">
          <Field label="Hold music" htmlFor="qmu" error={p('music')}>
            <select id="qmu" className="input" value={q.music ?? ''} onChange={(e) => setQ({ ...q, music: e.target.value })}>
              <option value="">The system's ({settings?.system.music?.find((m) => m.id === settings.system.holdMusic)?.name ?? 'tone'})</option>
              <option value="tone">The queue tone</option>
              {settings?.system.music?.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
            </select>
          </Field>
          <Field label="Language" htmlFor="qla" error={p('language')}>
            <select id="qla" className="input" value={q.language ?? ''} onChange={(e) => setQ({ ...q, language: e.target.value })}>
              <option value="">The system's</option>
              {langs?.languages.map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
            </select>
          </Field>
          <Field label="Weight" htmlFor="qwe" error={p('weight')} hint="Queues sharing agents: the higher weight is served first.">
            <input id="qwe" type="number" min={0} max={100} className="input" value={q.weight || ''} onChange={(e) => setQ({ ...q, weight: Number(e.target.value) })} />
          </Field>
        </div>
        <div className="space-y-2">
          <span className="label">Tell waiting callers</span>
          <div className="flex flex-wrap items-center gap-4 text-sm text-slate-300">
            <label className="flex items-center gap-2"><Toggle checked={!!q.announcePosition} onChange={(v) => setQ({ ...q, announcePosition: v, announceSeconds: q.announceSeconds || 60 })} label="Announce place in line" /> their place in line</label>
            <label className="flex items-center gap-2"><Toggle checked={!!q.announceWait} onChange={(v) => setQ({ ...q, announceWait: v, announceSeconds: q.announceSeconds || 60 })} label="Announce expected wait" /> the expected wait</label>
            {(q.announcePosition || q.announceWait) && (
              <Field label="Every (s)" htmlFor="qan" error={p('announceSeconds')}>
                <input id="qan" type="number" min={15} max={600} className="input w-24" value={q.announceSeconds || ''} onChange={(e) => setQ({ ...q, announceSeconds: Number(e.target.value) })} />
              </Field>
            )}
          </div>
          {(q.announcePosition || q.announceWait) && <p className="text-xs text-slate-500">Spoken with the queue prompts in Sounds; nothing is said until they are recorded.</p>}
        </div>
        {q.maxWaitSeconds > 0 && (
          <Field label="After the longest wait" htmlFor="qt" error={p('timeout')}>
            <select id="qt" className="input" value={timeoutKey} onChange={(e) => {
              const [type, target] = e.target.value.split(':')
              setQ({ ...q, timeout: type === 'voicemail' ? { type: 'voicemail', target } : { type: 'hangup', target: '' } })
            }}>
              <option value="hangup">Say goodbye (a tone) and hang up</option>
              {exts?.items.filter((x) => x.voicemail?.enabled).map((x) => <option key={x.id} value={`voicemail:${x.number}`}>Leave a message for {x.number} {x.name}</option>)}
            </select>
          </Field>
        )}
      </fieldset>
    </Drawer>
  )
}
