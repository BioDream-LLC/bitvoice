import { ArrowDownToLine, BadgeCheck, CircleAlert, History as HistoryIcon, PackageCheck, RotateCcw, Undo2, Upload } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useApplyUpgradeMutation, useCancelUpgradeMutation, useRollbackUpgradeMutation, useUpgradeQuery, useUploadUpgradeMutation, type ApiErrorBody } from './api'
import { useIsAdmin } from './Editor'
import { Badge, ErrorBox, Field, Page, problemFor, relTime, Spinner } from './ui'

/** Waits for the server to come back after a restart, then reloads the page on the new version. */
export function useComeBack(active: boolean) {
  useEffect(() => {
    if (!active) return
    let stop = false
    const poll = async () => {
      await new Promise((r) => setTimeout(r, 2500))
      while (!stop) {
        try {
          const r = await fetch('./api/health/live', { cache: 'no-store' })
          if (r.ok) { location.reload(); return }
        } catch { /* still restarting */ }
        await new Promise((r) => setTimeout(r, 1500))
      }
    }
    poll()
    return () => { stop = true }
  }, [active])
}

export function Updates() {
  const admin = useIsAdmin()
  const { data, isLoading } = useUpgradeQuery(undefined, { pollingInterval: 3000 })
  const [upload, { isLoading: uploading }] = useUploadUpgradeMutation()
  const [apply] = useApplyUpgradeMutation()
  const [rollback] = useRollbackUpgradeMutation()
  const [cancel] = useCancelUpgradeMutation()
  const [program, setProgram] = useState<File | null>(null)
  const [sig, setSig] = useState<File | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [restarting, setRestarting] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  useComeBack(restarting)
  if (isLoading || !data) return <Page icon={PackageCheck} title="Updates"><Spinner /></Page>
  const st = data.state
  const busy = data.calls > 0

  const send = async () => {
    if (!program) { setErr({ message: 'please fix the highlighted fields', problems: [{ field: 'program', message: 'choose the BitVoice program file' }] }); return }
    const f = new FormData()
    f.append('program', program)
    if (sig) f.append('signature', sig)
    setErr(null)
    try {
      const r = await upload(f).unwrap()
      toast.success(`Version ${r.state.stagedVersion} is ready`)
      setProgram(null); setSig(null)
      if (fileRef.current) fileRef.current.value = ''
    } catch (e) { setErr(errorOf(e)) }
  }
  const go = async (what: 'switch' | 'rollback', when: 'now' | 'idle') => {
    if (when === 'now' && busy && !confirm(`${data.calls} call${data.calls === 1 ? ' is' : 's are'} in progress and will be cut off. Restart now?`)) return
    try {
      const r = await (what === 'switch' ? apply({ when }) : rollback({ when })).unwrap()
      if (!r.waiting || r.calls === 0 || when === 'now') setRestarting(true)
    } catch (e) { toast.error(errorOf(e).message) }
  }

  return (
    <Page icon={PackageCheck} title="Updates" accent="from-emerald-300 to-sky-300"
      intro="Install a new version of BitVoice, and go back to the previous one with one click. The version you are leaving is kept.">
      {restarting && (
        <div role="status" className="card mb-4 flex items-center gap-3 border-sky-800 p-4 text-sky-200"><Spinner label="Restarting" /> BitVoice is restarting. This page reloads when it is back, usually within ten seconds.</div>
      )}
      {data.waiting && !restarting && (
        <div role="status" className="card mb-4 flex flex-wrap items-center gap-3 border-amber-800 p-4 text-amber-200">
          <HistoryIcon size={18} aria-hidden />
          <span className="flex-1">Waiting for {data.calls} call{data.calls === 1 ? '' : 's'} to finish, then {data.waiting === 'switch' ? 'starting the new version' : 'going back'} (asked {relTime(data.since)}).</span>
          {admin && <><button className="btn" onClick={() => go(data.waiting as 'switch' | 'rollback', 'now')}>Restart now</button><button className="btn" onClick={() => cancel()}>Cancel</button></>}
        </div>
      )}
      {!data.possible && <div className="mb-4"><ErrorBox message="This server was started in a way that cannot restart itself, so updates are switched off." /></div>}
      <div className="grid gap-6 lg:grid-cols-2">
        <section className="card space-y-3 p-5" aria-labelledby="ver-h">
          <h2 id="ver-h" className="font-semibold text-slate-100">Versions</h2>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
            <dt className="text-slate-400">Running</dt><dd className="font-mono text-emerald-300">{data.running}{st.pending && <span className="ml-2 font-sans text-xs text-amber-300">on trial: goes back by itself if it fails to start</span>}</dd>
            <dt className="text-slate-400">To go back to</dt><dd className="font-mono text-slate-200">{st.previousVersion || <span className="font-sans text-slate-500">none yet</span>}</dd>
            <dt className="text-slate-400">Ready to install</dt>
            <dd className="font-mono text-sky-300">{st.stagedVersion ? <>{st.stagedVersion} {st.stagedSigned ? <Badge status="ok"><BadgeCheck size={12} aria-hidden /> signed</Badge> : <Badge status="warn">unsigned</Badge>}</> : <span className="font-sans text-slate-500">nothing uploaded</span>}</dd>
          </dl>
          {admin && data.possible && (
            <div className="flex flex-wrap gap-2 pt-2">
              <button className="btn btn-primary" disabled={!st.stagedVersion || !!data.waiting} onClick={() => go('switch', busy ? 'idle' : 'now')}>
                <ArrowDownToLine size={15} aria-hidden /> Install {st.stagedVersion}{busy ? ' when calls finish' : ''}
              </button>
              <button className="btn" disabled={!st.previousVersion || !!data.waiting} onClick={() => go('rollback', busy ? 'idle' : 'now')}>
                <Undo2 size={15} aria-hidden /> Go back to {st.previousVersion || 'the previous version'}
              </button>
            </div>
          )}
          <p className="text-xs text-slate-500">Installing restarts BitVoice: phones re-register on their own and calls in progress end, so by default it waits until nobody is on a call. {busy ? <span className="text-amber-300">{data.calls} call{data.calls === 1 ? ' is' : 's are'} in progress now.</span> : <span className="text-emerald-300">No calls in progress.</span>}</p>
        </section>
        {admin && data.possible && (
          <section className="card space-y-4 p-5" aria-labelledby="up-h">
            <h2 id="up-h" className="flex items-center gap-2 font-semibold text-slate-100"><Upload size={17} className="text-sky-300" aria-hidden /> Upload a new version</h2>
            {err && <ErrorBox message={err.message} problems={err.problems?.filter((p) => p.field !== 'program')} />}
            <Field label="Program" htmlFor="upp" error={problemFor(err?.problems, 'program')} hint="The BitVoice file for this computer, from the release page.">
              <input ref={fileRef} id="upp" type="file" className="input" onChange={(e) => { setProgram(e.target.files?.[0] ?? null); setErr(null) }} />
            </Field>
            <Field label="Signature (.sig)" htmlFor="ups" hint={data.signedBuild ? <span className="text-amber-300">Required: this server installs only releases signed by BioDream.</span> : 'Optional for this development build.'}>
              <input id="ups" type="file" accept=".sig" className="input" onChange={(e) => setSig(e.target.files?.[0] ?? null)} />
            </Field>
            <button className="btn" disabled={uploading} onClick={send}>{uploading ? <Spinner label="Checking" /> : <Upload size={15} aria-hidden />} {uploading ? 'Checking…' : 'Upload and check'}</button>
          </section>
        )}
      </div>
      <section className="card mt-6 p-5" aria-labelledby="hist-h">
        <h2 id="hist-h" className="mb-3 flex items-center gap-2 font-semibold text-slate-100"><RotateCcw size={17} className="text-violet-300" aria-hidden /> What happened</h2>
        {st.history.length === 0 ? <p className="text-sm text-slate-500">Nothing yet.</p> : (
          <ol className="space-y-2 text-sm">
            {st.history.map((h, i) => (
              <li key={i} className="flex gap-3">
                <span className="w-28 shrink-0 text-slate-500" title={new Date(h.at).toLocaleString()}>{relTime(h.at)}</span>
                <span className={h.what.includes('did not start') ? 'text-rose-300' : h.what.includes('went back') ? 'text-amber-300' : 'text-slate-200'}>
                  {h.what.includes('did not start') && <CircleAlert size={14} className="mr-1 inline" aria-hidden />}{h.what}
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>
    </Page>
  )
}
