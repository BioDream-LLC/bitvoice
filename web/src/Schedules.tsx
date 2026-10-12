import { CalendarClock, CalendarX, Moon, Plus, Sun, Trash2 } from 'lucide-react'
import { useState } from 'react'
import {
  useCreateScheduleMutation, useDeleteScheduleMutation, useSchedulesQuery, useSettingsQuery, useUpdateScheduleMutation,
  type ApiErrorBody, type Schedule,
} from './api'
import { useOpenParam } from './useOpenParam'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { DestinationPicker } from './Extensions'
import { Badge, Empty, Field, Page, problemFor } from './ui'
import { useNow } from './useNow'

const dayNames = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

/** The time in a zone as weekday, YYYY-MM-DD and HH:MM, the way the server compares them. */
function partsIn(tz: string | undefined, now: number) {
  const f = new Intl.DateTimeFormat('en-CA', { timeZone: tz || 'UTC', weekday: 'short', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
  const p = Object.fromEntries(f.formatToParts(new Date(now)).map((x) => [x.type, x.value]))
  return { day: dayNames.indexOf(p.weekday), date: `${p.year}-${p.month}-${p.day}`, time: `${p.hour === '24' ? '00' : p.hour}:${p.minute}` }
}

export function isOpen(s: Schedule, tz: string | undefined, now: number) {
  const { day, date, time } = partsIn(tz, now)
  if (s.holidays.some((h) => h.date === date)) return false
  const yesterday = (day + 6) % 7
  return s.hours.some((h) => h.end > h.start
    ? h.days.includes(day) && time >= h.start && time < h.end
    // Overnight hours (22:00 to 06:00) run from the listed day into the next morning.
    : (h.days.includes(day) && time >= h.start) || (h.days.includes(yesterday) && time < h.end))
}

const blank: Schedule = {
  id: '', name: 'Office hours', hours: [{ days: [1, 2, 3, 4, 5], start: '09:00', end: '17:00' }], holidays: [],
  open: { type: 'hangup', target: '' }, closed: { type: 'hangup', target: '' },
}

export function Schedules() {
  const admin = useIsAdmin()
  const { data } = useSchedulesQuery()
  const { data: settings } = useSettingsQuery()
  const tz = settings?.system.timezone
  const now = useNow()
  const [edit, setEdit] = useState<Schedule | null>(null)
  useOpenParam(data?.items, setEdit)
  return (
    <Page icon={CalendarClock} title="Schedules" accent="from-amber-400 to-sky-400"
      intro={<>Opening hours and holidays. Use a schedule as a call destination to send calls one way when open and another when closed. Times are in <span className="text-sky-300">{tz || 'UTC'}</span>.</>}
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit(blank)}><Plus size={15} aria-hidden /> Add schedule</button>}>
      {!data?.items.length ? <Empty icon={CalendarClock} title="No schedules yet">For example: weekdays 9 to 5 ring the front desk, otherwise voicemail.</Empty> : (
        <div className="grid gap-4 lg:grid-cols-2">
          {data.items.map((s) => {
            const open = isOpen(s, tz, now)
            return (
              <button key={s.id} className="card p-5 text-left hover:border-slate-700" onClick={() => setEdit(s)}>
                <div className="flex items-center gap-3">
                  {open ? <Sun size={18} className="text-amber-300" aria-hidden /> : <Moon size={18} className="text-sky-300" aria-hidden />}
                  <h2 className="font-semibold text-slate-100">{s.name}</h2>
                  <span className="ml-auto"><Badge status={open ? 'ok' : 'idle'}>{open ? 'Open now' : 'Closed now'}</Badge></span>
                </div>
                <Week s={s} />
                {s.holidays.length > 0 && <p className="mt-2 text-xs text-slate-500"><CalendarX size={12} className="mr-1 inline" aria-hidden />{s.holidays.length} holiday{s.holidays.length > 1 ? 's' : ''}</p>}
              </button>
            )
          })}
        </div>
      )}
      {edit && <ScheduleEditor s={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

/** A week strip: one bar per day, open hours filled. */
function Week({ s }: { s: Schedule }) {
  const toMin = (t: string) => Number(t.slice(0, 2)) * 60 + Number(t.slice(3))
  return (
    <div className="mt-4 space-y-1" aria-label="Opening hours">
      {[1, 2, 3, 4, 5, 6, 0].map((d) => (
        <div key={d} className="flex items-center gap-2 text-xs">
          <span className="w-8 text-slate-500">{dayNames[d]}</span>
          <div className="relative h-2.5 flex-1 rounded bg-slate-800">
            {s.hours.flatMap((h) => {
              // Each range as [start, end] minutes on day d; overnight hours spill into the next day.
              const a = toMin(h.start), b = toMin(h.end)
              if (b > a) return h.days.includes(d) ? [[a, b]] : []
              return [...(h.days.includes(d) ? [[a, 1440]] : []), ...(h.days.includes((d + 6) % 7) ? [[0, b]] : [])]
            }).map(([a, b], i) => (
              <span key={i} className="absolute inset-y-0 rounded bg-amber-400/80" style={{ left: `${a / 14.4}%`, width: `${(b - a) / 14.4}%` }} />
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

function ScheduleEditor({ s: initial, onClose }: { s: Schedule; onClose: () => void }) {
  const admin = useIsAdmin()
  const [s, setS] = useState(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [create] = useCreateScheduleMutation()
  const [update] = useUpdateScheduleMutation()
  const [remove] = useDeleteScheduleMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const setHours = (i: number, v: Partial<Schedule['hours'][number]>) => setS((s0) => ({ ...s0, hours: s0.hours.map((h, j) => j === i ? { ...h, ...v } : h) }))
  const save = async () => {
    const e = await attempt(() => (isNew ? create(s) : update(s)).unwrap(), `Schedule ${s.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New schedule' : s.name} icon={<CalendarClock className="text-amber-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Schedule removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <Field label="Name" htmlFor="sn" error={p('name')}><input id="sn" className="input" value={s.name} onChange={(e) => { const v = e.target.value; setS((s0) => ({ ...s0, name: v })) }} /></Field>
        <div className="space-y-2">
          <span className="label">Open</span>
          {s.hours.map((h, i) => (
            <div key={i} className="card flex flex-wrap items-center gap-2 p-3">
              <div className="flex gap-1" role="group" aria-label={`Days for opening hours ${i + 1}`}>
                {[1, 2, 3, 4, 5, 6, 0].map((d) => (
                  <button type="button" key={d} aria-pressed={h.days.includes(d)}
                    onClick={() => setHours(i, { days: h.days.includes(d) ? h.days.filter((x) => x !== d) : [...h.days, d] })}
                    className={`w-10 rounded py-1 text-xs ${h.days.includes(d) ? 'bg-amber-400 font-semibold text-slate-950' : 'bg-slate-800 text-slate-400'}`}>{dayNames[d]}</button>
                ))}
              </div>
              <input type="time" className="input w-28" aria-label="Opens at" value={h.start} onChange={(e) => setHours(i, { start: e.target.value })} />
              <span className="text-slate-500">to</span>
              <input type="time" className="input w-28" aria-label="Closes at" value={h.end === '24:00' ? '23:59' : h.end} onChange={(e) => setHours(i, { end: e.target.value })} />
              <button type="button" className="btn ml-auto px-2" aria-label="Remove these hours" onClick={() => setS({ ...s, hours: s.hours.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
              {h.end < h.start && h.end !== '24:00' && <p className="w-full text-xs text-sky-300">Overnight: open until {h.end} the next morning.</p>}
              {p(`hours[${i}]`) && <p className="w-full text-xs text-rose-300">{p(`hours[${i}]`)}</p>}
            </div>
          ))}
          <button type="button" className="btn" onClick={() => setS({ ...s, hours: [...s.hours, { days: [6], start: '10:00', end: '14:00' }] })}><Plus size={14} className="text-amber-300" aria-hidden /> Add hours</button>
        </div>
        <div className="space-y-2">
          <span className="label">Holidays (closed all day)</span>
          {s.holidays.map((h, i) => (
            <div key={i} className="flex items-center gap-2">
              <input type="date" className="input w-44" aria-label="Holiday date" value={h.date} onChange={(e) => { const v = e.target.value; setS((s0) => ({ ...s0, holidays: s0.holidays.map((x, j) => j === i ? { ...x, date: v } : x) })) }} />
              <input className="input" aria-label="Holiday name" placeholder="New Year's Day" value={h.name} onChange={(e) => { const v = e.target.value; setS((s0) => ({ ...s0, holidays: s0.holidays.map((x, j) => j === i ? { ...x, name: v } : x) })) }} />
              <button type="button" className="btn px-2" aria-label="Remove holiday" onClick={() => setS((s0) => ({ ...s0, holidays: s0.holidays.filter((_, j) => j !== i) }))}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
              {p(`holidays[${i}].date`) && <p className="text-xs text-rose-300">{p(`holidays[${i}].date`)}</p>}
            </div>
          ))}
          <button type="button" className="btn" onClick={() => setS({ ...s, holidays: [...s.holidays, { date: new Date().toLocaleDateString('en-CA'), name: '' }] })}><Plus size={14} className="text-sky-300" aria-hidden /> Add holiday</button>
        </div>
        <DestinationPicker noSchedules label="When open, calls go to" required value={s.open} onChange={(d) => d && setS((s0) => ({ ...s0, open: d }))} error={p('open')} />
        <DestinationPicker noSchedules label="When closed, calls go to" required value={s.closed} onChange={(d) => d && setS((s0) => ({ ...s0, closed: d }))} error={p('closed')} />
      </fieldset>
    </Drawer>
  )
}
