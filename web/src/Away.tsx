import { BellOff, CalendarCheck } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useAwayQuery, useCheckCalendarMutation, useSetAwayMutation, useSettingsQuery, useWorkspaceStatusQuery, type Extension } from './api'
import { providerName } from './Workspace'
import { Field, Toggle, relTime } from './ui'

/** AwayBadge shows that an extension is not ringing now, and why. */
export function AwayBadge({ ext }: { ext: string }) {
  const { data } = useAwayQuery()
  const a = data?.items.find((x) => x.ext === ext)
  if (!a) return null
  return (
    <span className="inline-flex items-center gap-1 rounded bg-violet-500/15 px-1.5 text-xs text-violet-200" title={a.until ? `until ${new Date(a.until).toLocaleString()}` : 'until switched off'}>
      <BellOff size={11} aria-hidden />{a.reason}{a.until && <span className="text-violet-300/70"> · ends {relTime(a.until)}</span>}
    </span>
  )
}

/** CalendarFields: the extension's calendar, what happens while busy, and do not disturb now. */
export function CalendarFields({ x, set, p, isNew }: { x: Extension; set: (c: Extension['calendar']) => void; p: (f: string) => string | undefined; isNew: boolean }) {
  const c = x.calendar ?? { secret: '', whenBusy: 'voicemail', forwardTo: '', ignoreMeetings: false }
  const [check, { isLoading }] = useCheckCalendarMutation()
  const [setAway] = useSetAwayMutation()
  const { data } = useAwayQuery()
  const away = data?.items.find((a) => a.ext === x.number)
  const problem = data?.problems?.[x.number]
  const [found, setFound] = useState<string | null>(null)
  const { data: settings } = useSettingsQuery()
  const ws = settings?.system.workspace
  const fromService = !!ws?.provider && ws.calendars && !!x.email && !c.secret
  const { data: wsStatus } = useWorkspaceStatusQuery(undefined, { skip: !fromService })
  const tryIt = async () => {
    try {
      const r = await check({ secret: c.secret, ignoreMeetings: c.ignoreMeetings }).unwrap()
      setFound(r.next.length ? `Read ${r.events} busy times this week. Next: ${r.next.map((e) => `${e.summary || (e.away ? 'time off' : 'busy')} ${new Date(e.start).toLocaleString([], { weekday: 'short', hour: '2-digit', minute: '2-digit' })}`).join('; ')}` : `Read the calendar: nothing busy in the next seven days.`)
    } catch (e) { setFound(null); toast.error(errorOf(e).message) }
  }
  return (
    <fieldset className="space-y-3 rounded-lg border border-slate-800 p-3">
      <legend className="flex items-center gap-1.5 px-1 text-sm font-medium text-violet-300"><CalendarCheck size={14} aria-hidden /> Calendar and do not disturb</legend>
      {!isNew && (
        <label className="flex items-center gap-3 text-sm text-slate-300">
          <Toggle checked={away?.source === 'manual'} onChange={(on) => setAway({ ext: x.number, on }).unwrap().catch((e) => toast.error(errorOf(e).message))} label="Do not disturb" />
          <span>Do not disturb now {away && <span className="ml-1"><AwayBadge ext={x.number} /></span>}</span>
        </label>
      )}
      <Field label="Calendar's private address" htmlFor="xcal" error={p('calendar.secret')}
        hint={<>Outlook: Settings, Calendar, Shared calendars, Publish a calendar, the ICS link. Google: Settings, the calendar, Secret address in iCal format. It works like a password: anyone with it can read the calendar.</>}>
        <div className="flex gap-2">
          <input id="xcal" className="input font-mono" type="password" autoComplete="off" placeholder="https://…/calendar.ics" value={c.secret} onChange={(e) => set({ ...c, secret: e.target.value.trim() })} />
          <button type="button" className="btn" disabled={!c.secret || isLoading} onClick={tryIt}>Check</button>
        </div>
      </Field>
      {fromService && <p className="text-sm text-sky-200">Read from {providerName(ws?.provider)}, as {x.email}{wsStatus?.problems?.[x.number] ? <span className="text-amber-300">: {wsStatus.problems[x.number]}</span> : ''}. A private address here is used instead.</p>}
      {found && <p role="status" className="text-sm text-emerald-300">{found}</p>}
      {problem && <p className="text-sm text-amber-300">{problem}</p>}
      <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!c.ignoreMeetings} onChange={(v) => set({ ...c, ignoreMeetings: !v })} label="Meetings count as busy" /> Meetings count as busy <span className="text-xs text-slate-500">(time off always does; events marked free never do)</span></label>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="While busy, calls" htmlFor="xcb" error={p('calendar.whenBusy')}>
          <select id="xcb" className="input" value={c.whenBusy || 'voicemail'} onChange={(e) => set({ ...c, whenBusy: e.target.value as 'voicemail' })}>
            <option value="voicemail">Go to voicemail</option><option value="forward">Are forwarded</option><option value="ring">Ring as usual</option>
          </select>
        </Field>
        {c.whenBusy === 'forward' && (
          <Field label="Forward to" htmlFor="xcf" error={p('calendar.forwardTo')} hint="An extension, or a phone number such as a mobile.">
            <input id="xcf" className="input font-mono" inputMode="tel" value={c.forwardTo} onChange={(e) => set({ ...c, forwardTo: e.target.value })} />
          </Field>
        )}
      </div>
    </fieldset>
  )
}
