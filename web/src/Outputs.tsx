import { Activity, Copy, Download, FileSpreadsheet, Network, Plus, Send, Trash2, Webhook } from 'lucide-react'
import { toast } from 'sonner'
import { useCdrCsvQuery, useSnmpStatusQuery, useTestCdrOutputsMutation, useTurnStatusQuery, useWebSocketStatusQuery, type SystemSettings, type TURN } from './api'
import { Field, ListInput, Toggle } from './ui'

type SNMP = NonNullable<SystemSettings['snmp']>
type Outputs = NonNullable<SystemSettings['cdrOutputs']>

const snmpDefaults: SNMP = { enabled: false, listen: ':161', community: '', allowFrom: [], baseOid: '', contact: '', location: '', traps: [] }

/** Settings: the SNMP agent for monitoring systems, and where its traps go. */
export function SNMPSettings({ snmp, onChange, p }: { snmp: SystemSettings['snmp']; onChange: (v: SNMP) => void; p: (f: string) => string | undefined }) {
  const s: SNMP = { ...snmpDefaults, ...snmp }
  const { data: status } = useSnmpStatusQuery(undefined, { pollingInterval: 10000 })
  const set = (v: Partial<SNMP>) => onChange({ ...s, ...v })
  const traps = s.traps ?? []
  return (
    <section className="card space-y-4 p-5" aria-labelledby="snmp-h">
      <h2 id="snmp-h" className="flex items-center gap-2 font-semibold text-slate-100"><Activity size={17} className="text-emerald-300" aria-hidden /> SNMP monitoring</h2>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={s.enabled} onChange={(v) => set({ enabled: v })} label="SNMP agent" />
        SNMP agent <span className="text-xs text-slate-500">(SNMPv2c, read only: for Zabbix, PRTG, LibreNMS, Nagios)</span></label>
      {s.enabled && status && (status.running
        ? <p className="text-sm text-emerald-300">Answering on {status.address}</p>
        : status.error && <p className="text-sm text-rose-300">Not running: {status.error}. Port 161 needs the right to open low ports; or use another, such as :1161.</p>)}
      {s.enabled && (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Listen on" htmlFor="snl" error={p('system.snmp.listen')}><input id="snl" className="input font-mono" value={s.listen} onChange={(e) => set({ listen: e.target.value.trim() })} placeholder=":161" /></Field>
          <Field label="Community" htmlFor="snc" error={p('system.snmp.community')} hint="Like a password, sent in clear: not public."><input id="snc" className="input font-mono" value={s.community} onChange={(e) => set({ community: e.target.value.trim() })} /></Field>
          <Field label="Answer only these networks" htmlFor="sna" error={p('system.snmp.allowFrom')} hint="Your monitoring servers, such as 10.0.0.5 or 10.0.0.0/24.">
            <ListInput id="sna" mono value={s.allowFrom ?? []} onChange={(v) => set({ allowFrom: v })} placeholder="10.0.0.0/24" invalid={!!p('system.snmp.allowFrom')} />
          </Field>
          <Field label="Base OID" htmlFor="snb" error={p('system.snmp.baseOid')} hint="Under your enterprise number; empty for BitVoice's default.">
            <input id="snb" className="input font-mono" value={s.baseOid} onChange={(e) => set({ baseOid: e.target.value.trim() })} placeholder="1.3.6.1.4.1.8072.9999.9999.7" />
          </Field>
          <Field label="Contact (sysContact)" htmlFor="snt"><input id="snt" className="input" value={s.contact} onChange={(e) => set({ contact: e.target.value })} placeholder="noc@example.com" /></Field>
          <Field label="Location (sysLocation)" htmlFor="sno"><input id="sno" className="input" value={s.location} onChange={(e) => set({ location: e.target.value })} placeholder="Server room, 2nd floor" /></Field>
        </div>
      )}
      <div className="space-y-2">
        <span className="label">Traps</span>
        <p className="text-xs text-slate-500">Sent when a trunk goes down or comes back, an emergency call is made, or a server of the cluster stops answering.</p>
        {traps.map((t, i) => (
          <div key={i} className="grid gap-2 sm:grid-cols-[1fr_12rem_auto]">
            <input className="input font-mono" aria-label={`Trap ${i + 1} manager`} value={t.host} placeholder="10.0.0.5:162" onChange={(e) => set({ traps: traps.map((x, j) => (j === i ? { ...x, host: e.target.value.trim() } : x)) })} />
            <input className="input font-mono" aria-label={`Trap ${i + 1} community`} value={t.community} placeholder="community" onChange={(e) => set({ traps: traps.map((x, j) => (j === i ? { ...x, community: e.target.value.trim() } : x)) })} />
            <button type="button" className="btn" aria-label={`Remove trap ${i + 1}`} onClick={() => set({ traps: traps.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
            {(p(`system.snmp.traps[${i}].host`) || p(`system.snmp.traps[${i}].community`)) && <p className="text-sm text-rose-300 sm:col-span-3">{p(`system.snmp.traps[${i}].host`) ?? p(`system.snmp.traps[${i}].community`)}</p>}
          </div>
        ))}
        <div className="flex flex-wrap gap-2">
          <button type="button" className="btn" onClick={() => set({ traps: [...traps, { host: '', community: '' }] })}><Plus size={14} aria-hidden /> Add a trap receiver</button>
          <a className="btn" href="./api/snmp/mib"><Download size={14} className="text-sky-300" aria-hidden /> BITVOICE-MIB</a>
        </div>
      </div>
    </section>
  )
}

/** Settings: more places call records go: syslog, a CSV file a day, an external PostgreSQL table. */
export function CDROutputSettings({ out, onChange, p }: { out: SystemSettings['cdrOutputs']; onChange: (v: Outputs) => void; p: (f: string) => string | undefined }) {
  const o: Outputs = { csv: false, syslog: { host: '', port: 0, transport: 'udp', facility: 16 }, postgres: { url: '', table: 'cdr' }, ...out }
  const [test, { isLoading }] = useTestCdrOutputsMutation()
  const run = async () => {
    const r = await test().unwrap().catch(() => null)
    if (!r) { toast.error('The test could not run'); return }
    const lines = Object.entries(r).map(([k, v]) => `${k}: ${v}`)
    if (!lines.length) toast.message('Set a syslog server or a PostgreSQL address first, and save.')
    else if (Object.values(r).every((v) => v === 'ok')) toast.success(`Test record sent (${Object.keys(r).join(', ')})`)
    else toast.error(lines.join('; '))
  }
  return (
    <section className="card space-y-4 p-5" aria-labelledby="cdro-h">
      <h2 id="cdro-h" className="flex items-center gap-2 font-semibold text-slate-100"><FileSpreadsheet size={17} className="text-fuchsia-300" aria-hidden /> Call records elsewhere</h2>
      <p className="text-sm text-slate-400">Each call record, as the call ends, can also go to these, beside Call history.</p>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={o.csv} onChange={(v) => onChange({ ...o, csv: v })} label="CSV files" />
        A CSV file a day <span className="text-xs text-slate-500">(Asterisk's Master.csv columns; download them on Call history)</span></label>
      <div className="grid gap-3 sm:grid-cols-[1fr_7rem_7rem_8rem]">
        <Field label="Syslog server" htmlFor="slh" error={p('system.cdrOutputs.syslog.host')} hint="Empty: off."><input id="slh" className="input font-mono" value={o.syslog.host} onChange={(e) => onChange({ ...o, syslog: { ...o.syslog, host: e.target.value.trim() } })} placeholder="logs.example.com" /></Field>
        <Field label="Port" htmlFor="slp" error={p('system.cdrOutputs.syslog.port')}><input id="slp" type="number" className="input" value={o.syslog.port || ''} placeholder={o.syslog.transport === 'tls' ? '6514' : '514'} onChange={(e) => onChange({ ...o, syslog: { ...o.syslog, port: Number(e.target.value) } })} /></Field>
        <Field label="Transport" htmlFor="slt" error={p('system.cdrOutputs.syslog.transport')}>
          <select id="slt" className="input" value={o.syslog.transport || 'udp'} onChange={(e) => onChange({ ...o, syslog: { ...o.syslog, transport: e.target.value as Outputs['syslog']['transport'] } })}>
            <option value="udp">UDP</option><option value="tcp">TCP</option><option value="tls">TLS</option>
          </select>
        </Field>
        <Field label="Facility" htmlFor="slf" error={p('system.cdrOutputs.syslog.facility')}>
          <select id="slf" className="input" value={o.syslog.facility || 16} onChange={(e) => onChange({ ...o, syslog: { ...o.syslog, facility: Number(e.target.value) } })}>
            {[16, 17, 18, 19, 20, 21, 22, 23].map((f) => <option key={f} value={f}>local{f - 16}</option>)}
          </select>
        </Field>
      </div>
      <div className="grid gap-3 sm:grid-cols-[1fr_10rem]">
        <Field label="PostgreSQL database" htmlFor="pgu" error={p('system.cdrOutputs.postgres.url')} hint="Another database for reports and billing tools; the table is made with Asterisk's cdr columns. Empty: off.">
          <input id="pgu" className="input font-mono" value={o.postgres.url} onChange={(e) => onChange({ ...o, postgres: { ...o.postgres, url: e.target.value.trim() } })} placeholder="postgres://reports:secret@db.example.com/calls" />
        </Field>
        <Field label="Table" htmlFor="pgt" error={p('system.cdrOutputs.postgres.table')}><input id="pgt" className="input font-mono" value={o.postgres.table} onChange={(e) => onChange({ ...o, postgres: { ...o.postgres, table: e.target.value.trim() } })} placeholder="cdr" /></Field>
      </div>
      <button type="button" className="btn" disabled={isLoading} onClick={run}><Send size={14} className="text-emerald-300" aria-hidden /> Send a test record</button>
      <span className="ml-2 text-xs text-slate-500">(to the outputs as already saved)</span>
    </section>
  )
}

/** Call history: the CSV files written each day. */
export function CDRCsvFiles() {
  const { data } = useCdrCsvQuery()
  if (!data?.items.length) return null
  return (
    <details className="card mt-4 p-4">
      <summary className="cursor-pointer text-sm text-sky-300">CSV files ({data.items.length} days)</summary>
      <ul className="mt-2 grid gap-1 sm:grid-cols-3">
        {data.items.map((d) => (
          <li key={d.day}><a className="flex items-center gap-2 text-sm text-slate-300 hover:text-sky-300" href={`./api/cdr/csv/${d.day}`}><Download size={13} aria-hidden /> {d.day} <span className="text-xs text-slate-500">{Math.ceil(d.bytes / 1024)} KB</span></a></li>
        ))}
      </ul>
    </details>
  )
}

const turnDefaults: TURN = { enabled: false, port: 3478, tlsPort: 5349, relayMin: 49152, relayMax: 49407, secret: '' }

/** TURN: BitVoice relays browser phones' sound when their network blocks UDP or hides them behind a strict NAT. */
export function TURNSettings({ turn, onChange, p }: { turn: SystemSettings['turn']; onChange: (v: TURN) => void; p: (f: string) => string | undefined }) {
  const s: TURN = { ...turnDefaults, ...turn }
  const { data: status } = useTurnStatusQuery(undefined, { pollingInterval: 10000 })
  const set = (v: Partial<TURN>) => onChange({ ...s, ...v })
  return (
    <section className="card space-y-4 p-5" aria-labelledby="turn-h">
      <h2 id="turn-h" className="flex items-center gap-2 font-semibold text-slate-100"><Network size={17} className="text-sky-300" aria-hidden /> TURN for browser phones</h2>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={s.enabled} onChange={(v) => set({ enabled: v })} label="TURN server" />
        TURN server <span className="text-xs text-slate-500">(for browser phones and call links on networks that block UDP: hotels, guest Wi-Fi, strict company firewalls)</span></label>
      {s.enabled && status && (status.running
        ? <p className="text-sm text-emerald-300">Running on {status.port} (UDP and TCP){(status.tlsPort ?? 0) > 0 ? ` and ${status.tlsPort} (TLS)` : ''}; {status.relays ?? 0} relays open.</p>
        : <p className="text-sm text-amber-300">Not running yet: save, then it starts.</p>)}
      {s.enabled && (status?.problems?.length ?? 0) > 0 && <p className="text-sm text-rose-300">{status!.problems!.join('; ')}</p>}
      {s.enabled && (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Port (UDP and TCP)" htmlFor="tup" error={p('system.turn.port')}><input id="tup" type="number" className="input w-32" value={s.port || ''} onChange={(e) => set({ port: Number(e.target.value) })} /></Field>
          <Field label="TLS port" htmlFor="tut" error={p('system.turn.tlsPort')} hint="With this server's certificate; -1 for none. 443 gets through almost any firewall. Browsers are offered it only once the certificate is a trusted one for the name they use (Let's Encrypt, below)."><input id="tut" type="number" className="input w-32" value={s.tlsPort || ''} onChange={(e) => set({ tlsPort: Number(e.target.value) })} /></Field>
          <Field label="Relay ports from" htmlFor="tur" error={p('system.turn.relayMin')} hint="UDP; open them in the firewall."><input id="tur" type="number" className="input w-32" value={s.relayMin || ''} onChange={(e) => set({ relayMin: Number(e.target.value) })} /></Field>
          <Field label="to" htmlFor="tur2" error={p('system.turn.relayMax')}><input id="tur2" type="number" className="input w-32" value={s.relayMax || ''} onChange={(e) => set({ relayMax: Number(e.target.value) })} /></Field>
        </div>
      )}
      {s.enabled && <p className="text-xs text-slate-500">Browser phones and call links are given short-lived credentials made from a secret BitVoice keeps (<span className="font-mono">secret</span>, never shown again). It relays only to this server&apos;s own addresses, so nobody can use it to reach anything else.</p>}
    </section>
  )
}

/** SIP over WebSocket: WebRTC softphones (JsSIP, SIP.js and apps built on them) register at wss://…/ws. */
export function WebSocketSettings({ value, onChange }: { value: SystemSettings['webSocket']; onChange: (v: { enabled: boolean }) => void }) {
  const on = value?.enabled ?? false
  const { data: status } = useWebSocketStatusQuery(undefined, { pollingInterval: 10000 })
  const url = `wss://${location.host}/ws`
  return (
    <section className="card space-y-3 p-5" aria-labelledby="wss-h">
      <h2 id="wss-h" className="flex items-center gap-2 font-semibold text-slate-100"><Webhook size={17} className="text-violet-300" aria-hidden /> SIP over WebSocket</h2>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={on} onChange={(v) => onChange({ enabled: v })} label="SIP over WebSocket" />
        SIP over WebSocket <span className="text-xs text-slate-500">(for WebRTC softphones made with JsSIP, SIP.js and the like; BitVoice&apos;s own browser phone needs nothing)</span></label>
      {on && (
        <>
          <div className="flex items-center gap-2 text-sm">
            <span className="text-slate-400">Softphones connect to</span>
            <code className="font-mono text-violet-300">{url}</code>
            <button type="button" className="btn btn-ghost px-2" aria-label="Copy the WebSocket address" onClick={() => { void navigator.clipboard?.writeText(url); toast.success('Address copied') }}><Copy size={13} aria-hidden /></button>
          </div>
          <p className={(status?.connected ?? 0) > 0 ? 'text-sm text-emerald-300' : 'text-sm text-slate-500'}>{status?.connected ?? 0} softphones connected now.</p>
          <p className="text-xs text-slate-500">They sign in as an extension with its SIP password, like any phone, and the same protection applies (bans after failed sign-ins, calls per extension). Switch on TURN too for softphones on networks that block UDP.</p>
        </>
      )}
    </section>
  )
}
