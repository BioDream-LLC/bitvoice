import { CircleDot, Phone, Plus, Router, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useDeskStatusQuery, useExtensionsQuery, useSaveSettingsMutation, useSettingsQuery, type ApiErrorBody, type DeskProtocols } from './api'
import { attempt, useIsAdmin } from './Editor'
import { Field, Page, problemFor, Toggle } from './ui'

const defaults: DeskProtocols = { sccp: { enabled: false, port: 0, phones: [] }, mgcp: { enabled: false, port: 0, gateways: [] } }

function StatusText({ s }: { s?: string }) {
  if (!s) return <span className="text-xs text-slate-500">saved, not running yet</span>
  const ok = s.startsWith('registered') || s === 'ready' || s === 'in a call'
  return <span className={ok ? 'text-xs text-emerald-300' : 'text-xs text-amber-300'}><CircleDot size={11} className="mr-1 inline" aria-hidden />{s}</span>
}

/** Desk phones: Cisco Skinny (SCCP) phones and MGCP gateway ports, each a device of an extension. */
export function DeskPhones() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const { data: exts } = useExtensionsQuery()
  const { data: status } = useDeskStatusQuery(undefined, { pollingInterval: 5000 })
  const [save, { isLoading }] = useSaveSettingsMutation()
  const [edit, setEdit] = useState<DeskProtocols | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!data) return null
  const d: DeskProtocols = edit ?? {
    sccp: { ...defaults.sccp, ...data.system.desk?.sccp, phones: data.system.desk?.sccp?.phones ?? [] },
    mgcp: { ...defaults.mgcp, ...data.system.desk?.mgcp, gateways: data.system.desk?.mgcp?.gateways ?? [] },
  }
  const p = (f: string) => problemFor(err?.problems, 'system.desk.' + f)
  const setS = (v: Partial<DeskProtocols['sccp']>) => setEdit({ ...d, sccp: { ...d.sccp, ...v } })
  const setM = (v: Partial<DeskProtocols['mgcp']>) => setEdit({ ...d, mgcp: { ...d.mgcp, ...v } })
  const gws = d.mgcp.gateways
  const setGw = (i: number, v: Partial<DeskProtocols['mgcp']['gateways'][number]>) => setM({ gateways: gws.map((g, j) => (j === i ? { ...g, ...v } : g)) })
  const submit = async () => {
    const e = await attempt(() => save({ ...data, system: { ...data.system, desk: d } }).unwrap(), 'Desk phones saved')
    setErr(e)
    if (!e) setEdit(null)
  }
  const extSelect = (id: string, value: string, onChange: (v: string) => void, label: string) => (
    <select id={id} aria-label={label} className="input" value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">Extension…</option>
      {exts?.items.map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
    </select>
  )
  return (
    <Page icon={Phone} title="Desk phones (Skinny and MGCP)" accent="from-sky-400 to-teal-400"
      intro="For phones and gateways that do not speak SIP: Cisco desk phones with Skinny (SCCP) firmware, and the ports of MGCP gateways (analogue adapters, line cards). Each is one more device of an extension: it rings with the extension's other phones and calls out as the extension.">
      <fieldset disabled={!admin} className="space-y-6">
        <section className="card space-y-4 p-5" aria-labelledby="sccp-h">
          <h2 id="sccp-h" className="flex items-center gap-2 font-semibold text-slate-100"><Phone size={17} className="text-sky-300" aria-hidden /> Skinny (SCCP) phones</h2>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={d.sccp.enabled} onChange={(v) => setS({ enabled: v })} label="Skinny phones" />
            Accept Skinny phones <span className="text-xs text-slate-500">(Cisco 79xx, 69xx, 89xx with SCCP firmware; TCP port {d.sccp.port || 2000})</span></label>
          <p className="text-xs text-amber-300/90">Skinny has no passwords: a phone is known by its MAC address, and only phones on the local network are accepted. Keep the port off the internet. Tested against BitVoice's own Skinny test phone, not yet against Cisco hardware.</p>
          {d.sccp.enabled && (
            <>
              <Field label="Port" htmlFor="sccp-port" error={p('sccp.port')} hint="Phones look for their server on 2000.">
                <input id="sccp-port" type="number" className="input w-32" value={d.sccp.port || ''} placeholder="2000" onChange={(e) => setS({ port: Number(e.target.value) })} />
              </Field>
              <div className="space-y-2">
                <span className="label">Phones</span>
                {d.sccp.phones.map((ph, i) => (
                  <div key={i} className="grid items-center gap-2 sm:grid-cols-[1fr_14rem_12rem_auto]">
                    <input className="input font-mono" aria-label={`Phone ${i + 1} MAC address`} value={ph.device} placeholder="SEP001122334455 or 00:11:22:33:44:55"
                      onChange={(e) => setS({ phones: d.sccp.phones.map((x, j) => (j === i ? { ...x, device: e.target.value } : x)) })} />
                    {extSelect(`sccp-ext-${i}`, ph.extension, (v) => setS({ phones: d.sccp.phones.map((x, j) => (j === i ? { ...x, extension: v } : x)) }), `Phone ${i + 1} extension`)}
                    <StatusText s={status?.sccp[ph.device.toUpperCase()]} />
                    <button type="button" className="btn" aria-label={`Remove phone ${i + 1}`} onClick={() => setS({ phones: d.sccp.phones.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
                    {(p(`sccp.phones[${i}].device`) || p(`sccp.phones[${i}].extension`)) && <p className="text-sm text-rose-300 sm:col-span-4">{p(`sccp.phones[${i}].device`) ?? p(`sccp.phones[${i}].extension`)}</p>}
                  </div>
                ))}
                <button type="button" className="btn" onClick={() => setS({ phones: [...d.sccp.phones, { device: '', extension: '' }] })}><Plus size={14} aria-hidden /> Add a Skinny phone</button>
              </div>
              <p className="text-xs text-slate-500">Point the phone at this server (its TFTP settings file, or the phone's own menu). Each phone's first line rings its extension; keys, ringing, caller ID and redial work; hold and transfer are for the extension's other phones for now.</p>
            </>
          )}
        </section>

        <section className="card space-y-4 p-5" aria-labelledby="mgcp-h">
          <h2 id="mgcp-h" className="flex items-center gap-2 font-semibold text-slate-100"><Router size={17} className="text-teal-300" aria-hidden /> MGCP gateways</h2>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={d.mgcp.enabled} onChange={(v) => setM({ enabled: v })} label="MGCP gateways" />
            Be the call agent for MGCP gateways <span className="text-xs text-slate-500">(RFC 3435; UDP port {d.mgcp.port || 2727})</span></label>
          <p className="text-xs text-amber-300/90">MGCP has no passwords: give each gateway's address so only it is obeyed (without one, only the local network). Tested against BitVoice's own MGCP test gateway, not yet against gateway hardware.</p>
          {d.mgcp.enabled && (
            <>
              <Field label="Call agent port" htmlFor="mgcp-port" error={p('mgcp.port')} hint="Gateways send to 2727 unless set otherwise.">
                <input id="mgcp-port" type="number" className="input w-32" value={d.mgcp.port || ''} placeholder="2727" onChange={(e) => setM({ port: Number(e.target.value) })} />
              </Field>
              {gws.map((g, i) => (
                <div key={i} className="space-y-3 rounded-lg border border-slate-800 p-4">
                  <div className="grid gap-2 sm:grid-cols-[1fr_1fr_7rem_auto]">
                    <Field label="Gateway name" htmlFor={`mg-n-${i}`} error={p(`mgcp.gateways[${i}].name`)} hint="As in its endpoint names: aaln/1@name.">
                      <input id={`mg-n-${i}`} className="input font-mono" value={g.name} placeholder="ata1.office.lan" onChange={(e) => setGw(i, { name: e.target.value.trim() })} />
                    </Field>
                    <Field label="Address" htmlFor={`mg-h-${i}`} error={p(`mgcp.gateways[${i}].host`)} hint="Empty: where it last spoke from.">
                      <input id={`mg-h-${i}`} className="input font-mono" value={g.host} placeholder="10.0.0.40" onChange={(e) => setGw(i, { host: e.target.value.trim() })} />
                    </Field>
                    <Field label="Port" htmlFor={`mg-p-${i}`} error={p(`mgcp.gateways[${i}].port`)}>
                      <input id={`mg-p-${i}`} type="number" className="input" value={g.port || ''} placeholder="2427" onChange={(e) => setGw(i, { port: Number(e.target.value) })} />
                    </Field>
                    <button type="button" className="btn self-end" aria-label={`Remove gateway ${i + 1}`} onClick={() => setM({ gateways: gws.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
                  </div>
                  <span className="label">Lines</span>
                  {g.lines.map((l, j) => (
                    <div key={j} className="grid items-center gap-2 sm:grid-cols-[10rem_14rem_1fr_auto]">
                      <input className="input font-mono" aria-label={`Gateway ${i + 1} line ${j + 1} endpoint`} value={l.endpoint} placeholder="aaln/1"
                        onChange={(e) => setGw(i, { lines: g.lines.map((x, k) => (k === j ? { ...x, endpoint: e.target.value.trim() } : x)) })} />
                      {extSelect(`mg-e-${i}-${j}`, l.extension, (v) => setGw(i, { lines: g.lines.map((x, k) => (k === j ? { ...x, extension: v } : x)) }), `Gateway ${i + 1} line ${j + 1} extension`)}
                      <StatusText s={status?.mgcp[`${l.endpoint.toLowerCase()}@${g.name.toLowerCase()}`]} />
                      <button type="button" className="btn" aria-label={`Remove gateway ${i + 1} line ${j + 1}`} onClick={() => setGw(i, { lines: g.lines.filter((_, k) => k !== j) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
                      {(p(`mgcp.gateways[${i}].lines[${j}].endpoint`) || p(`mgcp.gateways[${i}].lines[${j}].extension`)) && <p className="text-sm text-rose-300 sm:col-span-4">{p(`mgcp.gateways[${i}].lines[${j}].endpoint`) ?? p(`mgcp.gateways[${i}].lines[${j}].extension`)}</p>}
                    </div>
                  ))}
                  {p(`mgcp.gateways[${i}].lines`) && <p className="text-sm text-rose-300">{p(`mgcp.gateways[${i}].lines`)}</p>}
                  <button type="button" className="btn" onClick={() => setGw(i, { lines: [...g.lines, { endpoint: `aaln/${g.lines.length + 1}`, extension: '' }] })}><Plus size={14} aria-hidden /> Add a line</button>
                </div>
              ))}
              <button type="button" className="btn" onClick={() => setM({ gateways: [...gws, { name: '', host: '', port: 0, lines: [{ endpoint: 'aaln/1', extension: '' }] }] })}><Plus size={14} aria-hidden /> Add a gateway</button>
              <p className="text-xs text-slate-500">Lift the handset for dial tone and dial; the number goes after a pause or #. Incoming calls ring the port with caller ID; ringback, busy and reorder tones are the gateway's own.</p>
            </>
          )}
        </section>
        <button type="button" className="btn btn-primary" disabled={isLoading || !edit} onClick={submit}><Save size={14} aria-hidden /> Save</button>
      </fieldset>
    </Page>
  )
}
