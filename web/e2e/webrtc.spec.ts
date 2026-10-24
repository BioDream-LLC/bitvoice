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
    // hello comes once the stream is listening
    await Promise.all([esA, esB].map((es) => new Promise((ok) => es.addEventListener('hello', ok, { once: true }))))
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
    await new Promise((ok) => esB.addEventListener('hello', ok, { once: true })) // the stream is listening
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
for (const ice of ['lite', 'full'] as const) {
test(`JsSIP softphones over SIP over WebSocket (${ice} ICE)`, async ({ page, context }) => {
  const { build } = await import('vite')
  const { resolve } = await import('node:path')
  const { readFileSync, mkdtempSync, rmSync } = await import('node:fs')
  const { tmpdir } = await import('node:os')
  const out = mkdtempSync(resolve(tmpdir(), 'jssip-'))
  await build({ configFile: false, logLevel: 'silent', build: { write: true, outDir: out, emptyOutDir: true, minify: false, copyPublicDir: false,
    lib: { entry: resolve('node_modules/jssip/lib/JsSIP.js'), name: 'JsSIP', formats: ['iife'], fileName: () => 'jssip.js' } } })
  const jssip = readFileSync(resolve(out, 'jssip.js'), 'utf8')
  rmSync(out, { recursive: true, force: true })

  await signIn(page)
  const secret = 'Softphone-Secret-2468'
  const setup = await page.evaluate(async ({ secret, ice }) => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const s = await (await fetch('./api/settings')).json()
    s.system.webSocket = { enabled: true }
    // full: BitVoice as a full ICE agent, offering to the callee (controlling) and answering the caller.
    s.system.ice = ice === 'full' ? { full: true, stun: ['127.0.0.1:9'], turn: [], relayOnly: false } : { full: false }
    let r = await fetch('./api/settings', { method: 'PUT', headers: H, body: JSON.stringify(s) })
    if (!r.ok) return JSON.stringify(await r.json())
    const have = ((await (await fetch('./api/extensions')).json()).items as { number: string }[]).map((x) => x.number)
    for (const n of ['271', '272'].filter((n) => !have.includes(n))) {
      r = await fetch('./api/extensions', { method: 'POST', headers: H, body: JSON.stringify({ number: n, name: 'Softphone ' + n, secret, enabled: true, outbound: true }) })
      if (!r.ok && r.status !== 409) return (await r.json()).message
    }
    return ''
  }, { secret, ice })
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
  if (ice === 'full') {
    await page.evaluate(async () => {
      const s = await (await fetch('./api/settings')).json()
      s.system.ice = { full: false }
      await fetch('./api/settings', { method: 'PUT', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify(s) })
    })
  }
})
}

// The same against SIP.js (another independent SIP stack, MIT, bundled here only for the test): two
// SIP.js user agents register over wss://…/ws, one calls the other, and the sound flows both ways.
test('SIP.js softphones over SIP over WebSocket', async ({ page, context }) => {
  const { build } = await import('vite')
  const { resolve } = await import('node:path')
  const { readFileSync, mkdtempSync, rmSync } = await import('node:fs')
  const { tmpdir } = await import('node:os')
  const out = mkdtempSync(resolve(tmpdir(), 'sipjs-'))
  await build({ configFile: false, logLevel: 'silent', build: { write: true, outDir: out, emptyOutDir: true, minify: false, copyPublicDir: false,
    lib: { entry: resolve('node_modules/sip.js/lib/index.js'), name: 'SIP', formats: ['iife'], fileName: () => 'sip.js' } } })
  const sipjs = readFileSync(resolve(out, 'sip.js'), 'utf8')
  rmSync(out, { recursive: true, force: true })

  await signIn(page)
  const secret = 'Softphone-Secret-1357'
  const setup = await page.evaluate(async (secret) => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const s = await (await fetch('./api/settings')).json()
    s.system.webSocket = { enabled: true }
    let r = await fetch('./api/settings', { method: 'PUT', headers: H, body: JSON.stringify(s) })
    if (!r.ok) return (await r.json()).message
    for (const n of ['273', '274']) {
      r = await fetch('./api/extensions', { method: 'POST', headers: H, body: JSON.stringify({ number: n, name: 'SIP.js ' + n, secret, enabled: true, outbound: true }) })
      if (!r.ok && r.status !== 409) return (await r.json()).message
    }
    return ''
  }, secret)
  expect(setup).toBe('')
  const bitvoice = new URL(page.url())

  await context.grantPermissions(['local-network-access']).catch(() => {})
  const phone = await context.newPage()
  await phone.route('https://localhost:9/**', (r) => r.fulfill({ contentType: 'text/html', body: '<!doctype html><title>softphone</title><script src="/sip.js"></script>' }))
  // UTF-8 said outright: SIP.js's parser tables are characters above ASCII.
  await phone.route('https://localhost:9/sip.js', (r) => r.fulfill({ contentType: 'text/javascript; charset=utf-8', body: sipjs }))
  phone.on('console', (m) => { if (process.env.BV_DEBUG) console.log('PHONE', m.text().slice(0, 400)) })
  await phone.goto('https://localhost:9/')
  const result = await phone.evaluate(async ({ secret, host }) => {
    const S = (window as any).SIP
    const name = host.split(':')[0]
    const agent = (user: string) => new S.UserAgent({
      uri: S.UserAgent.makeURI(`sip:${user}@${name}`),
      transportOptions: { server: `wss://${host}/ws` },
      authorizationUsername: user, authorizationPassword: secret,
      logLevel: 'warn',
      sessionDescriptionHandlerFactoryOptions: { iceGatheringTimeout: 500 },
    })
    const a = agent('273'), b = agent('274')
    let incoming: any = null
    b.delegate = { onInvite: (inv: any) => { incoming = inv; inv.accept({ sessionDescriptionHandlerOptions: { constraints: { audio: true, video: false } } }) } }
    await a.start(); await b.start()
    const reg = async (u: any) => {
      const r = new S.Registerer(u)
      await new Promise<void>((ok, bad) => {
        r.stateChange.addListener((st: string) => { if (st === 'Registered') ok() })
        r.register({ requestDelegate: { onReject: (resp: any) => bad(new Error('registration refused ' + resp.message.statusCode)) } })
      })
    }
    await reg(a); await reg(b)
    const inviter = new S.Inviter(a, S.UserAgent.makeURI(`sip:274@${name}`), { sessionDescriptionHandlerOptions: { constraints: { audio: true, video: false } } })
    await new Promise<void>((ok, bad) => {
      inviter.stateChange.addListener((st: string) => { if (st === 'Established') ok(); if (st === 'Terminated') bad(new Error('call ended')) })
      inviter.invite().catch(bad)
    })
    await new Promise((ok) => setTimeout(ok, 3000))
    const packets = async (sess: any) => {
      let n = 0
      const st = await sess.sessionDescriptionHandler.peerConnection.getStats()
      st.forEach((x: any) => { if (x.type === 'inbound-rtp' && x.kind === 'audio') n = x.packetsReceived })
      return n
    }
    ;(window as any).end = async () => { await inviter.bye().catch(() => {}); await new Promise((ok) => setTimeout(ok, 500)); await a.stop(); await b.stop() }
    return { caller: await packets(inviter), callee: incoming ? await packets(incoming) : -1, connected: inviter.sessionDescriptionHandler.peerConnection.connectionState }
  }, { secret, host: bitvoice.host })
  await phone.evaluate(async () => (window as any).end())
  console.log(JSON.stringify(result))
  expect(result.connected).toBe('connected')
  expect(result.caller).toBeGreaterThan(50)
  expect(result.callee).toBeGreaterThan(50)
})

// Coaching notes: two browser phones make a recorded call; a stand-in AI service writes it out; in Call
// history the supervisor measures it, adds a note at a moment, sees it counted on Call quality, and
// removes it.
test('a supervisor writes coaching notes on a recorded call', async ({ page }) => {
  const { createServer } = await import('node:http')
  const ai = createServer((req, res) => {
    let b = ''
    req.on('data', (c) => (b += c))
    req.on('end', () => {
      const say = (content: string) => res.end(JSON.stringify({ choices: [{ message: { content } }] }))
      if (b.includes('input_audio')) return say('My boiler is leaking, can someone come today?')
      if (b.includes('supervisor')) return say('{"moodStart":"worried","moodEnd":"calm","observations":[]}')
      say('{"summary":"A leaking boiler; a visit was booked.","actionItems":["Send a plumber"]}')
    })
  })
  await new Promise<void>((r) => ai.listen(18397, '127.0.0.1', r))
  try {
    await signIn(page)
    const name = await page.evaluate(async () => {
      const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
      const send = async (method: string, url: string, body: unknown) => {
        const r = await fetch(url, { method, headers: H, body: JSON.stringify(body) })
        const j = await r.json().catch(() => ({}))
        if (!r.ok) throw new Error(url + ': ' + j.message)
        return j
      }
      const st = await (await fetch('./api/settings')).json()
      st.system.ai = { ...st.system.ai, enabled: true, baseUrl: 'http://127.0.0.1:18397', model: 'm', calls: false }
      st.system.recording = { ...st.system.recording, mode: 'all', notice: 'off' }
      await send('PUT', './api/settings', st)
      const exts = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
      if (exts.length < 2) await send('POST', './api/extensions', { number: '177', name: 'Second browser', enabled: true, outbound: true, voicemail: { enabled: false }, record: false, secureMedia: false })
      const all = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
      const [a, b] = [all[0].number, all[1].number]
      const pa = await send('POST', './api/phone', { extension: a })
      const pb = await send('POST', './api/phone', { extension: b })
      let incoming: (id: string) => void = () => {}
      const gotIncoming = new Promise<string>((ok) => { incoming = ok })
      const esB = new EventSource(`./api/phone/${pb.id}/events`)
      esB.addEventListener('call', (e) => { const ev = JSON.parse((e as MessageEvent).data); if (ev.type === 'incoming') incoming(ev.callId) })
      await new Promise((ok) => esB.addEventListener('hello', ok, { once: true })) // the stream is listening
      const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
      const peer = async () => {
        const pc = new RTCPeerConnection()
        mic.getTracks().forEach((t) => pc.addTrack(t, mic))
        await pc.setLocalDescription(await pc.createOffer())
        return pc
      }
      const pcA = await peer()
      const call = await send('POST', `./api/phone/${pa.id}/calls`, { to: b, sdp: pcA.localDescription!.sdp })
      await pcA.setRemoteDescription({ type: 'answer', sdp: call.sdp })
      const callId = await Promise.race([gotIncoming, new Promise<string>((_, no) => setTimeout(() => no(new Error('no incoming call')), 8000))])
      const pcB = await peer()
      const ans = await send('POST', `./api/phone/${pb.id}/answer`, { callId, sdp: pcB.localDescription!.sdp })
      await pcB.setRemoteDescription({ type: 'answer', sdp: ans.sdp })
      await new Promise((ok) => setTimeout(ok, 12000)) // insight looks at calls of 10 s or more
      await fetch(`./api/phone/${pa.id}/calls/${call.callId}`, { method: 'DELETE', headers: H })
      esB.close(); pcA.close(); pcB.close()
      for (let i = 0; i < 50; i++) {
        const recs = (await (await fetch('./api/cdr?limit=5')).json()).items as { recording?: string }[]
        const r = recs.find((x) => x.recording)
        if (r) return r.recording!
        await new Promise((ok) => setTimeout(ok, 200))
      }
      throw new Error('the call was not recorded')
    })
    await page.evaluate(async (n) => {
      await fetch(`./api/recordings/${n}/transcript`, { method: 'POST', headers: { 'X-BitVoice': '1' } })
      for (let i = 0; i < 100; i++) {
        const r = await fetch(`./api/recordings/${n}/transcript`)
        if (r.ok && (await r.json()).status !== 'pending') return
        await new Promise((ok) => setTimeout(ok, 200))
      }
    }, name)

    await page.goto('/#/history')
    await page.getByRole('button', { name: /^Transcript of / }).first().click()
    const drawer = page.getByRole('dialog')
    await expect(drawer.getByText('A leaking boiler; a visit was booked.')).toBeVisible()
    await drawer.getByRole('button', { name: 'Look at this call' }).click()
    await expect(drawer.getByText('worried')).toBeVisible()
    await expect(drawer.getByText('No notes yet.')).toBeVisible()
    // A moment that is not a time is refused in place.
    await drawer.getByRole('textbox', { name: 'Moment' }).fill('soon')
    await drawer.getByRole('textbox', { name: 'Coaching note' }).fill('Let the caller finish before answering')
    await drawer.getByRole('button', { name: 'Add note' }).click()
    await expect(drawer.getByRole('alert')).toContainText('m:ss')
    await drawer.getByRole('textbox', { name: 'Moment' }).fill('0:04')
    await drawer.getByRole('button', { name: 'Add note' }).click()
    await expect(page.getByText('Note added')).toBeVisible()
    await expect(drawer.getByText('Let the caller finish before answering')).toBeVisible()
    await expect(drawer.getByText('0:04')).toBeVisible()
    await page.keyboard.press('Escape')

    await page.goto('/#/quality')
    const row = page.getByRole('region', { name: 'Conversations, by who took them' }).getByRole('row').nth(1)
    await expect(row.getByRole('cell').last()).toHaveText('1')

    await page.goto('/#/history')
    await page.getByRole('button', { name: /^Transcript of / }).first().click()
    await drawer.getByRole('button', { name: 'Remove the note at 0:04' }).click()
    await expect(drawer.getByText('No notes yet.')).toBeVisible()
  } finally {
    ai.close()
  }
})

// A script that runs the whole call: made on Scripts, tried with keys the caller would press, then
// dialled from the browser phone, which hears it and is hung up on by it.
test('a call script runs a call from the browser phone', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/scripts')
  await page.getByRole('button', { name: 'Add script' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Front door app')
  await page.getByRole('radio', { name: 'Runs the whole call' }).click()
  await expect(page.getByLabel('Number to dial it (optional)')).toBeVisible()
  // The call example is in the editor: try it with 1 pressed.
  await page.getByLabel('Keys the caller presses').fill('1')
  await page.getByRole('button', { name: 'Run', exact: true }).click()
  await expect(page.getByRole('list', { name: 'Steps' }).getByText('collect up to 1 keys')).toBeVisible()
  await expect(page.getByText('ringgroup 600')).toBeVisible()
  // A script of its own, for the real call: a beep, two seconds, then goodbye.
  await page.getByRole('textbox', { name: 'Script' }).fill('play("beep")\nwait(2)\nlog("done")\nhangup()')
  await page.getByLabel('Keys the caller presses').fill('')
  await page.getByRole('button', { name: 'Run', exact: true }).click()
  await expect(page.getByRole('list', { name: 'Steps' }).getByText('hang up')).toBeVisible()
  await expect(page.getByText('the script hung up or handed the call on')).toBeVisible()
  await page.getByLabel('Number to dial it (optional)').fill('851')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('runs the call · dial 851')).toBeVisible()

  await page.getByRole('button', { name: 'Browser phone' }).click()
  await page.getByRole('button', { name: /^Sign in as/ }).click()
  await expect(page.getByText(/^Extension/).first()).toBeVisible()
  for (const k of ['8', '5', '1']) await page.getByRole('group', { name: 'Keypad' }).getByRole('button', { name: k, exact: true }).click()
  await page.getByRole('button', { name: 'Call', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Browser phone' }).getByText(/^0:0[1-9]$/)).toBeVisible({ timeout: 10000 })
  await expect(page.getByText('the other party hung up')).toBeVisible({ timeout: 15000 })
  // The run list says how it went.
  await expect(page.getByText('ended: the script hung up')).toBeVisible()
  await expect(page.getByText('play beep → wait 2 s → hang up')).toBeVisible()
})

// An ARI application (Asterisk's Stasis): a program in the page connects to /ari/events, the browser
// phone dials the application's number, and the program answers it, plays to it and hangs it up.
test('an ARI application runs a call from the browser phone', async ({ page }) => {
  await signIn(page)
  // ARI on, with a login that may change calls.
  await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const s = await (await fetch('./api/settings')).json()
    s.system.compat.ari = true
    s.system.compat.users = [...(s.system.compat.users ?? []).filter((u: { username: string }) => u.username !== 'ivrapp'),
      { username: 'ivrapp', secret: 'a long ivr app secret', write: true }]
    await fetch('./api/settings', { method: 'PUT', headers: H, body: JSON.stringify(s) })
  })
  // The application, added on Settings.
  await page.goto('/#/settings')
  await page.getByRole('button', { name: 'Add application' }).click()
  const row = page.getByRole('listitem').filter({ has: page.getByLabel('Application name') }).last()
  await row.getByLabel('Application name').fill('e2eivr')
  await row.getByLabel('Number to dial it').fill('852')
  await row.getByLabel('Fallback').selectOption({ label: 'Echo test' })
  await page.getByRole('button', { name: 'Save' }).first().click()
  await expect(page.getByText('Settings saved')).toBeVisible()

  // The program: on StasisStart it plays a beep; when that finishes it hangs up.
  await page.evaluate(() => {
    const w = window as unknown as { ari: string[] }
    w.ari = []
    const key = 'api_key=ivrapp:' + encodeURIComponent('a long ivr app secret')
    const ws = new WebSocket(location.origin.replace('https', 'wss') + '/ari/events?app=e2eivr&' + key)
    ws.onmessage = async (m) => {
      const ev = JSON.parse(m.data)
      if (!['StasisStart', 'PlaybackStarted', 'PlaybackFinished', 'StasisEnd'].includes(ev.type)) return
      w.ari.push(ev.type)
      const ch = ev.channel?.id ?? ev.playback?.target_uri?.replace('channel:', '')
      if (ev.type === 'StasisStart') {
        await fetch(`./ari/channels/${ch}/answer?${key}`, { method: 'POST' })
        await fetch(`./ari/channels/${ch}/play?media=tone:beep,digits:42&${key}`, { method: 'POST' })
      }
      if (ev.type === 'PlaybackFinished') await fetch(`./ari/channels/${ch}?${key}`, { method: 'DELETE' })
    }
    return new Promise((ok) => { ws.onopen = ok })
  })
  await expect.poll(async () => page.evaluate(async () =>
    (await (await fetch('./ari/applications/e2eivr?api_key=ivrapp:' + encodeURIComponent('a long ivr app secret'))).json()).connected)).toBe(true)

  await page.getByRole('button', { name: 'Browser phone' }).click()
  await page.getByRole('button', { name: /^Sign in as/ }).click()
  await expect(page.getByText(/^Extension/).first()).toBeVisible()
  for (const k of ['8', '5', '2']) await page.getByRole('group', { name: 'Keypad' }).getByRole('button', { name: k, exact: true }).click()
  await page.getByRole('button', { name: 'Call', exact: true }).click()
  await expect(page.getByText('the other party hung up')).toBeVisible({ timeout: 15000 })
  await expect.poll(() => page.evaluate(() => (window as unknown as { ari: string[] }).ari.join(' ')))
    .toBe('StasisStart PlaybackStarted PlaybackFinished StasisEnd')
})

// Someone who is not an administrator signs the browser phone in with the extension's own password.
test('a viewer uses the browser phone with the extension password', async ({ page, browser }) => {
  await signIn(page)
  const secret = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    await fetch('./api/users', { method: 'POST', headers: H, body: JSON.stringify({ username: 'phoneviewer', name: 'Phone viewer', role: 'viewer', password: 'a viewer password here' }) })
    const exts = await (await fetch('./api/extensions')).json()
    return { number: exts.items[0].number as string, secret: exts.items[0].secret as string }
  })
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, permissions: ['microphone'] })
  const v = await ctx.newPage()
  await v.goto(page.url().split('#')[0])
  await v.getByLabel('Username').fill('phoneviewer')
  await v.getByLabel('Password').fill('a viewer password here')
  await v.getByRole('button', { name: 'Sign in' }).click()
  await expect(v.getByRole('navigation', { name: 'Main' })).toBeVisible({ timeout: 15000 })
  await v.getByRole('button', { name: 'Browser phone' }).click()
  await v.getByRole('textbox', { name: 'Extension', exact: true }).fill(secret.number)
  await v.getByLabel('SIP password').fill('wrong password')
  await v.getByRole('button', { name: /^Sign in as/ }).click()
  await expect(v.getByText('that extension and password do not match')).toBeVisible()
  await v.getByLabel('SIP password').fill(secret.secret)
  await v.getByRole('button', { name: /^Sign in as/ }).click()
  for (const k of ['*', '4', '3']) await v.getByRole('group', { name: 'Keypad' }).getByRole('button', { name: k, exact: true }).click()
  await v.getByRole('button', { name: 'Call', exact: true }).click()
  await expect(v.getByRole('region', { name: 'Browser phone' }).getByText(/^0:0[1-9]$/)).toBeVisible({ timeout: 10000 })
  await v.getByRole('button', { name: 'Hang up' }).click()
  await ctx.close()
})

// An AGI script (Asterisk's AGI()): a FastAGI server in the test runner, added on Settings and tried
// with Test; the browser phone dials it, the script beeps, writes a line to the log and hangs up.
// A sound uploaded for AGI scripts shows in their library.
test('an AGI script runs a call from the browser phone', async ({ page }) => {
  const { createServer } = await import('node:net')
  const heard: string[] = []
  const agi = createServer((sock) => {
    let buf = ''
    let step = 0
    const script = ['STREAM FILE beep ""', 'VERBOSE "e2e agi ran" 1', 'HANGUP']
    sock.on('data', (d) => {
      buf += d.toString()
      for (;;) {
        const i = buf.indexOf('\n')
        if (i < 0) return
        const line = buf.slice(0, i)
        buf = buf.slice(i + 1)
        if (step === 0) {
          if (line.startsWith('agi_')) { heard.push(line); continue }
          if (line === '') { if (heard.length === 0) { sock.end(); return } } // Test only connects
        } else heard.push(line)
        if (step < script.length) sock.write(script[step++] + '\n')
        else sock.end()
      }
    })
    sock.on('error', () => {})
  })
  await new Promise<void>((ok) => agi.listen(0, '127.0.0.1', ok))
  const port = (agi.address() as { port: number }).port
  try {
    await signIn(page)
    await page.goto('/#/settings')
    await page.getByRole('button', { name: 'Add AGI script' }).click()
    const row = page.getByRole('listitem').filter({ has: page.getByLabel('Script name') }).last()
    await row.getByLabel('Script name').fill('e2eagi')
    await row.getByLabel('Address').fill('lookup.php')
    await expect(row.getByText('A program in the AGI programs folder, run for each call.')).toBeVisible()
    await expect(row.getByRole('switch', { name: /^EAGI/ })).toBeVisible() // EAGI is for programs only
    await row.getByLabel('Address').fill('nothing/../bad')
    await page.getByRole('button', { name: 'Save' }).first().click()
    await expect(row.getByText(/agi:\/\/host:port\/script/)).toBeVisible() // refused in place
    await row.getByLabel('Address').fill(`agi://127.0.0.1:${port}/e2e?x=1`)
    await expect(row.getByRole('switch', { name: /^EAGI/ })).toHaveCount(0)
    await row.getByRole('button', { name: 'Test' }).click()
    await expect(row.getByRole('status')).toContainText(`connected to 127.0.0.1:${port}`)
    await row.getByLabel('Number to dial it').fill('853')
    await row.getByLabel('Fallback').selectOption({ label: 'Echo test' })
    await page.getByRole('button', { name: 'Save' }).first().click()
    await expect(page.getByText('Settings saved')).toBeVisible()

    // A sound for the scripts, uploaded into custom/.
    const wav = Buffer.alloc(44 + 8000)
    wav.write('RIFF', 0); wav.writeUInt32LE(36 + 8000, 4); wav.write('WAVEfmt ', 8); wav.writeUInt32LE(16, 16)
    wav.writeUInt16LE(1, 20); wav.writeUInt16LE(1, 22); wav.writeUInt32LE(8000, 24); wav.writeUInt32LE(16000, 28)
    wav.writeUInt16LE(2, 32); wav.writeUInt16LE(16, 34); wav.write('data', 36); wav.writeUInt32LE(8000, 40)
    await page.getByLabel('Sound files for AGI scripts').setInputFiles({ name: 'welcome.wav', mimeType: 'audio/wav', buffer: wav })
    await expect(page.getByRole('list', { name: 'AGI sounds' }).getByText('custom/welcome')).toBeVisible()

    await page.getByRole('button', { name: 'Browser phone' }).click()
    await page.getByRole('button', { name: /^Sign in as/ }).click()
    await expect(page.getByText(/^Extension/).first()).toBeVisible()
    for (const k of ['8', '5', '3']) await page.getByRole('group', { name: 'Keypad' }).getByRole('button', { name: k, exact: true }).click()
    await page.getByRole('button', { name: 'Call', exact: true }).click()
    await expect(page.getByText('the other party hung up')).toBeVisible({ timeout: 15000 })
    await page.keyboard.press('Escape')
    const runs = page.getByRole('list', { name: 'Last AGI runs' })
    await expect(runs.getByText('e2e agi ran')).toBeVisible()
    await expect(runs.getByText('ended: the call ended')).toBeVisible()
    expect(heard).toContain('agi_network_script: e2e?x=1')
    expect(heard).toContain('agi_dnid: 853')
    expect(heard.filter((l) => l.startsWith('200 '))).toEqual(['200 result=0 endpos=4000', '200 result=1', '200 result=1'])
  } finally {
    agi.close()
  }
})

// The rest of the ARI from a program in the page: a call from the browser phone enters an application,
// which mutes it, holds and unholds it (ChannelHold, ChannelUnhold), sets a custom device state and a
// mailbox, sends a user event to itself and hangs up; Settings shows what the program keeps.
test('an ARI program uses hold, device states, mailboxes and user events', async ({ page }) => {
  await signIn(page)
  await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const s = await (await fetch('./api/settings')).json()
    s.system.compat.ari = true
    s.system.compat.users = [...(s.system.compat.users ?? []).filter((u: { username: string }) => u.username !== 'moreapp'),
      { username: 'moreapp', secret: 'a long more app secret', write: true }]
    s.system.compat.apps = [...(s.system.compat.apps ?? []).filter((a: { name: string }) => a.name !== 'e2emore'),
      { name: 'e2emore', number: '854', fallback: { type: 'echo', target: '' } }]
    const r = await fetch('./api/settings', { method: 'PUT', headers: H, body: JSON.stringify(s) })
    if (!r.ok) throw new Error(await r.text())
  })
  await page.evaluate(() => {
    const w = window as unknown as { more: string[] }
    w.more = []
    const key = 'api_key=moreapp:' + encodeURIComponent('a long more app secret')
    const ari = (method: string, path: string, body?: unknown) => fetch(`./ari/${path}${path.includes('?') ? '&' : '?'}${key}`,
      { method, headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined })
    const ws = new WebSocket(location.origin.replace('https', 'wss') + '/ari/events?app=e2emore&' + key)
    ws.onmessage = async (m) => {
      const ev = JSON.parse(m.data)
      if (!['StasisStart', 'ChannelHold', 'ChannelUnhold', 'DeviceStateChanged', 'ChannelUserevent', 'StasisEnd'].includes(ev.type)) return
      w.more.push(ev.type)
      const ch = ev.channel?.id
      if (ev.type === 'StasisStart') {
        await ari('POST', 'applications/e2emore/subscription?eventSource=deviceState:Custom:e2elamp')
        await ari('POST', `channels/${ch}/mute?direction=in`)
        await ari('POST', `channels/${ch}/hold`)
        await new Promise((ok) => setTimeout(ok, 500))
        await ari('DELETE', `channels/${ch}/hold`)
        await ari('PUT', 'deviceStates/Custom:e2elamp?deviceState=BUSY')
        await ari('PUT', 'mailboxes/frontdoor@external?newMessages=3&oldMessages=1')
        await ari('POST', 'asterisk/variable?variable=E2E_GLOBAL&value=set')
        await ari('POST', `events/user/e2edone?application=e2emore&source=channel:${ch}`, { variables: { ok: 'yes' } })
      }
      if (ev.type === 'ChannelUserevent') await ari('DELETE', `channels/${ev.channel.id}`)
    }
    return new Promise((ok) => { ws.onopen = ok })
  })
  await page.getByRole('button', { name: 'Browser phone' }).click()
  await page.getByRole('button', { name: /^Sign in as/ }).click()
  await expect(page.getByText(/^Extension/).first()).toBeVisible()
  for (const k of ['8', '5', '4']) await page.getByRole('group', { name: 'Keypad' }).getByRole('button', { name: k, exact: true }).click()
  await page.getByRole('button', { name: 'Call', exact: true }).click()
  await expect(page.getByText('the other party hung up')).toBeVisible({ timeout: 15000 })
  await expect.poll(() => page.evaluate(() => (window as unknown as { more: string[] }).more.join(' ')))
    .toBe('StasisStart ChannelHold ChannelUnhold DeviceStateChanged ChannelUserevent StasisEnd')
  await page.keyboard.press('Escape')
  await page.goto('/#/settings')
  await expect(page.getByRole('region', { name: 'Custom device states' }).getByText('Custom:e2elamp')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Custom device states' }).getByText('BUSY')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Mailboxes kept by programs' }).getByText('frontdoor@external')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Global variables and bridges' }).getByText('E2E_GLOBAL')).toBeVisible()
  await expect(page.getByRole('region', { name: 'ARI applications now' }).getByText('deviceState:Custom:e2elamp').or(
    page.getByRole('region', { name: 'ARI applications now' }).getByText(/subscribed to Custom:e2elamp/))).toBeVisible()
})

// BitVoice behind NAT: full ICE against Chromium's own ICE agent. First with its host candidates and
// a STUN server (BitVoice's own TURN server answers STUN); then relaying every call through a TURN
// server (its own, with credentials made for it): Chromium reaches BitVoice's relayed address, the
// audio comes back through it. Find my addresses shows the candidates beforehand.
test('full ICE behind NAT, with STUN and through a TURN relay', async ({ page }) => {
  await signIn(page)
  // Settings changed by merging a patch into system (data, not code: the page's CSP forbids eval).
  const put = async (system: Record<string, unknown>) => page.evaluate(async (patch) => {
    const s = await (await fetch('./api/settings')).json()
    for (const [k, v] of Object.entries(patch)) s.system[k] = { ...(s.system[k] ?? {}), ...(v as object) }
    const r = await fetch('./api/settings', { method: 'PUT', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify(s) })
    return r.ok ? '' : JSON.stringify(await r.json())
  }, system)
  try {
    expect(await put({ turn: { enabled: true, port: 3478, tlsPort: -1 } })).toBe('')
    await expect.poll(async () => page.evaluate(async () => (await (await fetch('./api/turn')).json()).running), { timeout: 5000 }).toBe(true)
    const creds = await page.evaluate(async () => (await (await fetch('./api/ice')).json()).iceServers[0] as { username: string; credential: string })

    // On Settings: STUN on this computer, then the probe.
    await page.goto('/#/settings')
    const card = page.getByRole('region', { name: 'BitVoice behind NAT' })
    await card.getByRole('switch', { name: 'This server is behind NAT' }).click()
    await card.getByLabel('STUN servers').fill('127.0.0.1:3478')
    await card.getByLabel('STUN servers').press('Enter')
    await card.getByRole('button', { name: 'Find my addresses' }).click()
    await expect(card.getByRole('list', { name: 'Addresses BitVoice offers browsers' }).getByText('this computer').first()).toBeVisible()
    await page.getByRole('button', { name: 'Save' }).first().click()
    await expect(page.getByText('Settings saved')).toBeVisible()

    const echo = async () => page.evaluate(async () => {
      const pc = new RTCPeerConnection()
      const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
      mic.getTracks().forEach((t) => pc.addTrack(t, mic))
      await pc.setLocalDescription(await pc.createOffer())
      await new Promise<void>((ok) => { if (pc.iceGatheringState === 'complete') ok(); else pc.onicegatheringstatechange = () => pc.iceGatheringState === 'complete' && ok(); setTimeout(ok, 3000) })
      const r = await fetch('./api/phone/echo', { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify({ sdp: pc.localDescription!.sdp }) })
      const body = await r.json()
      if (!r.ok) return { error: body.message as string }
      await pc.setRemoteDescription({ type: 'answer', sdp: body.sdp })
      const deadline = Date.now() + 15000
      while (pc.connectionState !== 'connected' && Date.now() < deadline && pc.connectionState !== 'failed') await new Promise((ok) => setTimeout(ok, 100))
      const state = pc.connectionState
      await new Promise((ok) => setTimeout(ok, 2000))
      let received = 0, remote = ''
      const stats = await pc.getStats()
      const byId = new Map<string, any>()
      stats.forEach((s) => byId.set(s.id, s))
      stats.forEach((s) => {
        if (s.type === 'inbound-rtp' && s.kind === 'audio') received = s.packetsReceived
        if (s.type === 'candidate-pair' && s.nominated && s.state === 'succeeded') remote = byId.get(s.remoteCandidateId)?.candidateType ?? ''
      })
      pc.close()
      return { state, received, remote, lite: /a=ice-lite/.test(body.sdp), types: [...(body.sdp as string).matchAll(/typ (\w+)/g)].map((m) => m[1]),
        offered: [...pc.localDescription!.sdp.matchAll(/a=candidate:(.*)/g)].map((m) => m[1]).join(' | '), answer: [...(body.sdp as string).matchAll(/a=candidate:(.*)/g)].map((m) => m[1]).join(' | ') }
    })
    const direct = await echo()
    console.log('direct', JSON.stringify(direct))
    expect(direct.error).toBeUndefined()
    expect(direct.lite).toBe(false) // a full agent now
    expect(direct.types).toContain('host')
    expect(direct.state).toBe('connected')
    expect(direct.received).toBeGreaterThan(20)

    // Every call relayed through the TURN server.
    expect(await put({ ice: { full: true, stun: [], relayOnly: true, turn: [{ server: '127.0.0.1:3478', username: creds.username, password: creds.credential }] } })).toBe('')
    // Chromium hides its addresses behind mDNS names. Where BitVoice cannot resolve them (Linux without
    // a responder), its relay lets in the address the browser signed in from, and, for a browser on
    // this computer, every address of this computer.
    const relayed = await echo()
    console.log('relayed', JSON.stringify(relayed))
    expect(relayed.error).toBeUndefined()
    expect(relayed.types).toEqual(['relay'])
    expect(relayed.state).toBe('connected')
    expect(relayed.remote).toBe('relay') // Chromium's chosen pair is to BitVoice's relayed address
    expect(relayed.received).toBeGreaterThan(20)
  } finally {
    // Put the settings back even when a check failed, or later tests run behind a relay.
    expect(await put({ ice: { full: false, stun: [], turn: [], relayOnly: false }, turn: { enabled: false } })).toBe('')
  }
})

// An SFU video room: three browsers send their camera once and each sees the other two, each in its
// own picture; the big tile gets the full size of a simulcast camera, the small ones the small size.
test("a video room with everyone's video (SFU, simulcast)", async ({ page, browser }) => {
  test.setTimeout(120_000)
  await signIn(page)
  await page.goto('/#/conferences')
  await page.getByRole('button', { name: 'Add conference room' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Camera circle')
  const number = await page.getByLabel('Number', { exact: true }).inputValue()
  await page.getByLabel('Video', { exact: true }).selectOption('sfu')
  await expect(page.getByText('each in its own picture, in the size they show it')).toBeVisible()
  await page.getByLabel('Video codec').selectOption('VP8')
  await page.getByRole('button', { name: 'Save' }).click()
  const card = page.getByRole('button', { name: new RegExp(`^${number} Camera circle`) })
  await expect(card.getByText("everyone's video")).toBeVisible()
  await page.locator(`a[href="#/video-room/${number}"]`).click()
  await expect(page.getByRole('heading', { name: new RegExp(`Video room ${number}`) })).toBeVisible()

  const others = []
  for (let i = 0; i < 2; i++) {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, permissions: ['camera', 'microphone'] })
    const p = await ctx.newPage()
    await signIn(p)
    await p.goto(`/#/video-room/${number}`)
    others.push(p)
  }
  for (const p of [page, ...others]) await p.getByRole('button', { name: 'Join with my camera' }).click()
  const sizes = async (p: typeof page) => p.evaluate(() => [...document.querySelectorAll<HTMLVideoElement>('video[data-publisher]')].map((v) => v.videoWidth))
  for (const p of [page, ...others]) {
    await expect(p.getByRole('button', { name: 'Leave' })).toBeVisible()
    await expect.poll(async () => (await sizes(p)).filter((w) => w > 0).length, { timeout: 30_000 }).toBe(2)
  }
  // The first tile is big (full size), the second small: a simulcast camera sends both sizes.
  await expect.poll(async () => { const [big, small] = await sizes(page); return big > small && small > 0 }, { timeout: 30_000 }).toBe(true)
  const before = await sizes(page)
  // Making the small one big swaps the sizes, switched at the next full picture.
  await page.getByRole('button', { name: 'Make big' }).click()
  await expect.poll(async () => { const [a, b] = await sizes(page); return a < b && a > 0 }, { timeout: 30_000 }).toBe(true)
  expect(before[0]).toBeGreaterThan(before[1])
  // The API lists three cameras, with their layers and viewers.
  const room = await page.evaluate(async (n) => (await (await fetch(`./api/video-rooms/${n}`)).json()), number)
  expect(room.publishers).toHaveLength(3)
  for (const p of room.publishers) {
    expect(p.layers.length).toBeGreaterThan(1)
    expect(p.views).toBe(2)
  }
  // Leaving takes the camera out of everyone else's room.
  await others[1].getByRole('button', { name: 'Leave' }).click()
  await expect.poll(async () => (await sizes(page)).length, { timeout: 15_000 }).toBe(1)
  for (const p of others) await p.context().close()
})

// Without a language model, a recorded call's summary is the facts it said, read by rules, each
// shown with the sentence it came from.
test('call facts and summary without a language model', async ({ page }) => {
  const { createServer } = await import('node:http')
  // A whisper-server stand-in: every stretch of speech "says" the same sentence.
  const stt = createServer((req, res) => {
    req.resume()
    req.on('end', () => res.end(JSON.stringify({ text: "There's a fire at 418 Maple Avenue, nobody's hurt." })))
  })
  await new Promise<void>((r) => stt.listen(18398, '127.0.0.1', r))
  const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
  try {
    await signIn(page)
    const name = await page.evaluate(async (H) => {
      const send = async (method: string, url: string, body: unknown) => {
        const r = await fetch(url, { method, headers: H, body: JSON.stringify(body) })
        const j = await r.json().catch(() => ({}))
        if (!r.ok) throw new Error(url + ': ' + j.message)
        return j
      }
      const st = await (await fetch('./api/settings')).json()
      st.system.ai = { ...st.system.ai, enabled: true, model: '', calls: false, transcribeUrl: 'http://127.0.0.1:18398/inference' }
      st.system.recording = { ...st.system.recording, mode: 'all', notice: 'off' }
      await send('PUT', './api/settings', st)
      const exts = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
      if (exts.length < 2) await send('POST', './api/extensions', { number: '177', name: 'Second browser', enabled: true, outbound: true, voicemail: { enabled: false }, record: false, secureMedia: false })
      const all = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
      const [a, b] = [all[0].number, all[1].number]
      const pa = await send('POST', './api/phone', { extension: a })
      const pb = await send('POST', './api/phone', { extension: b })
      let incoming: (id: string) => void = () => {}
      const gotIncoming = new Promise<string>((ok) => { incoming = ok })
      const esB = new EventSource(`./api/phone/${pb.id}/events`)
      esB.addEventListener('call', (e) => { const ev = JSON.parse((e as MessageEvent).data); if (ev.type === 'incoming') incoming(ev.callId) })
      await new Promise((ok) => esB.addEventListener('hello', ok, { once: true }))
      const mic = await navigator.mediaDevices.getUserMedia({ audio: true })
      const peer = async () => {
        const pc = new RTCPeerConnection()
        mic.getTracks().forEach((t) => pc.addTrack(t, mic))
        await pc.setLocalDescription(await pc.createOffer())
        return pc
      }
      const pcA = await peer()
      const before = ((await (await fetch('./api/cdr?limit=5')).json()).items as { id: string }[]).map((x) => x.id)
      const call = await send('POST', `./api/phone/${pa.id}/calls`, { to: b, sdp: pcA.localDescription!.sdp })
      await pcA.setRemoteDescription({ type: 'answer', sdp: call.sdp })
      const callId = await Promise.race([gotIncoming, new Promise<string>((_, no) => setTimeout(() => no(new Error('no incoming call')), 8000))])
      const pcB = await peer()
      const ans = await send('POST', `./api/phone/${pb.id}/answer`, { callId, sdp: pcB.localDescription!.sdp })
      await pcB.setRemoteDescription({ type: 'answer', sdp: ans.sdp })
      await new Promise((ok) => setTimeout(ok, 4000))
      await fetch(`./api/phone/${pa.id}/calls/${call.callId}`, { method: 'DELETE', headers: H })
      esB.close(); pcA.close(); pcB.close()
      for (let i = 0; i < 50; i++) {
        const recs = (await (await fetch('./api/cdr?limit=5')).json()).items as { id: string; recording?: string }[]
        const r = recs.find((x) => x.recording && !before.includes(x.id))
        if (r) return r.recording!
        await new Promise((ok) => setTimeout(ok, 200))
      }
      throw new Error('the call was not recorded')
    }, H)
    await page.evaluate(async (n) => {
      await fetch(`./api/recordings/${n}/transcript`, { method: 'POST', headers: { 'X-BitVoice': '1' } })
      for (let i = 0; i < 100; i++) {
        const r = await fetch(`./api/recordings/${n}/transcript`)
        if (r.ok && (await r.json()).status !== 'pending') return
        await new Promise((ok) => setTimeout(ok, 200))
      }
    }, name)
    await page.goto('/#/history')
    await page.getByRole('button', { name: /^Transcript of / }).first().click()
    const drawer = page.getByRole('dialog')
    await expect(drawer.getByText('Fire at 418 Maple Avenue. Nobody hurt. Hazards: fire.')).toBeVisible()
    await expect(drawer.getByText('by rules, with no language model')).toBeVisible()
    const facts = drawer.getByRole('list', { name: 'Facts said on the call' })
    const item = (label: string) => facts.getByRole('listitem').filter({ has: page.getByText(label, { exact: true }) })
    await expect(item('Where')).toContainText('418 maple avenue')
    await expect(item('Hurt')).toContainText('nobody')
    await expect(facts.getByRole('listitem').first()).toContainText("There's a fire at 418 Maple Avenue, nobody's hurt.")
  } finally {
    await page.evaluate(async (H) => {
      const st = await (await fetch('./api/settings')).json()
      st.system.ai = { ...st.system.ai, transcribeUrl: '', enabled: false }
      await fetch('./api/settings', { method: 'PUT', headers: H, body: JSON.stringify(st) })
    }, H).catch(() => {})
    stt.close()
  }
})
