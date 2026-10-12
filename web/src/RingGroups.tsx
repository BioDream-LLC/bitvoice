import { createColumnHelper } from '@tanstack/react-table'
import { ArrowDown, ArrowUp, ListOrdered, Plus, Radio, Users } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  useCreateRingGroupMutation, useDeleteRingGroupMutation, useExtensionsQuery, useRingGroupsQuery,
  useUpdateRingGroupMutation, type ApiErrorBody, type RingGroup,
} from './api'
import { useOpenParam } from './useOpenParam'
import { nextFree, useTakenNumbers } from './numbers'
import { attempt, DeleteButton, Drawer, Table, useIsAdmin } from './Editor'
import { DestinationPicker } from './Extensions'
import { Chip, Empty, Field, Page, problemFor } from './ui'
import { clsx } from 'clsx'

const blank: RingGroup = { id: '', number: '', name: '', strategy: 'all', members: [], ringSeconds: 25, noAnswer: null }
const col = createColumnHelper<RingGroup>()

export function RingGroups() {
  const admin = useIsAdmin()
  const { data } = useRingGroupsQuery()
  const [edit, setEdit] = useState<RingGroup | null>(null)
  useOpenParam(data?.items, setEdit)
  const [params, setParams] = useSearchParams()
  const taken = useTakenNumbers()
  const add = () => {
    setEdit({ ...blank, number: nextFree(taken, 600) })
  }
  useEffect(() => { if (params.get('new') && admin) { add(); setParams({}, { replace: true }) } }, [params, admin]) // eslint-disable-line react-hooks/exhaustive-deps

  const columns = [
    col.accessor('number', { header: 'Number', cell: (c) => <Chip kind="ringgroup">{c.getValue()}</Chip> }),
    col.accessor('name', { header: 'Name', cell: (c) => <span className="font-medium text-slate-100">{c.getValue()}</span> }),
    col.accessor('strategy', { header: 'Rings', cell: (c) => c.getValue() === 'all'
      ? <span className="inline-flex items-center gap-1 text-xs text-emerald-300"><Radio size={12} aria-hidden /> Everyone at once</span>
      : <span className="inline-flex items-center gap-1 text-xs text-amber-300"><ListOrdered size={12} aria-hidden /> One after another</span> }),
    col.accessor('members', { header: 'Members', cell: (c) => <div className="flex flex-wrap gap-1">{c.getValue().map((m) => <Chip key={m} kind="extension">{m}</Chip>)}</div> }),
  ]
  return (
    <Page icon={Users} title="Ring groups" accent="from-violet-400 to-fuchsia-400"
      intro="One number that rings several people: sales, support, the front desk."
      actions={admin && <button className="btn btn-primary" onClick={add}><Plus size={15} aria-hidden /> Add ring group</button>}>
      <Table data={data?.items ?? []} columns={columns} onRow={setEdit}
        empty={<Empty icon={Users} title="No ring groups yet">A ring group rings several extensions from one number.</Empty>} />
      {edit && <Editor g={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function Editor({ g: initial, onClose }: { g: RingGroup; onClose: () => void }) {
  const admin = useIsAdmin()
  const [g, setG] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: exts } = useExtensionsQuery()
  const [create] = useCreateRingGroupMutation()
  const [update] = useUpdateRingGroupMutation()
  const [remove] = useDeleteRingGroupMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const toggle = (n: string) => setG((g0) => ({ ...g0, members: g0.members.includes(n) ? g0.members.filter((m) => m !== n) : [...g0.members, n] }))
  const move = (i: number, by: number) => setG((g0) => {
    const m = [...g0.members]
    const j = i + by
    if (j < 0 || j >= m.length) return g0
    ;[m[i], m[j]] = [m[j], m[i]]
    return { ...g0, members: m }
  })
  const nameOf = (n: string) => exts?.items.find((x) => x.number === n)?.name ?? ''
  const save = async () => {
    const e = await attempt(() => (isNew ? create(g) : update(g)).unwrap(), `Ring group ${g.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New ring group' : g.name} icon={<Users className="text-violet-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Ring group removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="gn" error={p('number')}><input id="gn" className="input font-mono text-violet-300" value={g.number} onChange={(e) => { const v = e.target.value.replace(/\D/g, ''); setG((g0) => ({ ...g0, number: v })) }} /></Field>
          <Field label="Name" htmlFor="gm" error={p('name')}><input id="gm" className="input" autoFocus={isNew} value={g.name} onChange={(e) => { const v = e.target.value; setG((g0) => ({ ...g0, name: v })) }} /></Field>
        </div>
        <div>
          <span className="label">How it rings</span>
          <div className="grid grid-cols-2 gap-2">
            {([['all', 'Everyone at once', 'First to answer gets the call', Radio, 'text-emerald-300'],
              ['sequential', 'One after another', 'In the order below', ListOrdered, 'text-amber-300']] as const).map(([id, t, d, Icon, c]) => (
              <button type="button" key={id} aria-pressed={g.strategy === id} onClick={() => setG((g0) => ({ ...g0, strategy: id }))}
                className={clsx('card p-3 text-left', g.strategy === id && 'ring-2 ring-violet-400')}>
                <Icon size={18} className={c} aria-hidden />
                <div className="mt-1 text-sm font-semibold text-slate-100">{t}</div>
                <div className="text-xs text-slate-500">{d}</div>
              </button>
            ))}
          </div>
        </div>
        <Field label="Members" error={p('members')}>
          <div className="grid gap-1.5 sm:grid-cols-2">
            {exts?.items.map((x) => {
              const on = g.members.includes(x.number)
              return (
                <button type="button" key={x.id} aria-pressed={on} onClick={() => toggle(x.number)}
                  className={clsx('flex items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm',
                    on ? 'border-sky-600 bg-sky-950/60 text-slate-100' : 'border-slate-800 text-slate-400 hover:border-slate-700')}>
                  <span className="font-mono text-sky-300">{x.number}</span> <span className="truncate">{x.name}</span>
                  {on && g.strategy === 'sequential' && <span className="ml-auto text-xs text-amber-300">#{g.members.indexOf(x.number) + 1}</span>}
                </button>
              )
            })}
          </div>
        </Field>
        {g.strategy === 'sequential' && g.members.length > 1 && (
          <div>
            <span className="label">Ringing order</span>
            <ol className="space-y-1">
              {g.members.map((m, i) => (
                <li key={m} className="flex items-center gap-2 rounded-lg border border-slate-800 px-3 py-1.5 text-sm">
                  <span className="w-6 text-xs text-amber-300">#{i + 1}</span>
                  <span className="font-mono text-sky-300">{m}</span> <span className="truncate text-slate-300">{nameOf(m)}</span>
                  <span className="ml-auto flex gap-1">
                    <button type="button" className="btn px-2 py-1" disabled={i === 0} onClick={() => move(i, -1)} aria-label={`Ring ${m} earlier`}><ArrowUp size={14} className="text-sky-300" aria-hidden /></button>
                    <button type="button" className="btn px-2 py-1" disabled={i === g.members.length - 1} onClick={() => move(i, 1)} aria-label={`Ring ${m} later`}><ArrowDown size={14} className="text-sky-300" aria-hidden /></button>
                  </span>
                </li>
              ))}
            </ol>
          </div>
        )}
        <Field label="Ring for (seconds)" htmlFor="gr" error={p('ringSeconds')} hint={g.strategy === 'sequential' ? 'For each member in turn.' : undefined}>
          <input id="gr" type="number" min={5} max={300} className="input w-32" value={g.ringSeconds} onChange={(e) => { const v = Number(e.target.value); setG((g0) => ({ ...g0, ringSeconds: v })) }} />
        </Field>
        <DestinationPicker label="If nobody answers" value={g.noAnswer ?? null} onChange={(d) => setG((g0) => ({ ...g0, noAnswer: d }))} exclude={g.number} error={p('noAnswer')} />
      </fieldset>
    </Drawer>
  )
}
