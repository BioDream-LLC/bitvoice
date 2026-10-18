import { clsx } from 'clsx'
import { Bot, Contact, FlaskConical, KeyRound, Moon, Plus, Sun, ToggleRight } from 'lucide-react'
import { useState } from 'react'
import {
  useAudioStreamsQuery, useCreateAudioStreamMutation, useDeleteAudioStreamMutation, useUpdateAudioStreamMutation, type AudioStream,
  useCreateLookupMutation, useCreatePinSetMutation, useCreateToggleMutation, useDeleteLookupMutation, useDeletePinSetMutation, useDeleteToggleMutation,
  useExtensionsQuery, useLookupsQuery, useOutboundRoutesQuery, useInboundRoutesQuery, usePinSetsQuery, useSettingsQuery, useSwitchToggleMutation, useTestLookupMutation,
  useToggleStateQuery, useTogglesQuery, useUpdateLookupMutation, useUpdatePinSetMutation, useUpdateToggleMutation,
  type ApiErrorBody, type CallerIDLookup, type PINSet, type Toggle as ToggleT,
} from './api'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { DestinationPicker, useDescribe } from './Extensions'
import { Empty, Field, ListInput, Page, problemFor, Toggle, freshName } from './ui'

/** Day/night toggles: switch a call flow over from a phone (*28 and its code), a busy lamp key or here. */
export function Toggles() {
  const admin = useIsAdmin()
  const { data } = useTogglesQuery()
  const { data: state } = useToggleStateQuery(undefined, { pollingInterval: 5000 })
  const { data: settings } = useSettingsQuery()
  const [flip] = useSwitchToggleMutation()
  const describe = useDescribe()
  const [edit, setEdit] = useState<ToggleT | null>(null)
  const code = settings?.system.codes?.toggle || '*28'
  const used = new Set(data?.items.map((t) => t.code))
  const nextCode = String([...Array(99).keys()].map((i) => i + 1).find((i) => !used.has(String(i))) ?? 1)
  return (
    <Page icon={ToggleRight} title="Day/night toggles" accent="from-indigo-400 to-amber-300"
      intro={`A switch for a call flow: calls go to the normal destination, or to the other one while someone has switched it over. Switch it from any phone by dialling ${code} and its code, from a busy lamp key watching ${code} and its code (lit while switched over), or here. It stays as it was left until switched back, and a schedule can sit behind either side.`}
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', name: freshName('Night service', data?.items.map((x) => x.name)), code: nextCode, normal: { type: 'hangup', target: '' }, override: { type: 'hangup', target: '' }, allowed: [] })}><Plus size={15} aria-hidden /> Add toggle</button>}>
      {!data?.items.length ? <Empty icon={ToggleRight} title="No toggles yet">For example: the office closes early, someone dials {code}1 on the way out, and calls go to voicemail until the morning.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((t) => {
            const on = state?.override.includes(t.id) ?? false
            return (
              <div key={t.id} className={clsx('card p-4', on && 'border-amber-700/70')}>
                <div className="flex items-center gap-2">
                  {on ? <Moon size={16} className="text-amber-300" aria-hidden /> : <Sun size={16} className="text-emerald-300" aria-hidden />}
                  <button className="font-semibold text-slate-100 hover:underline" onClick={() => setEdit(t)}>{t.name}</button>
                  <span className="font-mono text-xs text-indigo-300">{code}{t.code}</span>
                  <span className="ml-auto flex items-center gap-2 text-sm">
                    <span className={on ? 'text-amber-300' : 'text-emerald-300'}>{on ? 'Switched over' : 'Normal'}</span>
                    <Toggle checked={on} label={`Switch ${t.name} over`} onChange={(v) => void attempt(() => flip({ id: t.id, override: v }).unwrap(), v ? `${t.name} switched over` : `${t.name} back to normal`)} />
                  </span>
                </div>
                <p className={clsx('mt-2 text-sm', on ? 'text-slate-500' : 'text-slate-300')}>Normal: {describe(t.normal)}</p>
                <p className={clsx('text-sm', on ? 'text-amber-200' : 'text-slate-500')}>Switched over: {describe(t.override)}</p>
              </div>
            )
          })}
        </div>
      )}
      <p className="mt-4 text-sm text-slate-400">Send calls to a toggle from Call routing, a menu option or a schedule, like any other destination.</p>
      {edit && <ToggleEditor t={edit} code={code} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function ToggleEditor({ t: initial, code, onClose }: { t: ToggleT; code: string; onClose: () => void }) {
  const admin = useIsAdmin()
  const [t, setT] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: exts } = useExtensionsQuery()
  const [create] = useCreateToggleMutation()
  const [update] = useUpdateToggleMutation()
  const [remove] = useDeleteToggleMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const allowed = t.allowed ?? []
  const save = async () => {
    const e = await attempt(() => (isNew ? create(t) : update(t)).unwrap(), `Toggle ${t.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New toggle' : t.name} icon={<ToggleRight className="text-indigo-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Toggle removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Code" htmlFor="tgc" error={p('code')} hint={`Dial ${code}${t.code || '1'}.`}><input id="tgc" className="input font-mono text-indigo-300" value={t.code} onChange={(e) => setT({ ...t, code: e.target.value.replace(/\D/g, '') })} /></Field>
          <Field label="Name" htmlFor="tgn" error={p('name')}><input id="tgn" className="input" value={t.name} onChange={(e) => setT({ ...t, name: e.target.value })} /></Field>
        </div>
        <DestinationPicker label="Normally, calls go to" required value={t.normal} onChange={(d) => d && setT({ ...t, normal: d })} error={p('normal')} />
        <DestinationPicker label="Switched over, calls go to" required value={t.override} onChange={(d) => d && setT({ ...t, override: d })} error={p('override')} />
        <Field label="Who may switch it from a phone" error={p('allowed')} hint="Choose nobody to let any extension switch it. The web interface can always switch it.">
          <div className="grid gap-1.5 sm:grid-cols-2">
            {exts?.items.map((x) => {
              const on = allowed.includes(x.number)
              return (
                <button type="button" key={x.id} aria-pressed={on} onClick={() => setT({ ...t, allowed: on ? allowed.filter((m) => m !== x.number) : [...allowed, x.number] })}
                  className={clsx('flex items-center gap-2 rounded-lg border px-3 py-2 text-left text-sm', on ? 'border-indigo-600 bg-indigo-950/60 text-slate-100' : 'border-slate-800 text-slate-400 hover:border-slate-700')}>
                  <span className="font-mono text-indigo-300">{x.number}</span> <span className="truncate">{x.name}</span>
                </button>
              )
            })}
          </div>
        </Field>
      </fieldset>
    </Drawer>
  )
}

/** PIN sets: outbound routes ask for one of these PINs before calling out. */
export function PINSets() {
  const admin = useIsAdmin()
  const { data } = usePinSetsQuery()
  const { data: routes } = useOutboundRoutesQuery()
  const [edit, setEdit] = useState<PINSet | null>(null)
  return (
    <Page icon={KeyRound} title="PIN sets" accent="from-amber-400 to-emerald-400"
      intro="Lists of PINs an outbound route asks for before it calls out: to keep long-distance calls to those who have a PIN, or to bill calls to clients and projects by their code. Choose a PIN set on an outbound route in Call routing. Emergency calls never ask for one."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', name: 'Long distance', pins: [], record: true })}><Plus size={15} aria-hidden /> Add PIN set</button>}>
      {!data?.items.length ? <Empty icon={KeyRound} title="No PIN sets yet">For example: client codes, each written into the call record as its account code.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((s) => {
            const on = routes?.items.filter((r) => r.pinSet === s.id) ?? []
            return (
              <button key={s.id} className="card p-4 text-left hover:border-slate-700" onClick={() => setEdit(s)}>
                <div className="flex items-center gap-2">
                  <KeyRound size={16} className="text-amber-300" aria-hidden />
                  <span className="font-semibold text-slate-100">{s.name}</span>
                  <span className="ml-auto text-xs text-emerald-300">{s.pins.length} PINs</span>
                </div>
                <p className="mt-1 text-sm text-slate-400">{on.length ? `Asked for on ${on.map((r) => r.name).join(', ')}` : <span className="text-amber-300">Not on any outbound route yet</span>}{s.record ? ' · PIN kept as the account code' : ''}</p>
              </button>
            )
          })}
        </div>
      )}
      {edit && <PINSetEditor s={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function PINSetEditor({ s: initial, onClose }: { s: PINSet; onClose: () => void }) {
  const admin = useIsAdmin()
  const [s, setS] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [create] = useCreatePinSetMutation()
  const [update] = useUpdatePinSetMutation()
  const [remove] = useDeletePinSetMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(s) : update(s)).unwrap(), `PIN set ${s.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New PIN set' : s.name} icon={<KeyRound className="text-amber-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'PIN set removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <Field label="Name" htmlFor="psn" error={p('name')}><input id="psn" className="input" value={s.name} onChange={(e) => setS({ ...s, name: e.target.value })} /></Field>
        <Field label="PINs" htmlFor="psp" error={p('pins')} hint="4 to 10 digits each; press Enter after each one.">
          <ListInput id="psp" mono value={s.pins} onChange={(v) => setS({ ...s, pins: v })} placeholder="7001" />
        </Field>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={s.record} onChange={(v) => setS({ ...s, record: v })} label="Keep the PIN as the account code" />
          Keep the PIN in the call record as its account code <span className="text-xs text-slate-500">(Calls, and the call record outputs, show it)</span></label>
        <p className="text-xs text-slate-500">Callers hear "Please enter your PIN", enter it and #, and the call goes through. Three wrong PINs end the call and are logged in Security.</p>
      </fieldset>
    </Drawer>
  )
}

/** Caller ID lookups: name callers the provider sent no name for. */
export function Lookups() {
  const admin = useIsAdmin()
  const { data } = useLookupsQuery()
  const { data: routes } = useInboundRoutesQuery()
  const [edit, setEdit] = useState<CallerIDLookup | null>(null)
  return (
    <Page icon={Contact} title="Caller ID lookup" accent="from-sky-400 to-violet-400"
      intro="When the phone company sends a number but no name (and the company directory does not know it), BitVoice can ask a web service: your CRM, a contacts service, or OpenCNAM. Choose a lookup on an inbound route in Call routing. A slow or broken service never holds a call up: after its wait, the call rings without a name."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', name: 'CRM', kind: 'http', url: 'https://', field: '', username: '', password: '', timeoutMs: 1500, cacheHours: 24 })}><Plus size={15} aria-hidden /> Add lookup</button>}>
      {!data?.items.length ? <Empty icon={Contact} title="No lookups yet">For example: ask your CRM for the customer's name, so the phones show it while they ring.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((l) => {
            const on = routes?.items.filter((r) => r.lookup === l.id) ?? []
            return (
              <button key={l.id} className="card p-4 text-left hover:border-slate-700" onClick={() => setEdit(l)}>
                <div className="flex items-center gap-2">
                  <Contact size={16} className="text-sky-300" aria-hidden />
                  <span className="font-semibold text-slate-100">{l.name}</span>
                  <span className="ml-auto text-xs text-violet-300">{l.kind === 'opencnam' ? 'OpenCNAM' : 'Web address'}</span>
                </div>
                <p className="mt-1 text-sm text-slate-400">{on.length ? `Used by ${on.map((r) => r.name).join(', ')}` : <span className="text-amber-300">Not on any inbound route yet</span>}</p>
              </button>
            )
          })}
        </div>
      )}
      {edit && <LookupEditor l={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function LookupEditor({ l: initial, onClose }: { l: CallerIDLookup; onClose: () => void }) {
  const admin = useIsAdmin()
  const [l, setL] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [number, setNumber] = useState('')
  const [result, setResult] = useState<{ ok: boolean; name?: string; error?: string } | null>(null)
  const [create] = useCreateLookupMutation()
  const [update] = useUpdateLookupMutation()
  const [remove] = useDeleteLookupMutation()
  const [test, { isLoading: testing }] = useTestLookupMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(l) : update(l)).unwrap(), `Lookup ${l.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  const cnam = l.kind === 'opencnam'
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New caller ID lookup' : l.name} icon={<Contact className="text-sky-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Lookup removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <Field label="Name" htmlFor="lkn" error={p('name')}><input id="lkn" className="input" value={l.name} onChange={(e) => setL({ ...l, name: e.target.value })} /></Field>
        <div>
          <span className="label">Ask</span>
          <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="Ask">
            {([['http', 'A web address', 'Your CRM or any service answering with the name, as text or JSON.'], ['opencnam', 'OpenCNAM', 'The North American caller name database, with your account.']] as const).map(([k, t, h]) => (
              <button type="button" key={k} role="radio" aria-checked={l.kind === k} onClick={() => setL({ ...l, kind: k, url: k === 'opencnam' ? '' : l.url || 'https://' })}
                className={clsx('card p-3 text-left', l.kind === k && 'ring-2 ring-sky-400')}>
                <div className="text-sm font-semibold text-sky-300">{t}</div><div className="text-xs text-slate-500">{h}</div>
              </button>
            ))}
          </div>
          {p('kind') && <p className="mt-1 text-sm text-rose-300">{p('kind')}</p>}
        </div>
        {!cnam ? (
          <>
            <Field label="Address" htmlFor="lku" error={p('url')} hint="Put {number} where the caller's number goes. Asked with GET.">
              <input id="lku" className="input font-mono" value={l.url} onChange={(e) => setL({ ...l, url: e.target.value.trim() })} placeholder="https://crm.example.com/api/callers?phone={number}" />
            </Field>
            <Field label="Name field" htmlFor="lkf" error={p('field')} hint="Where the name is in a JSON answer, such as name or data.contact.name. Empty: the first line of a plain-text answer.">
              <input id="lkf" className="input font-mono" value={l.field} onChange={(e) => setL({ ...l, field: e.target.value.trim() })} placeholder="data.name" />
            </Field>
          </>
        ) : (
          <Field label="Address" htmlFor="lku" error={p('url')} hint="Leave empty for OpenCNAM's own.">
            <input id="lku" className="input font-mono" value={l.url} onChange={(e) => setL({ ...l, url: e.target.value.trim() })} placeholder="https://api.opencnam.com/v3/phone" />
          </Field>
        )}
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={cnam ? 'Account SID' : 'User name'} htmlFor="lkus" error={p('username')} hint={cnam ? undefined : 'Optional: basic authentication.'}>
            <input id="lkus" className="input font-mono" autoComplete="off" value={l.username} onChange={(e) => setL({ ...l, username: e.target.value.trim() })} />
          </Field>
          <Field label={cnam ? 'Auth token' : 'Password'} htmlFor="lkpw" error={p('password')}>
            <input id="lkpw" type="password" className="input font-mono" autoComplete="new-password" value={l.password} onChange={(e) => setL({ ...l, password: e.target.value })} />
          </Field>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Wait at most (ms)" htmlFor="lkt" error={p('timeoutMs')} hint="The call rings without a name after this.">
            <input id="lkt" type="number" className="input w-32" value={l.timeoutMs || ''} placeholder="1500" onChange={(e) => setL({ ...l, timeoutMs: Number(e.target.value) })} />
          </Field>
          <Field label="Keep answers (hours)" htmlFor="lkc" error={p('cacheHours')} hint="0 keeps them a day; -1 never keeps them.">
            <input id="lkc" type="number" className="input w-32" value={l.cacheHours} onChange={(e) => setL({ ...l, cacheHours: Number(e.target.value) })} />
          </Field>
        </div>
      </fieldset>
      {!isNew && admin && (
        <section className="mt-6 card space-y-3 p-4" aria-labelledby="lktest">
          <h3 id="lktest" className="flex items-center gap-2 text-sm font-semibold text-violet-300"><FlaskConical size={15} aria-hidden /> Try it (as saved)</h3>
          <div className="flex gap-2">
            <input aria-label="Number to look up" className="input max-w-xs font-mono" value={number} onChange={(e) => setNumber(e.target.value)} placeholder="+1 212 555 0199" />
            <button type="button" className="btn" disabled={testing || !number} onClick={async () => setResult(await test({ id: l.id, number }).unwrap().catch(() => ({ ok: false, error: 'could not ask' })))}>Look up</button>
          </div>
          {result && (result.ok
            ? <p className={result.name ? 'text-sm text-emerald-300' : 'text-sm text-amber-300'}>{result.name ? `Answered: ${result.name}` : 'Answered, but did not know the number.'}</p>
            : <p className="text-sm text-rose-300">Failed: {result.error}</p>)}
        </section>
      )}
    </Drawer>
  )
}

/** Voice bots: calls handed to a program over AudioSocket, with a fallback that always takes the caller. */
export function VoiceBots() {
  const admin = useIsAdmin()
  const { data } = useAudioStreamsQuery()
  const describe = useDescribe()
  const [edit, setEdit] = useState<AudioStream | null>(null)
  return (
    <Page icon={Bot} title="Voice bots (AudioSocket)" accent="from-fuchsia-400 to-sky-400"
      intro="Hand callers to a voice bot or speech service of your own over AudioSocket, the protocol Asterisk uses: BitVoice answers, connects to the bot, and streams the caller's audio and key presses both ways (8 kHz linear). Bots written for Asterisk work unchanged. Programs can also join a call in progress through ARI external media (Asterisk compatibility)."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', name: 'Voice bot', number: '', address: '', fallback: { type: 'hangup', target: '' } })}><Plus size={15} aria-hidden /> Add voice bot</button>}>
      {!data?.items.length ? <Empty icon={Bot} title="No voice bots yet">For example: after hours, calls go to your own AI agent at 10.0.0.9:9092; if it is down, to voicemail.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((s) => (
            <button key={s.id} className="card p-4 text-left hover:border-slate-700" onClick={() => setEdit(s)}>
              <div className="flex items-center gap-2">
                <Bot size={16} className="text-fuchsia-300" aria-hidden />
                {s.number && <span className="font-mono text-fuchsia-300">{s.number}</span>}
                <span className="font-semibold text-slate-100">{s.name}</span>
                <span className="ml-auto font-mono text-xs text-sky-300">{s.address}</span>
              </div>
              <p className="mt-1 text-sm text-slate-400">If the bot fails: <span className="text-amber-300">{describe(s.fallback)}</span></p>
            </button>
          ))}
        </div>
      )}
      {edit && <VoiceBotEditor s={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function VoiceBotEditor({ s: initial, onClose }: { s: AudioStream; onClose: () => void }) {
  const admin = useIsAdmin()
  const [s, setS] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [create] = useCreateAudioStreamMutation()
  const [update] = useUpdateAudioStreamMutation()
  const [remove] = useDeleteAudioStreamMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const e = await attempt(() => (isNew ? create(s) : update(s)).unwrap(), `Voice bot ${s.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New voice bot' : s.name} icon={<Bot className="text-fuchsia-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Voice bot removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="vbn" error={p('number')} hint="Optional."><input id="vbn" className="input font-mono text-fuchsia-300" value={s.number} onChange={(e) => setS({ ...s, number: e.target.value.replace(/\D/g, '') })} /></Field>
          <Field label="Name" htmlFor="vbm" error={p('name')}><input id="vbm" className="input" value={s.name} onChange={(e) => setS({ ...s, name: e.target.value })} /></Field>
        </div>
        <Field label="Bot address" htmlFor="vba" error={p('address')} hint="host:port of its AudioSocket server. BitVoice connects to it for each call.">
          <input id="vba" className="input font-mono" value={s.address} onChange={(e) => setS({ ...s, address: e.target.value.trim() })} placeholder="10.0.0.9:9092" />
        </Field>
        <DestinationPicker label="If the bot cannot take the call, or breaks off" required noSchedules={false} value={s.fallback.type ? s.fallback : null} onChange={(d) => d && setS({ ...s, fallback: d })} error={p('fallback')} />
        <p className="text-xs text-slate-500">The bot hanging up (an AudioSocket hang-up) ends the call. It gets the call&apos;s ID as the AudioSocket UUID, the caller&apos;s audio as 20 ms frames of 8 kHz 16-bit linear, and each key pressed.</p>
      </fieldset>
    </Drawer>
  )
}
