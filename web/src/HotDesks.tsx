import { LogIn, LogOut, MonitorSmartphone } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useExtensionsQuery, useHotDesksQuery, useHotDeskSignInMutation, useHotDeskSignOutMutation, useSettingsQuery } from './api'
import { attempt, useIsAdmin } from './Editor'
import { relTime } from './ui'

/** HotDesks: shared desk phones, who is signed in at each, and signing people in and out. */
export function HotDesks() {
  const admin = useIsAdmin()
  const { data } = useHotDesksQuery()
  const { data: exts } = useExtensionsQuery()
  const { data: settings } = useSettingsQuery()
  const [signIn] = useHotDeskSignInMutation()
  const [signOut] = useHotDeskSignOutMutation()
  const [pick, setPick] = useState<Record<string, string>>({})
  const codes = settings?.system.hotDesk
  const people = (exts?.items ?? []).filter((x) => x.enabled && !x.hotDesk && !x.kind)
  return (
    <section className="card mb-6 space-y-3 p-5" aria-labelledby="hd-h">
      <h2 id="hd-h" className="flex items-center gap-2 font-semibold text-slate-100"><MonitorSmartphone size={17} className="text-violet-300" aria-hidden /> Hot desks</h2>
      <p className="text-sm text-slate-400">
        A shared desk phone becomes whoever signs in at it: their calls ring there, and calls from it are theirs. At the phone, dial
        <span className="font-mono text-violet-300"> {codes?.login || '*33'}</span>, then your extension and #, then your voicemail PIN and #.
        <span className="font-mono text-violet-300"> {codes?.logout || '*34'}</span> signs out{codes?.signOutAt ? <>, and everyone is signed out at <span className="text-amber-300">{codes.signOutAt}</span> each day</> : ''}.
        Emergency calls still carry the desk's own location. Make an extension a hot desk in <Link to="/extensions" className="text-sky-300 underline">Extensions</Link>.
      </p>
      {!data?.items.length ? <p className="text-sm text-slate-500">No hot desks yet.</p> : (
        <ul className="divide-y divide-slate-800" aria-label="Hot desks">
          {data.items.map((d) => (
            <li key={d.number} className="flex flex-wrap items-center gap-3 py-2 text-sm">
              <span className="w-40 text-slate-200"><span className="font-mono text-sky-300">{d.number}</span> {d.name}</span>
              {d.in ? (
                <span className="flex-1 text-emerald-300">{d.in.user} {d.in.userName} <span className="text-xs text-slate-500">since {relTime(d.in.since)}</span></span>
              ) : <span className="flex-1 text-slate-500">Free</span>}
              {admin && (d.in ? (
                <button className="btn" onClick={() => attempt(() => signOut(d.number).unwrap(), `${d.in!.userName} signed out of ${d.name}`)}><LogOut size={14} className="text-rose-300" aria-hidden /> Sign out</button>
              ) : (
                <span className="flex gap-2">
                  <select className="input w-48" aria-label={`Sign someone in at ${d.name}`} value={pick[d.number] ?? ''} onChange={(e) => setPick({ ...pick, [d.number]: e.target.value })}>
                    <option value="">Choose someone</option>
                    {people.map((x) => <option key={x.number} value={x.number}>{x.number} {x.name}</option>)}
                  </select>
                  <button className="btn" disabled={!pick[d.number]} onClick={() => attempt(() => signIn({ desk: d.number, user: pick[d.number] }).unwrap(), 'Signed in')}><LogIn size={14} className="text-emerald-300" aria-hidden /> Sign in</button>
                </span>
              ))}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
