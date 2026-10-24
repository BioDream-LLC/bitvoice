import { clsx } from 'clsx'
import { Network, Plus, Radar, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { errorOf, useProbeIceMutation, type SystemSettings } from './api'
import { Field, ListInput, Toggle } from './ui'

type ICE = NonNullable<SystemSettings['ice']>
const blank: ICE = { full: false, stun: [], turn: [], relayOnly: false }

const typeWords: Record<string, [string, string]> = {
  host: ['this computer', 'text-sky-300'], srflx: ['seen through STUN (public)', 'text-emerald-300'],
  prflx: ['learnt from a check', 'text-violet-300'], relay: ['relayed by TURN', 'text-amber-300'],
}

/** BitVoice behind NAT: full ICE, with the STUN and TURN servers it uses to be reached. */
export function NATSettings({ value, onChange, p }: { value: SystemSettings['ice']; onChange: (v: ICE) => void; p: (f: string) => string | undefined }) {
  const ice: ICE = { ...blank, ...value }
  const set = (v: Partial<ICE>) => onChange({ ...ice, ...v })
  const relays = ice.turn ?? []
  const setRelay = (i: number, v: Partial<ICE['turn'] extends (infer R)[] | undefined ? R : never>) => set({ turn: relays.map((r, j) => (j === i ? { ...r, ...v } : r)) })
  const [probe, { isLoading }] = useProbeIceMutation()
  const [result, setResult] = useState<{ candidates: { type: string; address: string; base?: string }[]; srflx: boolean; relay: boolean } | { error: string } | null>(null)
  return (
    <section className="card space-y-4 p-5" aria-labelledby="nat-h">
      <h2 id="nat-h" className="flex items-center gap-2 font-semibold text-slate-100"><Network size={17} className="text-sky-300" aria-hidden /> BitVoice behind NAT</h2>
      <p className="text-sm text-slate-400">
        For a server without a public address of its own (behind an office router with no port forwarding, or in a cloud network). BitVoice then takes full part in ICE:
        it offers browsers its own addresses, its <span className="text-emerald-300">public address as STUN sees it</span>, and <span className="text-amber-300">relays on TURN servers</span>, and checks each one.
        Off, it answers browsers at the one address they used for this page, which suits a server with a public address.
      </p>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={ice.full} onChange={(v) => set({ full: v })} label="This server is behind NAT" /> This server is behind NAT (full ICE)</label>
      {ice.full && (
        <>
          <Field label="STUN servers" htmlFor="icestun" error={p('system.ice.stun') ?? (ice.stun ?? []).map((_, i) => p(`system.ice.stun[${i}]`)).find(Boolean)} hint="host:port, such as stun.l.google.com:19302. Press Enter after each.">
            <ListInput id="icestun" mono value={ice.stun ?? []} onChange={(v) => set({ stun: v })} placeholder="stun.example.net:3478" />
          </Field>
          <div>
            <div className="mb-1 flex items-center justify-between">
              <span className="text-sm text-slate-300">TURN servers BitVoice relays through</span>
              <button type="button" className="btn" onClick={() => set({ turn: [...relays, { server: '', username: '', password: '' }] })}><Plus size={14} aria-hidden /> Add TURN server</button>
            </div>
            {relays.length === 0 ? <p className="text-sm text-slate-500">None: browsers behind strict firewalls may not get through. Your own TURN server elsewhere, or a hosted one, goes here (UDP).</p> : (
              <ul className="space-y-2">
                {relays.map((r, i) => (
                  <li key={i} className="grid items-start gap-2 md:grid-cols-[2fr_1fr_1fr_auto]">
                    <Field label="Server" htmlFor={`its${i}`} error={p(`system.ice.turn[${i}].server`)}><input id={`its${i}`} className="input font-mono" placeholder="turn.example.net:3478" value={r.server} onChange={(e) => setRelay(i, { server: e.target.value.trim() })} /></Field>
                    <Field label="Username" htmlFor={`itu${i}`} error={p(`system.ice.turn[${i}].username`)}><input id={`itu${i}`} className="input font-mono" autoComplete="off" value={r.username} onChange={(e) => setRelay(i, { username: e.target.value })} /></Field>
                    <Field label="Password" htmlFor={`itp${i}`} error={p(`system.ice.turn[${i}].password`)} hint="Empty keeps the current one."><input id={`itp${i}`} type="password" autoComplete="off" className="input font-mono" value={r.password} onChange={(e) => setRelay(i, { password: e.target.value })} /></Field>
                    <button type="button" className="btn mt-6 text-rose-300" aria-label={`Remove TURN server ${r.server || i + 1}`} onClick={() => set({ turn: relays.filter((_, j) => j !== i) })}><Trash2 size={14} aria-hidden /></button>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!ice.relayOnly} onChange={(v) => set({ relayOnly: v })} label="Relay every call" />
            <span>Relay every call through TURN <span className="text-slate-500">(only for networks that require it: it adds a hop)</span></span></label>
          {p('system.ice.relayOnly') && <p className="field-error text-sm text-rose-300">{p('system.ice.relayOnly')}</p>}
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" className="btn" disabled={isLoading} onClick={() => probe(ice).unwrap().then(setResult).catch((e) => setResult({ error: errorOf(e).message }))}>
              <Radar size={14} aria-hidden /> Find my addresses
            </button>
            <span className="text-xs text-slate-500">Asks the servers above, as a call would, and changes nothing.</span>
          </div>
          {result && ('error' in result ? <p role="status" className="text-sm text-rose-300">{result.error}</p> : (
            <div role="status" className="space-y-1 text-sm">
              <ul aria-label="Addresses BitVoice offers browsers" className="space-y-0.5">
                {result.candidates.map((c) => (
                  <li key={c.type + c.address}><span className="font-mono text-slate-200">{c.address}</span> <span className={clsx(typeWords[c.type]?.[1])}>{typeWords[c.type]?.[0] ?? c.type}</span>{c.base && <span className="text-slate-500"> (from {c.base})</span>}</li>
                ))}
              </ul>
              {!result.srflx && (ice.stun?.length ?? 0) > 0 && <p className="text-amber-300">No STUN server answered: check its address and that UDP leaves this network.</p>}
              {!result.relay && relays.length > 0 && <p className="text-amber-300">No TURN server gave a relay: check its address and the username and password.</p>}
            </div>
          ))}
        </>
      )}
    </section>
  )
}
