import { ArrowDownLeft, ArrowUpRight, Download, FileText, Inbox, Mail, Paperclip, Plus, Printer, RotateCcw, Save, Send, Trash2, X } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  errorOf, useCreateFaxBoxMutation, useDeleteFaxBoxMutation, useDeleteFaxMutation, useFaxBoxesQuery, useFaxesQuery, useFaxKindsQuery, useRetryFaxMutation,
  useSaveSettingsMutation, useSendFaxMutation, useSettingsQuery, useTestFaxMailMutation, useUpdateFaxBoxMutation,
  type ApiErrorBody, type FaxBox, type FaxMail, type FaxRecord,
} from './api'
import { useDraft } from './useDraft'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { nextFree, useTakenNumbers } from './numbers'
import { Badge, Empty, Field, Page, StaleBanner, Toggle, problemFor, relTime } from './ui'

const statusOf: Record<FaxRecord['status'], { label: string; status: 'ok' | 'warn' | 'fail' | 'info' }> = {
  received: { label: 'Received', status: 'ok' },
  partial: { label: 'Part received', status: 'warn' },
  failed: { label: 'Failed', status: 'fail' },
  queued: { label: 'Waiting to send', status: 'info' },
  sending: { label: 'Sending', status: 'info' },
  sent: { label: 'Sent', status: 'ok' },
}
const outStatus = (f: FaxRecord) => (f.status === 'partial' && f.direction === 'out' ? 'Part sent' : statusOf[f.status].label)

/** Who sent a fax: the caller's number, else the identity the sending machine gave. */
/** The speed the pages went at, under the page count: the first thing to look at when one machine's faxes keep failing. */
const Speed = ({ rate }: { rate?: number }) => (rate ? <div className="text-xs text-slate-500">{rate.toLocaleString('en-US')} bit/s</div> : null)
const senderOf = (f: FaxRecord) => (f.from && f.from !== 'anonymous' ? f.from : f.station || 'unknown')

export function Faxes() {
  const admin = useIsAdmin()
  const { data: boxes } = useFaxBoxesQuery()
  const { data: faxes } = useFaxesQuery()
  const { data: settings } = useSettingsQuery()
  const [remove] = useDeleteFaxMutation()
  const [edit, setEdit] = useState<FaxBox | null>(null)
  const [sending, setSending] = useState(false)
  const [retry] = useRetryFaxMutation()
  const taken = useTakenNumbers()
  const tz = settings?.system.timezone
  const when = (s: string) => new Date(s).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: tz || undefined })
  return (
    <Page icon={Printer} title="Faxes" accent="from-violet-400 to-sky-400"
      intro="Fax numbers BitVoice answers itself, as a fax machine: each fax received becomes a PDF, kept here and emailed to whoever you choose. Faxes go out from here, or by email. Route a phone number to a fax number in Call routing."
      actions={<>
        {admin && <button className="btn" onClick={() => setEdit({ id: '', number: nextFree(taken, 300), name: 'Office fax', email: [], station: '', senders: [] })}><Plus size={15} className="text-violet-300" aria-hidden /> Add fax number</button>}
        {!!boxes?.items.length && <button className="btn btn-primary" onClick={() => setSending(true)}><Send size={15} aria-hidden /> Send a fax</button>}
      </>}>
      <section aria-labelledby="fxb-h" className="space-y-3">
        <h2 id="fxb-h" className="text-sm font-semibold text-violet-300">Fax numbers</h2>
        {!boxes?.items.length ? <Empty icon={Printer} title="No fax numbers yet">Add one, then send a phone number's calls to it in Call routing.</Empty> : (
          <div className="grid gap-3 md:grid-cols-2">
            {boxes.items.map((b) => (
              <button key={b.id} className="card p-4 text-left hover:border-slate-700" onClick={() => setEdit(b)}>
                <div className="flex items-center gap-2">
                  <Printer size={16} className="text-violet-300" aria-hidden />
                  <span className="font-mono text-amber-300">{b.number}</span>
                  <span className="font-semibold text-slate-100">{b.name}</span>
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-slate-400">
                  <Mail size={12} className="text-sky-300" aria-hidden />
                  {b.email.length ? b.email.join(', ') : <span className="text-slate-500">kept here only, not emailed</span>}
                </div>
                {!!b.senders?.length && <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-slate-400"><Send size={12} className="text-emerald-300" aria-hidden /> may send by email: {b.senders.join(', ')}</div>}
              </button>
            ))}
          </div>
        )}
      </section>
      <section aria-labelledby="fxr-h" className="mt-6 space-y-3">
        <h2 id="fxr-h" className="text-sm font-semibold text-violet-300">Sent and received</h2>
        {!faxes?.items.length ? <Empty icon={FileText} title="No faxes yet">Faxes received and sent appear here.</Empty> : (
          <div className="card overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-slate-500">
                <tr><th className="p-3">When</th><th className="p-3">From</th><th className="p-3">To</th><th className="p-3">Pages</th><th className="p-3">Status</th><th className="p-3"><span className="sr-only">Actions</span></th></tr>
              </thead>
              <tbody>
                {faxes.items.map((f) => (
                  <tr key={f.id} className="border-t border-slate-800">
                    <td className="p-3 text-slate-300">{when(f.at)}</td>
                    {f.direction === 'out' ? <>
                      <td className="p-3 text-slate-300"><span className="inline-flex items-center gap-1"><ArrowUpRight size={14} className="text-emerald-300" aria-label="Sent" /><span className="font-mono text-amber-300">{f.box}</span></span> {f.boxName}{f.by && <div className="text-xs text-slate-500">by {f.by}</div>}</td>
                      <td className="p-3"><span className="font-mono text-sky-300">{f.to}</span>{f.remoteId && f.remoteId !== f.to && <div className="text-xs text-slate-500">machine: {f.remoteId}</div>}</td>
                      <td className="p-3 text-slate-300">{f.status === 'sent' || !f.sent ? f.pages : `${f.sent} of ${f.pages}`}<Speed rate={f.rate} /></td>
                    </> : <>
                      <td className="p-3"><span className="inline-flex items-center gap-1"><ArrowDownLeft size={14} className="text-sky-300" aria-label="Received" /><span className="font-mono text-sky-300">{senderOf(f)}</span></span>{f.station && senderOf(f) !== f.station && <div className="text-xs text-slate-500">machine: {f.station}</div>}</td>
                      <td className="p-3 text-slate-300"><span className="font-mono text-amber-300">{f.box}</span> {f.boxName}</td>
                      <td className="p-3 text-slate-300">{f.pages}<Speed rate={f.rate} /></td>
                    </>}
                    <td className="p-3"><Badge status={statusOf[f.status].status}>{outStatus(f)}</Badge>{f.error && <div className="mt-1 max-w-xs text-xs text-slate-500">{f.error}</div>}
                      {f.subject && <div className="mt-1 max-w-xs truncate text-xs text-slate-500">Email: {f.subject}</div>}
                      {f.emailed?.length ? <div className="mt-1 text-xs text-slate-500">Emailed to {f.emailed.join(', ')}</div> : null}</td>
                    <td className="p-3">
                      <div className="flex justify-end gap-1">
                        {f.direction === 'out' && (f.status === 'failed' || f.status === 'partial') && <button className="btn" onClick={() => attempt(() => retry(f.id).unwrap(), 'Fax queued again')}><RotateCcw size={14} className="text-amber-300" aria-hidden /> Try again</button>}
                        {f.pages > 0 && <a className="btn" href={`/api/faxes/${f.id}.pdf`} target="_blank" rel="noreferrer"><FileText size={14} className="text-sky-300" aria-hidden /> View</a>}
                        {f.pages > 0 && <a className="btn" href={`/api/faxes/${f.id}.pdf`} download={`fax-${f.id}.pdf`}><Download size={14} className="text-emerald-300" aria-hidden /> PDF</a>}
                        {admin && <button className="btn-ghost p-2" aria-label={f.direction === 'out' ? `Delete the fax to ${f.to}` : `Delete the fax from ${senderOf(f)}`} onClick={() => attempt(() => remove(f.id).unwrap(), 'Fax deleted')}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      {admin && <FaxMailSetup />}
      {edit && <FaxEditor b={edit} onClose={() => setEdit(null)} />}
      {sending && boxes && <SendFax boxes={boxes.items} onClose={() => setSending(false)} />}
    </Page>
  )
}

function FaxEditor({ b: initial, onClose }: { b: FaxBox; onClose: () => void }) {
  const admin = useIsAdmin()
  const [b, setB] = useState(initial)
  const [emails, setEmails] = useState(initial.email.join(', '))
  const [senders, setSenders] = useState((initial.senders ?? []).join(', '))
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [create] = useCreateFaxBoxMutation()
  const [update] = useUpdateFaxBoxMutation()
  const [remove] = useDeleteFaxBoxMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const list = (s: string) => s.split(/[,;\s]+/).map((x) => x.trim()).filter(Boolean)
    const v = { ...b, email: list(emails), senders: list(senders) }
    const e = await attempt(() => (isNew ? create(v) : update(v)).unwrap(), `Fax number ${b.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  const emailErr = err?.problems?.find((x) => x.field.includes('.email['))?.message
  const sendersErr = err?.problems?.find((x) => x.field.includes('.senders['))?.message
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New fax number' : b.name} icon={<Printer className="text-violet-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Fax number removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="fxn" error={p('number')}><input id="fxn" className="input font-mono text-amber-300" value={b.number} onChange={(e) => { const v = e.target.value.replace(/\D/g, ''); setB((x) => ({ ...x, number: v })) }} /></Field>
          <Field label="Name" htmlFor="fxm" error={p('name')}><input id="fxm" className="input" value={b.name} onChange={(e) => { const v = e.target.value; setB((x) => ({ ...x, name: v })) }} /></Field>
        </div>
        <Field label="Email each fax to" htmlFor="fxe" error={emailErr} hint="Addresses separated by commas. Each fax arrives as a PDF. Empty: kept on this page only. Needs email set up in Settings.">
          <input id="fxe" className="input" value={emails} placeholder="office@example.com" onChange={(e) => setEmails(e.target.value)} />
        </Field>
        <Field label="Fax machine identity" htmlFor="fxs" error={p('station')} hint="What the sending fax machine shows and prints as the machine it reached, usually this fax number in full (+12125550199). Digits, spaces and +, up to 20.">
          <input id="fxs" className="input font-mono" value={b.station} placeholder="+12125550199" onChange={(e) => { const v = e.target.value; setB((x) => ({ ...x, station: v })) }} />
        </Field>
        <Field label="May also send faxes by email" htmlFor="fxw" error={sendersErr} hint="Besides the people above, who can always send: addresses, or @example.com for anyone at a domain, separated by commas. Used by email to fax, below the list of faxes.">
          <input id="fxw" className="input" value={senders} placeholder="@example.com" onChange={(e) => setSenders(e.target.value)} />
        </Field>
        <p className="text-xs text-slate-500">BitVoice sends and receives faxes over T.38, the fax standard for the internet: your provider must support it (most do). Faxes sent as sound cannot be received here; a fax machine on a gateway can still take them.</p>
      </fieldset>
    </Drawer>
  )
}

/** Send a fax: from one of the fax numbers, a note and documents. */
function SendFax({ boxes, onClose }: { boxes: FaxBox[]; onClose: () => void }) {
  const [box, setBox] = useState(boxes[0]?.number ?? '')
  const [to, setTo] = useState('')
  const [cover, setCover] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [send, { isLoading }] = useSendFaxMutation()
  const { data: kinds } = useFaxKindsQuery()
  const input = useRef<HTMLInputElement>(null)
  const go = async () => {
    const f = new FormData()
    f.set('box', box)
    f.set('to', to)
    f.set('cover', cover)
    for (const x of files) f.append('file', x, x.name)
    try {
      const r = await send(f).unwrap()
      toast.success(`Fax to ${r.to} queued`, { description: `${r.pages} page${r.pages === 1 ? '' : 's'}` })
      onClose()
    } catch (e) { setErr(errorOf(e)) }
  }
  const add = (list: FileList | null) => { if (list) setFiles((x) => [...x, ...Array.from(list)]) }
  return (
    <Drawer open onClose={onClose} title="Send a fax" icon={<Send className="text-emerald-300" aria-hidden />} error={err}
      footer={<button className="btn btn-primary" disabled={isLoading} onClick={go}><Send size={15} aria-hidden /> {isLoading ? 'Preparing the pages...' : 'Send'}</button>}>
      <div className="space-y-5">
        <div className="grid grid-cols-2 gap-4">
          <Field label="From" htmlFor="sfb">
            <select id="sfb" className="input" value={box} onChange={(e) => setBox(e.target.value)}>
              {boxes.map((b) => <option key={b.id} value={b.number}>{b.number} {b.name}</option>)}
            </select>
          </Field>
          <Field label="To" htmlFor="sft" hint="The fax number, as you would dial it.">
            <input id="sft" className="input font-mono text-sky-300" inputMode="tel" value={to} placeholder="212 555 0199" onChange={(e) => setTo(e.target.value)} />
          </Field>
        </div>
        <Field label="Cover note" htmlFor="sfc" hint="Optional: a first page with this text.">
          <textarea id="sfc" className="input min-h-28" value={cover} placeholder={'To: Accounts\nPlease find the signed contract attached.'} onChange={(e) => setCover(e.target.value)} />
        </Field>
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-slate-300">Documents</span>
            <button className="btn" onClick={() => input.current?.click()}><Paperclip size={14} className="text-sky-300" aria-hidden /> Add files</button>
            <input ref={input} type="file" multiple className="hidden" aria-label="Documents to fax" onChange={(e) => { add(e.target.files); e.target.value = '' }} />
          </div>
          <div className="rounded-lg border border-dashed border-slate-700 p-3 text-sm" onDragOver={(e) => e.preventDefault()} onDrop={(e) => { e.preventDefault(); add(e.dataTransfer.files) }}>
            {!files.length ? <p className="text-slate-500">Drop files here, or add them. They are faxed in this order, after the note.</p> : (
              <ul className="space-y-1">
                {files.map((f, i) => (
                  <li key={i} className="flex items-center gap-2">
                    <FileText size={14} className="text-sky-300" aria-hidden /><span className="flex-1 truncate text-slate-200">{f.name}</span>
                    <span className="text-xs text-slate-500">{Math.max(1, Math.round(f.size / 1024))} KB</span>
                    <button className="btn-ghost p-1" aria-label={`Remove ${f.name}`} onClick={() => setFiles((x) => x.filter((_, j) => j !== i))}><X size={14} className="text-rose-300" aria-hidden /></button>
                  </li>
                ))}
              </ul>
            )}
          </div>
          {kinds && <p className="text-xs text-slate-500">This server can fax: {kinds.kinds.join(', ')}. Pages are {kinds.paper === 'a4' ? 'A4' : 'US letter'} size.{!kinds.kinds.includes('PDF') && ' For PDF, install poppler on the server; for Word and other office files, LibreOffice too.'}</p>}
        </div>
      </div>
    </Drawer>
  )
}

const blankMail: FaxMail = { host: '', port: 993, username: '', password: '', folder: '', anySender: false }

/** The mailbox read for email to fax. */
function FaxMailSetup() {
  const { data } = useSettingsQuery()
  const { data: kinds } = useFaxKindsQuery(undefined, { pollingInterval: 60000 })
  // The same object while the saved settings are the same: a new one each render would look like a change.
  const server = useMemo(() => (data ? { ...blankMail, ...(data.system.faxMail ?? {}) } : undefined), [data])
  const { draft, setDraft, reset, stale } = useDraft<FaxMail>(server)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [save, { isLoading }] = useSaveSettingsMutation()
  const [test, { isLoading: testing }] = useTestFaxMailMutation()
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.faxMail.' + f)
  const set = (v: Partial<FaxMail>) => setDraft((d) => ({ ...d, ...v }))
  const doSave = async () => {
    try { const s = await save({ ...data, system: { ...data.system, faxMail: draft } }).unwrap(); reset({ ...blankMail, ...(s.system.faxMail ?? {}) }); setErr(null); toast.success(draft.host ? 'Email to fax saved' : 'Email to fax turned off') }
    catch (e) { setErr(errorOf(e)) }
  }
  const doTest = async () => {
    try { const r = await test(draft).unwrap(); toast.success('The mailbox works', { description: r.result }) }
    catch (e) { toast.error('The mailbox did not work', { description: errorOf(e).message }) }
  }
  const mail = kinds?.mail
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="fxm-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="fxm-h" className="flex items-center gap-2 font-semibold text-slate-100"><Inbox size={17} className="text-sky-300" aria-hidden /> Email to fax</h2>
      <p className="text-sm text-slate-400">
        BitVoice reads a mailbox every minute. Send it an email with the fax number in the subject (<span className="font-mono text-slate-300">Fax 212 555 0199</span>), or to
        <span className="font-mono text-slate-300"> name+2125550199@</span> if your mail server takes plus addresses. Attachments become the pages, the email's text the cover note.
        It goes out from the fax number whose people (or senders) include you, and you get an email when it has gone through.
      </p>
      <div className="grid gap-3 md:grid-cols-4">
        <Field label="Mail server (IMAP)" htmlFor="fmh" error={p('host')} hint="Empty turns email to fax off."><input id="fmh" className="input" value={draft.host} placeholder="imap.example.com" onChange={(e) => set({ host: e.target.value.trim() })} /></Field>
        <Field label="Port" htmlFor="fmp" error={p('port')} hint="993: IMAP over TLS."><input id="fmp" className="input font-mono" inputMode="numeric" value={draft.port || ''} onChange={(e) => set({ port: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
        <Field label="Username" htmlFor="fmu" error={p('username')}><input id="fmu" className="input" autoComplete="off" value={draft.username} placeholder="fax@example.com" onChange={(e) => set({ username: e.target.value.trim() })} /></Field>
        <Field label="Password" htmlFor="fmw" error={p('password')} hint="Blank keeps the saved one. An app password, for Google and Microsoft mailboxes."><input id="fmw" className="input font-mono" type="password" autoComplete="new-password" value={draft.password} onChange={(e) => set({ password: e.target.value })} /></Field>
      </div>
      <div className="grid gap-3 md:grid-cols-4">
        <Field label="Folder" htmlFor="fmf" error={p('folder')} hint="Blank: the inbox."><input id="fmf" className="input" value={draft.folder} placeholder="INBOX" onChange={(e) => set({ folder: e.target.value })} /></Field>
        <div className="md:col-span-3">
          <Toggle checked={draft.anySender} onChange={(v) => set({ anySender: v })} label="Accept mail the sender's domain did not vouch for" />
          <p className={draft.anySender ? 'mt-1 text-xs text-amber-300' : 'mt-1 text-xs text-slate-500'}>
            {draft.anySender ? 'Anyone who writes a permitted address in From could send faxes. Only for a mailbox no outside mail reaches.' : 'Off: a message is faxed only when your mail server found it really came from the sender\'s domain (DKIM, SPF or DMARC). Other messages are dropped without a reply.'}
          </p>
        </div>
      </div>
      {mail?.checked && !mail.checked.startsWith('0001') && draft.host === (data.system.faxMail?.host ?? '') && (
        <p className={mail.error ? 'text-sm text-rose-300' : 'text-sm text-emerald-300'}>{mail.error ? `Last read ${relTime(mail.checked)}: ${mail.error}` : `Read ${relTime(mail.checked)}.`}</p>
      )}
      <div className="flex justify-end gap-2">
        <button className="btn" disabled={!draft.host || testing} onClick={doTest}><Mail size={14} className="text-sky-300" aria-hidden /> {testing ? 'Trying...' : 'Test'}</button>
        <button className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={15} aria-hidden /> Save</button>
      </div>
    </section>
  )
}
