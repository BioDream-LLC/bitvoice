import { expect, test } from '@playwright/test'
import dgram from 'node:dgram'
import net from 'node:net'
import { readFileSync } from 'node:fs'
import { signIn } from './helpers'

// The browser phone joins the radio console with Chromium's fake microphone.
test.use({ launchOptions: { args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'] } })

// The 911 profile, NG911 core, radio console and faxing, driven through their pages.

const gis = JSON.stringify({ type: 'FeatureCollection', features: [
  { type: 'Feature', geometry: { type: 'LineString', coordinates: [[-76.89, 40.26], [-76.88, 40.26]] },
    properties: { RCL_NGUID: 'rcl1', St_PreDir: 'N', St_Name: 'Front', St_PosTyp: 'St', FromAddr_L: 100, ToAddr_L: 198, Parity_L: 'E',
      FromAddr_R: 101, ToAddr_R: 199, Parity_R: 'O', IncMuni_L: 'Harrisburg', IncMuni_R: 'Harrisburg', State_L: 'PA', State_R: 'PA' } }] })

test('core services: policy rule, border control, address validation', async ({ page }) => {
  await signIn(page)
  // A trunk to reach PSAPs by.
  await page.evaluate(async () => {
    await fetch('./api/trunks', { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: 'PSAP trunk', enabled: true, host: '127.0.0.1', port: 5098, transport: 'udp', codecs: [] }) })
  })
  await page.goto('./#/core')
  const sec = page.locator('section[aria-labelledby="cs-h"]')
  await sec.getByRole('button', { name: 'Add a PSAP' }).click()
  await sec.locator('#csn0').fill('East'); await sec.locator('#csu0').fill('sip:sos@east.example.gov'); await sec.locator('#cst0').selectOption({ label: 'PSAP trunk' })
  await sec.getByRole('button', { name: 'Add a PSAP' }).click()
  await sec.locator('#csn1').fill('West'); await sec.locator('#csu1').fill('sip:sos@west.example.gov'); await sec.locator('#cst1').selectOption({ label: 'PSAP trunk' })
  // A rule left unfinished is refused with the problem shown where it is.
  await sec.getByRole('button', { name: 'Add a rule' }).click()
  await sec.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(sec.getByText('name the rule')).toBeVisible()
  await expect(sec.getByText('send it to a PSAP in the list above')).toBeVisible()
  await sec.locator('#crn0').fill('Fire to West')
  await sec.locator('#crs0').fill('urn:service:sos.fire'); await sec.locator('#crs0').blur()
  await sec.locator('#cro0').selectOption({ label: 'West' })
  await sec.getByRole('switch', { name: 'Check calls from the ESInet' }).click()
  await sec.locator('#cbm').fill('30')
  await sec.locator('#cbd').fill('not-an-address')
  await sec.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(sec.getByText('an address or a range, like 203.0.113.0/24')).toBeVisible()
  await sec.locator('#cbd').fill('203.0.113.0/24')
  await sec.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Core services saved')).toBeVisible()
  await page.reload()
  await expect(sec.locator('#crn0')).toHaveValue('Fire to West')
  await expect(sec.locator('#cbd')).toHaveValue('203.0.113.0/24')

  // Address validation.
  await page.locator('input[aria-label="GIS address data"]').setInputFiles({ name: 'rcl.geojson', mimeType: 'application/geo+json', buffer: Buffer.from(gis) })
  await expect(page.getByText('1 road segment and 0 address points read')).toBeVisible()
  const form = page.getByRole('form', { name: 'Check an address' })
  await form.locator('#cvHNO').fill('123'); await form.locator('#cvRD').fill('Front'); await form.locator('#cvSTS').fill('Street')
  await form.locator('#cvA3').fill('Harrisburg'); await form.locator('#cvA1').fill('PA')
  await form.getByRole('button', { name: 'Check' }).click()
  await expect(form.getByRole('status')).toContainText('Valid: found as a road centerline')
  await form.locator('#cvHNO').fill('900')
  await form.getByRole('button', { name: 'Check' }).click()
  await expect(form.getByRole('status')).toContainText('Not a valid address')
  await expect(form.getByRole('status')).toContainText('Number')
})

test('radio console setup and tiles', async ({ page }) => {
  await signIn(page)
  await page.goto('./#/radio')
  await expect(page.getByText('The console is off')).toBeVisible()
  await page.locator('#rcn').fill('800')
  await page.getByRole('button', { name: 'Add a channel' }).click()
  await page.locator('#rcnm0').fill('Fire dispatch')
  await page.locator('#rcg0').fill('no port here')
  await page.locator('section[aria-labelledby="rs-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('the gateway\'s address and port, like 10.0.0.40:5000')).toBeVisible()
  await page.locator('#rcg0').fill('127.0.0.1:47300')
  await page.getByRole('button', { name: 'Add a channel' }).click()
  await page.locator('#rcnm1').fill('Police 1'); await page.locator('#rcg1').fill('127.0.0.1:47302')
  await page.locator('#rcp1').fill('46000') // the same port as the first
  await page.locator('section[aria-labelledby="rs-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('another channel uses this port')).toBeVisible()
  await page.locator('#rcp1').fill('46100')
  await page.locator('section[aria-labelledby="rs-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Radio console saved')).toBeVisible()
  const tiles = page.getByRole('region', { name: 'Channels' })
  await expect(tiles.getByRole('article', { name: 'Fire dispatch' })).toBeVisible()
  await expect(tiles.getByRole('article', { name: 'Police 1' })).toBeVisible()
  // Not on the console yet: talking is not possible.
  await expect(tiles.getByRole('button', { name: 'Talk on Police 1' })).toBeDisabled()
  await expect(page.getByText(/Dial 800 from your phone/)).toBeVisible()
})

/** A stand-in home RFSS: answers the console's ISSI group call and can replay the voice of a real
 * ISSI capture (NIST's sample) to it. */
async function standInRFSS() {
  const sip = dgram.createSocket('udp4'), rtp = dgram.createSocket('udp4')
  await new Promise<void>((r) => sip.bind(0, '127.0.0.1', r))
  await new Promise<void>((r) => rtp.bind(0, '127.0.0.1', r))
  const data = readFileSync(new URL('../../internal/p25/testdata/issi.pcap', import.meta.url))
  const link = data.readUInt32LE(20), spurt: Buffer[] = []
  for (let off = 24; off + 16 <= data.length;) {
    const n = data.readUInt32LE(off + 8), fr = data.subarray(off + 16, off + 16 + n); off += 16 + n
    const ip = link === 1 ? fr.subarray(14) : fr
    if (ip.length < 28 || ip[0] >> 4 !== 4 || ip[9] !== 17) continue
    const u = ip.subarray((ip[0] & 15) * 4)
    if (u.readUInt16BE(2) === 25000) spurt.push(Buffer.from(u.subarray(20, u.readUInt16BE(4))))
  }
  let invite = '', peer: { address: string; port: number } | undefined
  const invites: string[] = []
  let byes = 0, requests = 0
  sip.on('message', (m, from) => {
    const s = m.toString()
    if (!/^(INVITE|BYE) /.test(s)) return
    if (s.startsWith('INVITE')) { invite = s.split('\r\n')[0]; invites.push(invite) }
    if (s.startsWith('BYE')) byes++
    const keep = s.split('\r\n').filter((l) => /^(Via|From|Call-ID|CSeq):/i.test(l)).join('\r\n')
    const to = /\r\nTo: (.*)/i.exec(s)![1]
    const body = s.startsWith('BYE') ? '' : `v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=TIA-P25-Groupcall\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio ${rtp.address().port} RTP/AVP 100\r\na=rtpmap:100 X-TIA-P25-IMBE/8000\r\n`
    sip.send(`SIP/2.0 200 OK\r\n${keep}\r\nTo: ${to};tag=rfss3\r\n${body ? 'Content-Type: application/sdp\r\n' : ''}Content-Length: ${body.length}\r\n\r\n${body}`, from.port, from.address)
  })
  rtp.on('message', (m, from) => {
    peer = from
    // A push-to-talk request (packet type 0) is granted: the grant carries the request's TSN.
    const p = m.subarray(12), nb = p[0] & 0x3f
    if (nb > 0 && (p[1] & 0x7f) === 1 && (p[1 + 4 * nb] & 0x7f) === 0) {
      requests++
      rtp.send(Buffer.concat([m.subarray(0, 12), Buffer.from([1, 0x81, 0, 0, 4, 1, 0, p[1 + 4 * nb + 2], 0])]), from.port, from.address)
    }
  })
  return {
    port: sip.address().port,
    invited: () => invite,
    invites: () => invites,
    byes: () => byes,
    requests: () => requests,
    linked: () => !!peer,
    replay: async () => {
      for (const b of spurt) {
        rtp.send(Buffer.concat([Buffer.from([0x80, 100, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1]), b]), peer!.port, peer!.address)
        await new Promise((r) => setTimeout(r, 20))
      }
    },
    close: () => { sip.close(); rtp.close() },
  }
}

test('radio console: a P25 talkgroup over the ISSI', async ({ page }) => {
  const rfss = await standInRFSS()
  try {
    await signIn(page)
    await page.goto('./#/radio')
    const setup = page.locator('section[aria-labelledby="rs-h"]')
    await page.getByRole('button', { name: 'Add a channel' }).click()
    await page.locator('#rcnm2').fill('County fire')
    await page.locator('#rck2').selectOption('p25')
    await expect(page.getByText('This console on the P25 system')).toBeVisible()
    await page.locator('#rcg2').fill('819')
    await page.locator('#rch2').fill('127.0.0.1:' + rfss.port)
    await page.locator('#rchi2').fill('3')
    await page.locator('#p25w').fill('bee07zz') // only hex digits stay, upper case
    await expect(page.locator('#p25w')).toHaveValue('BEE07')
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('the system ID: 3 hexadecimal digits, like AD5')).toBeVisible()
    await expect(page.getByText("this console's RFSS ID, 1 to 254")).toBeVisible()
    await page.locator('#p25s').fill('AD5')
    await page.locator('#p25r').fill('2')
    await page.locator('#p25u').fill('4097')
    await page.locator('#p25p').fill('25062')
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Radio console saved')).toBeVisible()

    // The group call comes up with the talkgroup's ISSI identity.
    const tile = page.getByRole('region', { name: 'Channels' }).getByRole('article', { name: 'County fire' })
    await expect(tile.getByText('P25', { exact: true })).toBeVisible({ timeout: 10_000 })
    expect(rfss.invited()).toBe('INVITE sip:bee07ad50333@p25dr;user=TIA-P25-SG SIP/2.0')
    await expect.poll(rfss.linked, { timeout: 10_000 }).toBe(true)

    // Recording on, an AES-256 key, and the talkgroup transmitting with it.
    await setup.getByRole('switch', { name: /Record every transmission/ }).click()
    await setup.getByRole('button', { name: 'Add a key' }).click()
    await page.locator('#pkn0').fill('Fire tac')
    await page.locator('#pki0').fill('257')
    await page.locator('#pkk0').fill('abcd')
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('64 hexadecimal digits for this algorithm')).toBeVisible()
    await page.locator('#pkk0').fill('00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff')
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Radio console saved').last()).toBeVisible()
    await expect(page.locator('#pkk0')).toHaveValue('') // never shown again
    await page.locator('#rctk2').selectOption({ label: 'Fire tac (AES-256)' })
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(tile.getByText('TX AES-256')).toBeVisible({ timeout: 10_000 })
    await expect(tile.getByText('P25', { exact: true })).toBeVisible({ timeout: 10_000 })

    // Radio 197 (0xc5) gets a name; a second name for it is refused.
    await setup.getByRole('button', { name: 'Name a radio' }).click()
    await page.locator('#rau0').fill('197'); await page.locator('#ran0').fill('Engine 5')
    await setup.getByRole('button', { name: 'Name a radio' }).click()
    await page.locator('#rau1').fill('197'); await page.locator('#ran1').fill('Engine 6')
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('this radio is named twice')).toBeVisible()
    await setup.getByRole('button', { name: 'Remove the name for unit 197' }).last().click()
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Radio console saved').last()).toBeVisible()

    // A radio talks: its name and unit ID show while it does, and go when it stops.
    const replay = rfss.replay()
    await expect(tile.getByText('Engine 5')).toBeVisible({ timeout: 5_000 })
    await expect(tile.getByText('(bee07.ad5.0000c5)')).toBeVisible()
    await replay
    await expect(tile.getByText('Engine 5')).toBeHidden({ timeout: 5_000 })
    // ... and is in the radio log, recorded, by name.
    const log = page.getByRole('region', { name: 'Radio log' })
    await expect(log.getByText('Engine 5 (bee07.ad5.0000c5)')).toBeVisible({ timeout: 10_000 })
    await log.getByRole('button', { name: 'Play' }).first().click()
    await expect(log.getByLabel(/Recording of County fire/)).toBeVisible()

    // A dispatcher joins with the browser phone, sends an alert tone and calls a radio.
    await page.getByRole('button', { name: 'Browser phone', exact: true }).click()
    await page.getByRole('button', { name: /^Sign in as/ }).click()
    await expect(page.getByText(/^Extension/).first()).toBeVisible()
    await page.getByRole('button', { name: 'Join with the browser phone' }).click()
    await expect(page.getByText(/On the console:/)).toBeVisible({ timeout: 10_000 })
    // Simulselect needs two channels ticked.
    const simul = page.getByRole('region', { name: 'Simulselect' })
    await expect(simul.getByRole('button', { name: /Talk on 0 channels/ })).toBeDisabled()
    const picks = page.getByRole('region', { name: 'Channels' }).getByRole('checkbox', { name: /^Simulselect / })
    await picks.nth(0).check(); await picks.nth(1).check()
    await expect(simul.getByRole('button', { name: 'Talk on 2 channels' })).toBeEnabled()
    await picks.nth(0).uncheck(); await picks.nth(1).uncheck()
    const before = rfss.requests()
    await tile.getByLabel('Alert tone on County fire').selectOption('beep')
    await expect.poll(rfss.requests, { timeout: 5_000 }).toBeGreaterThan(before)
    await expect(log.getByText('Beep')).toBeVisible({ timeout: 10_000 })
    await page.getByLabel('Call a radio (unit ID)').fill('197')
    await page.getByRole('form', { name: 'Call a radio' }).getByRole('button', { name: 'Call' }).click()
    const priv = page.getByRole('region', { name: 'Channels' }).getByRole('article', { name: 'Unit 197' })
    await expect(priv.getByText('Private call: unit 197')).toBeVisible({ timeout: 5_000 })
    expect(rfss.invites()).toContain('INVITE sip:bee07ad50000c5@p25dr;user=TIA-P25-SU SIP/2.0')
    const byes = rfss.byes()
    await priv.getByRole('button', { name: 'Hang up' }).click()
    await expect(priv).toBeHidden({ timeout: 5_000 })
    await expect.poll(rfss.byes, { timeout: 5_000 }).toBeGreaterThan(byes)
  } finally {
    rfss.close()
  }
})

test('send a fax: a note and a document, queued', async ({ page }) => {
  await signIn(page)
  await page.goto('./#/faxes')
  await page.getByRole('button', { name: 'Add fax number' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: 'Send a fax' })).toBeVisible()
  await page.getByRole('button', { name: 'Send a fax' }).click()
  const d = page.getByRole('dialog', { name: 'Send a fax' })
  await d.locator('#sft').fill('2125550199')
  await d.locator('#sfc').fill('Two pages to follow.')
  await d.getByLabel('Documents to fax').setInputFiles({ name: 'letter.txt', mimeType: 'text/plain', buffer: Buffer.from('Dear Sir,\nPlease find the form attached.\n') })
  await d.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByText('Fax to 2125550199 queued')).toBeVisible()
  // No outbound route: it waits to try again, and says so.
  await expect(page.getByRole('region', { name: /Sent and received/ }).getByText('2125550199').first()).toBeVisible()
})

test('911 calls page: settings and reports', async ({ page }) => {
  await signIn(page)
  await page.goto('./#/psap')
  await page.locator('#psa').fill('psap.county.example.gov')
  await page.locator('#psl').fill('not an address')
  await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.locator('section[aria-labelledby="pss-h"]').getByText(/host:port|address and port/).first()).toBeVisible()
  await page.locator('#psl').fill('127.0.0.1:4999')
  await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/911 settings saved|saved/i).first()).toBeVisible()
  await page.getByRole('tab', { name: /Answer times/ }).click()
  await expect(page.getByRole('region', { name: 'Answer times' })).toBeVisible()
  await page.getByRole('tab', { name: /i3 log/ }).click()
  await expect(page.getByRole('region', { name: 'i3 log' })).toBeVisible()
})

// TTY on 911 calls: the setting saves, and text to a call that does not exist is refused plainly.
test('911 calls page: TTY setting and text replies', async ({ page }) => {
  await signIn(page)
  await page.goto('./#/psap')
  const box = page.getByRole('checkbox', { name: /Listen for TTYs on 911 calls/ })
  await expect(box).toBeChecked()
  await box.uncheck()
  await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/911 settings saved/).first()).toBeVisible()
  expect((await page.evaluate(async () => (await (await fetch('./api/settings')).json()).system.psap)).ttyOff).toBe(true)
  await box.check()
  await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/911 settings saved/).first()).toBeVisible()
  const r = await page.evaluate(async () => {
    const x = await fetch('./api/calls/nope/text', { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify({ text: 'HELLO GA' }) })
    return { status: x.status, body: await x.json() }
  })
  expect(r.status).toBe(409)
  expect(JSON.stringify(r.body)).toContain('no such call')
})

/** A stand-in text control centre: offers text to 911 over SIP and MSRP, as a TCC does, and reads the
 * PSAP's replies. */
async function standInTCC() {
  const sip = dgram.createSocket('udp4')
  await new Promise<void>((r) => sip.bind(0, '127.0.0.1', r))
  const callId = `tcc-${Date.now()}`
  const replies: string[] = []
  let bye = false, answer = '', tag = ''
  sip.on('message', (m, from) => {
    const s = m.toString()
    if (s.startsWith('SIP/2.0 200') && /CSeq: 1 INVITE/i.test(s)) {
      answer = s.split('\r\n\r\n')[1]
      tag = /\r\nTo: (.*)/i.exec(s)![1]
      sip.send(`ACK sip:127.0.0.1:25060 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:${sip.address().port};branch=z9hG4bKack${Date.now()}\r\nFrom: <sip:+17175550142@tcc.example.net>;tag=tcc1\r\nTo: ${tag}\r\nCall-ID: ${callId}\r\nCSeq: 1 ACK\r\nContent-Length: 0\r\n\r\n`, from.port, from.address)
    }
    if (s.startsWith('BYE ')) {
      bye = true
      const keep = s.split('\r\n').filter((l) => /^(Via|From|To|Call-ID|CSeq):/i.test(l)).join('\r\n')
      sip.send(`SIP/2.0 200 OK\r\n${keep}\r\nContent-Length: 0\r\n\r\n`, from.port, from.address)
    }
  })
  const sdp = 'v=0\r\no=tcc 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=message 9 TCP/MSRP *\r\na=accept-types:text/plain\r\na=path:msrp://127.0.0.1:9/tcc1;tcp\r\na=setup:active\r\n'
  sip.send(`INVITE urn:service:sos SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:${sip.address().port};branch=z9hG4bKinv${Date.now()}\r\nFrom: <sip:+17175550142@tcc.example.net>;tag=tcc1\r\nTo: <urn:service:sos>\r\nCall-ID: ${callId}\r\nCSeq: 1 INVITE\r\nContact: <sip:tcc@127.0.0.1:${sip.address().port}>\r\nMax-Forwards: 70\r\nContent-Type: application/sdp\r\nContent-Length: ${sdp.length}\r\n\r\n${sdp}`, 25060, '127.0.0.1')
  await expect.poll(() => answer, { timeout: 5000 }).toContain('m=message')
  const path = /a=path:(\S+)/.exec(answer)![1]
  const [, host, port] = /msrp:\/\/([^:]+):(\d+)\//.exec(path)!
  const sock = net.connect(Number(port), host)
  await new Promise<void>((r) => sock.on('connect', () => r()))
  let buf = ''
  sock.on('data', (d) => {
    buf += d.toString()
    for (let m; (m = /MSRP (\S+) SEND\r\n[\s\S]*?\r\n\r\n([\s\S]*?)\r\n-------\1\$\r\n/.exec(buf));) {
      replies.push(m[2]); buf = buf.slice(m.index + m[0].length)
      sock.write(`MSRP ${m[1]} 200 OK\r\nTo-Path: ${path}\r\nFrom-Path: msrp://127.0.0.1:9/tcc1;tcp\r\n-------${m[1]}$\r\n`)
    }
  })
  const send = (text: string) => {
    const tid = `t${Math.random().toString(36).slice(2, 10)}`
    const n = Buffer.byteLength(text)
    sock.write(`MSRP ${tid} SEND\r\nTo-Path: ${path}\r\nFrom-Path: msrp://127.0.0.1:9/tcc1;tcp\r\nMessage-ID: ${tid}\r\nByte-Range: 1-${n}/${n}\r\nContent-Type: text/plain\r\n\r\n${text}\r\n-------${tid}$\r\n`)
  }
  return { send, replies, byed: () => bye, close: () => { sock.destroy(); sip.close() } }
}

// Text to 911: a TCC's session shows on the 911 page, a call-taker takes it, replies, and ends it.
test('text to 911 from a text control centre', async ({ page }) => {
  await signIn(page)
  const ok = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const tr = await fetch('./api/trunks', { method: 'POST', headers: H, body: JSON.stringify({ name: 'ESInet', enabled: true, host: '127.0.0.1', port: 5099, transport: 'udp', codecs: [], esinet: true, register: false, allowFrom: ['127.0.0.1'] }) })
    const t = (await tr.json()).item
    const r = await fetch('./api/inbound-routes', { method: 'POST', headers: H, body: JSON.stringify({ name: '911 from the ESInet', did: '', trunk: t.id, destination: { type: 'hangup', target: '' } }) })
    return r.ok && !!t.id
  })
  expect(ok).toBe(true)
  const tcc = await standInTCC()
  try {
    tcc.send('there is a man outside my window')
    await page.goto('./#/psap')
    const card = page.getByRole('article', { name: /911 call from \+?17175550142/ })
    await expect(card.getByText('Text to 911').first()).toBeVisible()
    await expect(card.getByText('there is a man outside my window')).toBeVisible()
    await expect(card.getByRole('textbox', { name: 'Reply' })).toHaveCount(0) // not yet taken
    await card.getByRole('button', { name: 'Take this text' }).click()
    await expect(card.getByText(/Taken by/)).toBeVisible()
    await card.getByRole('textbox', { name: 'Reply' }).fill('Are the doors locked?')
    await card.getByRole('button', { name: 'Send' }).click()
    await expect.poll(() => tcc.replies.join('|')).toContain('Are the doors locked?')
    await expect(card.getByText('Are the doors locked?')).toBeVisible()
    await card.getByRole('button', { name: 'End conversation' }).click()
    await expect.poll(() => tcc.byed(), { timeout: 5000 }).toBe(true)
    await expect(card).toHaveCount(0)
  } finally {
    tcc.close()
  }
})
