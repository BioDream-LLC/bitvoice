import { Database, Pencil, Server as ServerIcon, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'
import { errorOf, useForgetServerMutation, useServersQuery, useSetServerMutation, type ClusterNode } from './api'
import { useIsAdmin } from './Editor'
import { ServersSetup } from './ServersSetup'
import { Badge, Empty, Field, Page, relTime } from './ui'

/** Servers: the BitVoice servers sharing one PostgreSQL database, and the phones and calls on each. */
export function Servers() {
  const { data } = useServersQuery(undefined, { pollingInterval: 4000 })
  return (
    <Page icon={ServerIcon} title="Servers" accent="from-sky-400 to-violet-400"
      intro="BitVoice servers sharing one database work as one phone system: a call reaches a phone whichever server it is registered with, settings changed on one apply on all, and live calls show on every server.">
      {!data ? null : !data.shared ? (
        <Empty icon={Database} title="This server runs alone">
          Servers share phones and calls through a PostgreSQL database. Move the data into PostgreSQL in <Link className="text-sky-300 underline" to="/settings">Settings, Database</Link>, then start another BitVoice with the same database address (Settings there, or <span className="font-mono">-database</span>).
        </Empty>
      ) : (
        <>
          <ul className="grid gap-3 lg:grid-cols-2" aria-label="Servers">
            {data.nodes!.map((n) => <NodeCard key={n.id} n={n} all={data.nodes!} />)}
          </ul>
          <ServersSetup address={data.address} />
          <section className="card mt-6 space-y-2 p-5 text-sm text-slate-300" aria-labelledby="fail-h">
            <h2 id="fail-h" className="font-semibold text-slate-100">When a server stops</h2>
            <ul className="list-disc space-y-1 pl-5">
              <li><span className="text-emerald-300">Calls carry on.</span> A running server picks up each call within seconds and moves the sound to itself; a planned stop or upgrade hands calls over with no gap.</li>
              <li><span className="text-sky-300">Phones carry on</span> through their backup server, the DNS name, or the shared address (whichever server holds it answers at the same address).</li>
              <li><span className="text-violet-300">Provider lines</span> set to "one server at a time" move to a running server; lines every server registers carry on from the others.</li>
              <li>Busy lamps, parking, paging and texts work across servers all the time.</li>
            </ul>
          </section>
        </>
      )}
    </Page>
  )
}

function NodeCard({ n, all }: { n: ClusterNode; all: ClusterNode[] }) {
  const admin = useIsAdmin()
  const [edit, setEdit] = useState(false)
  const [name, setName] = useState(n.name)
  const [addr, setAddr] = useState(n.sipAddr)
  const [pub, setPub] = useState(n.publicAddr ?? '')
  const [save, { isLoading }] = useSetServerMutation()
  const [forget] = useForgetServerMutation()
  const doSave = async () => {
    try { await save({ name, sipAddr: addr, publicAddr: pub }).unwrap(); setEdit(false); toast.success('Server saved') } catch (e) { toast.error(errorOf(e).message) }
  }
  return (
    <li className="card space-y-2 p-4">
      <div className="flex items-center gap-2">
        <ServerIcon size={16} className={n.alive ? 'text-emerald-300' : 'text-rose-300'} aria-hidden />
        <h2 className="flex-1 font-semibold text-slate-100">{n.name}{n.self && <span className="ml-2 text-xs font-normal text-sky-300">this server</span>}</h2>
        <Badge status={n.alive ? 'ok' : 'fail'}>{n.alive ? 'Running' : 'Stopped'}</Badge>
      </div>
      <dl className="grid grid-cols-[7rem_1fr] gap-y-1 text-sm">
        <dt className="text-slate-500">Reached at</dt><dd className="font-mono text-slate-200">{n.sipAddr}</dd>
        {n.info.public && <><dt className="text-slate-500">Public address</dt><dd className="font-mono text-amber-300">{n.info.public}{n.self && !n.publicAddr && <span className="ml-2 font-sans text-xs text-slate-500">from Settings</span>}</dd></>}
        <dt className="text-slate-500">Phones</dt><dd className="text-slate-200">{n.info.phones}</dd>
        <dt className="text-slate-500">Calls now</dt><dd className="text-slate-200">{n.info.calls?.length ?? 0}</dd>
        <dt className="text-slate-500">{n.alive ? 'Running since' : 'Last seen'}</dt><dd className="text-slate-300">{relTime(n.alive ? n.started : n.seen)}</dd>
        <dt className="text-slate-500">Version</dt><dd className="font-mono text-slate-400">{n.version}</dd>
        {n.alive && all.some((o) => o.alive && o.id !== n.id) && (
          <>
            <dt className="text-slate-500">Other servers</dt>
            <dd>
              {Object.keys(n.info.unreachable ?? {}).length === 0
                ? <span className="text-emerald-300">Reaches every one{n.info.tlsPort ? ', encrypted (TLS and SRTP)' : ''}</span>
                : <ul className="space-y-0.5">{Object.entries(n.info.unreachable!).map(([id, why]) => (
                    <li key={id} className="text-rose-300">Cannot reach {all.find((o) => o.id === id)?.name ?? id}: <span className="text-rose-200/80">{why}</span></li>
                  ))}</ul>}
            </dd>
          </>
        )}
      </dl>
      {admin && n.self && !edit && <button type="button" className="btn text-xs" onClick={() => setEdit(true)}><Pencil size={12} aria-hidden /> Name and addresses</button>}
      {edit && (
        <div className="space-y-2">
          <Field label="Name" htmlFor="srvname"><input id="srvname" className="input" value={name} onChange={(e) => setName(e.target.value)} /></Field>
          <Field label="Where the other servers reach it" htmlFor="srvaddr" hint="Its address and SIP port on the network the servers share, such as 10.0.0.11:5060.">
            <input id="srvaddr" className="input font-mono" value={addr} onChange={(e) => setAddr(e.target.value.trim())} />
          </Field>
          <Field label="Its own public address (optional)" htmlFor="srvpub" hint="Only when each server is reached from the internet at an address of its own (cloud servers with public IPs). Providers and phones outside are then told this one, so a call this server takes over keeps its sound. Empty: the public address in Settings.">
            <input id="srvpub" className="input font-mono" placeholder="203.0.113.7" value={pub} onChange={(e) => setPub(e.target.value.trim())} />
          </Field>
          <div className="flex gap-2"><button type="button" className="btn btn-primary" disabled={isLoading} onClick={doSave}>Save</button><button type="button" className="btn" onClick={() => setEdit(false)}>Cancel</button></div>
        </div>
      )}
      {admin && !n.alive && (
        <button type="button" className="btn text-xs text-rose-300" onClick={() => { if (confirm(`Forget ${n.name}? Do this only if it is gone for good.`)) forget(n.id).unwrap().then(() => toast.success('Forgotten')).catch((e) => toast.error(errorOf(e).message)) }}>
          <Trash2 size={12} aria-hidden /> Forget this server
        </button>
      )}
    </li>
  )
}
