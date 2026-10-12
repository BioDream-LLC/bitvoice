import { Copy, Globe, Network, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useSaveSettingsMutation, useServersDnsQuery, useSettingsQuery, type ApiErrorBody, type ClusterSettings, type SharedAddress } from './api'
import { useIsAdmin } from './Editor'
import { Badge, Field, problemFor, relTime } from './ui'

const empty: ClusterSettings = { name: '', sharedAddress: '', interface: '' }

/** How phones find the servers: one DNS name listing every server, or one address that moves. */
export function ServersSetup({ address }: { address?: SharedAddress }) {
  const admin = useIsAdmin()
  const { data: settings } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const [c, setC] = useState<ClusterSettings>(empty)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  useEffect(() => { if (settings) setC({ ...empty, ...settings.system.cluster }) }, [settings])
  const saved = { ...empty, ...settings?.system.cluster }
  const dirty = JSON.stringify(c) !== JSON.stringify(saved)
  const p = (f: string) => problemFor(err?.problems, 'system.cluster.' + f) ?? problemFor(err?.problems, 'cluster.' + f)
  const set = (k: keyof ClusterSettings, v: string) => setC((x) => ({ ...x, [k]: v.trim() }))
  const submit = async () => {
    if (!settings) return
    try {
      await save({ ...settings, system: { ...settings.system, cluster: c } }).unwrap()
      setErr(null)
      toast.success(c.sharedAddress !== saved.sharedAddress ? 'Saved. Restart each server to answer on the new shared address.' : 'Saved')
    } catch (e) {
      const body = errorOf(e)
      setErr(body)
      toast.error(body?.message ?? 'Could not save')
    }
  }
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="find-h">
      <div>
        <h2 id="find-h" className="flex items-center gap-2 font-semibold text-slate-100"><Globe size={16} className="text-sky-300" aria-hidden /> How phones find the servers</h2>
        <p className="mt-1 text-sm text-slate-400">Phones BitVoice sets up are given a backup server by themselves. For other phones (and Snom), give them one of these instead of a server's own address.</p>
      </div>
      <fieldset disabled={!admin} className="grid gap-4 md:grid-cols-2">
        <Field label="DNS name for every server" htmlFor="cl-name" error={p('name')}
          hint="A name with SRV records listing each server, such as pbx.example.com. Phones set up by BitVoice use it; check the records below.">
          <input id="cl-name" className="input font-mono" value={c.name} placeholder="pbx.example.com" onChange={(e) => set('name', e.target.value)} />
        </Field>
        <div className="grid grid-cols-[1fr_8rem] gap-3">
          <Field label="Shared address" htmlFor="cl-addr" error={p('sharedAddress')}
            hint="An unused address with its network size, such as 192.168.1.50/24. One running server holds it at a time; Linux only, with the NET_ADMIN and NET_RAW permissions.">
            <input id="cl-addr" className="input font-mono" value={c.sharedAddress} placeholder="192.168.1.50/24" onChange={(e) => set('sharedAddress', e.target.value)} />
          </Field>
          <Field label="Network card" htmlFor="cl-if" error={p('interface')} hint="Empty: the one on that network.">
            <input id="cl-if" className="input font-mono" value={c.interface} placeholder="eth0" onChange={(e) => set('interface', e.target.value)} />
          </Field>
        </div>
      </fieldset>
      {admin && (
        <div className="flex justify-end">
          <button type="button" className="btn btn-primary" disabled={!dirty || isLoading} onClick={submit}>Save</button>
        </div>
      )}
      {saved.sharedAddress && <AddressStatus a={address} />}
      {saved.name && <DnsStatus />}
    </section>
  )
}

function AddressStatus({ a }: { a?: SharedAddress }) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-slate-700/60 p-3 text-sm" role="status">
      <Network size={16} className="text-violet-300" aria-hidden />
      <span className="font-mono text-slate-200">{a?.address}</span>
      {a?.error ? <Badge status="fail">This server cannot hold it</Badge> : a?.held ? <Badge status="ok">Held by this server</Badge> : <Badge status="idle">Standby</Badge>}
      <span className={a?.error ? 'text-rose-300' : 'text-slate-400'}>
        {a?.error || (a?.held ? `since ${relTime(a.since)}` : a?.holder ? <>Held by <span className="text-teal-300">{a.holder}</span>; this server takes it within seconds if that one stops</> : 'Waiting for a server to take it')}
      </span>
    </div>
  )
}

function DnsStatus() {
  const { data, isFetching, refetch } = useServersDnsQuery()
  const k = data?.check
  if (!k) return null
  const ok = !k.error && !k.missing?.length && !k.extra?.length
  const copy = async () => { await navigator.clipboard.writeText(k.suggested.join('\n')); toast.success('Records copied') }
  return (
    <div className="space-y-3 rounded-lg border border-slate-700/60 p-3 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <Globe size={16} className="text-sky-300" aria-hidden />
        <span className="font-mono text-slate-200">{k.name}</span>
        {ok ? <Badge status="ok">Lists every running server</Badge> : <Badge status="warn">Needs records</Badge>}
        <span className="text-xs text-slate-500">checked {relTime(k.at)}</span>
        <button type="button" className="btn ml-auto" onClick={() => refetch()} disabled={isFetching} aria-label="Check DNS again"><RefreshCw size={14} aria-hidden className={isFetching ? 'animate-spin' : ''} /> Check again</button>
      </div>
      {k.error && <p className="text-amber-300">{k.error}</p>}
      {!!k.missing?.length && <p className="text-amber-300">Not listed: <span className="font-mono">{k.missing.join(', ')}</span></p>}
      {!!k.extra?.length && <p className="text-amber-300">Listed but no server there: <span className="font-mono">{k.extra.join(', ')}</span></p>}
      {!!k.records?.length && (
        <ul className="space-y-0.5 font-mono text-xs text-slate-400" aria-label="Records found">
          {k.records.map((r, i) => <li key={i}><span className="text-sky-300">{r.service}</span> {r.target}:{r.port}</li>)}
        </ul>
      )}
      {!ok && !!k.suggested?.length && (
        <div>
          <div className="mb-1 flex items-center justify-between">
            <span className="text-slate-300">Records to create at your DNS provider</span>
            <button type="button" className="btn" onClick={copy}><Copy size={14} aria-hidden /> Copy</button>
          </div>
          <pre className="overflow-x-auto rounded bg-slate-950/60 p-2 text-xs text-emerald-200">{k.suggested.join('\n')}</pre>
        </div>
      )}
    </div>
  )
}
