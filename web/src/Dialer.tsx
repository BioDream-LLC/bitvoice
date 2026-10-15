import { clsx } from 'clsx'
import { Ban, Megaphone, Pause, PhoneCall, PhoneForwarded, Play, Plus, SkipForward, Trash2, Upload, UserRound } from 'lucide-react'
import { useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  useAddDncMutation, useDncCountQuery, useImportDncMutation, useCampaignContactsQuery, useCampaignsQuery, useClearContactsMutation, useCreateCampaignMutation, useDeleteCampaignMessageMutation,
  useDeleteCampaignMutation, useDialerQuery, useDncQuery, useExtensionsQuery, usePreviewCallMutation, usePreviewNextMutation, usePutCampaignMessageMutation,
  useQueuesQuery, useRemoveDncMutation, useUpdateCampaignMutation, useUploadContactsMutation, useVoicemailQuery,
  type ApiErrorBody, type Campaign, type DialContact, type DialerRow,
} from './api'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { AudioRecording } from './Greeting'
import { Empty, Field, ListInput, Page, problemFor, Toggle } from './ui'

const modes = [
  ['preview', 'Preview', 'The agent sees the contact first, then calls (or skips). Nobody is ever kept waiting.'],
  ['progressive', 'Progressive', 'One call for each free agent: few abandoned calls, a little idle time.'],
  ['power', 'Power', 'A set number of calls per free agent, for lists where many do not answer.'],
  ['predictive', 'Predictive', 'BitVoice works out how many to call, keeping abandoned calls under the limit.'],
] as const
const dayNames = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const stateColour: Record<string, string> = { pending: 'text-sky-300', retry: 'text-amber-300', dialling: 'text-violet-300', done: 'text-emerald-300', failed: 'text-rose-300', dnc: 'text-slate-400' }

const blank = (queue: string): Campaign => ({ id: '', name: 'New campaign', mode: 'progressive', queue, active: false, callerId: '', as: '', ratio: 1.5, maxAbandon: 3,
  hours: [{ days: [1, 2, 3, 4, 5], start: '09:00', end: '20:00' }], timezone: '', ringSeconds: 25, attempts: 3, retryMinutes: 60, machine: 'hangup' })

/** The outbound dialer: campaigns that call lists of contacts for a queue's agents. */
export function Dialer() {
  const admin = useIsAdmin()
  const { data } = useCampaignsQuery()
  const { data: live } = useDialerQuery(undefined, { pollingInterval: 3000 })
  const { data: queues } = useQueuesQuery()
  const [update] = useUpdateCampaignMutation()
  const [edit, setEdit] = useState<Campaign | null>(null)
  const [open, setOpen] = useState<string>('')
  return (
    <Page icon={PhoneForwarded} title="Outbound dialer" accent="from-emerald-400 to-sky-400"
      intro="Campaigns call lists of contacts for a queue's agents: preview, progressive, power or predictive. Numbers on the do-not-call list are never called, calls are made only within the campaign's hours, answering machines are told apart from people, and a person answered with no agent free hears your message, with 9 to be taken off the list."
      actions={admin && <button className="btn btn-primary" disabled={!queues?.items.length} onClick={() => setEdit(blank(queues?.items[0]?.number ?? ''))}><Plus size={15} aria-hidden /> Add campaign</button>}>
      {!queues?.items.length && <p className="mb-3 text-sm text-amber-300">Make a queue with its agents first: a campaign's calls go to a queue.</p>}
      {!data?.items.length ? <Empty icon={PhoneForwarded} title="No campaigns yet">For example: renewals, called progressively by the Sales queue, weekdays 9 to 8.</Empty> : (
        <div className="space-y-3">
          {data.items.map((k) => {
            const st = live?.find((x) => x.id === k.id)
            return (
              <div key={k.id} className="card p-4">
                <div className="flex flex-wrap items-center gap-3">
                  <button className="font-semibold text-slate-100 hover:underline" onClick={() => setEdit(k)}>{k.name}</button>
                  <span className="text-xs text-sky-300">{modes.find((m) => m[0] === k.mode)?.[1]}</span>
                  <span className="text-xs text-slate-500">queue {k.queue}</span>
                  {st && <span className={st.open ? 'text-xs text-emerald-300' : 'text-xs text-slate-500'}>{st.open ? 'within its hours' : 'outside its hours'}</span>}
                  <span className="ml-auto flex items-center gap-2">
                    {admin && k.mode !== 'preview' && (
                      <button type="button" className={clsx('btn', k.active && 'btn-primary')} onClick={() => void attempt(() => update({ ...k, active: !k.active }).unwrap(), k.active ? `${k.name} paused` : `${k.name} started`)}>
                        {k.active ? <><Pause size={14} aria-hidden /> Pause</> : <><Play size={14} aria-hidden /> Start</>}
                      </button>
                    )}
                    <button type="button" className="btn" onClick={() => setOpen(open === k.id ? '' : k.id)}>{open === k.id ? 'Hide contacts' : 'Contacts'}</button>
                  </span>
                </div>
                {st && <Numbers st={st} k={k} />}
                {open === k.id && <Contacts k={k} />}
              </div>
            )
          })}
        </div>
      )}
      <DoNotCall />
      {edit && <CampaignEditor k={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function Numbers({ st, k }: { st: DialerRow; k: Campaign }) {
  const total = Object.values(st.states).reduce((a, b) => a + b, 0)
  const warn = (k.mode === 'power' || k.mode === 'predictive') && st.abandonRate > k.maxAbandon
  return (
    <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-sm">
      <span className="text-slate-400">{total} contacts:</span>
      {Object.entries(st.states).map(([s, n]) => <span key={s} className={stateColour[s] ?? 'text-slate-300'}>{n} {s}</span>)}
      <span className="text-slate-400">ringing <span className="text-violet-300">{st.ringing}</span></span>
      <span className="text-slate-400">free agents <span className="text-emerald-300">{st.freeAgents}</span></span>
      {k.mode === 'predictive' && <span className="text-slate-400">calls per agent <span className="text-sky-300">{st.ratio}</span></span>}
      <span className={warn ? 'text-rose-300' : 'text-slate-400'}>abandoned {st.abandonRate}%{warn ? ` (over ${k.maxAbandon}%: one call per agent until back under)` : ''}</span>
    </div>
  )
}

function Contacts({ k }: { k: Campaign }) {
  const admin = useIsAdmin()
  const [state, setState] = useState('')
  const { data } = useCampaignContactsQuery({ id: k.id, state }, { pollingInterval: 5000 })
  const [upload, { isLoading }] = useUploadContactsMutation()
  const [clear] = useClearContactsMutation()
  const file = useRef<HTMLInputElement>(null)
  const send = async (f: File) => {
    try {
      const r = await upload({ id: k.id, csv: await f.text() }).unwrap()
      toast.success(`${r.added} contacts added${r.doNotCall ? `, ${r.doNotCall} on the do-not-call list` : ''}${r.skipped ? `, ${r.skipped} lines skipped` : ''}`)
    } catch (e) { toast.error((e as { data?: { message?: string } }).data?.message ?? 'The list could not be read') }
  }
  return (
    <div className="mt-3 space-y-3 border-t border-slate-800 pt-3">
      <div className="flex flex-wrap items-center gap-2">
        <select className="input w-40" aria-label="Show contacts" value={state} onChange={(e) => setState(e.target.value)}>
          <option value="">All</option>{['pending', 'retry', 'dialling', 'done', 'failed', 'dnc'].map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        {admin && <>
          <button type="button" className="btn" disabled={isLoading} onClick={() => file.current?.click()}><Upload size={14} className="text-sky-300" aria-hidden /> Add a list (CSV)</button>
          <input ref={file} type="file" accept=".csv,text/csv" className="hidden" aria-label={`${k.name} contact list`} onChange={(e) => { const f = e.target.files?.[0]; if (f) void send(f); e.target.value = '' }} />
          <button type="button" className="btn" onClick={() => void attempt(() => clear({ id: k.id, state: state || undefined }).unwrap(), 'Contacts removed')}><Trash2 size={14} className="text-rose-300" aria-hidden /> Remove {state || 'all'}</button>
        </>}
        <span className="text-xs text-slate-500">A header row with a column called number (or phone); name, and any other columns, are shown to the agent.</span>
      </div>
      {k.mode === 'preview' && <PreviewDesk k={k} />}
      {!data?.length ? <p className="text-sm text-slate-500">No contacts{state ? ` ${state}` : ''}.</p> : (
        <table className="w-full text-sm">
          <thead><tr className="text-left text-xs text-slate-500"><th className="py-1">Number</th><th>Name</th><th>State</th><th>Tries</th><th>Last result</th><th>Agent</th></tr></thead>
          <tbody>
            {data.map((c) => (
              <tr key={c.id} className="border-t border-slate-800">
                <td className="py-1 font-mono text-sky-300">{c.number}</td><td className="text-slate-200">{c.name}</td>
                <td className={stateColour[c.state] ?? 'text-slate-300'}>{c.state}</td><td className="text-slate-400">{c.attempts}</td>
                <td className="text-slate-400">{c.result}</td><td className="font-mono text-slate-400">{c.agent}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

/** Preview: an agent takes the next contact, sees it, and calls or skips. */
function PreviewDesk({ k }: { k: Campaign }) {
  const { data: queues } = useQueuesQuery()
  const agents = queues?.items.find((q) => q.number === k.queue)?.agents ?? []
  const [agent, setAgent] = useState(agents[0] ?? '')
  const [cur, setCur] = useState<DialContact | null>(null)
  const [next] = usePreviewNextMutation()
  const [call] = usePreviewCallMutation()
  const take = async () => {
    try { setCur(await next({ id: k.id, agent }).unwrap()) } catch { toast.info('No contact is due') }
  }
  return (
    <div className="rounded-lg border border-sky-900/60 bg-sky-950/20 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <UserRound size={15} className="text-sky-300" aria-hidden />
        <label htmlFor={`pa-${k.id}`} className="text-sm text-slate-300">Agent</label>
        <select id={`pa-${k.id}`} className="input w-36" value={agent} onChange={(e) => setAgent(e.target.value)}>{agents.map((a) => <option key={a} value={a}>{a}</option>)}</select>
        <button type="button" className="btn" disabled={!agent || !!cur} onClick={take}>Next contact</button>
      </div>
      {cur && (
        <div className="mt-2 space-y-2">
          <p className="text-sm"><span className="font-mono text-sky-300">{cur.number}</span> <span className="text-slate-100">{cur.name}</span></p>
          {cur.fields && Object.keys(cur.fields).length > 0 && (
            <dl className="grid gap-x-3 text-xs sm:grid-cols-[8rem_1fr]">{Object.entries(cur.fields).map(([a, b]) => <><dt key={a} className="text-slate-500">{a}</dt><dd className="text-slate-300">{b}</dd></>)}</dl>
          )}
          <div className="flex gap-2">
            <button type="button" className="btn btn-primary" onClick={async () => { if (!await attempt(() => call({ id: k.id, contact: cur.id, agent }).unwrap(), `Calling ${cur.number}: ${agent}'s phone rings first`)) setCur(null) }}><PhoneCall size={14} aria-hidden /> Call</button>
            <button type="button" className="btn" onClick={async () => { if (!await attempt(() => call({ id: k.id, contact: cur.id, agent, skip: true }).unwrap(), 'Skipped')) setCur(null) }}><SkipForward size={14} aria-hidden /> Skip</button>
          </div>
        </div>
      )}
    </div>
  )
}

function DoNotCall() {
  const admin = useIsAdmin()
  const { data } = useDncQuery()
  const [add] = useAddDncMutation()
  const [remove] = useRemoveDncMutation()
  const [nums, setNums] = useState<string[]>([])
  const { data: count } = useDncCountQuery()
  const [importFile, { isLoading: importing }] = useImportDncMutation()
  const [replace, setReplace] = useState(true)
  const [file, setFile] = useState<File | null>(null)
  return (
    <section className="card mt-6 space-y-3 p-5" aria-labelledby="dnc-h">
      <h2 id="dnc-h" className="flex items-center gap-2 font-semibold text-slate-100"><Ban size={17} className="text-rose-300" aria-hidden /> Do-not-call list</h2>
      <p className="text-sm text-slate-400">No campaign calls these numbers. People who press 9 on the campaign message are added at once. Load the national list (in the US, the FTC's registry) for the area codes you call, as your licence allows.</p>
      {admin && (
        <div className="flex flex-wrap items-end gap-2">
          <Field label="Add numbers" htmlFor="dncadd" hint="Press Enter after each.">
            <ListInput id="dncadd" mono value={nums} onChange={setNums} placeholder="+1 312 555 0100" />
          </Field>
          <button type="button" className="btn" disabled={!nums.length} onClick={async () => { if (!await attempt(() => add(nums).unwrap(), `${nums.length} added`)) setNums([]) }}>Add</button>
        </div>
      )}
      {count && <p className="text-sm text-slate-300"><span className="font-semibold text-rose-300">{count.total.toLocaleString()}</span> numbers on the list{count.national ? <>, <span className="text-amber-300">{count.national.toLocaleString()}</span> from the national registry</> : ''}.</p>}
      {admin && (
        <div className="space-y-2 rounded-lg border border-slate-800 p-3">
          <p className="text-sm text-slate-300">National registry</p>
          <p className="text-xs text-slate-500">The file the registry gives you (the FTC's download is one "area code,number" a line; any file with one number a line works). Large files take a minute.</p>
          <div className="flex flex-wrap items-center gap-3">
            <input aria-label="Registry file" type="file" accept=".txt,.csv,text/plain,text/csv" className="text-sm text-slate-300" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
            <label className="flex items-center gap-2 text-sm text-slate-300"><Toggle checked={replace} onChange={setReplace} label="Replace the last import" /> Replace the last import <span className="text-xs text-slate-500">(numbers that left the registry are callable again; your own stay)</span></label>
            <button type="button" className="btn" disabled={!file || importing} onClick={async () => {
              if (!file) return
              try {
                const r = await importFile({ file, replace }).unwrap()
                toast.success(`${r.read.toLocaleString()} numbers read${r.skipped ? `, ${r.skipped} lines skipped` : ''}`)
                setFile(null)
              } catch (e) { toast.error((e as { data?: { message?: string } }).data?.message ?? 'The file was not read') }
            }}><Upload size={14} aria-hidden /> {importing ? 'Reading…' : 'Import'}</button>
          </div>
        </div>
      )}
      {!data?.length ? <p className="text-sm text-slate-500">The list is empty.</p> : (
        <ul className="grid gap-1 text-sm sm:grid-cols-2 lg:grid-cols-3">
          {data.slice(0, 300).map((d) => (
            <li key={d.number} className="flex items-center gap-2">
              <span className="font-mono text-slate-200">{d.number}</span><span className="truncate text-xs text-slate-500">{d.reason}</span>
              {admin && <button type="button" className="btn btn-ghost ml-auto px-1" aria-label={`Remove ${d.number}`} onClick={() => void attempt(() => remove(d.number).unwrap(), `${d.number} removed`)}><Trash2 size={12} className="text-rose-300" aria-hidden /></button>}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function CampaignEditor({ k: initial, onClose }: { k: Campaign; onClose: () => void }) {
  const admin = useIsAdmin()
  const [k, setK] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const { data: queues } = useQueuesQuery()
  const { data: exts } = useExtensionsQuery()
  const { data: vm } = useVoicemailQuery()
  const [create] = useCreateCampaignMutation()
  const [update] = useUpdateCampaignMutation()
  const [remove] = useDeleteCampaignMutation()
  const [putMsg] = usePutCampaignMessageMutation()
  const [delMsg] = useDeleteCampaignMessageMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const set = <K extends keyof Campaign>(f: K, v: Campaign[K]) => setK((x) => ({ ...x, [f]: v }))
  const setHours = (i: number, v: Partial<Campaign['hours'][number]>) => setK((x) => ({ ...x, hours: x.hours.map((h, j) => (j === i ? { ...h, ...v } : h)) }))
  const save = async () => {
    const e = await attempt(() => (isNew ? create(k) : update(k)).unwrap(), `Campaign ${k.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New campaign' : k.name} icon={<PhoneForwarded className="text-emerald-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Campaign removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <Field label="Name" htmlFor="kn" error={p('name')}><input id="kn" className="input" value={k.name} onChange={(e) => set('name', e.target.value)} /></Field>
        <div>
          <span className="label">How it calls</span>
          <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="How it calls">
            {modes.map(([id, t, h]) => (
              <button type="button" key={id} role="radio" aria-checked={k.mode === id} onClick={() => set('mode', id)} className={clsx('card p-3 text-left', k.mode === id && 'ring-2 ring-emerald-400')}>
                <div className="text-sm font-semibold text-emerald-300">{t}</div><div className="text-xs text-slate-500">{h}</div>
              </button>
            ))}
          </div>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Queue" htmlFor="kq" error={p('queue')} hint="Its agents take the calls.">
            <select id="kq" className="input" value={k.queue} onChange={(e) => set('queue', e.target.value)}>{queues?.items.map((q) => <option key={q.id} value={q.number}>{q.number} {q.name}</option>)}</select>
          </Field>
          <Field label="Caller ID" htmlFor="kc" error={p('callerId')} hint="The number contacts see; empty for the trunk's.">
            <input id="kc" className="input font-mono" value={k.callerId} onChange={(e) => set('callerId', e.target.value)} placeholder="+1 212 555 0100" />
          </Field>
          <Field label="Calls are made as" htmlFor="ka" error={p('as')} hint="Its outbound routes apply; empty for the first agent's.">
            <select id="ka" className="input" value={k.as} onChange={(e) => set('as', e.target.value)}>
              <option value="">The first agent</option>
              {exts?.items.filter((x) => x.outbound).map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
            </select>
          </Field>
          {k.mode === 'power' && (
            <Field label="Calls per free agent" htmlFor="kr" error={p('ratio')}><input id="kr" type="number" step={0.1} min={1} max={3} className="input w-28" value={k.ratio} onChange={(e) => set('ratio', Number(e.target.value))} /></Field>
          )}
          {(k.mode === 'power' || k.mode === 'predictive') && (
            <Field label="Abandoned calls at most (%)" htmlFor="kab" error={p('maxAbandon')} hint="Of calls answered by a person. 3% is the US rule (TCPA).">
              <input id="kab" type="number" step={0.5} min={0.5} max={10} className="input w-28" value={k.maxAbandon} onChange={(e) => set('maxAbandon', Number(e.target.value))} />
            </Field>
          )}
        </div>
        <div className="space-y-2">
          <span className="label">Calls may be made</span>
          {k.hours.map((h, i) => (
            <div key={i} className="card flex flex-wrap items-center gap-2 p-3">
              <div className="flex gap-1" role="group" aria-label={`Days for calling hours ${i + 1}`}>
                {[1, 2, 3, 4, 5, 6, 0].map((d) => (
                  <button type="button" key={d} aria-pressed={h.days.includes(d)} onClick={() => setHours(i, { days: h.days.includes(d) ? h.days.filter((x) => x !== d) : [...h.days, d] })}
                    className={`w-10 rounded py-1 text-xs ${h.days.includes(d) ? 'bg-emerald-400 font-semibold text-slate-950' : 'bg-slate-800 text-slate-400'}`}>{dayNames[d]}</button>
                ))}
              </div>
              <input type="time" className="input w-28" aria-label="From" value={h.start} onChange={(e) => setHours(i, { start: e.target.value })} />
              <span className="text-slate-500">to</span>
              <input type="time" className="input w-28" aria-label="Until" value={h.end} onChange={(e) => setHours(i, { end: e.target.value })} />
              <button type="button" className="btn ml-auto px-2" aria-label="Remove these hours" onClick={() => set('hours', k.hours.filter((_, j) => j !== i))}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
            </div>
          ))}
          {p('hours') && <p className="text-sm text-rose-300">{p('hours')}</p>}
          <button type="button" className="btn" onClick={() => set('hours', [...k.hours, { days: [6], start: '10:00', end: '17:00' }])}><Plus size={14} aria-hidden /> Add hours</button>
          <Field label="In time zone" htmlFor="ktz" error={p('timezone')} hint="Empty for the system's. In the US, call 8 am to 9 pm where the contact is.">
            <input id="ktz" className="input" value={k.timezone} onChange={(e) => set('timezone', e.target.value.trim())} placeholder="America/Chicago" />
          </Field>
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="Ring (seconds)" htmlFor="krs" error={p('ringSeconds')}><input id="krs" type="number" className="input w-24" value={k.ringSeconds} onChange={(e) => set('ringSeconds', Number(e.target.value))} /></Field>
          <Field label="Tries per contact" htmlFor="kat" error={p('attempts')}><input id="kat" type="number" className="input w-24" value={k.attempts} onChange={(e) => set('attempts', Number(e.target.value))} /></Field>
          <Field label="Minutes between tries" htmlFor="krm" error={p('retryMinutes')}><input id="krm" type="number" className="input w-24" value={k.retryMinutes} onChange={(e) => set('retryMinutes', Number(e.target.value))} /></Field>
        </div>
        <Field label="When an answering machine picks up" htmlFor="km" error={p('machine')}>
          <select id="km" className="input" value={k.machine} onChange={(e) => set('machine', e.target.value as Campaign['machine'])}>
            <option value="hangup">Hang up, and try again later</option>
            <option value="message">Leave the message after the greeting</option>
            <option value="agent">Do not detect machines: every answer goes to an agent</option>
          </select>
        </Field>
        {k.mode !== 'preview' && (
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={k.active} onChange={(v) => set('active', v)} label="Calling" /> Calling now (within its hours)</label>
        )}
      </fieldset>
      {!isNew && (
        <div className="mt-6 flex items-start gap-2">
          <Megaphone size={16} className="mt-2 text-amber-300" aria-hidden />
          <div className="flex-1">
            <AudioRecording label="Message" missing="No message recorded: a tone plays. Record who is calling, why, and that pressing 9 stops these calls."
              has={!!vm?.greetings?.includes('campaign-' + k.id)} src={`./api/campaigns/${k.id}/message`} saved="Message saved"
              save={(wav) => putMsg({ id: k.id, wav }).unwrap()} reset={() => delMsg(k.id).unwrap()} />
          </div>
        </div>
      )}
    </Drawer>
  )
}
