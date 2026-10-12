import { Check, Download, Pause, Play, Sparkles, Voicemail as VoicemailIcon } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import WaveSurfer from 'wavesurfer.js'
import { useDeleteVoicemailMutation, useExtensionsQuery, useMarkHeardMutation, useVoicemailQuery, type Voicemail } from './api'
import { attempt, DeleteButton, useIsAdmin } from './Editor'
import { Badge, Chip, Empty, Page, formatDuration, relTime } from './ui'

export function VoicemailPage() {
  const { data } = useVoicemailQuery()
  const { data: exts } = useExtensionsQuery()
  const [box, setBox] = useState('')
  const items = useMemo(() => (data?.items ?? []).filter((v) => !box || v.box === box), [data, box])
  const fresh = items.filter((v) => !v.heard).length
  const nameOf = (n: string) => exts?.items.find((x) => x.number === n)?.name ?? ''
  return (
    <Page icon={VoicemailIcon} title="Voicemail" accent="from-violet-400 to-sky-400"
      intro={<>Messages callers left. Phones show a waiting light; dial <span className="font-mono text-violet-300">*97</span> from an extension to hear its messages.</>}
      actions={
        <select className="input w-56" aria-label="Mailbox" value={box} onChange={(e) => setBox(e.target.value)}>
          <option value="">Every mailbox</option>
          {exts?.items.filter((x) => x.voicemail?.enabled).map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
        </select>
      }>
      {items.length > 0 && (
        <p className="mb-4 text-sm">
          <span className={fresh ? 'font-semibold text-violet-300' : 'text-slate-400'}>{fresh} new</span>
          <span className="text-slate-500"> · {items.length - fresh} heard</span>
        </p>
      )}
      {!items.length ? <Empty icon={VoicemailIcon} title="No messages">When a call to an extension is not answered, the caller can leave a message here.</Empty> : (
        <ul className="space-y-3">
          {items.map((v) => <Message key={`${v.box}/${v.id}`} v={v} boxName={nameOf(v.box)} />)}
        </ul>
      )}
    </Page>
  )
}

function Message({ v, boxName }: { v: Voicemail; boxName: string }) {
  const admin = useIsAdmin()
  const [mark] = useMarkHeardMutation()
  const [remove] = useDeleteVoicemailMutation()
  const el = useRef<HTMLDivElement>(null)
  const ws = useRef<WaveSurfer | null>(null)
  const [playing, setPlaying] = useState(false)
  const url = `./api/voicemail/${v.box}/${v.id}/audio`

  useEffect(() => {
    if (!el.current) return
    const w = WaveSurfer.create({
      container: el.current, url, height: 44, barWidth: 2, barGap: 2, barRadius: 2, cursorWidth: 1,
      waveColor: '#475569', progressColor: v.heard ? '#38bdf8' : '#a78bfa', cursorColor: '#e2e8f0', normalize: true,
    })
    w.on('play', () => setPlaying(true))
    w.on('pause', () => setPlaying(false))
    w.on('finish', () => {
      setPlaying(false)
      if (!v.heard && admin) mark({ box: v.box, id: v.id, heard: true })
    })
    ws.current = w
    return () => w.destroy()
  }, [url]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <li className={`card flex items-center gap-4 p-4 ${v.heard ? '' : 'border-violet-800/70'}`}>
      <button className="btn h-11 w-11 shrink-0 justify-center rounded-full p-0" aria-label={playing ? 'Pause' : 'Play message'} onClick={() => ws.current?.playPause()}>
        {playing ? <Pause size={18} className="text-violet-300" aria-hidden /> : <Play size={18} className="text-violet-300" aria-hidden />}
      </button>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          {!v.heard && <Badge status="info">New</Badge>}
          <span className="font-mono text-amber-300">{v.from || 'Unknown caller'}</span>
          {v.fromName && <span className="text-slate-300">{v.fromName}</span>}
          <span className="text-slate-500">for</span>
          <Chip kind="extension">{v.box}{boxName ? ` ${boxName}` : ''}</Chip>
          <span className="ml-auto text-xs text-slate-500" title={new Date(v.at).toLocaleString()}>{relTime(v.at)}</span>
          <span className="font-mono text-xs tabular-nums text-emerald-300">{formatDuration(v.seconds)}</span>
        </div>
        <div ref={el} className="mt-2" aria-hidden />
        {v.urgency && (
          <div className="mt-2 flex gap-1.5 text-xs">
            <span className={`rounded-md px-1.5 py-0.5 ${v.urgency === 'urgent' ? 'bg-rose-900 text-rose-100' : v.urgency === 'low' ? 'bg-slate-800 text-slate-400' : 'bg-sky-950 text-sky-200'}`}>{v.urgency === 'urgent' ? 'Urgent' : v.urgency === 'low' ? 'Low priority' : 'Normal'}</span>
            {v.category && v.category !== 'other' && <span className="rounded-md bg-violet-950 px-1.5 py-0.5 text-violet-200">{v.category}</span>}
          </div>
        )}
        {v.summary && <p className="mt-2 text-sm font-medium text-sky-200"><Sparkles size={13} className="mr-1 inline text-violet-300" aria-hidden />{v.summary}</p>}
        {v.transcript && <p className="mt-1 text-sm text-slate-400">“{v.transcript}”</p>}
        {v.aiError && <p className="mt-1 text-xs text-amber-300">No transcript: {v.aiError}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <a className="btn" href={url} download aria-label="Download message"><Download size={15} className="text-sky-300" aria-hidden /></a>
        {admin && (
          <button className="btn" aria-label={v.heard ? 'Mark as new' : 'Mark as heard'} title={v.heard ? 'Mark as new' : 'Mark as heard'}
            onClick={() => mark({ box: v.box, id: v.id, heard: !v.heard })}>
            <Check size={15} className={v.heard ? 'text-slate-500' : 'text-emerald-300'} aria-hidden />
          </button>
        )}
        {admin && <DeleteButton what="this message" onDelete={() => { attempt(() => remove({ box: v.box, id: v.id }).unwrap(), 'Message deleted') }} />}
      </div>
    </li>
  )
}
