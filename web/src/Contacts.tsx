import { createColumnHelper } from '@tanstack/react-table'
import { BookUser, Download, Phone, Plus, Trash2, Upload } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import { useAddContactMutation, useDeleteContactMutation, useDirectoryQuery, useSaveContactMutation, type ApiErrorBody, type DirContact, type DirNumber } from './api'
import { attempt, DeleteButton, Drawer, Table, useIsAdmin } from './Editor'
import { useOpenParam } from './useOpenParam'
import { TimelineLink } from './Timeline'
import { Empty, Field, Page, problemFor } from './ui'

const blank: DirContact = { id: '', name: '', company: '', numbers: [{ kind: 'work', number: '' }], email: '', notes: '' }
const kindColour: Record<DirNumber['kind'], string> = { work: 'text-sky-300', mobile: 'text-emerald-300', home: 'text-amber-300', other: 'text-slate-300' }
const col = createColumnHelper<DirContact>()

/** Contacts: the company directory every desk phone and the browser phone show, beside the extensions. */
export function Contacts() {
  const admin = useIsAdmin()
  const { data } = useDirectoryQuery()
  const [edit, setEdit] = useState<DirContact | null>(null)
  const [q, setQ] = useState('')
  useOpenParam(data?.items, setEdit)
  const rows = useMemo(() => {
    const w = q.trim().toLowerCase()
    const digits = w.replace(/\D/g, '')
    return (data?.items ?? []).filter((c) => !w || `${c.name} ${c.company ?? ''} ${c.email ?? ''}`.toLowerCase().includes(w) || (digits.length > 2 && c.numbers.some((n) => n.number.replace(/\D/g, '').includes(digits))))
  }, [data, q])
  const columns = [
    col.accessor('name', { header: 'Name', cell: (c) => <span className="inline-flex items-center gap-2 font-medium text-slate-100"><BookUser size={14} className="text-teal-300" aria-hidden />{c.getValue()}</span> }),
    col.accessor('company', { header: 'Company', cell: (c) => <span className="text-sm text-slate-300">{c.getValue()}</span> }),
    col.accessor('numbers', { header: 'Numbers', enableSorting: false, cell: (c) => (
      <span className="flex flex-wrap gap-x-3 font-mono text-xs">{c.getValue().map((n, i) => <span key={i} className={kindColour[n.kind]}>{n.number} <span className="font-sans text-slate-500">{n.kind}</span></span>)}</span>
    ) }),
    col.display({ id: 'timeline', header: () => <span className="sr-only">Timeline</span>, cell: (c) => <span onClick={(e) => e.stopPropagation()}><TimelineLink number={c.row.original.numbers[0]?.number ?? ''} /></span> }),
  ]
  return (
    <Page icon={BookUser} title="Contacts" accent="from-teal-300 to-sky-300"
      intro="The company directory: suppliers, clients, other offices. Every provisioned desk phone and the browser phone list these beside the extensions, and an incoming call from one of these numbers shows the contact's name."
      actions={<div className="flex flex-wrap gap-2">
        <a className="btn" href="./api/directory.csv" download><Download size={15} className="text-sky-300" aria-hidden /> Export</a>
        {admin && <ImportButton />}
        {admin && <button className="btn btn-primary" onClick={() => setEdit({ ...blank, numbers: [{ kind: 'work', number: '' }] })}><Plus size={15} aria-hidden /> Add contact</button>}
      </div>}>
      <input type="search" className="input mb-4 max-w-sm" aria-label="Find a contact" placeholder="Find by name, company or number" value={q} onChange={(e) => setQ(e.target.value)} />
      <Table data={rows} columns={columns} onRow={(r) => admin && setEdit(r)}
        empty={<Empty icon={BookUser} title={q ? 'Nobody matches' : 'No contacts yet'}>{q ? 'Try part of the name or number.' : 'Add the people you call most, or import a spreadsheet (CSV) with name and phone columns.'}</Empty>} />
      {edit && <ContactEditor contact={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

/** ImportButton reads a CSV, says what it would do, then imports it (adding, or replacing every contact). */
function ImportButton() {
  const file = useRef<HTMLInputElement>(null)
  const [pending, setPending] = useState<{ text: string; count: number; name: string } | null>(null)
  const [replace, setReplace] = useState(false)
  const [problems, setProblems] = useState<ApiErrorBody | null>(null)
  const send = async (text: string, q: string) => {
    const r = await fetch(`./api/directory/import${q}`, { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'text/csv' }, body: text })
    return { ok: r.ok, body: await r.json().catch(() => ({ message: 'BitVoice did not answer' })) }
  }
  const pick = async (f: File) => {
    const text = await f.text()
    const r = await send(text, '?check=1')
    if (!r.ok) { setProblems(r.body); setPending({ text: '', count: 0, name: f.name }); return }
    setProblems(null)
    setPending({ text, count: r.body.contacts, name: f.name })
  }
  const run = async () => {
    if (!pending) return
    const r = await send(pending.text, replace ? '?replace=1' : '')
    if (r.ok) { toast.success(`Contacts imported: ${r.body.added} added, ${r.body.updated} updated`); setPending(null) }
    else setProblems(r.body)
  }
  return (
    <>
      <button className="btn" onClick={() => file.current?.click()}><Upload size={15} className="text-emerald-300" aria-hidden /> Import</button>
      <input ref={file} type="file" accept=".csv,text/csv" className="hidden" aria-label="Contacts file (CSV)" onChange={(e) => { const f = e.target.files?.[0]; if (f) pick(f); e.target.value = '' }} />
      {pending && (
        <Drawer open onClose={() => setPending(null)} title={`Import ${pending.name}`} icon={<Upload size={18} className="text-emerald-300" aria-hidden />}
          onSave={problems ? undefined : run} saveLabel="Import">
          {problems ? (
            <div role="alert" className="space-y-2 text-sm">
              <p className="text-rose-300">{problems.message || 'The file has problems; nothing was imported.'}</p>
              <ul className="list-disc space-y-1 pl-5 text-slate-300">{problems.problems?.map((p, i) => <li key={i}><span className="text-amber-300">{p.field}</span>: {p.message}</li>)}</ul>
              <p className="text-slate-400">The first row names the columns: name, company, work (or phone), mobile, home, other, email, notes.</p>
            </div>
          ) : (
            <div className="space-y-4 text-sm">
              <p className="text-slate-200">{pending.count} contacts are ready. A contact with the same name as one there is updated, not added twice.</p>
              <label className="flex items-center gap-2 text-slate-300"><input type="checkbox" checked={replace} onChange={(e) => setReplace(e.target.checked)} />
                <span>Replace every contact there is now <span className="text-rose-300">(the others are removed)</span></span></label>
            </div>
          )}
        </Drawer>
      )}
    </>
  )
}

function ContactEditor({ contact, onClose }: { contact: DirContact; onClose: () => void }) {
  const [add, { isLoading: a }] = useAddContactMutation()
  const [save, { isLoading: u }] = useSaveContactMutation()
  const [remove] = useDeleteContactMutation()
  const [c, setC] = useState(contact)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const isNew = !contact.id
  const p = (f: string) => problemFor(err?.problems, f)
  const setNum = (i: number, v: Partial<DirNumber>) => setC((c0) => ({ ...c0, numbers: c0.numbers.map((n, j) => (j === i ? { ...n, ...v } : n)) }))
  const doSave = async () => {
    const r = await attempt(() => (isNew ? add(c) : save(c)).unwrap(), isNew ? 'Contact added' : 'Contact saved')
    if (r) setErr(r)
    else onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New contact' : contact.name} icon={<BookUser size={18} className="text-teal-300" aria-hidden />} onSave={doSave} saving={a || u} error={err}
      footer={!isNew && <DeleteButton what={contact.name} onDelete={async () => { if (!await attempt(() => remove(contact.id).unwrap(), 'Contact removed', { problemsInToast: true })) onClose() }} />}>
      <Field label="Name" htmlFor="dn" error={p('name')}><input id="dn" className="input" autoFocus value={c.name} onChange={(e) => setC({ ...c, name: e.target.value })} placeholder="Acme Plumbing" /></Field>
      <Field label="Company" htmlFor="dc"><input id="dc" className="input" value={c.company ?? ''} onChange={(e) => setC({ ...c, company: e.target.value })} /></Field>
      <fieldset className="space-y-2">
        <legend className="mb-1 flex items-center gap-1.5 text-sm font-medium text-slate-300"><Phone size={14} className="text-emerald-300" aria-hidden /> Numbers</legend>
        {c.numbers.map((n, i) => (
          <div key={i} className="flex items-start gap-2">
            <select aria-label={`Number ${i + 1}: kind`} className="input w-28" value={n.kind} onChange={(e) => setNum(i, { kind: e.target.value as DirNumber['kind'] })}>
              <option value="work">Work</option><option value="mobile">Mobile</option><option value="home">Home</option><option value="other">Other</option>
            </select>
            <div className="flex-1">
              <input aria-label={`Number ${i + 1}`} className="input font-mono" inputMode="tel" value={n.number} onChange={(e) => setNum(i, { number: e.target.value })} placeholder="+12125550100" />
              {p(`numbers[${i}].number`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`numbers[${i}].number`)}</p>}
            </div>
            {c.numbers.length > 1 && <button type="button" className="btn-ghost p-2" aria-label={`Remove number ${i + 1}`} onClick={() => setC({ ...c, numbers: c.numbers.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>}
          </div>
        ))}
        {err?.problems?.find((x) => x.field === 'numbers') && <p className="field-error text-xs text-rose-300">{err.problems.find((x) => x.field === 'numbers')!.message}</p>}
        <button type="button" className="btn" onClick={() => setC({ ...c, numbers: [...c.numbers, { kind: 'mobile', number: '' }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Another number</button>
      </fieldset>
      <Field label="Email" htmlFor="de" error={p('email')}><input id="de" type="email" className="input" value={c.email ?? ''} onChange={(e) => setC({ ...c, email: e.target.value })} /></Field>
      <Field label="Notes" htmlFor="dno" error={p('notes')} hint="Shown on the caller card when they ring."><textarea id="dno" rows={3} className="input" value={c.notes ?? ''} onChange={(e) => setC({ ...c, notes: e.target.value })} /></Field>
    </Drawer>
  )
}
