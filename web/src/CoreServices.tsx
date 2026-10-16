import { ArrowDown, ArrowUp, CheckCircle2, FileSignature, Flag, ListOrdered, Map as MapIcon, MapPinCheck, Network, PhoneCall, Plus, Save, Search, Shield, ShieldAlert, Trash2, Upload, Waypoints, XCircle } from 'lucide-react'
import { useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  api, errorOf, useBoundariesQuery, useDeleteBoundaryMutation, useLazyFindAreaQuery, useSaveBoundaryMutation, useSaveSettingsMutation, useSettingsQuery, useTrunksQuery, useSchedulesQuery,
  useAddressDataQuery, useDeleteAddressesMutation, useValidateAddressMutation, useBcfLogQuery, useLisDataQuery, useLazyLisDataQuery, useDeleteLISMutation, useReceivedLogQuery,
  useCorePoliciesQuery, useStorePolicyMutation, useDeletePolicyMutation, useDiscrepanciesQuery, useResolveDiscrepancyMutation, useFileDiscrepancyMutation, useBadActorsQuery, useClearBadActorMutation,
  useDequeueRegsQuery, useAdrRecordsQuery, useAddADRMutation, useDeleteADRMutation, useLocatorRecordsQuery, useAddLocatorMutation, useDeleteLocatorMutation, useTestCallRequestsQuery, useRunTestCallMutation, useDeleteTestCallsMutation,
  type ApiErrorBody, type Boundary, type CoreRule, type CoreSettings, type Validation, type DiscrepancyReport,
} from './api'
import { attempt, useIsAdmin } from './Editor'
import { store } from './store'
import { useDraft } from './useDraft'
import { Badge, Empty, Field, Page, StaleBanner, Toggle, problemFor } from './ui'

const blankBCF = { enabled: false, perMinute: 0, concurrent: 0, deny: [] as string[], mark: false, verifyIdentity: false, badActors: false, badActorBlock: false, badActorScore: 0, badActorHours: 0 }
const blankCore: CoreSettings = { ecrf: false, lis: false, logging: false, pcaRoots: '', source: '', allowFrom: [], psaps: [], default: '', lostUrl: '', forestGuide: '', rules: [], bcf: blankBCF,
  policyStore: false, queueUri: '', discrepancy: false, drContact: '', geocode: false, msag: false, adr: false, locator: false, dequeue: false, testCalls: false, maxDequeueS: 0 }
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
      <ReceivedLog />
      <BCFLog />
      <I3Services />
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
        <Toggle checked={draft.logging} onChange={(v) => set({ logging: v })} label="Run an i3 logging service (NENA-STA-010)" />
        <p className="text-sm text-slate-400">The other elements send their log events, signed (JWS), to <span className="font-mono text-amber-300">{origin}/i3log/LogEvents</span>; they are kept and can be read back by ID, call or incident. The same addresses only.</p>
        <Field label="PSAP Credentialing Agency roots" htmlFor="cspca" error={p('pcaRoots')} hint="The PCA's root certificates, PEM. With them, a log event is taken only when its signer's certificate comes from the PCA; without, the signature alone is checked.">
          <textarea id="cspca" className="input h-24 font-mono text-xs" value={draft.pcaRoots} placeholder="-----BEGIN CERTIFICATE-----" onChange={(e) => set({ pcaRoots: e.target.value })} />
        </Field>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="May ask (your ESInet)" htmlFor="csa" error={p('allowFrom') ?? err?.problems?.find((x) => x.field.startsWith('system.core.allowFrom['))?.message} hint="Addresses or ranges, separated by commas.">
            <input id="csa" className="input font-mono" value={allow ?? draft.allowFrom.join(', ')} placeholder="10.20.0.0/16" onChange={(e) => setAllow(e.target.value)} />
          </Field>
          <Field label="ECRF name" htmlFor="css" hint="This ECRF's name in answers; blank uses the server's name."><input id="css" className="input" value={draft.source} onChange={(e) => set({ source: e.target.value.trim() })} /></Field>
          <Field label="Forest guide" htmlFor="csfg" error={p('forestGuide')} hint="Where a query for a place none of your areas covers goes (RFC 5582): fetched there for a recursive query, a redirect otherwise. Blank: notFound.">
            <input id="csfg" className="input font-mono" value={draft.forestGuide} placeholder="https://fg.nationwide.example/lost" onChange={(e) => set({ forestGuide: e.target.value.trim() })} />
          </Field>
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
          <Toggle checked={!!draft.bcf.badActors} onChange={(v) => set({ bcf: { ...draft.bcf, badActors: v } })} label="Mark each source and take bad actor reports (Call-Info emergency-source, /i3/BadActor/v1/BadActors)" />
          {draft.bcf.badActors && <div className="grid gap-3 md:grid-cols-3">
            <Toggle checked={!!draft.bcf.badActorBlock} onChange={(v) => set({ bcf: { ...draft.bcf, badActorBlock: v } })} label="Refuse a reported source's calls (else raise their suspicion)" />
            {!draft.bcf.badActorBlock && <Field label="Raise suspicion by" htmlFor="cbbs" error={p('bcf.badActorScore')} hint="0 to 100 (50 when blank), for the policy rules' suspicion conditions."><input id="cbbs" className="input w-24" inputMode="numeric" value={draft.bcf.badActorScore || ''} placeholder="50" onChange={(e) => set({ bcf: { ...draft.bcf, badActorScore: Number(e.target.value.replace(/\D/g, '')) || 0 } })} /></Field>}
            <Field label="A report lasts (hours)" htmlFor="cbbh" hint="24 when blank."><input id="cbbh" className="input w-24" inputMode="numeric" value={draft.bcf.badActorHours || ''} placeholder="24" onChange={(e) => set({ bcf: { ...draft.bcf, badActorHours: Number(e.target.value.replace(/\D/g, '')) || 0 } })} /></Field>
          </div>}
        </>}
      </div>
      <div className="space-y-3">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-violet-300"><Waypoints size={15} className="text-sky-300" aria-hidden /> NENA i3 web services</h3>
        <p className="text-sm text-slate-400">The other elements of the ESInet use these at <span className="font-mono text-amber-300">{origin}/i3/&lt;service&gt;/v1</span>, as NENA-STA-010.3 defines them. Only the addresses in <span className="text-slate-200">May ask</span> above get an answer.</p>
        <div className="grid gap-2 md:grid-cols-2">
          <Toggle checked={!!draft.policyStore} onChange={(v) => set({ policyStore: v })} label="Policy Store: keep signed routing policies, and the ESRP runs them" />
          <Toggle checked={!!draft.discrepancy} onChange={(v) => set({ discrepancy: v })} label="Discrepancy reports: take them, and answer them" />
          <Toggle checked={!!draft.geocode} onChange={(v) => set({ geocode: v })} label="Geocode conversion, from the address points below" />
          <Toggle checked={!!draft.msag} onChange={(v) => set({ msag: v })} label="MSAG conversion, from the centerlines' MSAG communities" />
          <Toggle checked={!!draft.adr} onChange={(v) => set({ adr: v })} label="Additional Data by caller (identity-searchable ADR)" />
          <Toggle checked={!!draft.locator} onChange={(v) => set({ locator: v })} label="Service/agency locator" />
          <Toggle checked={!!draft.dequeue} onChange={(v) => set({ dequeue: v })} label="Dequeue registration: positions sign up to take a queue's calls" />
          <Toggle checked={!!draft.testCalls} onChange={(v) => set({ testCalls: v })} label="Test call generator: PSAPs ask for test calls" />
        </div>
        <div className="grid gap-3 md:grid-cols-3">
          {draft.policyStore && <Field label="This ESRP's incoming queue" htmlFor="csq" error={p('queueUri')} hint="The policyQueueName of the OriginationRoutePolicy run first. Blank: sip:sos@ and the ECRF name.">
            <input id="csq" className="input font-mono" value={draft.queueUri ?? ''} placeholder="sip:esrp@esrp.state.example.gov" onChange={(e) => set({ queueUri: e.target.value.trim() })} /></Field>}
          {draft.discrepancy && <Field label="Who answers discrepancy reports" htmlFor="csdr" error={p('drContact')} hint="A name, and an email or phone, separated by commas: sent as a jCard.">
            <input id="csdr" className="input" value={draft.drContact ?? ''} placeholder="Core services desk, core@state.example.gov" onChange={(e) => set({ drContact: e.target.value })} /></Field>}
          {draft.dequeue && <Field label="Longest registration (seconds)" htmlFor="csdq" error={p('maxDequeueS')} hint="86400 (a day) when blank.">
            <input id="csdq" className="input w-28" inputMode="numeric" value={draft.maxDequeueS || ''} placeholder="86400" onChange={(e) => set({ maxDequeueS: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>}
        </div>
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

/** What the i3 logging service has taken, by day. */
function ReceivedLog() {
  const [day, setDay] = useState(new Date().toISOString().slice(0, 10))
  const { data } = useReceivedLogQuery(day, { pollingInterval: 10000 })
  const items = data?.items ?? []
  return (
    <section className="card mt-6 space-y-3 p-5" aria-labelledby="cg-h">
      <h2 id="cg-h" className="flex items-center gap-2 font-semibold text-slate-100"><ListOrdered size={17} className="text-sky-300" aria-hidden /> Log events received</h2>
      <Field label="Day (UTC)" htmlFor="cgd"><input id="cgd" type="date" className="input w-48" value={day} onChange={(e) => setDay(e.target.value)} /></Field>
      {!items.length ? <p className="text-sm text-slate-400">No log events on this day.</p> : (
        <table className="w-full text-sm"><thead className="text-left text-xs text-slate-500"><tr><th className="py-1">Received</th><th>Event</th><th>From</th><th>Signed by</th><th>Call</th></tr></thead>
          <tbody>{items.map((e) => (
            <tr key={e.logEventId} className="border-t border-slate-800">
              <td className="py-1 font-mono text-xs text-slate-400">{new Date(e.received).toLocaleTimeString()}</td>
              <td><Badge status="info">{String(e.type).replace(/LogEvent$/, '')}</Badge> <span className="text-xs text-slate-500">{e.elementId}</span></td>
              <td className="font-mono text-xs text-slate-400">{e.from}</td>
              <td>{e.signer} {e.chained ? <Badge status="ok">PCA</Badge> : <Badge status="warn">not traced to the PCA</Badge>}</td>
              <td className="font-mono text-xs text-slate-500">{e.callId?.split(':')[4]}</td>
            </tr>))}</tbody></table>
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

const tabs = ['Policies', 'Discrepancy reports', 'Bad actors', 'Dequeue', 'Additional Data', 'Locator', 'Test calls'] as const
type Tab = (typeof tabs)[number]

/** What the i3 web services hold: stored policies, reports, bad actors, registrations, records. */
function I3Services() {
  const [tab, setTab] = useState<Tab>('Policies')
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="ci-h">
      <h2 id="ci-h" className="flex items-center gap-2 font-semibold text-slate-100"><Waypoints size={17} className="text-sky-300" aria-hidden /> i3 web services</h2>
      <div className="flex flex-wrap gap-2" role="tablist" aria-label="i3 web services">
        {tabs.map((x) => <button key={x} role="tab" aria-selected={tab === x} className={tab === x ? 'btn btn-primary' : 'btn'} onClick={() => setTab(x)}>{x}</button>)}
      </div>
      {tab === 'Policies' ? <Policies /> : tab === 'Discrepancy reports' ? <Discrepancies /> : tab === 'Bad actors' ? <BadActors /> : tab === 'Dequeue' ? <Dequeue />
        : tab === 'Additional Data' ? <ADR /> : tab === 'Locator' ? <Locator /> : <TestCalls />}
    </section>
  )
}

const examplePolicy = JSON.stringify({ policyOwner: 'esrp.state.example.gov', policyType: 'OriginationRoutePolicy', policyQueueName: 'sip:esrp@esrp.state.example.gov',
  policyRules: [{ id: 'default', priority: 0, actions: [{ actionType: 'RouteAction', recipientUri: 'sip:sos@psap.county.example.gov' }] }] }, null, 2)

function Policies() {
  const admin = useIsAdmin()
  const { data } = useCorePoliciesQuery()
  const [store, { isLoading }] = useStorePolicyMutation()
  const [del] = useDeletePolicyMutation()
  const [text, setText] = useState('')
  const [open, setOpen] = useState<string | null>(null)
  const submit = async () => {
    const v = text.trim()
    let body: { jws?: string; policy?: unknown } = { jws: v }
    if (v.startsWith('{')) {
      try { const o = JSON.parse(v); body = o.protected && o.signature ? { jws: v } : { policy: o } } catch { toast.error('That is not JSON'); return }
    }
    try { const r = await store(body).unwrap(); toast.success(`Stored: ${r.owner} ${r.key}`); setText('') } catch (e) { toast.error(errorOf(e).message) }
  }
  return (
    <div className="space-y-3">
      <p className="text-sm text-slate-400">Routing policies (Policy Routing Rules), each signed by the agency that owns it. The ESRP runs the <span className="text-slate-200">OriginationRoutePolicy</span> for its incoming queue first, then the <span className="text-slate-200">NormalNextHopRoutePolicy</span> of the PSAP the ECRF chose; without one, the policy rules above decide.</p>
      {!data?.items?.length ? <Empty icon={FileSignature} title="No policies stored">Agencies store them at /i3/PolicyStore/v1/Policies, or paste one below.</Empty> : (
        <table className="w-full text-sm" aria-label="Stored policies">
          <thead><tr className="text-left text-slate-400"><th className="py-1">Owner</th><th>Type</th><th>Queue or ID</th><th>Signed by</th><th>Rules</th><th></th></tr></thead>
          <tbody>
            {data.items.map((x) => (
              <tr key={x.owner + x.key} className="border-t border-slate-800 align-top">
                <td className="py-1.5">{x.owner}</td><td>{x.type}{x.expired && <Badge status="fail">expired</Badge>}</td><td className="font-mono text-xs">{x.queue || x.id}</td>
                <td>{x.signer} {x.chained ? <Badge status="ok">PCA</Badge> : <Badge status="warn">not traced to the PCA</Badge>}</td>
                <td>
                  <button className="btn-ghost text-sky-300 underline" onClick={() => setOpen(open === x.key ? null : x.key)}>{x.rules?.length ?? 0} rules</button>
                  {open === x.key && <ol className="mt-1 space-y-1 text-xs">{[...(x.rules ?? [])].sort((a, b) => b.priority - a.priority).map((r) => (
                    <li key={r.id} className="rounded border border-slate-800 p-1.5"><span className="font-semibold text-slate-200">{r.id}</span> <span className="text-slate-500">priority {r.priority}</span>
                      <div className="text-slate-400">if {r.conditions?.length ? r.conditions.map((c) => String(c.conditionType)).join(' and ') : 'always'}: {r.actions.map((a) => String(a.actionType) + (a.recipientUri ? ' ' + a.recipientUri : '')).join(', ')}</div></li>))}</ol>}
                </td>
                <td>{admin && <button className="btn-ghost p-1" aria-label={`Delete the ${x.type} of ${x.owner}`} onClick={() => del({ policyOwner: x.owner, policyType: x.type, policyQueueName: x.queue || undefined, policyId: x.id || undefined }).unwrap().then(() => toast.success('Deleted')).catch((e) => toast.error(errorOf(e).message))}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>}</td>
              </tr>))}
          </tbody>
        </table>)}
      {admin && <>
        <Field label="Store a policy" htmlFor="cpol" hint="A JWS signed by the policy's owner (flattened JSON, or compact), or a policy object to sign with this server's certificate (its owner must be this server's name).">
          <textarea id="cpol" className="input h-40 font-mono text-xs" value={text} placeholder={examplePolicy} onChange={(e) => setText(e.target.value)} />
        </Field>
        <div className="flex justify-end"><button className="btn btn-primary" disabled={isLoading || !text.trim()} onClick={submit}><FileSignature size={14} aria-hidden /> Store</button></div>
      </>}
    </div>
  )
}

const resolutions = ['DiscrepancyCorrected', 'NoDiscrepancy', 'DataCorrected', 'RecordsCorrected', 'PolicyCorrected', 'PolicyUpdated', 'ProblemCorrected', 'Acknowledged', 'NoError', 'OtherResponse']
const reportTypes = ['LostDiscrepancyReport', 'GisDiscrepancyReport', 'LisDiscrepancyReport', 'PolicyDiscrepancyReport', 'EsrpDiscrepancyReport', 'BcfDiscrepancyReport', 'SipDiscrepancyReport',
  'CallTransferDiscrepancyReport', 'AdrDiscrepancyReport', 'NetworkDiscrepancyReport', 'OriginatingServiceDiscrepancyReport', 'TestCallDiscrepancyReport', 'LoggingDiscrepancyReport']

function Discrepancies() {
  const admin = useIsAdmin()
  const { data } = useDiscrepanciesQuery()
  const [resolve] = useResolveDiscrepancyMutation()
  const [file, { isLoading }] = useFileDiscrepancyMutation()
  const [answer, setAnswer] = useState<Record<string, { resolution: string; comments: string }>>({})
  const [to, setTo] = useState('')
  const [kind, setKind] = useState(reportTypes[0])
  const [severity, setSeverity] = useState('Minor')
  const [service, setService] = useState('')
  const [comments, setComments] = useState('')
  const doFile = () => attempt(() => file({ to, report: { reportType: kind, problemSeverity: severity, problemService: service, problemComments: comments } }).unwrap(), 'Report filed', { problemsInToast: true }).then(() => { setComments('') })
  const rows = (dir: DiscrepancyReport['direction']) => (data?.items ?? []).filter((d) => d.direction === dir)
  return (
    <div className="space-y-4">
      <h3 className="text-sm font-semibold text-violet-300">Reports about this server's services</h3>
      {!rows('received').length ? <p className="text-sm text-slate-500">None. Other agencies file them at /i3/DiscrepancyReports/v1/Reports.</p> : (
        <ul className="space-y-2" aria-label="Reports received">
          {rows('received').map((d) => (
            <li key={d.id} className="rounded-lg border border-slate-800 p-3 text-sm" aria-label={`Report ${d.id}`}>
              <div className="flex flex-wrap items-center gap-2"><Flag size={14} className="text-amber-300" aria-hidden /><span className="font-semibold text-slate-200">{String(d.report.reportType)}</span>
                <span className="text-slate-400">from {String(d.report.reportingAgencyName)}, {new Date(d.at).toLocaleString()}</span>
                <Badge status={d.status === 'resolved' ? 'ok' : 'warn'}>{d.status}</Badge> <span className="text-slate-500">{String(d.report.problemSeverity)}</span></div>
              <p className="mt-1 text-slate-300">{String(d.report.problemComments ?? d.report.problem ?? '')}</p>
              {d.resolution ? <p className="text-xs text-emerald-300">Resolved: {String(d.resolution.resolution)} {d.resolution.responseComments ? `— ${String(d.resolution.responseComments)}` : ''}</p>
                : admin && <div className="mt-2 flex flex-wrap items-end gap-2">
                  <Field label="Resolution" htmlFor={`dr-r-${d.id}`}><select id={`dr-r-${d.id}`} className="input" value={answer[d.id]?.resolution ?? ''} onChange={(e) => setAnswer((a) => ({ ...a, [d.id]: { comments: a[d.id]?.comments ?? '', resolution: e.target.value } }))}>
                    <option value="">Choose…</option>{resolutions.map((r) => <option key={r}>{r}</option>)}</select></Field>
                  <Field label="Comments" htmlFor={`dr-c-${d.id}`}><input id={`dr-c-${d.id}`} className="input" value={answer[d.id]?.comments ?? ''} onChange={(e) => setAnswer((a) => ({ ...a, [d.id]: { resolution: a[d.id]?.resolution ?? '', comments: e.target.value } }))} /></Field>
                  <button className="btn" disabled={!answer[d.id]?.resolution} onClick={() => attempt(() => resolve({ id: d.id, ...answer[d.id] }).unwrap(), 'Resolution sent to the reporter', { problemsInToast: true })}>Send the resolution</button>
                </div>}
              {d.error && <p className="text-xs text-rose-300">{d.error}</p>}
            </li>))}
        </ul>)}
      <h3 className="text-sm font-semibold text-violet-300">Reports this server filed</h3>
      {!rows('filed').length ? <p className="text-sm text-slate-500">None yet.</p> : (
        <ul className="space-y-1 text-sm" aria-label="Reports filed">
          {rows('filed').map((d) => <li key={d.id}>{String(d.report.reportType)} to <span className="font-mono text-xs">{d.to}</span>, {new Date(d.at).toLocaleString()} <Badge status={d.status === 'resolved' ? 'ok' : d.error ? 'fail' : 'warn'}>{d.error ? 'not delivered' : d.status}</Badge>
            {d.resolution && <span className="text-emerald-300"> {String(d.resolution.resolution)}</span>}{d.error && <span className="text-xs text-rose-300"> {d.error}</span>}</li>)}
        </ul>)}
      {admin && <div className="grid items-end gap-3 rounded-lg border border-slate-800 p-3 md:grid-cols-3">
        <Field label="File a report with" htmlFor="drto" hint="The other agency's DR service."><input id="drto" className="input font-mono" value={to} placeholder="https://ecrf.state.example.gov/i3/DiscrepancyReports/v1" onChange={(e) => setTo(e.target.value.trim())} /></Field>
        <Field label="About" htmlFor="drkind"><select id="drkind" className="input" value={kind} onChange={(e) => setKind(e.target.value)}>{reportTypes.map((r) => <option key={r} value={r}>{r.replace('DiscrepancyReport', '')}</option>)}</select></Field>
        <Field label="Severity" htmlFor="drsev"><select id="drsev" className="input" value={severity} onChange={(e) => setSeverity(e.target.value)}>{['Minor', 'Major', 'Critical'].map((x) => <option key={x}>{x}</option>)}</select></Field>
        <Field label="The service" htmlFor="drsvc" hint="Its name or address."><input id="drsvc" className="input" value={service} onChange={(e) => setService(e.target.value)} /></Field>
        <Field label="What is wrong" htmlFor="drcom"><input id="drcom" className="input" value={comments} onChange={(e) => setComments(e.target.value)} /></Field>
        <button className="btn btn-primary" disabled={isLoading || !to || !comments} onClick={doFile}><Flag size={14} aria-hidden /> File</button>
      </div>}
    </div>
  )
}

function BadActors() {
  const admin = useIsAdmin()
  const { data } = useBadActorsQuery()
  const [clear] = useClearBadActorMutation()
  return !data?.items?.length ? <Empty icon={ShieldAlert} title="No bad actors reported">PSAPs report a source by the ID its calls carry (Call-Info, purpose emergency-source).</Empty> : (
    <table className="w-full text-sm" aria-label="Bad actors">
      <thead><tr className="text-left text-slate-400"><th className="py-1">Source</th><th>Source ID</th><th>Reported by</th><th>Until</th><th></th></tr></thead>
      <tbody>{data.items.map((b) => <tr key={b.sourceId} className="border-t border-slate-800"><td className="py-1.5 font-mono">{b.source}</td><td className="font-mono text-xs">{b.sourceId}</td><td>{b.by}</td><td>{new Date(b.until).toLocaleString()}</td>
        <td>{admin && <button className="btn py-0.5" onClick={() => attempt(() => clear(b.sourceId).unwrap(), 'Lifted', { problemsInToast: true })}>Lift</button>}</td></tr>)}</tbody>
    </table>)
}

function Dequeue() {
  const { data } = useDequeueRegsQuery()
  return !data?.items?.length ? <Empty icon={PhoneCall} title="No positions registered">They register at /i3/DequeueRegistration/v1/Registrations for a PSAP's queue; the ESRP then sends that queue's calls to them, the most preferred first.</Empty> : (
    <table className="w-full text-sm" aria-label="Dequeue registrations">
      <thead><tr className="text-left text-slate-400"><th className="py-1">Queue</th><th>Taker</th><th>Preference</th><th>Until</th></tr></thead>
      <tbody>{data.items.map((d) => <tr key={d.queueUri + d.dequeuerUri} className="border-t border-slate-800"><td className="py-1.5 font-mono text-xs">{d.queueUri}</td><td className="font-mono text-xs">{d.dequeuerUri}</td><td>{d.dequeuePreference}</td><td>{new Date(d.expires).toLocaleTimeString()}</td></tr>)}</tbody>
    </table>)
}

const adrTypes = ['EmergencyCallData.SubscriberInfo', 'EmergencyCallData.ServiceInfo', 'EmergencyCallData.DeviceInfo', 'EmergencyCallData.ProviderInfo', 'EmergencyCallData.Comment']

function ADR() {
  const admin = useIsAdmin()
  const { data } = useAdrRecordsQuery()
  const [add, { isLoading }] = useAddADRMutation()
  const [del] = useDeleteADRMutation()
  const [caller, setCaller] = useState('')
  const [type, setType] = useState(adrTypes[0])
  const [xml, setXml] = useState('')
  return (
    <div className="space-y-3">
      <p className="text-sm text-slate-400">Additional Data kept by caller, for ESInet elements that look a caller up at /i3/Adr/v1/AdditionalData?callerUri= (an identity-searchable ADR).</p>
      {!!data?.items?.length && <table className="w-full text-sm" aria-label="Additional Data records">
        <thead><tr className="text-left text-slate-400"><th className="py-1">Caller</th><th>Block</th><th>Added</th><th></th></tr></thead>
        <tbody>{data.items.map((r) => <tr key={r.id} className="border-t border-slate-800"><td className="py-1.5 font-mono">{r.caller}</td><td>{r.type.replace('EmergencyCallData.', '')}</td><td>{new Date(r.added).toLocaleDateString()} by {r.addedBy}</td>
          <td>{admin && <button className="btn-ghost p-1" aria-label={`Delete the ${r.type} of ${r.caller}`} onClick={() => attempt(() => del(r.id).unwrap(), 'Deleted', { problemsInToast: true })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>}</td></tr>)}</tbody>
      </table>}
      {admin && <div className="grid gap-3 md:grid-cols-2">
        <Field label="Caller" htmlFor="adrc" hint="tel:+number or sip:user@domain"><input id="adrc" className="input font-mono" value={caller} placeholder="tel:+17175550100" onChange={(e) => setCaller(e.target.value.trim())} /></Field>
        <Field label="Block" htmlFor="adrt"><select id="adrt" className="input" value={type} onChange={(e) => setType(e.target.value)}>{adrTypes.map((x) => <option key={x} value={x}>{x.replace('EmergencyCallData.', '')}</option>)}</select></Field>
        <div className="md:col-span-2"><Field label="The block (XML, RFC 7852)" htmlFor="adrx"><textarea id="adrx" className="input h-28 font-mono text-xs" value={xml} onChange={(e) => setXml(e.target.value)} /></Field></div>
        <div className="md:col-span-2 flex justify-end"><button className="btn btn-primary" disabled={isLoading || !caller || !xml} onClick={() => attempt(() => add({ caller, type, xml }).unwrap(), 'Kept', { problemsInToast: true }).then(() => setXml(''))}><Plus size={14} aria-hidden /> Keep</button></div>
      </div>}
    </div>
  )
}

function Locator() {
  const admin = useIsAdmin()
  const { data } = useLocatorRecordsQuery()
  const [add, { isLoading }] = useAddLocatorMutation()
  const [del] = useDeleteLocatorMutation()
  const [x, setX] = useState({ serviceAgencyId: '', serviceAgencyName: '', types: 'psap', loggerUri: '', dscRptUri: '', emergencySipInterfaceUri: '' })
  return (
    <div className="space-y-3">
      <p className="text-sm text-slate-400">Agencies and services others find at /i3/ServiceAgencyLocator/v1: this agency (from its 911 settings) and those added here.</p>
      {!data?.items?.length ? <p className="text-sm text-slate-500">Nothing yet: set this agency's ID on the 911 calls page, or add one below.</p> : (
        <table className="w-full text-sm" aria-label="Locator records">
          <thead><tr className="text-left text-slate-400"><th className="py-1">Agency</th><th>ID</th><th>Types</th><th>Logging service</th><th></th></tr></thead>
          <tbody>{data.items.map((r) => <tr key={r.recordId} className="border-t border-slate-800"><td className="py-1.5">{r.serviceAgencyName}</td><td className="font-mono text-xs">{r.serviceAgencyId}</td><td>{r.serviceAgencyTypes.join(', ')}</td><td className="font-mono text-xs">{r.loggerUri}</td>
            <td>{admin && r.recordId !== 'self' && <button className="btn-ghost p-1" aria-label={`Delete ${r.serviceAgencyName}`} onClick={() => attempt(() => del(r.recordId).unwrap(), 'Deleted', { problemsInToast: true })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>}</td></tr>)}</tbody>
        </table>)}
      {admin && <div className="grid items-end gap-3 md:grid-cols-3">
        <Field label="Agency ID" htmlFor="loci"><input id="loci" className="input font-mono" value={x.serviceAgencyId} placeholder="police.county.example.gov" onChange={(e) => setX({ ...x, serviceAgencyId: e.target.value.trim() })} /></Field>
        <Field label="Name" htmlFor="locn"><input id="locn" className="input" value={x.serviceAgencyName} onChange={(e) => setX({ ...x, serviceAgencyName: e.target.value })} /></Field>
        <Field label="Types" htmlFor="loct" hint="psap, police, fire, ems ..., by commas."><input id="loct" className="input" value={x.types} onChange={(e) => setX({ ...x, types: e.target.value })} /></Field>
        <Field label="Logging service" htmlFor="locl"><input id="locl" className="input font-mono" value={x.loggerUri} placeholder="https://log.county.example.gov/i3log" onChange={(e) => setX({ ...x, loggerUri: e.target.value.trim() })} /></Field>
        <Field label="Discrepancy reports" htmlFor="locd"><input id="locd" className="input font-mono" value={x.dscRptUri} onChange={(e) => setX({ ...x, dscRptUri: e.target.value.trim() })} /></Field>
        <Field label="Emergency calls to" htmlFor="locs"><input id="locs" className="input font-mono" value={x.emergencySipInterfaceUri} placeholder="sip:sos@police.county.example.gov" onChange={(e) => setX({ ...x, emergencySipInterfaceUri: e.target.value.trim() })} /></Field>
        <div className="md:col-span-3 flex justify-end"><button className="btn btn-primary" disabled={isLoading || !x.serviceAgencyId || !x.serviceAgencyName} onClick={() => attempt(() => add({ serviceAgencyId: x.serviceAgencyId, serviceAgencyName: x.serviceAgencyName, serviceAgencyTypes: commas(x.types), loggerUri: x.loggerUri, dscRptUri: x.dscRptUri || undefined, emergencySipInterfaceUri: x.emergencySipInterfaceUri || undefined }).unwrap(), 'Added', { problemsInToast: true })}><Plus size={14} aria-hidden /> Add</button></div>
      </div>}
    </div>
  )
}

function TestCalls() {
  const admin = useIsAdmin()
  const { data } = useTestCallRequestsQuery()
  const [run, { isLoading }] = useRunTestCallMutation()
  const [del] = useDeleteTestCallsMutation()
  return !data?.items?.length ? <Empty icon={PhoneCall} title="No PSAP has asked for test calls">A PSAP asks at /i3/TestCall/v1/SendTestCalls/&lt;its agency ID&gt;: a call to urn:service:test.sos every so many minutes, with the location it gives.</Empty> : (
    <ul className="space-y-3" aria-label="Test call requests">
      {data.items.map((r) => (
        <li key={r.psapId} className="rounded-lg border border-slate-800 p-3 text-sm" aria-label={`Test calls to ${r.psapId}`}>
          <div className="flex flex-wrap items-center gap-2"><PhoneCall size={14} className="text-emerald-300" aria-hidden /><span className="font-semibold text-slate-200">{r.psapId}</span>
            <span className="text-slate-400">every {r.frequency} minutes, {new Date(r.startDate).toLocaleDateString()} to {new Date(r.endDate).toLocaleDateString()}</span>
            {admin && <><button className="btn py-0.5" disabled={isLoading} onClick={() => run(r.psapId).unwrap().then((x) => (x.ok ? toast.success('Test call answered: ' + x.echoed + ' packets back') : toast.error(x.problem ?? 'The test call failed'))).catch((e) => toast.error(errorOf(e).message))}>Place one now</button>
              <button className="btn py-0.5" onClick={() => attempt(() => del(r.psapId).unwrap(), 'Stopped', { problemsInToast: true })}>Stop</button></>}</div>
          {!!r.results?.length && <table className="mt-2 w-full text-xs" aria-label={`Results for ${r.psapId}`}>
            <thead><tr className="text-left text-slate-500"><th>When</th><th>Answer</th><th>Packets back</th><th>Result</th></tr></thead>
            <tbody>{r.results.map((x) => <tr key={x.at} className="border-t border-slate-800 align-top"><td className="py-1">{new Date(x.at).toLocaleString()}</td><td className="whitespace-pre-line">{x.status ? `${x.status} ${x.reason ?? ''}\n` : ''}{x.answer}</td><td>{x.echoed} of {x.sent}</td>
              <td>{x.ok ? <span className="inline-flex items-center gap-1 text-emerald-300"><CheckCircle2 size={12} aria-hidden /> passed</span> : <span className="inline-flex items-center gap-1 text-rose-300"><XCircle size={12} aria-hidden /> {x.problem}</span>}</td></tr>)}</tbody>
          </table>}
        </li>))}
    </ul>)
}
