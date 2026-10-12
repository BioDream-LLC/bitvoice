import { copyText } from './Setup'
import { clsx } from 'clsx'
import { CloudUpload, Play, Save, TestTube2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { useSaveSettingsMutation, useSettingsQuery, type ApiErrorBody, type Settings } from './api'
import { useIsAdmin } from './Editor'
import { useDraft } from './useDraft'
import { StaleBanner, radioKeys } from './ui'
import { Badge, Field, Page, Toggle, problemFor, relTime } from './ui'

type Archive = Settings['system']['archive']
interface Status { target: string; lastRun?: string; lastOk?: string; error?: string; uploaded: number; pending: number; running: boolean }

const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }

function policyFor(a: Archive) {
  const prefix = a.s3.prefix ? a.s3.prefix.replace(/\/$/, '') + '/' : ''
  return JSON.stringify({
    Version: '2012-10-17',
    Statement: [{ Sid: 'BitVoiceArchive', Effect: 'Allow', Action: ['s3:PutObject'], Resource: [`arn:aws:s3:::${a.s3.bucket || 'your-bucket'}/${prefix}*`] }],
  }, null, 2)
}

export function ArchivePage() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const { draft: a, setDraft: setA, reset, stale } = useDraft<Archive>(data?.system.archive)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [status, setStatus] = useState<Status | null>(null)
  const [testing, setTesting] = useState(false)
  const [tested, setTested] = useState<{ ok: boolean; text: string } | null>(null)
  useEffect(() => {
    const load = () => fetch('./api/archive').then((r) => r.json()).then(setStatus).catch(() => {})
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  }, [])
  if (!a || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.archive.' + f)
  const set = (v: Partial<Archive>) => { setTested(null); setA((a0) => ({ ...a0, ...v })) }
  const test = async () => {
    setTesting(true); setTested(null)
    try {
      const r = await fetch('./api/archive/test', { method: 'POST', headers: H, body: JSON.stringify(a) })
      const j = await r.json().catch(() => ({}))
      if (r.ok) { setErr(null); setTested({ ok: true, text: `Connected: wrote ${j.wrote}.` }) }
      else if (j.problems) setErr(j)
      else setTested({ ok: false, text: j.message ?? 'The test failed.' })
    } catch { setTested({ ok: false, text: 'BitVoice did not answer. Is it still running?' }) }
    setTesting(false)
  }
  const doSave = async () => {
    try { const saved = await save({ ...data, system: { ...data.system, archive: a } }).unwrap(); reset(saved.system.archive); setErr(null); toast.success('Archive saved') }
    catch (e) { setErr((e as { data?: ApiErrorBody }).data ?? { message: 'The archive settings could not be saved.' }) }
  }
  return (
    <Page icon={CloudUpload} title="Archive" accent="from-sky-400 to-teal-400"
      intro="Keep copies off this computer: a nightly backup of the configuration, and every call recording and voicemail, in Amazon S3 or Azure Blob Storage."
      actions={admin && <button className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={15} aria-hidden /> Save</button>}>
      {status && status.target && (
        <section className="card mb-6 flex flex-wrap items-center gap-4 p-4 text-sm">
          <Badge status={status.error ? 'fail' : status.lastOk ? 'ok' : 'info'}>{status.running ? 'Copying…' : status.error ? 'Problem' : status.lastOk ? 'Up to date' : 'Waiting'}</Badge>
          <span className="font-mono text-xs text-sky-300">{status.target}</span>
          <span className="text-slate-400">{status.uploaded} files copied{status.pending ? `, ${status.pending} waiting` : ''}</span>
          {status.lastOk && <span className="text-slate-500">last copied {relTime(status.lastOk)}</span>}
          {status.error && <span className="w-full text-rose-300">{status.error}</span>}
          {admin && <button className="btn ml-auto" onClick={async () => { const r = await fetch('./api/archive/run', { method: 'POST', headers: H }).catch(() => null); if (r?.ok) toast.success('Copying now'); else toast.error((await r?.json().catch(() => null))?.message ?? 'Could not start copying') }}><Play size={14} className="text-emerald-300" aria-hidden /> Copy now</button>}
        </section>
      )}
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <fieldset disabled={!admin} className="space-y-6">
        <div className="grid gap-3 sm:grid-cols-3" role="radiogroup" aria-label="Where to archive">
          {([['', 'Off', 'Nothing leaves this computer'], ['s3', 'Amazon S3', 'A bucket in your AWS account'], ['azure', 'Azure Blob Storage', 'A container in your Azure storage account']] as const).map(([id, t, d], i) => (
            <button type="button" key={id} role="radio" aria-checked={a.target === id} onClick={() => set({ target: id })}
              {...radioKeys(i, ['', 's3', 'azure'] as const, a.target as '' | 's3' | 'azure', (v) => set({ target: v }))}
              className={clsx('card p-4 text-left', a.target === id && 'ring-2 ring-sky-400')}>
              <div className={clsx('font-semibold', id === 's3' ? 'text-amber-300' : id === 'azure' ? 'text-sky-300' : 'text-slate-300')}>{t}</div>
              <div className="text-xs text-slate-500">{d}</div>
            </button>
          ))}
        </div>
        {a.target === 's3' && (
          <section className="card grid gap-4 p-5 sm:grid-cols-2">
            <Field label="Region" htmlFor="ar" error={p('s3.region')}><input id="ar" className="input font-mono" placeholder="us-east-1" value={a.s3.region} onChange={(e) => set({ s3: { ...a.s3, region: e.target.value.trim() } })} /></Field>
            <Field label="Bucket" htmlFor="ab" error={p('s3.bucket')}><input id="ab" className="input font-mono" value={a.s3.bucket} onChange={(e) => set({ s3: { ...a.s3, bucket: e.target.value.trim() } })} /></Field>
            <Field label="Folder in the bucket" htmlFor="ap"><input id="ap" className="input font-mono" placeholder="bitvoice/" value={a.s3.prefix} onChange={(e) => set({ s3: { ...a.s3, prefix: e.target.value.trim() } })} /></Field>
            <Field label="Access key ID" htmlFor="ak" hint="Leave both keys empty when BitVoice runs on EC2 or ECS with an IAM role: no keys are stored at all."><input id="ak" className="input font-mono" value={a.s3.accessKey} onChange={(e) => set({ s3: { ...a.s3, accessKey: e.target.value.trim() } })} /></Field>
            <Field label="Secret access key" htmlFor="as" error={p('s3.secretKey')} hint="Leave empty to keep the current one."><input id="as" className="input font-mono" type="password" autoComplete="off" value={a.s3.secretKey} onChange={(e) => set({ s3: { ...a.s3, secretKey: e.target.value } })} /></Field>
            <Field label="Endpoint" htmlFor="ae" hint="Only for S3-compatible storage that is not AWS."><input id="ae" className="input font-mono" value={a.s3.endpoint} onChange={(e) => set({ s3: { ...a.s3, endpoint: e.target.value.trim() } })} /></Field>
            <div className="sm:col-span-2">
              <span className="label">The only permission BitVoice needs (attach it to the role or user)</span>
              <pre className="rounded-lg bg-slate-950 p-3 font-mono text-xs text-emerald-300">{policyFor(a)}</pre>
              <button type="button" className="btn mt-2" onClick={async () => { if (await copyText(policyFor(a))) toast.success('Policy copied'); else toast.error('Copying is blocked here; select the text instead') }}>Copy the policy</button>
              <p className="mt-1 text-xs text-slate-500">For tamper-evident retention, turn on S3 Object Lock for the bucket: BitVoice only ever adds files.</p>
            </div>
          </section>
        )}
        {a.target === 'azure' && (
          <section className="card grid gap-4 p-5">
            <Field label="Container SAS URL" htmlFor="az" error={p('azure.containerSas')} hint="Storage account → Containers → your container → Shared access tokens, with Create and Write permission. Leave empty to keep the current one.">
              <input id="az" className="input font-mono" type="password" autoComplete="off" placeholder="https://account.blob.core.windows.net/container?sv=…&sig=…" value={a.azure.containerSas} onChange={(e) => set({ azure: { ...a.azure, containerSas: e.target.value.trim() } })} />
            </Field>
            <Field label="Folder in the container" htmlFor="azp"><input id="azp" className="input font-mono" placeholder="bitvoice" value={a.azure.prefix} onChange={(e) => set({ azure: { ...a.azure, prefix: e.target.value.trim() } })} /></Field>
            <p className="text-xs text-slate-500">For tamper-evident retention, add a time-based immutability policy to the container.</p>
          </section>
        )}
        {a.target && (
          <section className="card space-y-3 p-5">
            <p className="text-sm text-slate-300">The configuration is copied every night. Also copy:</p>
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={a.recordings} onChange={(v) => set({ recordings: v })} label="Call recordings" /> Call recordings</label>
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={a.voicemail} onChange={(v) => set({ voicemail: v })} label="Voicemail" /> Voicemail</label>
            {admin && (
              <div className="flex flex-wrap items-center gap-3">
                <button type="button" className="btn" onClick={test} disabled={testing}><TestTube2 size={14} className="text-emerald-300" aria-hidden /> {testing ? 'Testing…' : 'Test the connection'}</button>
                {tested && <span role="status" className={tested.ok ? 'text-sm text-emerald-300' : 'text-sm text-rose-300'}>{tested.text}</span>}
              </div>
            )}
          </section>
        )}
      </fieldset>
    </Page>
  )
}

