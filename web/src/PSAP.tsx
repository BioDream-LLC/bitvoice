import { ArrowDown, ArrowUp, Cable, Download, HeartPulse, Pencil, Plus, Printer, ScrollText, Save, Siren, Table2, Timer, Trash2 } from 'lucide-react'
import { Fragment, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useAliParseMutation, useAliTestMutation, useExposureQuery, useCallsQuery, usePsapLogQuery, usePsapReportQuery, useSaveSettingsMutation, useSettingsQuery, type ALIField, type ALILink, type ALIRecordView, type ReportLayout, type ReportColumn, useLazyLaidOutQuery, type ApiErrorBody, type MapSource, type PSAPSettings } from './api'
import { useIsAdmin } from './Editor'
import { EmergencyCard } from './EmergencyCard'
import { useDraft } from './useDraft'
import { Badge, Empty, Field, Page, StaleBanner, problemFor } from './ui'

const blankMap: MapSource = { file: '', url: '', attribution: '' }
const blankPSAP: PSAPSettings = { agencyId: '', elementId: '', aliAddress: '', eidoUrl: '', eidoToken: '', answerTarget: 15, ttyOff: false, locationServer: '', videoLinkBase: '', videoFrom: '', videoStreams: 0, videoKbps: 0, uploadMb: 0, mediaDays: 0, recordVideo: false, testCallsOff: false, aliLink: { address: '', request: '', layout: [], timeoutS: 0 } }

export function PSAP() {
  const admin = useIsAdmin()
  const { data: calls } = useCallsQuery(undefined, { pollingInterval: 2000 })
  const live = (calls?.items ?? []).filter((c) => c.emergency)
  const [tab, setTab] = useState<'report' | 'log' | 'exposure' | 'layouts'>('report')
  return (
    <Page icon={Siren} title="911 calls" accent="from-rose-400 to-amber-400"
      intro="The 911 profile: calls from the emergency services network (ESInet) with the caller's location and the network's data, the answer-time report, the NENA i3 log, and what CAD is sent when a call is answered.">
      <section aria-labelledby="psl-h" className="space-y-3">
        <h2 id="psl-h" className="text-sm font-semibold text-rose-300">Now</h2>
        {!live.length ? <Empty icon={Siren} title="No 911 calls now">Calls from a trunk marked ESInet, or that bring a location, appear here as they ring.</Empty>
          : live.map((c) => <EmergencyCard key={c.id} call={c} />)}
      </section>
      <div className="mt-6 flex gap-2" role="tablist" aria-label="Reports">
        <button role="tab" aria-selected={tab === 'report'} className={tab === 'report' ? 'btn btn-primary' : 'btn'} onClick={() => setTab('report')}><Timer size={14} aria-hidden /> Answer times</button>
        <button role="tab" aria-selected={tab === 'log'} className={tab === 'log' ? 'btn btn-primary' : 'btn'} onClick={() => setTab('log')}><ScrollText size={14} aria-hidden /> i3 log</button>
        <button role="tab" aria-selected={tab === 'exposure'} className={tab === 'exposure' ? 'btn btn-primary' : 'btn'} onClick={() => setTab('exposure')}><HeartPulse size={14} aria-hidden /> Exposure</button>
        <button role="tab" aria-selected={tab === 'layouts'} className={tab === 'layouts' ? 'btn btn-primary' : 'btn'} onClick={() => setTab('layouts')}><Table2 size={14} aria-hidden /> Report layouts</button>
      </div>
      {tab === 'report' ? <Report /> : tab === 'log' ? <I3Log /> : tab === 'layouts' ? <Layouts /> : <ExposureReport />}
      {admin && <PSAPSetup />}
    </Page>
  )
}

const iso = (d: Date) => d.toISOString().slice(0, 10)

function Report() {
  const [from, setFrom] = useState(iso(new Date(Date.now() - 6 * 864e5)))
  const [to, setTo] = useState(iso(new Date()))
  const { data: r } = usePsapReportQuery({ from, to })
  const labels = ['0-10 s', '10-15 s', '15-20 s', '20-40 s', 'over 40 s']
  const most = Math.max(1, ...(r?.buckets ?? [1]))
  return (
    <section className="card mt-3 space-y-4 p-5" aria-label="Answer times">
      <div className="flex flex-wrap items-end gap-3">
        <Field label="From" htmlFor="psf"><input id="psf" type="date" className="input" value={from} onChange={(e) => setFrom(e.target.value)} /></Field>
        <Field label="To" htmlFor="pst"><input id="pst" type="date" className="input" value={to} onChange={(e) => setTo(e.target.value)} /></Field>
        <a className="btn" href={`./api/psap/report?format=csv&from=${from}&to=${to}`} download><Download size={14} className="text-emerald-300" aria-hidden /> A row a day (CSV)</a>
        <a className="btn" href={`./api/psap/report?format=csv&by=call&from=${from}&to=${to}`} download><Download size={14} className="text-emerald-300" aria-hidden /> A row a call (CSV)</a>
      </div>
      {!r || !r.calls ? <p className="text-sm text-slate-400">No 911 calls in these days.</p> : <>
        <div className="grid gap-3 sm:grid-cols-4">
          <Stat label={`Answered within ${r.target} s`} value={`${r.withinTarget.toFixed(1)}%`} status={r.withinTarget >= 90 ? 'ok' : 'fail'} note="goal 90%" />
          <Stat label="Within 20 s" value={`${r.within20.toFixed(1)}%`} status={r.within20 >= 95 ? 'ok' : 'warn'} note="goal 95%" />
          <Stat label="Average to answer" value={`${r.average.toFixed(1)} s`} note={`longest ${Math.round(r.longest)} s`} />
          <Stat label="Calls" value={String(r.calls)} note={`${r.answered} answered, ${r.abandoned} abandoned`} status={r.abandoned ? 'warn' : 'ok'} />
        </div>
        <div className="space-y-1" aria-label="Calls by time to answer">
          {r.buckets.map((n, i) => (
            <div key={i} className="flex items-center gap-3 text-sm">
              <span className="w-20 text-slate-400">{labels[i]}</span>
              <div className="h-3 flex-1 rounded bg-slate-800"><div className={`h-3 rounded ${i < 2 ? 'bg-emerald-500' : i < 3 ? 'bg-amber-500' : 'bg-rose-500'}`} style={{ width: `${(100 * n) / most}%` }} /></div>
              <span className="w-10 text-right font-mono text-slate-300">{n}</span>
            </div>
          ))}
        </div>
        {r.byTaker.length > 0 && (
          <table className="w-full text-sm"><thead className="text-left text-xs text-slate-500"><tr><th className="py-1">Call-taker</th><th>Calls</th><th>Average to answer</th></tr></thead>
            <tbody>{r.byTaker.map((t) => <tr key={t.ext} className="border-t border-slate-800"><td className="py-1"><span className="font-mono text-amber-300">{t.ext}</span> {t.name}</td><td>{t.calls}</td><td>{t.average.toFixed(1)} s</td></tr>)}</tbody></table>
        )}
      </>}
    </section>
  )
}

function Stat({ label, value, note, status }: { label: string; value: string; note?: string; status?: 'ok' | 'warn' | 'fail' }) {
  const color = status === 'ok' ? 'text-emerald-300' : status === 'warn' ? 'text-amber-300' : status === 'fail' ? 'text-rose-300' : 'text-sky-200'
  return <div className="rounded-lg border border-slate-800 p-3"><div className="text-xs text-slate-500">{label}</div><div className={`text-2xl font-semibold ${color}`}>{value}</div>{note && <div className="text-xs text-slate-500">{note}</div>}</div>
}

/** Who has looked at callers' video and photos, and how often: for the centre's wellness programme. */
function ExposureReport() {
  const [from, setFrom] = useState(iso(new Date(Date.now() - 29 * 864e5)))
  const [to, setTo] = useState(iso(new Date()))
  const { data } = useExposureQuery({ from, to })
  const rows = data?.people ?? []
  return (
    <section className="card mt-3 space-y-3 p-5" aria-label="Exposure">
      <p className="text-sm text-slate-400">Every time someone reveals a caller's video or opens a photo or clip, it is counted here: for supervisors and the wellness programme, never for discipline.</p>
      <div className="flex flex-wrap items-end gap-3">
        <Field label="From" htmlFor="pxf"><input id="pxf" type="date" className="input" value={from} onChange={(e) => setFrom(e.target.value)} /></Field>
        <Field label="To" htmlFor="pxt"><input id="pxt" type="date" className="input" value={to} onChange={(e) => setTo(e.target.value)} /></Field>
      </div>
      {!rows.length ? <p className="text-sm text-slate-400">Nobody has looked at callers' video or photos in these days.</p> : (
        <table className="w-full text-sm"><thead className="text-left text-xs text-slate-500"><tr><th className="py-1">Who</th><th>Looks</th><th>Calls</th><th>Last</th></tr></thead>
          <tbody>{rows.map((r) => <tr key={r.who} className="border-t border-slate-800"><td className="py-1 text-slate-200">{r.who}</td><td className={r.looks > 20 ? 'text-amber-300' : 'text-slate-300'}>{r.looks}</td><td>{r.calls}</td><td className="text-xs text-slate-400">{new Date(r.last).toLocaleString()}</td></tr>)}</tbody></table>
      )}
    </section>
  )
}

function I3Log() {
  const [day, setDay] = useState(iso(new Date()))
  const { data } = usePsapLogQuery(day, { pollingInterval: 10000 })
  const days = useMemo(() => Array.from(new Set([iso(new Date()), ...(data?.days ?? [])])), [data])
  return (
    <section className="card mt-3 space-y-3 p-5" aria-label="i3 log">
      <Field label="Day (UTC)" htmlFor="psd"><select id="psd" className="input w-48" value={day} onChange={(e) => setDay(e.target.value)}>{days.map((d) => <option key={d}>{d}</option>)}</select></Field>
      {!data?.items.length ? <p className="text-sm text-slate-400">No events on this day.</p> : (
        <table className="w-full text-sm"><thead className="text-left text-xs text-slate-500"><tr><th className="py-1">Time</th><th>Event</th><th>Call</th><th>From</th><th>Agent</th></tr></thead>
          <tbody>{data.items.map((e, i) => (
            <tr key={i} className="border-t border-slate-800">
              <td className="py-1 font-mono text-xs text-slate-400">{new Date(e.timestamp).toLocaleTimeString()}</td>
              <td><Badge status={e.logEventType.startsWith('Eido') && e.extension?.ok === false ? 'fail' : 'info'}>{e.logEventType.replace(/LogEvent$/, '')}</Badge></td>
              <td className="font-mono text-xs text-slate-500">{e.callId?.split(':')[4]}</td>
              <td className="font-mono text-sky-300">{e.from}</td>
              <td className="text-slate-300">{e.agencyAgentId}</td>
            </tr>))}</tbody></table>
      )}
    </section>
  )
}

function PSAPSetup() {
  const { data } = useSettingsQuery()
  const server = useMemo(() => (data ? { ...blankPSAP, ...(data.system.psap ?? {}), aliLink: { ...blankPSAP.aliLink!, ...(data.system.psap?.aliLink ?? {}), layout: data.system.psap?.aliLink?.layout ?? [] }, map: { ...blankMap, ...(data.system.map ?? {}) } } : undefined), [data])
  const { draft, setDraft, reset, stale } = useDraft<PSAPSettings & { map: MapSource }>(server)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [save, { isLoading }] = useSaveSettingsMutation()
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.psap.' + f)
  const set = (v: Partial<PSAPSettings>) => setDraft((d) => ({ ...d, ...v }))
  const setMap = (v: Partial<MapSource>) => setDraft((d) => ({ ...d, map: { ...d.map, ...v } }))
  const pm = (f: string) => problemFor(err?.problems, 'system.map.' + f)
  const doSave = async () => {
    const { map, ...psap } = draft
    try { const s = await save({ ...data, system: { ...data.system, psap, map } }).unwrap(); reset({ ...blankPSAP, ...(s.system.psap ?? {}), map: { ...blankMap, ...(s.system.map ?? {}) } }); setErr(null); toast.success('911 settings saved') }
    catch (e) { setErr(errorOf(e)) }
  }
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="pss-h">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <h2 id="pss-h" className="font-semibold text-slate-100">911 settings</h2>
      <p className="text-sm text-slate-400">Mark the trunk from your ESInet provider as <span className="text-slate-200">ESInet</span> (Trunks), and send its calls to your call-takers' queue or ring group in Call routing.</p>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="Agency ID" htmlFor="psa" error={p('agencyId')} hint="Your agency's domain, as i3 names agencies."><input id="psa" className="input" value={draft.agencyId} placeholder="psap.county.example.gov" onChange={(e) => set({ agencyId: e.target.value.trim() })} /></Field>
        <Field label="Element ID" htmlFor="pse" hint="This system's name in the i3 log; blank uses the server's name."><input id="pse" className="input" value={draft.elementId} onChange={(e) => set({ elementId: e.target.value.trim() })} /></Field>
        <Field label="Answer goal (seconds)" htmlFor="psg" error={p('answerTarget')} hint="NENA: 90% of calls within 15 s."><input id="psg" className="input font-mono" inputMode="numeric" value={draft.answerTarget || ''} onChange={(e) => set({ answerTarget: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
      </div>
      <Field label="Location lookup (ALI)" htmlFor="psls" error={p('locationServer')} hint="For 911 calls that bring only a callback number (CAMA trunks, or ESInet calls with no location): an ALI database or location server that answers HELD by telephone number (RFC 6155).">
        <input id="psls" className="input font-mono" value={draft.locationServer} placeholder="https://ali.example.gov/held" onChange={(e) => set({ locationServer: e.target.value.trim() })} />
      </Field>
      <ALILinkSetup link={draft.aliLink!} set={(l) => set({ aliLink: l })} p={p} />
      <label className="flex items-center gap-2 text-sm text-slate-300">
        <input type="checkbox" checked={!draft.ttyOff} onChange={(e) => set({ ttyOff: !e.target.checked })} />
        Listen for TTYs on 911 calls <span className="text-xs text-slate-500">(deaf callers' text telephones: their text shows on the call, and replies go back as TTY tones)</span>
      </label>
      <label className="flex items-center gap-2 text-sm text-slate-300">
        <input type="checkbox" checked={!draft.testCallsOff} onChange={(e) => set({ testCallsOff: !e.target.checked })} />
        Answer test calls <span className="text-xs text-slate-500">(urn:service:test.sos from the ESInet: answered with no call-taker, three packets of sound sent back, then hung up)</span>
      </label>
      <h3 className="text-sm font-semibold text-violet-300">CAD</h3>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="ALI spill to" htmlFor="psl" error={p('aliAddress')} hint="CAD's address and port (the ALI text goes there over TCP when a call is answered), or a serial port: serial:/dev/ttyS0?baud=9600."><input id="psl" className="input font-mono" value={draft.aliAddress} placeholder="10.0.0.20:4000" onChange={(e) => set({ aliAddress: e.target.value.trim() })} /></Field>
        <Field label="EIDO to" htmlFor="psu" error={p('eidoUrl')} hint="CAD's address for EIDOs (NENA-STA-024), sent by HTTPS POST. Plain http only to CAD on this computer (http://127.0.0.1:…)."><input id="psu" className="input font-mono" value={draft.eidoUrl} placeholder="https://cad.example.gov/eido" onChange={(e) => set({ eidoUrl: e.target.value.trim() })} /></Field>
        <Field label="EIDO token" htmlFor="pst2" hint="Sent as a bearer token, if CAD asks for one. Blank keeps the saved one."><input id="pst2" type="password" autoComplete="off" className="input font-mono" value={draft.eidoToken} onChange={(e) => set({ eidoToken: e.target.value })} /></Field>
      </div>
      <h3 className="text-sm font-semibold text-violet-300">Caller video and photos</h3>
      <p className="text-sm text-slate-400">A call-taker can send a 911 caller a link: the caller's phone browser shares its camera live, or sends photos and clips (each hashed as it arrives, with its time and GPS read). Callers' phones must reach this server at the link's address, over HTTPS, and its media ports over UDP.</p>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="Link address" htmlFor="pvb" error={p('videoLinkBase')} hint="The https address callers' phones reach this server at. Blank: the address of the page the call-taker uses."><input id="pvb" className="input font-mono" value={draft.videoLinkBase} placeholder="https://video.psap.example.gov" onChange={(e) => set({ videoLinkBase: e.target.value.trim() })} /></Field>
        <Field label="Text the link from" htmlFor="pvf" error={p('videoFrom')} hint="A texting number (Texting). Blank: the call-taker reads the link out."><input id="pvf" className="input font-mono" value={draft.videoFrom} placeholder="+17175550100" onChange={(e) => set({ videoFrom: e.target.value.trim() })} /></Field>
        <Field label="Live videos at once" htmlFor="pvs" error={p('videoStreams')} hint="On this server (4 when blank): never at the expense of calls."><input id="pvs" className="input w-24 font-mono" inputMode="numeric" value={draft.videoStreams || ''} placeholder="4" onChange={(e) => set({ videoStreams: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
        <Field label="Video bit rate (kbit/s)" htmlFor="pvk" error={p('videoKbps')} hint="Each caller's video is asked to stay under it (1500 when blank)."><input id="pvk" className="input w-28 font-mono" inputMode="numeric" value={draft.videoKbps || ''} placeholder="1500" onChange={(e) => set({ videoKbps: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
        <Field label="Largest file (MB)" htmlFor="pvu" error={p('uploadMb')} hint="200 when blank."><input id="pvu" className="input w-24 font-mono" inputMode="numeric" value={draft.uploadMb || ''} placeholder="200" onChange={(e) => set({ uploadMb: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
        <label className="flex items-center gap-2 text-sm text-slate-300 md:col-span-3">
          <input type="checkbox" checked={!!draft.recordVideo} onChange={(e) => set({ recordVideo: e.target.checked })} />
          Record callers' live video <span className="text-xs text-slate-500">(kept with the photos and clips as it was sent, not converted, and hashed when it ends)</span>
        </label>
        <Field label="Keep files and the exposure log (days)" htmlFor="pvd" error={p('mediaDays')} hint="Blank: until deleted. Calls held as evidence are kept."><input id="pvd" className="input w-24 font-mono" inputMode="numeric" value={draft.mediaDays || ''} placeholder="" onChange={(e) => set({ mediaDays: Number(e.target.value.replace(/\D/g, '')) || 0 })} /></Field>
      </div>
      <h3 className="text-sm font-semibold text-violet-300">Map</h3>
      <p className="text-sm text-slate-400">The map on each 911 call and of radio positions. For a map with no internet, give an .mbtiles file of raster tiles on this server (your GIS team or a tile tool such as TileMill or gdal2tiles makes one); or a tile server's address. Leave both empty for no map.</p>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="Map file on this server" htmlFor="pmf" error={pm('file')} hint="The path of an .mbtiles file of PNG, JPEG or WebP tiles."><input id="pmf" className="input font-mono" value={draft.map.file} placeholder="/var/lib/bitvoice/county.mbtiles" onChange={(e) => setMap({ file: e.target.value })} /></Field>
        <Field label="Or a tile server" htmlFor="pmu" error={pm('url')} hint="With {z}, {x} and {y}. The call-takers' browsers fetch from it."><input id="pmu" className="input font-mono" value={draft.map.url} placeholder="https://tiles.example.gov/{z}/{x}/{y}.png" onChange={(e) => setMap({ url: e.target.value })} /></Field>
        <Field label="Credit" htmlFor="pma" error={pm('attribution')} hint="What the tiles' licence asks the map to show."><input id="pma" className="input" value={draft.map.attribution} placeholder="© County GIS" onChange={(e) => setMap({ attribution: e.target.value })} /></Field>
      </div>
      <div className="flex justify-end"><button className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={15} aria-hidden /> Save</button></div>
    </section>
  )
}

const aliFields = ['callback', 'class', 'name', 'street', 'hno', 'hns', 'prd', 'rd', 'sts', 'pod', 'loc', 'unit', 'floor', 'building', 'city', 'county', 'state', 'zip', 'lat', 'lon', 'unc', 'company', 'esn', 'pilot']
const aliFieldLabel: Record<string, string> = { callback: 'Callback number', class: 'Class of service', name: 'Name', street: 'Street (whole line)', hno: 'House number', hns: 'Number suffix', prd: 'Direction before', rd: 'Street name',
  sts: 'Street type', pod: 'Direction after', loc: 'More (apartment, suite)', unit: 'Unit', floor: 'Floor', building: 'Building', city: 'City', county: 'County', state: 'State', zip: 'ZIP', lat: 'Latitude', lon: 'Longitude',
  unc: 'Uncertainty (m)', company: 'Phone company', esn: 'ESN', pilot: 'Pilot number' }
const sampleALI = '001 (717) 555-0100   10/15 14:05\nRESD VERIZON\nJOHNSON PAT\n123 N MARKET ST\nAPT 4B\nHARRISBURG          PA 17101'
const numInput = (v: string) => Number(v.replace(/\D/g, '')) || 0

/** The legacy ALI link: where to ask, what to send, and where each field sits in the answer. */
function ALILinkSetup({ link, set, p }: { link: ALILink; set: (l: ALILink) => void; p: (f: string) => string | undefined }) {
  const [sample, setSample] = useState(sampleALI)
  const [number, setNumber] = useState('')
  const [read, setRead] = useState<{ lines?: string[]; record?: ALIRecordView; error?: string; text?: string } | null>(null)
  const [parse] = useAliParseMutation()
  const [test, { isLoading }] = useAliTestMutation()
  const setField = (i: number, v: Partial<ALIField>) => set({ ...link, layout: link.layout.map((f, j) => (j === i ? { ...f, ...v } : f)) })
  const lines = (read?.lines ?? (read?.text ?? '').split('\n')).filter((l) => l !== undefined)
  return (
    <div className="space-y-3 rounded-lg border border-slate-800 p-3">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-violet-300"><Cable size={15} className="text-amber-300" aria-hidden /> Legacy ALI link</h3>
      <p className="text-sm text-slate-400">Where there is no location server above: BitVoice asks your ALI controller (or the ALI database's data link) for a callback number's record, over the network or a serial port, and reads the answer by the layout below. ALI layouts differ between providers: copy yours from a printout of an ALI screen.</p>
      <div className="grid gap-3 md:grid-cols-3">
        <Field label="ALI link address" htmlFor="pal" error={p('aliLink.address')} hint="host:port, or a serial port: serial:/dev/ttyUSB0?baud=9600&parity=none (Linux and macOS).">
          <input id="pal" className="input font-mono" value={link.address} placeholder="serial:/dev/ttyUSB0?baud=9600" onChange={(e) => set({ ...link, address: e.target.value.trim() })} /></Field>
        <Field label="Request" htmlFor="palr" hint="{ani10} the ten digits, {ani7} the last seven, {pos2} the position, \x02 STX, \x03 ETX. Blank: \x02{ani10}\x03.">
          <input id="palr" className="input font-mono" value={link.request} placeholder="\x02{ani10}\x03" onChange={(e) => set({ ...link, request: e.target.value })} /></Field>
        <Field label="Wait for the answer (seconds)" htmlFor="palt" error={p('aliLink.timeoutS')} hint="4 when blank."><input id="palt" className="input w-24" inputMode="numeric" value={link.timeoutS || ''} placeholder="4" onChange={(e) => set({ ...link, timeoutS: numInput(e.target.value) })} /></Field>
      </div>
      <table className="w-full text-sm" aria-label="ALI layout">
        <thead><tr className="text-left text-slate-400"><th className="py-1">Field</th><th>Line</th><th>Column</th><th>Length (0: to the end)</th><th></th></tr></thead>
        <tbody>{link.layout.map((f, i) => (
          <tr key={i} className="border-t border-slate-800">
            <td className="py-1"><select className="input" aria-label={`Field ${i + 1}`} value={f.name} onChange={(e) => setField(i, { name: e.target.value })}>{aliFields.map((n) => <option key={n} value={n}>{aliFieldLabel[n]}</option>)}</select></td>
            <td><input className="input w-16" aria-label={`Line of field ${i + 1}`} inputMode="numeric" value={f.line || ''} onChange={(e) => setField(i, { line: numInput(e.target.value) })} /></td>
            <td><input className="input w-16" aria-label={`Column of field ${i + 1}`} inputMode="numeric" value={f.col || ''} onChange={(e) => setField(i, { col: numInput(e.target.value) })} /></td>
            <td><input className="input w-16" aria-label={`Length of field ${i + 1}`} inputMode="numeric" value={f.len || ''} placeholder="0" onChange={(e) => setField(i, { len: numInput(e.target.value) })} /></td>
            <td><button className="btn-ghost p-1" aria-label={`Remove field ${i + 1}`} onClick={() => set({ ...link, layout: link.layout.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button></td>
          </tr>))}</tbody>
      </table>
      {p('aliLink.layout') && <p className="text-sm text-rose-300" role="alert">{p('aliLink.layout')}</p>}
      <button className="btn" onClick={() => set({ ...link, layout: [...link.layout, { name: aliFields.find((n) => !link.layout.some((f) => f.name === n)) ?? 'street', line: link.layout.length + 1, col: 1, len: 0 }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Place a field</button>
      <div className="grid gap-3 md:grid-cols-2">
        <Field label="A sample of your ALI text" htmlFor="pals" hint="Paste an ALI record as your provider sends it, to check the layout.">
          <textarea id="pals" className="input h-36 font-mono text-xs" value={sample} onChange={(e) => setSample(e.target.value)} /></Field>
        <div className="space-y-2">
          <div className="flex flex-wrap items-end gap-2">
            <button className="btn" onClick={() => parse({ text: sample, layout: link.layout }).unwrap().then(setRead).catch((e) => toast.error(errorOf(e).message))}>Read the sample</button>
            <Field label="Or ask the link for" htmlFor="paln"><input id="paln" className="input w-40 font-mono" value={number} placeholder="7175550100" onChange={(e) => setNumber(e.target.value)} /></Field>
            <button className="btn" disabled={isLoading || !link.address || !number} onClick={() => test({ number, link }).unwrap().then(setRead).catch((e) => toast.error(errorOf(e).message))}>Ask</button>
          </div>
          {read && <div className="space-y-2" aria-label="What the layout reads">
            {!!lines.length && <pre className="overflow-x-auto rounded bg-slate-950 p-2 text-xs text-slate-300" aria-label="ALI lines">{'     ' + '1234567890'.repeat(4) + '\n' + lines.map((l, i) => String(i + 1).padStart(3) + '  ' + l).join('\n')}</pre>}
            {read.error && <p className="text-sm text-amber-300" role="status">{read.error}</p>}
            {read.record && <dl className="grid grid-cols-[auto_1fr] gap-x-3 text-xs" aria-label="ALI fields read">{Object.entries(read.record.fields ?? {}).map(([k, v]) => <Fragment key={k}><dt className="text-slate-500">{aliFieldLabel[k] ?? k}</dt><dd className="font-mono text-slate-200">{v}</dd></Fragment>)}</dl>}
          </div>}
        </div>
      </div>
    </div>
  )
}

const rowKinds: [string, string][] = [['day', 'A row a day'], ['week', 'A row a week'], ['month', 'A row a month'], ['hour', 'A row an hour of the day'], ['taker', 'A row a call-taker'], ['total', 'One row in all']]
const columnKinds: [string, string][] = [['period', 'The row (date, hour, call-taker)'], ['calls', 'Calls'], ['answered', 'Answered'], ['abandoned', 'Abandoned'], ['abandonedPct', 'Abandoned (%)'],
  ['withinPct', 'Answered within N s (%)'], ['withinCount', 'Answered within N s'], ['overCount', 'Answered after more than N s'], ['avgAnswer', 'Average to answer (s)'], ['longestAnswer', 'Longest to answer (s)'],
  ['p90Answer', '90% answered within (s)'], ['avgLength', 'Average call length (s)'], ['textCalls', 'Text calls (any)'], ['ttyCalls', 'TTY calls'], ['rttCalls', 'Real-time text calls'], ['msrpCalls', 'Text to 911 calls']]
const needsSeconds = (k: string) => k === 'withinPct' || k === 'withinCount' || k === 'overCount'
const blankLayout: ReportLayout = { id: '', name: '', rows: 'day', total: true, columns: [{ kind: 'period', seconds: 0, label: 'Date' }, { kind: 'calls', seconds: 0, label: '' }, { kind: 'withinPct', seconds: 15, label: '' }, { kind: 'abandoned', seconds: 0, label: '' }] }

/** Report layouts: a PSAP lays out the rows and columns its state's form asks for, and runs them. */
function Layouts() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const [run, { data: laid, isFetching }] = useLazyLaidOutQuery()
  const [edit, setEdit] = useState<ReportLayout | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [from, setFrom] = useState(iso(new Date(new Date().getFullYear(), new Date().getMonth(), 1)))
  const [to, setTo] = useState(iso(new Date()))
  const [chosen, setChosen] = useState('')
  if (!data) return null
  const layouts = data.system.psap?.reportLayouts ?? []
  const p = (f: string) => problemFor(err?.problems, f)
  const at = edit ? layouts.findIndex((l) => l.id === edit.id && edit.id !== '') : -1
  const idx = at >= 0 ? at : layouts.length
  const pf = (f: string) => p(`system.psap.reportLayouts[${idx}].${f}`)
  const store = async (next: ReportLayout[]) => {
    try { await save({ ...data, system: { ...data.system, psap: { ...data.system.psap!, reportLayouts: next } } }).unwrap(); setErr(null); setEdit(null); toast.success('Report layouts saved') }
    catch (e) { setErr(errorOf(e)) }
  }
  const setCol = (i: number, v: Partial<ReportColumn>) => edit && setEdit({ ...edit, columns: edit.columns.map((c, j) => (j === i ? { ...c, ...v } : c)) })
  const move = (i: number, d: number) => { if (!edit) return; const c = [...edit.columns]; const [x] = c.splice(i, 1); c.splice(i + d, 0, x); setEdit({ ...edit, columns: c }) }
  const href = (fmt: string) => `./api/psap/layouts/${chosen}?from=${from}&to=${to}&format=${fmt}`
  return (
    <section className="card mt-4 space-y-4 p-5" aria-label="Report layouts">
      <p className="text-sm text-slate-400">Lay out the report your state or county asks for: what each row is, which columns, their headings, and the seconds the answer-time columns count to. Then run it over any dates, to read here, download as a spreadsheet, or print.</p>
      {!layouts.length ? <Empty icon={Table2} title="No report layouts yet">{admin ? 'Make one below, from your form.' : 'An administrator makes them.'}</Empty> : (
        <ul className="space-y-1" aria-label="Layouts">{layouts.map((l) => (
          <li key={l.id} className="flex flex-wrap items-center gap-2 text-sm">
            <button className={chosen === l.id ? 'btn btn-primary py-0.5' : 'btn py-0.5'} onClick={() => setChosen(l.id)}>{l.name}</button>
            <span className="text-slate-500">{rowKinds.find((r) => r[0] === l.rows)?.[1]}, {l.columns.length} columns</span>
            {admin && <><button className="btn-ghost p-1" aria-label={`Edit ${l.name}`} onClick={() => setEdit(structuredClone(l))}><Pencil size={13} aria-hidden /></button>
              <button className="btn-ghost p-1" aria-label={`Delete ${l.name}`} onClick={() => store(layouts.filter((x) => x.id !== l.id))}><Trash2 size={13} className="text-rose-300" aria-hidden /></button></>}
          </li>))}</ul>)}
      {chosen && <div className="space-y-2">
        <div className="flex flex-wrap items-end gap-2">
          <Field label="From" htmlFor="rlf"><input id="rlf" type="date" className="input" value={from} onChange={(e) => setFrom(e.target.value)} /></Field>
          <Field label="To" htmlFor="rlt"><input id="rlt" type="date" className="input" value={to} onChange={(e) => setTo(e.target.value)} /></Field>
          <button className="btn btn-primary" disabled={isFetching} onClick={() => run({ id: chosen, from, to })}>Run</button>
          <a className="btn" href={href('csv')}><Download size={14} aria-hidden /> Spreadsheet (CSV)</a>
          <a className="btn" href={href('html')} target="_blank" rel="noreferrer"><Printer size={14} aria-hidden /> Page to print</a>
        </div>
        {laid && <div className="overflow-x-auto"><table className="text-sm" aria-label={laid.name}>
          <thead><tr>{laid.header.map((h, i) => <th key={i} className="border-b border-slate-700 px-2 py-1 text-left text-slate-300">{h}</th>)}</tr></thead>
          <tbody>{laid.rows.map((r, i) => <tr key={i} className={r[0] === 'total' ? 'font-semibold text-slate-100' : ''}>{r.map((v, j) => <td key={j} className="border-b border-slate-800 px-2 py-1 font-mono">{v}</td>)}</tr>)}</tbody>
        </table></div>}
      </div>}
      {admin && !edit && <button className="btn" onClick={() => setEdit(structuredClone(blankLayout))}><Plus size={14} className="text-emerald-300" aria-hidden /> New report layout</button>}
      {admin && edit && <div className="space-y-3 rounded-lg border border-slate-800 p-3" aria-label="Report layout">
        <div className="grid gap-3 md:grid-cols-3">
          <Field label="Name" htmlFor="rln" error={pf('name')}><input id="rln" className="input" value={edit.name} placeholder="State form 911-A" onChange={(e) => setEdit({ ...edit, name: e.target.value })} /></Field>
          <Field label="Rows" htmlFor="rlr" error={pf('rows')}><select id="rlr" className="input" value={edit.rows} onChange={(e) => setEdit({ ...edit, rows: e.target.value })}>{rowKinds.map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select></Field>
          <label className="flex items-center gap-2 text-sm text-slate-300"><input type="checkbox" checked={edit.total} onChange={(e) => setEdit({ ...edit, total: e.target.checked })} /> A total row at the bottom</label>
        </div>
        <table className="w-full text-sm" aria-label="Columns">
          <thead><tr className="text-left text-slate-400"><th className="py-1">Column</th><th>Seconds</th><th>Heading (blank: a plain one)</th><th></th></tr></thead>
          <tbody>{edit.columns.map((c, i) => (
            <tr key={i} className="border-t border-slate-800">
              <td className="py-1"><select className="input" aria-label={`Column ${i + 1}`} value={c.kind} onChange={(e) => setCol(i, { kind: e.target.value, seconds: needsSeconds(e.target.value) ? c.seconds || 15 : 0 })}>{columnKinds.map(([k, l]) => <option key={k} value={k}>{l}</option>)}</select></td>
              <td>{needsSeconds(c.kind) && <input className="input w-20" aria-label={`Seconds of column ${i + 1}`} inputMode="numeric" value={c.seconds || ''} onChange={(e) => setCol(i, { seconds: Number(e.target.value.replace(/\D/g, '')) || 0 })} />}
                {pf(`columns[${i}].seconds`) && <span className="block text-xs text-rose-300">{pf(`columns[${i}].seconds`)}</span>}</td>
              <td><input className="input" aria-label={`Heading of column ${i + 1}`} value={c.label} onChange={(e) => setCol(i, { label: e.target.value })} /></td>
              <td className="whitespace-nowrap">
                <button className="btn-ghost p-1" aria-label={`Move column ${i + 1} up`} disabled={i === 0} onClick={() => move(i, -1)}><ArrowUp size={13} aria-hidden /></button>
                <button className="btn-ghost p-1" aria-label={`Move column ${i + 1} down`} disabled={i === edit.columns.length - 1} onClick={() => move(i, 1)}><ArrowDown size={13} aria-hidden /></button>
                <button className="btn-ghost p-1" aria-label={`Remove column ${i + 1}`} onClick={() => setEdit({ ...edit, columns: edit.columns.filter((_, j) => j !== i) })}><Trash2 size={13} className="text-rose-300" aria-hidden /></button>
              </td>
            </tr>))}</tbody>
        </table>
        {pf('columns') && <p className="text-sm text-rose-300" role="alert">{pf('columns')}</p>}
        <div className="flex flex-wrap justify-between gap-2">
          <button className="btn" onClick={() => setEdit({ ...edit, columns: [...edit.columns, { kind: 'calls', seconds: 0, label: '' }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a column</button>
          <div className="flex gap-2"><button className="btn" onClick={() => { setEdit(null); setErr(null) }}>Cancel</button>
            <button className="btn btn-primary" disabled={isLoading} onClick={() => store(at >= 0 ? layouts.map((l, j) => (j === at ? edit : l)) : [...layouts, edit])}><Save size={14} aria-hidden /> Save the layout</button></div>
        </div>
      </div>}
    </section>
  )
}
