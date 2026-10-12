import { clsx } from 'clsx'
import { Braces, FlaskConical, Plus } from 'lucide-react'
import { useEffect, useState } from 'react'
import {
  useCreateScriptMutation, useDeleteScriptMutation, useScriptsQuery, useTestScriptMutation, useUpdateScriptMutation,
  type ApiErrorBody, type Script, type ScriptCall, type ScriptTest,
} from './api'
import { useOpenParam } from './useOpenParam'
import { CodeEditor } from './CodeEditor'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { DestinationPicker } from './Extensions'
import { onServerEvent } from './store'
import { Badge, Empty, Field, Page, problemFor, relTime, radioKeys } from './ui'

const examples: Record<'js' | 'lua', string> = {
  js: `// The call is in \`call\`: from, fromName, to, direction, trunk, weekday (0 = Sunday), hour, minute, date.
// Return {type, target} to choose a destination, or nothing to use the fallback.
if (call.from.startsWith("212")) {
  log("New York caller")
  return { type: "ringgroup", target: "600" }
}
if (call.weekday === 0 || call.weekday === 6) return { type: "voicemail", target: "100" }`,
  lua: `-- The call is in \`call\`: from, fromName, to, direction, trunk, weekday (0 = Sunday), hour, minute, date.
-- Return {type=..., target=...} to choose a destination, or nothing to use the fallback.
if string.sub(call.from, 1, 3) == "212" then
  log("New York caller")
  return { type = "ringgroup", target = "600" }
end
if call.weekday == 0 or call.weekday == 6 then
  return { type = "voicemail", target = "100" }
end`,
}

interface Run { script: string; at: string; from: string; to: string; decision: string; error?: string; millis: number }

export function Scripts() {
  const admin = useIsAdmin()
  const { data } = useScriptsQuery()
  const [edit, setEdit] = useState<Script | null>(null)
  useOpenParam(data?.items, setEdit)
  const [runs, setRuns] = useState<Run[]>([])
  useEffect(() => onServerEvent((type, d) => { if (type === 'script.ran') setRuns((r) => [d as Run, ...r].slice(0, 50)) }), [])
  const nameOf = (id: string) => data?.items.find((s) => s.id === id)?.name ?? id
  return (
    <Page icon={Braces} title="Scripts" accent="from-fuchsia-400 to-sky-400"
      intro="Route calls with a few lines of JavaScript or Lua when menus and schedules are not enough. A script that fails or takes too long never loses the call: it goes to the fallback. Emergency calls never run scripts."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', name: 'Route by caller', language: 'js', source: examples.js, fallback: { type: 'hangup', target: '' } })}><Plus size={15} aria-hidden /> Add script</button>}>
      {!data?.items.length ? <Empty icon={Braces} title="No scripts yet">Use a script as the destination of an incoming route.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((s) => (
            <button key={s.id} className="card flex items-center gap-3 p-4 text-left hover:border-slate-700" onClick={() => setEdit(s)}>
              <Braces size={18} className={s.language === 'js' ? 'text-amber-300' : 'text-sky-300'} aria-hidden />
              <span className="font-semibold text-slate-100">{s.name}</span>
              <span className={clsx('ml-auto text-xs', s.language === 'js' ? 'text-amber-300' : 'text-sky-300')}>{s.language === 'js' ? 'JavaScript' : 'Lua'}</span>
            </button>
          ))}
        </div>
      )}
      {runs.length > 0 && (
        <section className="mt-8">
          <h2 className="mb-2 text-lg font-semibold text-slate-100">Recent runs</h2>
          <ul className="card divide-y divide-slate-800/70 text-sm">
            {runs.map((r, i) => (
              <li key={i} className="flex items-center gap-3 px-4 py-2">
                <span className="w-28 text-xs text-slate-500">{relTime(r.at)}</span>
                <span className="text-fuchsia-300">{nameOf(r.script)}</span>
                <span className="font-mono text-xs text-amber-300">{r.from} → {r.to}</span>
                <span className={r.error ? 'text-rose-300' : 'text-emerald-300'}>{r.error ?? r.decision}</span>
                <span className="ml-auto font-mono text-xs text-slate-500">{r.millis.toFixed(1)} ms</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {edit && <ScriptEditor s={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function ScriptEditor({ s: initial, onClose }: { s: Script; onClose: () => void }) {
  const admin = useIsAdmin()
  const [s, setS] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [create] = useCreateScriptMutation()
  const [update] = useUpdateScriptMutation()
  const [remove] = useDeleteScriptMutation()
  const [test, { isLoading }] = useTestScriptMutation()
  const now = new Date()
  const [call, setCall] = useState<ScriptCall>({ from: '2125550100', fromName: 'Test caller', to: '100', direction: 'inbound', trunk: '',
    weekday: now.getDay(), hour: now.getHours(), minute: now.getMinutes(), date: now.toLocaleDateString('en-CA') })
  const [result, setResult] = useState<ScriptTest | null>(null)
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(s) : update(s)).unwrap(), `Script ${s.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New script' : s.name} icon={<Braces className="text-fuchsia-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Script removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[1fr_auto] items-end gap-4">
          <Field label="Name" htmlFor="sn" error={p('name')}><input id="sn" className="input" value={s.name} onChange={(e) => { const v = e.target.value; setS((s0) => ({ ...s0, name: v })) }} /></Field>
          <div className="flex rounded-lg border border-slate-800 p-0.5" role="radiogroup" aria-label="Language">
            {(['js', 'lua'] as const).map((l, i) => (
              <button type="button" key={l} role="radio" aria-checked={s.language === l}
                {...radioKeys(i, ['js', 'lua'] as const, s.language, (v) => setS((s0) => ({ ...s0, language: v, source: s0.source === examples[s0.language] ? examples[v] : s0.source })))}
                onClick={() => setS((s0) => ({ ...s0, language: l, source: s0.source === examples[s0.language] ? examples[l] : s0.source }))}
                className={clsx('rounded-md px-3 py-1.5 text-sm', s.language === l ? 'bg-slate-800 font-semibold ' + (l === 'js' ? 'text-amber-300' : 'text-sky-300') : 'text-slate-500')}>
                {l === 'js' ? 'JavaScript' : 'Lua'}
              </button>
            ))}
          </div>
        </div>
        <div>
          <CodeEditor key={s.language} label="Script" language={s.language} value={s.source} onChange={(v) => setS((x) => ({ ...x, source: v }))} readOnly={!admin} />
          {p('source') && <p className="mt-1 font-mono text-xs text-rose-300">{p('source')}</p>}
        </div>
        <DestinationPicker label="Fallback: when the script has no answer or fails" required value={s.fallback} onChange={(d) => d && setS((s0) => ({ ...s0, fallback: d }))} error={p('fallback')} />
      </fieldset>
      <section className="card space-y-3 p-4">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-slate-100"><FlaskConical size={15} className="text-emerald-300" aria-hidden /> Try it</h3>
        <div className="grid grid-cols-3 gap-2">
          <Field label="From" htmlFor="tf"><input id="tf" className="input font-mono" value={call.from} onChange={(e) => { const v = e.target.value; setCall((call0) => ({ ...call0, from: v })) }} /></Field>
          <Field label="To" htmlFor="tt"><input id="tt" className="input font-mono" value={call.to} onChange={(e) => { const v = e.target.value; setCall((call0) => ({ ...call0, to: v })) }} /></Field>
          <Field label="Hour" htmlFor="th"><input id="th" type="number" min={0} max={23} className="input" value={call.hour} onChange={(e) => { const v = Math.min(23, Math.max(0, Math.trunc(Number(e.target.value) || 0))); setCall((call0) => ({ ...call0, hour: v })) }} /></Field>
        </div>
        <button type="button" className="btn" disabled={isLoading} onClick={async () => {
          try { setResult(await test({ language: s.language, source: s.source, call }).unwrap()) } catch { /* shown by the server's message */ }
        }}><FlaskConical size={14} className="text-emerald-300" aria-hidden /> Run</button>
        {result && (
          <div className="space-y-1 text-sm">
            {result.error
              ? <p className="text-rose-300">{result.error} <span className="text-slate-500">(the call would go to the fallback)</span></p>
              : result.type
                ? <p><Badge status="ok">Goes to</Badge> <span className="text-emerald-300">{result.type} {result.target}</span></p>
                : <p className="text-slate-400">No opinion: the call goes to the fallback.</p>}
            {result.log?.map((l, i) => <p key={i} className="font-mono text-xs text-violet-300">log: {l}</p>)}
            <p className="text-xs text-slate-500">{result.millis.toFixed(2)} ms</p>
          </div>
        )}
      </section>
    </Drawer>
  )
}
