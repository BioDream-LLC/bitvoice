import { useEffect, useRef } from 'react'
import type { usePhone } from './usePhone'

/**
 * The bridge between the staff phone page and the BitVoice mobile apps, which show this page in a web
 * view and put calls on the phone's own call screen (CallKit on iPhone, Telecom on Android).
 *
 * The app sets `window.bitvoiceNative` before the page loads: `{ device, secret, platform }` and a `post`
 * function (iOS: webkit.messageHandlers.bitvoice, Android: a JavaScript interface). The page then signs
 * in with the device's credentials, tells the app about each call, and answers or hangs up when the app
 * says so through `window.bitvoice.answer(callId)` and `window.bitvoice.hangup(callId)`.
 */
export interface NativeHost { device: string; secret: string; platform: 'ios' | 'android'; post: (msg: string) => void }

declare global {
  interface Window {
    bitvoiceNative?: NativeHost
    bitvoice?: { answer: (id: string) => void; hangup: (id: string) => void; mute: (on: boolean) => void; dtmf: (k: string) => void; hold: (on: boolean) => void; transfer: (to: string) => void }
    webkit?: { messageHandlers?: Record<string, { postMessage: (m: unknown) => void }> }
  }
}

/** The host app, if this page runs inside one. */
export function nativeHost(): NativeHost | null {
  const n = window.bitvoiceNative
  return n && n.device && n.secret ? n : null
}

export function useNativeBridge(api: ReturnType<typeof usePhone>) {
  const host = nativeHost()
  const told = useRef(new Map<string, string>())

  // Sign in once with the device's credentials.
  useEffect(() => {
    if (host && !api.phone) api.openDevice(host.device, host.secret)
  }, [host, api.phone]) // eslint-disable-line react-hooks/exhaustive-deps

  // Tell the app when a call appears or changes state.
  useEffect(() => {
    if (!host) return
    for (const c of api.calls) {
      if (told.current.get(c.id) === c.state) continue
      told.current.set(c.id, c.state)
      host.post(JSON.stringify({ type: 'call', id: c.id, pbxCall: c.pbxCall ?? '', state: c.state, direction: c.direction, number: c.number, name: c.name ?? '', reason: c.reason ?? '' }))
    }
  }, [host, api.calls])

  useEffect(() => {
    if (!host) return
    host.post(JSON.stringify({ type: 'phone', ready: !!api.phone, extension: api.phone?.extension ?? '', error: api.error }))
  }, [host, api.phone, api.error])

  // Commands from the app.
  useEffect(() => {
    if (!host) return
    window.bitvoice = {
      answer: (id) => { api.answer(id) },
      hangup: (id) => { api.hangup(id) },
      mute: (on) => { const c = api.calls.find((x) => x.state === 'connected'); if (c) api.mute(c.id, on) },
      dtmf: (k) => { const c = api.calls.find((x) => x.state === 'connected'); if (c) api.dtmf(c.id, k) },
      hold: (on) => { const c = api.calls.find((x) => x.state === 'connected'); if (c) api.hold(c.id, on) },
      transfer: (to) => { const c = api.calls.find((x) => x.state === 'connected'); if (c) api.transfer(c.id, to) },
    }
    return () => { window.bitvoice = undefined }
  }, [host, api])
}
