import { Copy, RefreshCw, Save, ShieldCheck, Upload } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useAcmeCheckMutation, useAcmeReachMutation, useAcmeStatusQuery, useCertificateQuery, useExtensionsQuery, useInstallCertificateMutation, useSaveSettingsMutation, useSettingsQuery, useSystemQuery, type ACME, type ApiErrorBody, type Trunk } from './api'
import { attempt, useIsAdmin } from './Editor'
import { Field, ListInput, problemFor, Toggle } from './ui'

/** TeamsSetup: the SBC name, a certificate check, and the PowerShell that adds this server to Teams. */
export function TeamsSetup({ t, set, p }: { t: Trunk; set: (k: 'sbcName', v: string) => void; p: (f: string) => string | undefined }) {
  const { data: cert } = useCertificateQuery()
  const { data: exts } = useExtensionsQuery()
  const fqdn = t.sbcName || 'sbc.example.com'
  const extLen = Math.max(2, ...(exts?.items ?? []).map((x) => x.number.length).filter((n) => n < 8), 3)
  const covers = cert && t.sbcName && cert.names.some((n) => n.toLowerCase() === t.sbcName!.toLowerCase() || (n.startsWith('*.') && t.sbcName!.toLowerCase().endsWith(n.slice(1).toLowerCase()) && t.sbcName!.split('.').length === n.split('.').length))
  const ps = [
    '# In PowerShell, as a Teams administrator (Install-Module MicrosoftTeams first)',
    'Connect-MicrosoftTeams',
    `New-CsOnlinePSTNGateway -Fqdn ${fqdn} -SipSignalingPort 5061 -MaxConcurrentSessions 100 -Enabled $true`,
    'Set-CsOnlinePstnUsage -Identity Global -Usage @{Add="BitVoice"}',
    `New-CsOnlineVoiceRoute -Identity "BitVoice" -NumberPattern ".*" -OnlinePstnGatewayList ${fqdn} -Priority 1 -OnlinePstnUsages "BitVoice"`,
    'New-CsOnlineVoiceRoutingPolicy "BitVoice" -OnlinePstnUsages "BitVoice"',
    `# So Teams users can dial ${extLen}-digit extensions as they are`,
    `New-CsTenantDialPlan -Identity "BitVoice" -NormalizationRules @{Add=(New-CsVoiceNormalizationRule -Parent Global -Name "Extensions" -Pattern '^(\\d{${extLen}})$' -Translation '$1' -InMemory)}`,
    '# For each Teams user who should call through BitVoice:',
    'Grant-CsOnlineVoiceRoutingPolicy -Identity user@example.com -PolicyName "BitVoice"',
    'Grant-CsTenantDialPlan -Identity user@example.com -PolicyName "BitVoice"',
    'Set-CsPhoneNumberAssignment -Identity user@example.com -PhoneNumber +12125550142 -PhoneNumberType DirectRouting',
  ].join('\n')
  return (
    <div className="space-y-3 rounded-lg border border-violet-900 bg-violet-950/20 p-3">
      <Field label="This server's public name (SBC)" htmlFor="tsbc" error={p('sbcName')}
        hint="A name in a domain your Microsoft 365 tenant has verified, pointing at this server, with port 5061 open both ways to 52.112.0.0/14 and 52.120.0.0/14.">
        <input id="tsbc" className="input font-mono" placeholder="sbc.example.com" value={t.sbcName ?? ''} onChange={(e) => set('sbcName', e.target.value.trim())} />
      </Field>
      {cert && t.sbcName && (
        <p className={`text-sm ${cert.selfSigned || !covers ? 'text-rose-300' : 'text-emerald-300'}`}>
          {cert.selfSigned ? 'The certificate is self-signed: Microsoft will refuse it. Install one from a public authority in Settings, Certificate.'
            : !covers ? `The certificate (${cert.names.join(', ')}) does not cover ${t.sbcName}. Install one that does in Settings, Certificate.`
            : `The certificate covers ${t.sbcName} and ends ${new Date(cert.notAfter).toLocaleDateString()}.`}
        </p>
      )}
      <div>
        <div className="mb-1 flex items-center justify-between">
          <span className="text-xs font-medium uppercase tracking-wide text-slate-400">Add this server to Teams</span>
          <button type="button" className="btn text-xs" onClick={() => navigator.clipboard.writeText(ps).then(() => toast.success('Copied'))}><Copy size={12} aria-hidden /> Copy</button>
        </div>
        <pre className="max-h-64 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-300" aria-label="PowerShell for Teams">{ps}</pre>
        <p className="mt-1 text-xs text-slate-400">Teams users then reach BitVoice extensions and phone lines; to ring Teams users from BitVoice, add an outbound route for their numbers through this trunk. Calls go to sip, sip2 and sip3.pstnhub.microsoft.com in turn.</p>
      </div>
    </div>
  )
}

/** CertificateSettings: the certificate used for the web interface, SIP over TLS and Teams. */
export function CertificateSettings() {
  const admin = useIsAdmin()
  const { data } = useCertificateQuery()
  const [install, { isLoading }] = useInstallCertificateMutation()
  const [cert, setCert] = useState('')
  const [key, setKey] = useState('')
  if (!data) return null
  const ends = new Date(data.notAfter)
  const soon = ends.getTime() - Date.now() < 21 * 86400e3
  const doInstall = async () => {
    try { const i = await install({ cert, key }).unwrap(); setCert(''); setKey(''); toast.success(`Installed: ${i.names.join(', ')}`) }
    catch (e) { toast.error(errorOf(e).message) }
  }
  return (
    <section className="card space-y-3 p-5" aria-labelledby="cert-h">
      <h2 id="cert-h" className="flex items-center gap-2 font-semibold text-slate-100"><ShieldCheck size={17} className="text-emerald-300" aria-hidden /> Certificate</h2>
      <p className="text-sm text-slate-400">Used for this web interface, phones on SIP over TLS, and connections out such as Microsoft Teams.</p>
      <dl className="grid gap-x-4 gap-y-1 text-sm sm:grid-cols-[8rem_1fr]">
        <dt className="text-slate-500">For</dt><dd className="font-mono text-slate-200">{data.names.join(', ') || data.subject}</dd>
        <dt className="text-slate-500">From</dt><dd className={data.selfSigned ? 'text-amber-300' : 'text-slate-200'}>{data.selfSigned ? 'Made by BitVoice (self-signed): fine on your own network, not for Teams' : data.issuer}</dd>
        <dt className="text-slate-500">Ends</dt><dd className={soon ? 'text-rose-300' : 'text-slate-200'}>{ends.toLocaleDateString()}</dd>
      </dl>
      <LetsEncrypt />
      {admin && (
        <details className="space-y-2">
          <summary className="cursor-pointer text-sm text-sky-300">Install a certificate from an authority</summary>
          <Field label="Certificate, with its chain (PEM)" htmlFor="certpem"><textarea id="certpem" className="input h-28 font-mono text-xs" placeholder="-----BEGIN CERTIFICATE-----" value={cert} onChange={(e) => setCert(e.target.value)} /></Field>
          <Field label="Private key (PEM, no password)" htmlFor="keypem"><textarea id="keypem" className="input h-24 font-mono text-xs" placeholder="-----BEGIN PRIVATE KEY-----" value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off" /></Field>
          <button type="button" className="btn btn-primary" disabled={!cert || !key || isLoading} onClick={doInstall}><Upload size={14} aria-hidden /> Install</button>
        </details>
      )}
    </section>
  )
}

const acmeDefaults: ACME = { enabled: false, domains: [], email: '', directory: '', challenge: '', httpPort: 0 }

/** Let's Encrypt: BitVoice gets the certificate for its public name and renews it. */
function LetsEncrypt() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const { data: sys } = useSystemQuery()
  const { data: st } = useAcmeStatusQuery(undefined, { pollingInterval: 5000 })
  const [save, { isLoading }] = useSaveSettingsMutation()
  const [check] = useAcmeCheckMutation()
  const [reach, { isLoading: reaching }] = useAcmeReachMutation()
  const [reachOut, setReachOut] = useState<{ name: string; ok: boolean; detail: string }[] | null>(null)
  const [edit, setEdit] = useState<ACME | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!data) return null
  const a: ACME = edit ?? { ...acmeDefaults, ...data.system.acme, domains: data.system.acme?.domains ?? [] }
  const set = (v: Partial<ACME>) => setEdit({ ...a, ...v })
  const p = (f: string) => problemFor(err?.problems, 'system.acme.' + f)
  const port = sys?.httpsPort || 8443
  const kind = a.challenge || (port === 443 ? 'tls-alpn-01' : 'http-01')
  const submit = async () => {
    const e = await attempt(() => save({ ...data, system: { ...data.system, acme: a } }).unwrap(), "Let's Encrypt settings saved")
    setErr(e)
    if (!e) setEdit(null)
  }
  const colour = st?.state === 'ok' ? 'text-emerald-300' : st?.state === 'failed' ? 'text-rose-300' : st?.state === 'working' ? 'text-sky-300' : 'text-slate-400'
  const said = st?.state === 'ok' ? `Issued; renewed from ${st.nextTry ? new Date(st.nextTry).toLocaleDateString() : 'about 30 days before it ends'}.`
    : st?.state === 'failed' ? `Failed: ${st.detail}. Tried again ${st.nextTry ? new Date(st.nextTry).toLocaleString() : 'later'}.`
    : st?.state === 'working' ? `Asking the authority (${st.challenge})…` : 'Off.'
  return (
    <div className="space-y-3 rounded-lg border border-slate-800 p-4">
      <label className="flex items-center gap-3 text-sm text-slate-300">
        <Toggle checked={a.enabled} disabled={!admin} onChange={(v) => set({ enabled: v })} label="Let's Encrypt" />
        Get the certificate from Let's Encrypt and renew it <span className="text-xs text-slate-500">(free; the names must reach this server from the internet)</span>
      </label>
      {a.enabled && (
        <fieldset disabled={!admin} className="space-y-3">
          <p className={`text-sm ${colour}`}>{said}</p>
          {st?.listening && <p className="text-xs text-slate-500">Answering http-01 on port {st.listening}.</p>}
          {st?.portProblem && <p className="text-xs text-amber-300">{st.portProblem}.</p>}
          <Field label="Names" htmlFor="acd" error={p('domains')} hint="The names this server is reached by; press Enter after each. They must point here in public DNS.">
            <ListInput id="acd" mono value={a.domains} onChange={(v) => set({ domains: v })} placeholder="pbx.example.com" />
          </Field>
          <Field label="Email" htmlFor="ace" error={p('email')} hint="Optional: the authority writes here about problems.">
            <input id="ace" className="input max-w-sm" type="email" value={a.email} onChange={(e) => set({ email: e.target.value })} placeholder="admin@example.com" />
          </Field>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="Proof" htmlFor="acc" error={p('challenge')} hint={kind === 'http-01' ? `Let's Encrypt asks for a file on port 80 of each name.` : `Let's Encrypt connects to port 443 of each name.`}>
              <select id="acc" className="input" value={a.challenge} onChange={(e) => set({ challenge: e.target.value as ACME['challenge'] })}>
                <option value="">Choose for me ({port === 443 ? 'tls-alpn-01' : 'http-01'})</option>
                <option value="http-01">http-01 (port 80)</option>
                <option value="tls-alpn-01">tls-alpn-01 (port 443)</option>
              </select>
            </Field>
            {kind === 'http-01' && (
              <Field label="Port 80 here" htmlFor="acp" error={p('httpPort')} hint="Where BitVoice answers it; or forward port 80 to the web port.">
                <input id="acp" type="number" className="input w-28" value={a.httpPort || ''} placeholder="80" onChange={(e) => set({ httpPort: Number(e.target.value) })} />
              </Field>
            )}
            <Field label="Authority" htmlFor="acdir" error={p('directory')}>
              <select id="acdir" className="input" value={a.directory === '' || a.directory === 'staging' ? a.directory : 'other'} onChange={(e) => set({ directory: e.target.value === 'other' ? 'https://' : e.target.value })}>
                <option value="">Let's Encrypt</option>
                <option value="staging">Let's Encrypt staging (test certificates)</option>
                <option value="other">Another ACME authority</option>
              </select>
            </Field>
          </div>
          {a.directory !== '' && a.directory !== 'staging' && (
            <Field label="Directory address" htmlFor="acdu" error={p('directory')}><input id="acdu" className="input font-mono" value={a.directory} onChange={(e) => set({ directory: e.target.value.trim() })} /></Field>
          )}
        </fieldset>
      )}
      {admin && (
        <div className="flex gap-2">
          <button type="button" className="btn btn-primary" disabled={isLoading || !edit} onClick={submit}><Save size={14} aria-hidden /> Apply Let's Encrypt settings</button>
          {a.enabled && !edit && <button type="button" className="btn" onClick={() => void attempt(() => check().unwrap(), 'Checking now')}><RefreshCw size={14} aria-hidden /> Check now</button>}
          {a.enabled && kind === 'http-01' && a.domains.length > 0 && (
            <button type="button" className="btn" disabled={reaching} onClick={async () => { try { setReachOut((await reach(a.domains).unwrap()).names) } catch { toast.error('The check could not run') } }}>
              <ShieldCheck size={14} aria-hidden /> {reaching ? 'Checking…' : 'Test the names'}
            </button>
          )}
        </div>
      )}
      {reachOut && (
        <ul className="space-y-1 text-xs" aria-label="Name test results">
          {reachOut.map((r) => <li key={r.name} className={r.ok ? 'text-emerald-300' : 'text-amber-300'}><span className="font-mono">{r.name}</span>: {r.detail}</li>)}
        </ul>
      )}
    </div>
  )
}
