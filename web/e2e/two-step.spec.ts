import { createHmac } from 'node:crypto'
import { expect, test } from '@playwright/test'
import { signIn } from './helpers'

// RFC 6238 code from a base32 secret, as an authenticator app computes it.
function totp(secret: string, at = Date.now()) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const c of secret.replace(/=+$/, '')) bits += alphabet.indexOf(c).toString(2).padStart(5, '0')
  const key = Buffer.from(bits.match(/.{8}/g)!.map((b) => parseInt(b, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 30000)))
  const h = createHmac('sha1', key).update(counter).digest()
  const off = h[h.length - 1] & 15
  return String((h.readUInt32BE(off) & 0x7fffffff) % 1000000).padStart(6, '0')
}

// Two-step sign-in set up through the GUI, used to sign in, then turned off again.
test('two-step sign-in', async ({ page }) => {
  await signIn(page)
  await page.goto('/#/users')
  await page.getByRole('button', { name: 'Set up' }).click()
  const key = (await page.locator('span.break-all').innerText()).trim()
  await page.getByLabel('Then enter the code it shows').fill(totp(key))
  await page.getByRole('button', { name: 'Turn on' }).click()
  await expect(page.getByText('Two-step sign-in is on')).toBeVisible()
  await page.getByRole('button', { name: 'Sign out' }).click()
  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password').fill('a very good password')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText('enter the code from your authenticator app')).toBeVisible()
  await page.getByLabel('Code from your authenticator app').fill(totp(key, Date.now() + 30000))
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible()
  await page.goto('/#/users')
  await page.getByLabel('Your password, to turn it off').fill('a very good password')
  await page.getByRole('button', { name: 'Turn off' }).click()
  await expect(page.getByText('Two-step sign-in is off')).toBeVisible()
})
