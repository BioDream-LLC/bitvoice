import { createColumnHelper } from '@tanstack/react-table'
import { TeamsSetup } from './Teams'
import { Eye, EyeOff, Plus, Server } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  useCreateTrunkMutation, useDeleteTrunkMutation, useServersQuery, useTrunkStatusQuery, useTrunksQuery, useUpdateTrunkMutation,
  type ApiErrorBody, type Trunk, type TrunkStatus,
} from './api'
import { CodecList } from './Codecs'
import { RatesEditor } from './Rates'
import { useOpenParam } from './useOpenParam'
import { attempt, DeleteButton, Drawer, Table, useIsAdmin } from './Editor'
import { Badge, Chip, Empty, Field, Page, Toggle, problemFor, relTime, type Status } from './ui'

/** Cloud carriers that connect as plain SIP trunks, with what each needs. */
const cloudTrunks: { name: string; trunk: Partial<Trunk>; note: string }[] = [
  {
    name: 'Microsoft Teams',
    trunk: { name: 'Microsoft Teams', host: 'sip.pstnhub.microsoft.com', port: 5061, transport: 'tls', register: false, username: '', secureMedia: true, teams: true, sbcName: '' },
    note: 'Direct Routing: BitVoice becomes the SBC for your Teams tenant. Enter this server\'s public name below, install a certificate for it from a public authority (Settings, Certificate), then run the PowerShell shown below in your tenant.',
  },
  {
    name: 'Amazon Chime SDK Voice Connector',
    trunk: { name: 'Amazon Chime', host: 'VOICE-CONNECTOR-ID.voiceconnector.chime.aws', port: 5061, transport: 'tls', register: false, username: '', secureMedia: true },
    note: 'Put your Voice Connector\'s outbound host name in Provider server. In the AWS console, turn on termination with this server\'s public address in the allowed hosts, and origination pointing at this server; add the signalling addresses AWS lists for your Region under Also accept calls from.',
  },
  {
    name: 'Azure Communication Services',
    trunk: { name: 'Azure Communication Services', host: 'sip.pstnhub.microsoft.com', port: 5061, transport: 'tls', register: false, username: '',
      allowFrom: ['52.112.0.0/14', '52.120.0.0/14'], secureMedia: true },
    note: 'Direct routing to Azure Communication Services (and Microsoft Teams): add this server as an SBC in Azure with its public name, set From domain to that name, and give BitVoice a certificate for it from a public authority. Microsoft\'s SIP proxies are already allowed.',
  },
]

const blank: Trunk = {
  id: '', name: '', enabled: true, host: '', port: 5060, transport: 'udp', username: '', authUser: '', password: '',
  register: true, fromDomain: '', callerId: '', maxChannels: 0, allowFrom: [], secureMedia: false, codecs: [],
}

export function trunkBadge(s?: TrunkStatus) {
  const map: Record<string, [Status, string]> = {
    registered: ['ok', 'Registered'], reachable: ['ok', 'Reachable'], starting: ['info', 'Connecting'],
    failed: ['fail', 'Failed'], unreachable: ['fail', 'Unreachable'], disabled: ['idle', 'Switched off'],
    standby: ['idle', 'Standby'],
  }
  const [st, label] = map[s?.state ?? 'starting'] ?? ['info', s?.state ?? '']
  return <Badge status={st}>{label}</Badge>
}

const col = createColumnHelper<Trunk & { status?: TrunkStatus }>()

export function Trunks() {
  const admin = useIsAdmin()
  const { data } = useTrunksQuery()
  const { data: status } = useTrunkStatusQuery()
  const [edit, setEdit] = useState<Trunk | null>(null)
  useOpenParam(data?.items, setEdit)
  const [params, setParams] = useSearchParams()
  useEffect(() => { if (params.get('new') && admin) { setEdit(blank); setParams({}, { replace: true }) } }, [params, admin]) // eslint-disable-line react-hooks/exhaustive-deps
  const rows = useMemo(() => (data?.items ?? []).map((t) => ({ ...t, status: status?.items.find((s) => s.id === t.id) })), [data, status])
  const columns = [
    col.accessor('name', { header: 'Trunk', cell: (c) => <Chip kind="trunk">{c.getValue()}</Chip> }),
    col.accessor('host', { header: 'Provider', cell: (c) => <span className="font-mono text-xs text-amber-300">{c.getValue()}:{c.row.original.port}</span> }),
    col.display({ id: 'state', header: 'Status', cell: (c) => trunkBadge(c.row.original.status) }),
    col.display({ id: 'detail', header: 'Detail', cell: (c) => {
      const s = c.row.original.status
      return s?.error ? <span className="text-xs text-rose-300">{s.error}</span>
        : s?.latencyMs ? <span className="text-xs text-emerald-300">{s.latencyMs} ms · since {relTime(s.since)}</span> : null
    } }),
  ]
  return (
    <Page icon={Server} title="Trunks" accent="from-teal-400 to-emerald-400"
      intro="SIP trunks connect BitVoice to the phone network through a provider, for outside calls and 911."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit(blank)}><Plus size={15} aria-hidden /> Add trunk</button>}>
      <Table data={rows} columns={columns} onRow={(r) => setEdit(data!.items.find((t) => t.id === r.id)!)}
        empty={<Empty icon={Server} title="No trunks yet">Your SIP provider gives you a server address, username and password. Add them here to make outside calls.</Empty>} />
      {edit && <Editor t={edit} status={status?.items.find((s) => s.id === edit.id)} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function Editor({ t: initial, status, onClose }: { t: Trunk; status?: TrunkStatus; onClose: () => void }) {
  const admin = useIsAdmin()
  const [t, setT] = useState(initial)
  const [note, setNote] = useState('')
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [show, setShow] = useState(false)
  const [create] = useCreateTrunkMutation()
  const [update] = useUpdateTrunkMutation()
  const [remove] = useDeleteTrunkMutation()
  const { data: servers } = useServersQuery()
  const isNew = !initial.id
  const set = <K extends keyof Trunk>(k: K, v: Trunk[K]) => setT((t0) => ({ ...t0, [k]: v }))
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(t) : update(t)).unwrap(), `Trunk ${t.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New trunk' : t.name} icon={<Server className="text-teal-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Trunk removed', { problemsInToast: true })) onClose() }} />}>
      {!isNew && (
        <div className="card flex items-center gap-3 p-3 text-sm">
          {trunkBadge(status)}
          <span className={status?.error ? 'text-rose-300' : 'text-slate-400'}>{status?.error || (status?.state === 'standby' ? <>Held by <span className="text-teal-300">{status.heldBy || 'another server'}</span>; calls out go through it, and this server takes the line if it stops</> : status?.addresses?.length ? `Provider at ${status.addresses.join(', ')}` : '')}</span>
        </div>
      )}
      <fieldset disabled={!admin} className="space-y-5">
        {isNew && (
          <div className="flex flex-wrap items-center gap-2 text-sm" role="group" aria-label="Start from a cloud provider">
            <span className="text-xs text-slate-500">Start from:</span>
            {cloudTrunks.map((c) => (
              <button type="button" key={c.name} className="btn text-xs" title={c.note} onClick={() => { setT((t0) => ({ ...t0, ...c.trunk })); setNote(c.note) }}>{c.name}</button>
            ))}
          </div>
        )}
        {note && <p className="rounded-lg border border-sky-900 bg-sky-950/40 p-3 text-xs text-sky-200">{note}</p>}
        {t.teams && <TeamsSetup t={t} set={set} p={p} />}
        {!t.teams && !t.esinet && (
          <Field label="Protocol" htmlFor="tproto" error={p('protocol')} hint={t.protocol === 'iax2' ? 'IAX2 is Asterisk\u2019s own protocol, on port 4569: for an Asterisk or FreePBX server, or an IAX2 provider. Leave the server empty when the other server registers here with the username and password.' : undefined}>
            <select id="tproto" className="input" value={t.protocol ?? ''} onChange={(e) => {
              const v = e.target.value as Trunk['protocol']
              setT((t0) => ({ ...t0, protocol: v, port: v === 'iax2' ? (t0.port === 5060 ? 4569 : t0.port) : (t0.port === 4569 ? 5060 : t0.port) }))
            }}>
              <option value="">SIP</option><option value="iax2">IAX2 (Asterisk, FreePBX)</option>
            </select>
          </Field>
        )}
        <Field label="Name" htmlFor="tn" error={p('name')}><input id="tn" className="input" autoFocus={isNew} value={t.name} onChange={(e) => set('name', e.target.value)} placeholder="Main line" /></Field>
        <div className="grid grid-cols-[1fr_6rem_6rem] gap-3">
          <Field label="Provider server" htmlFor="th" error={p('host')}><input id="th" className="input font-mono" value={t.host} onChange={(e) => set('host', e.target.value.trim())} placeholder="sip.provider.com" /></Field>
          <Field label="Port" htmlFor="tp"><input id="tp" type="number" className="input" value={t.port} onChange={(e) => set('port', Number(e.target.value))} /></Field>
          {t.protocol === 'iax2' ? <div /> : (
          <Field label="Transport" htmlFor="tt">
            <select id="tt" className="input" value={t.transport} onChange={(e) => set('transport', e.target.value as Trunk['transport'])}>
              <option value="udp">UDP</option><option value="tcp">TCP</option><option value="tls">TLS (encrypted)</option>
            </select>
          </Field>
          )}
        </div>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Username" htmlFor="tu" error={p('username')}><input id="tu" className="input" value={t.username} onChange={(e) => set('username', e.target.value.trim())} /></Field>
          <Field label="Password" htmlFor="tw" hint={!isNew ? 'Leave empty to keep the current one.' : undefined}>
            <div className="flex gap-2">
              <input id="tw" className="input" type={show ? 'text' : 'password'} value={t.password} onChange={(e) => set('password', e.target.value)} />
              <button type="button" className="btn" aria-label={show ? 'Hide password' : 'Show password'} onClick={() => setShow(!show)}>{show ? <EyeOff size={15} aria-hidden /> : <Eye size={15} aria-hidden />}</button>
            </div>
          </Field>
        </div>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={t.register} onChange={(v) => set('register', v)} label="Register" /> Register with the provider</label>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={t.enabled} onChange={(v) => set('enabled', v)} label="Enabled" /> Enabled</label>
        {servers?.shared && (
          <Field label="Shared between servers" htmlFor="tsh" error={p('shared')}
            hint={t.shared === 'one' ? 'One server registers and carries the line; the others send their calls out through it, and the next one takes the line within seconds if it stops. For providers that allow one registration per account.' : 'Every server registers and places calls on its own. For providers that allow several registrations, or none.'}>
            <select id="tsh" className="input" value={t.shared ?? ''} onChange={(e) => set('shared', e.target.value as Trunk['shared'])}>
              <option value="">Each server uses it</option><option value="one">One server at a time</option>
            </select>
          </Field>
        )}
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={t.secureMedia} onChange={(v) => set('secureMedia', v)} label="Encrypt media" /> Encrypt media (SRTP), if your provider supports it</label>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!t.esinet} onChange={(v) => set('esinet', v)} label="ESInet" /> <span>From the ESInet <span className="text-xs text-slate-500">(911 profile: calls on this trunk are 911 calls, shown with their location on <a className="text-sky-300 underline" href="#/psap">911 calls</a>)</span></span></label>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!t.richCallData} onChange={(v) => set('richCallData', v)} label="Send branded caller ID" /> <span>Send branded caller ID <span className="text-xs text-slate-500">(the logo and reason set on <a className="text-sky-300 underline" href="#/reputation">Caller ID reputation</a>, for providers that pass rich call data on)</span></span></label>
        <div className="space-y-2">
          <label className="flex items-center gap-3 text-sm text-slate-300">
            <Toggle checked={(t.codecs ?? []).length > 0} label="Choose codecs for this trunk" onChange={(v) => set('codecs', v ? ['PCMU', 'PCMA'] : [])} />
            {(t.codecs ?? []).length > 0 ? 'Codecs for this trunk, best first' : 'Codecs: same as Settings'}
          </label>
          {(t.codecs ?? []).length > 0 && <CodecList id="tcodecs" value={t.codecs} onChange={(v) => set('codecs', v)} error={problemFor(err?.problems, 'codecs')} />}
          {problemFor(err?.problems, 'codecs') && <p className="field-error text-sm text-rose-300">{problemFor(err?.problems, 'codecs')}</p>}
        </div>
        <details className="card p-4" open={['authUser', 'fromDomain', 'callerId', 'allowFrom', 'maxChannels'].some((f) => p(f)) || undefined}>
          <summary className="cursor-pointer text-sm font-semibold text-slate-300">Advanced</summary>
          <div className="mt-4 space-y-4">
            <Field label="Authentication username" htmlFor="ta" error={p('authUser')} hint="Only if your provider gives a separate one."><input id="ta" className="input" value={t.authUser} onChange={(e) => set('authUser', e.target.value)} /></Field>
            <Field label="From domain" htmlFor="tf" error={p('fromDomain')} hint="Domain in the From header. Empty uses the provider server."><input id="tf" className="input" value={t.fromDomain} onChange={(e) => set('fromDomain', e.target.value)} /></Field>
            <Field label="Default caller ID" htmlFor="tc" error={p('callerId')} hint="Your main number, shown on outbound calls."><input id="tc" className="input font-mono" value={t.callerId} onChange={(e) => set('callerId', e.target.value)} /></Field>
            <Field label="Also accept calls from" htmlFor="tl" error={p('allowFrom')} hint="Extra provider addresses or networks, one per line (for example 203.0.113.0/24).">
              <textarea id="tl" rows={3} className="input font-mono" value={(t.allowFrom ?? []).join('\n')} onChange={(e) => set('allowFrom', e.target.value.split('\n').map((s) => s.trim()).filter(Boolean))} />
            </Field>
            <Field label="Most calls at once" htmlFor="tx" error={p('maxChannels')} hint="The number of channels your plan includes. 0 for no limit.">
              <input id="tx" type="number" min={0} className="input w-32" value={t.maxChannels} onChange={(e) => set('maxChannels', Math.max(0, Math.trunc(Number(e.target.value) || 0)))} />
            </Field>
          </div>
        </details>
        <RatesEditor t={t} onChange={(v) => setT((t0) => ({ ...t0, ...v }))} p={p} />
      </fieldset>
    </Drawer>
  )
}
