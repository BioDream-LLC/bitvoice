import { createColumnHelper } from '@tanstack/react-table'
import { Building, MapPin, Plus } from 'lucide-react'
import { useState } from 'react'
import { useCreateSiteMutation, useDeleteSiteMutation, useExtensionsQuery, useSitesQuery, useUpdateSiteMutation, type ApiErrorBody, type Site } from './api'
import { attempt, DeleteButton, Drawer, Table, useIsAdmin } from './Editor'
import { useOpenParam } from './useOpenParam'
import { Empty, Field, ListInput, Page, problemFor } from './ui'

const blank: Site = { id: '', name: '', country: 'US', state: '', county: '', city: '', street: '', number: '', postalCode: '', networks: [] }
const col = createColumnHelper<Site & { phones: number }>()

export const siteLine = (s: Site) => [`${s.number} ${s.street}`.trim(), s.city, `${s.state} ${s.postalCode}`.trim()].filter(Boolean).join(', ')

/** Sites: the buildings phones are in, with the civic address emergency calls carry. */
export function Sites() {
  const admin = useIsAdmin()
  const { data } = useSitesQuery()
  const { data: exts } = useExtensionsQuery()
  const [edit, setEdit] = useState<Site | null>(null)
  useOpenParam(data?.items, setEdit)
  const rows = (data?.items ?? []).map((s) => ({ ...s, phones: exts?.items.filter((x) => x.site === s.id).length ?? 0 }))
  const columns = [
    col.accessor('name', { header: 'Site', cell: (c) => <span className="inline-flex items-center gap-2 font-medium text-slate-100"><Building size={14} className="text-amber-300" aria-hidden />{c.getValue()}</span> }),
    col.display({ id: 'address', header: 'Address', cell: (c) => <span className="text-sm text-emerald-300">{siteLine(c.row.original)}</span> }),
    col.accessor('networks', { header: 'Networks', cell: (c) => <span className="font-mono text-xs text-sky-300">{c.getValue().join(', ') || 'any'}</span> }),
    col.accessor('phones', { header: 'Extensions', cell: (c) => <span className="text-sm text-slate-300">{c.getValue()}</span> }),
  ]
  return (
    <Page icon={MapPin} title="Sites" accent="from-amber-300 to-emerald-300"
      intro="Your buildings and offices, with the street address emergency calls carry. A phone registering from outside its site's networks is treated as a remote worker, who is asked to confirm where they are."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ ...blank })}><Plus size={15} aria-hidden /> Add site</button>}>
      <Table data={rows} columns={columns} onRow={(r) => admin && setEdit(data!.items.find((s) => s.id === r.id)!)}
        empty={<Empty icon={MapPin} title="No sites yet">Add your building so 911 calls carry its address.</Empty>} />
      {edit && <SiteEditor site={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function SiteEditor({ site, onClose }: { site: Site; onClose: () => void }) {
  const [create, { isLoading: a }] = useCreateSiteMutation()
  const [update, { isLoading: u }] = useUpdateSiteMutation()
  const [remove] = useDeleteSiteMutation()
  const [s, setS] = useState(site)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const isNew = !site.id
  const p = (f: string) => problemFor(err?.problems, f)
  const set = (k: keyof Site) => (e: React.ChangeEvent<HTMLInputElement>) => { const v = e.target.value; setS((s0) => ({ ...s0, [k]: v })) }
  const save = async () => {
    const r = await attempt(() => (isNew ? create(s) : update(s)).unwrap(), isNew ? 'Site added' : 'Site saved')
    if (r) setErr(r)
    else onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New site' : s.name} icon={<Building size={18} className="text-amber-300" aria-hidden />} onSave={save} saving={a || u} error={err}
      footer={!isNew && <DeleteButton what={site.name} onDelete={async () => { if (!await attempt(() => remove(site.id).unwrap(), 'Site removed', { problemsInToast: true })) onClose() }} />}>
      <Field label="Name" htmlFor="sn" error={p('name')}><input id="sn" className="input" autoFocus value={s.name} onChange={set('name')} placeholder="Head office" /></Field>
      <div className="grid grid-cols-[6rem_1fr] gap-3">
        <Field label="Number" htmlFor="sh" error={p('number')}><input id="sh" className="input" value={s.number} onChange={set('number')} placeholder="12" /></Field>
        <Field label="Street" htmlFor="sr" error={p('street')}><input id="sr" className="input" value={s.street} onChange={set('street')} placeholder="Main Street" /></Field>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="City" htmlFor="sc" error={p('city')}><input id="sc" className="input" value={s.city} onChange={set('city')} /></Field>
        <Field label="State or province" htmlFor="sst"><input id="sst" className="input" value={s.state} onChange={set('state')} placeholder="NY" /></Field>
        <Field label="Postal code" htmlFor="spc"><input id="spc" className="input" value={s.postalCode} onChange={set('postalCode')} /></Field>
        <Field label="Country" htmlFor="sco" error={p('country')} hint="Two letters, such as US."><input id="sco" className="input uppercase" maxLength={2} value={s.country} onChange={set('country')} /></Field>
      </div>
      <Field label="Networks" htmlFor="snw" error={p('networks')} hint="The addresses phones at this site use, such as 10.1.0.0/16. Empty: every phone of the site counts as there.">
        <ListInput id="snw" mono value={s.networks} onChange={(v) => setS((s0) => ({ ...s0, networks: v }))} placeholder="10.1.0.0/16" />
      </Field>
    </Drawer>
  )
}
