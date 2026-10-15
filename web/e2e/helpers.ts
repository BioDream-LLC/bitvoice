import { expect, type Page } from '@playwright/test'

// Signs in as admin, running the setup wizard first on a fresh server.
export async function signIn(page: Page) {
  await page.goto('/')
  const setup = page.getByRole('heading', { name: 'Welcome to BitVoice' })
  const login = page.getByRole('button', { name: 'Sign in' })
  await expect(setup.or(login)).toBeVisible()
  if (await setup.isVisible()) {
    await page.getByLabel('Organisation name').fill('Echo Lab')
    await page.getByRole('button', { name: 'Next' }).click()
    await page.getByLabel('Password', { exact: true }).fill('a very good password')
    await page.getByLabel('Password again').fill('a very good password')
    await page.getByRole('button', { name: 'Next' }).click()
    await page.getByLabel('Name').nth(1).fill('Lab phone')
    await page.getByRole('button', { name: 'Next' }).click()
    await page.getByRole('switch', { name: 'I have a SIP trunk' }).click()
    await page.getByRole('button', { name: 'Next' }).click()
    await page.getByRole('button', { name: 'Start BitVoice' }).click()
  } else {
    await page.getByLabel('Username').fill('admin')
    await page.getByLabel('Password').fill('a very good password')
    await login.click()
  }
  // a sign-in hashes the password (deliberately slow): give it room on a busy machine
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible({ timeout: 15_000 })
}
