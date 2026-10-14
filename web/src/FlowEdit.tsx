import { ExternalLink, Plus, Trash2, Workflow } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import {
  useCreateInboundMutation, useExtensionsQuery, useInboundRoutesQuery, useMenusQuery, useQueuesQuery, useReceptionistsQuery,
  useRingGroupsQuery, useSchedulesQuery, useScriptsQuery, useTrunksQuery, useUpdateExtensionMutation, useUpdateInboundMutation,
  useUpdateMenuMutation, useUpdateQueueMutation, useUpdateReceptionistMutation, useUpdateRingGroupMutation, useUpdateScheduleMutation, useUpdateToggleMutation, useTogglesQuery, useAudioStreamsQuery, useUpdateAudioStreamMutation,
  useUpdateScriptMutation, type ApiErrorBody, type Destination, type InboundRoute, type Menu,
} from './api'
import { attempt, Drawer } from './Editor'
import { DestinationPicker } from './Extensions'
import { Field, problemFor } from './ui'

/** A box on the call-flow diagram: what it is, and which item it stands for. */
export interface FlowBox { kind: string; target?: string }

/**
 * FlowEdit edits where calls go from one box of the diagram, without leaving it. Each kind of box
 * saves through its own item, so every check and revision is the same as on its own page.
 */
export function FlowEdit({ box, onClose, onOpen }: { box: FlowBox; onClose: () => void; onOpen: () => void }) {
  const t = box.target ?? ''
  const { data: routes } = useInboundRoutesQuery()
  const { data: exts } = useExtensionsQuery()
  const { data: groups } = useRingGroupsQuery()
  const { data: queues } = useQueuesQuery()
  const { data: menus } = useMenusQuery()
  const { data: schedules } = useSchedulesQuery()
  const { data: toggles } = useTogglesQuery()
  const { data: streams } = useAudioStreamsQuery()
  const { data: scripts } = useScriptsQuery()
  const { data: receptionists } = useReceptionistsQuery()
  const props = { onClose, onOpen }
  switch (box.kind) {
    case 'route': { const x = routes?.items.find((r) => r.id === t); return x ? <RouteEdit route={x} {...props} /> : null }
    case 'extension': { const x = exts?.items.find((e) => e.number === t); return x ? <One title={`${x.number} ${x.name}`} links={[['If nobody answers', x.noAnswer, false, 'noAnswer']]} {...props} useSave={() => { const [u, s] = useUpdateExtensionMutation(); return [(l) => u({ ...x, noAnswer: l[0] }).unwrap(), s.isLoading] }} exclude={x.number} /> : null }
    case 'ringgroup': { const x = groups?.items.find((g) => g.number === t); return x ? <One title={`${x.number} ${x.name}`} links={[['If nobody answers', x.noAnswer, false, 'noAnswer']]} {...props} useSave={() => { const [u, s] = useUpdateRingGroupMutation(); return [(l) => u({ ...x, noAnswer: l[0] }).unwrap(), s.isLoading] }} exclude={x.number} /> : null }
    case 'queue': { const x = queues?.items.find((q) => q.number === t); return x ? <One title={`${x.number} ${x.name}`} links={[['After waiting too long', x.timeout, false, 'timeout']]} {...props} useSave={() => { const [u, s] = useUpdateQueueMutation(); return [(l) => u({ ...x, timeout: l[0] }).unwrap(), s.isLoading] }} exclude={x.number} /> : null }
    case 'schedule': { const x = schedules?.items.find((s) => s.id === t); return x ? <One title={x.name} links={[['When open', x.open, true, 'open'], ['When closed', x.closed, true, 'closed']]} {...props} useSave={() => { const [u, s] = useUpdateScheduleMutation(); return [(l) => u({ ...x, open: l[0]!, closed: l[1]! }).unwrap(), s.isLoading] }} /> : null }
    case 'audiosocket': { const x = streams?.items.find((s) => s.id === t); return x ? <One title={x.name} links={[['If the bot fails', x.fallback, true, 'fallback']]} {...props} useSave={() => { const [u, s] = useUpdateAudioStreamMutation(); return [(l) => u({ ...x, fallback: l[0]! }).unwrap(), s.isLoading] }} /> : null }
    case 'toggle': { const x = toggles?.items.find((s) => s.id === t); return x ? <One title={x.name} links={[['Normally', x.normal, true, 'normal'], ['Switched over', x.override, true, 'override']]} {...props} useSave={() => { const [u, s] = useUpdateToggleMutation(); return [(l) => u({ ...x, normal: l[0]!, override: l[1]! }).unwrap(), s.isLoading] }} /> : null }
    case 'script': { const x = scripts?.items.find((s) => s.id === t); return x ? <One title={x.name} links={[['If the script fails', x.fallback, true, 'fallback']]} {...props} useSave={() => { const [u, s] = useUpdateScriptMutation(); return [(l) => u({ ...x, fallback: l[0]! }).unwrap(), s.isLoading] }} /> : null }
    case 'receptionist': { const x = receptionists?.items.find((r) => r.id === t); return x ? <One title={x.name} links={[['A person is', x.person, true, 'person']]} {...props} noSelf={`receptionist:${x.id}`} useSave={() => { const [u, s] = useUpdateReceptionistMutation(); return [(l) => u({ ...x, person: l[0]! }).unwrap(), s.isLoading] }} /> : null }
    case 'menu': { const x = menus?.items.find((m) => m.id === t); return x ? <MenuEdit menu={x} {...props} /> : null }
  }
  return (
    <Drawer open onClose={onClose} title="Ends here" icon={<Workflow size={18} className="text-amber-300" aria-hidden />}>
      <p className="text-sm text-slate-400">Calls stop here: there is nowhere further to send them.</p>
      {box.kind !== 'hangup' && box.kind !== 'echo' && <OpenButton onOpen={onOpen} />}
    </Drawer>
  )
}

type Link = [label: string, value: Destination | null | undefined, required: boolean, field: string]
type Saver = () => [(links: (Destination | null)[]) => Promise<unknown>, boolean]

function OpenButton({ onOpen }: { onOpen: () => void }) {
  return <button type="button" className="btn" onClick={onOpen}><ExternalLink size={15} className="text-sky-300" aria-hidden /> Open its full editor</button>
}

/** One edits a box whose outgoing links are a fixed list of destinations. */
function One({ title, links, useSave, onClose, onOpen, exclude, noSelf }: { title: string; links: Link[]; useSave: Saver; onClose: () => void; onOpen: () => void; exclude?: string; noSelf?: string }) {
  const [save, saving] = useSave()
  const [vals, setVals] = useState(links.map(([, v]) => v ?? null))
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  return (
    <FlowDrawer title={title} onClose={onClose} onOpen={onOpen} saving={saving} error={err}
      onSave={async () => { const e = await attempt(() => save(vals), 'Saved'); if (e) setErr(e); else onClose() }}>
      {links.map(([label, , required, field], i) => (
        <DestinationPicker key={label} label={label} required={required} value={vals[i]} exclude={exclude} noSchedules={!!noSelf}
          emptyLabel="Hang up" error={problemFor(err?.problems, field)}
          onChange={(d) => setVals((v0) => v0.map((v, j) => (j === i ? d : v)))} />
      ))}
    </FlowDrawer>
  )
}

function FlowDrawer({ title, children, onClose, onOpen, onSave, saving, error }: { title: string; children: ReactNode; onClose: () => void; onOpen: () => void; onSave: () => void; saving: boolean; error: ApiErrorBody | null }) {
  return (
    <Drawer open onClose={onClose} title={title} icon={<Workflow size={18} className="text-amber-300" aria-hidden />} onSave={onSave} saving={saving} error={error}
      footer={<OpenButton onOpen={onOpen} />}>
      <p className="text-sm text-slate-400">Where calls go from here.</p>
      {children}
    </Drawer>
  )
}

function RouteEdit({ route, onClose, onOpen }: { route: InboundRoute; onClose: () => void; onOpen: () => void }) {
  const [update, { isLoading }] = useUpdateInboundMutation()
  const [d, setD] = useState<Destination | null>(route.destination)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  return (
    <FlowDrawer title={`${route.name || 'Route'}: ${route.did || 'any number'}`} onClose={onClose} onOpen={onOpen} saving={isLoading} error={err}
      onSave={async () => { const e = await attempt(() => update({ ...route, destination: d! }).unwrap(), 'Saved'); if (e) setErr(e); else onClose() }}>
      <DestinationPicker label="Calls go to" required value={d} onChange={setD} error={problemFor(err?.problems, 'destination')} />
    </FlowDrawer>
  )
}

function MenuEdit({ menu, onClose, onOpen }: { menu: Menu; onClose: () => void; onOpen: () => void }) {
  const [update, { isLoading }] = useUpdateMenuMutation()
  const [m, setM] = useState(menu)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const used = new Set(m.options.map((o) => o.key))
  const free = '1234567890*#'.split('').find((k) => !used.has(k))
  const p = (f: string) => problemFor(err?.problems, f)
  return (
    <FlowDrawer title={m.name} onClose={onClose} onOpen={onOpen} saving={isLoading} error={err}
      onSave={async () => { const e = await attempt(() => update(m).unwrap(), 'Saved'); if (e) setErr(e); else onClose() }}>
      {m.options.map((o, i) => (
        <div key={i} className="grid grid-cols-[5rem_1fr_auto] items-end gap-2">
          <Field label="Key" htmlFor={`fk${i}`} error={p(`options[${i}].key`)}>
            <select id={`fk${i}`} className="input" value={o.key} onChange={(e) => { const v = e.target.value; setM((m0) => ({ ...m0, options: m0.options.map((x, j) => (j === i ? { ...x, key: v } : x)) })) }}>
              {'1234567890*#'.split('').filter((k) => k === o.key || !used.has(k)).map((k) => <option key={k}>{k}</option>)}
            </select>
          </Field>
          <DestinationPicker label={`Key ${o.key} goes to`} required value={o.destination} error={p(`options[${i}].destination`)}
            onChange={(d) => d && setM((m0) => ({ ...m0, options: m0.options.map((x, j) => (j === i ? { ...x, destination: d } : x)) }))} />
          <button type="button" className="btn mb-0.5" aria-label={`Remove key ${o.key}`} onClick={() => setM((m0) => ({ ...m0, options: m0.options.filter((_, j) => j !== i) }))}>
            <Trash2 size={15} className="text-rose-300" aria-hidden />
          </button>
        </div>
      ))}
      {free && (
        <button type="button" className="btn" onClick={() => setM((m0) => ({ ...m0, options: [...m0.options, { key: free, label: '', destination: { type: 'hangup', target: '' } }] }))}>
          <Plus size={15} className="text-emerald-300" aria-hidden /> Add key {free}
        </button>
      )}
      <DestinationPicker label="If no key is pressed" value={m.timeout ?? null} emptyLabel="Hang up"
        onChange={(d) => setM((m0) => ({ ...m0, timeout: d }))} error={p('timeout')} />
    </FlowDrawer>
  )
}

/** NewRoute adds an incoming route from the diagram. */
export function NewRoute({ onClose }: { onClose: () => void }) {
  const [create, { isLoading }] = useCreateInboundMutation()
  const { data: trunks } = useTrunksQuery()
  const [r, setR] = useState<Partial<InboundRoute>>({ name: '', did: '', trunk: '', destination: undefined })
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const p = (f: string) => problemFor(err?.problems, f)
  return (
    <Drawer open onClose={onClose} title="New incoming route" icon={<Workflow size={18} className="text-amber-300" aria-hidden />} saving={isLoading} error={err}
      onSave={async () => { const e = await attempt(() => create(r).unwrap(), 'Route added'); if (e) setErr(e); else onClose() }}>
      <Field label="Name" htmlFor="nr-n" error={p('name')}><input id="nr-n" className="input" autoFocus value={r.name} onChange={(e) => { const v = e.target.value; setR((r0) => ({ ...r0, name: v })) }} /></Field>
      <Field label="Number called" htmlFor="nr-d" error={p('did')} hint="Empty: any number not matched by another route.">
        <input id="nr-d" className="input font-mono" value={r.did} onChange={(e) => { const v = e.target.value; setR((r0) => ({ ...r0, did: v })) }} />
      </Field>
      <Field label="On trunk" htmlFor="nr-t" error={p('trunk')}>
        <select id="nr-t" className="input" value={r.trunk} onChange={(e) => { const v = e.target.value; setR((r0) => ({ ...r0, trunk: v })) }}>
          <option value="">Any trunk</option>
          {trunks?.items.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
        </select>
      </Field>
      <DestinationPicker label="Calls go to" required value={r.destination ?? null} onChange={(d) => setR((r0) => ({ ...r0, destination: d ?? undefined }))} error={p('destination')} />
    </Drawer>
  )
}
