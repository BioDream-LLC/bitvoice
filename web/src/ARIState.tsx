import { Activity, RefreshCw } from 'lucide-react'
import { useAriStateQuery } from './api'

/** What ARI programs keep in BitVoice right now: applications, custom device states, mailboxes, variables and bridges. */
export function ARIState() {
  const { data, refetch, isFetching } = useAriStateQuery(undefined, { pollingInterval: 10000 })
  if (!data) return null
  const globals = Object.entries(data.globals)
  return (
    <div aria-labelledby="aristate-h">
      <div className="mb-2 flex items-center justify-between">
        <h3 id="aristate-h" className="flex items-center gap-2 text-sm font-medium text-slate-200"><Activity size={15} className="text-emerald-300" aria-hidden /> What programs keep now</h3>
        <button type="button" className="btn" onClick={() => refetch()} disabled={isFetching}><RefreshCw size={14} aria-hidden /> Refresh</button>
      </div>
      <div className="grid gap-3 text-sm md:grid-cols-2">
        <section aria-label="ARI applications now" className="rounded-lg border border-slate-800 p-3">
          <h4 className="mb-1 text-slate-300">Applications</h4>
          {data.applications.length === 0 ? <p className="text-slate-500">None.</p> : data.applications.map((a) => (
            <div key={a.name} className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-sky-300">{a.name}</span>
              <span className={a.connected ? 'text-emerald-300' : 'text-amber-300'}>{a.connected ? 'program connected' : 'no program connected'}</span>
              <span className="text-slate-400">{a.channel_ids.length} channels, {a.bridge_ids.length} bridges</span>
              {(a.endpoint_ids.length + a.device_names.length) > 0 && <span className="text-slate-400">subscribed to {[...a.endpoint_ids, ...a.device_names].join(', ')}</span>}
              {a.events_disallowed.length > 0 && <span className="text-slate-500">filters out {a.events_disallowed.map((e) => e.type).join(', ')}</span>}
            </div>
          ))}
        </section>
        <section aria-label="Custom device states" className="rounded-lg border border-slate-800 p-3">
          <h4 className="mb-1 text-slate-300">Custom device states</h4>
          {data.devices.length === 0 ? <p className="text-slate-500">None set.</p> : data.devices.map((d) => (
            <div key={d.name}><span className="font-mono text-slate-200">{d.name}</span> <span className={d.state === 'NOT_INUSE' ? 'text-emerald-300' : d.state === 'UNAVAILABLE' ? 'text-slate-500' : 'text-amber-300'}>{d.state}</span></div>
          ))}
        </section>
        <section aria-label="Mailboxes kept by programs" className="rounded-lg border border-slate-800 p-3">
          <h4 className="mb-1 text-slate-300">Mailboxes kept by programs</h4>
          {data.mailboxes.length === 0 ? <p className="text-slate-500">None.</p> : data.mailboxes.map((m) => (
            <div key={m.name}><span className="font-mono text-slate-200">{m.name}</span> <span className="text-violet-300">{m.new_messages} new</span>, <span className="text-slate-400">{m.old_messages} old</span></div>
          ))}
        </section>
        <section aria-label="Global variables and bridges" className="rounded-lg border border-slate-800 p-3">
          <h4 className="mb-1 text-slate-300">Global variables and bridges</h4>
          {globals.length === 0 && data.bridges.length === 0 ? <p className="text-slate-500">None.</p> : null}
          {globals.map(([k, v]) => <div key={k}><span className="font-mono text-sky-300">{k}</span> = <span className="font-mono text-emerald-300">{v}</span></div>)}
          {data.bridges.map((b) => <div key={b.id}><span className="font-mono text-fuchsia-300">bridge {b.id}</span> <span className="text-slate-400">{b.name} {b.channels.length} channels</span></div>)}
        </section>
      </div>
    </div>
  )
}
