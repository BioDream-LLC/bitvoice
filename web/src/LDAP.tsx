import { clsx } from 'clsx'
import { FolderTree, PlugZap, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useLdapStatusQuery, useRingGroupsQuery, useSyncLdapMutation, useTestLdapMutation, type LDAPReport, type LDAPSettings } from './api'
import { Field, ListInput, Toggle, relTime } from './ui'

/** The defaults of each kind of directory, as the server fills them in when the boxes are empty. */
const defaults = {
  ad: { userFilter: '(&(objectCategory=person)(objectClass=user)(!(userAccountControl:1.2.840.113556.1.4.803:=2)))', loginAttr: 'sAMAccountName', nameAttr: 'displayName', emailAttr: 'mail', numberAttr: 'telephoneNumber' },
  ldap: { userFilter: '(objectClass=inetOrgPerson)', loginAttr: 'uid', nameAttr: 'cn', emailAttr: 'mail', numberAttr: 'telephoneNumber' },
}

/** What a sync did, in plain words. */
function Report({ r }: { r: LDAPReport }) {
  const lines: [string, string[], string][] = [['Made', r.created, 'text-emerald-300'], ['Updated', r.updated, 'text-sky-300'], ['Turned off', r.disabled, 'text-amber-300'],
    ['Turned on again', r.enabled, 'text-emerald-300'], ['Ring groups', r.ringGroups, 'text-violet-300'], ['To look at', r.problems, 'text-amber-300']]
  return (
    <div role="status" className="space-y-1 rounded-lg border border-slate-800 p-3 text-sm">
      {r.error ? <p className="text-rose-300">The last sync failed {relTime(r.at)}: {r.error}</p> : (
        <p className="text-slate-300">Read {relTime(r.at)}: <span className="text-sky-300">{r.people} people</span>, {r.withNumber} with a number. {r.changed ? <span className="text-emerald-300">Changes were saved as one revision.</span> : <span className="text-slate-400">Nothing needed changing.</span>}</p>
      )}
      {lines.filter(([, l]) => l?.length).map(([label, l, colour]) => (
        <p key={label}><span className={colour}>{label}:</span> <span className="text-slate-300">{l.join('; ')}</span></p>
      ))}
    </div>
  )
}

/** The company directory: Active Directory or another LDAP server makes extensions and fills ring groups, and its people may sign in. */
export function LDAPSettingsCard({ value, onChange, error, admin }: { value: LDAPSettings; onChange: (v: Partial<LDAPSettings>) => void; error: (f: string) => string | undefined; admin: boolean }) {
  const { data: status } = useLdapStatusQuery(undefined, { skip: !admin })
  const { data: groups } = useRingGroupsQuery()
  const [test, { isLoading: testing }] = useTestLdapMutation()
  const [sync, { isLoading: syncing }] = useSyncLdapMutation()
  const [result, setResult] = useState<Awaited<ReturnType<ReturnType<typeof test>['unwrap']>> | null>(null)
  const d = defaults[value.kind === 'ad' ? 'ad' : 'ldap']
  const maps = value.ringGroups ?? []
  const setMap = (i: number, v: Partial<{ group: string; ringGroup: string }>) => onChange({ ringGroups: maps.map((m, j) => (j === i ? { ...m, ...v } : m)) })
  const plain = value.url.startsWith('ldap://') && !value.startTls
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="ldap-h">
      <h2 id="ldap-h" className="flex items-center gap-2 font-semibold text-slate-100"><FolderTree size={17} className="text-emerald-300" aria-hidden /> Company directory (Active Directory and LDAP)</h2>
      <p className="text-sm text-slate-400">
        BitVoice reads your directory: people with a phone number become <span className="text-sky-300">extensions</span>, directory groups fill <span className="text-violet-300">ring groups</span>,
        and people can sign in here with their directory password. It only reads; nothing is written to the directory.
      </p>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value.enabled} onChange={(v) => onChange({ enabled: v })} label="Use the company directory" /> Use the company directory</label>
      {value.enabled && (
        <>
          <div className="flex gap-2" role="radiogroup" aria-label="Kind of directory">
            {(['ad', 'ldap'] as const).map((k) => (
              <button key={k} type="button" role="radio" aria-checked={value.kind === k} className={clsx('btn', value.kind === k && 'border-emerald-500 text-emerald-200')}
                onClick={() => onChange({ kind: k, ...(value.kind !== k ? { userFilter: '', loginAttr: '', nameAttr: '', emailAttr: '', numberAttr: '' } : {}) })}>
                {k === 'ad' ? 'Active Directory' : 'OpenLDAP or another LDAP server'}
              </button>
            ))}
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Server" htmlFor="ldu" error={error('system.ldap.url')} hint={plain ? <span className="text-amber-300">Plain ldap:// sends passwords unencrypted: switch StartTLS on, or use ldaps://.</span> : 'ldaps://dc1.example.com (port 636), or ldap:// with StartTLS.'}>
              <input id="ldu" className="input font-mono" placeholder="ldaps://dc1.example.com" value={value.url} onChange={(e) => onChange({ url: e.target.value.trim() })} />
            </Field>
            <label className="flex items-center gap-3 pt-7 text-sm text-slate-300"><Toggle checked={value.startTls} onChange={(v) => onChange({ startTls: v })} label="StartTLS" /> StartTLS (encrypt an ldap:// connection)</label>
            <Field label="Reading account" htmlFor="ldb" error={error('system.ldap.bindDn')} hint={value.kind === 'ad' ? 'reader@example.com, or its DN.' : 'Its DN, such as cn=reader,dc=example,dc=com.'}>
              <input id="ldb" className="input font-mono" autoComplete="off" value={value.bindDn} onChange={(e) => onChange({ bindDn: e.target.value.trim() })} />
            </Field>
            <Field label="Its password" htmlFor="ldp" error={error('system.ldap.bindPassword')} hint="Leave empty to keep the current one. A vault reference works too.">
              <input id="ldp" type="password" autoComplete="off" className="input font-mono" value={value.bindPassword} onChange={(e) => onChange({ bindPassword: e.target.value })} />
            </Field>
            <Field label="Base DN" htmlFor="ldbase" error={error('system.ldap.baseDn')} hint="Where people and groups are.">
              <input id="ldbase" className="input font-mono" placeholder="dc=example,dc=com" value={value.baseDn} onChange={(e) => onChange({ baseDn: e.target.value.trim() })} />
            </Field>
            <Field label="Which people" htmlFor="ldf" error={error('system.ldap.userFilter')} hint="An LDAP filter; empty for everyone (on Active Directory, disabled accounts are left out).">
              <input id="ldf" className="input font-mono" placeholder={d.userFilter} value={value.userFilter === d.userFilter ? '' : value.userFilter} onChange={(e) => onChange({ userFilter: e.target.value.trim() })} />
            </Field>
          </div>
          <div className="grid gap-4 md:grid-cols-5">
            {([['loginAttr', 'Sign-in name', 'ldla'], ['nameAttr', 'Name', 'ldna'], ['emailAttr', 'Email', 'ldea'], ['numberAttr', 'Phone number', 'ldnu']] as const).map(([k, label, id]) => (
              <Field key={k} label={label} htmlFor={id} hint={`Attribute (${d[k]})`}>
                <input id={id} className="input font-mono" placeholder={d[k]} value={value[k] === d[k] ? '' : value[k]} onChange={(e) => onChange({ [k]: e.target.value.trim() })} />
              </Field>
            ))}
            <Field label="Digits kept" htmlFor="lddig" error={error('system.ldap.numberDigits')} hint="The last digits are the extension (4 of +1 212 555 0123 is 0123). 0: all.">
              <input id="lddig" type="number" min={0} max={8} className="input" value={value.numberDigits ?? 0} onChange={(e) => onChange({ numberDigits: Number(e.target.value) || 0 })} />
            </Field>
          </div>
          <Field label="Company certificate authority (optional)" htmlFor="ldca" error={error('system.ldap.caCert')} hint="Paste it (PEM) when the server's certificate comes from your own authority; empty trusts the computer's authorities.">
            <textarea id="ldca" className="input h-20 font-mono text-xs" spellCheck={false} placeholder="-----BEGIN CERTIFICATE-----" value={value.caCert ?? ''} onChange={(e) => onChange({ caCert: e.target.value })} />
          </Field>
          {admin && (
            <div className="flex flex-wrap items-center gap-2">
              <button type="button" className="btn" disabled={testing || !value.url} onClick={() => test(value).unwrap().then(setResult).catch((e) => setResult({ ok: false, message: errorOf(e).message }))}>
                <PlugZap size={14} aria-hidden /> Test
              </button>
              {result && (
                <span role="status" className={result.ok ? 'text-sm text-emerald-300' : 'text-sm text-rose-300'}>
                  {result.ok ? `Signed in${result.secure ? ' over an encrypted connection' : ''} and read ${result.people} people, ${result.withNumber} with a number, in ${result.millis} ms.` : result.message}
                </span>
              )}
            </div>
          )}
          {result?.ok && (result.sample?.length ?? 0) > 0 && (
            <table className="w-full text-sm" aria-label="People with a number">
              <thead><tr className="text-left text-slate-400"><th className="py-1">Sign-in name</th><th>Name</th><th>Number in the directory</th><th>Extension</th></tr></thead>
              <tbody>{result.sample!.map((p) => <tr key={p.login} className="border-t border-slate-800"><td className="py-1 font-mono text-sky-300">{p.login}</td><td>{p.name}</td><td className="text-slate-400">{p.raw}</td><td className="font-mono text-emerald-300">{p.number}</td></tr>)}</tbody>
            </table>
          )}
          <div className="space-y-3 rounded-lg border border-slate-800 p-3">
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value.syncExtensions} onChange={(v) => onChange({ syncExtensions: v })} label="Make extensions from the directory" />
              <span>Make and update extensions for people with a number <span className="text-slate-500">(an extension you made by hand is never changed, unless it has the same person's name or email)</span></span></label>
            {value.syncExtensions && (
              <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value.disableMissing} onChange={(v) => onChange({ disableMissing: v })} label="Turn off the extensions of people who left" />
                Turn off the extensions of people who left the directory or were disabled (they come back on if the person does)</label>
            )}
            <div>
              <div className="mb-1 flex items-center justify-between">
                <span className="text-sm text-slate-300">Ring groups filled from directory groups</span>
                {admin && <button type="button" className="btn" onClick={() => onChange({ ringGroups: [...maps, { group: '', ringGroup: groups?.items[0]?.number ?? '' }] })}><Plus size={14} aria-hidden /> Add</button>}
              </div>
              {maps.length === 0 ? <p className="text-sm text-slate-500">None.</p> : (
                <ul className="space-y-2">
                  {maps.map((m, i) => (
                    <li key={i} className="grid items-start gap-2 md:grid-cols-[2fr_1fr_auto]">
                      <Field label="Directory group" htmlFor={`ldg${i}`} error={error(`system.ldap.ringGroups[${i}].group`)} hint="Its name (cn) or DN. Nested groups count on Active Directory.">
                        <input id={`ldg${i}`} className="input font-mono" value={m.group} onChange={(e) => setMap(i, { group: e.target.value })} />
                      </Field>
                      <Field label="Ring group" htmlFor={`ldr${i}`} error={error(`system.ldap.ringGroups[${i}].ringGroup`)}>
                        <select id={`ldr${i}`} className="input" value={m.ringGroup} onChange={(e) => setMap(i, { ringGroup: e.target.value })}>
                          <option value="">Choose…</option>
                          {groups?.items.map((g) => <option key={g.id} value={g.number}>{g.number} {g.name}</option>)}
                        </select>
                      </Field>
                      {admin && <button type="button" className="btn mt-6 text-rose-300" aria-label={`Remove the ring group of ${m.group || i + 1}`} onClick={() => onChange({ ringGroups: maps.filter((_, j) => j !== i) })}><Trash2 size={14} aria-hidden /></button>}
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div className="flex flex-wrap items-end gap-3">
              <Field label="Read it every (minutes)" htmlFor="ldsm" error={error('system.ldap.syncMinutes')}>
                <input id="ldsm" type="number" min={5} max={1440} className="input w-28" value={value.syncMinutes} onChange={(e) => onChange({ syncMinutes: Number(e.target.value) || 60 })} />
              </Field>
              {admin && <button type="button" className="btn" disabled={syncing} onClick={() => sync().unwrap().then((r) => r.error ? toast.error(r.error) : toast.success(r.changed ? 'Directory synced' : 'Nothing needed changing')).catch((e) => toast.error(errorOf(e).message))}>
                <RefreshCw size={14} aria-hidden /> Sync now
              </button>}
              <span className="text-xs text-slate-500">Save the settings first: a sync reads them as saved.</span>
            </div>
            {status?.last && <Report r={status.last} />}
          </div>
          <div className="space-y-3 rounded-lg border border-slate-800 p-3">
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value.signIn} onChange={(v) => onChange({ signIn: v })} label="Directory sign-in" />
              People sign in here with their directory name and password</label>
            {value.signIn && (
              <div className="grid gap-4 md:grid-cols-2">
                <Field label="Administrator groups" htmlFor="ldag" error={error('system.ldap.adminGroups')} hint="Names (cn) or DNs; press Enter after each.">
                  <ListInput id="ldag" mono value={value.adminGroups ?? []} onChange={(v) => onChange({ adminGroups: v })} invalid={!!error('system.ldap.adminGroups')} />
                </Field>
                <Field label="Viewer groups" htmlFor="ldvg" hint="Empty: everyone else in the directory may sign in as a viewer.">
                  <ListInput id="ldvg" mono value={value.viewerGroups ?? []} onChange={(v) => onChange({ viewerGroups: v })} />
                </Field>
              </div>
            )}
          </div>
        </>
      )}
    </section>
  )
}
