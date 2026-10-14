import { clsx } from 'clsx'
import { AlarmClock, BedDouble, Hotel as HotelIcon, LogIn, LogOut, Plug, Sparkles, X } from 'lucide-react'
import { useState } from 'react'
import {
  useCancelWakeUpMutation, useCheckInMutation, useCheckOutMutation, useExtensionsQuery, useHotelQuery, usePromptsQuery,
  useSaveSettingsMutation, useSetRoomStatusMutation, useSetWakeUpMutation, useSettingsQuery, useWakeUpsQuery,
  type ApiErrorBody, type Hotel as HotelSettings,
} from './api'
import { attempt, useIsAdmin } from './Editor'
import { Empty, Field, Page, problemFor, relTime, Toggle } from './ui'

const defaults: HotelSettings = { wakeUpTries: 3, wakeUpRetryMinutes: 5, notify: '', barVacant: false, checkOutClear: true,
  pms: { kind: '', host: '', port: 5010, chargeCalls: false, chargeUrl: '', markup: 0 } }

/** Local date-time for an <input type="datetime-local">, a minute from now by default. */
function localInput(d: Date) {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

/** Hotel: guest rooms, wake-up calls and the link to the hotel system. */
export function Hotel() {
  return (
    <Page icon={HotelIcon} title="Hotel and wake-up calls" accent="from-amber-400 to-violet-400"
      intro="Wake-up calls for anyone (guests, night shifts, on-call staff), and for hotels: guest rooms checked in and out by the hotel system, calls charged to the guest's bill, and room status from housekeeping.">
      <WakeUps />
      <Rooms />
      <HotelSettingsCard />
    </Page>
  )
}

function WakeUps() {
  const admin = useIsAdmin()
  const { data } = useWakeUpsQuery()
  const { data: exts } = useExtensionsQuery()
  const [setWake] = useSetWakeUpMutation()
  const [cancel] = useCancelWakeUpMutation()
  const [ext, setExt] = useState('')
  const [at, setAt] = useState(() => localInput(new Date(Date.now() + 3600_000)))
  const [daily, setDaily] = useState(false)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const book = async () => setErr(await attempt(() => setWake({ ext, at: new Date(at).toISOString(), daily }).unwrap(), `Wake-up call for ${ext} booked`))
  return (
    <section className="card mb-6 space-y-4 p-5" aria-labelledby="wk-h">
      <h2 id="wk-h" className="flex items-center gap-2 font-semibold text-slate-100"><AlarmClock size={18} className="text-amber-300" aria-hidden /> Wake-up calls</h2>
      <p className="text-sm text-slate-400">People book their own from their phone with <span className="font-mono text-sky-300">*62</span> (four digits, such as 0630; star cancels).
        An unanswered call is tried again; see the settings below.</p>
      {admin && (
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Extension" htmlFor="wke">
            <select id="wke" className="input w-56" value={ext} onChange={(e) => setExt(e.target.value)}>
              <option value="">Choose…</option>
              {exts?.items.map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
            </select>
          </Field>
          <Field label="When" htmlFor="wka" error={problemFor(err?.problems, 'at')}><input id="wka" type="datetime-local" className="input" value={at} onChange={(e) => setAt(e.target.value)} /></Field>
          <label className="flex items-center gap-2 pb-2 text-sm text-slate-300"><Toggle checked={daily} onChange={setDaily} label="Every day" /> Every day</label>
          <button type="button" className="btn btn-primary" disabled={!ext} onClick={book}><AlarmClock size={14} aria-hidden /> Book</button>
        </div>
      )}
      {!data?.items?.length ? <p className="text-sm text-slate-500">No wake-up calls booked.</p> : (
        <ul className="divide-y divide-slate-800 rounded-md border border-slate-800">
          {(data.items ?? []).map((w) => (
            <li key={w.id} className="flex flex-wrap items-center gap-3 p-2 text-sm">
              <span className="font-mono text-sky-300">{w.extension}</span>
              <span className="text-slate-200">{new Date(w.at).toLocaleString([], { weekday: 'short', hour: '2-digit', minute: '2-digit' })}</span>
              {w.daily && <span className="text-violet-300">every day</span>}
              <span className="text-xs text-slate-500">by {w.by}</span>
              {w.tries > 0 && <span className="text-xs text-amber-300">try {w.tries + 1}, last: {w.last}</span>}
              {admin && <button type="button" className="btn ml-auto px-2 py-1" aria-label={`Cancel wake-up of ${w.extension}`} onClick={() => attempt(() => cancel(w.extension).unwrap(), 'Wake-up call cancelled')}><X size={13} className="text-rose-300" aria-hidden /></button>}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function Rooms() {
  const admin = useIsAdmin()
  const { data } = useHotelQuery()
  const { data: langs } = usePromptsQuery()
  const [checkIn] = useCheckInMutation()
  const [checkOut] = useCheckOutMutation()
  const [status] = useSetRoomStatusMutation()
  const [form, setForm] = useState<{ room: string; name: string; account: string; language: string } | null>(null)
  if (!data) return null
  return (
    <section className="card mb-6 space-y-4 p-5" aria-labelledby="rm-h">
      <div className="flex flex-wrap items-center gap-3">
        <BedDouble size={18} className="text-violet-300" aria-hidden />
        <h2 id="rm-h" className="font-semibold text-slate-100">Guest rooms</h2>
        <span className="text-sm text-slate-400"><span className="text-emerald-300">{data.rooms.filter((r) => r.guest).length}</span> of {data.rooms.length} occupied</span>
        {data.link && (data.link.up
          ? <span className="ml-auto flex items-center gap-1 text-sm text-emerald-300"><Plug size={14} aria-hidden /> Hotel system linked{data.link.lastSeen && <span className="text-xs text-slate-500"> (heard {relTime(data.link.lastSeen)})</span>}</span>
          : <span className="ml-auto flex items-center gap-1 text-sm text-rose-300"><Plug size={14} aria-hidden /> Hotel system not reachable{data.link.waiting > 0 && ` (${data.link.waiting} waiting to send)`}</span>)}
      </div>
      {!data.rooms.length ? <Empty icon={BedDouble} title="No guest rooms">Switch on Guest room on the extension of each room's phone.</Empty> : (
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {data.rooms.map((r) => (
            <div key={r.extension} className={clsx('rounded-md border p-3', r.guest ? 'border-emerald-800 bg-emerald-950/20' : 'border-slate-800')}>
              <div className="flex items-center gap-2">
                <span className="font-mono text-sky-300">{r.extension}</span><span className="truncate text-slate-300">{r.name}</span>
                {r.status && <span className="ml-auto rounded bg-violet-500/15 px-1.5 text-xs text-violet-300" title={`Reported ${relTime(r.statusAt)}`}>status {r.status}</span>}
              </div>
              <p className={clsx('mt-1 text-sm', r.guest ? 'text-emerald-300' : 'text-slate-500')}>{r.guest ? r.guest.name || 'Guest' : 'Vacant'}{r.guest?.language && <span className="text-xs text-slate-400"> · {r.guest.language}</span>}</p>
              {r.wakeUp && <p className="text-xs text-amber-300">Wake-up {new Date(r.wakeUp.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</p>}
              {admin && (
                <div className="mt-2 flex flex-wrap gap-1">
                  {r.guest
                    ? <button type="button" className="btn px-2 py-1 text-xs" onClick={() => attempt(() => checkOut(r.extension).unwrap(), `Room ${r.extension} checked out`)}><LogOut size={12} className="text-rose-300" aria-hidden /> Check out</button>
                    : <button type="button" className="btn px-2 py-1 text-xs" onClick={() => setForm({ room: r.extension, name: '', account: '', language: '' })}><LogIn size={12} className="text-emerald-300" aria-hidden /> Check in</button>}
                  <select className="input w-28 py-1 text-xs" aria-label={`Status of room ${r.extension}`} value="" onChange={(e) => e.target.value && attempt(() => status({ room: r.extension, status: e.target.value }).unwrap(), 'Room status saved')}>
                    <option value="">Status…</option>
                    {[1, 2, 3, 4, 5, 6, 7, 8, 9].map((n) => <option key={n} value={String(n)}>{n}</option>)}
                  </select>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
      {form && (
        <div className="flex flex-wrap items-end gap-3 rounded-md border border-slate-700 p-3">
          <span className="pb-2 font-mono text-sky-300">{form.room}</span>
          <Field label="Guest name" htmlFor="gn"><input id="gn" className="input w-56" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></Field>
          <Field label="Reservation" htmlFor="ga"><input id="ga" className="input w-36 font-mono" value={form.account} onChange={(e) => setForm({ ...form, account: e.target.value })} /></Field>
          <Field label="Guest language" htmlFor="gl">
            <select id="gl" className="input w-40" value={form.language} onChange={(e) => setForm({ ...form, language: e.target.value })}>
              <option value="">The system's</option>
              {langs?.languages.map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
            </select>
          </Field>
          <button type="button" className="btn btn-primary" onClick={async () => { if (!await attempt(() => checkIn(form).unwrap(), `${form.name || 'Guest'} checked in to ${form.room}`)) setForm(null) }}><LogIn size={14} aria-hidden /> Check in</button>
          <button type="button" className="btn" onClick={() => setForm(null)}>Cancel</button>
        </div>
      )}
      <p className="text-xs text-slate-500">Housekeeping report a room's status from its phone with <span className="font-mono text-sky-300">*68</span> and a digit (the hotel system says what each digit means).</p>
    </section>
  )
}

function HotelSettingsCard() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const { data: exts } = useExtensionsQuery()
  const [save] = useSaveSettingsMutation()
  const [h, setH] = useState<HotelSettings | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!data) return null
  const cur: HotelSettings = h ?? { ...defaults, ...data.system.hotel, pms: { ...defaults.pms, ...data.system.hotel?.pms } }
  const set = (v: Partial<HotelSettings>) => setH({ ...cur, ...v })
  const setP = (v: Partial<HotelSettings['pms']>) => setH({ ...cur, pms: { ...cur.pms, ...v } })
  const p = (f: string) => problemFor(err?.problems, 'system.hotel.' + f)
  const submit = async () => { const e = await attempt(() => save({ ...data, system: { ...data.system, hotel: cur } }).unwrap(), 'Hotel settings saved'); setErr(e); if (!e) setH(null) }
  return (
    <section className="card space-y-4 p-5" aria-labelledby="hs-h">
      <h2 id="hs-h" className="flex items-center gap-2 font-semibold text-slate-100"><Sparkles size={18} className="text-violet-300" aria-hidden /> Settings</h2>
      <fieldset disabled={!admin} className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="Tries" htmlFor="ht" error={p('wakeUpTries')} hint="An unanswered wake-up is tried again."><input id="ht" type="number" min={1} max={10} className="input" value={cur.wakeUpTries || ''} onChange={(e) => set({ wakeUpTries: Number(e.target.value) })} /></Field>
          <Field label="Minutes apart" htmlFor="hr" error={p('wakeUpRetryMinutes')}><input id="hr" type="number" min={1} max={60} className="input" value={cur.wakeUpRetryMinutes || ''} onChange={(e) => set({ wakeUpRetryMinutes: Number(e.target.value) })} /></Field>
          <Field label="Never answered: ring" htmlFor="hn" error={p('notify')} hint="Reception, after the last try.">
            <select id="hn" className="input" value={cur.notify} onChange={(e) => set({ notify: e.target.value })}>
              <option value="">Nobody</option>
              {exts?.items.map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
            </select>
          </Field>
        </div>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={cur.barVacant} onChange={(v) => set({ barVacant: v })} label="Bar vacant rooms" /> Vacant rooms call only extensions and emergency numbers</label>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={cur.checkOutClear} onChange={(v) => set({ checkOutClear: v })} label="Clear on check-out" /> On check-out, delete the room's voicemail and cancel its wake-up call, forwarding and do not disturb</label>
        <div>
          <span className="label">Hotel system (PMS)</span>
          <div className="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Hotel system link">
            {([['', 'None', 'Check guests in and out here.'], ['fias', 'FIAS', 'Oracle Opera and the many systems that speak FIAS, over TCP.'], ['api', 'REST API', 'The hotel system calls BitVoice with an API key.']] as const).map(([id, t, d]) => (
              <button type="button" key={id} role="radio" aria-checked={cur.pms.kind === id} onClick={() => setP({ kind: id })}
                className={clsx('card p-3 text-left', cur.pms.kind === id && 'ring-2 ring-violet-400')}>
                <div className="text-sm font-semibold text-violet-300">{t}</div><div className="text-xs text-slate-500">{d}</div>
              </button>
            ))}
          </div>
          {p('pms.kind') && <p className="text-sm text-rose-300">{p('pms.kind')}</p>}
        </div>
        {cur.pms.kind === 'fias' && (
          <div className="grid gap-3 sm:grid-cols-[1fr_8rem]">
            <Field label="Hotel system address" htmlFor="hph" error={p('pms.host')} hint="BitVoice connects to its FIAS interface."><input id="hph" className="input font-mono" value={cur.pms.host} onChange={(e) => setP({ host: e.target.value.trim() })} placeholder="10.0.0.40" /></Field>
            <Field label="Port" htmlFor="hpp" error={p('pms.port')}><input id="hpp" type="number" className="input" value={cur.pms.port || ''} placeholder="5010" onChange={(e) => setP({ port: Number(e.target.value) })} /></Field>
          </div>
        )}
        {cur.pms.kind === 'api' && (
          <p className="text-sm text-slate-400">The hotel system calls <span className="font-mono text-sky-300">POST /api/hotel/rooms/&lt;room&gt;/checkin</span> and <span className="font-mono text-sky-300">/checkout</span>, and <span className="font-mono text-sky-300">PUT /api/wakeups/&lt;room&gt;</span>, with an API key from Settings, Automation.</p>
        )}
        {cur.pms.kind && (
          <div className="space-y-3">
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={cur.pms.chargeCalls} onChange={(v) => setP({ chargeCalls: v })} label="Charge calls" /> Charge outside calls from occupied rooms to the guest's bill (from the trunk's rates, in Costs)</label>
            {cur.pms.chargeCalls && (
              <div className="grid gap-3 sm:grid-cols-[8rem_1fr]">
                <Field label="Markup (%)" htmlFor="hpm" error={p('pms.markup')}><input id="hpm" type="number" min={0} max={1000} className="input" value={cur.pms.markup || ''} onChange={(e) => setP({ markup: Number(e.target.value) })} /></Field>
                {cur.pms.kind === 'api' && <Field label="Send charges to" htmlFor="hpu" error={p('pms.chargeUrl')} hint="Each call is posted as JSON."><input id="hpu" className="input font-mono" value={cur.pms.chargeUrl} onChange={(e) => setP({ chargeUrl: e.target.value.trim() })} placeholder="https://pms.example/charges" /></Field>}
              </div>
            )}
          </div>
        )}
      </fieldset>
      {admin && <div className="flex justify-end"><button type="button" className="btn btn-primary" disabled={!h} onClick={submit}>Save</button></div>}
    </section>
  )
}
