import { Database as DbIcon, MoveRight, PlugZap } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useDatabaseQuery, useMoveDatabaseMutation, useTestDatabaseMutation } from './api'
import { Field, Spinner } from './ui'
import { useComeBack } from './Updates'

const tableWords: Record<string, string> = { cdrs: 'call records', revisions: 'settings versions', audit: 'audit entries', texts: 'texts', transcripts: 'transcripts', docs: 'lists (users, contacts …)' }

function size(b?: number) {
  if (!b) return ''
  return b > 1e9 ? `${(b / 1e9).toFixed(1)} GB` : b > 1e6 ? `${(b / 1e6).toFixed(1)} MB` : `${Math.ceil(b / 1e3)} kB`
}

/** DatabaseSettings: where BitVoice keeps its data, and moving it into (or out of) PostgreSQL. */
export function DatabaseSettings() {
  const { data } = useDatabaseQuery()
  const [test, { isLoading: testing }] = useTestDatabaseMutation()
  const [move, { isLoading: moving }] = useMoveDatabaseMutation()
  const [url, setUrl] = useState('')
  const [checked, setChecked] = useState<{ where: string; empty: boolean } | null>(null)
  const [restarting, setRestarting] = useState(false)
  useComeBack(restarting)
  if (!data) return null
  const pg = data.kind === 'postgres'
  const doTest = async () => {
    try { setChecked(await test({ url }).unwrap()) } catch (e) { setChecked(null); toast.error(errorOf(e).message) }
  }
  const doMove = async (to: string) => {
    const where = to ? (checked?.where ?? 'PostgreSQL') : "this server's own file"
    if (!confirm(`Copy all the data into ${where} and restart BitVoice on it? Calls cannot be made for the few seconds it takes. The current data is kept where it is.`)) return
    try {
      const r = await move({ url: to }).unwrap()
      toast.success(`Copied into ${r.where}`)
      if (r.restarting) setRestarting(true)
    } catch (e) { toast.error(errorOf(e).message) }
  }
  return (
    <section className="card space-y-3 p-5" aria-labelledby="db-h">
      <h2 id="db-h" className="flex items-center gap-2 font-semibold text-slate-100"><DbIcon size={17} className="text-sky-300" aria-hidden /> Database</h2>
      {restarting && <div role="status" className="flex items-center gap-3 rounded-lg border border-sky-800 p-3 text-sm text-sky-200"><Spinner label="Restarting" /> BitVoice is restarting on the new database. This page reloads when it is back.</div>}
      <p className="text-sm text-slate-300">
        {pg ? <>The data is in <span className="font-medium text-violet-300">PostgreSQL</span> ({data.where}). Other BitVoice servers can share it, and reporting tools can read it.</>
          : <>The data is in <span className="font-medium text-emerald-300">this server's own file</span> <span className="font-mono text-xs text-slate-400">{data.where}</span>{data.bytes ? <span className="text-slate-400"> ({size(data.bytes)})</span> : null}: nothing to install, and one file to back up.</>}
        {' '}Recordings and voicemail stay as sound files in the data folder either way.
      </p>
      {data.counts && (
        <p className="text-xs text-slate-400">{Object.entries(data.counts).filter(([, n]) => n > 0).map(([k, n]) => `${n.toLocaleString()} ${tableWords[k] ?? k}`).join(' · ')}</p>
      )}
      {data.fixed ? (
        <p className="text-sm text-amber-300">BitVoice was started with a database named on its command line (-database or BITVOICE_DATABASE), so it is changed there, not here.</p>
      ) : !pg ? (
        <details className="space-y-2">
          <summary className="cursor-pointer text-sm text-sky-300">Move the data into PostgreSQL</summary>
          <p className="text-xs text-slate-400">For several servers sharing one set of data, or for reports in tools such as Power BI or Grafana. Make an empty database and a user who owns it first.</p>
          <Field label="PostgreSQL address" htmlFor="pgurl" hint="postgres://user:password@host:5432/database — add ?sslmode=require for a server elsewhere. The password is kept in the data folder, readable by BitVoice alone.">
            <div className="flex gap-2">
              <input id="pgurl" className="input font-mono" type="password" autoComplete="off" placeholder="postgres://bitvoice:…@db.example.com:5432/bitvoice" value={url} onChange={(e) => { setUrl(e.target.value.trim()); setChecked(null) }} />
              <button type="button" className="btn" disabled={!url || testing} onClick={doTest}><PlugZap size={14} className="text-amber-300" aria-hidden /> Test</button>
            </div>
          </Field>
          {checked && (
            <p role="status" className={`text-sm ${checked.empty ? 'text-emerald-300' : 'text-rose-300'}`}>
              {checked.empty ? `Reached ${checked.where}: it is empty and ready.` : `Reached ${checked.where}, but it already holds BitVoice data: use an empty database.`}
            </p>
          )}
          <button type="button" className="btn btn-primary" disabled={!checked?.empty || moving} onClick={() => doMove(url)}><MoveRight size={14} aria-hidden /> Copy the data and restart on PostgreSQL</button>
        </details>
      ) : (
        <button type="button" className="btn" disabled={moving} onClick={() => doMove('')}><MoveRight size={14} aria-hidden /> Move the data back into this server's own file</button>
      )}
    </section>
  )
}
