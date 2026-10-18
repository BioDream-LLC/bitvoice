import { clsx } from 'clsx'
import { Grid3x3, Plus } from 'lucide-react'
import { useState } from 'react'
import {
  useCreateMenuMutation, useDeleteMenuMutation, useMenusQuery, useResetPromptMutation, useSavePromptMutation,
  useUpdateMenuMutation, useVoicemailQuery, type ApiErrorBody, type Menu,
} from './api'
import { useOpenParam } from './useOpenParam'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { DestinationPicker, useDescribe } from './Extensions'
import { AudioRecording } from './Greeting'
import { Badge, Empty, Field, Page, Toggle, problemFor, freshName } from './ui'

const keypad = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '*', '0', '#']

const blank: Menu = { id: '', number: '', name: 'Main menu', dialExtensions: true, timeoutSeconds: 5, repeats: 2, options: [], timeout: null }

export function Menus() {
  const admin = useIsAdmin()
  const describe = useDescribe()
  const { data } = useMenusQuery()
  const { data: vm } = useVoicemailQuery()
  const [edit, setEdit] = useState<Menu | null>(null)
  useOpenParam(data?.items, setEdit)
  return (
    <Page icon={Grid3x3} title="Menus" accent="from-sky-400 to-violet-400"
      intro='Auto attendants: callers hear a recording ("press 1 for sales, 2 for support") and choose with a key.'
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ ...blank, name: freshName(blank.name, data?.items.map((x) => x.name)) })}><Plus size={15} aria-hidden /> Add menu</button>}>
      {!data?.items.length ? <Empty icon={Grid3x3} title="No menus yet">Send your main number to a menu to greet callers and let them choose.</Empty> : (
        <div className="grid gap-4 lg:grid-cols-2">
          {data.items.map((m) => (
            <button key={m.id} className="card p-5 text-left hover:border-slate-700" onClick={() => setEdit(m)}>
              <div className="flex items-center gap-3">
                <Grid3x3 size={18} className="text-sky-300" aria-hidden />
                <h2 className="font-semibold text-slate-100">{m.name}</h2>
                {m.number && <span className="font-mono text-xs text-sky-300">{m.number}</span>}
                <span className="ml-auto">{vm?.greetings?.includes(`menu-${m.id}`) ? <Badge status="ok">Prompt recorded</Badge> : <Badge status="warn">No prompt</Badge>}</span>
              </div>
              <ul className="mt-3 space-y-1 text-sm">
                {m.options.map((o) => (
                  <li key={o.key} className="flex items-center gap-2">
                    <span className="grid h-6 w-6 place-items-center rounded bg-slate-800 font-mono text-xs font-bold text-sky-300">{o.key}</span>
                    <span className="text-slate-300">{o.label || describe(o.destination)}</span>
                  </li>
                ))}
                {m.dialExtensions && <li className="text-xs text-slate-500">Callers can also dial an extension number.</li>}
              </ul>
            </button>
          ))}
        </div>
      )}
      {edit && <MenuEditor m={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function MenuEditor({ m: initial, onClose }: { m: Menu; onClose: () => void }) {
  const admin = useIsAdmin()
  const [m, setM] = useState(initial)
  const [key, setKey] = useState(initial.options[0]?.key ?? '1')
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: vm } = useVoicemailQuery()
  const [create] = useCreateMenuMutation()
  const [update] = useUpdateMenuMutation()
  const [remove] = useDeleteMenuMutation()
  const [savePrompt, { isLoading }] = useSavePromptMutation()
  const [resetPrompt] = useResetPromptMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const opt = m.options.find((o) => o.key === key)
  const setOpt = (v: Partial<Menu['options'][number]> | null) => setM((m0) => {
    const rest = m0.options.filter((o) => o.key !== key)
    if (v === null) return { ...m0, options: rest }
    const cur = m0.options.find((o) => o.key === key) ?? { key, label: '', destination: { type: 'hangup' as const, target: '' } }
    return { ...m0, options: [...rest, { ...cur, ...v }].sort((a, b) => keypad.indexOf(a.key) - keypad.indexOf(b.key)) }
  })
  const save = async () => {
    const e = await attempt(() => (isNew ? create(m) : update(m)).unwrap(), `Menu ${m.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New menu' : m.name} icon={<Grid3x3 className="text-sky-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Menu removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[1fr_8rem] gap-4">
          <Field label="Name" htmlFor="mn" error={p('name')}><input id="mn" className="input" value={m.name} onChange={(e) => { const v = e.target.value; setM((m0) => ({ ...m0, name: v })) }} /></Field>
          <Field label="Number" htmlFor="mu" error={p('number')} hint="Optional."><input id="mu" className="input font-mono text-sky-300" value={m.number} onChange={(e) => { const v = e.target.value.replace(/\D/g, ''); setM((m0) => ({ ...m0, number: v })) }} /></Field>
        </div>
        {isNew
          ? <p className="text-sm text-slate-500">Save the menu, then record its prompt.</p>
          : <AudioRecording label="Prompt" missing="No prompt recorded: callers hear a beep, then may press a key." has={!!vm?.greetings?.includes(`menu-${m.id}`)}
              src={`./api/menus/${m.id}/prompt`} busy={isLoading} saved="Prompt saved"
              save={(wav) => savePrompt({ id: m.id, wav }).unwrap()} reset={() => resetPrompt(m.id).unwrap()} />}
        <div className="grid grid-cols-[auto_1fr] gap-5">
          <div className="grid grid-cols-3 gap-1.5" role="group" aria-label="Keys">
            {keypad.map((k) => {
              const used = m.options.some((o) => o.key === k)
              return (
                <button type="button" key={k} aria-pressed={key === k} aria-label={`Key ${k}${used ? ', in use' : ''}`} onClick={() => setKey(k)}
                  className={clsx('h-11 w-11 rounded-lg font-mono text-lg font-bold', key === k ? 'ring-2 ring-sky-400' : '',
                    used ? 'bg-sky-500/80 text-slate-950' : 'bg-slate-800 text-slate-400')}>{k}</button>
              )
            })}
          </div>
          <div className="space-y-3">
            <h3 className="text-sm font-semibold text-slate-200">Key <span className="font-mono text-sky-300">{key}</span></h3>
            {opt ? (
              <>
                <Field label="What callers hear it as" htmlFor="ol"><input id="ol" className="input" value={opt.label} placeholder="Sales" onChange={(e) => { const v = e.target.value; setOpt({ label: v }) }} /></Field>
                <DestinationPicker label="Sends callers to" required value={opt.destination} onChange={(d) => d && setOpt({ destination: d })} error={p(`options[${m.options.indexOf(opt)}].destination`)} />
                <button type="button" className="btn" onClick={() => setOpt(null)}>Unused</button>
              </>
            ) : <button type="button" className="btn" onClick={() => setOpt({})}><Plus size={14} className="text-sky-300" aria-hidden /> Use key {key}</button>}
            {p('options') && <p className="text-xs text-rose-300">{p('options')}</p>}
          </div>
        </div>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={m.dialExtensions} onChange={(v) => setM({ ...m, dialExtensions: v })} label="Callers can dial an extension" /> Callers can dial an extension number</label>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Wait for a key (seconds)" htmlFor="mt" error={p('timeoutSeconds')}><input id="mt" type="number" min={1} max={60} className="input" value={m.timeoutSeconds} onChange={(e) => { const v = Number(e.target.value); setM((m0) => ({ ...m0, timeoutSeconds: v })) }} /></Field>
          <Field label="Play the prompt (times)" htmlFor="mr" error={p('repeats')}><input id="mr" type="number" min={1} max={5} className="input" value={m.repeats} onChange={(e) => { const v = Number(e.target.value); setM((m0) => ({ ...m0, repeats: v })) }} /></Field>
        </div>
        <DestinationPicker label="If nobody presses a key" emptyLabel="Say goodbye and hang up" value={m.timeout ?? null} onChange={(d) => setM((m0) => ({ ...m0, timeout: d }))} error={p('timeout')} />
      </fieldset>
    </Drawer>
  )
}
