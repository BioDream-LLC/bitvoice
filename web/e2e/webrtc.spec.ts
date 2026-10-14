import { expect, test } from '@playwright/test'
import { signIn } from './helpers'

// Real Chromium against BitVoice's own ICE-lite, DTLS and SRTP: the browser's fake microphone goes to the
// echo test and must come back decrypted. This is the interoperability check for the WebRTC stack.
test.use({ launchOptions: { args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'] } })


test('browser audio through ICE-lite, DTLS-SRTP and back', async ({ page }) => {
  await signIn(page)
  const result = await page.evaluate(async () => {
    const pc = new RTCPeerConnection()
    const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
    mic.getTracks().forEach((t) => pc.addTrack(t, mic))
    await pc.setLocalDescription(await pc.createOffer())
    const r = await fetch('./api/phone/echo', { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' },
      body: JSON.stringify({ sdp: pc.localDescription!.sdp }) })
    const body = await r.json()
    if (!r.ok) return { error: body.message }
    await pc.setRemoteDescription({ type: 'answer', sdp: body.sdp })
    const deadline = Date.now() + 10000
    while (pc.connectionState !== 'connected' && Date.now() < deadline) {
      if (pc.connectionState === 'failed') break
      await new Promise((ok) => setTimeout(ok, 100))
    }
    const state = pc.connectionState
    await new Promise((ok) => setTimeout(ok, 2000))
    let received = 0, codec = ''
    const stats = await pc.getStats()
    stats.forEach((s) => {
      if (s.type === 'inbound-rtp' && s.kind === 'audio') received = s.packetsReceived
      if (s.type === 'codec' && s.mimeType?.startsWith('audio/')) codec = s.mimeType
    })
    pc.close()
    return { state, received, codec, ice: pc.iceConnectionState }
  })
  console.log(JSON.stringify(result))
  expect(result.error).toBeUndefined()
  expect(result.state).toBe('connected')
  expect(result.received).toBeGreaterThan(20)
})

// Two browser phones call each other through the call engine: each is registered to it as a device of its
// extension, so this is a real extension-to-extension call with WebRTC on both ends.
test('browser phone to browser phone through the engine', async ({ page }) => {
  await signIn(page)
  const result = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const post = async (url: string, body: unknown) => {
      const r = await fetch(url, { method: 'POST', headers: H, body: JSON.stringify(body) })
      const j = await r.json().catch(() => ({}))
      if (!r.ok) throw new Error(url + ': ' + j.message)
      return j
    }
    const exts = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
    if (exts.length < 2) {
      await post('./api/extensions', { number: '177', name: 'Second browser', enabled: true, outbound: true, voicemail: { enabled: false }, record: false, secureMedia: false })
    }
    const all = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
    const [a, b] = [all[0].number, all[1].number]
    const pa = await post('./api/phone', { extension: a })
    const pb = await post('./api/phone', { extension: b })
    const events: Record<string, string[]> = { a: [], b: [] }
    let incoming: (id: string) => void = () => {}
    const gotIncoming = new Promise<string>((ok) => { incoming = ok })
    const esA = new EventSource(`./api/phone/${pa.id}/events`)
    esA.addEventListener('call', (e) => events.a.push(JSON.parse((e as MessageEvent).data).type))
    const esB = new EventSource(`./api/phone/${pb.id}/events`)
    esB.addEventListener('call', (e) => {
      const ev = JSON.parse((e as MessageEvent).data)
      events.b.push(ev.type)
      if (ev.type === 'incoming') incoming(ev.callId)
    })
    await new Promise((ok) => setTimeout(ok, 300))
    const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
    const peer = async () => {
      const pc = new RTCPeerConnection()
      mic.getTracks().forEach((t) => pc.addTrack(t, mic))
      await pc.setLocalDescription(await pc.createOffer())
      return pc
    }
    const pcA = await peer()
    const call = await post(`./api/phone/${pa.id}/calls`, { to: b, sdp: pcA.localDescription!.sdp })
    await pcA.setRemoteDescription({ type: 'answer', sdp: call.sdp })
    const callId = await Promise.race([gotIncoming, new Promise<string>((_, no) => setTimeout(() => no(new Error('no incoming call')), 8000))])
    const pcB = await peer()
    const ans = await post(`./api/phone/${pb.id}/answer`, { callId, sdp: pcB.localDescription!.sdp })
    await pcB.setRemoteDescription({ type: 'answer', sdp: ans.sdp })
    await new Promise((ok) => setTimeout(ok, 3000))
    const inbound = async (pc: RTCPeerConnection) => {
      let n = 0
      ;(await pc.getStats()).forEach((s) => { if (s.type === 'inbound-rtp') n = s.packetsReceived })
      return n
    }
    const res = { stateA: pcA.connectionState, stateB: pcB.connectionState, recvA: await inbound(pcA), recvB: await inbound(pcB), events }
    await fetch(`./api/phone/${pa.id}/calls/${call.callId}`, { method: 'DELETE', headers: H })
    await new Promise((ok) => setTimeout(ok, 800))
    esA.close(); esB.close(); pcA.close(); pcB.close()
    return res
  })
  console.log(JSON.stringify(result))
  expect(result.stateA).toBe('connected')
  expect(result.stateB).toBe('connected')
  expect(result.recvA).toBeGreaterThan(20)
  expect(result.recvB).toBeGreaterThan(20)
  expect(result.events.b).toContain('ended')
})

// Opus against Chromium's own libopus: phone A speaks Opus, phone B is held to G.711, so BitVoice
// decodes A's Opus for B and encodes B's audio as Opus for A. What each browser receives is decoded
// in the page (Opus with WebCodecs, which is Chromium's libopus; µ-law by its table) and must carry
// the fake microphone's tone, not silence.
test('Opus converted both ways against the browser', async ({ page }) => {
  await signIn(page)
  const result = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const post = async (url: string, body: unknown) => {
      const r = await fetch(url, { method: 'POST', headers: H, body: JSON.stringify(body) })
      const j = await r.json().catch(() => ({}))
      if (!r.ok) throw new Error(url + ': ' + j.message)
      return j
    }
    const exts = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
    if (exts.length < 2) {
      await post('./api/extensions', { number: '177', name: 'Second browser', enabled: true, outbound: true, voicemail: { enabled: false }, record: false, secureMedia: false })
    }
    const all = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
    const [a, b] = [all[0].number, all[1].number]
    const pa = await post('./api/phone', { extension: a })
    const pb = await post('./api/phone', { extension: b })
    let incoming: (id: string) => void = () => {}
    const gotIncoming = new Promise<string>((ok) => { incoming = ok })
    const esB = new EventSource(`./api/phone/${pb.id}/events`)
    esB.addEventListener('call', (e) => { const ev = JSON.parse((e as MessageEvent).data); if (ev.type === 'incoming') incoming(ev.callId) })
    await new Promise((ok) => setTimeout(ok, 300))
    const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
    const ulaw = (u: number) => { u = ~u & 0xff; const t = (((u & 0x0f) << 3) + 0x84) << ((u & 0x70) >> 4); return (u & 0x80 ? 0x84 - t : t - 0x84) / 32768 }
    type Heard = { frames: number; peak: number; errors: number; loud: number; crossings: number; hz: number }
    // The pitch of the loud parts, by counting zero crossings: a tone stays a tone, garbage is noise.
    const measure = (h: Heard, f: ArrayLike<number>, rate: number) => {
      const block = rate / 100 // 10 ms
      for (let s = 0; s + block <= f.length; s += block) {
        let e = 0, z = 0
        for (let i = s; i < s + block; i++) {
          e += f[i] * f[i]; h.peak = Math.max(h.peak, Math.abs(f[i]))
          if (i > s && (f[i] < 0) !== (f[i - 1] < 0)) z++
        }
        if (Math.sqrt(e / block) > 0.05) { h.loud += block; h.crossings += z }
      }
      if (h.loud) h.hz = Math.round((h.crossings / h.loud) * rate / 2)
    }
    const listen = (pc: RTCPeerConnection, heard: Heard) => {
      let dec: AudioDecoder | null = null
      pc.ontrack = (e) => {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const { readable, writable } = (e.receiver as any).createEncodedStreams()
        readable.pipeThrough(new TransformStream({
          transform(frame: { data: ArrayBuffer; timestamp: number }, ctl) {
            heard.frames++
            const codec = (e.receiver.getParameters().codecs[0]?.mimeType ?? '').toLowerCase()
            if (codec === 'audio/pcmu') {
              measure(heard, Array.from(new Uint8Array(frame.data), ulaw), 8000)
            } else if (codec === 'audio/opus') {
              if (!dec) {
                dec = new AudioDecoder({ output: (d) => { const all = new Float32Array(d.numberOfFrames * d.numberOfChannels); d.copyTo(all, { planeIndex: 0, format: 'f32' }); const f = all.filter((_, i) => i % d.numberOfChannels === 0); measure(heard, f, d.sampleRate); d.close() }, error: () => { heard.errors++ } })
                dec.configure({ codec: 'opus', sampleRate: 48000, numberOfChannels: 2 })
              }
              dec.decode(new EncodedAudioChunk({ type: 'key', timestamp: heard.frames * 20000, data: frame.data }))
            }
            ctl.enqueue(frame)
          },
        })).pipeTo(writable)
      }
    }
    const peer = async (heard: Heard, only?: string) => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const pc = new RTCPeerConnection({ encodedInsertableStreams: true } as any)
      listen(pc, heard)
      const tr = pc.addTransceiver(mic.getTracks()[0], { direction: 'sendrecv', streams: [mic] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const s = (tr.sender as any).createEncodedStreams(); s.readable.pipeTo(s.writable)
      if (only) tr.setCodecPreferences(RTCRtpReceiver.getCapabilities('audio')!.codecs.filter((c) => c.mimeType === only || c.mimeType === 'audio/telephone-event'))
      await pc.setLocalDescription(await pc.createOffer())
      return pc
    }
    const blank = (): Heard => ({ frames: 0, peak: 0, errors: 0, loud: 0, crossings: 0, hz: 0 })
    const heardA = blank(), heardB = blank()
    const pcA = await peer(heardA)
    const call = await post(`./api/phone/${pa.id}/calls`, { to: b, sdp: pcA.localDescription!.sdp })
    await pcA.setRemoteDescription({ type: 'answer', sdp: call.sdp })
    const callId = await Promise.race([gotIncoming, new Promise<string>((_, no) => setTimeout(() => no(new Error('no incoming call')), 8000))])
    const pcB = await peer(heardB, 'audio/PCMU')
    const ans = await post(`./api/phone/${pb.id}/answer`, { callId, sdp: pcB.localDescription!.sdp })
    await pcB.setRemoteDescription({ type: 'answer', sdp: ans.sdp })
    await new Promise((ok) => setTimeout(ok, 4000))
    const codecs = [pcA, pcB].map((pc) => pc.getReceivers()[0]?.getParameters().codecs[0]?.mimeType)
    await fetch(`./api/phone/${pa.id}/calls/${call.callId}`, { method: 'DELETE', headers: H })
    await new Promise((ok) => setTimeout(ok, 500))
    esB.close(); pcA.close(); pcB.close()
    return { codecs, a: heardA, b: heardB }
  })
  console.log(JSON.stringify(result))
  expect(result.codecs).toEqual(['audio/opus', 'audio/PCMU'])
  for (const side of [result.a, result.b]) {
    expect(side.frames).toBeGreaterThan(50)
    expect(side.errors).toBe(0)
    expect(side.peak).toBeGreaterThan(0.05)
  }
  // Both fake microphones play the same beep, so both directions must hear about the same pitch. Counting
  // zero crossings in 10 ms blocks is a rough measure (blocks at the start and end of each beep count too):
  // it has read 334 to 408 Hz for the same beep on different machines, so it allows 30%. Garbage or noise
  // reads far above 1 kHz.
  for (const side of [result.a, result.b]) expect(side.hz).toBeLessThan(1000)
  expect(Math.abs(result.a.hz - result.b.hz)).toBeLessThan(result.b.hz * 0.3)
})

// The docked browser phone, used the way a person would: sign in as an extension, dial the echo test.
test('the browser phone in the GUI calls the echo test', async ({ page }) => {
  await signIn(page)
  await page.getByRole('button', { name: 'Browser phone' }).click()
  await page.getByRole('button', { name: /^Sign in as/ }).click()
  await expect(page.getByText(/^Extension/).first()).toBeVisible()
  for (const k of ['*', '4', '3']) await page.getByRole('group', { name: 'Keypad' }).getByRole('button', { name: k, exact: true }).click()
  await page.getByRole('button', { name: 'Call', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Browser phone' }).getByText(/^0:0[1-9]$/)).toBeVisible({ timeout: 10000 })
  await page.getByRole('button', { name: 'Hang up' }).click()
  await expect(page.getByText('you hung up')).toBeVisible()
})

test('a greeting recorded in the browser is saved and plays back', async ({ page }) => {
  await signIn(page)
  await page.goto('./#/extensions')
  await page.getByRole('cell', { name: '100', exact: true }).click()
  const d = page.getByRole('dialog')
  await d.getByRole('button', { name: 'Record greeting in the browser' }).click()
  await expect(d.getByRole('button', { name: /Stop/ })).toBeVisible()
  await page.waitForTimeout(1500)
  await d.getByRole('button', { name: /Stop/ }).click()
  await expect(page.getByText('Greeting saved')).toBeVisible()
  const dur = await d.locator('audio').evaluate(async (a: HTMLAudioElement) => {
    a.load()
    await new Promise((ok) => { a.onloadedmetadata = ok; a.onerror = ok })
    return a.duration
  })
  expect(dur).toBeGreaterThan(0.5)
})

// A call link: someone who is not signed in opens the link's page, gives a name and calls; BitVoice
// answers it (voicemail here) and the call shows who it is from.
test('a call link places a browser call without signing in', async ({ page, browser }) => {
  await signIn(page)
  const slug = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const exts = await (await fetch('./api/extensions')).json()
    const box = exts.items.find((x: { voicemail: { enabled: boolean } }) => x.voicemail.enabled).number
    const s = await (await fetch('./api/settings')).json()
    s.system.callLinks = [{ name: 'Support', enabled: true, askName: true, title: 'Call support', intro: 'We answer quickly.', destination: { type: 'voicemail', target: box } }]
    const r = await fetch('./api/settings', { method: 'PUT', headers: H, body: JSON.stringify(s) })
    const saved = await r.json()
    return saved.system.callLinks[0].slug as string
  })
  expect(slug).toMatch(/^[a-z0-9-]{8,}$/)
  const guest = await browser.newContext({ permissions: ['microphone'] })
  const p = await guest.newPage()
  await p.goto(`/call/${slug}`)
  await expect(p.getByRole('heading', { name: 'Call support' })).toBeVisible()
  await expect(p.getByRole('button', { name: 'Call now' })).toBeDisabled()
  await p.getByLabel('Your name').fill('Jo Web')
  await p.getByRole('button', { name: 'Call now' }).click()
  await expect(p.getByRole('status')).toHaveText(/^0:0[1-9]$/, { timeout: 10000 })
  const from = await page.evaluate(async () => (await (await fetch('./api/calls')).json()).items.map((c: { fromName: string }) => c.fromName))
  expect(from).toContain('Web: Jo Web')
  await p.getByRole('button', { name: 'Hang up' }).click()
  await expect(p.getByText('Call ended. Thank you.')).toBeVisible()
  await guest.close()
  // An unknown address says so.
  const q = await browser.newPage()
  await q.goto('/call/nosuchlinkatall')
  await expect(q.getByRole('heading', { name: 'This link does not work' })).toBeVisible()
  await q.close()
})

// TURN: Chromium's own TURN client, told to use relays only, reaches the echo test through BitVoice's
// TURN server: over UDP, then over TCP alone (as from a network that blocks UDP).
test('browser audio relayed through BitVoice TURN', async ({ page }) => {
  await signIn(page)
  const ok = await page.evaluate(async () => {
    const s = await (await fetch('./api/settings')).json()
    s.system.turn = { ...(s.system.turn ?? {}), enabled: true, port: 3478, tlsPort: -1 }
    const r = await fetch('./api/settings', { method: 'PUT', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify(s) })
    return r.ok ? '' : (await r.json()).message
  })
  expect(ok).toBe('')
  await expect.poll(async () => page.evaluate(async () => (await (await fetch('./api/turn')).json()).running), { timeout: 5000 }).toBe(true)
  for (const transport of ['udp', 'tcp']) {
    const result = await page.evaluate(async (transport) => {
      const ice = (await (await fetch('./api/ice')).json()).iceServers as RTCIceServer[]
      const urls = (ice[0].urls as string[]).filter((u) => u.endsWith('transport=' + transport)).map((u) => u.replace(/^turn:[^:]+/, 'turn:127.0.0.1'))
      const pc = new RTCPeerConnection({ iceServers: [{ ...ice[0], urls }], iceTransportPolicy: 'relay' })
      const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
      mic.getTracks().forEach((t) => pc.addTrack(t, mic))
      await pc.setLocalDescription(await pc.createOffer())
      const r = await fetch('./api/phone/echo', { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' },
        body: JSON.stringify({ sdp: pc.localDescription!.sdp }) })
      const body = await r.json()
      if (!r.ok) return { error: body.message }
      await pc.setRemoteDescription({ type: 'answer', sdp: body.sdp })
      const deadline = Date.now() + 15000
      while (pc.connectionState !== 'connected' && Date.now() < deadline && pc.connectionState !== 'failed') await new Promise((ok) => setTimeout(ok, 100))
      const state = pc.connectionState
      await new Promise((ok) => setTimeout(ok, 2000))
      let received = 0, local = '', relayProto = ''
      const stats = await pc.getStats()
      const byId = new Map<string, any>()
      stats.forEach((s) => byId.set(s.id, s))
      stats.forEach((s) => {
        if (s.type === 'inbound-rtp' && s.kind === 'audio') received = s.packetsReceived
        if (s.type === 'candidate-pair' && s.nominated && s.state === 'succeeded') {
          const l = byId.get(s.localCandidateId)
          local = l?.candidateType ?? ''
          relayProto = l?.relayProtocol ?? ''
        }
      })
      const relays = (await (await fetch('./api/turn')).json()).relays
      pc.close()
      return { state, received, local, relayProto, relays }
    }, transport)
    console.log(transport, JSON.stringify(result))
    expect(result.error).toBeUndefined()
    expect(result.state).toBe('connected')
    // relay, or peer-reflexive through the relay (the relay's address as the echo test sees it); with
    // iceTransportPolicy relay Chromium sends nothing any other way.
    expect(['relay', 'prflx']).toContain(result.local)
    expect(result.relayProto).toBe(transport)
    expect(result.received).toBeGreaterThan(20)
    expect(result.relays).toBeGreaterThan(0)
  }
})

// SIP over WebSocket against JsSIP (an independent SIP stack, MIT, bundled here only for the test):
// two JsSIP phones register over wss://…/ws, one calls the other, and the sound flows both ways
// through the gateway's WebRTC endpoints and the call engine.
test('JsSIP softphones over SIP over WebSocket', async ({ page, context }) => {
  const { build } = await import('vite')
  const { resolve } = await import('node:path')
  const { readFileSync, mkdtempSync } = await import('node:fs')
  const { tmpdir } = await import('node:os')
  const out = mkdtempSync(resolve(tmpdir(), 'jssip-'))
  await build({ configFile: false, logLevel: 'silent', build: { write: true, outDir: out, emptyOutDir: true, minify: false, copyPublicDir: false,
    lib: { entry: resolve('node_modules/jssip/lib/JsSIP.js'), name: 'JsSIP', formats: ['iife'], fileName: () => 'jssip.js' } } })
  const jssip = readFileSync(resolve(out, 'jssip.js'), 'utf8')

  await signIn(page)
  const secret = 'Softphone-Secret-2468'
  const setup = await page.evaluate(async (secret) => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const s = await (await fetch('./api/settings')).json()
    s.system.webSocket = { enabled: true }
    let r = await fetch('./api/settings', { method: 'PUT', headers: H, body: JSON.stringify(s) })
    if (!r.ok) return (await r.json()).message
    for (const n of ['271', '272']) {
      r = await fetch('./api/extensions', { method: 'POST', headers: H, body: JSON.stringify({ number: n, name: 'Softphone ' + n, secret, enabled: true, outbound: true }) })
      if (!r.ok && r.status !== 409) return (await r.json()).message
    }
    return ''
  }, secret)
  expect(setup).toBe('')
  const bitvoice = new URL(page.url())

  // The softphone is a web page of its own, on another origin, as a real WebRTC softphone would be.
  await context.grantPermissions(['local-network-access']).catch(() => {})
  const phone = await context.newPage()
  await phone.route('https://localhost:9/**', (r) => r.fulfill({ contentType: 'text/html', body: '<!doctype html><title>softphone</title><script src="/jssip.js"></script>' }))
  await phone.route('https://localhost:9/jssip.js', (r) => r.fulfill({ contentType: 'text/javascript', body: jssip }))
  phone.on('console', (m) => { if (process.env.BV_DEBUG) console.log('PHONE', m.text().slice(0, 400)) })
  await phone.goto('https://localhost:9/')
  const result = await phone.evaluate(async ({ secret, host }) => {
    const J = (window as any).JsSIP
    J.debug.enable('JsSIP:Transport JsSIP:UA JsSIP:RTCSession JsSIP:WebSocketInterface')
    const url = `wss://${host}/ws`
    const name = host.split(':')[0]
    const ua = (user: string) => new J.UA({ sockets: [new J.WebSocketInterface(url)], uri: `sip:${user}@${name}`, password: secret, register: true })
    const a = ua('271'), b = ua('272')
    const registered = (u: any) => new Promise<void>((ok, bad) => { u.on('registered', () => ok()); u.on('registrationFailed', (e: any) => bad(new Error('registration failed: ' + e.cause))) })
    a.start(); b.start()
    await Promise.all([registered(a), registered(b)])
    const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
    let answered: any = null
    b.on('newRTCSession', (e: any) => {
      if (e.originator !== 'remote') return
      answered = e.session
      e.session.answer({ mediaStream: mic })
    })
    const s = a.call(`sip:272@${name}`, { mediaStream: mic })
    await new Promise<void>((ok, bad) => { s.on('confirmed', () => ok()); s.on('failed', (e: any) => bad(new Error('call failed: ' + e.cause))) })
    await new Promise((ok) => setTimeout(ok, 3000))
    const packets = async (sess: any) => {
      let n = 0
      const st = await sess.connection.getStats()
      st.forEach((x: any) => { if (x.type === 'inbound-rtp' && x.kind === 'audio') n = x.packetsReceived })
      return n
    }
    ;(window as any).end = async () => { s.terminate(); await new Promise((ok) => setTimeout(ok, 500)); a.stop(); b.stop() }
    return { caller: await packets(s), callee: answered ? await packets(answered) : -1, connected: s.connection.connectionState }
  }, { secret, host: bitvoice.host })
  const ws = await page.evaluate(async () => (await (await fetch('./api/websocket')).json()).connected)
  await phone.evaluate(async () => (window as any).end())
  console.log(JSON.stringify(result), ws)
  expect(result.connected).toBe('connected')
  expect(result.caller).toBeGreaterThan(50)
  expect(result.callee).toBeGreaterThan(50)
  expect(ws).toBe(2)
})
