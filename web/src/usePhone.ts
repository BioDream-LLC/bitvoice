import { useCallback, useEffect, useRef, useState } from 'react'
import { newPeer } from './ice'
import { openMicrophone } from './mic'

// The browser phone: one WebRTC peer connection per call, signalling over BitVoice's /api/phone endpoints.

export interface PhoneCall {
  id: string
  pbxCall?: string // BitVoice's call ID, for incoming calls
  direction: 'in' | 'out'
  number: string
  name?: string
  state: 'incoming' | 'calling' | 'ringing' | 'connected' | 'ended'
  since: number
  reason?: string
  muted?: boolean
  briefing?: string // what the call is about, when it was passed on with a briefing
}

const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }

async function post<T>(url: string, body: unknown): Promise<T> {
  const r = await fetch(url, { method: 'POST', headers: H, body: JSON.stringify(body) })
  const j = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error(j.message ?? `request failed (${r.status})`)
  return j as T
}

/** A ring sound made in the browser (no audio files): two tones, repeating. */
function ringer() {
  let ctx: AudioContext | null = null
  let timer: ReturnType<typeof setInterval> | undefined
  return {
    start() {
      if (timer) return
      ctx = new AudioContext()
      const ring = () => {
        if (!ctx) return
        for (const [f, at] of [[440, 0], [480, 0]] as const) {
          const o = ctx.createOscillator(), g = ctx.createGain()
          o.frequency.value = f
          g.gain.setValueAtTime(0.08, ctx.currentTime + at)
          g.gain.setValueAtTime(0, ctx.currentTime + at + 1.2)
          o.connect(g).connect(ctx.destination)
          o.start(ctx.currentTime + at)
          o.stop(ctx.currentTime + at + 1.3)
        }
      }
      ring()
      timer = setInterval(ring, 3000)
    },
    stop() {
      clearInterval(timer)
      timer = undefined
      ctx?.close()
      ctx = null
    },
  }
}

export function usePhone() {
  const [phone, setPhone] = useState<{ id: string; extension: string; name: string } | null>(null)
  const [calls, setCalls] = useState<PhoneCall[]>([])
  const [error, setError] = useState('')
  const pcs = useRef(new Map<string, RTCPeerConnection>())
  const audio = useRef<HTMLAudioElement | null>(null)
  const ring = useRef(ringer())
  const quiet = useRef(false) // inside a mobile app, which rings itself
  const creds = useRef<Record<string, string> | null>(null) // to sign in again after BitVoice restarts

  // Events can arrive before the page has added the call they are about (an answer within milliseconds
  // of dialling); they are kept and applied when the call appears.
  const early = useRef(new Map<string, Partial<PhoneCall>>())
  const update = useCallback((id: string, v: Partial<PhoneCall>) => setCalls((cs) => {
    if (!cs.some((c) => c.id === id)) {
      early.current.set(id, { ...early.current.get(id), ...v })
      return cs
    }
    return cs.map((c) => (c.id === id ? { ...c, ...v } : c))
  }), [])

  const peer = useCallback(async () => {
    const pc = await newPeer()
    const mic = await openMicrophone({ echoCancellation: true, noiseSuppression: true })
    mic.getTracks().forEach((t) => pc.addTrack(t, mic))
    pc.ontrack = (e) => {
      if (!audio.current) {
        audio.current = new Audio()
        audio.current.autoplay = true
      }
      audio.current.srcObject = e.streams[0] ?? new MediaStream([e.track])
      audio.current.play().catch(() => {})
    }
    await pc.setLocalDescription(await pc.createOffer())
    return pc
  }, [])

  const drop = useCallback((id: string) => {
    const pc = pcs.current.get(id)
    pc?.getSenders().forEach((s) => s.track?.stop())
    pc?.close()
    pcs.current.delete(id)
  }, [])

  // Events for this phone.
  useEffect(() => {
    if (!phone) return
    const es = new EventSource(`./api/phone/${phone.id}/events`)
    es.addEventListener('call', (e) => {
      const ev = JSON.parse((e as MessageEvent).data) as { type: string; callId: string; from?: string; fromName?: string; reason?: string; pbxCall?: string }
      if (ev.type === 'incoming') {
        setCalls((cs) => [...cs, { id: ev.callId, direction: 'in', number: ev.from ?? '', name: ev.fromName, state: 'incoming', since: Date.now(), pbxCall: ev.pbxCall }])
        if (!quiet.current) ring.current.start()
        if (!quiet.current && document.hidden && 'Notification' in window && Notification.permission === 'granted') {
          new Notification('Incoming call', { body: `${ev.fromName ?? ''} ${ev.from ?? ''}`.trim() })
        }
      } else if (ev.type === 'ringing') {
        update(ev.callId, { state: 'ringing' })
      } else if (ev.type === 'answered') {
        ring.current.stop()
        update(ev.callId, { state: 'connected', since: Date.now() })
      } else if (ev.type === 'ended' || ev.type === 'failed') {
        ring.current.stop()
        drop(ev.callId)
        update(ev.callId, { state: 'ended', reason: ev.reason })
        setTimeout(() => { early.current.delete(ev.callId); setCalls((cs) => cs.filter((c) => c.id !== ev.callId)) }, 2500)
      }
    })
    es.addEventListener('briefing', (e) => {
      const b = JSON.parse((e as MessageEvent).data) as { callId: string; summary: string }
      setCalls((cs) => cs.map((c) => (c.pbxCall === b.callId ? { ...c, briefing: b.summary } : c)))
    })
    let checking = false
    es.onerror = () => {
      setError('Connection to BitVoice lost; reconnecting…')
      // After a restart BitVoice no longer knows this phone: sign in again with what was used before.
      if (checking) return
      checking = true
      setTimeout(async () => {
        const ctl = new AbortController()
        try {
          const r = await fetch(`./api/phone/${phone.id}/events`, { signal: ctl.signal })
          ctl.abort()
          if (r.status === 404 && creds.current) {
            es.close()
            const c = creds.current
            setPhone(await post<{ id: string; extension: string; name: string }>('./api/phone', c))
          }
        } catch { /* still away: the browser keeps retrying */ }
        checking = false
      }, 2000)
    }
    es.onopen = () => setError('')
    return () => es.close()
  }, [phone, update, drop])

  const open = useCallback(async (extension: string, password?: string) => {
    setError('')
    try {
      const p = await post<{ id: string; extension: string; name: string }>('./api/phone', { extension, password: password ?? '' })
      creds.current = { extension, password: password ?? '' }
      setPhone(p)
      if ('Notification' in window && Notification.permission === 'default') Notification.requestPermission().catch(() => {})
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  // A paired BitVoice app signs in with its device credentials; the app's own call screen rings, so the
  // page stays quiet.
  const openDevice = useCallback(async (device: string, secret: string) => {
    setError('')
    quiet.current = true
    try {
      setPhone(await post<{ id: string; extension: string; name: string }>('./api/phone', { device, secret }))
      creds.current = { device, secret }
    } catch (e) {
      setError((e as Error).message)
    }
  }, [])

  const close = useCallback(async () => {
    if (!phone) return
    creds.current = null
    pcs.current.forEach((_, id) => drop(id))
    setCalls([])
    await fetch(`./api/phone/${phone.id}`, { method: 'DELETE', headers: H }).catch(() => {})
    setPhone(null)
  }, [phone, drop])

  const dial = useCallback(async (number: string) => {
    if (!phone || !number) return
    setError('')
    try {
      const pc = await peer()
      const r = await post<{ callId: string; sdp: string }>(`./api/phone/${phone.id}/calls`, { to: number, sdp: pc.localDescription!.sdp })
      pcs.current.set(r.callId, pc)
      await pc.setRemoteDescription({ type: 'answer', sdp: r.sdp })
      // Events for this call may have come first (the echo test answers at once). update() keeps them in
      // early from inside a state updater, which React runs later, in order: so read them in an updater too,
      // never here, or an answer that came first is lost and the call stays on Calling for ever.
      setCalls((cs) => [...cs, { id: r.callId, direction: 'out', number, state: 'calling', since: Date.now(), ...early.current.get(r.callId) }])
    } catch (e) {
      setError((e as Error).message)
    }
  }, [phone, peer])

  const answer = useCallback(async (id: string) => {
    if (!phone) return
    ring.current.stop()
    try {
      const pc = await peer()
      pcs.current.set(id, pc)
      const r = await post<{ sdp: string }>(`./api/phone/${phone.id}/answer`, { callId: id, sdp: pc.localDescription!.sdp })
      await pc.setRemoteDescription({ type: 'answer', sdp: r.sdp })
    } catch (e) {
      setError((e as Error).message)
    }
  }, [phone, peer])

  const hangup = useCallback(async (id: string) => {
    if (!phone) return
    ring.current.stop()
    drop(id)
    update(id, { state: 'ended', reason: 'you hung up' })
    await fetch(`./api/phone/${phone.id}/calls/${id}`, { method: 'DELETE', headers: H }).catch(() => {})
    setTimeout(() => setCalls((cs) => cs.filter((c) => c.id !== id)), 1500)
  }, [phone, drop, update])

  const dtmf = useCallback((id: string, key: string) => {
    pcs.current.get(id)?.getSenders().find((s) => s.dtmf)?.dtmf?.insertDTMF(key, 120, 60)
  }, [])

  const mute = useCallback((id: string, on: boolean) => {
    pcs.current.get(id)?.getSenders().forEach((s) => { if (s.track) s.track.enabled = !on })
    update(id, { muted: on })
  }, [update])

  useEffect(() => () => { ring.current.stop() }, [])
  return { phone, calls, error, open, openDevice, close, dial, answer, hangup, dtmf, mute }
}
