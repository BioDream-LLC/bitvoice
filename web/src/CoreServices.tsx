import { ArrowDown, ArrowUp, CheckCircle2, ListOrdered, Map as MapIcon, MapPinCheck, Network, Plus, Save, Search, Shield, ShieldAlert, Trash2, Upload, XCircle } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  api, errorOf, useBoundariesQuery, useDeleteBoundaryMutation, useLazyFindAreaQuery, useSaveBoundaryMutation, useSaveSettingsMutation, useSettingsQuery, useTrunksQuery, useSchedulesQuery,
  useAddressDataQuery, useDeleteAddressesMutation, useValidateAddressMutation, useBcfLogQuery, useLisDataQuery, useLazyLisDataQuery, useDeleteLISMutation,
  type ApiErrorBody, type Boundary, type CoreRule, type CoreSettings, type Validation,
} from './api'
import { attempt, useIsAdmin } from './Editor'
import { store } from './store'
import { useDraft } from './useDraft'
import { Badge, Empty, Field, Page, StaleBanner, Toggle, problemFor } from './ui'

const blankBCF = { enabled: false, perMinute: 0, concurrent: 0, deny: [] as string[], mark: false, verifyIdentity: false }
const blankCore: CoreSettings = { ecrf: false, lis: false, source: '', allowFrom: [], psaps: [], default: '', lostUrl: '', rules: [], bcf: blankBCF }
/** Place names may have spaces in them: split on commas only. */
const commas = (s: string) => s.split(',').map((x) => x.trim()).filter(Boolean)
const list = (s: string) => s.split(/[,\s]+/).map((x) => x.trim()).filter(Boolean)

export function CoreServices() {
  const admin = useIsAdmin()
  return (
    <Page icon={Network} title="Core services" accent="from-sky-400 to-violet-400"
      intro="The NG911 Core Services profile, for a 911 authority running its own core: the ECRF answering LoST from your service boundaries, and the ESRP sending each 911 call to the PSAP that serves the caller's location.">
      {admin && <CoreSetup />}
      <Areas />
      <TryPlace />
      <AddressData />
      <LocationRecords />
      <BCFLog />
    </Page>
  )
}

function CoreSetup() {
  const { data } = useSettingsQuery()
  const { data: trunks } = useTrunksQuery()
  const { data: schedules } = useSchedulesQuery()
  const [deny, setDeny] = useState<string | null>(null)
  const server = useMemo(() => (data ? { ...blankCore, ...(data.system.core ?? {}), psaps: data.system.core?.psaps ?? [], allowFrom: data.system.core?.allowFrom ?? [],
    rules: data.system.core?.rules ?? [], bcf: { ...blankBCF, ...(data.system.core?.bcf ?? {}), deny: data.system.core?.bcf?.deny ?? [] } } : undefined), [data])
  const { draft, setDraft, reset, stale } = useDraft<CoreSettings>(server)
  const [allow, setAllow] = useState<string | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [save, { isLoading }] = useSaveSettingsMutation()
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.core.' + f)
  const set = (v: Partial<CoreSettings>) => setDraft((d) => ({ ...d, ...v }))
  const setPSAP = (i: number, v: Partial<CoreSettings['psaps'][0]>) => set({ psaps: draft.psaps.map((x, j) => (j === i ? { ...x, ...v } : x)) })
  const doSave = async () => {
    const v = { ...draft, allowFrom: allow === null ? draft.allowFrom : list(allow), bcf: { ...draft.bcf, deny: deny === null ? draft.bcf.deny : list(deny) } }
    try { const s = await save({ ...data, system: { ...data.system, core: v } }).unwrap(); reset({ ...blankCore, ...(s.system.core ?? {}) }); setAllow(null); setDeny(null); setErr(null); toast.success('Core services saved') }
    catch (e) { setErr(errorOf(e)) }
  }
  const origin = location.origin
  return (
    <section className="card space-y-5 p-5" aria-labelledby="cs-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="cs-h" className="font-semibold text-slate-100">ECRF and ESRP</h2>
      <div className="space-y-3">
        <Toggle checked={draft.ecrf} onChange={(v) => set({ ecrf: v })} label="Answer LoST queries (ECRF)" />
        <p className="text-sm text-slate-400">Other NG911 elements ask at <span className="font-mono text-amber-300">{origin}/lost</span> (LoST, RFC 5222). Only the addresses below get an answer.</p>
        <Toggle checked={draft.lis} onChange={(v) => set({ lis: v })} label="Answer location requests by telephone number (LIS)" />
        <p className="text-sm text-slate-400">PSAPs and legacy network gateways ask at <span className="font-mono text-amber-300">{origin}/held</span> (HELD, RFC 5985, by number as RFC 6155 has it), from the location records below. The same addresses only.</p>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="May ask (your ESInet)" htmlFor="csa" error={p('allowFrom') ?? err?.problems?.find((x) => x.field.startsWith('system.core.allowFrom['))?.message} hint="Addresses or ranges, separated by commas.">
            <input id="csa" className="input font-mono" value={allow ?? draft.allowFrom.join(', ')} placeholder="10.20.0.0/16" onChange={(e) => setAllow(e.target.value)} />
          </Field>
          <Field label="ECRF name" htmlFor="css" hint="This ECRF's name in answers; blank uses the server's name."><input id="css" className="input" value={draft.source} onChange={(e) => set({ source: e.target.value.trim() })} /></Field>
        </div>
      </div>
      <div className="space-y-3">
        <h3 className="text-sm font-semibold text-violet-300">PSAPs the ESRP sends to</h3>
        <p className="text-sm text-slate-400">Send the ESInet's incoming calls to <span className="text-slate-200">ESRP</span> in Call routing. Each call goes to the PSAP whose URI the ECRF answers with for the caller's location, over that PSAP's trunk, and to its overflow PSAP when it does not take it.</p>
        {draft.psaps.map((x, i) => (
          <div key={i} className="grid items-end gap-3 rounded-lg border border-slate-800 p-3 md:grid-cols-[1fr_1fr_1fr_1fr_auto]">
            <Field label="Name" htmlFor={`csn${i}`}><input id={`csn${i}`} className="input" value={x.name} onChange={(e) => setPSAP(i, { name: e.target.value })} /></Field>
            <Field label="URI" htmlFor={`csu${i}`} error={p(`psaps[${i}].uri`)}><input id={`csu${i}`} className="input font-mono" value={x.uri} placeholder="sip:sos@psap.example.gov" onChange={(e) => setPSAP(i, { uri: e.target.value.trim() })} /></Field>
            <Field label="Trunk" htmlFor={`cst${i}`} error={p(`psaps[${i}].trunk`)}>
              <select id={`cst${i}`} className="input" value={x.trunk} onChange={(e) => setPSAP(i, { trunk: e.target.value })}>
                <option value="">Choose…</option>
                {trunks?.items.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
              </select>
            </Field>
            <Field label="Overflow to" htmlFor={`cso${i}`} error={p(`psaps[${i}].overflow`)}>
              <select id={`cso${i}`} className="input" value={x.overflow} onChange={(e) => setPSAP(i, { overflow: e.target.value })}>
                <option value="">Nobody</option>
                {draft.psaps.filter((y, j) => j !== i && y.uri).map((y) => <option key={y.uri} value={y.uri}>{y.name || y.uri}</option>)}
              </select>
            </Field>
            <button className="btn-ghost p-2" aria-label={`Remove PSAP ${x.name || i + 1}`} onClick={() => set({ psaps: draft.psaps.filter((_, j) => j !== i) })}><Trash2 size={15} className="text-rose-300" aria-hidden /></button>
          </div>
        ))}
        <button className="btn" onClick={() => set({ psaps: [...draft.psaps, { uri: '', name: '', trunk: '', overflow: '' }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a PSAP</button>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="Default PSAP" htmlFor="csd" error={p('default')} hint="For calls without a location, or from a place no area covers.">
            <select id="csd" className="input" value={draft.default} onChange={(e) => set({ default: e.target.value })}>
              <option value="">None (refuse the call)</option>
              {draft.psaps.filter((y) => y.uri).map((y) => <option key={y.uri} value={y.uri}>{y.name || y.uri}</option>)}
            </select>
          </Field>
          <Field label="Outside ECRF" htmlFor="csl" error={p('lostUrl')} hint="Blank: this server's own areas, below. Else the LoST address of the ECRF to ask."><input id="csl" className="input font-mono" value={draft.lostUrl} placeholder="https://ecrf.state.example.gov/lost" onChange={(e) => set({ lostUrl: e.target.value.trim() })} /></Field>
        </div>
      </div>
      <Rules draft={draft} set={set} p={p} trunks={trunks?.items ?? []} schedules={schedules?.items ?? []} />
      <div className="space-y-3">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-violet-300"><Shield size={15} className="text-rose-300" aria-hidden /> Border control (BCF)</h3>
        <p className="text-sm text-slate-400">Checks every call from an ESInet trunk before anything else: refuses denied addresses, and addresses that start too many calls a minute or hold too many at once (a telephony denial-of-service attack).</p>
        <Toggle checked={draft.bcf.enabled} onChange={(v) => set({ bcf: { ...draft.bcf, enabled: v } })} label="Check calls from the ESInet" />
        {draft.bcf.enabled && <>
          <div className="grid gap-3 md:grid-cols-3">
            <Field label="Calls a minute, per address" htmlFor="cbm" error={p('bcf.perMinute')} hint="0: no limit."><input id="cbm" className="input w-32" inputMode="numeric" value={draft.bcf.perMinute || ''} placeholder="0" onChange={(e) => set({ bcf: { ...draft.bcf, perMinute: Number(e.target.value.replace(/\D/g, '')) || 0 } })} /></Field>
            <Field label="Calls at once, per address" htmlFor="cbc" error={p('bcf.concurrent')} hint="0: no limit."><input id="cbc" className="input w-32" inputMode="numeric" value={draft.bcf.concurrent || ''} placeholder="0" onChange={(e) => set({ bcf: { ...draft.bcf, concurrent: Number(e.target.value.replace(/\D/g, '')) || 0 } })} /></Field>
            <Field label="Always refuse" htmlFor="cbd" error={err?.problems?.find((x) => x.field.startsWith('system.core.bcf.deny['))?.message} hint="Addresses or ranges, separated by commas.">
              <input id="cbd" className="input font-mono" value={deny ?? draft.bcf.deny.join(', ')} placeholder="203.0.113.0/24" onChange={(e) => setDeny(e.target.value)} />
            </Field>
          </div>
          <Toggle checked={draft.bcf.mark} onChange={(v) => set({ bcf: { ...draft.bcf, mark: v } })} label="Over a limit: let the call through marked suspicious (for a policy rule) instead of refusing it" />
          <Toggle checked={draft.bcf.verifyIdentity} onChange={(v) => set({ bcf: { ...draft.bcf, verifyIdentity: v } })} label="Check STIR/SHAKEN signatures: a call whose signed caller ID fails goes through marked suspicious" />
          <p className="text-xs text-slate-500">Signatures are checked against the STI roots set under Call screening in Settings. A 911 call is never refused for a bad signature.</p>
        </>}
      </div>
      <div className="flex justify-end"><button className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={15} aria-hidden /> Save</button></div>
    </section>
  )
}

const blankRule: CoreRule = { id: '', name: '', services: [], psaps: [], schedule: '', whenOpen: false, noLocation: false, suspicious: false, trunks: [], action: 'route', to: '' }

/** The ESRP's policy rules: first match decides, after the ECRF has picked a PSAP. */
function Rules({ draft, set, p, trunks, schedules }: {
  draft: CoreSettings; set: (v: Partial<CoreSettings>) => void; p: (f: string) => string | undefined
  trunks: { id: string; name: string }[]; schedules: { id: string; name: string }[]
}) {
  const setRule = (i: number, v: Partial<CoreRule>) => set({ rules: draft.rules.map((r, j) => (j === i ? { ...r, ...v } : r)) })
  const move = (i: number, by: number) => {
    const j = i + by
    if (j < 0 || j >= draft.rules.length) return
    const next = [...draft.rules]; [next[i], next[j]] = [next[j], next[i]]; set({ rules: next })
  }
  const psaps = draft.psaps.filter((x) => x.uri)
  return (
    <div className="space-y-3">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-violet-300"><ListOrdered size={15} className="text-amber-300" aria-hidden /> Policy rules</h3>
      <p className="text-sm text-slate-400">Checked in order once the ECRF has picked a PSAP; the first rule whose conditions all hold sends the call elsewhere, or refuses it. Leave a condition empty to ignore it. Calls no rule matches go where the ECRF said.</p>
      {draft.rules.map((r, i) => (
        <fieldset key={i} className="space-y-3 rounded-lg border border-slate-800 p-3" aria-label={`Rule ${r.name || i + 1}`}>
          <div className="grid items-end gap-3 md:grid-cols-[1fr_auto]">
            <Field label="Rule name" htmlFor={`crn${i}`} error={p(`rules[${i}].name`)}><input id={`crn${i}`} className="input" value={r.name} placeholder="Fire calls after hours" onChange={(e) => setRule(i, { name: e.target.value })} /></Field>
            <div className="flex gap-1">
              <button className="btn-ghost p-2" aria-label={`Move rule ${r.name || i + 1} up`} disabled={i === 0} onClick={() => move(i, -1)}><ArrowUp size={14} aria-hidden /></button>
              <button className="btn-ghost p-2" aria-label={`Move rule ${r.name || i + 1} down`} disabled={i === draft.rules.length - 1} onClick={() => move(i, 1)}><ArrowDown size={14} aria-hidden /></button>
              <button className="btn-ghost p-2" aria-label={`Remove rule ${r.name || i + 1}`} onClick={() => set({ rules: draft.rules.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
            </div>
          </div>
          <div className="grid gap-3 md:grid-cols-3">
            <Field label="Services" htmlFor={`crs${i}`} error={p(`rules[${i}].services[0]`)} hint="Commas between; sos.fire covers its sub-services.">
              <input key={r.services.join(',')} id={`crs${i}`} className="input font-mono" defaultValue={r.services.join(', ')} placeholder="urn:service:sos.fire" onBlur={(e) => setRule(i, { services: commas(e.target.value) })} />
            </Field>
            <Field label="When the ECRF picked" htmlFor={`crp${i}`}>
              <select id={`crp${i}`} className="input" value={r.psaps[0] ?? ''} onChange={(e) => setRule(i, { psaps: e.target.value ? [e.target.value] : [] })}>
                <option value="">Any PSAP</option>{psaps.map((x) => <option key={x.uri} value={x.uri}>{x.name || x.uri}</option>)}
              </select>
            </Field>
            <Field label="Came in on" htmlFor={`crt${i}`}>
              <select id={`crt${i}`} className="input" value={r.trunks[0] ?? ''} onChange={(e) => setRule(i, { trunks: e.target.value ? [e.target.value] : [] })}>
                <option value="">Any trunk</option>{trunks.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </select>
            </Field>
            <Field label="Schedule" htmlFor={`crh${i}`} error={p(`rules[${i}].schedule`)}>
              <select id={`crh${i}`} className="input" value={r.schedule ? `${r.schedule}:${r.whenOpen ? 'open' : 'closed'}` : ''}
                onChange={(e) => { const [id, w] = e.target.value.split(':'); setRule(i, { schedule: id ?? '', whenOpen: w === 'open' }) }}>
                <option value="">Any time</option>
                {schedules.flatMap((s) => [<option key={s.id + 'o'} value={`${s.id}:open`}>While {s.name} is open</option>, <option key={s.id + 'c'} value={`${s.id}:closed`}>While {s.name} is closed</option>])}
              </select>
            </Field>
            <div className="flex flex-col justify-end gap-2">
              <Toggle checked={r.noLocation} onChange={(v) => setRule(i, { noLocation: v })} label="Only calls with no location" />
              <Toggle checked={r.suspicious} onChange={(v) => setRule(i, { suspicious: v })} label="Only calls the BCF marked suspicious" />
            </div>
          </div>
          <div className="grid items-end gap-3 md:grid-cols-[12rem_1fr]">
            <Field label="Then" htmlFor={`cra${i}`} error={p(`rules[${i}].action`)}>
              <select id={`cra${i}`} className="input" value={r.action} onChange={(e) => setRule(i, { action: e.target.value as CoreRule['action'] })}>
                <option value="route">Send it to</option><option value="reject">Refuse it (busy)</option>
              </select>
            </Field>
            {r.action === 'route' && (
              <Field label="PSAP" htmlFor={`cro${i}`} error={p(`rules[${i}].to`)}>
                <select id={`cro${i}`} className="input" value={r.to} onChange={(e) => setRule(i, { to: e.target.value })}>
                  <option value="">Choose…</option>{psaps.map((x) => <option key={x.uri} value={x.uri}>{x.name || x.uri}</option>)}
                </select>
              </Field>
            )}
          </div>
        </fieldset>
      ))}
      <button className="btn" onClick={() => set({ rules: [...draft.rules, { ...blankRule }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a rule</button>
    </div>
  )
}

function Areas() {
  const admin = useIsAdmin()
  const { data } = useBoundariesQuery()
  const [service, setService] = useState('urn:service:sos')
  const [uri, setUri] = useState('')
  const [replace, setReplace] = useState(false)
  const [busy, setBusy] = useState(false)
  const file = useRef<HTMLInputElement>(null)
  const [remove] = useDeleteBoundaryMutation()
  const [edit, setEdit] = useState<Boundary | null>(null)
  const upload = async (f: File) => {
    setBusy(true)
    try {
      const r = await fetch(`./api/core/boundaries?${new URLSearchParams({ service, uri, replace: String(replace) })}`, { method: 'POST', body: await f.arrayBuffer(), headers: { 'X-BitVoice': '1' } })
      const d = await r.json().catch(() => ({}))
      if (!r.ok) throw { data: d }
      toast.success(`${d.added} areas added`)
      store.dispatch(api.util.invalidateTags(['Settings']))
    } catch (e) { toast.error(errorOf(e).message) }
    setBusy(false)
  }
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="ca-h">
      <h2 id="ca-h" className="flex items-center gap-2 font-semibold text-slate-100"><MapIcon size={17} className="text-emerald-300" aria-hidden /> Service areas</h2>
      {admin && (
        <div className="grid items-end gap-3 md:grid-cols-[1fr_1fr_auto_auto]">
          <Field label="Service" htmlFor="cav" hint="urn:service:sos for all of 911, or a part of it (sos.police, sos.fire, sos.ambulance)."><input id="cav" className="input font-mono" value={service} onChange={(e) => setService(e.target.value.trim())} /></Field>
          <Field label="PSAP for areas that do not say" htmlFor="cau" hint="Used when a polygon has no uri property."><input id="cau" className="input font-mono" value={uri} placeholder="sip:sos@psap.example.gov" onChange={(e) => setUri(e.target.value.trim())} /></Field>
          <Toggle checked={replace} onChange={setReplace} label="Replace this service's areas" />
          <button className="btn btn-primary" disabled={busy} onClick={() => file.current?.click()}><Upload size={15} aria-hidden /> {busy ? 'Reading…' : 'Upload GeoJSON'}</button>
          <input ref={file} type="file" className="hidden" accept=".geojson,.json,application/geo+json,application/json" aria-label="GeoJSON boundaries" onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = '' }} />
        </div>
      )}
      {!data?.items.length ? <Empty icon={MapIcon} title="No areas yet">Export your PSAP (or police, fire, EMS) boundaries from GIS as GeoJSON polygons, and upload them. Properties name, uri and number are read when there.</Empty> : (
        <table className="w-full text-sm"><thead className="text-left text-xs text-slate-500"><tr><th className="py-1">Area</th><th>Service</th><th>PSAP</th><th>Corners</th><th>Places</th><th><span className="sr-only">Actions</span></th></tr></thead>
          <tbody>{data.items.map((b) => (
            <tr key={b.id} className="border-t border-slate-800">
              <td className="py-1 text-slate-200">{b.name}</td>
              <td><Badge status="info">{b.service.replace('urn:service:', '')}</Badge></td>
              <td className="font-mono text-xs text-amber-300">{b.uri}</td>
              <td className="text-slate-400">{b.corners}</td>
              <td className="text-xs text-slate-400">{[...(b.cities ?? []), ...(b.counties ?? [])].join(', ')}</td>
              <td className="text-right">{admin && <>
                <button className="btn-ghost px-2 py-1 text-xs" onClick={() => setEdit(b)}>Edit</button>
                <button className="btn-ghost p-2" aria-label={`Delete area ${b.name}`} onClick={() => attempt(() => remove(b.id).unwrap(), 'Area removed')}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
              </>}</td>
            </tr>))}</tbody></table>
      )}
      {edit && <EditArea b={edit} onClose={() => setEdit(null)} />}
    </section>
  )
}

function EditArea({ b, onClose }: { b: Boundary; onClose: () => void }) {
  const [x, setX] = useState(b)
  const [cities, setCities] = useState((b.cities ?? []).join(', '))
  const [counties, setCounties] = useState((b.counties ?? []).join(', '))
  const [states, setStates] = useState((b.states ?? []).join(', '))
  const [save] = useSaveBoundaryMutation()
  return (
    <div className="space-y-3 rounded-lg border border-sky-900 p-3" aria-label={`Edit area ${b.name}`}>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="Name" htmlFor="ean"><input id="ean" className="input" value={x.name} onChange={(e) => setX({ ...x, name: e.target.value })} /></Field>
        <Field label="PSAP URI" htmlFor="eau"><input id="eau" className="input font-mono" value={x.uri} onChange={(e) => setX({ ...x, uri: e.target.value.trim() })} /></Field>
        <Field label="Service" htmlFor="eas"><input id="eas" className="input font-mono" value={x.service} onChange={(e) => setX({ ...x, service: e.target.value.trim() })} /></Field>
        <Field label="Cities (civic addresses)" htmlFor="eac" hint="A caller whose address names one of these (A3) is served here."><input id="eac" className="input" value={cities} onChange={(e) => setCities(e.target.value)} /></Field>
        <Field label="Counties" htmlFor="eao" hint="Used when no cities are listed (A2)."><input id="eao" className="input" value={counties} onChange={(e) => setCounties(e.target.value)} /></Field>
        <Field label="States" htmlFor="eat" hint="Two letters (A1); blank for any."><input id="eat" className="input" value={states} onChange={(e) => setStates(e.target.value)} /></Field>
      </div>
      <div className="flex justify-end gap-2">
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn btn-primary" onClick={async () => { if (!await attempt(() => save({ ...x, cities: commas(cities), counties: commas(counties), states: list(states) }).unwrap(), 'Area saved')) onClose() }}><Save size={14} aria-hidden /> Save</button>
      </div>
    </div>
  )
}

function TryPlace() {
  const [lat, setLat] = useState('')
  const [lon, setLon] = useState('')
  const [city, setCity] = useState('')
  const [state, setState] = useState('')
  const [service, setService] = useState('urn:service:sos')
  const [find, { data, isFetching }] = useLazyFindAreaQuery()
  return (
    <section className="card mt-6 space-y-3 p-5" aria-labelledby="ct-h">
      <h2 id="ct-h" className="flex items-center gap-2 font-semibold text-slate-100"><Search size={17} className="text-sky-300" aria-hidden /> Try a place</h2>
      <div className="grid items-end gap-3 md:grid-cols-6">
        <Field label="Latitude" htmlFor="ctl"><input id="ctl" className="input font-mono" value={lat} placeholder="40.2633" onChange={(e) => setLat(e.target.value)} /></Field>
        <Field label="Longitude" htmlFor="cto"><input id="cto" className="input font-mono" value={lon} placeholder="-76.8823" onChange={(e) => setLon(e.target.value)} /></Field>
        <Field label="or city" htmlFor="ctc"><input id="ctc" className="input" value={city} onChange={(e) => setCity(e.target.value)} /></Field>
        <Field label="State" htmlFor="cts"><input id="cts" className="input" value={state} onChange={(e) => setState(e.target.value)} /></Field>
        <Field label="Service" htmlFor="ctv"><input id="ctv" className="input font-mono" value={service} onChange={(e) => setService(e.target.value.trim())} /></Field>
        <button className="btn btn-primary" disabled={isFetching} onClick={() => find({ lat, lon, city, state, service })}><Search size={14} aria-hidden /> Find</button>
      </div>
      {data && (data.found && data.area
        ? <p className="text-sm text-emerald-300">Served by <span className="font-semibold">{data.area.name}</span>: calls go to <span className="font-mono">{data.area.uri}</span> ({data.area.service.replace('urn:service:', '')}).</p>
        : <p className="text-sm text-amber-300">No area serves that place ({data.why}). The ESRP would use the default PSAP.</p>)}
    </section>
  )
}

/** The LVF: the 911 authority's address data, and a check of one address against it. */
const civicFields: [string, string, string][] = [
  ['HNO', 'Number', '122'], ['PRD', 'Before', 'N'], ['RD', 'Street', 'Main'], ['STS', 'Type', 'St'], ['POD', 'After', ''],
  ['A3', 'City', 'Springfield'], ['A2', 'County', ''], ['A1', 'State', 'IL'], ['PC', 'Postal code', ''],
]
const plural = (n: number, w: string) => `${n.toLocaleString()} ${w}${n === 1 ? '' : 's'}`
const article = (w: string) => (/^[aeiou]/i.test(w) ? 'an ' : 'a ') + w
const tokenName: Record<string, string> = Object.fromEntries([...civicFields.map(([k, n]) => [k, n]), ['country', 'Country'], ['HNS', 'Number suffix'], ['LOC', 'Location'], ['NAM', 'Name']])

function AddressData() {
  const admin = useIsAdmin()
  const { data } = useAddressDataQuery()
  const [remove] = useDeleteAddressesMutation()
  const [check, { data: result, error, isLoading: checking, reset }] = useValidateAddressMutation()
  const [replace, setReplace] = useState(true)
  const [busy, setBusy] = useState(false)
  const [addr, setAddr] = useState<Record<string, string>>({})
  const file = useRef<HTMLInputElement>(null)
  const upload = async (f: File) => {
    setBusy(true)
    try {
      const r = await fetch(`./api/core/addresses?replace=${replace}`, { method: 'POST', body: await f.arrayBuffer(), headers: { 'X-BitVoice': '1' } })
      const d = await r.json().catch(() => ({}))
      if (!r.ok) throw { data: d }
      toast.success(`${plural(d.streets, 'road segment')} and ${plural(d.points, 'address point')} read`)
      store.dispatch(api.util.invalidateTags(['Settings']))
    } catch (e) { toast.error(errorOf(e).message) }
    setBusy(false)
  }
  const have = (data?.streets ?? 0) + (data?.points ?? 0) > 0
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="cl-h">
      <h2 id="cl-h" className="flex items-center gap-2 font-semibold text-slate-100"><MapPinCheck size={17} className="text-teal-300" aria-hidden /> Address validation (LVF)</h2>
      <p className="text-sm text-slate-400">Checks civic addresses against your 911 GIS data: NENA road centerlines (address ranges on each side) and site/structure address points. Carriers and location servers ask with LoST (validateLocation), and the answer says which parts of the address are valid and which are not.</p>
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm text-slate-300">{have ? <><span className="font-semibold text-emerald-300">{data!.streets.toLocaleString()}</span> road segments, <span className="font-semibold text-emerald-300">{data!.points.toLocaleString()}</span> address points</> : 'No address data yet.'}</span>
        {admin && <>
          <Toggle checked={replace} onChange={setReplace} label="Replace what is there" />
          <button className="btn btn-primary" disabled={busy} onClick={() => file.current?.click()}><Upload size={15} aria-hidden /> {busy ? 'Reading…' : 'Upload GIS GeoJSON'}</button>
          <input ref={file} type="file" className="hidden" accept=".geojson,.json,application/geo+json,application/json" aria-label="GIS address data" onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = '' }} />
          {have && <button className="btn" onClick={() => attempt(() => remove().unwrap(), 'Address data removed')}><Trash2 size={14} className="text-rose-300" aria-hidden /> Remove</button>}
        </>}
      </div>
      {have && (
        <form className="space-y-3" aria-label="Check an address" onSubmit={(e) => { e.preventDefault(); reset(); check(Object.fromEntries(Object.entries(addr).filter(([, v]) => v.trim()))) }}>
          <h3 className="text-sm font-semibold text-violet-300">Check an address</h3>
          <div className="grid gap-3 sm:grid-cols-3 lg:grid-cols-9">
            {civicFields.map(([k, label, ph]) => (
              <Field key={k} label={label} htmlFor={`cv${k}`}><input id={`cv${k}`} className="input" value={addr[k] ?? ''} placeholder={ph} onChange={(e) => setAddr((a) => ({ ...a, [k]: e.target.value }))} /></Field>
            ))}
          </div>
          <button className="btn btn-primary" disabled={checking}><Search size={15} aria-hidden /> Check</button>
          {error && <p className="text-sm text-rose-300">{errorOf(error).message}</p>}
          {result && <ValidationResult v={result} />}
        </form>
      )}
    </section>
  )
}

/** The LIS's location records: a spreadsheet of numbers and their addresses. */
function LocationRecords() {
  const admin = useIsAdmin()
  const { data } = useLisDataQuery()
  const [remove] = useDeleteLISMutation()
  const [find, { data: found, isFetching }] = useLazyLisDataQuery()
  const [replace, setReplace] = useState(false)
  const [busy, setBusy] = useState(false)
  const [num, setNum] = useState('')
  const file = useRef<HTMLInputElement>(null)
  const upload = async (f: File) => {
    setBusy(true)
    try {
      const r = await fetch(`./api/core/lis?replace=${replace}`, { method: 'POST', body: await f.arrayBuffer(), headers: { 'X-BitVoice': '1' } })
      const d = await r.json().catch(() => ({}))
      if (!r.ok) throw { data: d }
      toast.success(`${plural(d.added, 'location record')} read, ${d.records.toLocaleString()} in all`)
      store.dispatch(api.util.invalidateTags(['Settings']))
    } catch (e) { toast.error(errorOf(e).message) }
    setBusy(false)
  }
  const n = data?.records ?? 0
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="cr-h">
      <h2 id="cr-h" className="flex items-center gap-2 font-semibold text-slate-100"><MapPinCheck size={17} className="text-amber-300" aria-hidden /> Location records (LIS)</h2>
      <p className="text-sm text-slate-400">What the LIS answers with: a spreadsheet (CSV) with a header row, a <span className="font-mono text-slate-300">number</span> column and the address as <span className="font-mono text-slate-300">hno, rd, sts, unit, floor, a3 (city), a1 (state), pc, country</span>, with <span className="font-mono text-slate-300">lat, lon, radius</span> when known. A number listed again replaces its old record.</p>
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm text-slate-300">{n ? <><span className="font-semibold text-emerald-300">{n.toLocaleString()}</span> numbers</> : 'No location records yet.'}</span>
        {admin && <>
          <Toggle checked={replace} onChange={setReplace} label="Replace every record" />
          <button className="btn btn-primary" disabled={busy} onClick={() => file.current?.click()}><Upload size={15} aria-hidden /> {busy ? 'Reading…' : 'Upload CSV'}</button>
          <input ref={file} type="file" className="hidden" accept=".csv,text/csv" aria-label="Location records" onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = '' }} />
          {n > 0 && <button className="btn" onClick={() => attempt(() => remove().unwrap(), 'Location records removed')}><Trash2 size={14} className="text-rose-300" aria-hidden /> Remove all</button>}
        </>}
      </div>
      {n > 0 && (
        <form className="flex flex-wrap items-end gap-3" aria-label="Look up a number" onSubmit={(e) => { e.preventDefault(); if (num.trim()) find(num.trim()) }}>
          <Field label="Look up a number" htmlFor="crn"><input id="crn" className="input w-48 font-mono" value={num} placeholder="7175550100" onChange={(e) => setNum(e.target.value)} /></Field>
          <button className="btn" disabled={isFetching}><Search size={14} aria-hidden /> Look up</button>
          {found && (found.found
            ? <span className="text-sm text-emerald-200">{[found.found.civic.number, found.found.civic.preDir, found.found.civic.street, found.found.civic.streetType].filter(Boolean).join(' ')}{found.found.civic.unit ? `, ${found.found.civic.unit}` : ''}, {found.found.civic.city} {found.found.civic.state}{found.found.lat ? <span className="ml-2 font-mono text-xs text-slate-400">{found.found.lat}, {found.found.lon}</span> : null}</span>
            : <span className="text-sm text-amber-300">No record for this number.</span>)}
        </form>
      )}
    </section>
  )
}

function ValidationResult({ v }: { v: Validation }) {
  const ok = v.matched && v.invalid.length === 0
  return (
    <div role="status" className={`rounded-lg border p-3 text-sm ${ok ? 'border-emerald-800 bg-emerald-950/30' : 'border-rose-800 bg-rose-950/30'}`}>
      <p className="flex items-center gap-2 font-semibold">
        {ok ? <CheckCircle2 size={16} className="text-emerald-300" aria-hidden /> : <XCircle size={16} className="text-rose-300" aria-hidden />}
        <span className={ok ? 'text-emerald-200' : 'text-rose-200'}>{ok ? `Valid: found as ${article(v.matched!)}` : v.matched ? `Found as ${article(v.matched)}, but some parts do not match` : 'Not a valid address'}</span>
      </p>
      <dl className="mt-2 grid gap-1 sm:grid-cols-3">
        {([['Valid', v.valid, 'text-emerald-300'], ['Not valid', v.invalid, 'text-rose-300'], ['Not checked', v.unchecked, 'text-slate-400']] as const).map(([label, list, color]) => (
          <div key={label}><dt className="text-xs text-slate-500">{label}</dt><dd className={color}>{list.length ? list.map((k) => tokenName[k] ?? k).join(', ') : '—'}</dd></div>
        ))}
      </dl>
      {v.found && <p className="mt-2 text-slate-300">In the GIS data: <span className="text-slate-100">{[v.found.HNO, v.found.PRD, v.found.RD, v.found.STS, v.found.POD].filter(Boolean).join(' ')}, {[v.found.A3, v.found.A1, v.found.PC].filter(Boolean).join(' ')}</span></p>}
    </div>
  )
}

/** What the BCF refused or marked lately. */
function BCFLog() {
  const { data } = useBcfLogQuery(undefined, { pollingInterval: 10000 })
  if (!data?.items.length) return null
  return (
    <section className="card mt-6 space-y-3 p-5" aria-labelledby="cb-h">
      <h2 id="cb-h" className="flex items-center gap-2 font-semibold text-slate-100"><ShieldAlert size={17} className="text-rose-300" aria-hidden /> Border control: latest</h2>
      <table className="w-full text-sm"><thead className="text-left text-xs text-slate-500"><tr><th className="py-1">When</th><th>From address</th><th>Caller</th><th>Done</th><th>Why</th></tr></thead>
        <tbody>{data.items.slice(0, 50).map((x, i) => (
          <tr key={i} className="border-t border-slate-800">
            <td className="py-1 text-slate-400">{new Date(x.at).toLocaleString()}</td>
            <td className="font-mono text-xs text-amber-300">{x.source}</td>
            <td className="text-slate-300">{x.from}</td>
            <td>{x.action === 'refused' ? <Badge status="fail">refused</Badge> : <Badge status="warn">marked</Badge>}</td>
            <td className="text-slate-400">{x.why}</td>
          </tr>))}</tbody></table>
    </section>
  )
}
