import { CalendarClock, CloudCog, RefreshCw, Upload } from 'lucide-react'
import { useRef, useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useCheckWorkspaceMutation, useSyncWorkspaceMutation, useWorkspaceStatusQuery, type Workspace, type WorkspaceCheck } from './api'
import { useIsAdmin } from './Editor'
import { Badge, Field, relTime, Toggle } from './ui'

const blank: Workspace = { provider: '', tenantId: '', clientId: '', clientSecret: '', serviceAccountKey: '', adminEmail: '', contacts: true, calendars: true }
export const providerName = (p?: string) => (p === 'microsoft365' ? 'Microsoft 365' : p === 'google' ? 'Google Workspace' : p ?? '')

/** Settings: Microsoft 365 or Google Workspace, read for the directory and for who is away. */
export function WorkspaceSettings({ value, onChange, error }: { value: Workspace; onChange: (v: Partial<Workspace>) => void; error: (f: string) => string | undefined }) {
  const v = { ...blank, ...(value ?? {}) }
  const admin = useIsAdmin()
  const { data: st } = useWorkspaceStatusQuery(undefined, { pollingInterval: 60_000, skip: !v.provider })
  // Until the first sync after saving, look again sooner: it runs within half a minute.
  const waiting = !!st && st.provider === v.provider && v.contacts && !st.synced && !st.error
  useWorkspaceStatusQuery(undefined, { pollingInterval: 5_000, skip: !waiting })
  const [check, { isLoading: checking }] = useCheckWorkspaceMutation()
  const [sync, { isLoading: syncing }] = useSyncWorkspaceMutation()
  const [found, setFound] = useState<WorkspaceCheck | null>(null)
  const file = useRef<HTMLInputElement>(null)
  const name = providerName(v.provider)
  const tryIt = async () => {
    try { setFound(await check(v).unwrap()) } catch (e) { setFound(null); toast.error(errorOf(e).message) }
  }
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="ws-h">
      <h2 id="ws-h" className="flex items-center gap-2 font-semibold text-slate-100"><CloudCog size={17} className="text-sky-300" aria-hidden /> Microsoft 365 and Google Workspace</h2>
      <p className="text-sm text-slate-400">
        Read your organisation&apos;s people and shared contacts into <a className="text-sky-300 underline" href="#/contacts">Contacts</a> (desk phones, the browser phone and caller names use them),
        and each person&apos;s calendar for who is away: an extension whose person (by its email) is in a meeting or out of office does not ring. BitVoice only reads; nothing is changed there.
      </p>
      <Field label="Read from" htmlFor="wsp" error={error('system.workspace.provider')}>
        <select id="wsp" className="input max-w-xs" value={v.provider} onChange={(e) => {
          setFound(null)
          const provider = e.target.value as Workspace['provider']
          // Choosing a service for the first time reads both, as most want; either can be switched off.
          onChange(!v.provider && provider && !v.contacts && !v.calendars ? { provider, contacts: true, calendars: true } : { provider })
        }}>
          <option value="">Nothing</option><option value="microsoft365">Microsoft 365</option><option value="google">Google Workspace</option>
        </select>
      </Field>
      {v.provider === 'microsoft365' && (
        <>
          <ol className="list-decimal space-y-1 pl-5 text-xs text-slate-400">
            <li>In the Entra admin centre, <span className="text-slate-200">App registrations, New registration</span>: name it BitVoice, single tenant, no redirect address.</li>
            <li><span className="text-slate-200">API permissions, Add a permission, Microsoft Graph, Application permissions</span>: <span className="font-mono text-sky-300">User.Read.All</span>, <span className="font-mono text-sky-300">OrgContact.Read.All</span> and <span className="font-mono text-sky-300">Calendars.ReadBasic</span>; then <span className="text-slate-200">Grant admin consent</span>.</li>
            <li><span className="text-slate-200">Certificates &amp; secrets, New client secret</span>: copy its value below (it is shown once). Note when it expires: BitVoice says when it no longer works.</li>
          </ol>
          <div className="grid gap-3 md:grid-cols-3">
            <Field label="Directory (tenant) ID" htmlFor="wst" error={error('system.workspace.tenantId')} hint="On the app's Overview, or your domain such as contoso.onmicrosoft.com.">
              <input id="wst" className="input font-mono" value={v.tenantId} onChange={(e) => onChange({ tenantId: e.target.value.trim() })} />
            </Field>
            <Field label="Application (client) ID" htmlFor="wsc" error={error('system.workspace.clientId')}>
              <input id="wsc" className="input font-mono" value={v.clientId} onChange={(e) => onChange({ clientId: e.target.value.trim() })} />
            </Field>
            <Field label="Client secret" htmlFor="wss" error={error('system.workspace.clientSecret')} hint="Leave empty to keep the current one.">
              <input id="wss" type="password" autoComplete="off" className="input font-mono" value={v.clientSecret} onChange={(e) => onChange({ clientSecret: e.target.value })} />
            </Field>
          </div>
        </>
      )}
      {v.provider === 'google' && (
        <>
          <ol className="list-decimal space-y-1 pl-5 text-xs text-slate-400">
            <li>In Google Cloud, in a project of yours: switch on the <span className="text-slate-200">People API</span> and the <span className="text-slate-200">Google Calendar API</span>; make a <span className="text-slate-200">service account</span> and a <span className="text-slate-200">key</span> for it (JSON).</li>
            <li>In the Google Admin console, <span className="text-slate-200">Security, Access and data control, API controls, Domain-wide delegation, Add new</span>: the service account&apos;s client ID, with the scopes
              <span className="break-all font-mono text-sky-300"> https://www.googleapis.com/auth/directory.readonly,https://www.googleapis.com/auth/calendar.freebusy</span>.</li>
            <li>Give the key file below, and an administrator&apos;s email for the account to act as.</li>
          </ol>
          <div className="grid gap-3 md:grid-cols-2">
            <Field label="Service account key (JSON)" htmlFor="wsk" error={error('system.workspace.serviceAccountKey')} hint="Leave empty to keep the current one.">
              <div className="space-y-2">
                <textarea id="wsk" className="input h-24 font-mono text-xs" autoComplete="off" spellCheck={false} placeholder='{"type": "service_account", …}' value={v.serviceAccountKey} onChange={(e) => onChange({ serviceAccountKey: e.target.value })} />
                <input ref={file} type="file" accept=".json,application/json" className="hidden" aria-label="Key file" onChange={async (e) => { const f = e.target.files?.[0]; if (f) onChange({ serviceAccountKey: await f.text() }); e.target.value = '' }} />
                <button type="button" className="btn text-xs" onClick={() => file.current?.click()}><Upload size={13} aria-hidden /> Choose the key file</button>
              </div>
            </Field>
            <Field label="Administrator it acts as" htmlFor="wsa" error={error('system.workspace.adminEmail')} hint="A Workspace administrator's email.">
              <input id="wsa" type="email" className="input" value={v.adminEmail} onChange={(e) => onChange({ adminEmail: e.target.value.trim() })} />
            </Field>
          </div>
          <p className="text-xs text-slate-500">Google gives busy times without saying why, so an extension is in a meeting (never on time off) while its person is busy.</p>
        </>
      )}
      {v.provider && (
        <>
          <div className="flex flex-wrap gap-6">
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={v.contacts} onChange={(x) => onChange({ contacts: x })} label="People and shared contacts into Contacts" /> People and shared contacts into Contacts</label>
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={v.calendars} onChange={(x) => onChange({ calendars: x })} label="Calendars say who is away" /> Calendars say who is away</label>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {admin && <button type="button" className="btn" disabled={checking} onClick={tryIt}>Check now</button>}
            {admin && st?.provider === v.provider && v.contacts && <button type="button" className="btn" disabled={syncing} onClick={() => sync().unwrap().then((r) => r.error ? toast.error(r.error) : toast.success(`Contacts in step with ${name}`)).catch((e) => toast.error(errorOf(e).message))}><RefreshCw size={13} aria-hidden /> Sync contacts now</button>}
          </div>
          {found && (
            <div role="status" className="space-y-1 rounded-lg border border-emerald-900 bg-emerald-950/30 p-3 text-sm">
              <p className="text-emerald-200">Signed in to {name}: {found.people} people and {found.shared} shared contacts; {found.withNumbers} have a phone number and go into Contacts.</p>
              <p className="text-slate-300">{found.matched.length} extension{found.matched.length === 1 ? '' : 's'} match a person by email{found.matched.length ? ` (${found.matched.join(', ')})` : ''}.
                {found.unmatched.length > 0 && <span className="text-amber-300"> {found.unmatched.length} have an email {name} does not know: {found.unmatched.join(', ')}.</span>}</p>
              {found.calendarError ? <p className="text-amber-300">Calendars: {found.calendarError}</p> : found.busyTimes !== undefined && <p className="text-slate-300">Calendars read: {found.busyTimes} busy times in the next day.</p>}
            </div>
          )}
          {st && st.provider === v.provider && (
            <div className="flex flex-wrap items-center gap-3 text-xs text-slate-400" aria-label="How the link last went">
              {v.contacts && (st.error ? <Badge status="fail">Contacts: {st.error}</Badge> : st.synced ? <span><Badge status="ok">Contacts in step</Badge> {st.total} contacts, {relTime(st.synced)} ({st.added} added, {st.updated} changed, {st.removed} removed)</span> : <Badge status="idle">Contacts not read yet (save first)</Badge>)}
              {v.calendars && (st.calendarError ? <Badge status="fail">Calendars: {st.calendarError}</Badge> : st.calendarsRead ? <span className="inline-flex items-center gap-1"><CalendarClock size={12} className="text-violet-300" aria-hidden /> {st.matched} extensions&apos; calendars read {relTime(st.calendarsRead)}</span> : null)}
            </div>
          )}
        </>
      )}
    </section>
  )
}
