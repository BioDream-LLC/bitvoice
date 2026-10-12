import qrcode from 'qrcode-generator'
import { ShieldCheck, Smartphone } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { api, useMeQuery } from './api'
import { store } from './store'
import { Badge, Field } from './ui'

const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }

async function post(url: string, body?: unknown) {
  const r = await fetch(url, { method: 'POST', headers: H, body: body ? JSON.stringify(body) : undefined })
  const j = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error(j.message ?? 'failed')
  return j
}

function qrSvg(text: string) {
  const q = qrcode(0, 'M')
  q.addData(text)
  q.make()
  return q.createSvgTag({ cellSize: 4, margin: 2, scalable: true })
}

/** Two-step sign-in for the signed-in person: scan a QR code, confirm a code. */
export function TwoStep() {
  const { data: me } = useMeQuery()
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null)
  const [code, setCode] = useState('')
  const [pw, setPw] = useState('')
  const refresh = () => store.dispatch(api.util.invalidateTags(['Me']))
  return (
    <section className="card mt-8 max-w-xl space-y-4 p-5">
      <h2 className="flex items-center gap-2 font-semibold text-slate-100"><ShieldCheck size={17} className="text-emerald-300" aria-hidden /> Two-step sign-in
        <span className="ml-auto">{me?.mfa ? <Badge status="ok">On</Badge> : <Badge status="warn">Off</Badge>}</span></h2>
      {me?.mfa ? (
        <form className="flex items-end gap-2" onSubmit={async (e) => { e.preventDefault(); try { await post('./api/auth/mfa/off', { password: pw }); toast.success('Two-step sign-in is off'); setPw(''); refresh() } catch (err) { toast.error((err as Error).message) } }}>
          <Field label="Your password, to turn it off" htmlFor="mo"><input id="mo" type="password" className="input" autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} /></Field>
          <button className="btn" disabled={!pw}>Turn off</button>
        </form>
      ) : setup ? (
        <form className="grid gap-4 sm:grid-cols-[10rem_1fr]" onSubmit={async (e) => { e.preventDefault(); try { await post('./api/auth/mfa/confirm', { code }); toast.success('Two-step sign-in is on'); setSetup(null); setCode(''); refresh() } catch (err) { toast.error((err as Error).message) } }}>
          <div className="rounded-lg bg-white p-1" role="img" aria-label="QR code for your authenticator app" dangerouslySetInnerHTML={{ __html: qrSvg(setup.uri) }} />
          <div className="space-y-3">
            <p className="text-sm text-slate-400">Scan the code with an authenticator app (Microsoft Authenticator, Google Authenticator, 1Password…), or type this key: <span className="break-all font-mono text-xs text-amber-300">{setup.secret}</span></p>
            <Field label="Then enter the code it shows" htmlFor="mc"><input id="mc" className="input font-mono tracking-widest" inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} /></Field>
            <button className="btn btn-primary" disabled={code.length !== 6}>Turn on</button>
          </div>
        </form>
      ) : (
        <div className="space-y-3">
          <p className="text-sm text-slate-400">Ask for a code from your phone as well as your password when you sign in.</p>
          <button className="btn" onClick={async () => { try { setSetup(await post('./api/auth/mfa/start')) } catch (err) { toast.error((err as Error).message) } }}><Smartphone size={15} className="text-emerald-300" aria-hidden /> Set up</button>
        </div>
      )}
    </section>
  )
}
