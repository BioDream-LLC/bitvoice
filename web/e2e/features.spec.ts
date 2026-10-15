import { readFileSync } from 'node:fs'
import { expect, test } from '@playwright/test'
import { signIn } from './helpers'

/** A WAV file of a tone, for uploads. */
function wav(seconds: number, hz = 440, rate = 8000): Buffer {
  const n = seconds * rate
  const b = Buffer.alloc(44 + n * 2)
  b.write('RIFF', 0); b.writeUInt32LE(36 + n * 2, 4); b.write('WAVEfmt ', 8)
  b.writeUInt32LE(16, 16); b.writeUInt16LE(1, 20); b.writeUInt16LE(1, 22); b.writeUInt32LE(rate, 24)
  b.writeUInt32LE(rate * 2, 28); b.writeUInt16LE(2, 32); b.writeUInt16LE(16, 34); b.write('data', 36); b.writeUInt32LE(n * 2, 40)
  for (let i = 0; i < n; i++) b.writeInt16LE(Math.round(Math.sin(2 * Math.PI * hz * i / rate) * 8000), 44 + i * 2)
  return b
}

// Sounds: a prompt recorded in Spanish, a language added, hold music uploaded and made the default.
test('sounds: prompts per language and hold music', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/sounds')
  await expect(page.getByRole('heading', { name: 'Sounds' })).toBeVisible()
  await page.getByRole('tab', { name: /Español/ }).click()
  await page.getByRole('button', { name: /goodbye\s*Adiós/ }).click()
  await page.getByLabel(/goodbye \(Español\) file/).setInputFiles({ name: 'adios.wav', mimeType: 'audio/wav', buffer: wav(1) })
  await expect(page.getByText('Prompt saved')).toBeVisible()
  await expect(page.getByRole('tab', { name: /Español 1\// })).toBeVisible()

  await page.getByLabel('Add a language').fill('sv')
  await page.getByRole('button', { name: 'Add', exact: true }).first().click()
  await expect(page.getByRole('tab', { name: /^sv/ })).toBeVisible()

  await page.getByLabel('New hold music').fill('Calm')
  await page.getByRole('button', { name: 'Add', exact: true }).last().click()
  await expect(page.getByText('Hold music Calm added')).toBeVisible()
  await page.getByLabel('Calm tracks').setInputFiles([
    { name: 'Morning Rain.wav', mimeType: 'audio/wav', buffer: wav(3, 330) },
    { name: 'waves.wav', mimeType: 'audio/wav', buffer: wav(2, 550) },
  ])
  await expect(page.getByText('Morning-Rain', { exact: true })).toBeVisible()
  await expect(page.getByText('waves', { exact: true })).toBeVisible()
  await expect(page.getByLabel('Default for everyone')).toHaveValue('calm')
  await page.getByRole('switch', { name: 'Shuffle Calm' }).click()
  await expect(page.getByText('Shuffled')).toBeVisible()
  await page.getByRole('button', { name: 'Delete waves' }).click()
  await expect(page.getByText('waves', { exact: true })).toHaveCount(0)
})

// Queues: the new strategies, agent order and penalties, weight, music and announcements.
test('queue strategies, penalties and announcements', async ({ page }) => {
  await signIn(page)
  for (const n of ['201', '202']) {
    await page.goto('/#/extensions')
    await page.getByRole('button', { name: 'Add extension' }).click()
    await page.getByLabel('Number', { exact: true }).fill(n)
    await page.getByLabel('Name', { exact: true }).fill('Agent ' + n)
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('cell', { name: n, exact: true })).toBeVisible()
  }
  await page.goto('/#/queues')
  await page.getByRole('button', { name: 'Add queue' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Help desk')
  await page.getByRole('button', { name: /Weighted random/ }).click()
  await page.getByRole('button', { name: /201\s*Agent 201/ }).click()
  await page.getByRole('button', { name: /202\s*Agent 202/ }).click()
  await page.getByRole('button', { name: 'Move 202 up' }).click()
  await page.getByLabel('Penalty of 201').fill('300')
  await page.getByLabel('Weight').fill('5')
  await page.getByRole('switch', { name: 'Announce place in line' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('penalties are 0 to 100').first()).toBeVisible()
  await page.getByLabel('Penalty of 201').fill('10')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Queue Help desk saved')).toBeVisible()
  const saved = await page.evaluate(async () => (await (await fetch('./api/queues')).json()).items.find((q: { name: string }) => q.name === 'Help desk'))
  expect(saved.strategy).toBe('weighted-random')
  expect(saved.agents).toEqual(['202', '201'])
  expect(saved.penalties).toEqual({ 201: 10 })
  expect(saved.weight).toBe(5)
  expect(saved.announcePosition).toBe(true)
  expect(saved.announceSeconds).toBe(60)
})

// Dial-by-name, dial-in and the hotel: set up through the GUI.
test('directory, dial-in and hotel', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/directories')
  await page.getByRole('button', { name: 'Add directory' }).click()
  await page.getByRole('radio', { name: /First name/ }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: /Staff directory/ })).toBeVisible()
  await expect(page.getByRole('button', { name: /Staff directory/ }).getByText('First name')).toBeVisible()

  await page.goto('/#/dial-in')
  await page.getByRole('button', { name: 'Add dial-in' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('set a PIN or the numbers allowed to call in').first()).toBeVisible()
  await page.getByLabel('PIN', { exact: true }).fill('12')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('a PIN is 4 to 10 digits').first()).toBeVisible()
  await page.getByLabel('PIN', { exact: true }).fill('48151623')
  await page.getByLabel('Only from these numbers').fill('+12125550199')
  await page.getByLabel('Only from these numbers').press('Enter')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: /Staff dial-in/ }).getByText('1 allowed numbers')).toBeVisible()
  const saved = await page.evaluate(async () => (await (await fetch('./api/dial-ins')).json()).items[0])
  expect(saved.pin).toBe('48151623') // administrators see it; viewers get it hidden
  expect(saved.allowedCallers).toEqual(['+12125550199'])

  // A guest room, checked in on the Hotel page; a wake-up booked; the settings validated.
  await page.goto('/#/extensions')
  await page.getByRole('cell', { name: '201', exact: true }).click()
  await page.getByRole('switch', { name: 'Guest room' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await page.goto('/#/hotel')
  await page.getByRole('button', { name: 'Check in' }).first().click()
  await page.getByLabel('Guest name').fill('Ann Smith')
  await page.getByLabel('Guest language').selectOption('fr')
  await page.getByRole('button', { name: 'Check in' }).last().click()
  await expect(page.getByText('Ann Smith checked in to 201')).toBeVisible()
  await expect(page.getByText('1 of 1 occupied')).toBeVisible()
  await page.getByLabel('Extension', { exact: true }).selectOption('201')
  await page.getByRole('button', { name: 'Book' }).click()
  await expect(page.getByText('Wake-up call for 201 booked')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Cancel wake-up of 201' })).toBeVisible()
  await page.getByRole('radio', { name: /FIAS/ }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('enter the hotel system\u2019s address').or(page.getByText("enter the hotel system's address")).first()).toBeVisible()
  await page.getByLabel('Hotel system address').fill('10.0.0.40')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Hotel settings saved')).toBeVisible()
  await expect(page.getByText('Hotel system not reachable')).toBeVisible()
  await page.getByRole('button', { name: 'Check out' }).click()
  await expect(page.getByText('0 of 1 occupied')).toBeVisible()
})

// STIR/SHAKEN signing on a trunk: a key that is not the certificate's is refused; the right pair is
// kept, and the certificate published at /stir/<trunk>.pem.
test('a trunk signs its calls (STIR/SHAKEN)', async ({ page }) => {
  await signIn(page)
  const key = readFileSync(new URL('./fixtures/sti-test.key', import.meta.url), 'utf8')
  const cert = readFileSync(new URL('./fixtures/sti-test.pem', import.meta.url), 'utf8')
  await page.goto('/#/trunks')
  await page.getByRole('button', { name: 'Add trunk' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Signed line')
  await page.getByLabel('Provider server').fill('127.0.0.1')
  await page.getByLabel('Port', { exact: true }).fill('25998')
  await page.getByLabel('Username', { exact: true }).fill('acct')
  await page.getByRole('switch', { name: 'Sign caller ID' }).click()
  await page.getByLabel('STI certificate').fill(cert)
  await page.getByLabel('Private key').fill('-----BEGIN EC PRIVATE KEY-----\nnonsense\n-----END EC PRIVATE KEY-----')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('the signing key could not be read').first()).toBeVisible()
  await page.getByLabel('Private key').fill(key)
  await page.getByLabel('Attestation').selectOption('B')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Signed line').first()).toBeVisible()
  const t = await page.evaluate(async () => (await (await fetch('./api/trunks')).json()).items.find((x: { name: string }) => x.name === 'Signed line'))
  expect(t.shaken.sign).toBe(true)
  expect(t.shaken.attest).toBe('B')
  expect(t.shaken.origId).toMatch(/^[0-9a-f-]{36}$/)
  const pem = await page.evaluate(async (id) => (await fetch(`./stir/${id}.pem`)).text(), t.id)
  expect(pem).toContain('BEGIN CERTIFICATE')
})

// HD conference rooms: on for new rooms, shown on the room's card.
test('HD conference room', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/conferences')
  await page.getByRole('button', { name: 'Add conference room' }).click()
  await page.getByLabel('Name', { exact: true }).fill('HD huddle')
  await expect(page.getByRole('switch', { name: 'HD audio' })).toBeChecked()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: /HD huddle/ }).getByText('HD', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: /HD huddle/ }).click()
  await page.getByRole('switch', { name: 'HD audio' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: /HD huddle/ }).getByText('HD', { exact: true })).toHaveCount(0)
})

// Staff chat: two people signed in to the portal in two browsers; a message arrives live.
test('staff chat and presence in the portal', async ({ page, browser }) => {
  await signIn(page)
  await page.evaluate(async () => {
    const list = (await (await fetch('./api/extensions')).json()).items as { id: string; number: string; voicemail: object }[]
    for (const x of list.filter((x) => x.number === '100' || x.number === '202')) {
      await fetch(`./api/extensions/${x.id}`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-BitVoice': '1' },
        body: JSON.stringify({ ...x, voicemail: { ...x.voicemail, enabled: true, pin: '4826' } }) })
    }
  })
  await page.goto('/#/settings')
  await page.getByRole('button', { name: 'Add a room' }).click()
  await page.getByLabel('Room 1 name').fill('Front desk')
  await page.getByRole('button', { name: 'Save' }).first().click()
  await expect(page.getByText('Settings saved')).toBeVisible()

  const portal = async (ext: string) => {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true })
    const p = await ctx.newPage()
    await p.goto('/#/me')
    await p.getByLabel('Your extension').fill(ext)
    await p.getByLabel('Voicemail PIN').fill('4826')
    await p.getByRole('button', { name: 'Sign in' }).click()
    await p.getByRole('button', { name: 'Chat' }).click()
    return { ctx, p }
  }
  const a = await portal('100')
  const b = await portal('202')
  await expect(a.p.getByRole('button', { name: /Front desk/ })).toBeVisible()
  await b.p.getByLabel('Your status').fill('At lunch')
  await b.p.getByLabel('Your status').blur()
  await a.p.getByRole('button', { name: /Agent 202/ }).click()
  await expect(a.p.getByRole('button', { name: /Agent 202/ }).getByText('At lunch')).toBeVisible()
  await expect(a.p.getByRole('button', { name: /Agent 202/ }).getByRole('img', { name: 'Offline' })).toBeVisible()
  await a.p.getByLabel('Message').fill('Can you cover the front desk?')
  await a.p.getByLabel('Message').press('Enter')
  await expect(a.p.getByText('Can you cover the front desk?')).toBeVisible()
  // 202 sees it counted as unread, live, then opens it.
  await expect(b.p.getByRole('button', { name: /Reception 100/ }).getByText('1', { exact: true })).toBeVisible()
  await b.p.getByRole('button', { name: /Reception 100/ }).click()
  await expect(b.p.getByText('Can you cover the front desk?')).toBeVisible()
  await b.p.getByLabel('Message').fill('Yes, on my way')
  await b.p.getByRole('button', { name: 'Send' }).click()
  await expect(a.p.getByText('Yes, on my way')).toBeVisible()
  await a.ctx.close()
  await b.ctx.close()
})

// More makes provisioned: a Cisco multiplatform phone, marked untested, gets its file over HTTPS.
test('provisioning a Cisco phone', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/phones')
  const auto = page.getByRole('switch', { name: 'Automatic set-up' })
  if ((await auto.getAttribute('aria-checked')) !== 'true') await auto.click()
  await page.getByRole('button', { name: 'Add phone' }).click()
  await page.getByRole('button', { name: /Cisco/ }).click()
  await expect(page.getByText(/has not been tried on one of its phones yet/)).toBeVisible()
  await page.getByLabel('MAC address').fill('00-1b-54-aa-bb-cc')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('00:1B:54:AA:BB:CC')).toBeVisible()
  const pv = await page.evaluate(async () => (await (await fetch('./api/settings')).json()).system.provisioning)
  const r = await page.request.get('/provision/001b54aabbcc.xml', { headers: { Authorization: 'Basic ' + Buffer.from(`${pv.username}:${pv.password}`).toString('base64') } })
  expect(r.status()).toBe(200)
  const body = await r.text()
  expect(body).toContain('<flat-profile>')
  expect(body).toContain('<Register_1_>Yes</Register_1_>')
})

// SNMP and call-record outputs: weak settings refused, good ones kept, the MIB downloadable.
test('SNMP monitoring and call records elsewhere', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/settings')
  await page.getByRole('switch', { name: 'SNMP agent' }).click()
  await page.getByLabel('Listen on').fill('127.0.0.1:18161')
  await page.getByLabel('Community', { exact: true }).fill('public')
  await page.getByRole('button', { name: 'Save' }).first().click()
  await expect(page.getByText('public and private are the first guesses').first()).toBeVisible()
  await page.getByLabel('Community', { exact: true }).fill('watchtower7')
  await page.getByLabel('Answer only these networks').fill('127.0.0.1')
  await page.getByLabel('Answer only these networks').press('Enter')
  await page.getByRole('switch', { name: 'CSV files' }).click()
  await page.getByRole('button', { name: 'Save' }).first().click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  await expect(page.getByText('Answering on 127.0.0.1:18161')).toBeVisible({ timeout: 15000 })
  const mib = await page.evaluate(async () => (await fetch('./api/snmp/mib')).text())
  expect(mib).toContain('BITVOICE-MIB DEFINITIONS')
  await page.getByRole('switch', { name: 'SNMP agent' }).click()
  await page.getByRole('button', { name: 'Save' }).first().click()
  await expect(page.getByText('Settings saved')).toBeVisible()
})

// Video conversion: switched on in Settings, which says whether ffmpeg is installed.
test('video conversion setting', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/settings')
  await page.getByRole('switch', { name: 'Convert video' }).click()
  await expect(page.getByText(/ffmpeg is (installed|not installed)/)).toBeVisible()
  await page.getByRole('button', { name: 'Save' }).first().click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  const s = await page.evaluate(async () => (await (await fetch('./api/settings')).json()).system)
  expect(s.videoTranscode).toBe(true)
  await page.getByRole('switch', { name: 'Convert video' }).click()
  await page.getByRole('button', { name: 'Save' }).first().click()
  await expect(page.getByText('Settings saved')).toBeVisible()
})

// The newer codecs can be chosen in Settings and are saved in the order shown.
test('codecs: iLBC, Speex and G.723.1', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/settings')
  await expect(page.getByText('(pass-through only)').first()).toBeVisible()
  for (const name of ['iLBC', 'Speex', 'G.723.1']) {
    const sw = page.getByRole('switch', { name: `Use ${name}` }).first()
    if ((await sw.getAttribute('aria-checked')) !== 'true') await sw.click()
  }
  await page.getByRole('button', { name: 'Save' }).first().click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  const s = await page.evaluate(async () => (await (await fetch('./api/settings')).json()).system)
  expect(s.codecs).toEqual(expect.arrayContaining(['ILBC', 'SPEEX', 'G723']))
})

// Skinny phones and MGCP gateway lines: added, validated, saved, and shown with their state.
test('Skinny phones and MGCP gateways', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/extensions')
  await page.getByRole('button', { name: 'Add extension' }).click()
  await page.getByLabel('Number', { exact: true }).fill('240')
  await page.getByLabel('Name', { exact: true }).fill('Lobby desk')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('cell', { name: '240', exact: true })).toBeVisible()

  await page.goto('/#/desk-phones')
  await page.getByRole('switch', { name: 'Skinny phones' }).click()
  await page.getByRole('button', { name: 'Add a Skinny phone' }).click()
  await page.getByLabel('Phone 1 MAC address').fill('not a mac')
  await page.getByLabel('Phone 1 extension').selectOption('240')
  await page.getByRole('switch', { name: 'MGCP gateways' }).click()
  await page.getByRole('button', { name: 'Add a gateway' }).click()
  await page.getByLabel('Gateway name').fill('ata1.office.lan')
  await page.getByLabel('Gateway 1 line 1 extension').selectOption('240')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('enter the phone\u2019s MAC address', { exact: false }).or(page.getByText("enter the phone's MAC address", { exact: false }))).toBeVisible()
  await page.getByLabel('Phone 1 MAC address').fill('00:11:22:33:44:55')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Desk phones saved')).toBeVisible()
  const s = await page.evaluate(async () => (await (await fetch('./api/settings')).json()).system.desk)
  expect(s.sccp.phones[0]).toEqual({ device: 'SEP001122334455', extension: '240' })
  expect(s.mgcp.gateways[0].lines[0]).toEqual({ endpoint: 'aaln/1', extension: '240' })
  await expect(page.getByText(/not connected|waiting for the gateway|not registered/).first()).toBeVisible()
})

// An H.323 trunk: port 1720, no transport, username or registration; the gateway's address is needed.
test('an H.323 trunk', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/trunks')
  await page.getByRole('button', { name: 'Add trunk' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Old gateway')
  await page.getByLabel('Protocol').selectOption('h323')
  await expect(page.getByLabel('Port', { exact: true })).toHaveValue('1720')
  await expect(page.getByLabel('Transport')).toHaveCount(0)
  await expect(page.getByLabel('Username', { exact: true })).toHaveCount(0)
  await expect(page.getByText(/Fast Connect with G.711/)).toBeVisible()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText("enter the H.323 gateway's address").first()).toBeVisible()
  await page.getByLabel('Provider server').fill('127.0.0.1')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Old gateway').first()).toBeVisible()
  const t = await page.evaluate(async () => (await (await fetch('./api/trunks')).json()).items.find((x: { name: string }) => x.name === 'Old gateway'))
  expect(t.protocol).toBe('h323')
  expect(t.port).toBe(1720)
})

// Telephony cards: switched on (a token is made), ports added and checked, the card trunk added,
// and bitvoice-dahdi's API answers the token from this computer.
test('telephony cards', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/extensions')
  await page.getByRole('button', { name: 'Add extension' }).click()
  await page.getByLabel('Number', { exact: true }).fill('250')
  await page.getByLabel('Name', { exact: true }).fill('Warehouse phone')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('cell', { name: '250', exact: true })).toBeVisible()

  await page.goto('/#/cards')
  await page.getByRole('switch', { name: 'Telephony cards' }).click()
  await expect(page.getByText('bitvoice-dahdi is not running')).toBeVisible()
  const token = await page.getByRole('textbox', { name: 'Token' }).inputValue()
  expect(token).toMatch(/^[0-9a-f]{48}$/)
  await page.getByRole('button', { name: 'Add a port' }).click()
  await page.getByRole('button', { name: 'Add a port' }).click()
  await page.getByLabel('Port 2 kind').selectOption('line')
  await page.getByLabel('Port 2 line number').fill('2125550100')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('choose the extension this phone belongs to')).toBeVisible()
  await page.getByLabel('Port 1 extension').selectOption('250')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Telephony cards saved')).toBeVisible()
  await expect(page.getByText(/Lines need the card trunk/)).toBeVisible()

  await page.goto('/#/trunks')
  await page.getByRole('button', { name: 'Add trunk' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Card lines')
  await page.getByLabel('Protocol').selectOption('dahdi')
  await expect(page.getByLabel('Provider server')).toHaveCount(0)
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Card lines').first()).toBeVisible()

  const bridge = await page.evaluate(async (tok) => {
    const r = await fetch('./api/cards/bridge', { headers: { Authorization: 'Bearer ' + tok } })
    return { status: r.status, body: await r.json() }
  }, token)
  expect(bridge.status).toBe(200)
  expect(bridge.body.channels.map((c: { kind: string }) => c.kind)).toEqual(['phone', 'line'])
  expect(bridge.body.channels[0].extension).toBe('250')
})

// Day/night toggles, PIN sets and caller ID lookups: made, put on routes, switched from the page.
test('toggles, PIN sets and caller ID lookup', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/toggles')
  await page.getByRole('button', { name: 'Add toggle' }).click()
  await page.getByLabel('Normally, calls go to').selectOption('hangup:')
  await page.getByLabel('Switched over, calls go to').selectOption('echo:')
  await page.getByLabel('Code').fill('')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/a toggle's code is 1 to 3 digits/).first()).toBeVisible()
  await page.getByLabel('Code').fill('1')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('*281')).toBeVisible()
  await expect(page.getByText('Normal', { exact: true })).toBeVisible()
  await page.getByRole('switch', { name: 'Switch Night service over' }).click()
  await expect(page.getByText('Switched over', { exact: true })).toBeVisible()
  const state = await page.evaluate(async () => (await (await fetch('./api/toggles/state')).json()).override)
  expect(state).toHaveLength(1)
  await page.getByRole('switch', { name: 'Switch Night service over' }).click()
  await expect(page.getByText('Normal', { exact: true })).toBeVisible()

  await page.goto('/#/pin-sets')
  await page.getByRole('button', { name: 'Add PIN set' }).click()
  await page.getByLabel('PINs').fill('12')
  await page.getByLabel('PINs').press('Enter')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/is not a PIN/).first()).toBeVisible()
  await page.getByRole('button', { name: /Remove 12/ }).click()
  await page.getByLabel('PINs').fill('7001')
  await page.getByLabel('PINs').press('Enter')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Not on any outbound route yet')).toBeVisible()

  await page.goto('/#/lookups')
  await page.getByRole('button', { name: 'Add lookup' }).click()
  await page.getByLabel('Address').fill('https://crm.example.com/callers')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/put \{number\} in the address/).first()).toBeVisible()
  await page.getByLabel('Address').fill('http://127.0.0.1:9/callers?n={number}')
  await page.getByLabel('Name field').fill('data.name')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Not on any inbound route yet')).toBeVisible()
  await page.getByRole('button', { name: /CRM/ }).click()
  await page.getByLabel('Number to look up').fill('2125550199')
  await page.getByRole('button', { name: 'Look up' }).click()
  await expect(page.getByText(/^Failed:/)).toBeVisible()
  await page.keyboard.press('Escape')

  // On the routes: an outbound route asks for the PIN set, an inbound route uses the lookup.
  await page.goto('/#/routes')
  const sets = await page.evaluate(async () => (await (await fetch('./api/pin-sets')).json()).items)
  const lookups = await page.evaluate(async () => (await (await fetch('./api/lookups')).json()).items)
  await page.getByRole('button', { name: 'Add' }).first().click()
  await page.getByLabel('Name', { exact: true }).fill('Main number')
  await page.getByLabel('Send calls to').selectOption('toggle:' + (await page.evaluate(async () => (await (await fetch('./api/toggles')).json()).items[0].id)))
  await page.getByLabel('Caller ID lookup').selectOption(lookups[0].id)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('a day/night toggle')).toBeVisible()
  const routes = await page.evaluate(async () => (await (await fetch('./api/inbound-routes')).json()).items)
  expect(routes.at(-1).lookup).toBe(lookups[0].id)
  expect(sets[0].pins).toEqual(['7001'])
})

// Let's Encrypt: the settings are checked, applied, and the manager reports what happened (here an
// authority that cannot be reached, so it fails plainly and says when it tries again).
test("Let's Encrypt certificate", async ({ page }) => {
  await signIn(page)
  await page.goto('/#/settings')
  await page.getByRole('switch', { name: "Let's Encrypt" }).click()
  await page.getByRole('button', { name: "Apply Let's Encrypt settings" }).click()
  await expect(page.getByText(/enter the name this server is reached by/).first()).toBeVisible()
  await page.getByLabel('Names').fill('pbx.example.com')
  await page.getByLabel('Names').press('Enter')
  await page.getByLabel('Authority').selectOption('other')
  await page.getByLabel('Directory address').fill('https://127.0.0.1:9/directory')
  await page.getByLabel('Port 80 here').fill('0')
  await page.getByRole('button', { name: "Apply Let's Encrypt settings" }).click()
  await expect(page.getByText("Let's Encrypt settings saved")).toBeVisible()
  await expect(page.getByText(/^Failed: .*Tried again/)).toBeVisible({ timeout: 15000 })
  const st = await page.evaluate(async () => (await (await fetch('./api/certificate/acme')).json()))
  expect(st.state).toBe('failed')
  expect(st.challenge).toBe('http-01')
  await page.getByRole('button', { name: 'Test the names' }).click()
  await expect(page.getByRole('list', { name: 'Name test results' }).getByText(/pbx\.example\.com/)).toBeVisible({ timeout: 30000 })
})

// TURN for browser phones: switched on in Settings, it reports its ports.
test('TURN server setting', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/settings')
  const turn = page.getByRole('switch', { name: 'TURN server' })
  if ((await turn.getAttribute('aria-checked')) !== 'true') await turn.click()
  await page.getByLabel('Port (UDP and TCP)').fill('3479')
  await page.getByLabel('TLS port').fill('-1')
  await page.getByLabel('Relay ports from').fill('80')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/relay ports are a range of at least 16/).first()).toBeVisible()
  await page.getByLabel('Relay ports from').fill('49152')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  await expect(page.getByText(/Running on 3479 \(UDP and TCP\)/)).toBeVisible({ timeout: 15000 })
  const ice = await page.evaluate(async () => (await (await fetch('./api/ice')).json()).iceServers)
  expect(ice[0].urls[0]).toMatch(/^turn:.*:3479\?transport=udp$/)
  expect(ice[0].username).toMatch(/^\d+:web$/)
  const s = await page.evaluate(async () => (await (await fetch('./api/settings')).json()).system.turn)
  expect(s.secret.length).toBeGreaterThanOrEqual(24) // administrators see it; viewers never
})

// SIP over WebSocket: switched on in Settings, it shows the address softphones use.
test('SIP over WebSocket setting', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/settings')
  const ws = page.getByRole('switch', { name: 'SIP over WebSocket' })
  if ((await ws.getAttribute('aria-checked')) !== 'true') {
    await ws.click()
    await page.getByRole('button', { name: 'Save', exact: true }).click()
    await expect(page.getByText('Settings saved')).toBeVisible()
  }
  await expect(page.getByText(/^wss:\/\/127\.0\.0\.1:\d+\/ws$/)).toBeVisible()
  await expect(page.getByText(/^\d+ softphones connected now\.$/)).toBeVisible()
  const st = await page.evaluate(async () => (await (await fetch('./api/websocket')).json()))
  expect(st.enabled).toBe(true)
})

// Voice bots: a bot needs an address and a fallback; it is offered as a destination.
test('voice bots over AudioSocket', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/voice-bots')
  await page.getByRole('button', { name: 'Add voice bot' }).click()
  await page.getByLabel('Bot address').fill('not an address')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/enter the bot's address and port/).first()).toBeVisible()
  await page.getByLabel('Bot address').fill('127.0.0.1:9092')
  await page.getByLabel('If the bot cannot take the call, or breaks off').selectOption('echo:')
  await page.getByLabel('Number', { exact: true }).fill('473')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('If the bot fails:')).toBeVisible()
  await expect(page.getByText('127.0.0.1:9092')).toBeVisible()
  await page.goto('/#/routes')
  await page.getByRole('button', { name: 'Add' }).first().click()
  const options = await page.getByLabel('Send calls to').locator('option').allTextContents()
  expect(options.some((o) => o.includes('(voice bot)'))).toBe(true)
})

// MP3 hold music: an MP3 track is added (decoded by the browser, or by BitVoice), an MP3 sent to the
// API is decoded by BitVoice, and a class can play an internet radio stream.
test('MP3 hold music and internet radio', async ({ page }) => {
  const { readFileSync } = await import('node:fs')
  const mp3 = readFileSync(new URL('../../internal/media/mp3/testdata/m2_22k.mp3', import.meta.url))
  await signIn(page)
  await page.goto('/#/sounds')
  await page.getByLabel('New hold music').fill('Radio')
  await page.getByRole('button', { name: 'Add', exact: true }).last().click()
  await expect(page.getByText('Hold music Radio added')).toBeVisible()
  await page.getByLabel('Radio tracks').setInputFiles({ name: 'jingle.mp3', mimeType: 'audio/mpeg', buffer: mp3 })
  await expect(page.getByText('jingle', { exact: true })).toBeVisible()
  // Straight to the API: decoded by BitVoice.
  const secs = await page.evaluate(async (b64) => {
    const body = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
    const r = await fetch('./api/music/radio/raw-mp3', { method: 'PUT', headers: { 'X-BitVoice': '1', 'Content-Type': 'audio/mpeg' }, body })
    return (await r.json()).seconds
  }, mp3.toString('base64'))
  expect(secs).toBe(4)
  await page.getByLabel('Internet radio').first().fill('ftp://nope')
  await page.getByRole('button', { name: 'Set stream' }).click()
  await expect(page.getByText(/enter the stream's http:\/\/ or https:\/\/ address/).first()).toBeVisible()
  await page.getByLabel('Internet radio').first().fill('https://radio.example.com/live.mp3')
  await page.getByRole('button', { name: 'Set stream' }).click()
  await expect(page.getByText('Stream set')).toBeVisible()
  await expect(page.getByText('connects when someone is put on hold')).toBeVisible()
})

// A video room: following the speaker, or showing the leader (which needs a leader PIN).
test('video conference room', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/conferences')
  await page.getByRole('button', { name: 'Add conference room' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Video huddle')
  await page.getByLabel('Video', { exact: true }).selectOption('leader')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/showing the leader needs a leader PIN/).first()).toBeVisible()
  await page.getByLabel('Video', { exact: true }).selectOption('talker')
  await expect(page.getByLabel('Video codec')).toHaveValue('H264')
  await page.getByLabel('Video codec').selectOption('VP8')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('button', { name: /Video huddle/ }).getByText('video', { exact: true })).toBeVisible()
  const rooms = await page.evaluate(async () => (await (await fetch('./api/conferences')).json()).items)
  const r = rooms.find((x: { name: string }) => x.name === 'Video huddle')
  expect(r.video).toBe('talker')
  expect(r.videoCodec).toBe('VP8')
})

// A T1 PRI on the card: its channels are worked out, overlaps with ports are refused, and
// bitvoice-dahdi is told the span's D- and B-channels.
test('T1 and E1 PRI on a telephony card', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/cards')
  const on = page.getByRole('switch', { name: 'Telephony cards' })
  if (!(await on.isChecked())) await on.click()
  await page.getByRole('button', { name: 'Add a T1 or E1' }).click()
  await page.getByLabel('First channel').fill('1')
  await expect(page.getByText('Channels 1 to 24, D-channel 24.')).toBeVisible()
  await page.getByLabel('Line', { exact: true }).selectOption('e1')
  await expect(page.getByText('Channels 1 to 31, D-channel 16.')).toBeVisible()
  await expect(page.getByLabel('Switch type')).toHaveValue('euroisdn')
  await page.getByLabel('First channel').fill('101')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Telephony cards saved')).toBeVisible()
  const token = await page.getByRole('textbox', { name: 'Token' }).inputValue()
  const cfg = await page.evaluate(async (tok) => (await (await fetch('./api/cards/bridge', { headers: { Authorization: 'Bearer ' + tok } })).json()), token)
  expect(cfg.spans[0].d).toBe(116)
  expect(cfg.spans[0].b).toHaveLength(30)
  expect(cfg.spans[0].q931[15]).toBe(17) // timeslot 16 is the D-channel
})

// The outbound dialer: a campaign is made for a queue, a CSV list is loaded (one number on the
// do-not-call list), preview hands out the next contact, and the do-not-call list takes numbers.
test('outbound dialer campaign', async ({ page }) => {
  await signIn(page)
  const qs = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    let q = (await (await fetch('./api/queues')).json()).items
    if (!q.length) {
      const exts = (await (await fetch('./api/extensions')).json()).items
      await fetch('./api/queues', { method: 'POST', headers: H, body: JSON.stringify({ number: '760', name: 'Outbound', agents: [exts[0].number], strategy: 'ring-all', ringSeconds: 15 }) })
      q = (await (await fetch('./api/queues')).json()).items
    }
    return q
  })
  expect(qs.length).toBeGreaterThan(0)
  await page.goto('/#/dialer')
  await page.getByRole('textbox', { name: 'Add numbers' }).fill('3125550199')
  await page.getByRole('textbox', { name: 'Add numbers' }).press('Enter')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByText('3125550199')).toBeVisible()
  await page.getByRole('button', { name: 'Add campaign' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Renewals')
  await page.getByRole('radio', { name: /Preview/ }).click()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Renewals' })).toBeVisible()
  await page.getByRole('button', { name: 'Contacts' }).click()
  await page.getByLabel('Renewals contact list').setInputFiles({ name: 'list.csv', mimeType: 'text/csv',
    buffer: Buffer.from('name,number,plan\nAnn Smith,(312) 555-0101,Gold\nBob Jones,312-555-0199,Silver\nBad,12,x\n') })
  await expect(page.getByText(/2 contacts added, 1 on the do-not-call list, 1 lines skipped/)).toBeVisible()
  await page.getByRole('button', { name: 'Next contact' }).click()
  await expect(page.getByText('Ann Smith', { exact: true })).toBeVisible()
  await expect(page.getByText('Gold')).toBeVisible()
  await page.getByRole('button', { name: 'Skip' }).click()
  await expect(page.getByText('Skipped')).toBeVisible()
  const st = await page.evaluate(async () => (await (await fetch('./api/dialer')).json()))
  expect(st[0].states.dnc).toBe(1)
  expect(st[0].states.pending).toBe(1)
})

// The inbox: the web chat is switched on, a visitor on another site (a page with the script tag) writes,
// the conversation appears in the inbox, an agent answers, and the visitor's box shows the answer.
test('inbox: the web chat box on a website', async ({ page, context }) => {
  await signIn(page)
  await page.goto('/#/inbox')
  await page.getByRole('button', { name: 'Channels' }).click()
  await page.getByRole('switch', { name: 'Web chat' }).click()
  await page.getByLabel('Title').fill('Talk to Echo Lab')
  await page.getByLabel('First line visitors see').fill('Hello! How can we help?')
  await page.getByRole('switch', { name: 'WhatsApp' }).click()
  await page.getByRole('button', { name: 'Save channels' }).click()
  await expect(page.getByText(/enter the phone number ID from WhatsApp Manager/).first()).toBeVisible()
  await page.getByRole('switch', { name: 'WhatsApp' }).click()
  await page.getByRole('button', { name: 'Save channels' }).click()
  await expect(page.getByText('Inbox channels saved')).toBeVisible()
  const base = new URL(page.url()).origin

  await context.grantPermissions(['local-network-access']).catch(() => {})
  const site = await context.newPage()
  await site.route('https://localhost:9/**', (r) => r.fulfill({ contentType: 'text/html',
    body: `<!doctype html><title>Shop</title><h1>Our shop</h1><script src="${base}/webchat.js" async></script>` }))
  await site.goto('https://localhost:9/shop')
  const chat = site.locator('[data-bitvoice-chat]')
  await chat.getByRole('button', { name: 'Chat' }).first().click()
  await expect(chat.getByText('Talk to Echo Lab')).toBeVisible()
  await expect(chat.getByText('Hello! How can we help?')).toBeVisible()
  await chat.getByLabel('Your name').fill('Bob')
  await chat.getByLabel('Message').fill('Do you deliver on Saturdays?')
  await chat.getByRole('button', { name: 'Send' }).click()
  await expect(chat.getByText('Do you deliver on Saturdays?')).toBeVisible()

  await page.getByRole('button', { name: 'Back to the inbox' }).click()
  await page.getByRole('list', { name: 'Conversations list' }).getByText('Bob').click()
  await expect(page.getByText('Do you deliver on Saturdays?')).toBeVisible()
  await page.getByLabel('Reply').fill('Yes, until noon.')
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(chat.getByText('Yes, until noon.')).toBeVisible({ timeout: 10000 })
  await page.getByRole('button', { name: 'Close' }).click()
  await expect(page.getByText('Closed', { exact: true })).toBeVisible()
})
