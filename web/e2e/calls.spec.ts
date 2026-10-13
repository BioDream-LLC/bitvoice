import { expect, test } from '@playwright/test'
import { signIn } from './helpers'

// The office call features, set up through the GUI the way an administrator would: a conference room,
// the feature codes and company speed dials, and an extension's follow-me, pickup group and forwarding.
test('conference rooms, feature codes, follow-me and forwarding', async ({ page }) => {
  await signIn(page)

  // A conference room with a PIN; the leader PIN must differ.
  await page.goto('/#/conferences')
  await page.getByRole('button', { name: 'Add conference room' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Daily stand-up')
  await page.getByLabel('PIN', { exact: true }).fill('4321')
  await page.getByLabel('Leader PIN').fill('4321')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('the leader PIN must differ').first()).toBeVisible()
  await page.getByLabel('Leader PIN').fill('8765')
  await page.getByRole('switch', { name: 'Record the room' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: /Daily stand-up/ })).toBeVisible()
  await expect(page.getByRole('button', { name: /Daily stand-up/ }).getByText('Recorded')).toBeVisible()

  // Feature codes: a bad code is refused, a company speed dial is kept.
  await page.goto('/#/codes')
  await page.getByLabel('Forward every call', { exact: true }).fill('72')
  await page.getByRole('button', { name: 'Add a speed dial' }).click()
  await page.getByLabel('Speed dial 1 number').fill('+1 212 555 0199')
  await page.getByLabel('Speed dial 1 name').fill('Head office')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('use a feature code such as *72').first()).toBeVisible()
  await page.getByLabel('Forward every call', { exact: true }).fill('*72')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Feature codes saved').first()).toBeVisible()
  await page.reload()
  await expect(page.getByLabel('Speed dial 1 number')).toHaveValue('+12125550199')

  // An extension: follow-me to a mobile, a pickup group, then forwarding set right now.
  await page.goto('/#/extensions')
  await page.getByRole('row').nth(1).click()
  await page.getByRole('switch', { name: 'Follow-me' }).click()
  await page.getByLabel('Follow-me number 1').fill('2125550123')
  await page.getByLabel('Pickup group').fill('Front office')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/saved/).first()).toBeVisible()
  await page.getByRole('row').nth(1).click()
  await expect(page.getByLabel('Follow-me number 1')).toHaveValue('2125550123')
  await expect(page.getByLabel('Pickup group')).toHaveValue('Front office')
  await page.getByLabel('Forward every call to').fill('100')
  await page.getByRole('button', { name: 'Save forwarding' }).click()
  await expect(page.getByText(/cannot be forwarded to the extension itself|Forwarding saved/).first()).toBeVisible()
  await page.getByRole('region', { name: 'Forwarding right now' }).getByRole('switch', { name: 'Do not disturb' }).click()
  await expect(page.getByText('Do not disturb on').first()).toBeVisible()
  await page.getByRole('region', { name: 'Forwarding right now' }).getByRole('switch', { name: 'Do not disturb' }).click()
  await expect(page.getByText('Do not disturb off').first()).toBeVisible()
})

// The staff portal: a person signs in with their extension and voicemail PIN and sets their own calls.
test('staff portal', async ({ page }) => {
  await signIn(page)
  const ext = await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const all = (await (await fetch('./api/extensions')).json()).items
    const x = all[0]
    x.voicemail = { ...x.voicemail, enabled: true, pin: '2468' }
    x.outbound = true
    const r = await fetch(`./api/extensions/${x.id}`, { method: 'PUT', headers: H, body: JSON.stringify(x) })
    if (!r.ok) throw new Error(await r.text())
    await fetch('./api/auth/logout', { method: 'POST', headers: H })
    return x.number as string
  })
  await page.goto('/#/me')
  await page.getByLabel('Your extension').fill(ext)
  await page.getByLabel('Voicemail PIN').fill('1111')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText('that extension and PIN do not match').first()).toBeVisible()
  await page.getByLabel('Voicemail PIN').fill('2468')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(`Extension ${ext}`)).toBeVisible()

  await page.getByRole('switch', { name: 'Do not disturb' }).click()
  await expect(page.getByText('On: your phones do not ring')).toBeVisible()
  await page.getByLabel('Every call to').fill(ext)
  await page.getByRole('button', { name: 'Save forwarding' }).click()
  await expect(page.getByText('cannot be forwarded to the extension itself').first()).toBeVisible()
  await page.getByLabel('Every call to').fill('')
  await page.getByLabel('When I do not answer, to').fill('+1 212 555 0100')
  await page.getByRole('button', { name: 'Save forwarding' }).click()
  await expect(page.getByText('Forwarding saved').first()).toBeVisible()

  const follow = page.getByRole('switch', { name: 'Follow-me' })
  if (await follow.getAttribute('aria-checked') !== 'true') await follow.click() // the first test may have switched it on
  await page.getByLabel('Follow-me number 1').fill('2125550123')
  await page.getByRole('button', { name: 'Add a speed dial' }).click()
  await page.getByLabel(/^Speed dial \d+ number$/).last().fill('2125550144')
  await page.getByRole('button', { name: 'Save follow-me and speed dials' }).click()
  await expect(page.getByText('Saved', { exact: true }).first()).toBeVisible()

  await page.getByRole('button', { name: /^Voicemail/ }).click()
  await expect(page.getByText('No messages.')).toBeVisible()
  await page.getByRole('button', { name: 'Your calls' }).click()
  await page.getByRole('button', { name: 'PIN' }).click()
  await page.getByLabel('Current PIN').fill('2468')
  await page.getByLabel('New PIN').fill('12')
  await page.getByRole('button', { name: 'Change PIN' }).click()
  await expect(page.getByText('4 to 10 digits').first()).toBeVisible()
  await page.getByLabel('New PIN').fill('13579')
  await page.getByRole('button', { name: 'Change PIN' }).click()
  await expect(page.getByText('PIN changed').first()).toBeVisible()
  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByLabel('Your extension')).toBeVisible()
})

// Hosting another organisation: created from the main system, opened by the provider, signed in to by
// its own administrator at its own address, and seeing none of the main system.
test('hosted organisations', async ({ page, browser }) => {
  await signIn(page)
  await page.goto('/#/tenants')
  await page.getByRole('button', { name: 'Add organisation' }).click()
  await page.getByLabel('Name', { exact: true }).fill('Acme Dental')
  await page.getByLabel('Short name').fill('api')
  await page.getByLabel('Username').fill('boss')
  await page.getByLabel('Password').fill('short')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('is a word BitVoice uses itself').first()).toBeVisible()
  await expect(page.getByText('at least 12 characters').first()).toBeVisible()
  await page.getByLabel('Short name').fill('acme')
  await page.getByLabel('Password').fill('acme long password')
  await page.getByLabel('Extensions', { exact: true }).fill('5')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: 'Acme Dental' })).toBeVisible()
  await expect(page.getByText('acme.<extension>')).toBeVisible()

  // Its own administrator, in a separate browser: the organisation's own phone system, no server screens.
  const other = await browser.newContext()
  const acme = await other.newPage()
  await acme.goto('/t/acme/')
  await acme.getByLabel('Username').fill('boss')
  await acme.getByLabel('Password').fill('acme long password')
  await acme.getByRole('button', { name: 'Sign in' }).click()
  await expect(acme.getByRole('navigation', { name: 'Main' })).toBeVisible()
  await expect(acme.getByRole('navigation', { name: 'Main' }).getByText('Organisations')).toHaveCount(0)
  await expect(acme.getByRole('navigation', { name: 'Main' }).getByText('Updates')).toHaveCount(0)
  await acme.goto('/t/acme/#/extensions')
  await expect(acme.getByText('No extensions yet').first()).toBeVisible()
  await other.close()

  // The provider opens it from the main system.
  await page.getByRole('button', { name: 'Open' }).first().click()
  await expect(page).toHaveURL(/\/t\/acme\//)
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible()
  await page.goto('/#/tenants')
  await page.getByRole('button', { name: 'Acme Dental' }).click()
  await page.getByRole('switch', { name: 'Suspended' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('suspended', { exact: true })).toBeVisible()
})
