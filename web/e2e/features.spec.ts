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
