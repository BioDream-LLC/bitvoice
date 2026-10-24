import { AppWindow, Cable, KeyRound, Plus, Trash2 } from 'lucide-react'
import { useSystemQuery, type Settings } from './api'
import { AGISettings } from './AGI'
import { DestinationPicker } from './Extensions'
import { Field, Toggle } from './ui'

type Compat = Settings['system']['compat']

/** A random secret of 24 letters and digits, from the browser's cryptographic generator. */
function newSecret() {
  const abc = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const b = crypto.getRandomValues(new Uint8Array(24))
  return Array.from(b, (x) => abc[x % abc.length]).join('')
}

/** Settings for AMI and ARI, Asterisk's control interfaces, so tools built for Asterisk keep working. */
export function CompatSettings({ value, onChange, error, admin }: {
  value: Compat; onChange: (v: Partial<Compat>) => void; error: (f: string) => string | undefined; admin: boolean
}) {
  const { data: sys } = useSystemQuery()
  const users = value.users ?? []
  const setUser = (i: number, v: Partial<Compat['users'][number]>) => onChange({ users: users.map((u, j) => (j === i ? { ...u, ...v } : u)) })
  const port = (value.amiListen || '127.0.0.1:5038').split(':').pop()
  const apps = value.apps ?? []
  const setApp = (i: number, v: Partial<NonNullable<Compat['apps']>[number]>) => onChange({ apps: apps.map((a, j) => (j === i ? { ...a, ...v } : a)) })
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="compat-h">
      <h2 id="compat-h" className="flex items-center gap-2 font-semibold text-slate-100"><Cable size={17} className="text-orange-300" aria-hidden /> Asterisk compatibility</h2>
      <p className="text-sm text-slate-400">
        Wallboards, CRM click-to-dial and monitoring tools written for Asterisk can talk to BitVoice. They see calls as channels and can list, place, transfer and hang up calls.
        AMI and ARI are off unless you switch them on; AGI scripts run only on the calls you send to them.
      </p>
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-3">
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value.ami} onChange={(v) => onChange({ ami: v })} label="AMI" /> AMI (the manager interface)</label>
          {value.ami && (
            <Field label="AMI listens on" htmlFor="amil" error={error('system.compat.amiListen')}
              hint={<>127.0.0.1 keeps it to this computer; use 0.0.0.0:{port} for your network.{sys?.amiAddress ? <> Now listening on <span className="font-mono text-emerald-300">{sys.amiAddress}</span>.</> : value.ami ? <span className="text-amber-300"> Not listening yet: save first.</span> : null}</>}>
              <input id="amil" className="input font-mono" placeholder="127.0.0.1:5038" value={value.amiListen} onChange={(e) => onChange({ amiListen: e.target.value.trim() })} />
            </Field>
          )}
        </div>
        <div className="space-y-3">
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value.ari} onChange={(v) => onChange({ ari: v })} label="ARI" /> ARI (the REST interface)</label>
          {value.ari && (
            <p className="text-xs text-slate-500">At <span className="font-mono text-sky-300">{location.origin}/ari/</span> with the same logins (basic authentication or <span className="font-mono">?api_key=user:secret</span>). Events stream from <span className="font-mono text-sky-300">/ari/events</span>.</p>
          )}
        </div>
      </div>
      {(value.ari || apps.length > 0) && (
        <div>
          <div className="mb-2 flex items-center justify-between">
            <h3 className="flex items-center gap-2 text-sm font-medium text-slate-200"><AppWindow size={15} className="text-sky-300" aria-hidden /> ARI applications</h3>
            {admin && <button type="button" className="btn" onClick={() => onChange({ apps: [...apps, { name: '', number: '', fallback: { type: 'hangup', target: '' } }] })}><Plus size={14} aria-hidden /> Add application</button>}
          </div>
          <p className="mb-2 text-xs text-slate-500">
            A call sent to an application (as Asterisk's <span className="font-mono">Stasis()</span>) is answered and handed to the program connected to
            <span className="font-mono text-sky-300"> /ari/events?app=NAME</span>, which runs it: play, record, keys, bridges, hang up or continue. With no program connected, or once the program lets go, the call goes to the fallback.
          </p>
          {apps.length === 0 ? <p className="text-sm text-slate-500">No applications yet.</p> : (
            <ul className="space-y-2">
              {apps.map((a, i) => (
                <li key={i} className="grid items-start gap-3 rounded-lg border border-slate-800 p-3 md:grid-cols-[1fr_0.6fr_1.4fr_auto]">
                  <Field label="Application name" htmlFor={`an${i}`} error={error(`system.compat.apps[${i}].name`)}>
                    <input id={`an${i}`} className="input font-mono" value={a.name} autoComplete="off" onChange={(e) => setApp(i, { name: e.target.value.trim() })} />
                  </Field>
                  <Field label="Number to dial it" htmlFor={`anum${i}`} error={error(`system.compat.apps[${i}].number`)} hint="Optional.">
                    <input id={`anum${i}`} className="input font-mono" inputMode="numeric" value={a.number ?? ''} onChange={(e) => setApp(i, { number: e.target.value.trim() })} />
                  </Field>
                  <DestinationPicker label="Fallback" required value={a.fallback} onChange={(d) => d && setApp(i, { fallback: d })} error={error(`system.compat.apps[${i}].fallback`)} />
                  {admin && <button type="button" className="btn mt-6 text-rose-300" aria-label={`Remove application ${a.name || i + 1}`} onClick={() => onChange({ apps: apps.filter((_, j) => j !== i) })}><Trash2 size={14} aria-hidden /></button>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      <AGISettings value={value} onChange={onChange} error={error} admin={admin} />
      {(value.ami || value.ari || users.length > 0) && (
        <div>
          <div className="mb-2 flex items-center justify-between">
            <h3 className="flex items-center gap-2 text-sm font-medium text-slate-200"><KeyRound size={15} className="text-amber-300" aria-hidden /> Logins</h3>
            {admin && <button type="button" className="btn" onClick={() => onChange({ users: [...users, { username: '', secret: newSecret(), write: false }] })}><Plus size={14} aria-hidden /> Add login</button>}
          </div>
          {users.length === 0 && error('system.compat.users') && <p className="field-error mb-2 text-sm text-rose-300">{error('system.compat.users')}</p>}
          {users.length === 0 ? <p className="text-sm text-slate-500">No logins yet.</p> : (
            <ul className="space-y-2">
              {users.map((u, i) => (
                <li key={i} className="grid items-start gap-3 rounded-lg border border-slate-800 p-3 md:grid-cols-[1fr_1.4fr_auto_auto]">
                  <Field label="Username" htmlFor={`cu${i}`} error={error(`system.compat.users[${i}].username`)}>
                    <input id={`cu${i}`} className="input font-mono" value={u.username} autoComplete="off" onChange={(e) => setUser(i, { username: e.target.value })} />
                  </Field>
                  <Field label="Secret" htmlFor={`cs${i}`} error={error(`system.compat.users[${i}].secret`)} hint={u.secret ? undefined : 'Leave empty to keep the current one.'}>
                    <div className="flex gap-2">
                      <input id={`cs${i}`} className="input font-mono" value={u.secret} autoComplete="off" onChange={(e) => setUser(i, { secret: e.target.value })} />
                      <button type="button" className="btn shrink-0" onClick={() => setUser(i, { secret: newSecret() })}>New</button>
                    </div>
                  </Field>
                  <label className="flex items-center gap-2 pt-7 text-sm text-slate-300">
                    <Toggle checked={u.write} onChange={(v) => setUser(i, { write: v })} label={`Login ${u.username || i + 1} may change calls`} />
                    <span className={u.write ? 'text-amber-300' : 'text-slate-400'}>{u.write ? 'Can change calls' : 'Read only'}</span>
                  </label>
                  {admin && <button type="button" className="btn mt-6 text-rose-300" aria-label={`Remove login ${u.username || i + 1}`} onClick={() => onChange({ users: users.filter((_, j) => j !== i) })}><Trash2 size={14} aria-hidden /></button>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  )
}
