import { clsx } from 'clsx'
import { Crown, Hand, Lock, LockOpen, Mic, MicOff, PhoneOff, Plus, Radio, Users2, Video } from 'lucide-react'
import { useState } from 'react'
import {
  useConferenceLiveQuery, useConferencesQuery, useCreateConferenceMutation, useDeleteConferenceMutation, useLockConferenceMutation,
  useMuteConferenceMemberMutation, useRemoveConferenceMemberMutation, useSystemQuery, useUpdateConferenceMutation,
  type ApiErrorBody, type Conference, type ConferenceRoom,
} from './api'
import { attempt, DeleteButton, Drawer, useIsAdmin } from './Editor'
import { nextFree, useTakenNumbers } from './numbers'
import { Empty, Field, Page, Toggle, problemFor, relTime, freshName } from './ui'

/** Conference rooms: numbers people dial to talk together, and the rooms in use now. */
export function Conferences() {
  const admin = useIsAdmin()
  const { data } = useConferencesQuery()
  const { data: live } = useConferenceLiveQuery(undefined, { pollingInterval: 2000 })
  const [edit, setEdit] = useState<Conference | null>(null)
  const taken = useTakenNumbers()
  const add = () => setEdit({ id: '', number: nextFree(taken, 500), name: freshName('Team room', data?.items.map((x) => x.name)), pin: '', leaderPin: '', waitForLeader: false, muteMembers: false, maxMembers: 50, record: false, quiet: false, hd: true })
  const inUse = new Map((live?.items ?? []).map((r) => [r.number, r]))
  return (
    <Page icon={Users2} title="Conference rooms" accent="from-teal-400 to-sky-400"
      intro="A number people dial to all talk together: staff from their desks, and outside callers through an incoming route or a menu. Give it a PIN to keep it private, and a leader PIN for briefings."
      actions={admin && <button className="btn btn-primary" onClick={add}><Plus size={15} aria-hidden /> Add conference room</button>}>
      {(live?.items.length ?? 0) > 0 && (
        <section className="mb-6 space-y-3" aria-labelledby="live-h">
          <h2 id="live-h" className="flex items-center gap-2 text-sm font-semibold text-emerald-300"><Radio size={15} aria-hidden /> In use now</h2>
          {live!.items.map((r) => <LiveRoom key={r.number} r={r} />)}
        </section>
      )}
      {!data?.items.length ? <Empty icon={Users2} title="No conference rooms yet">For example: the team's daily call on 500, with a PIN.</Empty> : (
        <div className="grid gap-3 md:grid-cols-2">
          {data.items.map((c) => {
            const r = inUse.get(c.number)
            return (
              <div key={c.id} className="flex flex-col gap-1">
              <button className="card p-4 text-left hover:border-slate-700" onClick={() => setEdit(c)}>
                <div className="flex items-center gap-2">
                  <Users2 size={16} className="text-teal-300" aria-hidden />
                  <span className="font-mono text-teal-300">{c.number}</span>
                  <span className="font-semibold text-slate-100">{c.name}</span>
                  {r && <span className="ml-auto rounded-full bg-emerald-950 px-2 py-0.5 text-xs text-emerald-300">{(r.members ?? []).length} in now</span>}
                </div>
                <div className="mt-2 flex flex-wrap gap-2 text-xs">
                  {c.pin ? <span className="text-amber-300">PIN</span> : <span className="text-slate-500">No PIN</span>}
                  {c.leaderPin && <span className="text-violet-300">Leader PIN</span>}
                  {c.waitForLeader && <span className="text-violet-300">Waits for the leader</span>}
                  {c.muteMembers && <span className="text-sky-300">Members muted</span>}
                  {c.record && <span className="text-rose-300">Recorded</span>}
                  {c.hd && <span className="text-teal-300">HD</span>}
                  {c.video && <span className="flex items-center gap-1 text-fuchsia-300"><Video size={12} aria-hidden /> {c.video === 'sfu' ? "everyone's video" : 'video'}</span>}
                  <span className="text-slate-500">Up to {c.maxMembers}</span>
                </div>
              </button>
              {c.video === 'sfu' && <a className="btn justify-center text-fuchsia-200" href={`#/video-room/${c.number}`}><Video size={14} aria-hidden /> Open the video room</a>}
              </div>
            )
          })}
        </div>
      )}
      <section className="card mt-6 space-y-2 p-4 text-sm text-slate-400" aria-labelledby="keys-h">
        <h2 id="keys-h" className="font-semibold text-slate-200">Keys in a room</h2>
        <p><kbd className="font-mono text-teal-300">1</kbd> mutes and unmutes yourself (in a room where members are muted, it asks to speak). A leader's <kbd className="font-mono text-violet-300">2</kbd> locks the room so nobody new can join, and unlocks it.</p>
      </section>
      {edit && <ConferenceEditor c={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function LiveRoom({ r }: { r: ConferenceRoom }) {
  const admin = useIsAdmin()
  const [lock] = useLockConferenceMutation()
  const [mute] = useMuteConferenceMemberMutation()
  const [remove] = useRemoveConferenceMemberMutation()
  const members = r.members ?? []
  return (
    <div className="card border-emerald-900/60 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-teal-300">{r.number}</span>
        <span className="font-semibold text-slate-100">{r.name}</span>
        <span className="text-xs text-slate-500">since {relTime(r.started)}</span>
        {r.records && <span className="rounded-full bg-rose-950 px-2 py-0.5 text-xs text-rose-300">Recording</span>}
        {r.hd && <span className="rounded-full bg-teal-950 px-2 py-0.5 text-xs text-teal-300">HD</span>}
        {r.video && <span className="flex items-center gap-1 rounded-full bg-fuchsia-950 px-2 py-0.5 text-xs text-fuchsia-300"><Video size={12} aria-hidden /> {r.video === 'leader' ? 'showing the leader' : r.video === 'grid' ? `grid of ${r.grid?.length ?? 0}` : 'following the speaker'}</span>}
        {r.locked && <span className="rounded-full bg-amber-950 px-2 py-0.5 text-xs text-amber-300">Locked</span>}
        {admin && (
          <button type="button" className="btn btn-ghost ml-auto text-sm" onClick={() => attempt(() => lock({ number: r.number, locked: !r.locked }).unwrap(), r.locked ? 'Room unlocked' : 'Room locked')}>
            {r.locked ? <><LockOpen size={14} aria-hidden /> Unlock</> : <><Lock size={14} aria-hidden /> Lock</>}
          </button>
        )}
      </div>
      <ul className="mt-3 divide-y divide-slate-800" aria-label={`People in ${r.name}`}>
        {members.map((m) => (
          <li key={m.id} className="flex items-center gap-3 py-2 text-sm">
            <span aria-hidden className={clsx('h-2.5 w-2.5 rounded-full', m.speaking ? 'bg-emerald-400 shadow-[0_0_8px] shadow-emerald-400' : m.waiting ? 'bg-amber-500' : 'bg-slate-700')} />
            <span className="font-mono text-sky-300">{m.number}</span>
            <span className="text-slate-200">{m.name}</span>
            {m.leader && <span className="flex items-center gap-1 text-xs text-violet-300"><Crown size={12} aria-hidden /> leader</span>}
            {m.hd && <span className="text-xs text-teal-300" title="On a wideband codec: hears and is heard in HD">HD</span>}
            {m.video && (() => {
              const on = r.video === 'grid' ? !!r.grid?.includes(m.id) : r.showing === m.id
              return <span className={clsx('flex items-center gap-1 text-xs', on ? 'text-fuchsia-300' : 'text-slate-500')} title={on ? (r.video === 'grid' ? 'In the grid' : 'Everyone sees this picture') : 'Sends and receives video'}><Video size={12} aria-hidden />{on ? (r.video === 'grid' ? ' in the grid' : ' on screen') : ''}</span>
            })()}
            {m.waiting && <span className="text-xs text-amber-300">waiting for the leader</span>}
            {m.hand && <span className="flex items-center gap-1 text-xs text-amber-300"><Hand size={12} aria-hidden /> asks to speak</span>}
            <span className="ml-auto text-xs text-slate-500">{relTime(m.joined)}</span>
            {admin && !m.waiting && (
              <button type="button" className="btn btn-ghost px-2" aria-label={m.muted ? `Unmute ${m.name}` : `Mute ${m.name}`}
                onClick={() => attempt(() => mute({ number: r.number, id: m.id, muted: !m.muted }).unwrap(), m.muted ? `${m.name} unmuted` : `${m.name} muted`)}>
                {m.muted ? <MicOff size={14} className="text-rose-300" aria-hidden /> : <Mic size={14} className="text-emerald-300" aria-hidden />}
              </button>
            )}
            {admin && (
              <button type="button" className="btn btn-ghost px-2" aria-label={`Remove ${m.name}`}
                onClick={() => attempt(() => remove({ number: r.number, id: m.id }).unwrap(), `${m.name} removed`)}>
                <PhoneOff size={14} className="text-rose-300" aria-hidden />
              </button>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

function ConferenceEditor({ c: initial, onClose }: { c: Conference; onClose: () => void }) {
  const admin = useIsAdmin()
  const [c, setC] = useState(initial)
  const { data: sys } = useSystemQuery()
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [create] = useCreateConferenceMutation()
  const [update] = useUpdateConferenceMutation()
  const [remove] = useDeleteConferenceMutation()
  const isNew = !initial.id
  const p = (f: string) => problemFor(err?.problems, f)
  const set = <K extends keyof Conference>(k: K, v: Conference[K]) => setC((c0) => ({ ...c0, [k]: v }))
  const save = async () => {
    const e = await attempt(() => (isNew ? create(c) : update(c)).unwrap(), `Conference room ${c.name} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New conference room' : c.name} icon={<Users2 className="text-teal-300" aria-hidden />}
      onSave={admin ? save : undefined} error={err}
      footer={admin && !isNew && <DeleteButton what={initial.name} onDelete={async () => { if (!await attempt(() => remove(initial.id).unwrap(), 'Conference room removed', { problemsInToast: true })) onClose() }} />}>
      <fieldset disabled={!admin} className="space-y-5">
        <div className="grid grid-cols-[8rem_1fr] gap-4">
          <Field label="Number" htmlFor="cn" error={p('number')}><input id="cn" className="input font-mono text-teal-300" value={c.number} onChange={(e) => set('number', e.target.value.replace(/\D/g, ''))} /></Field>
          <Field label="Name" htmlFor="cm" error={p('name')}><input id="cm" className="input" value={c.name} onChange={(e) => set('name', e.target.value)} /></Field>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="PIN" htmlFor="cp" error={p('pin')} hint="Asked before joining. Empty: anyone who dials the number joins.">
            <input id="cp" inputMode="numeric" autoComplete="off" className="input font-mono text-amber-300" value={c.pin} onChange={(e) => set('pin', e.target.value.replace(/[^0-9•]/g, ''))} />
          </Field>
          <Field label="Leader PIN" htmlFor="clp" error={p('leaderPin')} hint="Optional. Joins as a leader, who can lock the room.">
            <input id="clp" inputMode="numeric" autoComplete="off" className="input font-mono text-violet-300" value={c.leaderPin} onChange={(e) => set('leaderPin', e.target.value.replace(/[^0-9•]/g, ''))} />
          </Field>
        </div>
        <div className="space-y-3">
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={c.waitForLeader} onChange={(v) => set('waitForLeader', v)} label="Wait for the leader" /> Wait for the leader <span className="text-xs text-slate-500">(others hear the hold tone until a leader joins; the room closes when the last leader leaves)</span></label>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={c.muteMembers} onChange={(v) => set('muteMembers', v)} label="Members join muted" /> Members join muted <span className="text-xs text-slate-500">(a briefing: they press 1 to ask to speak)</span></label>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={c.record} onChange={(v) => set('record', v)} label="Record the room" /> Record the room <span className="text-xs text-slate-500">(one recording in Call history)</span></label>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!!c.hd} onChange={(v) => set('hd', v)} label="HD audio" /> HD audio <span className="text-xs text-slate-500">(mixed at 16 kHz: wideband phones and the browser phone hear each other in HD; the others hear the room as usual)</span></label>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={!c.quiet} onChange={(v) => set('quiet', !v)} label="Tones when people join and leave" /> Tones when people join and leave</label>
        </div>
        <div className="grid gap-3 sm:grid-cols-[1fr_9rem]">
          <Field label="Video" htmlFor="cvid" error={p('video')} hint={c.video === 'grid' ? 'Everyone sees everyone, up to nine at once, made by ffmpeg on this server (Settings, video conversion). Each member costs some processor time.' : c.video === 'sfu' ? "Browsers open the room's page and see every camera, each in its own picture, in the size they show it (up to 16 cameras); phones in the room hear it and video phones see whoever speaks." : "Video phones see one picture, switched as people talk. Nothing is decoded, so every member's video uses one codec."}>
            <select id="cvid" className="input" value={c.video ?? ''} onChange={(e) => set('video', e.target.value as Conference['video'])}>
              <option value="">Sound only</option>
              <option value="talker">Follow the speaker (the speaker sees the one before)</option>
              <option value="leader">Show the leader (the leader sees whoever speaks)</option>
              <option value="grid">Grid: everyone sees everyone (needs ffmpeg)</option>
              <option value="sfu">Everyone's video: each camera on its own, in the room's page (browsers)</option>
            </select>
          </Field>
          {c.video === 'grid' && sys && !sys.ffmpeg && <p className="text-xs text-amber-300 sm:col-span-2">ffmpeg is not installed on this server: until it is, the room follows the speaker instead.</p>}
          {c.video && (
            <Field label="Video codec" htmlFor="cvc" error={p('videoCodec')}>
              <select id="cvc" className="input" value={c.videoCodec || 'H264'} onChange={(e) => set('videoCodec', e.target.value)}>
                <option value="H264">H.264</option>
                <option value="VP8">VP8</option>
              </select>
            </Field>
          )}
        </div>
        <Field label="Most people at once" htmlFor="cmax" error={p('maxMembers')}>
          <input id="cmax" type="number" min={2} max={200} className="input w-32" value={c.maxMembers} onChange={(e) => set('maxMembers', Number(e.target.value))} />
        </Field>
      </fieldset>
    </Drawer>
  )
}
