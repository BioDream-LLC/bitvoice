import { Building2, KeyRound, LogIn } from 'lucide-react'
import { useState } from 'react'
import { errorOf, useAuthOptionsQuery, useLoginMutation } from './api'
import { Logo, useTheme } from './Shell'
import { ErrorBox, Field } from './ui'

export function Login() {
  useTheme()
  const [login, { isLoading, error }] = useLoginMutation()
  const [username, setUsername] = useState('')
  const [pw, setPw] = useState('')
  const [code, setCode] = useState('')
  const needCode = !!(error as { data?: { mfa?: boolean } } | undefined)?.data?.mfa
  const { data: opts } = useAuthOptionsQuery()
  // A failed single sign-on comes back as ?sso_error=...; show it once, then tidy the address.
  const [ssoError] = useState(() => {
    const m = new URLSearchParams(location.search).get('sso_error')
    if (m) history.replaceState(null, '', location.pathname + location.hash)
    return m
  })
  const password = opts?.password ?? true
  return (
    <div className="grid min-h-full place-items-center bg-[radial-gradient(ellipse_at_top,var(--color-slate-900),var(--color-slate-950))] p-6">
      <form className="card rise w-full max-w-sm space-y-4 p-8" onSubmit={(e) => { e.preventDefault(); login({ username, password: pw, code }) }}>
        <div className="flex flex-col items-center gap-2 pb-2">
          <Logo size={48} />
          <h1 className="bg-gradient-to-r from-sky-300 to-violet-300 bg-clip-text text-2xl font-bold text-transparent">BitVoice</h1>
          <p className="text-sm text-slate-400">Sign in to manage your phone system</p>
        </div>
        {ssoError && <ErrorBox message={ssoError} />}
        {error && <ErrorBox message={errorOf(error).message} />}
        {opts?.sso.enabled && (
          <>
            <a href="./api/auth/sso/start" className="btn btn-primary w-full justify-center"><Building2 size={16} aria-hidden /> {opts.sso.label}</a>
            {password && <div className="flex items-center gap-3 text-xs text-slate-500" aria-hidden><span className="h-px flex-1 bg-slate-800" />or with a password<span className="h-px flex-1 bg-slate-800" /></div>}
          </>
        )}
        {password && <>
        <Field label="Username" htmlFor="u">
          <input id="u" className="input" autoComplete="username" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />
        </Field>
        <Field label="Password" htmlFor="p">
          <input id="p" type="password" className="input" autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} />
        </Field>
        {needCode && (
          <Field label="Code from your authenticator app" htmlFor="c">
            <input id="c" className="input text-center font-mono text-lg tracking-[0.4em]" inputMode="numeric" autoComplete="one-time-code" autoFocus maxLength={6}
              value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} />
          </Field>
        )}
        <button className={opts?.sso.enabled ? 'btn w-full justify-center' : 'btn btn-primary w-full justify-center'} disabled={isLoading || !username || !pw}>
          <LogIn size={16} aria-hidden /> Sign in
        </button>
        <p className="flex items-center justify-center gap-1 text-xs text-slate-500"><KeyRound size={12} aria-hidden /> Forgot it? Another administrator can reset it.</p>
        </>}
      </form>
    </div>
  )
}
