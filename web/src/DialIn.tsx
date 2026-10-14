import { clsx } from 'clsx'
import { BookA, KeyRound, Plus, ShieldAlert } from 'lucide-react'
import { useState } from 'react'
import {
  useCreateDialInMutation, useCreateDirectoryMutation, useDeleteDialInMutation, useDeleteDirectoryMutation, useDialInsQuery,
  useDirectoriesQuery, useExtensionsQuery, usePromptsQuery, useUpdateDialInMutation, useUpdateDirectoryMutation,
  type ApiErrorBody, type DialIn, type Directory,
} from './api'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { nextFree, useTakenNumbers } from './numbers'
import { Chip, Empty, Field, ListInput, Page, problemFor } from './ui'

const matches = [
  ['last', 'Last name', 'Callers spell the last name: S-M-I for Ann Smith.'],
  ['first', 'First name', 'Callers spell the first name: A-N-N for Ann Smith.'],
  ['any', 'Either', 'Any word of the name matches.'],
] as const

function LanguageField({ value, onChange, id, error }: { value: string; onChange: (v: string) => void; id: string; error?: string }) {
  const { data: langs } = usePromptsQuery()
  return (
    <Field label="Language" htmlFor={id} error={error} hint="The prompts callers hear.">
      <select id={id} className="input" value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">The system's</option>
        {langs?.languages.map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
      </select>
    </Field>
  )
}

/** Dial-by-name directories: callers spell a name on the keypad and are put through. */
export function Directories() {
  const admin = useIsAdmin()
  const { data } = useDirectoriesQuery()
  const [edit, setEdit] = useState<Directory | null>(null)
  const taken = useTakenNumbers()
  return (
    <Page icon={BookA} title="Dial-by-name directory" accent="from-sky-400 to-emerald-400"
      intro="Callers spell the first letters of a name on the keypad (2 for ABC, 3 for DEF…), hear who matches and press a key to be put through. Use one as a menu option, or give it a number."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', number: nextFree(taken, 411), name: 'Staff directory', match: 'last', members: [] })}><Plus size={15} aria-hidden /> Add directory</button>}>
      {!data?.items.length ? <Empty icon={BookA} title="No directories yet">For example: every member of staff on 411, spelled by last name.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((d) => (
            <button key={d.id} className="card p-4 text-left hover:border-slate-700" onClick={() => setEdit(d)}>
              <div className="flex items-center gap-2">
                <BookA size={16} className="text-sky-300" aria-hidden />
                {d.number && <span className="font-mono text-sky-300">{d.number}</span>}
                <span className="font-semibold text-slate-100">{d.name}</span>
                <span className="ml-auto text-xs text-emerald-300">{matches.find((m) => m[0] === d.match)?.[1]}</span>
              </div>
              <p className="mt-1 text-sm text-slate-400">{d.members?.length ? `${d.members.length} people` : 'Everyone with a phone'}</p>
            </button>
          ))}
        </div>
      )}
      <p className="mt-4 text-sm text-slate-400">Each person's own recorded name is said when they record one (Extensions, Spoken name); otherwise their number is read out.</p>
      {edit && <DirectoryEditor d={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function DirectoryEditor({ d: initial, onClose }: { d: Directory; onClose: () => void }) {
  const admin = useIsAdmin()
  const [d, setD] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: exts } = useExtensionsQuery()
  const [create] = useCreateDirectoryMutation()
  const [update] = useUpdateDirectoryMutation()
  const [remove] = useDeleteDirectoryMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const members = d.members ?? []
  const save = async () => {
    const e = await attempt(() => (isNew ? create(d) : update(d)).unwrap(), `Directory ${d.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New directory' : d.name} icon={<BookA className="text-sky-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Directory removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="dn" error={p('number')} hint="Optional."><input id="dn" className="input font-mono text-sky-300" value={d.number} onChange={(e) => setD({ ...d, number: e.target.value.replace(/\D/g, '') })} /></Field>
          <Field label="Name" htmlFor="dm" error={p('name')}><input id="dm" className="input" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} /></Field>
        </div>
        <div>
          <span className="label">Callers spell</span>
          <div className="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Callers spell">
            {matches.map(([id, t, h]) => (
              <button type="button" key={id} role="radio" aria-checked={d.match === id} onClick={() => setD({ ...d, match: id })}
                className={clsx('card p-3 text-left', d.match === id && 'ring-2 ring-sky-400')}>
                <div className="text-sm font-semibold text-sky-300">{t}</div><div className="text-xs text-slate-500">{h}</div>
              </button>
            ))}
          </div>
        </div>
        <LanguageField id="dla" value={d.language ?? ''} onChange={(v) => setD({ ...d, language: v })} error={p('language')} />
        <Field label="Who is listed" error={p('members')} hint="Choose nobody to list everyone with a phone.">
          <div className="grid gap-1.5 sm:grid-cols-2">
            {exts?.items.filter((x) => !x.kind || x.kind === 'analog').map((x) => {
              const on = members.includes(x.number)
              return (
                <button type="button" key={x.id} aria-pressed={on} onClick={() => setD({ ...d, members: on ? members.filter((m) => m !== x.number) : [...members, x.number] })}
                  className={clsx('flex items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm', on ? 'border-sky-600 bg-sky-950/60 text-slate-100' : 'border-slate-800 text-slate-400 hover:border-slate-700')}>
                  <span className="font-mono text-sky-300">{x.number}</span> <span className="truncate">{x.name}</span>
                </button>
              )
            })}
          </div>
        </Field>
      </fieldset>
    </Drawer>
  )
}

/** Dial-in (DISA): call in from outside, prove who you are, call out as the company. */
export function DialIns() {
  const admin = useIsAdmin()
  const { data } = useDialInsQuery()
  const [edit, setEdit] = useState<DialIn | null>(null)
  const taken = useTakenNumbers()
  const { data: exts } = useExtensionsQuery()
  return (
    <Page icon={KeyRound} title="Dial-in" accent="from-amber-400 to-rose-400"
      intro="Call in from a mobile or another phone, prove who you are with a PIN or your number, and call out again through the office: with its caller ID and its rates. Route a phone number to it on Call routing, or give it an inside number."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', number: nextFree(taken, 450), name: 'Staff dial-in', pin: '', allowedCallers: [], as: exts?.items.find((x) => x.outbound)?.number ?? '' })}><Plus size={15} aria-hidden /> Add dial-in</button>}>
      <div className="mb-4 flex items-start gap-2 rounded-md border border-amber-900/60 bg-amber-950/30 p-3 text-sm text-amber-200">
        <ShieldAlert size={16} className="mt-0.5 shrink-0" aria-hidden />
        <span>Anyone who gets through can call anywhere the chosen extension can, at your cost. Use a long PIN, list the callers' numbers where you can, and keep the extension's outside calls to what is needed. Three wrong PINs end the call, and every attempt is logged in Security.</span>
      </div>
      {!data?.items.length ? <Empty icon={KeyRound} title="No dial-ins yet">For example: staff on the road call 450, enter their PIN and dial customers as the office.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((d) => (
            <button key={d.id} className="card p-4 text-left hover:border-slate-700" onClick={() => setEdit(d)}>
              <div className="flex items-center gap-2">
                <KeyRound size={16} className="text-amber-300" aria-hidden />
                {d.number && <span className="font-mono text-amber-300">{d.number}</span>}
                <span className="font-semibold text-slate-100">{d.name}</span>
              </div>
              <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
                {d.pin && <span className="text-emerald-300">PIN</span>}
                {(d.allowedCallers?.length ?? 0) > 0 && <span className="text-emerald-300">{d.allowedCallers!.length} allowed numbers</span>}
                <span className="text-slate-400">calls as</span> <Chip kind="extension">{d.as}</Chip>
              </div>
            </button>
          ))}
        </div>
      )}
      {edit && <DialInEditor d={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function DialInEditor({ d: initial, onClose }: { d: DialIn; onClose: () => void }) {
  const admin = useIsAdmin()
  const [d, setD] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: exts } = useExtensionsQuery()
  const [create] = useCreateDialInMutation()
  const [update] = useUpdateDialInMutation()
  const [remove] = useDeleteDialInMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(d) : update(d)).unwrap(), `Dial-in ${d.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New dial-in' : d.name} icon={<KeyRound className="text-amber-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Dial-in removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="in" error={p('number')} hint="Optional."><input id="in" className="input font-mono text-amber-300" value={d.number} onChange={(e) => setD({ ...d, number: e.target.value.replace(/\D/g, '') })} /></Field>
          <Field label="Name" htmlFor="im" error={p('name')}><input id="im" className="input" value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} /></Field>
        </div>
        <Field label="PIN" htmlFor="ip" error={p('pin')} hint="4 to 10 digits; longer is safer.">
          <input id="ip" className="input w-48 font-mono" inputMode="numeric" value={d.pin} onChange={(e) => setD({ ...d, pin: e.target.value === '••••' ? e.target.value : e.target.value.replace(/\D/g, '') })} />
        </Field>
        <Field label="Only from these numbers" htmlFor="ia" error={p('allowedCallers')} hint="Optional: the callers' own numbers; press Enter after each. Caller ID can be faked, so keep a PIN as well.">
          <ListInput id="ia" mono value={d.allowedCallers ?? []} onChange={(v) => setD({ ...d, allowedCallers: v })} placeholder="+12125550199" />
        </Field>
        <Field label="Calls are made as" htmlFor="ias" error={p('as')} hint="Its outside-call permission, caller ID and routes apply.">
          <select id="ias" className="input" value={d.as} onChange={(e) => setD({ ...d, as: e.target.value })}>
            <option value="">Choose…</option>
            {exts?.items.map((x) => <option key={x.id} value={x.number} disabled={!x.outbound}>{x.number} {x.name}{x.outbound ? '' : ' (no outside calls)'}</option>)}
          </select>
        </Field>
        <LanguageField id="ila" value={d.language ?? ''} onChange={(v) => setD({ ...d, language: v })} error={p('language')} />
      </fieldset>
    </Drawer>
  )
}
