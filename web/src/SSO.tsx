import { Building2, Copy } from 'lucide-react'
import { type Settings } from './api'
import { toast } from 'sonner'
import { copyText } from './Setup'
import { Field, ListInput, Toggle } from './ui'

type SSO = Settings['system']['sso']

/** Ready-made addresses for the common providers, so nobody has to look them up. */
const presets: { name: string; issuer: string; claim: string; label: string }[] = [
  { name: 'Microsoft Entra ID', issuer: 'https://login.microsoftonline.com/TENANT-ID/v2.0', claim: 'groups', label: 'Sign in with Microsoft' },
  { name: 'Google Workspace', issuer: 'https://accounts.google.com', claim: 'groups', label: 'Sign in with Google' },
  { name: 'Okta', issuer: 'https://YOUR-ORG.okta.com', claim: 'groups', label: 'Sign in with Okta' },
  { name: 'Keycloak', issuer: 'https://KEYCLOAK-HOST/realms/REALM', claim: 'groups', label: 'Sign in with Keycloak' },
]

/** Single sign-on settings: an OpenID Connect provider signs people in, its groups decide their role. */
export function SSOSettings({ value, onChange, error }: { value: SSO; onChange: (v: Partial<SSO>) => void; error: (f: string) => string | undefined }) {
  const redirect = value.redirectUrl || `${location.origin}/api/auth/sso/callback`
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="sso-h">
      <h2 id="sso-h" className="flex items-center gap-2 font-semibold text-slate-100"><Building2 size={17} className="text-sky-300" aria-hidden /> Single sign-on</h2>
      <p className="text-sm text-slate-400">
        People sign in with their work account through your identity provider (any OpenID Connect provider). Their groups there decide whether they are an
        <span className="text-amber-300"> administrator</span> or a <span className="text-sky-300">viewer</span>; the account here is made the first time they sign in.
      </p>
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value.enabled} onChange={(v) => onChange({ enabled: v })} label="Use single sign-on" /> Use single sign-on</label>
      {value.enabled && (
        <>
          <div className="flex flex-wrap gap-2" role="group" aria-label="Fill in for a provider">
            {presets.map((p) => (
              <button key={p.name} type="button" className="btn text-xs" onClick={() => onChange({ issuer: p.issuer, groupsClaim: p.claim, label: p.label })}>{p.name}</button>
            ))}
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Issuer" htmlFor="ssoi" error={error('system.sso.issuer')} hint="The provider's address; replace any capitals placeholder.">
              <input id="ssoi" className="input font-mono" value={value.issuer} onChange={(e) => onChange({ issuer: e.target.value.trim() })} />
            </Field>
            <Field label="Button text" htmlFor="ssol"><input id="ssol" className="input" value={value.label} onChange={(e) => onChange({ label: e.target.value })} /></Field>
            <Field label="Client ID" htmlFor="ssoc" error={error('system.sso.clientId')}>
              <input id="ssoc" className="input font-mono" value={value.clientId} onChange={(e) => onChange({ clientId: e.target.value.trim() })} />
            </Field>
            <Field label="Client secret" htmlFor="ssos" hint="Leave empty to keep the current one, or for providers that need none.">
              <input id="ssos" type="password" autoComplete="off" className="input font-mono" value={value.clientSecret} onChange={(e) => onChange({ clientSecret: e.target.value })} />
            </Field>
          </div>
          <Field label="Redirect address" htmlFor="ssor" error={error('system.sso.redirectUrl')} hint="Register this with the provider. Change it only if people reach BitVoice through another address.">
            <div className="flex gap-2">
              <input id="ssor" className="input font-mono" placeholder={`${location.origin}/api/auth/sso/callback`} value={value.redirectUrl} onChange={(e) => onChange({ redirectUrl: e.target.value.trim() })} />
              <button type="button" className="btn shrink-0" onClick={async () => { if (await copyText(redirect)) toast.success('Redirect address copied'); else toast.error('Copying is blocked here; select the text instead') }}><Copy size={14} aria-hidden /> Copy</button>
            </div>
          </Field>
          <div className="grid gap-4 md:grid-cols-3">
            <Field label="Groups claim" htmlFor="ssog" hint="The token field listing groups.">
              <input id="ssog" className="input font-mono" value={value.groupsClaim} onChange={(e) => onChange({ groupsClaim: e.target.value.trim() })} />
            </Field>
            <Field label="Administrator groups" htmlFor="ssoa" error={error('system.sso.adminGroups')} hint="Press Enter after each.">
              <ListInput id="ssoa" mono value={value.adminGroups ?? []} onChange={(v) => onChange({ adminGroups: v })} invalid={!!error('system.sso.adminGroups')} />
            </Field>
            <Field label="Viewer groups" htmlFor="ssov" hint="Empty: everyone else the provider signs in is a viewer.">
              <ListInput id="ssov" mono value={value.viewerGroups ?? []} onChange={(v) => onChange({ viewerGroups: v })} />
            </Field>
          </div>
          <label className="flex items-center gap-3 text-sm text-slate-300">
            <Toggle checked={value.passwordSignIn} onChange={(v) => onChange({ passwordSignIn: v })} label="Also allow passwords" />
            <span>Also allow passwords <span className="text-slate-500">(off: passwords work only on this computer, so a provider outage never locks you out)</span></span>
          </label>
        </>
      )}
    </section>
  )
}
