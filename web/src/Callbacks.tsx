import { Bot, Check, PhoneOutgoing, PhoneForwarded, Plus, RotateCcw, Trash2, X } from 'lucide-react'
import { useState } from 'react'
import { errorOf, useAddCallbackMutation, useCallbacksQuery, useDeleteCallbackMutation, useExtensionsQuery, useRingBackMutation, useSetCallbackMutation, type ApiErrorBody, type Callback } from './api'
import { attempt, Drawer, useIsAdmin } from './Editor'
import { Badge, Empty, Field, Page, problemFor, relTime } from './ui'
import { toast } from 'sonner'

const RING_FROM = 'bitvoice.ringBackFrom'

/** Callbacks: people who asked to be rung back, taken by an AI receptionist or entered by staff. */
export function Callbacks() {
  const admin = useIsAdmin()
  const { data } = useCallbacksQuery()
  const [adding, setAdding] = useState(false)
  const [showClosed, setShowClosed] = useState(false)
  const open = data?.items.filter((c) => c.status === 'open') ?? []
  const closed = data?.items.filter((c) => c.status !== 'open') ?? []
  return (
    <Page icon={PhoneForwarded} title="Callbacks" accent="from-emerald-300 to-sky-300"
      intro="People who asked to be rung back. AI receptionists book them when a caller wants one; you can add them by hand too. Ring back from here, then mark each one done."
      actions={admin && <button className="btn btn-primary" onClick={() => setAdding(true)}><Plus size={15} aria-hidden /> Add callback</button>}>
      {open.length === 0 && closed.length === 0 ? <Empty icon={PhoneForwarded} title="No callbacks">When a caller asks an AI receptionist to be rung back, it appears here.</Empty> : (
        <div className="space-y-6">
          {open.length === 0 ? <p className="text-sm text-emerald-300">Nobody is waiting for a call back.</p> : (
            <ul className="grid gap-3 md:grid-cols-2" aria-label="Waiting for a call back">
              {open.map((c) => <CallbackCard key={c.id} c={c} admin={admin} />)}
            </ul>
          )}
          {closed.length > 0 && (
            <section aria-labelledby="cb-closed">
              <button id="cb-closed" className="mb-2 text-sm font-medium text-slate-400 hover:text-slate-200" aria-expanded={showClosed} onClick={() => setShowClosed((v) => !v)}>
                {showClosed ? 'Hide' : 'Show'} {closed.length} finished
              </button>
              {showClosed && <ul className="grid gap-3 md:grid-cols-2" aria-label="Finished callbacks">{closed.map((c) => <CallbackCard key={c.id} c={c} admin={admin} />)}</ul>}
            </section>
          )}
        </div>
      )}
      {adding && <CallbackEditor onClose={() => setAdding(false)} />}
    </Page>
  )
}

function CallbackCard({ c, admin }: { c: Callback; admin: boolean }) {
  const [set] = useSetCallbackMutation()
  const [del] = useDeleteCallbackMutation()
  const [note, setNote] = useState(c.note ?? '')
  const isOpen = c.status === 'open'
  const close = (status: Callback['status'], msg: string) => attempt(() => set({ id: c.id, status, note }).unwrap(), msg)
  return (
    <li className={`card space-y-2 p-4 ${isOpen ? 'border-emerald-900/60' : 'opacity-80'}`}>
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="font-semibold text-slate-100">{c.name || 'No name given'}</p>
          <p className="font-mono text-sm text-sky-300">{c.number}</p>
        </div>
        {isOpen ? <Badge status="warn">Waiting</Badge> : c.status === 'done' ? <Badge status="ok">Done</Badge> : <Badge status="idle">Cancelled</Badge>}
      </div>
      {c.reason && <p className="text-sm text-slate-300"><span className="text-slate-500">About: </span>{c.reason}</p>}
      {c.when && <p className="text-sm text-slate-300"><span className="text-slate-500">Best time: </span><span className="text-violet-300">{c.when}</span></p>}
      <p className="flex items-center gap-1.5 text-xs text-slate-500">
        {c.receptionist ? <><Bot size={13} className="text-violet-300" aria-hidden /> Taken by {c.receptionist}</> : 'Added by hand'}, {relTime(c.created)}
        {!isOpen && c.by && <>; {c.status} by {c.by} {relTime(c.closed)}</>}
      </p>
      {!admin && c.note && <p className="text-sm text-slate-300"><span className="text-slate-500">Note: </span>{c.note}</p>}
      {admin && (
        <>
          <label className="sr-only" htmlFor={`cbn-${c.id}`}>Note for {c.name || c.number}</label>
          <input id={`cbn-${c.id}`} className="input text-sm" placeholder="Note (what was agreed)" value={note} maxLength={1000} onChange={(e) => setNote(e.target.value)}
            onBlur={() => { if (note !== (c.note ?? '')) attempt(() => set({ id: c.id, status: c.status, note }).unwrap(), 'Note saved') }} />
          <div className="flex flex-wrap items-center gap-2">
            {isOpen ? (
              <>
                <RingBack c={c} />
                <button className="btn text-emerald-300" onClick={() => close('done', 'Marked done')}><Check size={14} aria-hidden /> Done</button>
                <button className="btn" onClick={() => close('cancelled', 'Cancelled')}><X size={14} className="text-slate-400" aria-hidden /> Cancel it</button>
              </>
            ) : <button className="btn" onClick={() => close('open', 'Opened again')}><RotateCcw size={14} className="text-amber-300" aria-hidden /> Open again</button>}
            <button className="btn ml-auto text-rose-300" aria-label={`Delete callback for ${c.name || c.number}`}
              onClick={() => { if (confirm(`Delete the callback for ${c.name || c.number}?`)) attempt(() => del(c.id).unwrap(), 'Callback deleted') }}><Trash2 size={14} aria-hidden /></button>
          </div>
        </>
      )}
    </li>
  )
}

/** RingBack rings the chosen extension and, once it is picked up, calls the number for it. */
function RingBack({ c }: { c: Callback }) {
  const { data: exts } = useExtensionsQuery()
  const [ring, { isLoading }] = useRingBackMutation()
  const [from, setFrom] = useState(() => localStorage.getItem(RING_FROM) ?? '')
  const go = async () => {
    if (!from) { toast.error('Choose the extension to ring first'); return }
    localStorage.setItem(RING_FROM, from)
    try {
      await ring({ id: c.id, extension: from }).unwrap()
      toast.success(`Calling ${c.number} for extension ${from}`)
    } catch (e) {
      toast.error(errorOf(e).message)
    }
  }
  return (
    <span className="flex items-center gap-1">
      <label className="sr-only" htmlFor={`cbf-${c.id}`}>Ring back from</label>
      <select id={`cbf-${c.id}`} className="input w-36 text-sm" value={from} onChange={(e) => setFrom(e.target.value)}>
        <option value="">From…</option>
        {exts?.items.filter((x) => x.enabled).map((x) => <option key={x.number} value={x.number}>{x.number} {x.name}</option>)}
      </select>
      <button className="btn btn-primary" disabled={isLoading} onClick={go} title="Your phone rings first; pick up and the call goes out">
        <PhoneOutgoing size={14} aria-hidden /> {isLoading ? 'Ringing your phone…' : 'Ring back'}
      </button>
    </span>
  )
}

function CallbackEditor({ onClose }: { onClose: () => void }) {
  const [add, { isLoading }] = useAddCallbackMutation()
  const [v, setV] = useState({ name: '', number: '', when: '', reason: '' })
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const r = await attempt(() => add(v).unwrap(), 'Callback added')
    if (r) setErr(r)
    else onClose()
  }
  const field = (k: keyof typeof v) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => { const x = e.target.value; setV((v0) => ({ ...v0, [k]: x })) }
  return (
    <Drawer open onClose={onClose} title="New callback" icon={<PhoneForwarded size={18} className="text-emerald-300" aria-hidden />} onSave={save} saving={isLoading} error={err}>
      <Field label="Number" htmlFor="cbnum" error={p('number')} hint="The number to ring, such as +12125550123 or an extension.">
        <input id="cbnum" className="input font-mono" autoFocus inputMode="tel" value={v.number} onChange={field('number')} />
      </Field>
      <Field label="Name" htmlFor="cbname" error={p('name')}><input id="cbname" className="input" value={v.name} onChange={field('name')} /></Field>
      <Field label="Best time" htmlFor="cbwhen" error={p('when')} hint="In their words, such as tomorrow morning."><input id="cbwhen" className="input" value={v.when} onChange={field('when')} /></Field>
      <Field label="About" htmlFor="cbwhy" error={p('reason')}><textarea id="cbwhy" rows={3} className="input" value={v.reason} onChange={field('reason')} /></Field>
    </Drawer>
  )
}
