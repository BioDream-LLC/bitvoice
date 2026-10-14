import { createColumnHelper } from '@tanstack/react-table'
import { Eye, EyeOff, Plus, Router, Save, Smartphone } from 'lucide-react'
import { useEffect, useState } from 'react'
import { onServerEvent } from './store'
import {
  useCreateDeviceMutation, useDeleteDeviceMutation, useExtensionsQuery, useDevicesQuery, useSaveSettingsMutation,
  useSettingsQuery, useSystemQuery, useUpdateDeviceMutation, type ApiErrorBody, type Device, type Vendor,
} from './api'
import { Compatibility } from './Compatibility'
import { HotDesks } from './HotDesks'
import { attempt, DeleteButton, Drawer, Table, useIsAdmin } from './Editor'
import { Copyable } from './Setup'
import { Chip, Empty, Field, Page, Toggle, problemFor, relTime } from './ui'

// tested: the file has been tried on the maker's phones; the others follow the maker's documented
// provisioning format and are marked "untested" until someone tries one.
const vendors: { id: Vendor; name: string; colour: string; hint: string; tested: boolean }[] = [
  { id: 'yealink', name: 'Yealink', colour: 'text-emerald-300', hint: 'T3x, T4x, T5x, W-series', tested: true },
  { id: 'polycom', name: 'Poly (Polycom)', colour: 'text-sky-300', hint: 'VVX, Edge E, CCX', tested: true },
  { id: 'grandstream', name: 'Grandstream', colour: 'text-rose-300', hint: 'GXP, GRP, WP', tested: true },
  { id: 'snom', name: 'Snom', colour: 'text-amber-300', hint: 'D-series', tested: true },
  { id: 'fanvil', name: 'Fanvil', colour: 'text-teal-300', hint: 'X-series, V-series', tested: false },
  { id: 'cisco', name: 'Cisco', colour: 'text-sky-300', hint: '6800, 7800, 8800 multiplatform (3PCC), SPA', tested: false },
  { id: 'mitel', name: 'Mitel (Aastra)', colour: 'text-violet-300', hint: '6800, 6900 SIP', tested: false },
  { id: 'sangoma', name: 'Sangoma', colour: 'text-fuchsia-300', hint: 'S-series', tested: false },
  { id: 'algo', name: 'Algo', colour: 'text-orange-300', hint: 'Paging speakers, strobes, adapters', tested: false },
  { id: 'cyberdata', name: 'CyberData', colour: 'text-orange-300', hint: 'Speakers, intercoms, paging adapters', tested: false },
]
const vendorOf = (v: Vendor) => vendors.find((x) => x.id === v)!
const formatMAC = (m: string) => m.match(/.{1,2}/g)?.join(':').toUpperCase() ?? m

const col = createColumnHelper<Device>()

export function Phones() {
  const admin = useIsAdmin()
  const { data } = useDevicesQuery()
  const { data: exts } = useExtensionsQuery()
  const [edit, setEdit] = useState<Device | null>(null)
  const [seen, setSeen] = useState<Record<string, string>>({})
  useEffect(() => onServerEvent((type, d) => {
    if (type !== 'provision.fetched') return
    const ev = d as { file: string; at: string }
    const mac = ev.file.toLowerCase().match(/[0-9a-f]{12}/)?.[0]
    if (mac) setSeen((s) => ({ ...s, [mac]: ev.at }))
  }), [])
  const nameOf = (n: string) => exts?.items.find((x) => x.number === n)?.name ?? ''
  const columns = [
    col.accessor('mac', { header: 'MAC address', cell: (c) => <span className="font-mono text-xs text-amber-300">{formatMAC(c.getValue())}</span> }),
    col.accessor('vendor', { header: 'Make', cell: (c) => { const v = vendorOf(c.getValue()); return <span className={v.colour}>{v.name} <span className="text-slate-500">{c.row.original.model}</span></span> } }),
    col.accessor('extension', { header: 'Extension', cell: (c) => <Chip kind="extension">{c.getValue()} {nameOf(c.getValue())}</Chip> }),
    col.display({ id: 'seen', header: 'Last fetched', cell: (c) => seen[c.row.original.mac]
      ? <span className="text-xs text-emerald-300">{relTime(seen[c.row.original.mac])}</span> : <span className="text-xs text-slate-500">not this session</span> }),
  ]
  return (
    <Page icon={Smartphone} title="Phones" accent="from-emerald-400 to-teal-400"
      intro="Desk phones BitVoice sets up on its own: add the MAC address from the sticker under the phone, choose its extension, and point the phone at the address below."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', mac: '', vendor: 'yealink', model: '', extension: exts?.items[0]?.number ?? '', label: '' })}><Plus size={15} aria-hidden /> Add phone</button>}>
      <ProvisioningCard />
      <HotDesks />
      <Table data={data?.items ?? []} columns={columns} onRow={setEdit}
        empty={<Empty icon={Smartphone} title="No phones added">Phones can also be set up by hand: see an extension's page.</Empty>} />
      {edit && <DeviceEditor d={edit} onClose={() => setEdit(null)} />}
      <Compatibility />
    </Page>
  )
}

function ProvisioningCard() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const { data: sys } = useSystemQuery()
  const [save] = useSaveSettingsMutation()
  const [show, setShow] = useState(false)
  const [pw, setPw] = useState('')
  if (!data) return null
  const pv = data.system.provisioning
  const host = sys?.addresses[0] ?? location.hostname
  const port = sys?.httpsPort || (location.protocol === 'https:' ? Number(location.port) || 443 : 0)
  const where = `${host}${port === 443 ? '' : ':' + port}`
  const url = port ? `https://${where}/provision/` : ''
  // Plain HTTP reaches the same port; phones send to port 80 when none is given, so it is always written.
  const plainURL = port && !pv.httpsOnly ? `http://${host}:${port}/provision/` : ''
  const update = (v: Partial<typeof pv>) => attempt(() => save({ ...data, system: { ...data.system, provisioning: { ...pv, ...v } } }).unwrap(), 'Provisioning saved')
  return (
    <section className="card mb-6 space-y-4 p-5">
      <div className="flex items-center gap-3">
        <Router size={18} className="text-emerald-300" aria-hidden />
        <h2 className="font-semibold text-slate-100">Automatic set-up</h2>
        {admin && <label className="ml-auto flex items-center gap-2 text-sm text-slate-300"><Toggle checked={pv.enabled} onChange={(v) => update({ enabled: v })} label="Automatic set-up" /> {pv.enabled ? <span className="text-emerald-300">On</span> : <span className="text-slate-500">Off</span>}</label>}
      </div>
      {pv.enabled && (
        <div className="grid gap-3 text-sm sm:grid-cols-3">
          <div>
            <span className="label">Provisioning address</span>
            {url ? <Copyable text={url} /> : <p className="text-sm text-rose-300">HTTPS is not running, so phones cannot fetch their settings. See the startup log.</p>}
            {plainURL && <div className="mt-2"><span className="label">For phones without HTTPS</span><Copyable text={plainURL} /></div>}
          </div>
          <div><span className="label">User name</span><Copyable text={pv.username} /></div>
          <div>
            <span className="label">Password</span>
            {admin ? (
              <span className="flex items-center gap-1">
                {show ? <Copyable text={pv.password} /> : <span className="font-mono text-slate-500">••••••••••••</span>}
                <button type="button" className="btn px-2 py-1" aria-label={show ? 'Hide password' : 'Show password'} onClick={() => setShow(!show)}>{show ? <EyeOff size={13} aria-hidden /> : <Eye size={13} aria-hidden />}</button>
              </span>
            ) : <span className="text-slate-500">Administrators can see it</span>}
          </div>
          <p className="text-xs text-slate-500 sm:col-span-3">
            Enter the address, user name and password in the phone's web page (Auto Provision), or hand the address out with DHCP option 66.
            Phones register over <span className="text-emerald-300">{pv.transport.toUpperCase()}</span>.
            {pv.httpsOnly
              ? <> Settings files go out over <span className="text-emerald-300">HTTPS only</span>.</>
              : <> Settings files go out over HTTPS, or <span className="text-amber-300">plain HTTP</span> for phones that cannot use HTTPS or do not trust this server's certificate: over HTTP the phone's SIP password crosses the network unencrypted.</>}
          </p>
          {admin && (
            <div className="flex flex-wrap items-end gap-3 sm:col-span-3">
              <Field label="Phones register over" htmlFor="pt">
                <select id="pt" className="input w-40" value={pv.transport} onChange={(e) => update({ transport: e.target.value as typeof pv.transport })}>
                  <option value="udp">UDP</option><option value="tcp">TCP</option><option value="tls">TLS (encrypted)</option>
                </select>
              </Field>
              <label className="flex items-center gap-2 pb-2 text-sm text-slate-300">
                <Toggle checked={!!pv.httpsOnly} onChange={(v) => update({ httpsOnly: v })} label="HTTPS only for phones" /> HTTPS only for phones
              </label>
              <Field label="New password" htmlFor="pp" hint="At least 12 characters. Every phone then needs it.">
                <input id="pp" className="input w-64" type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} />
              </Field>
              <button className="btn" disabled={pw.length < 12} onClick={async () => { if (!await update({ password: pw })) setPw('') }}><Save size={14} aria-hidden /> Change</button>
            </div>
          )}
        </div>
      )}
    </section>
  )
}

function DeviceEditor({ d: initial, onClose }: { d: Device; onClose: () => void }) {
  const admin = useIsAdmin()
  const [d, setD] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: exts } = useExtensionsQuery()
  const [create] = useCreateDeviceMutation()
  const [update] = useUpdateDeviceMutation()
  const [remove] = useDeleteDeviceMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(d) : update(d)).unwrap(), 'Phone saved')
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New phone' : formatMAC(d.mac)} icon={<Smartphone className="text-emerald-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what="this phone" onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Phone removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div>
          <span className="label">Make</span>
          <div className="grid grid-cols-2 gap-2">
            {vendors.map((v) => (
              <button type="button" key={v.id} aria-pressed={d.vendor === v.id} onClick={() => setD({ ...d, vendor: v.id })}
                className={`card p-3 text-left ${d.vendor === v.id ? 'ring-2 ring-emerald-400' : ''}`}>
                <div className={`text-sm font-semibold ${v.colour}`}>{v.name}{!v.tested && <span className="ml-2 text-[10px] font-normal uppercase tracking-wide text-amber-300">untested</span>}</div>
                <div className="text-xs text-slate-500">{v.hint}</div>
              </button>
            ))}
          </div>
          {!vendorOf(d.vendor).tested && <p className="mt-2 text-xs text-amber-300">This file follows {vendorOf(d.vendor).name}'s documented provisioning format but has not been tried on one of its phones yet. If the phone does not sign in, check its provisioning log and tell us what it says.</p>}
        </div>
        <div className="grid grid-cols-2 gap-4">
          <Field label="MAC address" htmlFor="dm" error={p('mac')} hint="On the sticker under the phone.">
            <input id="dm" className="input font-mono text-amber-300" autoFocus={isNew} value={d.mac} onChange={(e) => { const v = e.target.value; setD((d0) => ({ ...d0, mac: v })) }} placeholder="80:5E:C0:11:22:33" />
          </Field>
          <Field label="Model" htmlFor="do"><input id="do" className="input" value={d.model} onChange={(e) => { const v = e.target.value; setD((d0) => ({ ...d0, model: v })) }} placeholder="T54W" /></Field>
        </div>
        <Field label="Extension" htmlFor="dx" error={p('extension')}>
          <select id="dx" className="input" value={d.extension} onChange={(e) => { const v = e.target.value; setD((d0) => ({ ...d0, extension: v })) }}>
            {exts?.items.map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
          </select>
        </Field>
        <Field label="Label on the screen" htmlFor="dl" hint="Empty shows the extension number and name."><input id="dl" className="input" value={d.label} onChange={(e) => { const v = e.target.value; setD((d0) => ({ ...d0, label: v })) }} /></Field>
      </fieldset>
    </Drawer>
  )
}
