import { Copy, ShieldCheck, Upload } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useCertificateQuery, useExtensionsQuery, useInstallCertificateMutation, type Trunk } from './api'
import { useIsAdmin } from './Editor'
import { Field } from './ui'

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
