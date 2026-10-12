import { clsx } from 'clsx'
import { Megaphone, Plus } from 'lucide-react'
import { useState } from 'react'
import {
  useCreatePageGroupMutation, useDeletePageGroupMutation, useExtensionsQuery, usePageGroupsQuery, useUpdatePageGroupMutation,
  type ApiErrorBody, type PageGroup,
} from './api'
import { nextFree, useTakenNumbers } from './numbers'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { Chip, Empty, Field, Page, problemFor } from './ui'

export function Paging() {
  const admin = useIsAdmin()
  const { data } = usePageGroupsQuery()
  const [edit, setEdit] = useState<PageGroup | null>(null)
  const taken = useTakenNumbers()
  const add = () => {
    setEdit({ id: '', number: nextFree(taken, 800), name: 'Everyone', members: [], multicast: '' })
  }
  return (
    <Page icon={Megaphone} title="Paging" accent="from-amber-400 to-rose-400"
      intro="Dial a paging number to speak through every phone in the group at once: the phones answer on their speakers by themselves."
      actions={admin && <button className="btn btn-primary" onClick={add}><Plus size={15} aria-hidden /> Add paging group</button>}>
      {!data?.items.length ? <Empty icon={Megaphone} title="No paging groups yet">For example: every phone in the warehouse on 800.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((g) => (
            <button key={g.id} className="card p-4 text-left hover:border-slate-700" onClick={() => setEdit(g)}>
              <div className="flex items-center gap-2">
                <Megaphone size={16} className="text-amber-300" aria-hidden />
                <span className="font-mono text-amber-300">{g.number}</span>
                <span className="font-semibold text-slate-100">{g.name}</span>
              </div>
              <div className="mt-2 flex flex-wrap gap-1">{g.members.map((m) => <Chip key={m} kind="extension">{m}</Chip>)}{g.multicast && <span className="font-mono text-xs text-orange-300">multicast {g.multicast}</span>}</div>
            </button>
          ))}
        </div>
      )}
      {edit && <PageEditor g={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function PageEditor({ g: initial, onClose }: { g: PageGroup; onClose: () => void }) {
  const admin = useIsAdmin()
  const [g, setG] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: exts } = useExtensionsQuery()
  const [create] = useCreatePageGroupMutation()
  const [update] = useUpdatePageGroupMutation()
  const [remove] = useDeletePageGroupMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(g) : update(g)).unwrap(), `Paging group ${g.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New paging group' : g.name} icon={<Megaphone className="text-amber-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Paging group removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="pn" error={p('number')}><input id="pn" className="input font-mono text-amber-300" value={g.number} onChange={(e) => { const v = e.target.value.replace(/\D/g, ''); setG((g0) => ({ ...g0, number: v })) }} /></Field>
          <Field label="Name" htmlFor="pm" error={p('name')}><input id="pm" className="input" value={g.name} onChange={(e) => { const v = e.target.value; setG((g0) => ({ ...g0, name: v })) }} /></Field>
        </div>
        <Field label="Phones" error={p('members')}>
          <div className="grid gap-1.5 sm:grid-cols-2">
            {exts?.items.map((x) => {
              const on = g.members.includes(x.number)
              return (
                <button type="button" key={x.id} aria-pressed={on} onClick={() => setG((g0) => ({ ...g0, members: on ? g0.members.filter((m) => m !== x.number) : [...g0.members, x.number] }))}
                  className={clsx('flex items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm', on ? 'border-sky-600 bg-sky-950/60 text-slate-100' : 'border-slate-800 text-slate-400 hover:border-slate-700')}>
                  <span className="font-mono text-sky-300">{x.number}</span> <span className="truncate">{x.name}</span>
                </button>
              )
            })}
          </div>
        </Field>
        <p className="text-xs text-slate-500">Most desk phones answer pages by themselves. Some need auto-answer (intercom) allowed in their own settings.</p>
        <Field label="Also send to a multicast group" htmlFor="pmc" error={p('multicast')}
          hint="Optional. Overhead speakers and phones listening to this address and port hear the page without a call each (G.711 µ-law). Example: 239.0.1.10:5000.">
          <input id="pmc" className="input font-mono" placeholder="239.0.1.10:5000" value={g.multicast ?? ''} onChange={(e) => { const v = e.target.value.trim(); setG((g0) => ({ ...g0, multicast: v })) }} />
        </Field>
      </fieldset>
    </Drawer>
  )
}
