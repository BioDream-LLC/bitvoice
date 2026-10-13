import { clsx } from 'clsx'
import { BellOff, History, KeyRound, LogIn, LogOut, PhoneForwarded, Play, Plus, Trash2, Voicemail as VoicemailIcon, X } from 'lucide-react'
import { useState } from 'react'
import { Toaster } from 'sonner'
import {
  errorOf, useChangeMyPinMutation, useDeleteMyVoicemailMutation, useLogoutMutation, useMarkMyVoicemailMutation, useMeQuery, useMyCallsQuery,
  useMyVoicemailQuery, useMyselfQuery, usePortalLoginMutation, useResetMyGreetingMutation, useSaveMyGreetingMutation, useSaveMySettingsMutation,
  useSetMyDivertMutation, type ApiErrorBody, type FollowMe, type Myself, type SpeedDial,
} from './api'
import { attempt } from './Editor'
import { AudioRecording } from './Greeting'
import { Logo, useTheme } from './Shell'
import { ErrorBox, Field, Toggle, problemFor, relTime } from './ui'

/** #/me: the staff portal. People sign in with their extension and voicemail PIN and manage their own calls. */
export function Portal() {
  useTheme()
  const { data: me, isLoading, isError } = useMeQuery()
  if (isLoading) return null
  if (isError || !me || me.role !== 'extension') return <PortalLogin />
  return <PortalHome />
}

function PortalLogin() {
  const [login, { isLoading, error }] = usePortalLoginMutation()
  const [ext, setExt] = useState('')
  const [pin, setPin] = useState('')
  return (
    <div className="grid min-h-full place-items-center bg-[radial-gradient(ellipse_at_top,var(--color-slate-900),var(--color-slate-950))] p-6">
      <form className="card rise w-full max-w-sm space-y-4 p-8" onSubmit={(e) => { e.preventDefault(); login({ extension: ext, pin }) }}>
        <div className="flex flex-col items-center gap-2 pb-2">
          <Logo size={48} />
          <h1 className="bg-gradient-to-r from-teal-300 to-sky-300 bg-clip-text text-2xl font-bold text-transparent">Your phone</h1>
          <p className="text-center text-sm text-slate-400">Forwarding, do not disturb, follow-me, voicemail and your calls</p>
        </div>
        {error && <ErrorBox message={errorOf(error).message} />}
        <Field label="Your extension" htmlFor="pe"><input id="pe" className="input font-mono" inputMode="numeric" autoComplete="username" autoFocus value={ext} onChange={(e) => setExt(e.target.value.replace(/\D/g, ''))} /></Field>
        <Field label="Voicemail PIN" htmlFor="pp"><input id="pp" type="password" inputMode="numeric" className="input font-mono" autoComplete="current-password" value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} /></Field>
        <button className="btn btn-primary w-full justify-center" disabled={isLoading || !ext || !pin}><LogIn size={16} aria-hidden /> Sign in</button>
        <p className="flex items-center justify-center gap-1 text-center text-xs text-slate-500"><KeyRound size={12} aria-hidden /> No PIN yet? Ask your administrator to set one on your extension.</p>
        <p className="text-center text-xs"><a className="text-sky-300 underline" href="#/">Administrators sign in here</a></p>
      </form>
    </div>
  )
}

function PortalHome() {
  const { data: me } = useMyselfQuery()
  const [logout] = useLogoutMutation()
  const [tab, setTab] = useState<'calls' | 'voicemail' | 'history' | 'pin'>('calls')
  if (!me) return null
  const tabs = [
    { id: 'calls', label: 'Where calls go', icon: PhoneForwarded, color: 'text-teal-300' },
    { id: 'voicemail', label: `Voicemail${me.messages.new ? ` (${me.messages.new} new)` : ''}`, icon: VoicemailIcon, color: 'text-violet-300' },
    { id: 'history', label: 'Your calls', icon: History, color: 'text-fuchsia-300' },
    { id: 'pin', label: 'PIN', icon: KeyRound, color: 'text-amber-300' },
  ] as const
  return (
    <div className="min-h-full bg-slate-950">
      <Toaster theme="dark" richColors position="bottom-right" />
      <header className="flex items-center gap-3 border-b border-slate-800 px-6 py-4">
        <Logo size={32} />
        <div>
          <h1 className="font-semibold text-slate-100">{me.name}</h1>
          <p className="font-mono text-xs text-sky-300">Extension {me.number}</p>
        </div>
        <a className="btn btn-ghost ml-auto text-sm" href="#/phone">Browser phone</a>
        <button className="btn btn-ghost text-sm" onClick={() => logout()}><LogOut size={14} aria-hidden /> Sign out</button>
      </header>
      <nav className="flex gap-1 border-b border-slate-800 px-6" aria-label="Portal">
        {tabs.map((t) => (
          <button key={t.id} type="button" onClick={() => setTab(t.id)} aria-current={tab === t.id ? 'page' : undefined}
            className={clsx('flex items-center gap-2 border-b-2 px-3 py-3 text-sm', tab === t.id ? 'border-sky-400 text-slate-100' : 'border-transparent text-slate-400 hover:text-slate-200')}>
            <t.icon size={15} className={t.color} aria-hidden /> {t.label}
          </button>
        ))}
      </nav>
      <main className="mx-auto max-w-4xl space-y-5 p-6">
        {tab === 'calls' && <CallsGo me={me} />}
        {tab === 'voicemail' && <MyVoicemail me={me} />}
        {tab === 'history' && <MyCalls />}
        {tab === 'pin' && <MyPin />}
      </main>
    </div>
  )
}

function CallsGo({ me }: { me: Myself }) {
  const [setDivert] = useSetMyDivertMutation()
  const [saveSettings] = useSaveMySettingsMutation()
  const [fwd, setFwd] = useState({ all: me.divert.all ?? '', busy: me.divert.busy ?? '', noAnswer: me.divert.noAnswer ?? '' })
  const [fwdErr, setFwdErr] = useState<ApiErrorBody | null>(null)
  const [fm, setFm] = useState<FollowMe>({ ...me.followMe, strategy: me.followMe.strategy || 'sequential', ringSeconds: me.followMe.ringSeconds || 20 })
  const [sds, setSds] = useState<SpeedDial[]>(me.speedDials)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const p = (f: string) => problemFor(err?.problems, f)
  const nums = fm.numbers ?? []
  const c = me.codes
  return (
    <>
      <section className="card space-y-3 p-5" aria-labelledby="dnd-h">
        <h2 id="dnd-h" className="flex items-center gap-2 font-semibold text-rose-300"><BellOff size={16} aria-hidden /> Do not disturb</h2>
        <label className="flex items-center gap-3 text-sm text-slate-300">
          <Toggle checked={me.dnd} label="Do not disturb" onChange={(v) => attempt(() => setDivert({ dnd: v }).unwrap(), v ? 'Do not disturb on' : 'Do not disturb off')} />
          {me.dnd ? <span className="text-rose-300">On: your phones do not ring; calls go to voicemail.</span> : <span className="text-slate-400">Off: your phones ring.</span>}
        </label>
        <p className="text-xs text-slate-500">From your phone: {c.dndOn} on, {c.dndOff} off, {c.dndToggle} switches.</p>
      </section>
      <section className="card space-y-3 p-5" aria-labelledby="fwd-h">
        <h2 id="fwd-h" className="flex items-center gap-2 font-semibold text-sky-300"><PhoneForwarded size={16} aria-hidden /> Forwarding</h2>
        <div className="grid gap-3 sm:grid-cols-3">
          {([['all', 'Every call to', c.forwardAll], ['busy', 'When I am on a call, to', c.forwardBusy], ['noAnswer', 'When I do not answer, to', c.forwardNoAnswer]] as const).map(([k, l, code]) => (
            <Field key={k} label={l} htmlFor={`pf-${k}`} hint={`From your phone: ${code}`}>
              <input id={`pf-${k}`} className={clsx('input font-mono', fwd[k] && 'text-sky-300')} placeholder="Not forwarded" value={fwd[k]} onChange={(e) => setFwd({ ...fwd, [k]: e.target.value })} />
            </Field>
          ))}
        </div>
        {fwdErr && <p className="text-sm text-rose-300">{fwdErr.message}</p>}
        {!me.outbound && <p className="text-xs text-amber-300">Your extension cannot call outside numbers, so it can forward only to other extensions.</p>}
        <button type="button" className="btn btn-primary" onClick={async () => setFwdErr(await attempt(() => setDivert(fwd).unwrap(), 'Forwarding saved'))}>Save forwarding</button>
      </section>
      <section className="card space-y-3 p-5" aria-labelledby="fm-h">
        <h2 id="fm-h" className="flex items-center gap-2 font-semibold text-teal-300"><PhoneForwarded size={16} aria-hidden /> Follow-me</h2>
        <label className="flex items-center gap-3 text-sm text-slate-300">
          <Toggle checked={fm.enabled} label="Follow-me" onChange={(v) => setFm({ ...fm, enabled: v, numbers: v && !nums.length ? [''] : nums })} />
          When I do not answer, ring these numbers too
        </label>
        {fm.enabled && (
          <div className="space-y-2">
            {nums.map((n, i) => (
              <div key={i} className="flex items-center gap-2">
                <input aria-label={`Follow-me number ${i + 1}`} className="input max-w-xs font-mono text-teal-300" value={n} placeholder="My mobile: +1 212 555 0199"
                  onChange={(e) => setFm({ ...fm, numbers: nums.map((x, j) => (j === i ? e.target.value : x)) })} />
                <button type="button" className="btn btn-ghost px-2" aria-label={`Remove number ${i + 1}`} onClick={() => setFm({ ...fm, numbers: nums.filter((_, j) => j !== i) })}><X size={14} aria-hidden /></button>
                {p(`followMe.numbers[${i}]`) && <span className="text-xs text-rose-300">{p(`followMe.numbers[${i}]`)}</span>}
              </div>
            ))}
            {p('followMe.numbers') && <p className="text-xs text-rose-300">{p('followMe.numbers')}</p>}
            <button type="button" className="btn btn-ghost text-sm" onClick={() => setFm({ ...fm, numbers: [...nums, ''] })}><Plus size={14} aria-hidden /> Add a number</button>
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={fm.confirm} label="Press 1 to take the call" onChange={(v) => setFm({ ...fm, confirm: v })} /> On an outside number, I press 1 to take the call</label>
          </div>
        )}
        <p className="text-xs text-slate-500">From your phone, {c.followMe} switches follow-me on and off.</p>
      </section>
      <section className="card space-y-3 p-5" aria-labelledby="sd-h">
        <h2 id="sd-h" className="font-semibold text-amber-300">Speed dials</h2>
        <p className="text-xs text-slate-500">Dial {c.speedDial} and the code. Yours win over the company's.</p>
        {sds.map((s, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <input aria-label={`Speed dial ${i + 1} code`} className="input w-20 font-mono text-amber-300" maxLength={3} value={s.code} onChange={(e) => setSds(sds.map((x, j) => (j === i ? { ...x, code: e.target.value.replace(/\D/g, '') } : x)))} />
            <input aria-label={`Speed dial ${i + 1} number`} className="input w-48 font-mono text-teal-300" value={s.number} onChange={(e) => setSds(sds.map((x, j) => (j === i ? { ...x, number: e.target.value } : x)))} />
            <input aria-label={`Speed dial ${i + 1} name`} className="input w-48" value={s.name} onChange={(e) => setSds(sds.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
            <button type="button" className="btn btn-ghost px-2" aria-label={`Remove speed dial ${i + 1}`} onClick={() => setSds(sds.filter((_, j) => j !== i))}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
            {(p(`speedDials[${i}].code`) || p(`speedDials[${i}].number`)) && <span className="text-xs text-rose-300">{p(`speedDials[${i}].code`) ?? p(`speedDials[${i}].number`)}</span>}
          </div>
        ))}
        <button type="button" className="btn btn-ghost text-sm" onClick={() => setSds([...sds, { code: String(sds.length + 1), number: '', name: '' }])}><Plus size={14} aria-hidden /> Add a speed dial</button>
        {me.companySpeedDials.length > 0 && (
          <details className="text-sm text-slate-400"><summary className="cursor-pointer">The company's speed dials</summary>
            <ul className="mt-2 space-y-1">{me.companySpeedDials.map((s) => <li key={s.code}><span className="font-mono text-amber-300">{c.speedDial}{s.code}</span> {s.name || s.number} <span className="font-mono text-xs text-slate-500">{s.number}</span></li>)}</ul>
          </details>
        )}
      </section>
      {err && !err.problems?.length && <p className="text-sm text-rose-300">{err.message}</p>}
      <button type="button" className="btn btn-primary" onClick={async () => setErr(await attempt(() => saveSettings({ followMe: fm, speedDials: sds }).unwrap(), 'Saved'))}>Save follow-me and speed dials</button>
    </>
  )
}

function MyVoicemail({ me }: { me: Myself }) {
  const { data } = useMyVoicemailQuery()
  const [mark] = useMarkMyVoicemailMutation()
  const [del] = useDeleteMyVoicemailMutation()
  const [saveG, { isLoading }] = useSaveMyGreetingMutation()
  const [resetG] = useResetMyGreetingMutation()
  const [playing, setPlaying] = useState('')
  if (!me.voicemail) return <p className="text-sm text-slate-400">Voicemail is off for your extension. Ask your administrator to switch it on.</p>
  return (
    <>
      <section className="card p-5" aria-labelledby="vm-h">
        <h2 id="vm-h" className="mb-3 font-semibold text-violet-300">Messages</h2>
        {!data?.items.length ? <p className="text-sm text-slate-500">No messages.</p> : (
          <ul className="divide-y divide-slate-800">
            {data.items.map((v) => (
              <li key={v.id} className="py-3">
                <div className="flex items-center gap-3 text-sm">
                  {!v.heard && <span className="h-2 w-2 rounded-full bg-violet-400" aria-label="new" />}
                  <span className="text-slate-100">{v.fromName || v.from}</span>
                  <span className="font-mono text-xs text-slate-500">{v.from}</span>
                  <span className="text-xs text-slate-500">{relTime(v.at)} · {v.seconds}s</span>
                  <button type="button" className="btn btn-ghost ml-auto px-2" aria-label={`Play the message from ${v.fromName || v.from}`} onClick={() => { setPlaying(v.id); if (!v.heard) mark({ id: v.id, heard: true }) }}><Play size={14} className="text-emerald-300" aria-hidden /></button>
                  <button type="button" className="btn btn-ghost px-2" aria-label={`Delete the message from ${v.fromName || v.from}`} onClick={() => attempt(() => del(v.id).unwrap(), 'Message deleted')}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
                </div>
                {v.summary && <p className="mt-1 text-sm text-slate-300">{v.summary}</p>}
                {playing === v.id && <audio className="mt-2 w-full" controls autoPlay src={`./api/me/voicemail/${v.id}/audio`} />}
              </li>
            ))}
          </ul>
        )}
        <p className="mt-3 text-xs text-slate-500">From any phone here, dial {me.voicemailNumber} to hear them.</p>
      </section>
      <section className="card p-5">
        <AudioRecording canEdit label="Your greeting" missing="No greeting recorded: callers hear a beep only." has={!!data?.greeting} src="./api/me/greeting"
          busy={isLoading} saved="Greeting saved" save={(wav) => saveG(wav).unwrap()} reset={() => resetG().unwrap()} />
      </section>
    </>
  )
}

function MyCalls() {
  const { data } = useMyCallsQuery()
  if (!data) return null
  if (!data.items.length) return <p className="text-sm text-slate-500">No calls yet.</p>
  return (
    <table className="w-full text-sm">
      <thead><tr className="text-left text-xs text-slate-500"><th className="pb-2 font-normal">When</th><th className="pb-2 font-normal">Who</th><th className="pb-2 font-normal">What happened</th><th className="pb-2 font-normal">Length</th></tr></thead>
      <tbody className="divide-y divide-slate-800">
        {data.items.map((c) => (
          <tr key={c.id}>
            <td className="py-2 text-slate-400">{relTime(c.start)}</td>
            <td className="py-2"><span className="text-slate-200">{c.fromName || c.from}</span> <span className="text-slate-500">→</span> <span className="text-slate-200">{c.toName || c.to}</span></td>
            <td className={clsx('py-2', c.disposition === 'answered' ? 'text-emerald-300' : 'text-amber-300')}>{c.disposition}</td>
            <td className="py-2 font-mono text-slate-400">{c.billsec ? `${Math.floor(c.billsec / 60)}:${String(c.billsec % 60).padStart(2, '0')}` : ''}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function MyPin() {
  const [change] = useChangeMyPinMutation()
  const [old, setOld] = useState('')
  const [pin, setPin] = useState('')
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  return (
    <form className="card max-w-sm space-y-4 p-5" onSubmit={async (e) => { e.preventDefault(); const r = await attempt(() => change({ old, new: pin }).unwrap(), 'PIN changed'); setErr(r); if (!r) { setOld(''); setPin('') } }}>
      <h2 className="font-semibold text-amber-300">Change your PIN</h2>
      <p className="text-xs text-slate-500">The same PIN opens your voicemail from a phone, signs you in at hot desks and here.</p>
      <Field label="Current PIN" htmlFor="po"><input id="po" type="password" inputMode="numeric" className="input font-mono" value={old} onChange={(e) => setOld(e.target.value.replace(/\D/g, ''))} /></Field>
      <Field label="New PIN" htmlFor="pn" error={problemFor(err?.problems, 'new')} hint="4 to 10 digits."><input id="pn" type="password" inputMode="numeric" className="input font-mono" value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} /></Field>
      {err && !err.problems?.length && <p className="text-sm text-rose-300">{err.message}</p>}
      <button className="btn btn-primary" disabled={!old || !pin}>Change PIN</button>
    </form>
  )
}
