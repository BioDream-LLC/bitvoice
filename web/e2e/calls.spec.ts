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
