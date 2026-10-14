import { createColumnHelper } from '@tanstack/react-table'
import { AwayBadge, CalendarFields } from './Away'
import { clsx } from 'clsx'
import { Cable, DoorOpen, Eye, EyeOff, MapPin, Megaphone, Phone, PhoneForwarded, Plus, RefreshCw, Voicemail, Wifi, WifiOff, X } from 'lucide-react'
import { useEffect, useId, useMemo, useState, type Dispatch, type SetStateAction } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import {
  useCreateExtensionMutation, useDeleteExtensionMutation, useExtensionsQuery, useFaxBoxesQuery, useLazyNextExtensionQuery,
  useMenusQuery, useQueuesQuery, useScriptsQuery, useRegistrationsQuery, useSchedulesQuery, useRingGroupsQuery, useSystemQuery, useUpdateExtensionMutation, type ApiErrorBody,
  type Destination, type Extension, useReceptionistsQuery, useSitesQuery, useRemoteAddressQuery,
  useConferencesQuery, useDivertQuery, useSetDivertMutation, type Divert, type FollowMe, type SpeedDial,
  usePromptsQuery, useSettingsQuery, useDirectoriesQuery, useDialInsQuery, useSaveNameAudioMutation, useDeleteNameAudioMutation, useVoicemailQuery,
} from './api'
import { MobileDevices } from './Mobile'
import { useOpenParam } from './useOpenParam'
import { attempt, DeleteButton, Drawer, Table, useIsAdmin } from './Editor'
import { AudioRecording, Greeting } from './Greeting'
import { Copyable, PhoneHelp } from './Setup'
import { Badge, Chip, Empty, Field, Page, Toggle, problemFor, relTime } from './ui'

const blank: Extension = {
  id: '', number: '', name: '', email: '', secret: '', enabled: true, location: '', callerId: '', ringSeconds: 25,
  noAnswer: null, outbound: true, voicemail: { enabled: true, pin: '', maxSeconds: 180, email: false }, record: false, secureMedia: false,
  kind: '', door: { code: '*1', url: '', method: 'GET', username: '', password: '' }, site: '', floor: '', room: '',
}

/** What can be connected to an extension, with what it means. */
export const deviceKinds = [
  { id: '', label: 'Phone', icon: Phone, color: 'text-sky-300', detail: 'A desk phone, softphone or mobile app.' },
  { id: 'door', label: 'Door station', icon: DoorOpen, color: 'text-amber-300', detail: 'An intercom at a door. Whoever answers can open the door with a key code or the Open door button.' },
  { id: 'speaker', label: 'Overhead speaker', icon: Megaphone, color: 'text-orange-300', detail: 'A paging speaker or adapter. Calls to it are answered at once and heard over the speaker.' },
  { id: 'analog', label: 'Analog adapter port', icon: Cable, color: 'text-teal-300', detail: 'An old phone, fax or alarm panel on an adapter. Calls use standard G.711 audio, which faxes need.' },
] as const

const col = createColumnHelper<Extension & { online: number; agent: string }>()

export function Extensions() {
  const admin = useIsAdmin()
  const { data } = useExtensionsQuery()
  const { data: regs } = useRegistrationsQuery()
  const [edit, setEdit] = useState<Extension | null>(null)
  useOpenParam(data?.items, setEdit)
  const [params, setParams] = useSearchParams()
  const [next] = useLazyNextExtensionQuery()

  const add = async () => {
    const n = await next().unwrap().catch(() => ({ number: '', secret: '' }))
    setEdit({ ...blank, number: n.number, secret: n.secret })
  }
  useEffect(() => {
    if (params.get('new') && admin) { add(); setParams({}, { replace: true }) }
  }, [params, admin]) // eslint-disable-line react-hooks/exhaustive-deps

  const rows = useMemo(() => (data?.items ?? []).map((x) => {
    const r = regs?.items.filter((b) => b.extension === x.number) ?? []
    return { ...x, online: r.length, agent: r[0]?.userAgent ?? '' }
  }), [data, regs])

  const columns = [
    col.accessor('number', { header: 'Extension', cell: (c) => <Chip kind="extension">{c.getValue()}</Chip> }),
    col.accessor('name', { header: 'Name', cell: (c) => {
      const k = deviceKinds.find((d) => d.id === (c.row.original.kind ?? ''))
      return <span className="inline-flex flex-wrap items-center gap-2 font-medium text-slate-100">{c.getValue()}{k && k.id && <span className={`inline-flex items-center gap-1 text-xs ${k.color}`}><k.icon size={12} aria-hidden />{k.label}</span>}<AwayBadge ext={c.row.original.number} /></span>
    } }),
    col.accessor('online', {
      header: 'Phone', cell: (c) => !c.row.original.enabled ? <Badge status="idle">Switched off</Badge>
        : c.getValue() ? <Badge status="ok"><Wifi size={11} aria-hidden /> {c.getValue() > 1 ? `${c.getValue()} devices` : 'Connected'}</Badge>
          : <Badge status="warn"><WifiOff size={11} aria-hidden /> Not connected</Badge>,
    }),
    col.accessor('agent', { header: 'Device', cell: (c) => <span className="text-xs text-violet-300">{c.getValue()}</span> }),
    col.accessor('location', {
      header: '911 location', cell: (c) => c.getValue()
        ? <span className="inline-flex items-center gap-1 text-xs text-emerald-300"><MapPin size={12} aria-hidden />{c.getValue()}</span>
        : <span className="text-xs text-amber-300">Not set</span>,
    }),
  ]

  return (
    <Page icon={Phone} title="Extensions" intro="Each person or desk gets an extension number. Connect a desk phone, softphone or mobile app to it."
      actions={admin && <button className="btn btn-primary" onClick={add}><Plus size={15} aria-hidden /> Add extension</button>}>
      <Table data={rows} columns={columns} onRow={(r) => setEdit(data!.items.find((x) => x.id === r.id)!)}
        empty={<Empty icon={Phone} title="No extensions yet">Add one for each person who needs a phone.</Empty>} />
      {edit && <ExtensionEditor ext={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function ExtensionEditor({ ext, onClose }: { ext: Extension; onClose: () => void }) {
  const admin = useIsAdmin()
  const [x, setX] = useState(ext)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [show, setShow] = useState(false)
  const [create, c] = useCreateExtensionMutation()
  const [update, u] = useUpdateExtensionMutation()
  const [remove] = useDeleteExtensionMutation()
  const [next] = useLazyNextExtensionQuery()
  const { data: sys } = useSystemQuery()
  const { data: regs } = useRegistrationsQuery()
  const isNew = !ext.id
  const set = <K extends keyof Extension>(k: K, v: Extension[K]) => setX((x0) => ({ ...x0, [k]: v }))
  const p = (f: string) => problemFor(err?.problems, f)

  const save = async () => {
    // A new extension with voicemail sends unanswered calls to it unless something else was chosen.
    const body = isNew && x.voicemail.enabled && !x.noAnswer ? { ...x, noAnswer: { type: 'voicemail' as const, target: x.number } } : x
    const e = await attempt(() => (isNew ? create(body) : update(body)).unwrap(), isNew ? `Extension ${x.number} added` : `Extension ${x.number} saved`)
    setErr(e)
    if (!e) onClose()
  }
  const devices = regs?.items.filter((b) => b.extension === ext.number) ?? []

  return (
    <Drawer open onClose={onClose} title={isNew ? 'New extension' : `Extension ${ext.number}`} icon={<Phone className="text-sky-300" aria-hidden />}
      onSave={admin ? save : undefined} saving={c.isLoading || u.isLoading} error={err}
      footer={admin && !isNew && <DeleteButton what={`extension ${ext.number}`} onDelete={async () => {
        if (!await attempt(() => remove(ext.id).unwrap(), `Extension ${ext.number} removed`, { problemsInToast: true })) onClose()
      }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="xn" error={p('number')}>
            <input id="xn" className="input font-mono text-sky-300" value={x.number} aria-invalid={!!p('number')} onChange={(e) => set('number', e.target.value.replace(/\D/g, ''))} />
          </Field>
          <Field label="Name" htmlFor="xm" error={p('name')}>
            <input id="xm" className="input" autoFocus={isNew} value={x.name} onChange={(e) => set('name', e.target.value)} />
          </Field>
        </div>
        <Field label="Connected device" htmlFor="xk" hint={deviceKinds.find((d) => d.id === (x.kind ?? ''))?.detail}>
          <select id="xk" className="input" value={x.kind ?? ''} onChange={(e) => set('kind', e.target.value as Extension['kind'])}>
            {deviceKinds.map((d) => <option key={d.id} value={d.id}>{d.label}</option>)}
          </select>
        </Field>
        {!isNew && (x.kind ?? '') === '' && <MobileDevices ext={x.number} admin={admin} />}
        {x.kind === 'door' && <DoorSettings door={x.door ?? blank.door} onChange={(v) => setX((x0) => ({ ...x0, door: { ...(x0.door ?? blank.door), ...v } }))} p={p} />}
        <LocationFields x={x} set={set} p={p} />
        <Field label="Email" htmlFor="xe" error={p('email')} hint="For voicemail and call notifications.">
          <input id="xe" type="email" className="input" value={x.email} onChange={(e) => set('email', e.target.value)} />
        </Field>
        <DepartmentField value={x.department ?? ''} onChange={(v) => set('department', v)} />
        {(x.kind ?? '') === '' && !x.hotDesk && <CalendarFields x={x} set={(c) => set('calendar', c)} p={p} isNew={isNew} />}
        {admin && (
          <Field label="SIP password" htmlFor="xs" error={p('secret')} hint="Generated for you. Type it into the phone along with the extension number.">
            <div className="flex gap-2">
              <input id="xs" className="input font-mono text-amber-300" type={show ? 'text' : 'password'} value={x.secret} onChange={(e) => set('secret', e.target.value)} />
              <button type="button" className="btn" aria-label={show ? 'Hide password' : 'Show password'} onClick={() => setShow(!show)}>{show ? <EyeOff size={15} aria-hidden /> : <Eye size={15} aria-hidden />}</button>
              <button type="button" className="btn" aria-label="Generate a new password" title="Generate a new password"
                onClick={async () => { const n = await next().unwrap(); set('secret', n.secret); setShow(true) }}><RefreshCw size={15} className="text-emerald-300" aria-hidden /></button>
            </div>
          </Field>
        )}
        <div className="grid grid-cols-2 gap-4">
          <Field label="Ring for (seconds)" htmlFor="xr" error={p('ringSeconds')}>
            <input id="xr" type="number" min={5} max={300} className="input" value={x.ringSeconds} onChange={(e) => set('ringSeconds', Number(e.target.value))} />
          </Field>
          <Field label="Outside caller ID" htmlFor="xc" error={p('callerId')} hint="Number shown on outbound calls. Empty uses the trunk's.">
            <input id="xc" className="input font-mono" value={x.callerId} onChange={(e) => set('callerId', e.target.value)} />
          </Field>
        </div>
        <DestinationPicker label="If nobody answers" value={x.noAnswer ?? null} onChange={(d) => set('noAnswer', d)} exclude={x.number} error={p('noAnswer')} />
        <div className="space-y-3">
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={x.enabled} onChange={(v) => set('enabled', v)} label="Enabled" /> Enabled</label>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={x.outbound} onChange={(v) => set('outbound', v)} label="Can call outside numbers" /> Can call outside numbers</label>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={x.record} onChange={(v) => set('record', v)} label="Record calls" /> Record calls <span className="text-xs text-slate-500">(when Settings records selected extensions)</span></label>
          {(x.kind ?? '') === '' && <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={x.hotDesk ?? false} onChange={(v) => set('hotDesk', v)} label="Hot desk" /> Hot desk <span className="text-xs text-slate-500">(a shared phone that becomes whoever signs in at it; see Phones)</span></label>}
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={x.secureMedia} onChange={(v) => set('secureMedia', v)} label="Encrypt calls to this phone" /> Encrypt calls to this phone (SRTP) <span className="text-xs text-slate-500">(the phone must support it; use TLS so the keys stay private)</span></label>
        </div>
        {(x.kind ?? '') === '' && <FollowSettings x={x} setX={setX} p={p} />}
        <section className="card space-y-4 border-violet-900/50 p-4">
          <label className="flex items-center gap-3 text-sm font-semibold text-slate-100">
            <Toggle checked={x.voicemail.enabled} label="Voicemail" onChange={(v) => setX({ ...x, voicemail: { ...x.voicemail, enabled: v },
              noAnswer: v && !x.noAnswer ? { type: 'voicemail', target: x.number } : !v && x.noAnswer?.type === 'voicemail' && x.noAnswer.target === x.number ? null : x.noAnswer })} />
            <Voicemail size={16} className="text-violet-300" aria-hidden /> Voicemail
          </label>
          {x.voicemail.enabled && (
            <div className="grid grid-cols-2 gap-4">
              <Field label="Longest message (seconds)" htmlFor="vm" error={p('voicemail.maxSeconds')}>
                <input id="vm" type="number" min={10} max={900} className="input" value={x.voicemail.maxSeconds} onChange={(e) => { const v = { ...x.voicemail, maxSeconds: Number(e.target.value) }; setX((x0) => ({ ...x0, voicemail: v })) }} />
              </Field>
              <Field label="PIN" htmlFor="vp" error={p('voicemail.pin')} hint={isNew ? 'Optional, 4 to 10 digits.' : 'Leave empty to keep the current one.'}>
                <input id="vp" inputMode="numeric" className="input font-mono" value={x.voicemail.pin} onChange={(e) => { const v = { ...x.voicemail, pin: e.target.value.replace(/\D/g, '') }; setX((x0) => ({ ...x0, voicemail: v })) }} />
              </Field>
            </div>
          )}
          {x.voicemail.enabled && (
            <label className="flex items-center gap-3 text-sm text-slate-300">
              <Toggle checked={x.voicemail.email} label="Email new messages" onChange={(v) => setX({ ...x, voicemail: { ...x.voicemail, email: v } })} />
              Email new messages to {x.email ? <span className="text-sky-300">{x.email}</span> : <span className="text-amber-300">the email address above (none yet)</span>}
            </label>
          )}
          {x.voicemail.enabled && !isNew && <Greeting box={ext.number} />}
        </section>
      </fieldset>
      {!isNew && (ext.kind ?? '') === '' && <ForwardingNow ext={ext.number} />}
      {!isNew && (
        <div className="space-y-3">
          <h3 className="label">Connected devices</h3>
          {devices.length ? devices.map((d) => (
            <div key={d.contact} className="card flex items-center gap-3 p-3 text-sm">
              <Wifi size={16} className="text-emerald-300" aria-hidden />
              <div className="min-w-0 flex-1">
                <div className="truncate text-violet-300">{d.userAgent || 'Unknown device'}</div>
                <div className="font-mono text-xs text-slate-500">{d.source} · {d.transport} · since {relTime(d.since)}</div>
              </div>
            </div>
          )) : <p className="text-sm text-amber-300">No phone is connected to this extension yet.</p>}
          <PhoneHelp addresses={sys?.addresses ?? []} port={sys?.sipPort ?? 5060} tlsPort={sys?.tlsPort} />
          <p className="text-xs text-slate-500">Username <Copyable text={ext.number} /></p>
          <p className="text-xs text-slate-500">No desk phone? Open <a className="text-emerald-300 underline" href="#/phone" target="_blank" rel="noreferrer">the browser phone</a> and sign in with the extension number and SIP password.</p>
        </div>
      )}
    </Drawer>
  )
}

export function DestinationPicker({ label, value, onChange, exclude, required, noSchedules, emptyLabel, error }: {
  label: string; value: Destination | null; onChange: (d: Destination | null) => void; exclude?: string; required?: boolean; noSchedules?: boolean
  emptyLabel?: string; error?: string
}) {
  const id = useId()
  const { data: exts } = useExtensionsQuery()
  const { data: groups } = useRingGroupsQuery()
  const { data: queues } = useQueuesQuery()
  const { data: schedules } = useSchedulesQuery()
  const { data: menus } = useMenusQuery()
  const { data: scripts } = useScriptsQuery()
  const { data: receptionists } = useReceptionistsQuery()
  const { data: faxes } = useFaxBoxesQuery()
  const { data: rooms } = useConferencesQuery()
  const { data: dirs } = useDirectoriesQuery()
  const { data: dialIns } = useDialInsQuery()
  const key = value ? `${value.type}:${value.target}` : ''
  return (
    <Field label={label} htmlFor={id} error={error}>
      <select id={id} className="input" value={key} onChange={(e) => {
        const v = e.target.value
        if (!v) return onChange(null)
        const [type, target] = v.split(':')
        onChange({ type: type as Destination['type'], target: target ?? '' })
      }}>
        {!required && <option value="">{emptyLabel ?? 'Keep ringing until the caller gives up'}</option>}
        {required && !value && <option value="">Choose…</option>}
        <optgroup label="Extensions">
          {exts?.items.filter((x) => x.number !== exclude).map((x) => <option key={x.id} value={`extension:${x.number}`}>{x.number} {x.name}</option>)}
        </optgroup>
        <optgroup label="Ring groups">
          {groups?.items.filter((g) => g.number !== exclude).map((g) => <option key={g.id} value={`ringgroup:${g.number}`}>{g.number} {g.name}</option>)}
        </optgroup>
        <optgroup label="Queues">
          {queues?.items.filter((q) => q.number !== exclude).map((q) => <option key={q.id} value={`queue:${q.number}`}>{q.number} {q.name}</option>)}
        </optgroup>
        <optgroup label="Menus">
          {menus?.items.map((m) => <option key={m.id} value={`menu:${m.id}`}>{m.name}{m.number ? ` (${m.number})` : ''}</option>)}
        </optgroup>
        {!noSchedules && (
          <optgroup label="Schedules">
            {schedules?.items.map((s) => <option key={s.id} value={`schedule:${s.id}`}>{s.name} (open or closed)</option>)}
          </optgroup>
        )}
        {!noSchedules && (
          <optgroup label="Scripts">
            {scripts?.items.map((s) => <option key={s.id} value={`script:${s.id}`}>{s.name} (script)</option>)}
          </optgroup>
        )}
        {(receptionists?.items.length ?? 0) > 0 && !noSchedules && (
          <optgroup label="AI receptionists">
            {receptionists!.items.map((r) => <option key={r.id} value={`receptionist:${r.id}`}>{r.name} (AI receptionist)</option>)}
          </optgroup>
        )}
        <optgroup label="Voicemail">
          {exts?.items.filter((x) => x.voicemail?.enabled).map((x) => <option key={x.id} value={`voicemail:${x.number}`}>Voicemail of {x.number} {x.name}</option>)}
        </optgroup>
        {(faxes?.items.length ?? 0) > 0 && (
          <optgroup label="Fax">
            {faxes!.items.map((f) => <option key={f.id} value={`fax:${f.number}`}>{f.number} {f.name} (fax to PDF)</option>)}
          </optgroup>
        )}
        {((dirs?.items.length ?? 0) > 0 || (dialIns?.items.length ?? 0) > 0) && (
          <optgroup label="Directories and dial-in">
            {dirs?.items.map((d) => <option key={d.id} value={`directory:${d.id}`}>{d.name} (dial by name)</option>)}
            {dialIns?.items.map((d) => <option key={d.id} value={`dialin:${d.id}`}>{d.name} (dial-in)</option>)}
          </optgroup>
        )}
        {(rooms?.items.length ?? 0) > 0 && (
          <optgroup label="Conference rooms">
            {rooms!.items.map((r) => <option key={r.id} value={`conference:${r.number}`}>{r.number} {r.name} (conference room)</option>)}
          </optgroup>
        )}
        <optgroup label="Other">
          <option value="esrp:">ESRP: the PSAP for the caller's location (NG911 core)</option>
          <option value="echo:">Echo test</option>
          <option value="hangup:">Hang up</option>
        </optgroup>
      </select>
    </Field>
  )
}

function DoorSettings({ door, onChange, p }: { door: Extension['door']; onChange: (v: Partial<Extension['door']>) => void; p: (f: string) => string | undefined }) {
  return (
    <section className="card space-y-3 border-amber-900/50 p-4" aria-labelledby="door-h">
      <h3 id="door-h" className="flex items-center gap-2 text-sm font-semibold text-amber-300"><DoorOpen size={15} aria-hidden /> Opening the door</h3>
      <div className="grid gap-3 sm:grid-cols-[8rem_1fr]">
        <Field label="Key code" htmlFor="dc" error={p('door.code')} hint="Pressed during the call.">
          <input id="dc" className="input font-mono" value={door.code} onChange={(e) => onChange({ code: e.target.value.replace(/[^0-9*#]/g, '') })} maxLength={6} />
        </Field>
        <Field label="Door controller address" htmlFor="du" error={p('door.url')} hint="Optional. Empty: the door station hears the code and opens the door itself.">
          <input id="du" className="input font-mono" placeholder="http://10.0.0.20/api/switch/ctrl?switch=1&action=trigger" value={door.url} onChange={(e) => onChange({ url: e.target.value.trim() })} />
        </Field>
      </div>
      {door.url && (
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="Method" htmlFor="dm" error={p('door.method')}>
            <select id="dm" className="input" value={door.method || 'GET'} onChange={(e) => onChange({ method: e.target.value })}>{['GET', 'POST', 'PUT'].map((m) => <option key={m}>{m}</option>)}</select>
          </Field>
          <Field label="Username" htmlFor="dun"><input id="dun" className="input" autoComplete="off" value={door.username} onChange={(e) => onChange({ username: e.target.value })} /></Field>
          <Field label="Password" htmlFor="dpw"><input id="dpw" type="password" className="input" autoComplete="off" value={door.password} onChange={(e) => onChange({ password: e.target.value })} /></Field>
        </div>
      )}
    </section>
  )
}

/** Where the extension is for 911: its site, floor and room, and any detail; and a remote worker's address. */
function LocationFields({ x, set, p }: { x: Extension; set: <K extends keyof Extension>(k: K, v: Extension[K]) => void; p: (f: string) => string | undefined }) {
  const { data: sites } = useSitesQuery()
  const { data: remote } = useRemoteAddressQuery(x.number, { skip: !x.id })
  return (
    <fieldset className="space-y-3 rounded-lg border border-slate-800 p-3">
      <legend className="flex items-center gap-1.5 px-1 text-sm font-medium text-rose-300"><MapPin size={14} aria-hidden /> 911 location</legend>
      <div className="grid gap-3 sm:grid-cols-[1fr_6rem_6rem]">
        <Field label="Site" htmlFor="xsite" error={p('site')} hint={sites?.items.length ? undefined : <>Add your building under <Link className="text-sky-300 underline" to="/sites">Sites</Link>.</>}>
          <select id="xsite" className="input" value={x.site ?? ''} onChange={(e) => set('site', e.target.value)}>
            <option value="">None</option>
            {sites?.items.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        </Field>
        <Field label="Floor" htmlFor="xfl"><input id="xfl" className="input" value={x.floor ?? ''} onChange={(e) => set('floor', e.target.value)} /></Field>
        <Field label="Room" htmlFor="xrm"><input id="xrm" className="input" value={x.room ?? ''} onChange={(e) => set('room', e.target.value)} /></Field>
      </div>
      <Field label="More detail" htmlFor="xl" error={p('location')} hint="Wing, desk or landmark. Sent with emergency calls and shown to on-site staff.">
        <input id="xl" className="input" value={x.location} onChange={(e) => set('location', e.target.value)} placeholder="East wing, by the lifts" />
      </Field>
      {remote?.address && (
        <p className="text-xs text-amber-300">Working remotely: {remote.address.number} {remote.address.street}, {remote.address.city} (confirmed {relTime(remote.confirmed)}). Used for emergency calls when the phone is away from its site.</p>
      )}
    </fieldset>
  )
}

/** useDescribe names a destination the way people say it: "Extension 101 Front desk", "Menu Main". */
export function useDescribe() {
  const { data: exts } = useExtensionsQuery()
  const { data: groups } = useRingGroupsQuery()
  const { data: queues } = useQueuesQuery()
  const { data: menus } = useMenusQuery()
  const { data: schedules } = useSchedulesQuery()
  const { data: scripts } = useScriptsQuery()
  const { data: receptionists } = useReceptionistsQuery()
  return (d: Destination | null | undefined) => {
    if (!d) return 'Nowhere'
    const t = d.target
    switch (d.type) {
      case 'extension': return `Extension ${t} ${exts?.items.find((x) => x.number === t)?.name ?? ''}`.trim()
      case 'ringgroup': return `Ring group ${t} ${groups?.items.find((x) => x.number === t)?.name ?? ''}`.trim()
      case 'queue': return `Queue ${t} ${queues?.items.find((x) => x.number === t)?.name ?? ''}`.trim()
      case 'voicemail': return `Voicemail of ${t}`
      case 'menu': return `Menu ${menus?.items.find((x) => x.id === t)?.name ?? ''}`.trim()
      case 'schedule': return `Schedule ${schedules?.items.find((x) => x.id === t)?.name ?? ''}`.trim()
      case 'script': return `Script ${scripts?.items.find((x) => x.id === t)?.name ?? ''}`.trim()
      case 'receptionist': return `AI receptionist ${receptionists?.items.find((x) => x.id === t)?.name ?? ''}`.trim()
      case 'conference': return `Conference room ${t}`
      case 'directory': return 'Dial-by-name directory'
      case 'dialin': return 'Dial-in'
      case 'echo': return 'Echo test'
    }
    return 'Hang up'
  }
}

/** The extension's department, for call costs; picks from the departments already in use. */
function DepartmentField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { data } = useExtensionsQuery()
  const used = [...new Set((data?.items ?? []).map((e) => e.department ?? '').filter(Boolean))].sort()
  return (
    <Field label="Department" htmlFor="xdep" hint="Groups this extension's call costs on Costs, and lets a budget cover it.">
      <input id="xdep" className="input" list="xdep-list" value={value} onChange={(e) => onChange(e.target.value)} placeholder="Sales" />
      <datalist id="xdep-list">{used.map((d) => <option key={d} value={d} />)}</datalist>
    </Field>
  )
}

/** Follow-me, the pickup group, intercom and the person's own speed dials: configuration, kept with the extension. */
function FollowSettings({ x, setX, p }: { x: Extension; setX: Dispatch<SetStateAction<Extension>>; p: (f: string) => string | undefined }) {
  const fm: FollowMe = { enabled: false, numbers: [], strategy: 'sequential', ringSeconds: 20, confirm: true, ...(x.followMe ?? {}) }
  const nums = fm.numbers ?? []
  const setFm = (v: Partial<FollowMe>) => setX((x0) => ({ ...x0, followMe: { ...fm, ...(x0.followMe ?? {}), ...v } }))
  const sds = x.speedDials ?? []
  const setSds = (v: SpeedDial[]) => setX((x0) => ({ ...x0, speedDials: v }))
  return (
    <section className="card space-y-4 border-teal-900/50 p-4" aria-labelledby="follow-h">
      <h3 id="follow-h" className="flex items-center gap-2 text-sm font-semibold text-teal-300"><PhoneForwarded size={15} aria-hidden /> Follow-me, pickup and intercom</h3>
      <label className="flex items-center gap-3 text-sm text-slate-300">
        <Toggle checked={fm.enabled} onChange={(v) => setFm({ enabled: v, numbers: v && !nums.length ? [''] : nums })} label="Follow-me" />
        Follow-me <span className="text-xs text-slate-500">(when the phones here are not answered, ring these numbers before {x.noAnswer ? 'the no-answer destination' : 'giving up'})</span>
      </label>
      {fm.enabled && (
        <div className="space-y-3 pl-1">
          <ol className="space-y-2">
            {nums.map((n, i) => (
              <li key={i} className="flex items-center gap-2">
                <span className="w-5 text-right font-mono text-xs text-slate-500">{i + 1}.</span>
                <input aria-label={`Follow-me number ${i + 1}`} className="input max-w-xs font-mono text-teal-300" placeholder="+1 212 555 0199 or 105" value={n}
                  onChange={(e) => { const v = [...nums]; v[i] = e.target.value; setFm({ numbers: v }) }} />
                <button type="button" className="btn btn-ghost px-2" aria-label={`Remove number ${i + 1}`} onClick={() => setFm({ numbers: nums.filter((_, j) => j !== i) })}><X size={14} aria-hidden /></button>
                {p(`followMe.numbers[${i}]`) && <span className="text-xs text-rose-300">{p(`followMe.numbers[${i}]`)}</span>}
              </li>
            ))}
          </ol>
          {p('followMe.numbers') && <p className="text-xs text-rose-300">{p('followMe.numbers')}</p>}
          {nums.length < 10 && <button type="button" className="btn btn-ghost text-sm" onClick={() => setFm({ numbers: [...nums, ''] })}><Plus size={14} aria-hidden /> Add a number</button>}
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="How they ring" htmlFor="fms">
              <select id="fms" className="input" value={fm.strategy || 'sequential'} onChange={(e) => setFm({ strategy: e.target.value as FollowMe['strategy'] })}>
                <option value="sequential">One after another</option>
                <option value="together">All together</option>
              </select>
            </Field>
            <Field label="Ring each for (seconds)" htmlFor="fmr" error={p('followMe.ringSeconds')}>
              <input id="fmr" type="number" min={5} max={120} className="input" value={fm.ringSeconds} onChange={(e) => setFm({ ringSeconds: Number(e.target.value) })} />
            </Field>
          </div>
          <label className="flex items-center gap-3 text-sm text-slate-300">
            <Toggle checked={fm.confirm} onChange={(v) => setFm({ confirm: v })} label="Press 1 to take the call" />
            Outside numbers press 1 to take the call <span className="text-xs text-slate-500">(so a mobile's own voicemail never takes it)</span>
          </label>
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Pickup group" htmlFor="xpg" error={p('pickupGroup')} hint="Anyone in the same group answers this extension's ringing calls with the pickup code (*8).">
          <PickupGroupInput value={x.pickupGroup ?? ''} onChange={(v) => setX((x0) => ({ ...x0, pickupGroup: v }))} />
        </Field>
        <label className="flex items-center gap-3 self-center text-sm text-slate-300">
          <Toggle checked={!(x.noIntercom ?? false)} onChange={(v) => setX((x0) => ({ ...x0, noIntercom: !v }))} label="Allow intercom" />
          Allow intercom <span className="text-xs text-slate-500">(*80 then this number answers on the speaker)</span>
        </label>
      </div>
      <SoundFields x={x} setX={setX} p={p} />
      <SpeedDialList label="Own speed dials" hint="Dialled after the speed dial code (*0). They win over the company's with the same code." field="speedDials" list={sds} onChange={setSds} p={p} />
    </section>
  )
}

/** The language this person hears prompts in, and the music they play to someone they put on hold. */
function SoundFields({ x, setX, p }: { x: Extension; setX: Dispatch<SetStateAction<Extension>>; p: (f: string) => string | undefined }) {
  const { data: settings } = useSettingsQuery()
  const { data: langs } = usePromptsQuery()
  const { data: vm } = useVoicemailQuery()
  const [saveName, { isLoading }] = useSaveNameAudioMutation()
  const [deleteName] = useDeleteNameAudioMutation()
  const music = settings?.system.music ?? []
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <label className="flex items-center gap-3 text-sm text-slate-300 sm:col-span-2">
        <Toggle checked={x.guestRoom ?? false} onChange={(v) => setX((x0) => ({ ...x0, guestRoom: v }))} label="Guest room" />
        Guest room <span className="text-xs text-slate-500">(a hotel room's phone: checked in and out on Hotel, or by the hotel system)</span>
      </label>
      {x.id && x.number && (
        <div className="sm:col-span-2">
          <AudioRecording label="Spoken name" missing="No name recorded: the directory reads the number out." has={!!vm?.greetings?.includes(`name-${x.number}`)}
            src={`./api/extensions/${x.number}/name-audio`} busy={isLoading} saved="Name saved"
            save={(wav) => saveName({ ext: x.number, wav }).unwrap()} reset={() => deleteName(x.number).unwrap()} />
        </div>
      )}
      <Field label="Prompt language" htmlFor="xla" error={p('language')} hint="Wake-up calls and other prompts this phone hears.">
        <select id="xla" className="input" value={x.language ?? ''} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, language: v })) }}>
          <option value="">The system's</option>
          {langs?.languages.map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
        </select>
      </Field>
      <Field label="Hold music" htmlFor="xhm" error={p('holdMusic')} hint="What someone hears when this person puts them on hold or parks them.">
        <select id="xhm" className="input" value={x.holdMusic ?? ''} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, holdMusic: v })) }}>
          <option value="">The system's ({music.find((m) => m.id === settings?.system.holdMusic)?.name ?? 'tone'})</option>
          <option value="tone">The hold tone</option>
          {music.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
        </select>
      </Field>
    </div>
  )
}

function PickupGroupInput({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { data } = useExtensionsQuery()
  const used = [...new Set((data?.items ?? []).map((e) => e.pickupGroup ?? '').filter(Boolean))].sort()
  return (
    <>
      <input id="xpg" className="input" list="xpg-list" value={value} onChange={(e) => onChange(e.target.value)} placeholder="Front office" />
      <datalist id="xpg-list">{used.map((d) => <option key={d} value={d} />)}</datalist>
    </>
  )
}

/** A list of speed dials, editable in place. */
export function SpeedDialList({ label, hint, field, list, onChange, p }: {
  label: string; hint: string; field: string; list: SpeedDial[]; onChange: (v: SpeedDial[]) => void; p: (f: string) => string | undefined
}) {
  const set = (i: number, v: Partial<SpeedDial>) => onChange(list.map((s, j) => (j === i ? { ...s, ...v } : s)))
  return (
    <div className="space-y-2">
      <div className="label">{label}</div>
      <p className="text-xs text-slate-500">{hint}</p>
      {list.length > 0 && (
        <table className="w-full text-sm">
          <thead><tr className="text-left text-xs text-slate-500"><th className="w-24 pb-1 font-normal">Code</th><th className="pb-1 font-normal">Number</th><th className="pb-1 font-normal">Name</th><th className="w-8" /></tr></thead>
          <tbody>
            {list.map((s, i) => (
              <tr key={i}>
                <td className="py-1 pr-2"><input aria-label={`Speed dial ${i + 1} code`} className="input font-mono text-amber-300" inputMode="numeric" maxLength={3} value={s.code} onChange={(e) => set(i, { code: e.target.value.replace(/\D/g, '') })} /></td>
                <td className="py-1 pr-2"><input aria-label={`Speed dial ${i + 1} number`} className="input font-mono text-teal-300" value={s.number} onChange={(e) => set(i, { number: e.target.value })} /></td>
                <td className="py-1 pr-2"><input aria-label={`Speed dial ${i + 1} name`} className="input" value={s.name} onChange={(e) => set(i, { name: e.target.value })} /></td>
                <td><button type="button" className="btn btn-ghost px-2" aria-label={`Remove speed dial ${i + 1}`} onClick={() => onChange(list.filter((_, j) => j !== i))}><X size={14} aria-hidden /></button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {list.map((_, i) => ['code', 'number'].map((f) => p(`${field}[${i}].${f}`) && <p key={`${i}${f}`} className="text-xs text-rose-300">Speed dial {i + 1}: {p(`${field}[${i}].${f}`)}</p>))}
      <button type="button" className="btn btn-ghost text-sm" onClick={() => {
        const used = new Set(list.map((s) => s.code))
        let n = 1
        while (used.has(String(n))) n++
        onChange([...list, { code: String(n), number: '', name: '' }])
      }}><Plus size={14} aria-hidden /> Add a speed dial</button>
    </div>
  )
}

/** Where this extension's calls go right now: forwarding and do not disturb, set from the phone, the portal or here. */
export function ForwardingNow({ ext }: { ext: string }) {
  const admin = useIsAdmin()
  const { data } = useDivertQuery(ext)
  const [save] = useSetDivertMutation()
  const [draft, setDraft] = useState<{ all: string; busy: string; noAnswer: string } | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!data) return null
  const d = draft ?? { all: data.all ?? '', busy: data.busy ?? '', noAnswer: data.noAnswer ?? '' }
  const dirty = draft !== null
  const apply = async (body: Partial<Divert>, msg: string) => {
    const e = await attempt(() => save({ ext, ...body }).unwrap(), msg)
    setErr(e)
    if (!e) setDraft(null)
  }
  return (
    <section className="card space-y-3 border-sky-900/50 p-4" aria-labelledby="fwd-h">
      <h3 id="fwd-h" className="flex items-center gap-2 text-sm font-semibold text-sky-300"><PhoneForwarded size={15} aria-hidden /> Forwarding right now</h3>
      <p className="text-xs text-slate-500">Set from the phone with *72, *90 and *52 (cancel with *73, *91, *53), from the staff portal, or here. Not part of the configuration: changing it makes no revision.</p>
      <label className="flex items-center gap-3 text-sm text-slate-300">
        <Toggle checked={data.dnd} disabled={!admin} onChange={(v) => apply({ dnd: v }, v ? 'Do not disturb on' : 'Do not disturb off')} label="Do not disturb" />
        Do not disturb {data.dnd ? <span className="text-rose-300">on</span> : <span className="text-slate-500">off</span>} <span className="text-xs text-slate-500">(*78 on, *79 off, *76 switches)</span>
      </label>
      <fieldset disabled={!admin} className="grid gap-3 sm:grid-cols-3">
        {([['all', 'Forward every call to'], ['busy', 'When on a call, forward to'], ['noAnswer', 'When not answered, forward to']] as const).map(([k, l]) => (
          <Field key={k} label={l} htmlFor={`fw-${k}`}>
            <input id={`fw-${k}`} className={clsx('input font-mono', d[k] ? 'text-sky-300' : '')} placeholder="Not forwarded" value={d[k]} onChange={(e) => setDraft({ ...d, [k]: e.target.value })} />
          </Field>
        ))}
      </fieldset>
      {err && <p className="text-sm text-rose-300">{err.message}</p>}
      {admin && dirty && <div className="flex gap-2"><button type="button" className="btn btn-primary" onClick={() => apply(d, 'Forwarding saved')}>Save forwarding</button><button type="button" className="btn btn-ghost" onClick={() => { setDraft(null); setErr(null) }}>Cancel</button></div>}
      {data.followMe != null && <p className="text-xs text-teal-300">Follow-me is switched {data.followMeOn ? 'on' : 'off'} by the person (*21).</p>}
    </section>
  )
}
