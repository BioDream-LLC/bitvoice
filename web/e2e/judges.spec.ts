import { expect, test } from '@playwright/test'
import { signIn } from './helpers'

// The browser phone needs a microphone: Chromium's fake one.
test.use({ launchOptions: { args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'] } })

const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }

// The call-plan checker tries every situation and finds a call that goes nowhere; "Try this call" traces it.
test('check every situation finds a dead end and traces it', async ({ page }) => {
  await signIn(page)
  const ids = await page.evaluate(async (h) => {
    const post = async (u: string, b: unknown) => (await (await fetch('./api/' + u, { method: 'POST', headers: h, body: JSON.stringify(b) })).json()).item
    const x = await post('extensions', { number: '198', name: 'Gone home', enabled: false, ringSeconds: 20 })
    const g = await post('ring-groups', { number: '655', name: 'Nobody here', strategy: 'all', members: ['198'], ringSeconds: 10 })
    const r = await post('inbound-routes', { name: 'Dead line', did: '+15550001111', trunk: '', destination: { type: 'ringgroup', target: '655' } })
    return { x: x.id, g: g.id, r: r.id }
  }, H)
  await page.goto('/#/flow')
  const pc = page.getByRole('region', { name: 'Check every situation' })
  await expect(pc.getByText(/calls tried across/)).toBeVisible({ timeout: 20000 })
  const dead = pc.getByRole('listitem').filter({ hasText: 'Nobody in Nobody here' }).first()
  await expect(dead.getByText('A call goes nowhere')).toBeVisible()
  await expect(dead.getByText('a call to +15550001111 → Then')).toBeVisible()
  await dead.getByRole('button', { name: 'Try this call' }).first().click()
  await expect(page.getByLabel('Dials')).toHaveValue('+15550001111')
  await expect(page.getByRole('list', { name: 'The path the call takes' }).getByText(/Nobody in Nobody here/).first()).toBeVisible()
  // Fixed: the finding goes.
  await page.evaluate(async ({ h, ids }) => {
    await fetch('./api/inbound-routes/' + ids.r, { method: 'DELETE', headers: h })
    await fetch('./api/ring-groups/' + ids.g, { method: 'DELETE', headers: h })
    await fetch('./api/extensions/' + ids.x, { method: 'DELETE', headers: h })
  }, { h: H, ids })
  await pc.getByRole('button', { name: 'Check again' }).click()
  await expect(pc.getByText('Nobody in Nobody here')).toHaveCount(0)
})

// The toll-fraud guard and the trunk health judge are set in Settings; Security shows the judges' rulebooks,
// each checked in every situation, and the guard's judgements.
test('the judges: toll-fraud guard, trunk health judge and their rulebooks', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/settings')
  const fg = page.getByRole('region', { name: 'Toll-fraud guard' })
  await fg.getByRole('switch', { name: 'Toll-fraud guard' }).click()
  await fg.getByLabel('Calls abroad in 10 minutes').fill('3')
  await fg.getByLabel('Countries always allowed').fill('44, 33')
  await fg.getByRole('button', { name: 'Add an exception' }).click()
  await fg.getByLabel('Exception 1: extension').selectOption({ index: 1 })
  await page.getByRole('button', { name: 'Save', exact: true }).first().click()
  await expect(fg.getByText('choose the last day it applies')).toBeVisible()
  await fg.getByLabel('Exception 1: until').fill('2099-12-31')
  await fg.getByLabel('Exception 1: note').fill('Trip to London')
  const tj = page.getByRole('region', { name: 'Trunk health judge' })
  await tj.getByLabel('Failed calls in a row').fill('4')
  await page.getByRole('button', { name: 'Save', exact: true }).first().click()
  await expect(page.getByText(/Saved|saved/).first()).toBeVisible()
  const sec = await page.evaluate(async () => (await (await fetch('./api/settings')).json()))
  expect(sec.security.fraudGuard.on).toBe(true)
  expect(sec.security.fraudGuard.allowedCountries).toEqual(['44', '33'])
  expect(sec.security.fraudGuard.exceptions[0].note).toBe('Trip to London')
  expect(sec.system.trunkJudging.failures).toBe(4)

  await page.goto('/#/security')
  await expect(page.getByRole('heading', { name: 'Calls abroad the toll-fraud guard judged' })).toBeVisible()
  const rb = page.getByRole('region', { name: 'How BitVoice decides' })
  for (const name of ['Trunk health', 'Toll-fraud guard', 'Call quality diagnosis', 'Caller location check']) {
    await expect(rb.getByText(name, { exact: true })).toBeVisible()
  }
  await expect(rb.getByText(/checked in all 512 situations/)).toBeVisible() // the toll-fraud rulebook's nine facts
  await rb.getByText('Toll-fraud guard', { exact: true }).click()
  await expect(rb.getByText('an emergency call is never stopped').first()).toBeVisible()
  // Put things back for the other tests.
  await page.goto('/#/settings')
  await page.getByRole('region', { name: 'Toll-fraud guard' }).getByRole('switch', { name: 'Toll-fraud guard' }).click()
  await page.getByRole('button', { name: 'Save', exact: true }).first().click()
  await expect(page.getByText(/Saved|saved/).first()).toBeVisible()
})

// A call that cannot be put through says, in Call history, how it was routed.
test('call history says how a call was routed', async ({ page }) => {
  await signIn(page)
  const g = await page.evaluate(async (h) => {
    const post = async (u: string, b: unknown) => (await (await fetch('./api/' + u, { method: 'POST', headers: h, body: JSON.stringify(b) })).json()).item
    const x = await post('extensions', { number: '197', name: 'On leave', enabled: false, ringSeconds: 20 })
    const g = await post('ring-groups', { number: '656', name: 'Empty desk', strategy: 'all', members: ['197'], ringSeconds: 10 })
    return { id: g.id, x: x.id }
  }, H)
  await page.getByRole('button', { name: 'Browser phone' }).click()
  await page.getByRole('button', { name: /^Sign in as/ }).click()
  for (const k of ['6', '5', '6']) await page.getByRole('group', { name: 'Keypad' }).getByRole('button', { name: k, exact: true }).click()
  await page.getByRole('button', { name: 'Call', exact: true }).click()
  await page.goto('/#/history')
  const row = page.getByRole('row').filter({ hasText: '656' }).first()
  await expect(row).toBeVisible({ timeout: 15000 })
  await row.getByRole('button', { name: 'How it was routed' }).click()
  await expect(row.getByText(/Nobody in Empty desk had a phone connected/)).toBeVisible()
  await page.evaluate(async ({ h, g }) => {
    await fetch('./api/ring-groups/' + g.id, { method: 'DELETE', headers: h })
    await fetch('./api/extensions/' + g.x, { method: 'DELETE', headers: h })
  }, { h: H, g })
})
