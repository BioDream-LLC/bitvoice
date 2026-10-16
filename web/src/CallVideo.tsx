import { Camera, Copy, Eye, EyeOff, FileImage, Lock, LockOpen, MapPin, Send, Video, VideoOff } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useCallMediaQuery, useHoldMediaMutation, useRevealMutation, useVideoLinkMutation, type Call, type MediaItem } from './api'
import { Badge } from './ui'

const csrf = { 'X-BitVoice': '1' }

/** A 911 call's video and imagery: the link for the caller, the live video, the photos and clips.
 * Everything opens blurred; revealing it is the call-taker's choice, and is logged. */
export function CallVideoPanel({ call }: { call: Call }) {
  const em = call.emergency!
  const v = em.video
  const [link, setLink] = useState<{ url: string; viewUrl: string; sent: boolean; problem?: string } | null>(null)
  const [makeLink, { isLoading }] = useVideoLinkMutation()
  const { data: media } = useCallMediaQuery(call.id, { pollingInterval: 3000 })
  const items = media?.items ?? []
  const send = async () => {
    try {
      const r = await makeLink(call.id).unwrap()
      setLink(r)
      if (r.sent) toast.success('Video link texted to the caller')
      else if (r.problem) toast.warning(r.problem)
    } catch (e) { toast.error('No video link', { description: errorOf(e).message }) }
  }
  return (
    <section className="rounded-lg border border-violet-900/60 bg-slate-900/40 p-3" aria-label="Caller video">
      <div className="flex flex-wrap items-center gap-2 text-xs text-violet-300">
        <Camera size={13} aria-hidden /> Video and photos
        {v?.live && <Badge status="fail">LIVE{v.codec ? ` · ${v.codec}` : ''}</Badge>}
        {v?.viewers?.length ? <span className="text-slate-500">watched by {v.viewers.join(', ')}</span> : null}
      </div>
      {!v && !link && <button className="btn mt-2" disabled={isLoading} onClick={send}><Send size={14} className="text-violet-300" aria-hidden /> Send the caller a video link</button>}
      {(link || v) && <LinkLine url={link?.url} viewUrl={link?.viewUrl} sent={link?.sent} onResend={send} />}
      {v?.live && <LiveVideo callId={call.id} />}
      {items.length > 0 && <MediaList callId={call.id} items={items} held={!!media?.held} />}
    </section>
  )
}

function copy(text: string, what: string) {
  navigator.clipboard?.writeText(text).then(() => toast.success(`${what} copied`), () => toast.error('Could not copy'))
}

function LinkLine({ url, viewUrl, sent, onResend }: { url?: string; viewUrl?: string; sent?: boolean; onResend: () => void }) {
  if (!url) return <p className="mt-2 text-sm text-slate-400">A video link was made for this call. <button className="btn-ghost px-1 text-violet-300 underline" onClick={onResend}>Show it</button></p>
  return (
    <div className="mt-2 space-y-1 text-sm">
      <p className="text-slate-300">{sent ? 'Texted to the caller:' : 'Read this link to the caller (no texting number is set):'}</p>
      <div className="flex flex-wrap items-center gap-2">
        <code className="break-all rounded bg-slate-950 px-2 py-1 text-xs text-amber-200" aria-label="The caller's link">{url}</code>
        <button className="btn py-0.5" onClick={() => copy(url, 'Link')}><Copy size={12} aria-hidden /> Copy</button>
      </div>
      {viewUrl && <div className="flex flex-wrap items-center gap-2 text-xs text-slate-500">For CAD and units to watch (WHEP): <code className="break-all text-slate-400">{viewUrl}</code>
        <button className="btn py-0.5" onClick={() => copy(viewUrl, 'Viewing link')}><Copy size={12} aria-hidden /> Copy</button></div>}
    </div>
  )
}

/** The caller's live video over WHEP, blurred until revealed. */
function LiveVideo({ callId }: { callId: string }) {
  const el = useRef<HTMLVideoElement>(null)
  const [watching, setWatching] = useState(false)
  const [shown, setShown] = useState(false)
  const [stream, setStream] = useState<MediaStream | null>(null)
  const [reveal] = useRevealMutation()
  // the track arrives before the player is drawn: attach it once both are there
  useEffect(() => { if (el.current && stream) el.current.srcObject = stream }, [stream, watching])
  const pc = useRef<RTCPeerConnection | null>(null)
  const res = useRef<string | null>(null)
  const stop = () => {
    if (res.current) fetch(res.current, { method: 'DELETE', headers: csrf }).catch(() => {})
    pc.current?.close(); pc.current = null; res.current = null
    setWatching(false); setShown(false); setStream(null)
  }
  useEffect(() => stop, [])
  const watch = async () => {
    try {
      const p = new RTCPeerConnection()
      pc.current = p
      p.addTransceiver('video', { direction: 'recvonly' })
      p.ontrack = (ev) => setStream(ev.streams[0] ?? new MediaStream([ev.track]))
      await p.setLocalDescription(await p.createOffer())
      const r = await fetch(`./api/calls/${callId}/whep`, { method: 'POST', headers: { ...csrf, 'Content-Type': 'application/sdp' }, body: p.localDescription!.sdp })
      const text = await r.text()
      if (!r.ok) throw new Error(JSON.parse(text || '{}').message || text)
      res.current = r.headers.get('Location')
      await p.setRemoteDescription({ type: 'answer', sdp: text })
      setWatching(true)
    } catch (e) { stop(); toast.error('The video could not be shown', { description: String((e as Error).message ?? e) }) }
  }
  const doReveal = async () => { setShown(true); reveal({ id: callId, item: 'live' }) }
  return (
    <div className="mt-2 space-y-2">
      {!watching ? <button className="btn" onClick={watch}><Video size={14} className="text-rose-300" aria-hidden /> Watch the caller's video</button> : <>
        <div className="relative overflow-hidden rounded-lg bg-black">
          <video ref={el} autoPlay playsInline muted aria-label="The caller's live video" className={`h-56 w-full object-contain transition ${shown ? '' : 'scale-110 blur-2xl'}`} />
          {!shown && <div className="absolute inset-0 flex items-center justify-center">
            <button className="btn btn-primary" onClick={doReveal}><Eye size={14} aria-hidden /> Reveal (may be distressing)</button></div>}
        </div>
        <div className="flex gap-2">
          {shown && <button className="btn" onClick={() => setShown(false)}><EyeOff size={14} aria-hidden /> Blur again</button>}
          <button className="btn" onClick={stop}><VideoOff size={14} aria-hidden /> Stop watching</button>
        </div>
      </>}
    </div>
  )
}

function MediaList({ callId, items, held }: { callId: string; items: MediaItem[]; held: boolean }) {
  const [hold, { isLoading }] = useHoldMediaMutation()
  const [open, setOpen] = useState<Record<number, boolean>>({})
  return (
    <div className="mt-3 space-y-2">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <FileImage size={14} className="text-teal-300" aria-hidden /> <span className="text-slate-300">{items.length === 1 ? '1 file from the caller' : `${items.length} files from the caller`}</span>
        <button className="btn py-0.5" disabled={isLoading} onClick={() => hold({ id: callId, hold: !held }).unwrap().then(() => toast.success(held ? 'No longer held' : 'Held: kept past the retention period')).catch((e) => toast.error(errorOf(e).message))}>
          {held ? <><Lock size={12} className="text-amber-300" aria-hidden /> Held as evidence</> : <><LockOpen size={12} aria-hidden /> Hold as evidence</>}
        </button>
      </div>
      <ul className="grid gap-2 sm:grid-cols-2">
        {items.map((it) => (
          <li key={it.n} className="rounded-lg border border-slate-800 p-2 text-xs" aria-label={`File ${it.n}: ${it.name}`}>
            <div className="flex items-center justify-between gap-2"><span className="truncate font-semibold text-slate-200">{it.name}</span><span className="text-slate-500">{(it.size / 1024).toFixed(0)} KB</span></div>
            <div className="text-slate-500">{it.recording ? `live video recorded here, from ${new Date(it.taken ?? it.received).toLocaleTimeString()}` : <>{it.kind}{it.takenLocal ? `, taken ${it.takenLocal} (the camera's clock)` : it.taken ? `, taken ${new Date(it.taken).toLocaleString()}` : ''}</>} · {it.recording ? 'ended' : 'received'} {new Date(it.received).toLocaleTimeString()}</div>
            {it.hasGps && <a className="inline-flex items-center gap-1 text-sky-300 underline" href={`https://www.openstreetmap.org/?mlat=${it.lat}&mlon=${it.lon}#map=18/${it.lat}/${it.lon}`} target="_blank" rel="noreferrer"><MapPin size={11} aria-hidden /> {it.lat?.toFixed(5)}, {it.lon?.toFixed(5)}</a>}
            <div className="break-all font-mono text-[10px] text-slate-600" title="SHA-256 of the file as it arrived">SHA-256 {it.sha256}</div>
            {open[it.n]
              ? (it.type.startsWith('video/')
                ? <>
                  <video controls className="mt-1 max-h-48 w-full rounded" src={`./api/calls/${callId}/media/${it.n}${it.recording ? '?view=1' : ''}`} aria-label={`Clip ${it.name}`} />
                  {it.recording && <a className="text-sky-300 underline" href={`./api/calls/${callId}/media/${it.n}`} download={it.name}>Download the original (IVF, the hash above)</a>}
                </>
                : <img className="mt-1 max-h-48 w-full rounded object-contain" src={`./api/calls/${callId}/media/${it.n}`} alt={`Photo ${it.name} from the caller`} />)
              : <button className="btn mt-1 py-0.5" onClick={() => setOpen((o) => ({ ...o, [it.n]: true }))}><Eye size={12} aria-hidden /> Reveal (may be distressing)</button>}
          </li>
        ))}
      </ul>
    </div>
  )
}
