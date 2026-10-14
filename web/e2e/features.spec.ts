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
