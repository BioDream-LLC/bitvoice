import { test } from '@playwright/test'
import { shot } from './paths'
// Screenshots for a visual check; run with SHOTS=1. Instance name is set explicitly (no hostnames).
test.skip(!process.env.SHOTS, 'screenshots only on request')
test('screenshots', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/')
  await page.screenshot({ path: shot('setup') })
  await page.getByLabel('Organisation name').fill('Acme Dental')
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByLabel('Password', { exact: true }).fill('a very good password')
  await page.getByLabel('Password again').fill('a very good password')
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByLabel('Name').nth(1).fill('Dr Lee')
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByRole('switch', { name: 'I have a SIP trunk' }).click()
  await page.getByRole('button', { name: 'Next' }).click()
  await page.getByRole('button', { name: 'Start BitVoice' }).click()
  await page.getByRole('heading', { name: 'Acme Dental' }).waitFor()
  await page.waitForTimeout(600)
  await page.screenshot({ path: shot('dashboard') })
  await page.goto('/#/extensions')
  await page.getByRole('cell', { name: 'Dr Lee' }).click()
  await page.waitForTimeout(400)
  await page.screenshot({ path: shot('extension') })
  await page.evaluate(async () => {
    const H = { 'X-BitVoice': '1', 'Content-Type': 'application/json' }
    const post = (u: string, b: unknown) => fetch(u, { method: 'POST', headers: H, body: JSON.stringify(b) }).then((r) => r.json())
    const exts = (await (await fetch('./api/extensions')).json()).items as { number: string }[]
    const rg = await post('./api/ring-groups', { number: '600', name: 'Front office', strategy: 'all', members: exts.map((x) => x.number), ringSeconds: 20 })
    const g = rg.item ?? rg
    const sched = await post('./api/schedules', { name: 'Office hours', hours: [{ days: [1, 2, 3, 4, 5], start: '09:00', end: '17:00' }], holidays: [],
      open: { type: 'ringgroup', target: g.number }, closed: { type: 'voicemail', target: exts[0].number } })
    await post('./api/inbound-routes', { name: 'Main number', did: '+12125550100', trunk: '', destination: { type: 'schedule', target: sched.item?.id ?? sched.id } })
  })
  await page.goto('/#/flow')
  await page.waitForTimeout(800)
  await page.screenshot({ path: shot('flow') })
  await page.goto('/#/menus')
  await page.getByRole('button', { name: 'Add menu' }).click()
  await page.getByRole('button', { name: 'Use key 1' }).click()
  await page.waitForTimeout(400)
  await page.screenshot({ path: shot('menu') })
  await page.goto('/#/schedules')
  await page.getByRole('button', { name: 'Add schedule' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await page.waitForTimeout(500)
  await page.screenshot({ path: shot('schedules') })
})
