import { useIsAdmin } from './Editor'
import { Copy, GitBranch, KeyRound, Plus, RefreshCw, Trash2, Vault } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useCheckVaultMutation, useAddTokenMutation, useGitOpsQuery, useRevokeTokenMutation, useSyncGitOpsMutation, useTokensQuery, type ApiErrorBody, type Settings } from './api'
import { ChangeList } from './Revisions'
import { copyText } from './Setup'
import { Badge, ErrorBox, Field, problemFor, relTime, Toggle } from './ui'

/** API tokens for Terraform, scripts and CI: made here, shown once, revoked here. */
export function Tokens() {
  const { data } = useTokensQuery()
  const [add, { isLoading }] = useAddTokenMutation()
  const [revoke] = useRevokeTokenMutation()
  const [form, setForm] = useState<{ name: string; role: 'admin' | 'viewer'; days: number } | null>(null)
  const [made, setMade] = useState<string | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const create = async () => {
    if (!form) return
    try {
      const r = await add(form).unwrap()
      setMade(r.secret); setForm(null); setErr(null)
    } catch (e) { setErr(errorOf(e)) }
  }
  return (
    <section className="card mt-6 space-y-4 p-5" aria-labelledby="tok-h">
      <div className="flex items-center justify-between">
        <h2 id="tok-h" className="flex items-center gap-2 font-semibold text-slate-100"><KeyRound size={17} className="text-amber-300" aria-hidden /> API tokens</h2>
        {!form && <button className="btn" onClick={() => { setForm({ name: '', role: 'admin', days: 365 }); setMade(null) }}><Plus size={14} aria-hidden /> New token</button>}
      </div>
      <p className="text-sm text-slate-400">For Terraform, scripts and other programs: send <span className="font-mono text-sky-300">Authorization: Bearer bv_…</span>. The API is described at <a className="text-sky-300 underline" href="./api/openapi.json" target="_blank" rel="noreferrer">/api/openapi.json</a>.</p>
      {made && (
        <div role="status" className="rounded-lg border border-emerald-800 bg-emerald-950/40 p-3 text-sm">
          <p className="mb-2 text-emerald-200">Copy the token now: it is not shown again.</p>
          <div className="flex gap-2">
            <input readOnly className="input font-mono text-xs" value={made} aria-label="New API token" onFocus={(e) => e.target.select()} />
            <button className="btn shrink-0" onClick={async () => { if (await copyText(made)) toast.success('Token copied'); else toast.error('Copying is blocked here; select the text instead') }}><Copy size={14} aria-hidden /> Copy</button>
          </div>
        </div>
      )}
      {form && (
        <form className="grid gap-3 rounded-lg border border-slate-800 p-3 sm:grid-cols-[1fr_10rem_8rem_auto]" onSubmit={(e) => { e.preventDefault(); create() }}>
          {err && !err.problems?.length && <div className="sm:col-span-4"><ErrorBox message={err.message} /></div>}
          <Field label="Name" htmlFor="tkn" error={problemFor(err?.problems, 'name')}><input id="tkn" className="input" autoFocus placeholder="Terraform" value={form.name} onChange={(e) => { const v = e.target.value; setForm((f) => f && { ...f, name: v }) }} /></Field>
          <Field label="Can" htmlFor="tkr" error={problemFor(err?.problems, 'role')}>
            <select id="tkr" className="input" value={form.role} onChange={(e) => { const v = e.target.value as 'admin' | 'viewer'; setForm((f) => f && { ...f, role: v }) }}>
              <option value="admin">change everything</option><option value="viewer">only read</option>
            </select>
          </Field>
          <Field label="Expires in (days)" htmlFor="tkd" error={problemFor(err?.problems, 'days')} hint="0: never."><input id="tkd" type="number" min={0} className="input" value={form.days} onChange={(e) => { const v = Number(e.target.value); setForm((f) => f && { ...f, days: v }) }} /></Field>
          <div className="flex items-end gap-2 pb-0.5"><button className="btn btn-primary" disabled={isLoading}>Make it</button><button type="button" className="btn" onClick={() => { setForm(null); setErr(null) }}>Cancel</button></div>
        </form>
      )}
      {(data?.items.length ?? 0) === 0 ? <p className="text-sm text-slate-500">No tokens.</p> : (
        <ul className="divide-y divide-slate-800 text-sm">
          {data!.items.map((t) => {
            const expired = !!t.expires && new Date(t.expires) < new Date()
            return (
              <li key={t.id} className="flex flex-wrap items-center gap-3 py-2">
                <span className="font-medium text-slate-100">{t.name}</span>
                <span className="font-mono text-xs text-slate-500">{t.prefix}…</span>
                <Badge status={t.role === 'admin' ? 'warn' : 'idle'}>{t.role === 'admin' ? 'Changes everything' : 'Read only'}</Badge>
                <span className={`text-xs ${expired ? 'text-rose-300' : 'text-slate-500'}`}>
                  {t.lastUsed ? `used ${relTime(t.lastUsed)}` : 'never used'} · {t.expires ? (expired ? 'expired' : `expires ${new Date(t.expires).toLocaleDateString()}`) : 'no expiry'}
                </span>
                <button className="btn ml-auto px-2 py-1 text-rose-300" aria-label={`Revoke ${t.name}`} onClick={() => { if (confirm(`Revoke ${t.name}? Programs using it stop working at once.`)) revoke(t.id).unwrap().then(() => toast.success('Token revoked')).catch((e) => toast.error(errorOf(e).message)) }}><Trash2 size={13} aria-hidden /></button>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

type Git = Settings['system']['gitops']

/** The configuration read from a file in a Git repository. */
export function GitSettings({ value, onChange, error }: { value: Git; onChange: (v: Partial<Git>) => void; error: (f: string) => string | undefined }) {
  const admin = useIsAdmin()
  const { data } = useGitOpsQuery(undefined, { pollingInterval: 30000, skip: !admin })
  const [sync, { isLoading }] = useSyncGitOpsMutation()
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="git-h">
      <h2 id="git-h" className="flex items-center gap-2 font-semibold text-slate-100"><GitBranch size={17} className="text-orange-300" aria-hidden /> Configuration from Git</h2>
      <p className="text-sm text-slate-400">Keep the configuration in a Git repository and change it by commits, reviewed like code. Put the file from <a className="text-sky-300 underline" href="./api/config/export?secrets=0">Export for Git</a> (no passwords) in the repository; BitVoice reads it and keeps its own secrets.</p>
      <div className="grid gap-3 md:grid-cols-[1fr_14rem_8rem]">
        <Field label="File address" htmlFor="gu" error={error('system.gitops.url')} hint="The raw address of the file on the branch to follow.">
          <input id="gu" className="input font-mono" placeholder="https://raw.githubusercontent.com/org/phones/main/bitvoice.json" value={value?.url ?? ''} onChange={(e) => onChange({ url: e.target.value.trim() })} />
        </Field>
        <Field label="Access token" htmlFor="gt" hint="Read-only. Leave empty to keep the current one."><input id="gt" type="password" autoComplete="off" className="input font-mono" value={value?.token ?? ''} onChange={(e) => onChange({ token: e.target.value })} /></Field>
        <Field label="Every (minutes)" htmlFor="gm" error={error('system.gitops.everyMinutes')}><input id="gm" type="number" min={1} max={1440} className="input" value={value?.everyMinutes || 5} onChange={(e) => onChange({ everyMinutes: Number(e.target.value) })} /></Field>
      </div>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value?.apply ?? false} onChange={(v) => onChange({ apply: v })} label="Make the file's changes" />
        <span>Make the file's changes <span className="text-xs text-slate-500">(off: only show what would change)</span></span></label>
      {value?.url && data && (
        <div className="space-y-2 rounded-lg border border-slate-800 p-3 text-sm" role="status">
          <div className="flex flex-wrap items-center gap-2">
            {data.last ? <Badge status={data.ok ? (data.changes.length ? 'warn' : 'ok') : 'fail'}>{data.ok ? (data.changes.length ? (value.apply ? 'Changes made' : 'Differs from Git') : 'In step with Git') : 'Problem'}</Badge> : <Badge status="idle">Not checked yet</Badge>}
            {data.last && <span className="text-xs text-slate-500">checked {relTime(data.last)}{data.appliedRev ? `, last applied as revision ${data.appliedRev}` : ''}</span>}
            <button type="button" className="btn ml-auto text-xs" disabled={isLoading} onClick={() => sync().unwrap().catch((e) => toast.error(errorOf(e).message))}><RefreshCw size={13} aria-hidden /> Check now</button>
          </div>
          {data.error && <p className="text-rose-300">{data.error}</p>}
          {data.changes.length > 0 && <ChangeList items={data.changes} />}
        </div>
      )}
    </section>
  )
}

type VaultCfg = Settings['system']['vault']

/** Where secrets written as references come from: AWS Secrets Manager and Azure Key Vault. */
export function VaultSettings({ value, onChange }: { value: VaultCfg; onChange: (v: Partial<VaultCfg>) => void }) {
  const [ref, setRef] = useState('')
  const [check, { isLoading, data, error, reset }] = useCheckVaultMutation()
  const v = value ?? { awsAccessKey: '', awsSecretKey: '', azureTenant: '', azureClientId: '', azureClientSecret: '' }
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="vault-h">
      <h2 id="vault-h" className="flex items-center gap-2 font-semibold text-slate-100"><Vault size={17} className="text-amber-300" aria-hidden /> Secrets in a cloud vault</h2>
      <p className="text-sm text-slate-400">
        Any password or key in BitVoice can be a reference instead: <span className="font-mono text-sky-300">aws-sm:us-east-1:bitvoice/trunk#password</span> for
        AWS Secrets Manager, or <span className="font-mono text-sky-300">azure-kv:my-vault/trunk-password</span> for Azure Key Vault. BitVoice reads it when it is needed and never stores it.
      </p>
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-3">
          <h3 className="text-sm font-medium text-slate-200">AWS</h3>
          <div className="grid grid-cols-2 gap-2">
            <Field label="Access key" htmlFor="vak" hint="Empty: the machine's instance role."><input id="vak" className="input font-mono" autoComplete="off" value={v.awsAccessKey} onChange={(e) => onChange({ awsAccessKey: e.target.value.trim() })} /></Field>
            <Field label="Secret key" htmlFor="vas" hint="Leave empty to keep."><input id="vas" type="password" className="input font-mono" autoComplete="off" value={v.awsSecretKey} onChange={(e) => onChange({ awsSecretKey: e.target.value })} /></Field>
          </div>
        </div>
        <div className="space-y-3">
          <h3 className="text-sm font-medium text-slate-200">Azure</h3>
          <div className="grid grid-cols-3 gap-2">
            <Field label="Tenant" htmlFor="vat"><input id="vat" className="input font-mono" value={v.azureTenant} onChange={(e) => onChange({ azureTenant: e.target.value.trim() })} /></Field>
            <Field label="Client ID" htmlFor="vac" hint="Empty: managed identity."><input id="vac" className="input font-mono" value={v.azureClientId} onChange={(e) => onChange({ azureClientId: e.target.value.trim() })} /></Field>
            <Field label="Client secret" htmlFor="vacs" hint="Leave empty to keep."><input id="vacs" type="password" className="input font-mono" autoComplete="off" value={v.azureClientSecret} onChange={(e) => onChange({ azureClientSecret: e.target.value })} /></Field>
          </div>
        </div>
      </div>
      <form className="flex flex-wrap items-end gap-2" onSubmit={(e) => { e.preventDefault(); reset(); check({ ref }) }}>
        <Field label="Try a reference" htmlFor="vref" error={error ? errorOf(error).problems?.[0]?.message ?? errorOf(error).message : undefined} hint="Uses the saved settings above.">
          <input id="vref" className="input w-[28rem] max-w-full font-mono" value={ref} onChange={(e) => { setRef(e.target.value.trim()); reset() }} placeholder="aws-sm:us-east-1:bitvoice/email" />
        </Field>
        <button className="btn mb-6" disabled={!ref || isLoading}>{isLoading ? 'Reading…' : 'Read it'}</button>
        {data && <span role="status" className="mb-7 text-sm text-emerald-300">Read: {data.length} characters</span>}
      </form>
    </section>
  )
}
