import { expect, test } from '@playwright/test'
import dgram from 'node:dgram'
import http from 'node:http'
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
  await sec.getByRole('switch', { name: /Check STIR\/SHAKEN signatures/ }).click()
  await sec.getByRole('switch', { name: /Inspect media/ }).click()
  await sec.locator('#cbmb').fill('50')
  await sec.locator('#cbmp').fill('120')
  await sec.locator('#csfg').fill('fg.example')
  await sec.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(sec.getByText("enter the forest guide's https address")).toBeVisible()
  await expect(sec.getByText('200 to 9,000 bytes (0: 1,500)')).toBeVisible()
  await sec.locator('#cbmb').fill('')
  await sec.locator('#csfg').fill('https://fg.example/lost')
  await sec.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Core services saved')).toBeVisible()
  expect(await page.evaluate(async () => (await (await fetch('./api/settings')).json()).system.core.bcf)).toMatchObject({ verifyIdentity: true, inspectMedia: true, mediaPerSecond: 120, mediaMaxBytes: 0 })
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
  // Did you mean: the nearest number the street has on that side; Use fills it in and it checks valid.
  const mean = form.getByRole('region', { name: 'Did you mean' })
  await expect(mean.getByText('198 N Front St, Harrisburg PA')).toBeVisible()
  await expect(mean.getByText(/the nearest number N Front St has on that side/).first()).toBeVisible()
  await mean.getByRole('button', { name: 'Use 198 Front' }).click()
  await expect(form.locator('#cvHNO')).toHaveValue('198')
  await form.locator('#cvRD').fill('Frnot')
  await form.getByRole('button', { name: 'Check' }).click()
  await expect(mean.getByText(/a street of a similar name \(N Front St, not Frnot\)/).first()).toBeVisible()
  // A position with the address: road centerlines have no points, so it cannot be checked.
  await form.locator('#cvRD').fill('Front')
  await form.locator('#cvlat').fill('40.26'); await form.locator('#cvlon').fill('-76.885')
  await form.getByRole('button', { name: 'Check' }).click()
  await expect(form.getByText(/Position not checked: the address data has no point for this address/)).toBeVisible()
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
    replay: async (undecodable = false) => {
      for (const orig of spurt) {
        const b = Buffer.from(orig)
        if (undecodable) {
          // voice blocks relabelled to a type this console does not know, as half-rate voice would come
          const nb = b[0] & 0x3f
          for (let i = 0; i < nb; i++) if ((b[1 + 4 * i] & 0x7f) === 0) b[1 + 4 * i] = (b[1 + 4 * i] & 0x80) | 99
        }
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
    // Voice this console cannot decode (Phase 2 half-rate sent untranscoded): the tile says so.
    const odd = rfss.replay(true)
    await expect(tile.getByText(/Silent here: this talkgroup's voice comes as P25 Phase 2 half-rate/)).toBeVisible({ timeout: 5_000 })
    await odd
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
  await page.locator('#psls').fill('ftp://ali.example.gov')
  await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText("the location server's https address")).toBeVisible()
  await page.locator('#psls').fill('https://ali.example.gov/held')
  await page.locator('#psl').fill('127.0.0.1:4999')
  await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/911 settings saved|saved/i).first()).toBeVisible()
  await page.getByRole('tab', { name: /Answer times/ }).click()
  await expect(page.getByRole('region', { name: 'Answer times' })).toBeVisible()
  const [csv] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'A row a day (CSV)' }).click()])
  expect(csv.suggestedFilename()).toMatch(/^911-answer-times-.*\.csv$/)
  const body = (await import('node:fs')).readFileSync((await csv.path())!, 'utf8')
  expect(body.split('\n')[0]).toBe('day,calls,answered,abandoned,answered within 15 s (%),answered within 20 s (%),average to answer (s),longest to answer (s)')
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
    await card.getByRole('button', { name: 'Send', exact: true }).click()
    await expect.poll(() => tcc.replies.join('|')).toContain('Are the doors locked?')
    await expect(card.getByText('Are the doors locked?')).toBeVisible()
    await card.getByRole('button', { name: 'End conversation' }).click()
    await expect.poll(() => tcc.byed(), { timeout: 5000 }).toBe(true)
    await expect(card).toHaveCount(0)
  } finally {
    tcc.close()
  }
})

// Radio positions from a radio data gateway: posted to the API, shown on the console with the
// radio's name; a bad report is refused with the reason.
test('radio positions from a data gateway', async ({ page }) => {
  await signIn(page)
  const r = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const bad = await fetch('./api/radio/locations', { method: 'POST', headers: H, body: JSON.stringify({ unit: 1205, lat: 95, lon: 1 }) })
    const ok = await fetch('./api/radio/locations', { method: 'POST', headers: H, body: JSON.stringify([{ unit: 4242, lat: 40.2633, lon: -76.8823, speed: 50, heading: 270, accuracy: 8 }]) })
    return { bad: bad.status, badText: await bad.text(), ok: ok.status }
  })
  expect(r.bad).toBe(400)
  expect(r.badText).toContain('a position in degrees')
  expect(r.ok).toBe(200)
  await page.goto('./#/radio')
  const sec = page.getByRole('region', { name: 'Radio positions' })
  await expect(sec.getByText('40.26330, -76.88230')).toBeVisible()
  await expect(sec.getByText('50 km/h')).toBeVisible()
  await expect(sec.getByRole('link', { name: 'Map of unit 4242' })).toBeVisible()
})

// The map: a tile server set in the 911 settings draws radio positions; BitVoice fetches the tiles.
test('map of radio positions from the chosen tiles', async ({ page }) => {
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==', 'base64')
  let tiles = 0
  const srv = http.createServer((_req, res) => { tiles++; res.writeHead(200, { 'Content-Type': 'image/png' }); res.end(png) })
  await new Promise<void>((r) => srv.listen(0, '127.0.0.1', r))
  const port = (srv.address() as { port: number }).port
  try {
    await signIn(page)
    await page.goto('./#/psap')
    await page.locator('#pmu').fill(`http://127.0.0.1:${port}/{z}/{x}.png`)
    await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText(/a tile address with \{z\}, \{x\} and \{y\}/)).toBeVisible()
    await page.locator('#pmu').fill(`http://127.0.0.1:${port}/{z}/{x}/{y}.png`)
    await page.locator('#pma').fill('© Example tiles')
    await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText(/911 settings saved/).first()).toBeVisible()
    await page.evaluate(async () => {
      await fetch('./api/radio/locations', { method: 'POST', headers: { 'X-BitVoice': '1', 'Content-Type': 'application/json' }, body: JSON.stringify({ unit: 77, lat: 40.2633, lon: -76.8823, accuracy: 30 }) })
    })
    await page.goto('./#/radio')
    const map = page.getByRole('img', { name: "Map of the radios' positions" })
    await expect(map).toBeVisible()
    await expect(map.locator('canvas')).toBeVisible()
    await expect.poll(() => tiles, { timeout: 10_000 }).toBeGreaterThan(0)
    await expect(map.getByText('© Example tiles')).toBeAttached()
    await page.goto('./#/psap')
    await page.locator('#pmu').fill('')
    await page.locator('section[aria-labelledby="pss-h"]').getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText(/911 settings saved/).first()).toBeVisible()
  } finally {
    srv.close()
  }
})

// The LIS: location records uploaded on the Core services page answer HELD requests by number.
test('core services: location records answer HELD by number', async ({ page }) => {
  await signIn(page)
  await page.goto('./#/core')
  const setup = page.locator('section[aria-labelledby="cs-h"]')
  await setup.getByRole('switch', { name: /Answer location requests \(LIS\)/ }).click()
  await setup.getByRole('switch', { name: /Run an i3 logging service/ }).click()
  await page.locator('#cspca').fill('not a certificate')
  await page.locator('#csa').fill('127.0.0.1')
  await setup.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText("paste the PCA's root certificates in PEM (-----BEGIN CERTIFICATE-----)")).toBeVisible()
  await page.locator('#cspca').fill('')
  await page.locator('#csa').fill('127.0.0.1')
  await setup.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Core services saved')).toBeVisible()
  const recs = page.locator('section[aria-labelledby="cr-h"]')
  await recs.getByLabel('Location records').setInputFiles({ name: 'lis.csv', mimeType: 'text/csv', buffer: Buffer.from('number,hno,rd,sts,a3,a1,lat,lon\n7175550100,123,Front,St,Harrisburg,PA,40.2633,-76.8823\nnot a number,1,Main,St,Here,PA,,\n') })
  await expect(page.getByText(/line 3: "not a number" is not a telephone number/)).toBeVisible()
  await recs.getByLabel('Location records').setInputFiles({ name: 'lis.csv', mimeType: 'text/csv', buffer: Buffer.from('number,hno,rd,sts,a3,a1,lat,lon\n7175550100,123,Front,St,Harrisburg,PA,40.2633,-76.8823\n') })
  await expect(page.getByText('1 location record read, 1 in all')).toBeVisible()
  await recs.locator('#crn').fill('(717) 555-0100')
  await recs.getByRole('button', { name: 'Look up' }).click()
  await expect(recs.getByText(/123 Front St, Harrisburg PA/)).toBeVisible()
  const res = await page.request.post('/held', { headers: { 'Content-Type': 'application/held+xml' },
    data: '<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held"><locationType>any</locationType><device xmlns="urn:ietf:params:xml:ns:geopriv:held:id"><tel>7175550100</tel></device></locationRequest>' })
  const xml = await res.text()
  expect(xml).toContain('<locationResponse')
  expect(xml).toContain('<ca:HNO>123</ca:HNO>')
  expect(xml).toContain('<gml:pos>40.2633 -76.8823</gml:pos>')
  // By network address: a floor's subnet, found for the address a request names (RFC 6155) and,
  // with no device named, for the address the request came from (the browser here, 127.0.0.1).
  await recs.getByLabel('Location records').setInputFiles({ name: 'nets.csv', mimeType: 'text/csv', buffer: Buffer.from('network,hno,rd,sts,flr,a3,a1\n10.20.3.0/24,100,Campus,Dr,3,Harrisburg,PA\n127.0.0.0/8,1,Loopback,Way,,Harrisburg,PA\n') })
  await expect(page.getByText('2 location records read, 3 in all')).toBeVisible()
  await expect(recs.getByText(/2\s*networks/)).toBeVisible()
  await recs.locator('#crn').fill('10.20.3.17')
  await recs.getByRole('button', { name: 'Look up' }).click()
  await expect(recs.getByText('from 10.20.3.0/24')).toBeVisible()
  await expect(recs.getByText(/100 Campus Dr, Harrisburg PA, floor 3/)).toBeVisible()
  const byIP = await (await page.request.post('/held', { headers: { 'Content-Type': 'application/held+xml' },
    data: '<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held"><device xmlns="urn:ietf:params:xml:ns:geopriv:held:id"><ip v="4">10.20.3.17</ip></device></locationRequest>' })).text()
  expect(byIP).toContain('<ca:FLR>3</ca:FLR>')
  const self = await (await page.request.post('/held', { headers: { 'Content-Type': 'application/held+xml' },
    data: '<locationRequest xmlns="urn:ietf:params:xml:ns:geopriv:held"><locationType>civic</locationType></locationRequest>' })).text()
  expect(self).toContain('<ca:RD>Loopback</ca:RD>')
  // The logging service: a body that is not a JWS is refused (460 Bad LogEvent); the screen lists
  // what was taken (nothing yet).
  const lg = await page.request.post('/i3log/LogEvents', { headers: { 'Content-Type': 'application/json' }, data: '{"logEventType":"CallStartLogEvent"}' })
  expect(lg.status()).toBe(460)
  expect((await lg.json()).reason).toContain('not a JWS')
  expect((await page.request.get('/i3log/Versions')).status()).toBe(200)
  await expect(page.locator('section[aria-labelledby="cg-h"]').getByText('No log events on this day.')).toBeVisible()
})

// The NENA i3 web services: switched on in Core services; a policy signed with this server's own
// certificate is stored and runs; a discrepancy report filed with this server comes in, is resolved,
// and the resolution comes back; dequeue registrations, ADR records and test call requests show.
test('core services: the i3 web services', async ({ page }) => {
  await signIn(page)
  await page.goto('./#/core')
  const setup = page.locator('section[aria-labelledby="cs-h"]')
  for (const name of [/Policy Store/, /Discrepancy reports: take them/, /Additional Data by caller/, /Dequeue registration/, /Test call generator/, /Service\/agency locator/]) {
    const sw = setup.getByRole('switch', { name })
    if ((await sw.getAttribute('aria-checked')) !== 'true') await sw.click()
  }
  await page.locator('#csa').fill('127.0.0.1')
  await setup.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('who answers discrepancy reports: a name, and an email or phone')).toBeVisible()
  await page.locator('#csdr').fill('Core services desk, core@state.example.gov')
  await page.locator('#csq').fill('sip:esrp@127.0.0.1')
  await setup.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Core services saved')).toBeVisible()

  const svc = page.locator('section[aria-labelledby="ci-h"]')
  // A policy signed by this server (its certificate names 127.0.0.1).
  await svc.locator('#cpol').fill(JSON.stringify({ policyOwner: '127.0.0.1', policyType: 'OriginationRoutePolicy', policyQueueName: 'sip:esrp@127.0.0.1',
    policyRules: [{ id: 'night', priority: 5, conditions: [{ conditionType: 'CallSuspicionCondition', scoreFrom: 70, scoreTo: 100 }], actions: [{ actionType: 'BusyAction' }] },
      { id: 'default', priority: 0, actions: [{ actionType: 'RouteAction', recipientUri: 'sip:sos@psap.county.example.gov' }] }] }))
  await svc.getByRole('button', { name: 'Store' }).click()
  await expect(page.getByText(/Stored: 127\.0\.0\.1 OriginationRoutePolicy/)).toBeVisible()
  const pol = svc.getByRole('table', { name: 'Stored policies' })
  await expect(pol.getByText('not traced to the PCA')).toBeVisible()
  await pol.getByRole('button', { name: '2 rules' }).click()
  await expect(pol.getByText(/if CallSuspicionCondition: BusyAction/)).toBeVisible()
  const got = await page.request.get('/i3/PolicyStore/v1/Policies?policyType=OriginationRoutePolicy')
  expect(got.status()).toBe(200)
  expect((await got.json()).totalCount).toBe(1)

  // A discrepancy report filed with ourselves.
  await svc.getByRole('tab', { name: 'Discrepancy reports' }).click()
  await svc.locator('#drto').fill(new URL('/i3/DiscrepancyReports/v1', page.url()).href)
  await svc.locator('#drkind').selectOption('LostDiscrepancyReport')
  await svc.locator('#drsvc').fill('ecrf.state.example.gov')
  await svc.locator('#drcom').fill('routes Oak St to the wrong county')
  await svc.getByRole('button', { name: 'File' }).click()
  await expect(page.getByText('Report filed')).toBeVisible()
  const received = svc.getByRole('list', { name: 'Reports received' }).getByRole('listitem').first()
  await expect(received.getByText('routes Oak St to the wrong county')).toBeVisible()
  await received.getByLabel('Resolution').selectOption('DataCorrected')
  await received.getByLabel('Comments').fill('boundary fixed')
  await received.getByRole('button', { name: 'Send the resolution' }).click()
  await expect(received.getByText(/Resolved: DataCorrected/)).toBeVisible()
  await expect(svc.getByRole('list', { name: 'Reports filed' }).getByText('DataCorrected')).toBeVisible()

  // A position registers to take a queue's calls.
  const reg = await page.request.put('/i3/DequeueRegistration/v1/Registrations', { data: { queueUri: 'sip:esrp@127.0.0.1', dequeuerUri: 'sip:pos7@127.0.0.1', expirationTime: 300, dequeuePreference: 2 } })
  expect(reg.status()).toBe(200)
  await svc.getByRole('tab', { name: 'Dequeue' }).click()
  await expect(svc.getByRole('table', { name: 'Dequeue registrations' }).getByText('sip:pos7@127.0.0.1')).toBeVisible()

  // Additional Data kept for a caller, found by an ESInet element.
  await svc.getByRole('tab', { name: 'Additional Data' }).click()
  await svc.locator('#adrc').fill('tel:+17175550100')
  await svc.locator('#adrx').fill('<sub:EmergencyCallData.SubscriberInfo xmlns:sub="urn:ietf:params:xml:ns:EmergencyCallData:SubscriberInfo"><sub:SubscriberData><fn>Pat Example</fn></sub:SubscriberData></sub:EmergencyCallData.SubscriberInfo>')
  await svc.getByRole('button', { name: 'Keep' }).click()
  await expect(svc.getByRole('table', { name: 'Additional Data records' }).getByText('tel:+17175550100')).toBeVisible()
  const adr = await page.request.get('/i3/Adr/v1/AdditionalData?callerUri=' + encodeURIComponent('tel:+1 717 555 0100'))
  expect(await adr.text()).toContain('Pat Example')

  // The locator gives out other agencies added here.
  await svc.getByRole('tab', { name: 'Locator' }).click()
  await svc.locator('#loci').fill('police.county.example.gov')
  await svc.locator('#locn').fill('County Police')
  await svc.locator('#loct').fill('police')
  await svc.locator('#locl').fill('https://log.county.example.gov/i3log')
  await svc.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(svc.getByRole('table', { name: 'Locator records' }).getByText('County Police')).toBeVisible()
  const loc = await page.request.get('/i3/ServiceAgencyLocator/v1/LocatorRecordUris?serviceAgencyName=County%20Police')
  expect((await loc.json()).locatorUris[0].uriType).toBe('RecordUri')

  // A test call request is refused for a PSAP this ESRP does not know.
  const tc = await page.request.put('/i3/TestCall/v1/SendTestCalls/nobody.example.gov', { data: { location: '<presence/>', frequency: 60, discrepancyRateLimit: 1, startDate: new Date().toISOString(), endDate: new Date(Date.now() + 864e5).toISOString() } })
  expect(tc.status()).toBe(442)
  await svc.getByRole('tab', { name: 'Test calls' }).click()
  await expect(svc.getByText('No PSAP has asked for test calls')).toBeVisible()
})

// The legacy ALI link: the 911 settings place each field of the ALI provider's layout, read a sample
// by it, and ask a stand-in ALI controller (this test, over TCP) for a number's record.
test('911 calls: the legacy ALI link and its layout', async ({ page }) => {
  const asked: string[] = []
  const ali = net.createServer((sock) => {
    let buf = ''
    sock.on('data', (d) => {
      buf += d.toString('latin1')
      const end = buf.indexOf('\x03')
      if (end < 0) return
      asked.push(buf.slice(0, end + 1))
      buf = buf.slice(end + 1)
      sock.write('\x02001 (717) 555-0100\r\nRESD\r\nJOHNSON PAT\r\n123 N MARKET ST\r\nHARRISBURG          PA\r\n\x03')
    })
  })
  await new Promise<void>((ok) => ali.listen(18402, '127.0.0.1', ok))
  try {
    await signIn(page)
    await page.goto('./#/psap')
    const setup = page.locator('section[aria-labelledby="pss-h"]')
    await setup.locator('#pal').fill('serial:dev/ttyUSB0')
    await setup.getByRole('button', { name: 'Place a field' }).click()
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(setup.getByText(/the ALI controller's address and port, or its serial port/)).toBeVisible()
    await setup.locator('#pal').fill('127.0.0.1:18402')
    const layout = setup.getByRole('table', { name: 'ALI layout' })
    await layout.getByLabel('Field 1', { exact: true }).selectOption('callback')
    await layout.getByLabel('Line of field 1').fill('1')
    await layout.getByLabel('Column of field 1').fill('5')
    for (const [i, name, line, col, len] of [[2, 'class', '2', '1', '4'], [3, 'name', '3', '1', ''], [4, 'street', '4', '1', ''], [5, 'city', '5', '1', '20'], [6, 'state', '5', '21', '2']] as const) {
      await setup.getByRole('button', { name: 'Place a field' }).click()
      await layout.getByLabel(`Field ${i}`, { exact: true }).selectOption(name)
      await layout.getByLabel(`Line of field ${i}`).fill(line)
      await layout.getByLabel(`Column of field ${i}`).fill(col)
      if (len) await layout.getByLabel(`Length of field ${i}`).fill(len)
    }
    await setup.locator('#pals').fill('001 (717) 555-0100\nRESD\nJOHNSON PAT\n123 N MARKET ST\nHARRISBURG          PA')
    await setup.getByRole('button', { name: 'Read the sample' }).click()
    const fields = setup.getByLabel('ALI fields read')
    await expect(fields.getByText('JOHNSON PAT')).toBeVisible()
    await expect(fields.getByText('HARRISBURG')).toBeVisible()
    await setup.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('911 settings saved').first()).toBeVisible()
    await setup.locator('#paln').fill('(717) 555-0100')
    await setup.getByRole('button', { name: 'Ask', exact: true }).click()
    await expect(setup.getByLabel('ALI lines')).toContainText('123 N MARKET ST')
    await expect(fields.getByText('RESD')).toBeVisible()
    expect(asked[0]).toBe('\x027175550100\x03')
  } finally {
    ali.close()
  }
})

// Report layouts: a layout as a state form asks (a row a day, a total, answered within 10 s, its own
// headings), checked, saved, run, downloaded and opened as a page to print.
test('911 calls: report layouts', async ({ page }) => {
  await signIn(page)
  await page.goto('./#/psap')
  await page.getByRole('tab', { name: /Report layouts/ }).click()
  const box = page.getByRole('region', { name: 'Report layouts' })
  await box.getByRole('button', { name: 'New report layout' }).click()
  await box.getByRole('button', { name: 'Save the layout' }).click()
  await expect(box.getByText('name the report')).toBeVisible()
  await box.locator('#rln').fill('State form 911-A')
  const cols = box.getByRole('table', { name: 'Columns' })
  await cols.getByLabel('Column 3', { exact: true }).selectOption('withinCount')
  await cols.getByLabel('Seconds of column 3').fill('0')
  await box.getByRole('button', { name: 'Save the layout' }).click()
  await expect(cols.getByText('seconds, 1 to 600')).toBeVisible()
  await cols.getByLabel('Seconds of column 3').fill('10')
  await cols.getByLabel('Heading of column 3').fill('Ans <= 10s')
  await box.getByRole('button', { name: 'Add a column' }).click()
  await cols.getByLabel('Column 5', { exact: true }).selectOption('p90Answer')
  await cols.getByRole('button', { name: 'Move column 5 up' }).click()
  await expect(cols.getByLabel('Column 4', { exact: true })).toHaveValue('p90Answer')
  await box.getByRole('button', { name: 'Save the layout' }).click()
  await expect(page.getByText('Report layouts saved')).toBeVisible()
  await box.getByRole('list', { name: 'Layouts' }).getByRole('button', { name: 'State form 911-A', exact: true }).click()
  await box.getByRole('button', { name: 'Run' }).click()
  const table = box.getByRole('table', { name: 'State form 911-A' })
  await expect(table.getByRole('columnheader', { name: 'Ans <= 10s' })).toBeVisible()
  await expect(table.getByRole('columnheader', { name: '90% answered within (s)' })).toBeVisible()
  await expect(table.getByRole('cell', { name: 'total' })).toBeVisible()
  const [csv] = await Promise.all([page.waitForEvent('download'), box.getByRole('link', { name: /Spreadsheet/ }).click()])
  expect(csv.suggestedFilename()).toMatch(/^State-form-911-A-.*\.csv$/)
  const body = (await import('node:fs')).readFileSync((await csv.path())!, 'utf8')
  expect(body.split('\n')[0]).toBe('Date,calls,Ans <= 10s,90% answered within (s),abandoned')
  const [print] = await Promise.all([page.context().waitForEvent('page'), box.getByRole('link', { name: /Page to print/ }).click()])
  await expect(print.getByRole('heading', { name: 'State form 911-A' })).toBeVisible()
  await print.close()
})

// Caller video: a 911 conversation from a TCC; the call-taker sends a video link, the caller's
// phone (a second page, Chromium's fake camera) shares live video over WHIP and sends a photo; the
// call-taker watches over WHEP, blurred until revealed, sees the photo's hash, time and GPS, holds it,
// and the exposure report counts the looks.
test('caller video and photos on a 911 call', async ({ page, browser }) => {
  await signIn(page)
  const ok = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const have = (await (await fetch('./api/trunks')).json()).items.find((t: { name: string }) => t.name === 'ESInet')
    const t = have ?? (await (await fetch('./api/trunks', { method: 'POST', headers: H, body: JSON.stringify({ name: 'ESInet', enabled: true, host: '127.0.0.1', port: 5099, transport: 'udp', codecs: [], esinet: true, register: false, allowFrom: ['127.0.0.1'] }) })).json()).item
    const routes = (await (await fetch('./api/inbound-routes')).json()).items
    if (!routes.some((r: { trunk: string }) => r.trunk === t.id)) await fetch('./api/inbound-routes', { method: 'POST', headers: H, body: JSON.stringify({ name: '911 from the ESInet', did: '', trunk: t.id, destination: { type: 'hangup', target: '' } }) })
    return !!t.id
  })
  expect(ok).toBe(true)
  // Recording on (Settings › 911 calls), with the link address CAD is given in the EIDO.
  await page.goto('./#/psap')
  const setup = page.locator('section[aria-labelledby="pss-h"]')
  await setup.getByLabel('Link address', { exact: true }).fill('https://127.0.0.1:18380')
  await setup.getByRole('checkbox', { name: /Record callers' live video/ }).check()
  await setup.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('911 settings saved').first()).toBeVisible()
  const tcc = await standInTCC()
  try {
    tcc.send('he is outside, I can film him')
    await page.goto('./#/psap')
    const card = page.getByRole('article', { name: /911 call from \+?17175550142/ })
    await card.getByRole('button', { name: 'Take this text' }).click()
    await card.getByRole('button', { name: 'Send the caller a video link' }).click()
    const url = await card.getByLabel("The caller's link").textContent()
    expect(url).toMatch(/\/v\/[A-Za-z0-9_-]{24}$/)

    // The caller's phone.
    const phone = await browser.newContext({ ignoreHTTPSErrors: true, permissions: ['camera'] })
    const caller = await phone.newPage()
    await caller.goto(url!)
    await expect(caller.getByRole('heading', { name: 'Show 911 what is happening' })).toBeVisible()
    await caller.getByRole('button', { name: 'Share live video' }).click()
    await expect(caller.getByText('911 can see your video.')).toBeVisible({ timeout: 15_000 })
    await caller.locator('#files').setInputFiles(new URL('./fixtures/caller-photo.jpg', import.meta.url).pathname)
    await expect(caller.getByText('Sent to 911.')).toBeVisible()

    // The call-taker watches: blurred until revealed.
    await expect(card.getByRole('region', { name: 'Caller video' }).getByText(/LIVE · (H264|VP8)/)).toBeVisible({ timeout: 10_000 })
    await card.getByRole('button', { name: "Watch the caller's video" }).click()
    const video = card.getByLabel("The caller's live video")
    await expect(video).toHaveClass(/blur-2xl/)
    await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.videoWidth), { timeout: 15_000 }).toBeGreaterThan(0)
    await card.getByRole('button', { name: /^Reveal/ }).first().click()
    await expect(video).not.toHaveClass(/blur-2xl/)

    // The photo: hash, time and GPS, revealed on request, held as evidence.
    const file = card.getByRole('listitem', { name: 'File 1: caller-photo.jpg' })
    await expect(file.getByText(/40\.26330, -76\.88230/)).toBeVisible()
    await expect(file.getByText(/taken 2026-10-15 14:05:30 \(the camera's clock\)/)).toBeVisible()
    await expect(file.getByText(/SHA-256 [0-9a-f]{64}/)).toBeVisible()
    await file.getByRole('button', { name: /^Reveal/ }).click()
    await expect(file.getByRole('img', { name: /Photo caller-photo.jpg/ })).toBeVisible()
    await card.getByRole('button', { name: 'Hold as evidence' }).click()
    await expect(card.getByRole('button', { name: 'Held as evidence' })).toBeVisible()

    // CAD's EIDO names the photo and the live video, by the viewing token.
    const callId = await page.evaluate(async () => (await (await fetch('./api/calls')).json()).items.find((c: { emergency?: unknown }) => c.emergency).id)
    const eido = await page.evaluate(async (id) => (await fetch(`./api/calls/${id}/eido`)).json(), callId)
    const purposes = (eido.additionalDataComponent ?? []).map((a: { urlPurpose?: string }) => a.urlPurpose)
    expect(purposes).toEqual(expect.arrayContaining(['caller-photo', 'caller-video-live']))
    const photoURL = eido.additionalDataComponent.find((a: { urlPurpose?: string }) => a.urlPurpose === 'caller-photo').additionalDataByReferenceUrl
    expect(photoURL).toMatch(/^https:\/\/127\.0\.0\.1:18380\/m\/[A-Za-z0-9_-]{24}\/1$/)
    const fetched = await caller.request.get(photoURL)
    expect(fetched.status()).toBe(200)
    expect(fetched.headers()['content-security-policy']).toContain('sandbox')

    // The caller stops; the recording of the live video is kept, hashed, and plays.
    await caller.waitForTimeout(2000)
    await caller.getByRole('button', { name: 'Stop sharing' }).click()
    await expect(caller.getByText('You stopped sharing.')).toBeVisible()
    const recItem = card.getByRole('listitem', { name: /File 2: live video .*\.ivf/ })
    await expect(recItem.getByText(/live video recorded here/)).toBeVisible({ timeout: 10_000 })
    await expect(recItem.getByText(/SHA-256 [0-9a-f]{64}/)).toBeVisible()
    await recItem.getByRole('button', { name: /^Reveal/ }).click()
    const played = recItem.getByLabel(/Clip live video/)
    await expect.poll(() => played.evaluate((v: HTMLVideoElement) => v.videoWidth), { timeout: 15_000 }).toBeGreaterThan(0)
    await expect(recItem.getByRole('link', { name: /Download the original/ })).toBeVisible()
    await page.getByRole('tab', { name: /Exposure/ }).click()
    await expect(page.getByRole('region', { name: 'Exposure' }).locator('td').nth(1)).toHaveText(/^[2-9]|\d\d/)
    await phone.close()
  } finally {
    tcc.close()
  }
})

// A 911 route that hangs up calls is flagged in Health: BitVoice rings every call-taker instead.
test('health warns about a 911 route that would refuse calls', async ({ page }) => {
  await signIn(page)
  const made = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const have = (await (await fetch('./api/trunks')).json()).items.find((t: { name: string }) => t.name === 'ESInet')
    const t = have ?? (await (await fetch('./api/trunks', { method: 'POST', headers: H, body: JSON.stringify({ name: 'ESInet', enabled: true, host: '127.0.0.1', port: 5099, transport: 'udp', codecs: [], esinet: true, register: false, allowFrom: ['127.0.0.1'] }) })).json()).item
    const r = await (await fetch('./api/inbound-routes', { method: 'POST', headers: H, body: JSON.stringify({ name: 'Closed for 911', did: '7175550999', trunk: t.id, destination: { type: 'hangup', target: '' } }) })).json()
    return r.item?.id as string
  })
  expect(made).toBeTruthy()
  await page.goto('./#/health')
  await expect(page.getByText('The incoming route Closed for 911 hangs up 911 calls from ESInet')).toBeVisible()
  await expect(page.getByText(/so these ring every call-taker instead/).first()).toBeVisible()
  await page.evaluate(async (id) => { await fetch('./api/inbound-routes/' + id, { method: 'DELETE', headers: { 'X-BitVoice': '1' } }) }, made)
})
