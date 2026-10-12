import { Lock, LockOpen, Plus, Scale } from 'lucide-react'
import { useState } from 'react'
import { useAddHoldMutation, useHoldsQuery, useReleaseHoldMutation, useSettingsQuery, useUpdateHoldMutation, type ApiErrorBody, type Hold } from './api'
import { attempt, Drawer, useIsAdmin } from './Editor'
import { Badge, Empty, Field, ListInput, Page, problemFor, relTime } from './ui'

/** day gives the calendar date of an instant in the system's time zone, as the hold was entered. */
const day = (iso?: string, tz?: string) => {
  if (!iso) return ''
  try { return new Intl.DateTimeFormat('en-CA', { timeZone: tz || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(iso)) } catch { return iso.slice(0, 10) }
}

/** Legal holds: what they cover is kept whatever the retention settings say, and cannot be deleted. */
export function Holds() {
  const admin = useIsAdmin()
  const { data } = useHoldsQuery()
  const { data: settings } = useSettingsQuery()
  const [release] = useReleaseHoldMutation()
  const [edit, setEdit] = useState<Hold | null>(null)
  const active = data?.items.filter((h) => !h.released) ?? []
  const done = data?.items.filter((h) => h.released) ?? []
  const r = settings?.system.retention
  const keep = (d?: number) => (d ? `${d} days` : 'for ever')
  return (
    <Page icon={Scale} title="Legal holds" accent="from-amber-300 to-rose-300"
      intro="A hold keeps call records, recordings and voicemail for the people and dates it covers, whatever the retention settings say, and stops anyone deleting them until it is released."
      actions={admin && <button className="btn btn-primary" onClick={() => setEdit({ id: '', name: '', reason: '', numbers: [], created: '', by: '' })}><Plus size={15} aria-hidden /> Add hold</button>}>
      <p className="mb-4 text-sm text-slate-400">
        Kept now: recordings {keep(settings?.system.recording.keepDays)}, call history {keep(r?.callRecordDays)}, voicemail {keep(r?.voicemailDays)} (change in Settings, Call recording).
      </p>
      {active.length === 0 && done.length === 0 ? <Empty icon={Scale} title="No holds">Add one when a matter needs records kept.</Empty> : (
        <div className="space-y-6">
          <ul className="grid gap-3 md:grid-cols-2" aria-label="Active holds">
            {active.map((h) => (
              <li key={h.id} className="card space-y-2 border-amber-900/60 p-4">
                <div className="flex items-center gap-2">
                  <Lock size={16} className="text-amber-300" aria-hidden />
                  <button className="flex-1 text-left font-semibold text-slate-100 hover:underline disabled:no-underline" disabled={!admin} onClick={() => setEdit(h)}>{h.name}</button>
                  <Badge status="warn">Active</Badge>
                </div>
                <Covers h={h} />
                {h.reason && <p className="text-sm text-slate-400">{h.reason}</p>}
                <div className="flex items-center justify-between text-xs text-slate-500">
                  <span>by {h.by}, {relTime(h.created)}</span>
                  {admin && <button className="btn text-xs" onClick={() => { if (confirm(`Release "${h.name}"? What it kept becomes subject to the retention settings again.`)) attempt(() => release(h.id).unwrap(), 'Hold released') }}><LockOpen size={13} aria-hidden /> Release</button>}
                </div>
              </li>
            ))}
          </ul>
          {done.length > 0 && (
            <section aria-labelledby="rel-h">
              <h2 id="rel-h" className="mb-2 text-sm font-medium text-slate-400">Released</h2>
              <ul className="space-y-1 text-sm">
                {done.map((h) => <li key={h.id} className="flex gap-3 text-slate-400"><LockOpen size={14} className="mt-0.5 text-slate-500" aria-hidden /><span className="text-slate-300">{h.name}</span><span>released by {h.releasedBy} {relTime(h.released)}</span></li>)}
              </ul>
            </section>
          )}
        </div>
      )}
      {edit && <HoldEditor hold={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function Covers({ h }: { h: Hold }) {
  const tz = useSettingsQuery().data?.system.timezone
  return (
    <p className="text-sm text-slate-300">
      {h.numbers.length ? <>Calls and voicemail of <span className="font-mono text-sky-300">{h.numbers.join(', ')}</span></> : <span className="text-amber-300">Everything</span>}
      {h.since && h.until ? <> from <span className="text-violet-300">{day(h.since, tz)}</span> to <span className="text-violet-300">{day(h.until, tz)}</span></>
        : h.since ? <> from <span className="text-violet-300">{day(h.since, tz)}</span> onwards</>
          : h.until ? <> up to <span className="text-violet-300">{day(h.until, tz)}</span></> : <>, any date</>}
    </p>
  )
}

function HoldEditor({ hold, onClose }: { hold: Hold; onClose: () => void }) {
  const tz = useSettingsQuery().data?.system.timezone
  const [add, { isLoading: a }] = useAddHoldMutation()
  const [update, { isLoading: u }] = useUpdateHoldMutation()
  const [h, setH] = useState({ name: hold.name, reason: hold.reason, numbers: hold.numbers, since: day(hold.since, tz), until: day(hold.until, tz) })
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const p = (f: string) => problemFor(err?.problems, f)
  const save = async () => {
    const r = await attempt(() => (hold.id ? update({ id: hold.id, ...h }) : add(h)).unwrap(), hold.id ? 'Hold changed' : 'Hold added')
    if (r) setErr(r)
    else onClose()
  }
  return (
    <Drawer open onClose={onClose} title={hold.id ? hold.name : 'New legal hold'} icon={<Scale size={18} className="text-amber-300" aria-hidden />} onSave={save} saving={a || u} error={err}>
      <Field label="Name" htmlFor="hn" error={p('name')} hint="The matter, case or request number.">
        <input id="hn" className="input" autoFocus value={h.name} onChange={(e) => { const v = e.target.value; setH((h0) => ({ ...h0, name: v })) }} />
      </Field>
      <Field label="Numbers" htmlFor="hnum" error={p('numbers')} hint="Extensions or phone numbers, on either side of a call, or mailboxes. Empty holds everything.">
        <ListInput id="hnum" mono value={h.numbers} onChange={(v) => setH((h0) => ({ ...h0, numbers: v }))} placeholder="101, +15551234567" />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="From" htmlFor="hs" error={p('since')}><input id="hs" type="date" className="input" value={h.since} onChange={(e) => { const v = e.target.value; setH((h0) => ({ ...h0, since: v })) }} /></Field>
        <Field label="To" htmlFor="hu" error={p('until')} hint="Empty: and from now on."><input id="hu" type="date" className="input" value={h.until} onChange={(e) => { const v = e.target.value; setH((h0) => ({ ...h0, until: v })) }} /></Field>
      </div>
      <Field label="Reason" htmlFor="hr"><textarea id="hr" rows={3} className="input" value={h.reason} onChange={(e) => { const v = e.target.value; setH((h0) => ({ ...h0, reason: v })) }} /></Field>
    </Drawer>
  )
}
