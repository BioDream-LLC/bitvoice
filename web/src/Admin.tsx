import { FraudDecisions, FraudGuardSettings, Rulebooks, TrunkJudgingSettings } from './Judges'
import { Link } from 'react-router-dom'
import { DatabaseSettings } from './Database'
import { CertificateSettings } from './Teams'
import {
  AudioLines, Disc, Download, History, Mail, Send, Sparkles, KeyRound, Save, Settings as SettingsIcon, Shield, ShieldOff, Siren, Upload, UserCog, UserPlus,
} from 'lucide-react'
import { useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  useAuditQuery, useBansQuery, useChangePasswordMutation, useDeleteUserMutation, useExtensionsQuery, useHistoryQuery,
  useImportConfigMutation, useMeQuery, useRestoreMutation, useSaveSettingsMutation, useSaveUserMutation, useSettingsQuery, useSystemQuery,
  useUnbanMutation, useUsersQuery, type ApiErrorBody, type AuditEntry, type Role, type Settings as SettingsT, type User,
} from './api'
import { CompatSettings } from './Compat'
import { SSOSettings } from './SSO'
import { MobileSettings } from './Mobile'
import { EmergencySettings } from './EmergencyMode'
import { CRMSettings } from './Crm'
import { MetricsSettings } from './Metrics'
import { ConsentSettings } from './Consent'
import { ScreeningSettings } from './Screening'
import { GitSettings, Tokens, VaultSettings } from './Automation'
import { Ask } from './Ask'
import { ChatSettings } from './Chat'
import { CDROutputSettings, SNMPSettings, TURNSettings, WebSocketSettings } from './Outputs'
import { CodecList } from './Codecs'
import { attempt, DeleteButton, Drawer, Table, useIsAdmin } from './Editor'
import { createColumnHelper } from '@tanstack/react-table'
import { TwoStep } from './TwoStep'
import { useDraft } from './useDraft'
import { StaleBanner, ListInput } from './ui'
import { ChangeList, RestoreButton, WhatChanged, type Change } from './Revisions'
import { passwordProblem } from './Setup'
import { countryCodes, countryName, emergencyFor } from './countries'
import { AdminsOnly, Badge, Chip, Empty, ErrorBox, Field, Page, Toggle, problemFor, relTime } from './ui'

export function Security() {
  const admin = useIsAdmin()
  const { data: bans } = useBansQuery()
  const { data: audit } = useAuditQuery(undefined, { skip: !admin })
  const [unban] = useUnbanMutation()
  return (
    <Page icon={Shield} title="Security" accent="from-rose-400 to-orange-400"
      intro="BitVoice blocks addresses that keep guessing passwords and records every administrative action.">
      <section className="mb-8">
        <h2 className="mb-3 text-lg font-semibold text-slate-100">Blocked addresses</h2>
        {!bans?.items.length ? <Empty icon={Shield} title="Nothing blocked">Addresses are blocked automatically after repeated failed sign-ins.</Empty> : (
          <ul className="space-y-2">
            {bans.items.map((b) => (
              <li key={b.ip} className="card flex items-center gap-3 p-3 text-sm">
                <Badge status="fail">Blocked</Badge>
                <span className="font-mono text-rose-300">{b.ip}</span>
                <span className="flex-1 text-slate-400">{b.reason} · until {new Date(b.until).toLocaleTimeString()}</span>
                {admin && <button className="btn" onClick={() => attempt(() => unban(b.ip).unwrap(), `Unblocked ${b.ip}`)}><ShieldOff size={14} className="text-emerald-300" aria-hidden /> Unblock</button>}
              </li>
            ))}
          </ul>
        )}
      </section>
      <FraudDecisions />
      <Rulebooks />
      {admin && (
        <section>
          <h2 className="mb-3 text-lg font-semibold text-slate-100">Audit log</h2>
          <Table data={audit?.items ?? []} columns={auditColumns} />
        </section>
      )}
    </Page>
  )
}

const auditCol = createColumnHelper<AuditEntry>()
const auditColumns = [
  auditCol.accessor('at', { header: 'When', cell: (c) => <span className="whitespace-nowrap text-xs text-slate-500">{new Date(c.getValue()).toLocaleString()}</span> }),
  auditCol.accessor((e) => e.actor || 'unknown', { id: 'actor', header: 'Who', cell: (c) => <Chip kind="user">{c.getValue()}</Chip> }),
  auditCol.accessor('action', { header: 'What', cell: (c) => { const a = c.getValue(); return <span className={a.includes('fail') ? 'text-rose-300' : a.includes('delete') ? 'text-amber-300' : 'text-emerald-300'}>{a}</span> } }),
  auditCol.accessor('detail', { header: 'Detail', cell: (c) => <span className="text-slate-400">{c.getValue()}</span> }),
  auditCol.accessor('from', { header: 'From', cell: (c) => <span className="font-mono text-xs text-slate-500">{c.getValue()}</span> }),
]

export function Changes() {
  const admin = useIsAdmin()
  const { data } = useHistoryQuery()
  const [restore] = useRestoreMutation()
  const [importConfig] = useImportConfigMutation()
  const file = useRef<HTMLInputElement>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const current = data?.items[0]?.rev
  const [pending, setPending] = useState<{ name: string; body: unknown; changes: Change[] } | null>(null)
  // Importing replaces everything, so first ask the server what would change, then ask the person.
  const doImport = async (f: File) => {
    let body: unknown
    try { body = JSON.parse(await f.text()) } catch { setErr({ message: 'That file is not a BitVoice configuration.' }); return }
    const r = await fetch('./api/config/import?dryRun=1', { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    const j = await r.json().catch(() => ({}))
    if (!r.ok) { setErr({ message: j.message ?? 'That file could not be read.', problems: j.problems }); return }
    setErr(null)
    setPending({ name: f.name, body, changes: j.items ?? [] })
  }
  const confirmImport = async () => {
    if (!pending) return
    const e = await attempt(() => importConfig(pending.body).unwrap(), 'Configuration imported')
    setErr(e)
    if (!e) setPending(null)
  }
  return (
    <Page icon={History} title="Changes" accent="from-violet-400 to-sky-400"
      intro="Every change is saved as a revision. Restore any earlier one with a click, or export the whole system to a file."
      actions={admin && <>
        <a className="btn" href="./api/config/export" download><Download size={15} className="text-emerald-300" aria-hidden /> Export</a>
        <a className="btn" href="./api/config/export?secrets=0" download title="The whole configuration without passwords, for a Git repository"><Download size={15} className="text-orange-300" aria-hidden /> Export for Git</a>
        <button className="btn" onClick={() => file.current?.click()}><Upload size={15} className="text-sky-300" aria-hidden /> Import</button>
        <input ref={file} type="file" accept="application/json" className="hidden" aria-label="Configuration file" onChange={(e) => { const f = e.target.files?.[0]; if (f) doImport(f); e.target.value = '' }} />
      </>}>
      <Ask />
      {err && <div className="mb-4"><ErrorBox message={err.message} problems={err.problems} fields /></div>}
      {pending && (
        <div className="card mb-6 border-amber-700/50 p-4" role="group" aria-label="Import a configuration">
          <p className="mb-2 text-sm text-amber-200">Importing <span className="font-mono">{pending.name}</span> replaces the whole configuration, making {pending.changes.length} change{pending.changes.length === 1 ? '' : 's'}:</p>
          <ChangeList items={pending.changes} />
          <div className="mt-3 flex gap-2">
            <button type="button" className="btn btn-primary" onClick={confirmImport}><Upload size={14} aria-hidden /> Yes, import it</button>
            <button type="button" className="btn" onClick={() => setPending(null)}>Cancel</button>
          </div>
        </div>
      )}
      <ol className="relative space-y-3 border-l border-slate-800 pl-6">
        {data?.items.map((r) => (
          <li key={r.rev} className="card relative p-4">
            <span className={`absolute -left-[31px] top-5 h-3 w-3 rounded-full ring-4 ring-slate-950 ${r.rev === current ? 'bg-emerald-400' : 'bg-violet-400'}`} />
            <div className="flex items-center gap-3">
              <span className="font-mono text-xs text-violet-300">#{r.rev}</span>
              <span className="font-semibold text-slate-100">{r.summary}</span>
              {r.rev === current && <Badge status="ok">Current</Badge>}
              <span className="ml-auto text-xs text-slate-500">{relTime(r.at)}</span>
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-2 text-sm text-slate-400">
              by <Chip kind="user">{r.actor}</Chip>
              {admin && r.rev !== current && <RestoreButton rev={r.rev} onRestore={() => attempt(() => restore(r.rev).unwrap(), `Restored revision ${r.rev}`)} />}
            </div>
            {admin && r.rev > 1 && <WhatChanged rev={r.rev} />}
          </li>
        ))}
      </ol>
    </Page>
  )
}

export function Users() {
  const admin = useIsAdmin()
  const { data } = useUsersQuery(undefined, { skip: !admin })
  const { data: me } = useMeQuery()
  const [edit, setEdit] = useState<(User & { existing: boolean }) | null>(null)
  return (
    <Page icon={UserCog} title="Administrators" accent="from-fuchsia-400 to-pink-400"
      intro="People who can sign in to this interface. Viewers see everything but change nothing."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ username: '', name: '', role: 'viewer', created: '', existing: false })}><UserPlus size={15} aria-hidden /> Add person</button>}>
      {!admin && <div className="mb-4"><AdminsOnly what="The list of people who can sign in" /></div>}
      <div className="grid gap-3 md:grid-cols-2">
        {data?.items.map((u) => (
          <button key={u.username} className="card flex items-center gap-3 p-4 text-left hover:border-slate-700" onClick={() => admin && setEdit({ ...u, existing: true })} disabled={!admin}>
            <span className="grid h-10 w-10 place-items-center rounded-full bg-fuchsia-950 font-bold text-fuchsia-300">{(u.name || u.username).slice(0, 2).toUpperCase()}</span>
            <div className="flex-1">
              <div className="font-semibold text-slate-100">{u.name || u.username} {u.username === me?.username && <span className="text-xs text-slate-500">(you)</span>}</div>
              <div className="text-xs text-slate-500">{u.username} · last sign-in {u.lastLogin ? relTime(u.lastLogin) : 'never'}{u.sso && <span className="text-sky-300"> · single sign-on</span>}</div>
            </div>
            <Badge status={u.role === 'admin' ? 'info' : 'idle'}>{u.role === 'admin' ? 'Administrator' : 'Viewer'}</Badge>
          </button>
        ))}
      </div>
      <OwnPassword />
      <TwoStep />
      {admin && <Tokens />}
      {edit && <UserEditor u={edit} self={edit.username === me?.username} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function UserEditor({ u, self, onClose }: { u: User & { existing: boolean }; self: boolean; onClose: () => void }) {
  const [x, setX] = useState({ username: u.username, name: u.name, role: u.role as Role, password: '' })
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [save] = useSaveUserMutation()
  const [remove] = useDeleteUserMutation()
  const submit = async () => {
    const e = await attempt(() => save({ ...x, existing: u.existing }).unwrap(), `${x.username} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={u.existing ? u.name || u.username : 'New person'} icon={<UserCog className="text-fuchsia-300" aria-hidden />}
      onSave={submit} error={err}
      footer={u.existing && !self && <DeleteButton what={u.username} onDelete={async () => { if (!await attempt(() => remove(u.username).unwrap(), 'Removed', { problemsInToast: true })) onClose() }} />}>
      <Field label="Username" htmlFor="uu" error={problemFor(err?.problems, 'username')}><input id="uu" className="input" disabled={u.existing} value={x.username} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, username: v })) }} /></Field>
      <Field label="Name" htmlFor="un"><input id="un" className="input" value={x.name} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, name: v })) }} /></Field>
      <Field label="Role" htmlFor="ur">
        <select id="ur" className="input" value={x.role} disabled={self} onChange={(e) => { const v = e.target.value as Role; setX((x0) => ({ ...x0, role: v })) }}>
          <option value="admin">Administrator: can change everything</option><option value="viewer">Viewer: can look but not change</option>
        </select>
      </Field>
      {u.sso ? <p className="text-sm text-slate-400">Signs in through <span className="text-sky-300">single sign-on</span>; the provider's groups set the role again at each sign-in.</p> : self ? <p className="text-sm text-slate-400">To change your own password, use <span className="text-sky-300">Change your password</span> on this page: it asks for the current one.</p> : (
      <Field label={u.existing ? 'New password' : 'Password'} htmlFor="up" error={problemFor(err?.problems, 'password') || (x.password.length >= 10 ? passwordProblem(x.password) || undefined : undefined)} hint={u.existing ? 'Leave empty to keep the current one.' : 'At least 10 characters.'}>
        <input id="up" type="password" autoComplete="new-password" className="input" value={x.password} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, password: v })) }} />
      </Field>
      )}
    </Drawer>
  )
}

function OwnPassword() {
  const [change] = useChangePasswordMutation()
  const [x, setX] = useState({ current: '', new: '' })
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  return (
    <form className="card mt-8 max-w-xl space-y-4 p-5" onSubmit={async (e) => {
      e.preventDefault()
      const r = await attempt(() => change(x).unwrap(), 'Your password was changed')
      setErr(r)
      if (!r) setX({ current: '', new: '' })
    }}>
      <h2 className="flex items-center gap-2 font-semibold text-slate-100"><KeyRound size={17} className="text-amber-300" aria-hidden /> Change your password</h2>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Current password" htmlFor="pc" error={problemFor(err?.problems, 'current')}><input id="pc" type="password" autoComplete="current-password" className="input" value={x.current} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, current: v })) }} /></Field>
        <Field label="New password" htmlFor="pn" error={problemFor(err?.problems, 'password') || (x.new.length >= 10 ? passwordProblem(x.new) || undefined : undefined)}><input id="pn" type="password" autoComplete="new-password" className="input" value={x.new} onChange={(e) => { const v = e.target.value; setX((x0) => ({ ...x0, new: v })) }} /></Field>
      </div>
      <button className="btn" disabled={!x.current || !!passwordProblem(x.new)}><Save size={15} aria-hidden /> Change password</button>
    </form>
  )
}

export function Settings() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const { data: exts } = useExtensionsQuery()
  const { data: sysInfo } = useSystemQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const { draft: s, setDraft: setS, reset, stale } = useDraft<SettingsT>(data)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!s) return null
  const sys = s.system, sec = s.security
  const setSys = (v: Partial<typeof sys>) => setS((s0) => ({ ...s0, system: { ...s0.system, ...v } }))
  const setSec = (v: Partial<typeof sec>) => setS((s0) => ({ ...s0, security: { ...s0.security, ...v } }))
  const doSave = async () => {
    try { reset(await save(s).unwrap()); setErr(null); toast.success('Settings saved') } catch (e) { setErr((e as { data?: ApiErrorBody }).data ?? { message: 'The settings could not be saved.' }) }
  }
  const p = (f: string) => problemFor(err?.problems, f)
  const notify = new Set(sys.emergencyNotify)
  return (
    <Page icon={SettingsIcon} title="Settings" accent="from-slate-300 to-sky-300" intro="System-wide settings. Changes apply at once to new calls; calls in progress are never interrupted."
      actions={admin && <button className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={15} aria-hidden /> Save</button>}>
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      {err && <div className="mb-4"><ErrorBox message={err.message} problems={err.problems} /></div>}
      <fieldset disabled={!admin} className="grid gap-6 lg:grid-cols-2">
        <section className="card space-y-4 p-5">
          <h2 className="font-semibold text-slate-100">System</h2>
          <Field label="Name" htmlFor="sn" error={p('system.name')}><input id="sn" className="input" value={sys.name} onChange={(e) => setSys({ name: e.target.value })} /></Field>
          <Field label="Public address" htmlFor="sa" error={p('system.publicAddress')} hint="Only if phones or trunks reach BitVoice through a router with a public address. Leave empty otherwise.">
            <input id="sa" className="input font-mono" value={sys.publicAddress} onChange={(e) => setSys({ publicAddress: e.target.value.trim() })} />
          </Field>
          <div className="grid grid-cols-3 gap-3">
            <Field label="SIP port" htmlFor="sp" error={p('system.sipPort')}><input id="sp" type="number" className="input" value={sys.sipPort} onChange={(e) => setSys({ sipPort: Number(e.target.value) })} /></Field>
            <Field label="Media from" htmlFor="rm" error={p('system.rtpMin')}><input id="rm" type="number" className="input" value={sys.rtpMin} onChange={(e) => setSys({ rtpMin: Number(e.target.value) })} /></Field>
            <Field label="Media to" htmlFor="rx" error={p('system.rtpMax')}><input id="rx" type="number" className="input" value={sys.rtpMax} onChange={(e) => setSys({ rtpMax: Number(e.target.value) })} /></Field>
          </div>
          <Field label="Codecs" htmlFor="codecs" error={p('system.codecs')} hint="What phones may use, best first. BitVoice converts between any two of them when two parties differ (G.723.1 only with ffmpeg, below).">
            <CodecList id="codecs" value={sys.codecs ?? []} onChange={(v) => setSys({ codecs: v })} error={p('system.codecs')} />
          </Field>
          <div className="space-y-1">
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!sys.g723Convert} onChange={(v) => setSys({ g723Convert: v })} label="Convert G.723.1" />
              <span>Convert G.723.1 with ffmpeg <span className="text-xs text-slate-500">(old video phones and gateways that speak nothing else; off, it only passes between two parties that both use it)</span></span></label>
            {sys.g723Convert && sysInfo && (sysInfo.ffmpeg
              ? <p className="text-xs text-emerald-300">ffmpeg is installed: G.723.1 is converted when needed (ffmpeg&apos;s encoder sends 6.3 kbit/s; both rates are heard).</p>
              : <p className="text-xs text-amber-300">ffmpeg is not installed here, so G.723.1 is not converted. Install it (apt install ffmpeg, brew install ffmpeg) or give its path under video conversion.</p>)}
            {sys.g723Convert && sys.videoOff && (
              <Field label="ffmpeg program" htmlFor="sff2" error={p('system.ffmpeg')} hint="Empty finds ffmpeg on the path.">
                <input id="sff2" className="input font-mono" value={sys.ffmpeg ?? ''} onChange={(e) => setSys({ ffmpeg: e.target.value.trim() })} placeholder="/usr/bin/ffmpeg" />
              </Field>
            )}
          </div>
          <div className="space-y-1">
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!sys.videoOff} onChange={(v) => setSys({ videoOff: !v })} label="Video calls" /><span>Video calls</span></label>
            <p className="text-xs text-slate-500">Video passes between phones (and video apps) that both send it, as it is, when they have a codec in common (most use H.264). A phone without video gets sound only.</p>
            {!sys.videoOff && (
              <div className="space-y-2 pt-1">
                <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!sys.videoTranscode} onChange={(v) => setSys({ videoTranscode: v })} label="Convert video" />
                  <span>Convert video between H.264 and VP8 <span className="text-xs text-slate-500">(when two parties have no codec in common; about a processor core per call)</span></span></label>
                {sys.videoTranscode && (
                  <Field label="ffmpeg program" htmlFor="sff" error={p('system.ffmpeg')} hint="Empty finds ffmpeg on the path.">
                    <input id="sff" className="input font-mono" value={sys.ffmpeg ?? ''} onChange={(e) => setSys({ ffmpeg: e.target.value.trim() })} placeholder="/usr/bin/ffmpeg" />
                  </Field>
                )}
                {sys.videoTranscode && sysInfo && (sysInfo.ffmpeg
                  ? <p className="text-xs text-emerald-300">ffmpeg is installed: video is converted when needed.</p>
                  : <p className="text-xs text-amber-300">ffmpeg is not installed here, so video is not converted. Install it (apt install ffmpeg, brew install ffmpeg) or give its path.</p>)}
              </div>
            )}
          </div>
          <PublicSafetySwitch on={!!sys.publicSafety} set={(v) => setSys({ publicSafety: v })} />
          <Field label="Country" htmlFor="sc" hint="Sets the tones callers hear.">
            <div className="flex gap-2">
              <select id="sc" className="input" value={sys.country} onChange={(e) => setSys({ country: e.target.value })}>
                {countryCodes.map((c) => <option key={c} value={c}>{countryName(c)}</option>)}
              </select>
              {emergencyFor(sys.country).join(',') !== sys.emergencyNumbers.join(',') && (
                <button type="button" className="btn shrink-0" onClick={() => setSys({ emergencyNumbers: emergencyFor(sys.country) })}>Use its emergency numbers</button>
              )}
            </div>
          </Field>
          <Field label="Time zone" htmlFor="tz" error={p('system.timezone')} hint="Used by schedules and shown times.">
            <select id="tz" className="input" value={sys.timezone} onChange={(e) => setSys({ timezone: e.target.value })}>
              <option value="">UTC</option>
              {Intl.supportedValuesOf('timeZone').map((z) => <option key={z} value={z}>{z}</option>)}
            </select>
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Echo test number" htmlFor="se" error={p('system.echoNumber')}><input id="se" className="input font-mono text-violet-300" value={sys.echoNumber} onChange={(e) => setSys({ echoNumber: e.target.value })} /></Field>
            <Field label="Park number" htmlFor="spk" error={p('system.parkNumber')} hint="Transfer a call here to park it; the next nine numbers are the slots.">
              <input id="spk" className="input font-mono text-violet-300" value={sys.parkNumber} onChange={(e) => setSys({ parkNumber: e.target.value.replace(/\D/g, '') })} />
            </Field>
            <Field label="Ring back after (seconds)" htmlFor="sps" error={p('system.parkSeconds')}>
              <input id="sps" type="number" min={0} max={3600} className="input" value={sys.parkSeconds} onChange={(e) => setSys({ parkSeconds: Number(e.target.value) })} />
            </Field>
            <Field label="Voicemail number" htmlFor="sv" error={p('system.voicemailNumber')} hint="Dialled to hear your messages."><input id="sv" className="input font-mono text-violet-300" value={sys.voicemailNumber} onChange={(e) => setSys({ voicemailNumber: e.target.value })} /></Field>
            <Field label="Agent log in and out" htmlFor="sal" error={p('system.agentLogin')} hint="Queue agents dial it to join or leave every queue. off for none.">
              <input id="sal" className="input font-mono text-violet-300" value={sys.agentLogin ?? ''} onChange={(e) => setSys({ agentLogin: e.target.value.trim() })} />
            </Field>
            <Field label="Agent pause" htmlFor="sap" error={p('system.agentPause')} hint="Pauses or resumes queue calls.">
              <input id="sap" className="input font-mono text-violet-300" value={sys.agentPause ?? ''} onChange={(e) => setSys({ agentPause: e.target.value.trim() })} />
            </Field>
            <Field label="Hot desk sign in" htmlFor="shi" error={p('system.hotDesk.login')} hint="Dialled at a hot desk phone: then your extension, #, your voicemail PIN, #.">
              <input id="shi" className="input font-mono text-violet-300" value={sys.hotDesk?.login ?? ''} onChange={(e) => setSys({ hotDesk: { ...sys.hotDesk, login: e.target.value.trim() } })} />
            </Field>
            <Field label="Hot desk sign out" htmlFor="sho" error={p('system.hotDesk.logout')}>
              <input id="sho" className="input font-mono text-violet-300" value={sys.hotDesk?.logout ?? ''} onChange={(e) => setSys({ hotDesk: { ...sys.hotDesk, logout: e.target.value.trim() } })} />
            </Field>
            <Field label="Sign everyone out of hot desks at" htmlFor="shs" error={p('system.hotDesk.signOutAt')} hint="Every day, in the system time zone. Empty: they stay signed in.">
              <input id="shs" type="time" className="input w-36" value={sys.hotDesk?.signOutAt ?? ''} onChange={(e) => setSys({ hotDesk: { ...sys.hotDesk, signOutAt: e.target.value } })} />
            </Field>
          </div>
        </section>
        <section className="card space-y-4 border-rose-900/50 p-5">
          <h2 className="flex items-center gap-2 font-semibold text-slate-100"><Siren size={17} className="text-rose-300" aria-hidden /> Emergency calls</h2>
          <Field label="Emergency numbers" htmlFor="en" error={p('emergencyNumbers')} hint="Always allowed from every extension, even ones that cannot call outside.">
            <input id="en" className="input font-mono text-rose-300" value={sys.emergencyNumbers.join(', ')} onChange={(e) => setSys({ emergencyNumbers: e.target.value.split(/[,\s]+/).filter(Boolean) })} />
          </Field>
          <div>
            <span className="label">Tell these phones when someone dials 911</span>
            <div className="grid gap-1.5 sm:grid-cols-2">
              {exts?.items.map((x) => (
                <label key={x.id} className="flex items-center gap-2 rounded-lg border border-slate-800 px-3 py-2 text-sm">
                  <input type="checkbox" aria-label={`Notify ${x.number} ${x.name}`} checked={notify.has(x.number)} onChange={() => {
                    const n = new Set(notify); if (n.has(x.number)) n.delete(x.number); else n.add(x.number)
                    setSys({ emergencyNotify: [...n] })
                  }} />
                  <span className="font-mono text-sky-300">{x.number}</span> <span className="truncate text-slate-300">{x.name}</span>
                </label>
              ))}
            </div>
            <p className="mt-1 text-xs text-slate-500">They get a message on their phone screen with who called and where from (Kari's Law). Every open BitVoice screen also shows an alert.</p>
          </div>
          <Field label="Also email" htmlFor="eem" error={p('system.emergencyEmail')} hint="A security desk or facilities team. Uses the mail server below.">
            <ListInput id="eem" value={sys.emergencyEmail ?? []} onChange={(v) => setSys({ emergencyEmail: v })} placeholder="security@example.com" />
          </Field>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={sys.sendLocation ?? false} onChange={(v) => setSys({ sendLocation: v })} label="Send the caller's address" />
            <span>Send the caller's address with the call <span className="text-xs text-slate-500">(PIDF-LO, from <Link to="/sites" className="text-sky-300 underline">Sites</Link>; for providers that take it)</span></span></label>
        </section>
        <ChatSettings chat={sys.chat} onChange={(v) => setSys({ chat: v })} p={p} />
        <SNMPSettings snmp={sys.snmp} onChange={(v) => setSys({ snmp: v })} p={p} />
        <TURNSettings turn={sys.turn} onChange={(v) => setSys({ turn: v })} p={p} />
        <WebSocketSettings value={sys.webSocket} onChange={(v) => setSys({ webSocket: v })} />
        <CDROutputSettings out={sys.cdrOutputs} onChange={(v) => setSys({ cdrOutputs: v })} p={p} />
        <EmailSettings email={sys.email} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, email: { ...s0.system.email, ...v } } }))} error={p} admin={admin} />
        <EmergencySettings value={sys.emergencyMode} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, emergencyMode: { ...s0.system.emergencyMode, ...v } } }))} error={p} />
        <VaultSettings value={sys.vault} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, vault: { ...s0.system.vault, ...v } } }))} />
        <GitSettings value={sys.gitops} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, gitops: { ...s0.system.gitops, ...v } } }))} error={p} />
        <MobileSettings value={sys.mobile} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, mobile: { ...s0.system.mobile, ...v } } }))} error={p} />
        <CRMSettings value={sys.crm} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, crm: { ...s0.system.crm, ...v } } }))} error={p} />
        <ScreeningSettings value={sys.screening ?? { failed: 'label', unverified: 'allow', anonymous: 'allow' }} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, screening: { ...s0.system.screening, ...v } } }))} error={p} />
        <DatabaseSettings />
        <CertificateSettings />
        <MetricsSettings value={sys.metrics} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, metrics: { ...s0.system.metrics, ...v } } }))} error={p} />
        <SSOSettings value={sys.sso} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, sso: { ...s0.system.sso, ...v } } }))} error={p} />
        <CompatSettings value={sys.compat} onChange={(v) => setS((s0) => ({ ...s0, system: { ...s0.system, compat: { ...s0.system.compat, ...v } } }))} error={p} admin={admin} />
        <section className="card space-y-4 p-5 lg:col-span-2">
          <h2 className="flex items-center gap-2 font-semibold text-slate-100"><Sparkles size={17} className="text-violet-300" aria-hidden /> AI (optional)</h2>
          <p className="text-sm text-slate-400">A language model can write out and summarise voicemail and recorded calls, answer as a receptionist and turn plain words into changes. What it needs is sent to the model service you choose, and only when this is on. Any OpenAI-compatible service works: <span className="text-sky-300">OpenRouter</span> by default, or a model running on your own computer.</p>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={sys.ai.enabled} onChange={(v) => setSys({ ai: { ...sys.ai, enabled: v } })} label="Use AI" /> Use AI</label>
          {sys.ai.enabled && (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Service address" htmlFor="aib" error={p('system.ai.baseUrl')} hint="Empty uses OpenRouter."><input id="aib" className="input font-mono" placeholder="https://openrouter.ai/api/v1" value={sys.ai.baseUrl} onChange={(e) => setSys({ ai: { ...sys.ai, baseUrl: e.target.value.trim() } })} /></Field>
              <Field label="Model" htmlFor="aim" error={p('system.ai.model')} hint="For text: summaries, receptionists, asking. Without a speech-to-text model or service it must also accept audio. May be empty with a speech-to-text service: transcripts only."><input id="aim" className="input font-mono" placeholder="google/gemini-2.5-flash" value={sys.ai.model} onChange={(e) => setSys({ ai: { ...sys.ai, model: e.target.value.trim() } })} /></Field>
              <Field label="API key" htmlFor="aik" hint="Leave empty to keep the current one."><input id="aik" className="input font-mono" type="password" autoComplete="off" value={sys.ai.key} onChange={(e) => setSys({ ai: { ...sys.ai, key: e.target.value } })} /></Field>
              <Field label="Speech-to-text model" htmlFor="ai-am" error={p('system.ai.audioModel')} hint={sys.ai.transcribeUrl ? 'Not used: the speech-to-text service below writes speech down.' : 'A model that takes audio, for transcripts and receptionists, such as google/gemini-2.5-flash. Empty: the model above.'}>
                <input id="ai-am" className="input font-mono" disabled={!!sys.ai.transcribeUrl} value={sys.ai.audioModel ?? ''} onChange={(e) => setSys({ ai: { ...sys.ai, audioModel: e.target.value.trim() } })} />
              </Field>
              <div className="space-y-3 rounded-lg border border-slate-800 p-4 sm:col-span-2">
                <h3 className="flex items-center gap-2 text-sm font-semibold text-slate-200"><AudioLines size={15} className="text-emerald-300" aria-hidden /> Speech-to-text service <span className="text-xs font-normal text-emerald-300">no language model, can stay in the building</span></h3>
                <p className="text-xs text-slate-400">A speech recogniser such as whisper.cpp's <span className="font-mono">whisper-server</span>, run on this computer or one on your network, writes down what callers and staff say: their audio never leaves the building and nothing is paid per call. An OpenAI-compatible <span className="font-mono">…/audio/transcriptions</span> works too. With one set, the model above may be left empty: transcripts are written, and summaries and receptionists (which need a language model) are left out.</p>
                <div className="grid gap-4 sm:grid-cols-3">
                  <Field label="Address" htmlFor="ai-tu" error={p('system.ai.transcribeUrl')} hint="Empty: the model writes speech down.">
                    <input id="ai-tu" className="input font-mono" placeholder="http://127.0.0.1:8178/inference" value={sys.ai.transcribeUrl ?? ''} onChange={(e) => setSys({ ai: { ...sys.ai, transcribeUrl: e.target.value.trim() } })} />
                  </Field>
                  <Field label="Its model" htmlFor="ai-tm" hint="Only for services that ask, such as whisper-1. whisper-server ignores it.">
                    <input id="ai-tm" className="input font-mono" disabled={!sys.ai.transcribeUrl} value={sys.ai.transcribeModel ?? ''} onChange={(e) => setSys({ ai: { ...sys.ai, transcribeModel: e.target.value.trim() } })} />
                  </Field>
                  <Field label="Its key" htmlFor="ai-tk" hint="Only for services that ask. Leave empty to keep the current one.">
                    <input id="ai-tk" className="input font-mono" type="password" autoComplete="off" disabled={!sys.ai.transcribeUrl} value={sys.ai.transcribeKey ?? ''} onChange={(e) => setSys({ ai: { ...sys.ai, transcribeKey: e.target.value } })} />
                  </Field>
                </div>
              </div>
              <label className="flex items-center gap-3 self-end pb-2 text-sm text-slate-300"><Toggle checked={sys.ai.voicemail} onChange={(v) => setSys({ ai: { ...sys.ai, voicemail: v } })} label="Transcribe voicemail" /> Transcribe and summarise voicemail</label>
              <div className="sm:col-span-2">
                <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!sys.ai.calls} onChange={(v) => setSys({ ai: { ...sys.ai, calls: v } })} label="Transcribe recorded calls" /> Transcribe recorded calls, with a summary and action items</label>
                <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!sys.ai.insights} onChange={(v) => setSys({ ai: { ...sys.ai, insights: v } })} label="Call insight" />
                  <span>Note how each transcribed call went <span className="text-xs text-slate-500">(the caller's mood at the start and end, and moments a supervisor may want to hear, each with what was said; never a score)</span></span></label>
                {(sys.publicSafety || sys.ai.liveTranscripts) && <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!sys.ai.liveTranscripts} onChange={(v) => setSys({ ai: { ...sys.ai, liveTranscripts: v } })} label="Write out 911 calls live" />
                  <span>Write out 911 calls live <span className="text-xs text-slate-500">(each side as it is spoken, on the 911 screen and to CAD as call.transcript events; the calls' audio goes to the speech-to-text service, or the AI service, while they last, recorded or not)</span></span></label>}
                <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!sys.ai.briefings} onChange={(v) => setSys({ ai: { ...sys.ai, briefings: v } })} label="Transfer briefings" />
                  <span>Brief whoever a call is transferred to <span className="text-xs text-slate-500">(a two-sentence summary of the call so far, on their browser phone and desk phone screen; the call's audio goes to the AI service)</span></span></label>
                <p className="mt-1 pl-14 text-xs text-slate-500">Only calls that are recorded (Settings, Call recording, or an extension's own setting); each side is written out separately, so the transcript knows who said what. Calls can then be searched by what was said in <a className="text-sky-300 hover:underline" href="#/history">Call history</a>.</p>
              </div>
              <Field label="Speech model" htmlFor="ai-sm" hint="For AI receptionists: a text-to-speech model at the same service (an OpenAI-compatible /audio/speech), such as tts-1. Empty: receptionists hand every call to a person.">
                <input id="ai-sm" className="input font-mono" value={sys.ai.speechModel ?? ''} onChange={(e) => setSys({ ai: { ...sys.ai, speechModel: e.target.value } })} />
              </Field>
              <Field label="Voice" htmlFor="ai-v" hint="The speech model's voice name, such as alloy.">
                <input id="ai-v" className="input font-mono" value={sys.ai.voice ?? ''} onChange={(e) => setSys({ ai: { ...sys.ai, voice: e.target.value } })} />
              </Field>
            </div>
          )}
        </section>
        <section className="card space-y-3 p-5" aria-labelledby="audio-h">
          <h2 id="audio-h" className="flex items-center gap-2 font-semibold text-slate-100"><AudioLines size={17} className="text-emerald-300" aria-hidden /> Sound clean-up</h2>
          <p className="text-sm text-slate-400">Turns down steady background noise (fans, traffic, hiss) and evens out quiet and loud talkers.</p>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={sys.audio?.voicemail ?? false} onChange={(v) => setSys({ audio: { ...sys.audio, voicemail: v } })} label="Clean up voicemail and greetings" /> Clean up voicemail and recorded greetings as they are saved</label>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={sys.audio?.calls ?? false} onChange={(v) => setSys({ audio: { ...sys.audio, calls: v } })} label="Clean up calls" />
            <span>Clean up what each person sends during calls <span className="text-xs text-slate-500">(standard-quality calls; adds about 16 ms of delay)</span></span></label>
        </section>
        <section className="card space-y-4 p-5 lg:col-span-2">
          <h2 className="flex items-center gap-2 font-semibold text-slate-100"><Disc size={17} className="text-rose-300" aria-hidden /> Call recording</h2>
          <div className="grid gap-2 sm:grid-cols-3">
            {([['off', 'Off', 'No calls are recorded'], ['selected', 'Selected extensions', 'Calls to or from extensions marked Record calls'], ['all', 'Every call', 'Every answered call']] as const).map(([id, t, d]) => (
              <button type="button" key={id} aria-pressed={sys.recording.mode === id} onClick={() => setSys({ recording: { ...sys.recording, mode: id } })}
                className={`card p-3 text-left ${sys.recording.mode === id ? 'ring-2 ring-rose-400' : ''}`}>
                <div className={`text-sm font-semibold ${id === 'off' ? 'text-slate-300' : 'text-rose-300'}`}>{t}</div>
                <div className="text-xs text-slate-500">{d}</div>
              </button>
            ))}
          </div>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Keep recordings (days)" htmlFor="rk" error={p('system.recording.keepDays')}>
              <input id="rk" type="number" min={0} max={3650} className="input" value={sys.recording.keepDays} onChange={(e) => setSys({ recording: { ...sys.recording, keepDays: Number(e.target.value) } })} />
            </Field>
            <Field label="Keep call history (days)" htmlFor="rck" error={p('system.retention.callRecordDays')}>
              <input id="rck" type="number" min={0} className="input" value={sys.retention?.callRecordDays ?? 0} onChange={(e) => setSys({ retention: { ...sys.retention, callRecordDays: Number(e.target.value) } })} />
            </Field>
            <Field label="Keep voicemail (days)" htmlFor="rvk" error={p('system.retention.voicemailDays')}>
              <input id="rvk" type="number" min={0} className="input" value={sys.retention?.voicemailDays ?? 0} onChange={(e) => setSys({ retention: { ...sys.retention, voicemailDays: Number(e.target.value) } })} />
            </Field>
          </div>
          <p className="text-xs text-slate-500">0 keeps them until you delete them. Anything under a <Link to="/holds" className="text-sky-300 underline">legal hold</Link> is kept whatever its age. Recording laws differ: some places need everyone on the call to agree.</p>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Pause recording code" htmlFor="rpc" error={p('system.recording.pauseCode')} hint="Pressed on an extension during a call, before a card number is read out (PCI DSS).">
              <input id="rpc" className="input font-mono" value={sys.recording.pauseCode} onChange={(e) => setSys({ recording: { ...sys.recording, pauseCode: e.target.value.replace(/[^0-9*#]/g, '') } })} maxLength={6} />
            </Field>
            <Field label="Resume recording code" htmlFor="rrc" error={p('system.recording.resumeCode')}>
              <input id="rrc" className="input font-mono" value={sys.recording.resumeCode} onChange={(e) => setSys({ recording: { ...sys.recording, resumeCode: e.target.value.replace(/[^0-9*#]/g, '') } })} maxLength={6} />
            </Field>
          </div>
          <ConsentSettings rec={sys.recording} set={(r) => setSys({ recording: r })} p={p} />
        </section>
        <section className="card space-y-4 p-5 lg:col-span-2">
          <h2 className="flex items-center gap-2 font-semibold text-slate-100"><Shield size={17} className="text-rose-300" aria-hidden /> Protection</h2>
          <div className="grid gap-4 sm:grid-cols-4">
            <Field label="Block after failures" htmlFor="ft" error={p('security.failBanThreshold')}><input id="ft" type="number" className="input" value={sec.failBanThreshold} onChange={(e) => setSec({ failBanThreshold: Number(e.target.value) })} /></Field>
            <Field label="Within (seconds)" htmlFor="fw" error={p('security.failBanWindowSecs')}><input id="fw" type="number" className="input" value={sec.failBanWindowSecs} onChange={(e) => setSec({ failBanWindowSecs: Number(e.target.value) })} /></Field>
            <Field label="Block for (minutes)" htmlFor="fm" error={p('security.failBanMinutes')}><input id="fm" type="number" className="input" value={sec.failBanMinutes} onChange={(e) => setSec({ failBanMinutes: Number(e.target.value) })} /></Field>
            <Field label="Calls per extension" htmlFor="mc" error={p('security.maxCallsPerExtension')}><input id="mc" type="number" className="input" value={sec.maxCallsPerExtension} onChange={(e) => setSec({ maxCallsPerExtension: Number(e.target.value) })} /></Field>
          </div>
          <Field label="Never block these networks" htmlFor="tn" error={p('trustedNetworks')} hint="One per line, for example 192.168.1.0/24 for your office network.">
            <textarea id="tn" rows={3} className="input font-mono" value={sec.trustedNetworks.join('\n')} onChange={(e) => setSec({ trustedNetworks: e.target.value.split('\n').map((x) => x.trim()).filter(Boolean) })} />
          </Field>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={sec.blockPremium} onChange={(v) => setSec({ blockPremium: v })} label="Block premium numbers" /> Block premium-rate numbers (900, 976), the usual target of toll fraud</label>
        </section>
        <FraudGuardSettings value={sec.fraudGuard} onChange={(v) => setSec({ fraudGuard: v })} p={p} />
        <TrunkJudgingSettings value={sys.trunkJudging} onChange={(v) => setSys({ trunkJudging: v })} p={p} />
      </fieldset>
    </Page>
  )
}

/** Shows the 911 profile, radio console and Core Services in the menu: a lab track, off on a phone system. */
function PublicSafetySwitch({ on, set }: { on: boolean; set: (v: boolean) => void }) {
  const { data: info } = useSystemQuery()
  const inUse = info?.publicSafetyInUse
  return (
    <div className="space-y-1">
      <label className="flex items-center gap-3 text-sm text-slate-300">
        <Toggle checked={on || !!inUse} disabled={!!inUse} onChange={set} label="Public-safety profiles (lab)" /><span>Public-safety profiles (lab)</span>
      </label>
      <p className="text-xs text-slate-500">
        Shows the 911 profile, the radio console and NG911 Core Services in the menu. They are a <span className="text-amber-300">lab and design-partner track</span>: not certified for live 911 or radio dispatch, and not yet tried with a live ESInet, P25 system or CAD. Office emergency calling (911 from your phones, sites) does not depend on this and is always on.
        {inUse && <> <span className="text-amber-300">Stays on while {inUse} is set up.</span></>}
      </p>
    </div>
  )
}

function EmailSettings({ email, onChange, error, admin }: {
  email: SettingsT['system']['email']; onChange: (v: Partial<SettingsT['system']['email']>) => void; error: (f: string) => string | undefined; admin: boolean
}) {
  const [to, setTo] = useState('')
  const [sending, setSending] = useState(false)
  const set = onChange
  const test = async () => {
    setSending(true)
    try {
      const r = await fetch('./api/email/test', { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify({ email, to }) })
      const j = await r.json().catch(() => ({}))
      if (r.ok) toast.success(`Test message sent to ${to}`)
      else toast.error(j.message ?? 'The test failed')
    } catch { toast.error('BitVoice did not answer. Is it still running?') }
    setSending(false)
  }
  return (
    <section className="card space-y-4 p-5 lg:col-span-2">
      <h2 className="flex items-center gap-2 font-semibold text-slate-100"><Mail size={17} className="text-sky-300" aria-hidden /> Email</h2>
      <p className="text-sm text-slate-400">The mail server BitVoice sends voicemail through: your own, Microsoft 365 (smtp.office365.com), Google Workspace (smtp.gmail.com) or Amazon SES.</p>
      <div className="grid gap-4 sm:grid-cols-3">
        <Field label="Mail server" htmlFor="eh" error={error('system.email.host')}><input id="eh" className="input font-mono" placeholder="smtp.example.com" value={email.host} onChange={(e) => set({ host: e.target.value.trim() })} /></Field>
        <Field label="Port" htmlFor="ep" error={error('system.email.port')} hint="587 or 465."><input id="ep" type="number" className="input" value={email.port || 587} onChange={(e) => set({ port: Number(e.target.value) })} /></Field>
        <Field label="From" htmlFor="ef" error={error('system.email.from')}><input id="ef" className="input" placeholder="Phones <phones@example.com>" value={email.from} onChange={(e) => set({ from: e.target.value })} /></Field>
        <Field label="User name" htmlFor="eu"><input id="eu" className="input" autoComplete="off" value={email.username} onChange={(e) => set({ username: e.target.value.trim() })} /></Field>
        <Field label="Password" htmlFor="ew" hint="Leave empty to keep the current one."><input id="ew" type="password" className="input" autoComplete="new-password" value={email.password} onChange={(e) => set({ password: e.target.value })} /></Field>
        {admin && (
          <div className="flex items-end gap-2">
            <input className="input" aria-label="Send a test to" placeholder="you@example.com" value={to} onChange={(e) => setTo(e.target.value.trim())} />
            <button type="button" className="btn" disabled={sending || !email.host || !to.includes('@')} onClick={test}><Send size={14} className="text-emerald-300" aria-hidden /> {sending ? 'Sending…' : 'Test'}</button>
          </div>
        )}
      </div>
    </section>
  )
}
