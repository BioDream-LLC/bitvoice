import { clsx } from 'clsx'
import { newPeer } from './ice'
import { Mic, MicOff, Phone, PhoneOff } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { openMicrophone } from './mic'
import { formatDuration } from './ui'

const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
const keys = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '*', '0', '#']

interface Info { title: string; intro: string; askName: boolean; open: boolean; organisation: string }
type State = 'idle' | 'starting' | 'ringing' | 'connected' | 'ended'

/** CallPage: the public page a call link opens. A caller rings the business from the browser. */
export function CallPage({ slug }: { slug: string }) {
  const [info, setInfo] = useState<Info | null>(null)
  const [missing, setMissing] = useState(false)
  const [name, setName] = useState('')
  const [state, setState] = useState<State>('idle')
  const [error, setError] = useState('')
  const [since, setSince] = useState(0)
  const [now, setNow] = useState(Date.now())
  const [muted, setMuted] = useState(false)
  const pc = useRef<RTCPeerConnection | null>(null)
  const ids = useRef<{ phone: string; call: string } | null>(null)
  const es = useRef<EventSource | null>(null)
  const audio = useRef<HTMLAudioElement | null>(null)

  useEffect(() => {
    fetch(`./api/link/${encodeURIComponent(slug)}`).then(async (r) => (r.ok ? setInfo(await r.json()) : setMissing(true))).catch(() => setMissing(true))
  }, [slug])
  useEffect(() => { if (info) document.title = info.title }, [info])
  useEffect(() => {
    if (state !== 'connected') return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [state])
  useEffect(() => () => { hangup() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const stop = () => {
    es.current?.close()
    pc.current?.getSenders().forEach((s) => s.track?.stop())
    pc.current?.close()
    pc.current = null
  }
  const hangup = async () => {
    const i = ids.current
    ids.current = null
    stop()
    if (i) {
      await fetch(`./api/phone/${i.phone}/calls/${i.call}`, { method: 'DELETE', headers: H }).catch(() => {})
      await fetch(`./api/phone/${i.phone}`, { method: 'DELETE', headers: H }).catch(() => {})
    }
  }
  const call = async () => {
    setError('')
    setState('starting')
    try {
      const p = await newPeer()
      pc.current = p
      const mic = await openMicrophone({ echoCancellation: true, noiseSuppression: true })
      mic.getTracks().forEach((t) => p.addTrack(t, mic))
      p.ontrack = (e) => {
        if (!audio.current) { audio.current = new Audio(); audio.current.autoplay = true }
        audio.current.srcObject = e.streams[0] ?? new MediaStream([e.track])
        audio.current.play().catch(() => {})
      }
      await p.setLocalDescription(await p.createOffer())
      const r = await fetch(`./api/link/${encodeURIComponent(slug)}/call`, { method: 'POST', headers: H, body: JSON.stringify({ name, sdp: p.localDescription!.sdp }) })
      const j = await r.json().catch(() => ({}))
      if (!r.ok) throw new Error(j.message ?? 'The call could not be placed.')
      ids.current = { phone: j.phoneId, call: j.callId }
      const ev = new EventSource(`./api/phone/${j.phoneId}/events`)
      es.current = ev
      ev.addEventListener('call', (e) => {
        const d = JSON.parse((e as MessageEvent).data) as { type: string; reason?: string }
        if (d.type === 'ringing') setState('ringing')
        else if (d.type === 'answered') { setState('connected'); setSince(Date.now()) }
        else if (d.type === 'ended' || d.type === 'failed') { setState('ended'); setError(d.type === 'failed' ? (d.reason ?? 'Nobody could take the call.') : ''); hangup() }
      })
      await p.setRemoteDescription({ type: 'answer', sdp: j.sdp })
      setState((s) => (s === 'starting' ? 'ringing' : s))
    } catch (e) {
      stop()
      setState('idle')
      setError((e as Error).name === 'NotAllowedError' ? 'Allow the microphone to make the call.' : (e as Error).message)
    }
  }
  const dtmf = (k: string) => {
    const s = pc.current?.getSenders().find((x) => x.dtmf)
    s?.dtmf?.insertDTMF(k, 120, 60)
  }
  const mute = () => {
    const on = !muted
    pc.current?.getSenders().forEach((s) => { if (s.track) s.track.enabled = !on })
    setMuted(on)
  }

  if (missing) return <Shell><h1 className="text-xl font-semibold text-slate-100">This link does not work</h1><p className="mt-2 text-slate-400">Ask whoever gave it to you for a new one.</p></Shell>
  if (!info) return <Shell><p className="text-slate-400">One moment…</p></Shell>
  const live = state === 'ringing' || state === 'connected' || state === 'starting'
  return (
    <Shell>
      <h1 className="text-2xl font-semibold text-slate-50">{info.title}</h1>
      {info.intro && <p className="mt-2 text-slate-300">{info.intro}</p>}
      {!info.open ? <p className="mt-6 text-amber-300">This line is closed just now.</p> : (
        <div className="mt-6 space-y-4">
          {!live && info.askName && (
            <label className="block text-left text-sm text-slate-300">Your name
              <input className="input mt-1" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />
            </label>
          )}
          {!live ? (
            <button className="btn w-full justify-center border-emerald-600 bg-emerald-600 py-3 text-lg text-white hover:bg-emerald-500" disabled={info.askName && !name.trim()} onClick={call}>
              <Phone size={18} aria-hidden /> {state === 'ended' ? 'Call again' : 'Call now'}
            </button>
          ) : (
            <>
              <p role="status" className={clsx('text-lg', state === 'connected' ? 'font-mono tabular-nums text-emerald-300' : 'text-slate-300')}>
                {state === 'connected' ? formatDuration(Math.round((now - since) / 1000)) : 'Calling…'}
              </p>
              <div className="grid grid-cols-3 gap-2" role="group" aria-label="Keypad">
                {keys.map((k) => <button key={k} className="h-11 rounded-lg bg-slate-800 font-mono text-lg text-slate-100 hover:bg-slate-700" onClick={() => dtmf(k)}>{k}</button>)}
              </div>
              <div className="flex gap-2">
                <button className="btn flex-1 justify-center" onClick={mute} aria-pressed={muted}>{muted ? <MicOff size={16} className="text-amber-300" aria-hidden /> : <Mic size={16} aria-hidden />} {muted ? 'Unmute' : 'Mute'}</button>
                <button className="btn flex-1 justify-center border-rose-600 bg-rose-600 text-white hover:bg-rose-500" onClick={() => { hangup(); setState('ended') }}><PhoneOff size={16} aria-hidden /> Hang up</button>
              </div>
            </>
          )}
          {state === 'ended' && !error && <p className="text-slate-400">Call ended. Thank you.</p>}
          {error && <p role="alert" className="text-rose-300">{error}</p>}
          <p className="text-xs text-slate-500">The call uses your microphone and goes through {info.organisation || 'the business'}'s phone system. No app or phone number is needed.</p>
        </div>
      )}
    </Shell>
  )
}

function Shell({ children }: { children: React.ReactNode }) {
  return <main className="grid min-h-screen place-items-center bg-slate-950 p-6"><div className="card w-full max-w-sm p-6 text-center">{children}</div></main>
}
