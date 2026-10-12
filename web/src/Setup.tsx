// The first-run wizard: from a double-click to a phone that rings, in a few guided steps.
import { toast } from 'sonner'
import { countryCodes, countryName, emergencyFor, guessCountry } from './countries'
import { clsx } from 'clsx'
import {
  ArrowLeft, ArrowRight, Building2, Check, Copy, Globe, KeyRound, Phone, PhoneCall, Plus, Rocket, Server,
  ShieldCheck, Trash2, Users,
} from 'lucide-react'
import { useState } from 'react'
import { errorOf, setSetupCode, useCompleteSetupMutation, useSetupStatusQuery, type ApiErrorBody } from './api'
import { Logo, useTheme } from './Shell'
import { ErrorBox, Field, Spinner, Toggle, problemFor } from './ui'

interface Person { number: string; name: string; email: string; location: string }

const steps = [
  { id: 'org', label: 'Organisation', icon: Building2, color: 'text-sky-300' },
  { id: 'admin', label: 'Administrator', icon: ShieldCheck, color: 'text-fuchsia-300' },
  { id: 'people', label: 'People', icon: Users, color: 'text-violet-300' },
  { id: 'line', label: 'Phone line', icon: Server, color: 'text-teal-300' },
  { id: 'review', label: 'Finish', icon: Rocket, color: 'text-emerald-300' },
] as const

export function Setup() {
  useTheme()
  const { data: status, refetch } = useSetupStatusQuery()
  const [complete, { isLoading, error }] = useCompleteSetupMutation()
  const [step, setStep] = useState(0)
  const [code, setCode] = useState('')

  const [org, setOrg] = useState('')
  const [country, setCountry] = useState(guessCountry)
  const [purpose, setPurpose] = useState<'office' | 'contact-centre'>('office')
  const [admin, setAdmin] = useState({ username: 'admin', name: '', password: '', confirm: '' })
  const [people, setPeople] = useState<Person[]>([
    { number: '100', name: 'Reception', email: '', location: '' },
    { number: '101', name: '', email: '', location: '' },
  ])
  const [hasLine, setHasLine] = useState(true)
  const [trunk, setTrunk] = useState({ name: '', host: '', port: 5060, username: '', password: '', register: true })
  const [inbound, setInbound] = useState('all')

  if (!status) return <div className="grid h-full place-items-center"><Spinner /></div>

  if (!status.allowed) {
    return (
      <Frame>
        <form className="space-y-4" onSubmit={(e) => { e.preventDefault(); setSetupCode(code); refetch() }}>
          <h2 className="flex items-center gap-2 text-xl font-bold text-slate-50"><KeyRound className="text-amber-300" aria-hidden /> Enter the setup code</h2>
          <p className="text-sm text-slate-400">For safety, setup from another computer needs the six-digit code shown where BitVoice was started.</p>
          <input className="input text-center font-mono text-2xl tracking-[0.4em]" inputMode="numeric" maxLength={6} aria-label="Setup code"
            value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} autoFocus />
          <button className="btn btn-primary w-full justify-center" disabled={code.length !== 6}>Continue</button>
        </form>
      </Frame>
    )
  }

  const err: ApiErrorBody | undefined = error ? errorOf(error) : undefined
  const pwMismatch = admin.confirm !== '' && admin.password !== admin.confirm
  const canNext = [
    org.trim() !== '',
    admin.username.trim() !== '' && !passwordProblem(admin.password) && admin.password === admin.confirm,
    people.length > 0 && people.every((p) => /^\d{2,8}$/.test(p.number) && p.name.trim() !== '')
      && new Set(people.map((p) => p.number)).size === people.length,
    !hasLine || (trunk.host.trim() !== '' && trunk.username.trim() !== ''),
    true,
  ][step]

  const finish = () => complete({
    organisation: org, purpose, country, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    admin: { username: admin.username, name: admin.name, password: admin.password },
    extensions: people,
    trunk: hasLine ? { ...trunk, transport: 'udp', name: trunk.name || 'Phone line' } : null,
    inbound,
  }).unwrap().catch((e) => {
    // Take the person back to the step holding the first field the server refused.
    const f = errorOf(e)?.problems?.[0]?.field ?? ''
    const at = f.startsWith('trunks') || f.startsWith('inbound') ? 3 : f.startsWith('extensions') ? 2
      : f === 'username' || f === 'password' ? 1 : f.startsWith('system') ? 0 : -1
    if (at >= 0) setStep(at)
  })
  const tp = (f: string) => problemFor(err?.problems, 'trunks[0].' + f)
  const xp = (i: number, f: string) => problemFor(err?.problems, `extensions[${i}].${f}`)

  return (
    <Frame wide>
      <ol className="mb-8 flex items-center gap-1" aria-label="Setup steps">
        {steps.map((s, i) => (
          <li key={s.id} className="flex flex-1 items-center gap-2">
            <span className={clsx('grid h-8 w-8 shrink-0 place-items-center rounded-full border text-sm',
              i < step ? 'border-emerald-500 bg-emerald-500/20 text-emerald-300'
                : i === step ? 'border-sky-400 bg-sky-500/20 text-sky-200' : 'border-slate-700 text-slate-500')}
              aria-current={i === step ? 'step' : undefined}>
              {i < step ? <Check size={15} aria-hidden /> : <s.icon size={15} aria-hidden />}
            </span>
            <span className={clsx('hidden text-xs font-semibold sm:block', i === step ? 'text-slate-100' : 'text-slate-500')}>{s.label}</span>
            {i < steps.length - 1 && <span className="h-px flex-1 bg-slate-800" />}
          </li>
        ))}
      </ol>

      {/* One form for every step: Enter moves on, and password managers offer to save the new account. */}
      <form noValidate onSubmit={(e) => {
        e.preventDefault()
        if (step === steps.length - 1) finish()
        else if (canNext) setStep(step + 1)
      }}>
      {step === 0 && (
        <section className="space-y-5">
          <Heading icon={Building2} color="text-sky-300" title="Welcome to BitVoice">
            A complete phone system that runs on this computer. Five short steps and your phones will ring.
          </Heading>
          <Field label="Organisation name" htmlFor="org" hint="Shown on phone screens and in the interface.">
            <input id="org" className="input" autoFocus value={org} onChange={(e) => setOrg(e.target.value)} placeholder="Acme Dental" />
          </Field>
          <Field label="Country" htmlFor="country" hint={`Sets the emergency numbers (${emergencyFor(country).join(', ')}) and the tones callers hear.`}>
            <select id="country" className="input" value={country} onChange={(e) => setCountry(e.target.value)}>
              {countryCodes.map((c) => <option key={c} value={c}>{countryName(c)}</option>)}
            </select>
          </Field>
          <div>
            <span className="label">What will it be used for?</span>
            <div className="grid gap-3 sm:grid-cols-2">
              {([['office', 'Office phones', 'Desk phones, mobiles, reception and voicemail.', Phone, 'text-sky-300'],
                ['contact-centre', 'Contact centre', 'Queues of callers answered by a team.', PhoneCall, 'text-emerald-300']] as const)
                .map(([id, title, text, Icon, c]) => (
                  <button type="button" key={id} onClick={() => setPurpose(id)} aria-pressed={purpose === id}
                    className={clsx('card flex gap-3 p-4 text-left', purpose === id && 'ring-2 ring-sky-400')}>
                    <Icon className={c} aria-hidden />
                    <span><span className="block font-semibold text-slate-100">{title}</span><span className="text-sm text-slate-400">{text}</span></span>
                  </button>
                ))}
            </div>
          </div>
        </section>
      )}

      {step === 1 && (
        <section className="space-y-4">
          <Heading icon={ShieldCheck} color="text-fuchsia-300" title="Your administrator account">
            You will use this to sign in and manage everything. Choose a password nobody could guess.
          </Heading>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Username" htmlFor="au"><input id="au" className="input" value={admin.username} onChange={(e) => { const v = e.target.value; setAdmin((admin0) => ({ ...admin0, username: v })) }} /></Field>
            <Field label="Your name" htmlFor="an"><input id="an" className="input" value={admin.name} onChange={(e) => { const v = e.target.value; setAdmin((admin0) => ({ ...admin0, name: v })) }} /></Field>
            <Field label="Password" htmlFor="ap" hint="At least 10 characters; a short sentence works well."
              error={problemFor(err?.problems, 'password') || (admin.password.length >= 10 ? passwordProblem(admin.password) || undefined : undefined)}>
              <input id="ap" type="password" autoComplete="new-password" className="input" value={admin.password} onChange={(e) => { const v = e.target.value; setAdmin((admin0) => ({ ...admin0, password: v })) }} />
            </Field>
            <Field label="Password again" htmlFor="ac" error={pwMismatch ? 'The passwords do not match' : undefined}>
              <input id="ac" type="password" autoComplete="new-password" className="input" aria-invalid={pwMismatch} value={admin.confirm} onChange={(e) => { const v = e.target.value; setAdmin((admin0) => ({ ...admin0, confirm: v })) }} />
            </Field>
          </div>
          <Strength password={admin.password} />
        </section>
      )}

      {step === 2 && (
        <section className="space-y-4">
          <Heading icon={Users} color="text-violet-300" title="Who will have a phone?">
            Each person gets an extension number. Add the 911 location (building, floor, room) so responders know where to go.
          </Heading>
          <div className="space-y-2">
            {people.map((p, i) => (
              <div key={i} className="card grid items-end gap-2 p-3 sm:grid-cols-[6rem_1fr_1fr_auto]">
                <Field label="Extension" htmlFor={`n${i}`} error={personProblem(people, i, 'number') ?? xp(i, 'number')}>
                  <input id={`n${i}`} className="input font-mono text-sky-300" value={p.number}
                    onChange={(e) => { const v = e.target.value.replace(/\D/g, ''); setPeople((ps) => ps.map((x, j) => j === i ? { ...x, number: v } : x)) }} />
                </Field>
                <Field label="Name" htmlFor={`m${i}`} error={personProblem(people, i, 'name') ?? xp(i, 'name')}>
                  <input id={`m${i}`} className="input" value={p.name} placeholder="Full name or desk"
                    onChange={(e) => { const v = e.target.value; setPeople((ps) => ps.map((x, j) => j === i ? { ...x, name: v } : x)) }} />
                </Field>
                <Field label="911 location" htmlFor={`l${i}`}>
                  <input id={`l${i}`} className="input" value={p.location} placeholder="Main office, floor 2, room 210"
                    onChange={(e) => { const v = e.target.value; setPeople((ps) => ps.map((x, j) => j === i ? { ...x, location: v } : x)) }} />
                </Field>
                <button type="button" className="btn btn-danger" aria-label={`Remove ${p.number}`} disabled={people.length === 1}
                  onClick={() => setPeople((ps) => ps.filter((_, j) => j !== i))}><Trash2 size={15} aria-hidden /></button>
              </div>
            ))}
          </div>
          <button type="button" className="btn" onClick={() => {
            const used = new Set(people.map((p) => Number(p.number)))
            let n = 100
            while (used.has(n)) n++
            setPeople([...people, { number: String(n), name: '', email: '', location: people[people.length - 1]?.location || '' }])
          }}><Plus size={15} className="text-emerald-300" aria-hidden /> Add a person</button>
        </section>
      )}

      {step === 3 && (
        <section className="space-y-4">
          <Heading icon={Server} color="text-teal-300" title="Connect a phone line">
            A SIP trunk from a provider lets you call out and receive calls from the outside world. Your provider gives you these details.
          </Heading>
          <label className="flex items-center gap-3 text-sm text-slate-300">
            <Toggle checked={hasLine} onChange={setHasLine} label="I have a SIP trunk" /> I have SIP trunk details from my provider
          </label>
          {hasLine ? (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Provider server" htmlFor="th" hint="For example sip.provider.com" error={tp('host')}><input id="th" className="input" value={trunk.host} onChange={(e) => { const v = e.target.value.trim(); setTrunk((trunk0) => ({ ...trunk0, host: v })) }}
                onBlur={() => setTrunk((trunk0) => ({ ...trunk0, ...splitHost(trunk0.host, trunk0.port) }))} /></Field>
              <Field label="Port" htmlFor="tp" error={tp('port')}><input id="tp" type="number" className="input" min={1} max={65535} value={trunk.port || ''} onChange={(e) => { const v = Number(e.target.value); setTrunk((trunk0) => ({ ...trunk0, port: v })) }} /></Field>
              <Field label="Username or account" htmlFor="tu" error={tp('username')}><input id="tu" className="input" value={trunk.username} onChange={(e) => { const v = e.target.value.trim(); setTrunk((trunk0) => ({ ...trunk0, username: v })) }} /></Field>
              <Field label="Password" htmlFor="tw"><input id="tw" type="password" className="input" value={trunk.password} onChange={(e) => { const v = e.target.value; setTrunk((trunk0) => ({ ...trunk0, password: v })) }} /></Field>
              <label className="flex items-center gap-3 text-sm text-slate-300 sm:col-span-2">
                <Toggle checked={trunk.register} onChange={(v) => setTrunk({ ...trunk, register: v })} label="Register with the provider" />
                Register with the provider (most providers need this)
              </label>
              <Field label="Incoming calls ring" htmlFor="ib">
                <select id="ib" className="input" value={inbound} onChange={(e) => setInbound(e.target.value)}>
                  {people.length > 1 && <option value="all">Everyone at once</option>}
                  {people.map((p) => <option key={p.number} value={p.number}>{p.number} {p.name}</option>)}
                </select>
              </Field>
            </div>
          ) : (
            <p className="card p-4 text-sm text-slate-400">No problem: extensions can call each other now, and you can add a trunk later under <span className="text-teal-300">Trunks</span>.</p>
          )}
        </section>
      )}

      {step === 4 && (
        <section className="space-y-4">
          <Heading icon={Rocket} color="text-emerald-300" title="Ready to go">
            Check the details, then start your phone system. Everything can be changed later.
          </Heading>
          {err && <ErrorBox message={err.message} problems={err.problems} />}
          <div className="grid gap-3 sm:grid-cols-3">
            <Summary icon={Building2} color="text-sky-300" title={org} text={purpose === 'office' ? 'Office phones' : 'Contact centre'} />
            <Summary icon={Users} color="text-violet-300" title={`${people.length} extensions`} text={people.map((p) => p.number).join(', ')} />
            <Summary icon={Server} color="text-teal-300" title={hasLine ? trunk.host : 'No trunk yet'} text={hasLine ? 'Outside calls and 911' : 'Internal calls only'} />
          </div>
          <PhoneHelp addresses={status.addresses} port={status.sipPort} />
        </section>
      )}

      <div className="mt-8 flex justify-between">
        <button type="button" className="btn" disabled={step === 0} onClick={() => setStep(step - 1)}><ArrowLeft size={15} aria-hidden /> Back</button>
        {step < steps.length - 1 ? (
          <button type="submit" className="btn btn-primary" disabled={!canNext}>Next <ArrowRight size={15} aria-hidden /></button>
        ) : (
          <button type="submit" className="btn btn-primary" disabled={isLoading}><Rocket size={15} aria-hidden /> Start BitVoice</button>
        )}
      </div>
      </form>
    </Frame>
  )
}

function Frame({ children, wide }: { children: React.ReactNode; wide?: boolean }) {
  return (
    <div className="min-h-full bg-[radial-gradient(ellipse_at_top,var(--color-slate-900),var(--color-slate-950))] p-6">
      <div className="mx-auto mb-6 flex max-w-3xl items-center gap-2">
        <Logo />
        <span className="bg-gradient-to-r from-sky-300 to-violet-300 bg-clip-text text-lg font-bold text-transparent">BitVoice setup</span>
      </div>
      <div className={clsx('card rise mx-auto p-8', wide ? 'max-w-3xl' : 'max-w-md')}>{children}</div>
    </div>
  )
}

function Heading({ icon: Icon, color, title, children }: { icon: typeof Phone; color: string; title: string; children: React.ReactNode }) {
  return (
    <div>
      <h2 className="flex items-center gap-2 text-xl font-bold text-slate-50"><Icon className={color} aria-hidden /> {title}</h2>
      <p className="mt-1 text-sm text-slate-400">{children}</p>
    </div>
  )
}

function Summary({ icon: Icon, color, title, text }: { icon: typeof Phone; color: string; title: string; text: string }) {
  return (
    <div className="card p-4">
      <Icon className={color} size={20} aria-hidden />
      <div className="mt-2 truncate font-semibold text-slate-100">{title}</div>
      <div className="truncate text-sm text-slate-400">{text}</div>
    </div>
  )
}

const common = ['password', 'passw0rd', '123456789', '1234567890', 'qwertyuiop', 'asdfghjkl', 'letmein', 'iloveyou',
  'administrator', 'welcome1', 'changeme', 'abcdefghij', 'bitvoice']

/** Why the server would refuse a password (the same rules as the server's), or '' when it is fine. */
export function passwordProblem(p: string) {
  if ([...p].length < 10) return 'Use at least 10 characters'
  if (new Set([...p.toLowerCase()]).size < 5) return 'Use more different characters'
  const l = p.toLowerCase()
  if (common.some((w) => l.includes(w) && l.length < w.length + 4)) return 'That password is one attackers try first'
  return ''
}

// A provider's address as pasted ("sip:sip.example.com:5080") split into host and port, as the server does.
export function splitHost(v: string, port: number): { host: string; port: number } {
  const m = /^(?:sips?:|udp:\/\/|tcp:\/\/|tls:\/\/)?(?:[^@]*@)?(\[[^\]]+\]|[^:;/?]+)(?::(\d{1,5}))?(?:[;/?].*)?$/i.exec(v)
  if (!m || !/^(sips?:|udp:|tcp:|tls:)|:\d+$|@/i.test(v)) return { host: v, port }
  return { host: m[1].replace(/^\[|\]$/g, ''), port: m[2] ? Number(m[2]) : port }
}

// What is wrong with one person's row, shown under the field once there is something to say.
function personProblem(people: Person[], i: number, field: 'number' | 'name') {
  const p = people[i]
  if (field === 'number') {
    if (!/^\d{2,8}$/.test(p.number)) return '2 to 8 digits'
    if (people.some((x, j) => j < i && x.number === p.number)) return 'Already used above'
    return undefined
  }
  return p.name.trim() === '' ? 'Give a name or desk' : undefined
}

function Strength({ password }: { password: string }) {
  const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^a-zA-Z0-9]/].filter((r) => r.test(password)).length
  const distinct = new Set([...password.toLowerCase()]).size
  const score = passwordProblem(password) ? (password.length >= 10 ? 1 : 0)
    : Math.min(4, 2 + (password.length >= 16 ? 1 : 0) + (classes >= 3 || distinct >= 12 ? 1 : 0))
  const labels = ['Too short', 'Weak', 'Fair', 'Good', 'Strong']
  const colors = ['bg-rose-500', 'bg-rose-500', 'bg-amber-400', 'bg-emerald-400', 'bg-emerald-400']
  return (
    <div className="flex items-center gap-2" aria-live="polite">
      <div className="flex flex-1 gap-1">{[0, 1, 2, 3].map((i) => <span key={i} className={clsx('h-1.5 flex-1 rounded', i < score ? colors[score] : 'bg-slate-800')} />)}</div>
      <span className="text-xs text-slate-400">{labels[score]}</span>
    </div>
  )
}

/** What to type into a phone: shown in the wizard and on every extension. */
export function PhoneHelp({ addresses, port, tlsPort }: { addresses: string[]; port: number; tlsPort?: number }) {
  const server = addresses[0] ?? 'this computer'
  return (
    <div className="card p-4">
      <h3 className="flex items-center gap-2 font-semibold text-slate-100"><Globe size={16} className="text-sky-300" aria-hidden /> Connecting a phone</h3>
      <p className="mt-1 text-sm text-slate-400">In the phone or softphone app, set the SIP server to{' '}
        <Copyable text={port === 5060 ? server : `${server}:${port}`} />, the username to the extension number,
        and the password to the extension's SIP password (shown on its page).</p>
      {tlsPort ? <p className="mt-2 text-sm text-slate-400">For an encrypted connection choose transport <span className="text-emerald-300">TLS</span> and port <Copyable text={String(tlsPort)} />; the phone may ask you to accept BitVoice's certificate.</p> : null}
    </div>
  )
}

/** Copies text, also on plain http where the Clipboard API is missing (a LAN address). */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(text); return true }
  } catch { /* fall through to the old way */ }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  let ok = false
  try { ok = document.execCommand('copy') } catch { ok = false }
  area.remove()
  return ok
}

export function Copyable({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  return (
    <button type="button" className="mx-1 inline-flex items-center gap-1 rounded bg-slate-800 px-1.5 py-0.5 font-mono text-xs text-amber-300 hover:bg-slate-700"
      onClick={async () => {
        if (await copyText(text)) { setDone(true); setTimeout(() => setDone(false), 1200) } else toast.error('Copying is blocked here; select the text instead')
      }}
      title="Copy">
      {text} {done ? <Check size={11} className="text-emerald-300" aria-hidden /> : <Copy size={11} aria-hidden />}
    </button>
  )
}
