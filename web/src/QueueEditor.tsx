import { clsx } from 'clsx'
import { ListOrdered } from 'lucide-react'
import { useState } from 'react'
import {
  useCreateQueueMutation, useDeleteQueueMutation, useExtensionsQuery, useUpdateQueueMutation, type ApiErrorBody, type Queue,
} from './api'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { Field, Toggle, problemFor } from './ui'

const strategies = [
  ['longest-idle', 'Longest idle', 'The agent free the longest gets the next call. Fair to agents.'],
  ['round-robin', 'Round robin', 'Agents take turns in list order.'],
  ['ring-all', 'Ring everyone', 'Every free agent rings; first to answer gets it.'],
] as const

export function QueueEditor({ q: initial, onClose }: { q: Queue; onClose: () => void }) {
  const admin = useIsAdmin()
  const [q, setQ] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: exts } = useExtensionsQuery()
  const [create] = useCreateQueueMutation()
  const [update] = useUpdateQueueMutation()
  const [remove] = useDeleteQueueMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const toggle = (n: string) => setQ({ ...q, agents: q.agents.includes(n) ? q.agents.filter((a) => a !== n) : [...q.agents, n] })
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
          <div className="grid gap-2 sm:grid-cols-3">
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
                  {on && q.strategy === 'round-robin' && <span className="ml-auto text-xs text-orange-300">#{q.agents.indexOf(x.number) + 1}</span>}
                </button>
              )
            })}
          </div>
        </Field>
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
