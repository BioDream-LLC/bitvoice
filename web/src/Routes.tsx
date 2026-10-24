import { ArrowDown, ArrowDownLeft, ArrowUp, ArrowUpRight, Plus, Route, Siren } from 'lucide-react'
import { useState } from 'react'
import {
  useCreateInboundMutation, useCreateOutboundMutation, useDeleteInboundMutation, useDeleteOutboundMutation,
  useInboundRoutesQuery, useLookupsQuery, useOutboundRoutesQuery, usePinSetsQuery, useReorderOutboundMutation, useSettingsQuery, useTrunksQuery,
  useUpdateInboundMutation, useUpdateOutboundMutation, type ApiErrorBody, type Destination, type InboundRoute,
  type OutboundRoute,
} from './api'
import { useOpenParam } from './useOpenParam'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { DestinationPicker } from './Extensions'
import { Chip, Empty, Field, Page, problemFor } from './ui'

export function Routes() {
  const admin = useIsAdmin()
  const { data: inbound } = useInboundRoutesQuery()
  const { data: outbound } = useOutboundRoutesQuery()
  const { data: trunks } = useTrunksQuery()
  const { data: settings } = useSettingsQuery()
  const [reorder] = useReorderOutboundMutation()
  const [ib, setIb] = useState<InboundRoute | null>(null)
  useOpenParam(inbound?.items, setIb)
  const [ob, setOb] = useState<OutboundRoute | null>(null)
  const trunkName = (id: string) => trunks?.items.find((t) => t.id === id)?.name ?? 'any trunk'
  const emergencyNums = settings?.system.emergencyNumbers ?? []

  const move = (i: number, d: number) => {
    const ids = outbound!.items.map((r) => r.id)
    const [x] = ids.splice(i, 1)
    ids.splice(i + d, 0, x)
    attempt(() => reorder(ids).unwrap(), 'Route order saved')
  }

  return (
    <Page icon={Route} title="Call routing" accent="from-amber-400 to-orange-400"
      intro="Where calls from outside go, and which trunk carries the numbers people dial.">
      <section className="mb-10">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="flex items-center gap-2 text-lg font-semibold text-slate-100"><ArrowDownLeft className="text-emerald-300" aria-hidden /> Incoming calls</h2>
          {admin && <button className="btn" onClick={() => setIb({ id: '', name: '', did: '', trunk: '', destination: { type: 'extension', target: '' } })}><Plus size={15} className="text-emerald-300" aria-hidden /> Add</button>}
        </div>
        {!inbound?.items.length ? <Empty icon={ArrowDownLeft} title="No incoming routes">Calls from a trunk need a route to an extension or ring group.</Empty> : (
          <div className="grid gap-3 md:grid-cols-2">
            {inbound.items.map((r) => (
              <button key={r.id} className="card p-4 text-left hover:border-slate-700" onClick={() => setIb(r)}>
                <div className="font-semibold text-slate-100">{r.name || 'Unnamed route'}</div>
                <div className="mt-2 flex flex-wrap items-center gap-2 text-sm text-slate-400">
                  Calls to {r.did ? <Chip kind="number">{r.did}</Chip> : <span className="text-amber-300">any number</span>}
                  on {r.trunk ? <Chip kind="trunk">{trunkName(r.trunk)}</Chip> : <span className="text-teal-300">any trunk</span>}
                  ring <DestChip d={r.destination} />
                </div>
              </button>
            ))}
          </div>
        )}
      </section>

      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="flex items-center gap-2 text-lg font-semibold text-slate-100"><ArrowUpRight className="text-sky-300" aria-hidden /> Outgoing calls</h2>
          {admin && <button className="btn" onClick={() => setOb({ id: '', name: '', patterns: [], strip: 0, prepend: '', trunks: trunks?.items[0] ? [trunks.items[0].id] : [] })}><Plus size={15} className="text-sky-300" aria-hidden /> Add</button>}
        </div>
        <p className="mb-3 text-sm text-slate-500">The first route whose pattern matches the dialled number is used. Trunks are tried in order.</p>
        {!outbound?.items.length ? <Empty icon={ArrowUpRight} title="No outgoing routes">Add one so extensions can call outside numbers and 911.</Empty> : (
          <ol className="space-y-2">
            {outbound.items.map((r, i) => {
              const emergency = r.patterns.some((p) => emergencyNums.includes(p))
              return (
                <li key={r.id} className="card flex items-center gap-3 p-4">
                  <span className="grid h-7 w-7 place-items-center rounded-full bg-slate-800 text-xs font-bold text-amber-300">{i + 1}</span>
                  <button className="min-w-0 flex-1 text-left" onClick={() => setOb(r)}>
                    <div className="flex items-center gap-2 font-semibold text-slate-100">{emergency && <Siren size={15} className="text-rose-300" aria-label="Emergency" />}{r.name}</div>
                    <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs text-slate-400">
                      {r.patterns.map((p) => <code key={p} className="rounded bg-slate-800 px-1.5 py-0.5 text-amber-300">{p}</code>)}
                      <span>→</span>
                      {r.trunks.map((t) => <Chip key={t} kind="trunk">{trunkName(t)}</Chip>)}
                    </div>
                  </button>
                  {admin && (
                    <div className="flex flex-col">
                      <button className="rounded p-1 text-slate-500 hover:text-slate-200 disabled:opacity-30" disabled={i === 0} onClick={() => move(i, -1)} aria-label={`Move ${r.name} up`}><ArrowUp size={14} aria-hidden /></button>
                      <button className="rounded p-1 text-slate-500 hover:text-slate-200 disabled:opacity-30" disabled={i === outbound.items.length - 1} onClick={() => move(i, 1)} aria-label={`Move ${r.name} down`}><ArrowDown size={14} aria-hidden /></button>
                    </div>
                  )}
                </li>
              )
            })}
          </ol>
        )}
      </section>
      {ib && <InboundEditor r={ib} onClose={() => setIb(null)} />}
      {ob && <OutboundEditor r={ob} onClose={() => setOb(null)} />}
    </Page>
  )
}

function DestChip({ d }: { d: Destination }) {
  if (d.type === 'extension') return <Chip kind="extension">{d.target}</Chip>
  if (d.type === 'ringgroup') return <Chip kind="ringgroup">{d.target}</Chip>
  if (d.type === 'queue') return <Chip kind="queue">{d.target}</Chip>
  if (d.type === 'script') return <span className="text-fuchsia-300">a script</span>
  if (d.type === 'menu') return <span className="text-sky-300">a menu</span>
  if (d.type === 'schedule') return <span className="text-amber-300">a schedule</span>
  if (d.type === 'audiosocket') return <span className="text-fuchsia-300">a voice bot</span>
  if (d.type === 'toggle') return <span className="text-indigo-300">a day/night toggle</span>
  if (d.type === 'voicemail') return <span className="text-violet-300">voicemail of {d.target}</span>
  if (d.type === 'fax') return <span className="text-violet-300">fax {d.target}</span>
  if (d.type === 'esrp') return <span className="text-rose-300">ESRP (by location)</span>
  if (d.type === 'ariapp') return <span className="text-sky-300">ARI application {d.target}</span>
  if (d.type === 'agi') return <span className="text-sky-300">AGI script {d.target}</span>
  return <span className="text-violet-300">{d.type === 'echo' ? 'the echo test' : 'nobody (hang up)'}</span>
}

function InboundEditor({ r: initial, onClose }: { r: InboundRoute; onClose: () => void }) {
  const admin = useIsAdmin()
  const [r, setR] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: trunks } = useTrunksQuery()
  const [create] = useCreateInboundMutation()
  const [update] = useUpdateInboundMutation()
  const [remove] = useDeleteInboundMutation()
  const isNew = !initial.id
  const save = async () => {
    const e = await attempt(() => (isNew ? create(r) : update(r)).unwrap(), 'Incoming route saved')
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New incoming route' : r.name} icon={<ArrowDownLeft className="text-emerald-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what="this route" onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Route removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <Field label="Name" htmlFor="in"><input id="in" className="input" autoFocus value={r.name} onChange={(e) => { const v = e.target.value; setR((r0) => ({ ...r0, name: v })) }} placeholder="Main number" /></Field>
        <Field label="Number called (DID)" htmlFor="id" error={problemFor(err?.problems, 'did')} hint="Leave empty to catch every number on the trunk.">
          <input id="id" className="input font-mono text-amber-300" value={r.did} onChange={(e) => { const v = e.target.value.replace(/[^\d+]/g, ''); setR((r0) => ({ ...r0, did: v })) }} placeholder="+12125550100" />
        </Field>
        <Field label="Arriving on" htmlFor="it">
          <select id="it" className="input" value={r.trunk} onChange={(e) => { const v = e.target.value; setR((r0) => ({ ...r0, trunk: v })) }}>
            <option value="">Any trunk</option>
            {trunks?.items.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        </Field>
        <DestinationPicker label="Send calls to" required value={r.destination.target || r.destination.type === 'echo' || r.destination.type === 'hangup' ? r.destination : null}
          onChange={(d) => d && setR((r0) => ({ ...r0, destination: d }))} error={problemFor(err?.problems, 'destination')} />
        <LookupField value={r.lookup ?? ''} onChange={(v) => setR((r0) => ({ ...r0, lookup: v }))} error={problemFor(err?.problems, 'lookup')} />
      </fieldset>
    </Drawer>
  )
}

function OutboundEditor({ r: initial, onClose }: { r: OutboundRoute; onClose: () => void }) {
  const admin = useIsAdmin()
  const [r, setR] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: trunks } = useTrunksQuery()
  const [create] = useCreateOutboundMutation()
  const [update] = useUpdateOutboundMutation()
  const [remove] = useDeleteOutboundMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(r) : update(r)).unwrap(), 'Outgoing route saved')
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New outgoing route' : r.name} icon={<ArrowUpRight className="text-sky-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what="this route" onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Route removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <Field label="Name" htmlFor="on" error={p('name')}><input id="on" className="input" autoFocus value={r.name} onChange={(e) => { const v = e.target.value; setR((r0) => ({ ...r0, name: v })) }} placeholder="Local and national" /></Field>
        <Field label="Dial patterns" htmlFor="op" error={p('patterns')}
          hint={<>One per line. <code className="text-amber-300">X</code> any digit, <code className="text-amber-300">N</code> 2–9, <code className="text-amber-300">Z</code> 1–9, <code className="text-amber-300">[1-5]</code> a range, a final <code className="text-amber-300">.</code> for any further digits. US numbers: <code className="text-amber-300">NXXNXXXXXX</code>.</>}>
          <textarea id="op" rows={4} className="input font-mono text-amber-300" value={r.patterns.join('\n')}
            onChange={(e) => { const v = e.target.value.split('\n').map((s) => s.trim()).filter(Boolean); setR((r0) => ({ ...r0, patterns: v })) }} />
        </Field>
        <div className="grid grid-cols-2 gap-4">
          <Field label="Remove digits from the front" htmlFor="os" error={p('strip')} hint="For example 1 to drop a leading 9."><input id="os" type="number" min={0} max={10} className="input" value={r.strip} onChange={(e) => { const v = Number(e.target.value); setR((r0) => ({ ...r0, strip: v })) }} /></Field>
          <Field label="Then add in front" htmlFor="ox" error={p('prepend')} hint="For example 1 for US numbers."><input id="ox" className="input font-mono" value={r.prepend} onChange={(e) => { const v = e.target.value.replace(/[^\d+]/g, ''); setR((r0) => ({ ...r0, prepend: v })) }} /></Field>
        </div>
        <Field label="Trunks, tried in order" error={p('trunks')}>
          <div className="space-y-1.5">
            {trunks?.items.map((t) => {
              const idx = r.trunks.indexOf(t.id)
              return (
                <label key={t.id} className="flex items-center gap-3 rounded-lg border border-slate-800 px-3 py-2 text-sm">
                  <input type="checkbox" aria-label={`Use ${t.name}`} checked={idx >= 0} onChange={() => setR((r0) => ({ ...r0, trunks: r0.trunks.includes(t.id) ? r0.trunks.filter((x) => x !== t.id) : [...r0.trunks, t.id] }))} />
                  <Chip kind="trunk">{t.name}</Chip>
                  {idx >= 0 && <span className="ml-auto text-xs text-amber-300">#{idx + 1}</span>}
                </label>
              )
            })}
            {!trunks?.items.length && <p className="text-sm text-amber-300">Add a trunk first.</p>}
          </div>
        </Field>
        <PINSetField value={r.pinSet ?? ''} onChange={(v) => setR((r0) => ({ ...r0, pinSet: v }))} error={p('pinSet')} />
      </fieldset>
    </Drawer>
  )
}

/** The caller ID lookup an inbound route asks for callers sent without a name. */
function LookupField({ value, onChange, error }: { value: string; onChange: (v: string) => void; error?: string }) {
  const { data } = useLookupsQuery()
  return (
    <Field label="Caller ID lookup" htmlFor="ilk" error={error} hint={data?.items.length ? 'For callers the provider sends without a name.' : <>Add one on <a className="text-sky-300 underline" href="#/lookups">Caller ID lookup</a> to name callers from your CRM or OpenCNAM.</>}>
      <select id="ilk" className="input" value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">None</option>
        {data?.items.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
      </select>
    </Field>
  )
}

/** The PIN set an outbound route asks for before calling out. */
function PINSetField({ value, onChange, error }: { value: string; onChange: (v: string) => void; error?: string }) {
  const { data } = usePinSetsQuery()
  return (
    <Field label="Ask for a PIN" htmlFor="opin" error={error} hint={data?.items.length ? 'Callers enter one of its PINs before the call goes out. Emergency calls never ask.' : <>Make a list of PINs on <a className="text-sky-300 underline" href="#/pin-sets">PIN sets</a> to ask for one here.</>}>
      <select id="opin" className="input" value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">No PIN</option>
        {data?.items.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
      </select>
    </Field>
  )
}
