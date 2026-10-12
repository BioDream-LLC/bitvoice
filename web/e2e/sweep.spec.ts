import { readFileSync } from 'node:fs'
import { expect, test, type Page } from '@playwright/test'

// One journey through the product: setup wizard, then every screen in every theme, failing on console
// errors, failed requests, unnamed buttons or a screen without a heading. Then real edits through the GUI.
// Every page the app routes to, read from main.tsx so a new page is swept without being listed here.
const screens = [...readFileSync(new URL('../src/main.tsx', import.meta.url), 'utf8').matchAll(/<Route path="(\/[^"]*)"/g)]
  .map((m) => m[1]).filter((p) => !p.includes(':') && p !== '/phone' && p !== '/call')

function watch(page: Page) {
  const problems: string[] = []
  // The browser logs every non-2xx response as an error. A 401 before sign-in and a 422 for a refused form
  // are the API working as designed; anything else is a real problem.
  page.on('console', (m) => {
    if (m.type() === 'error' && !/status of (401|422)/.test(m.text())) problems.push('console: ' + m.text())
  })
  page.on('pageerror', (e) => problems.push('page: ' + e.message))
  page.on('response', (r) => { if (r.status() >= 500) problems.push(`${r.status()} ${r.url()}`) })
  return problems
}

async function unnamedButtons(page: Page) {
  return page.$$eval('button, a[href], input, select, textarea', (els) => els.filter((el) => {
    const e = el as HTMLElement
    if (e.offsetParent === null || (e as HTMLInputElement).type === 'hidden') return false
    const label = e.getAttribute('aria-label') || e.getAttribute('title') || e.textContent?.trim()
    const labelled = (e.id && document.querySelector(`label[for="${e.id}"]`)) || e.closest('label')?.textContent?.trim()
    return !label && !labelled && !e.getAttribute('placeholder')
  }).map((e) => e.outerHTML.slice(0, 120)))
}

test('setup wizard, every screen, and editing through the GUI', async ({ page }) => {
  const problems = watch(page)
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Welcome to BitVoice' })).toBeVisible()
  await page.getByLabel('Organisation name').fill('Acme Dental')
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByLabel('Your name').fill('Practice Owner')
  await page.getByLabel('Password', { exact: true }).fill('a very good password')
  await page.getByLabel('Password again').fill('a very good password')
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByLabel('Name').nth(1).fill('Dr Lee')
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByRole('switch', { name: 'I have a SIP trunk' }).click()
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByRole('button', { name: 'Start BitVoice' }).click()
  await expect(page.getByRole('heading', { name: 'Acme Dental' })).toBeVisible()

  for (const theme of ['Midnight', 'Dark', 'Light']) {
    await page.getByRole('radio', { name: theme }).click()
    for (const s of screens) {
      await page.goto('/#' + s)
      await expect(page.locator('h1')).toBeVisible()
      expect(await unnamedButtons(page), `unnamed controls on ${s} (${theme})`).toEqual([])
    }
  }
  await page.getByRole('radio', { name: 'Midnight' }).click()

  // Add an extension through the GUI.
  await page.goto('/#/extensions')
  await page.getByRole('button', { name: 'Add extension' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Hygienist')
  await page.getByLabel('More detail').fill('Suite 200, room 3')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('cell', { name: 'Hygienist' })).toBeVisible()

  // A duplicate number is refused with a field message.
  await page.getByRole('button', { name: 'Add extension' }).click()
  await page.getByLabel('Number', { exact: true }).fill('100')
  await page.getByLabel('Name', { exact: true }).fill('Duplicate')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/already used/).first()).toBeVisible()
  await page.getByRole('button', { name: 'Cancel' }).click()

  // A ring group and a trunk.
  await page.goto('/#/ring-groups')
  await page.getByRole('button', { name: 'Add ring group' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Front office')
  await page.getByRole('button', { name: /^100/ }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('cell', { name: 'Front office' })).toBeVisible()

  // A queue with an agent shows on the wallboard with the agent's live state.
  await page.goto('/#/queues')
  await page.getByRole('button', { name: 'Add queue' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Support')
  await page.getByRole('button', { name: /^100/ }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('heading', { name: 'Support' })).toBeVisible()
  await expect(page.getByText('No phone').first()).toBeVisible()

  // A menu: key 1 rings the front office ring group; then record its prompt by upload.
  await page.goto('/#/menus')
  await page.getByRole('button', { name: 'Add menu' }).click()
  await page.getByRole('button', { name: 'Key 1', exact: true }).click()
  await page.getByRole('button', { name: 'Use key 1' }).click()
  await page.getByLabel('What callers hear it as').fill('Front office')
  await page.getByLabel('Sends callers to').selectOption({ label: '600 Front office' })
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('No prompt')).toBeVisible()
  await page.getByRole('heading', { name: 'Main menu' }).click()
  await page.getByLabel('Prompt file').setInputFiles({ name: 'prompt.wav', mimeType: 'audio/wav', buffer: toneWav() })
  await expect(page.getByText('Prompt saved')).toBeVisible()
  await page.getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByText('Prompt recorded')).toBeVisible()

  // A routing script: the example sends 212 numbers to the ring group; try it, then save it.
  await page.goto('/#/scripts')
  await page.getByRole('button', { name: 'Add script' }).click()
  await page.getByRole('button', { name: 'Run' }).click()
  await expect(page.getByText('ringgroup 600')).toBeVisible()
  await expect(page.getByText('log: New York caller')).toBeVisible()
  await page.getByRole('radio', { name: 'Lua' }).click()
  await page.getByRole('button', { name: 'Run' }).click()
  await expect(page.getByText('ringgroup 600')).toBeVisible()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Script Route by caller saved')).toBeVisible()

  // A schedule open around the clock shows as open now.
  await page.goto('/#/schedules')
  await page.getByRole('button', { name: 'Add schedule' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Always open')
  for (const d of ['Sun', 'Sat']) await page.getByRole('button', { name: d, exact: true }).click()
  await page.getByLabel('Opens at').fill('00:00')
  await page.getByLabel('Closes at').fill('23:59')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Open now')).toBeVisible()

  // Automatic phone set-up: switch it on, add a phone by MAC address.
  await page.goto('/#/phones')
  // Opened fresh (a bookmark, a reload), #/phones is this page and not the staff phone at #/phone.
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Phones', exact: true })).toBeVisible()
  await page.getByRole('switch', { name: 'Automatic set-up' }).click()
  await expect(page.getByText('Provisioning saved')).toBeVisible()
  await page.getByRole('button', { name: 'Add phone' }).click()
  await page.getByLabel('MAC address').fill('80-5e-c0-11-22-33')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('80:5E:C0:11:22:33')).toBeVisible()

  await page.goto('/#/trunks')
  await page.getByRole('button', { name: 'Add trunk' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Main line')
  await page.getByLabel('Provider server').fill('127.0.0.1')
  await page.getByLabel('Port', { exact: true }).fill('25999')
  await page.getByLabel('Username', { exact: true }).fill('acct')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Main line').first()).toBeVisible()

  // Undo the trunk from the change history.
  await page.goto('/#/changes')
  await expect(page.getByText('Added trunk Main line')).toBeVisible()
  await page.getByRole('button', { name: /^Restore revision/ }).first().click()
  // It shows what restoring would change (the trunk going away) and waits for a yes.
  await expect(page.getByRole('group', { name: /^Restore revision/ }).getByText(/Main line/)).toBeVisible()
  await page.getByRole('button', { name: /^Yes, restore revision/ }).click()
  await expect(page.getByText(/Restored revision/).first()).toBeVisible()

  // A greeting uploaded in the extension editor is converted in the browser and played back.
  await page.goto('/#/extensions')
  await page.getByRole('cell', { name: 'Dr Lee' }).click()
  await expect(page.getByText(/callers hear a beep only/)).toBeVisible()
  await page.getByLabel('Greeting file').setInputFiles({ name: 'hello.wav', mimeType: 'audio/wav', buffer: toneWav() })
  await expect(page.getByText('Greeting saved')).toBeVisible()
  await expect(page.getByLabel('Current greeting')).toBeVisible()
  await page.getByRole('button', { name: 'Cancel' }).click()

  // Import a FreePBX extension list: 100 is already here, 150 is new.
  await page.goto('/#/import')
  await page.getByLabel('Files to import').setInputFiles({ name: 'extensions.csv', mimeType: 'text/csv',
    buffer: Buffer.from('extension,name,secret,voicemail\n100,Taken,abcdefghijklmnop,default\n150,Imported Person,abcdefghijklmnop,default\n') })
  await expect(page.getByText('number already used here')).toBeVisible()
  await page.getByRole('button', { name: 'Add 1 item' }).click()
  await expect(page.getByText('Imported 1')).toBeVisible()
  await page.goto('/#/extensions')
  await expect(page.getByRole('cell', { name: 'Imported Person' })).toBeVisible()

  // Archive: choose S3, see the least-privilege policy, and a bad region is refused with a field message.
  await page.goto('/#/archive')
  await page.getByRole('radio', { name: /Amazon S3/ }).click()
  await page.getByLabel('Region').fill('nowhere')
  await page.getByLabel('Bucket', { exact: true }).fill('acme-phones')
  await expect(page.locator('pre')).toContainText('arn:aws:s3:::acme-phones/*')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('an AWS region looks like us-east-1')).toBeVisible()
  await page.getByRole('radio', { name: /^Off/ }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Archive saved')).toBeVisible()

  // Settings save and the command palette.
  await page.goto('/#/settings')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  await page.keyboard.press('ControlOrMeta+k')
  await page.getByPlaceholder(/Go to a screen/).fill('trace')
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'SIP trace' })).toBeVisible()

  expect(problems).toEqual([])
})

// One second of 440 Hz at 44.1 kHz stereo, as a laptop would record it.
function toneWav(): Buffer {
  const rate = 44100, n = rate
  const b = Buffer.alloc(44 + n * 4)
  b.write('RIFF', 0); b.writeUInt32LE(36 + n * 4, 4); b.write('WAVEfmt ', 8); b.writeUInt32LE(16, 16)
  b.writeUInt16LE(1, 20); b.writeUInt16LE(2, 22); b.writeUInt32LE(rate, 24); b.writeUInt32LE(rate * 4, 28)
  b.writeUInt16LE(4, 32); b.writeUInt16LE(16, 34); b.write('data', 36); b.writeUInt32LE(n * 4, 40)
  for (let i = 0; i < n; i++) {
    const v = Math.round(Math.sin(2 * Math.PI * 440 * i / rate) * 8000)
    b.writeInt16LE(v, 44 + i * 4); b.writeInt16LE(v, 46 + i * 4)
  }
  return b
}
