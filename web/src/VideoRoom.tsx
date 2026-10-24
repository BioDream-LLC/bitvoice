import { clsx } from 'clsx'
import { Camera, CameraOff, LogOut, Phone, Video } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { useConferencesQuery } from './api'
import { newPeer } from './ice'
import { onServerEvent } from './store'
import { Empty, Field, Page } from './ui'

interface Publisher { id: string; name: string; codec: string; layers?: string[]; live: boolean; since: string; views: number }
interface RoomState { number: string; publishers: Publisher[] }

const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }

/** Waits until a peer connection has gathered its candidates (or a moment has passed). */
function gathered(pc: RTCPeerConnection) {
  return new Promise<void>((ok) => {
    if (pc.iceGatheringState === 'complete') return ok()
    pc.addEventListener('icegatheringstatechange', () => pc.iceGatheringState === 'complete' && ok())
    setTimeout(ok, 2500)
  })
}

/** One camera in the room, received in its own stream; big or small, it asks for the matching size. */
function Tile({ room, pin, pub, big, onPick }: { room: string; pin: string; pub: Publisher; big: boolean; onPick: () => void }) {
  const video = useRef<HTMLVideoElement>(null)
  const sub = useRef<{ pc: RTCPeerConnection; id: string } | null>(null)
  const [state, setState] = useState('connecting')
  useEffect(() => {
    let gone = false
    ;(async () => {
      const pc = await newPeer()
      pc.addTransceiver('video', { direction: 'recvonly' })
      pc.ontrack = (e) => { if (video.current) video.current.srcObject = new MediaStream([e.track]) }
      pc.onconnectionstatechange = () => setState(pc.connectionState)
      await pc.setLocalDescription(await pc.createOffer())
      await gathered(pc)
      const r = await fetch(`./api/video-rooms/${room}/subscribe`, { method: 'POST', headers: { ...H, 'X-Room-PIN': pin },
        body: JSON.stringify({ publisher: pub.id, sdp: pc.localDescription!.sdp, layer: big ? 'high' : 'low' }) })
      const j = await r.json()
      if (!r.ok || gone) { pc.close(); if (!gone) setState(j.message ?? 'failed'); return }
      sub.current = { pc, id: j.id }
      await pc.setRemoteDescription({ type: 'answer', sdp: j.sdp })
    })()
    return () => {
      gone = true
      const s = sub.current
      if (s) { s.pc.close(); fetch(`./api/video-rooms/${room}/${s.id}`, { method: 'DELETE', headers: { ...H, 'X-Room-PIN': pin } }).catch(() => {}) }
      sub.current = null
    }
  }, [room, pin, pub.id]) // eslint-disable-line react-hooks/exhaustive-deps
  // A tile made big asks for the full size, a small one for the small size (simulcast cameras).
  const layered = (pub.layers?.length ?? 0) > 1
  const first = useRef(true)
  useEffect(() => {
    if (first.current) { first.current = false; return } // the subscription already asked for this size
    const s = sub.current
    if (s && layered) {
      fetch(`./api/video-rooms/${room}/subscribe/${s.id}`, { method: 'PATCH', headers: { ...H, 'X-Room-PIN': pin }, body: JSON.stringify({ layer: big ? 'high' : 'low' }) }).catch(() => {})
    }
  }, [big, room, pin, layered])
  return (
    <figure className={clsx('relative overflow-hidden rounded-lg border bg-black', big ? 'border-fuchsia-500 md:col-span-3 md:row-span-2' : 'border-slate-800')}>
      <video ref={video} autoPlay playsInline muted className="aspect-video w-full object-contain" aria-label={`Video of ${pub.name}`} data-publisher={pub.id} />
      <figcaption className="absolute bottom-0 left-0 right-0 flex items-center gap-2 bg-black/60 px-2 py-1 text-xs">
        <span className="font-medium text-slate-100">{pub.name}</span>
        {(pub.layers?.length ?? 0) > 1 && <span className="text-fuchsia-300">{big ? 'full size' : 'small'}</span>}
        <span className={state === 'connected' ? 'text-emerald-300' : 'text-amber-300'}>{state === 'connected' ? '' : state}</span>
        {!big && <button type="button" className="ml-auto rounded bg-slate-800 px-2 py-0.5 text-slate-200" onClick={onPick}>Make big</button>}
      </figcaption>
    </figure>
  )
}

/** A video room: everyone's camera, each in its own picture (an SFU conference room). */
export function VideoRoom() {
  const { number = '' } = useParams()
  const { data: confs } = useConferencesQuery()
  const conf = confs?.items.find((c) => c.number === number)
  const [pin, setPin] = useState('')
  const [state, setState] = useState<RoomState | null>(null)
  const [error, setError] = useState('')
  const [mine, setMine] = useState<{ pc: RTCPeerConnection; id: string; stream: MediaStream } | null>(null)
  const [big, setBig] = useState('')
  const self = useRef<HTMLVideoElement>(null)
  const load = useCallback(async () => {
    const r = await fetch(`./api/video-rooms/${number}`, { headers: { ...H, 'X-Room-PIN': pin } })
    const j = await r.json()
    if (!r.ok) { setError(j.message); setState(null); return }
    setError('')
    setState(j)
  }, [number, pin])
  useEffect(() => { load() }, [load])
  useEffect(() => onServerEvent((type, d) => { if (type === 'sfu.changed' && (d as RoomState).number === number) load() }), [number, load])
  useEffect(() => () => { mine?.pc.close(); mine?.stream.getTracks().forEach((t) => t.stop()) }, [mine])

  const join = async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ video: { width: 1280, height: 720 }, audio: false })
      const pc = await newPeer()
      // Three sizes of the same picture (simulcast): each viewer gets the size it shows.
      pc.addTransceiver(stream.getVideoTracks()[0], { direction: 'sendonly', streams: [stream],
        sendEncodings: [{ rid: 'h', maxBitrate: 1_200_000 }, { rid: 'm', scaleResolutionDownBy: 2, maxBitrate: 400_000 }, { rid: 'l', scaleResolutionDownBy: 4, maxBitrate: 150_000 }] })
      await pc.setLocalDescription(await pc.createOffer())
      await gathered(pc)
      const r = await fetch(`./api/video-rooms/${number}/publish`, { method: 'POST', headers: { ...H, 'X-Room-PIN': pin }, body: JSON.stringify({ sdp: pc.localDescription!.sdp }) })
      const j = await r.json()
      if (!r.ok) { pc.close(); stream.getTracks().forEach((t) => t.stop()); toast.error(j.message); return }
      await pc.setRemoteDescription({ type: 'answer', sdp: j.sdp })
      if (self.current) self.current.srcObject = stream
      setMine({ pc, id: j.id, stream })
      // The room's sound is its conference: the browser phone calls it.
      window.dispatchEvent(new CustomEvent('bitvoice:dial', { detail: number }))
    } catch (e) {
      toast.error(`The camera could not be used: ${(e as Error).message}`)
    }
  }
  const leave = async () => {
    if (!mine) return
    mine.pc.close()
    mine.stream.getTracks().forEach((t) => t.stop())
    await fetch(`./api/video-rooms/${number}/${mine.id}`, { method: 'DELETE', headers: { ...H, 'X-Room-PIN': pin } }).catch(() => {})
    setMine(null)
  }
  const others = (state?.publishers ?? []).filter((p) => p.id !== mine?.id)
  const bigId = others.some((p) => p.id === big) ? big : others[0]?.id ?? ''
  return (
    <Page icon={Video} title={conf ? `Video room ${number} ${conf.name}` : `Video room ${number}`} accent="from-fuchsia-400 to-sky-400"
      intro="Everyone's camera, each in its own picture. The room's sound is its conference: joining calls it on the browser phone, and phones dialling the room are heard too.">
      {conf && conf.video !== 'sfu' && <p className="mb-4 text-sm text-amber-300">This room is not set to everyone's video (Conference rooms, Video).</p>}
      {conf?.pin && !state && (
        <div className="mb-4 max-w-xs"><Field label="Room PIN" htmlFor="vrpin"><input id="vrpin" className="input font-mono" inputMode="numeric" value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} /></Field></div>
      )}
      {error && <p role="alert" className="mb-4 text-sm text-rose-300">{error}</p>}
      {state && (
        <>
          <div className="mb-4 flex flex-wrap items-center gap-2">
            {!mine ? <button type="button" className="btn btn-primary" onClick={join}><Camera size={15} aria-hidden /> Join with my camera</button> : (
              <button type="button" className="btn" onClick={leave}><LogOut size={15} aria-hidden /> Leave</button>
            )}
            <button type="button" className="btn" onClick={() => window.dispatchEvent(new CustomEvent('bitvoice:dial', { detail: number }))}><Phone size={15} aria-hidden /> Call the room&apos;s sound</button>
            <span className="text-sm text-slate-400">{state.publishers.length} camera{state.publishers.length === 1 ? '' : 's'} in the room</span>
          </div>
          <div className="grid gap-3 md:grid-cols-4" aria-label="Cameras in the room">
            {mine && (
              <figure className="relative overflow-hidden rounded-lg border border-emerald-700 bg-black">
                <video ref={self} autoPlay playsInline muted className="aspect-video w-full object-contain" aria-label="Your camera" />
                <figcaption className="absolute bottom-0 left-0 bg-black/60 px-2 py-1 text-xs text-emerald-300">You</figcaption>
              </figure>
            )}
            {others.map((p) => <Tile key={p.id} room={number} pin={pin} pub={p} big={p.id === bigId} onPick={() => setBig(p.id)} />)}
          </div>
          {others.length === 0 && <div className="mt-4"><Empty icon={CameraOff} title="No one else's camera yet">When others join with their camera, they appear here.</Empty></div>}
        </>
      )}
    </Page>
  )
}
