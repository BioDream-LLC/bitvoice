import { createColumnHelper } from '@tanstack/react-table'
import { Bot, Plus } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import {
  useCreateReceptionistMutation, useDeleteReceptionistMutation, useExtensionsQuery, useReceptionistsQuery, useSettingsQuery,
  useUpdateReceptionistMutation, type ApiErrorBody, type Receptionist,
} from './api'
import { attempt, DeleteButton, Drawer, Table, useIsAdmin } from './Editor'
import { DestinationPicker, useDescribe } from './Extensions'
import { useOpenParam } from './useOpenParam'
import { Empty, ErrorBox, Field, Page, problemFor } from './ui'

const blank: Receptionist = { id: '', name: 'Receptionist', greeting: '', knowledge: '', person: { type: 'extension', target: '' }, messageBox: '' }
const col = createColumnHelper<Receptionist>()

/** AI receptionists: answer when nobody can, from approved information, and always hand over to a person. */
export function Receptionists() {
  const admin = useIsAdmin()
  const describe = useDescribe()
  const { data } = useReceptionistsQuery()
  const { data: settings } = useSettingsQuery()
  const [edit, setEdit] = useState<Receptionist | null>(null)
  useOpenParam(data?.items, setEdit)
  const ai = settings?.system.ai
  const ready = ai?.enabled && ai.speechModel
  const columns = [
    col.accessor('name', { header: 'Name', cell: (c) => <span className="inline-flex items-center gap-2 font-medium text-slate-100"><Bot size={14} className="text-violet-300" aria-hidden />{c.getValue()}</span> }),
    col.accessor('person', { header: 'A person is', cell: (c) => <span className="text-sm text-sky-300">{describe(c.getValue())}</span> }),
    col.accessor('messageBox', { header: 'Messages to', cell: (c) => <span className="font-mono text-xs text-violet-300">{c.getValue() || 'nobody'}</span> }),
  ]
  return (
    <Page icon={Bot} title="AI receptionists" accent="from-violet-300 to-sky-300"
      intro="Answers when nobody can: says it is automated, answers questions from the information you approve, takes messages and transfers to a person. Pressing 0 or asking for a person always reaches someone, and so does any problem with the AI service."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ ...blank })}><Plus size={15} aria-hidden /> Add receptionist</button>}>
      {!ready && <div className="mb-4"><ErrorBox message="Switch on AI and set a speech model in Settings: until then, calls to a receptionist go straight to its person." /></div>}
      <Table data={data?.items ?? []} columns={columns} onRow={(r) => admin && setEdit(r)}
        empty={<Empty icon={Bot} title="No receptionists yet">Add one, then choose it as where calls go (Call routing, a menu or an extension's no-answer).</Empty>} />
      {edit && <ReceptionistEditor r={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function ReceptionistEditor({ r, onClose }: { r: Receptionist; onClose: () => void }) {
  const [create, { isLoading: a }] = useCreateReceptionistMutation()
  const [update, { isLoading: u }] = useUpdateReceptionistMutation()
  const [remove] = useDeleteReceptionistMutation()
  const { data: exts } = useExtensionsQuery()
  const [x, setX] = useState(r)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const isNew = !r.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const res = await attempt(() => (isNew ? create(x) : update(x)).unwrap(), isNew ? 'Receptionist added' : 'Receptionist saved')
    if (res) setErr(res)
    else onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New receptionist' : x.name} icon={<Bot size={18} className="text-violet-300" aria-hidden />} onSave={save} saving={a || u} error={err}
      footer={!isNew && <DeleteButton what={r.name} onDelete={async () => { if (!await attempt(() => remove(r.id).unwrap(), 'Receptionist removed', { problemsInToast: true })) onClose() }} />}>
      <Field label="Name" htmlFor="rn" error={p('name')}><input id="rn" className="input" autoFocus value={x.name} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, name: v })) }} /></Field>
      <Field label="Greeting" htmlFor="rg" hint="Empty: it gives the system's name, says it is automated, and that saying person or pressing 0 reaches someone.">
        <textarea id="rg" rows={2} className="input" value={x.greeting} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, greeting: v })) }} />
      </Field>
      <Field label="Approved information" htmlFor="rk" error={p('knowledge')} hint="Opening hours, address, parking, services, prices: the only things it may say. It never invents anything else.">
        <textarea id="rk" rows={8} className="input" value={x.knowledge} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, knowledge: v })) }} placeholder={'Open Monday to Friday, 8 am to 5 pm.\nWe are at 12 Main Street, parking behind the building.'} />
      </Field>
      <DestinationPicker label="A person is" required value={x.person?.type ? x.person : null} noSchedules
        onChange={(d) => d && setX((x0) => ({ ...x0, person: d }))} error={p('person')} />
      <Field label="Take messages for" htmlFor="rm" error={p('messageBox')} hint="Its owner is also emailed each callback this receptionist books.">
        <select id="rm" className="input" value={x.messageBox} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, messageBox: v })) }}>
          <option value="">Nobody: offer a person instead</option>
          {exts?.items.filter((e) => e.voicemail?.enabled).map((e) => <option key={e.id} value={e.number}>Voicemail of {e.number} {e.name}</option>)}
        </select>
      </Field>
      <p className="text-xs text-slate-500">Calls go to the AI service in <Link to="/settings" className="text-sky-300 underline">Settings</Link>: what callers say is sent there to be understood and answered.</p>
    </Drawer>
  )
}
