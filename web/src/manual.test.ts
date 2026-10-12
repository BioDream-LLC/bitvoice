import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { manual } from './manual'

// The manual must follow the code: every page has a place in the menu with search words, and every
// page in the menu has an article in the manual that opens from it.
const main = readFileSync(resolve(__dirname, 'main.tsx'), 'utf8')
const shell = readFileSync(resolve(__dirname, 'Shell.tsx'), 'utf8')
const pages = [...main.matchAll(/<Route path="(\/[^"]*)"/g)].map((m) => m[1])
  .filter((p) => !p.includes(':') && !['/phone', '/call', '/help'].includes(p))
const menu = new Map([...shell.matchAll(/\{ to: '([^']+)', label: '([^']+)'[^}]*keys: '([^']*)'/g)].map((m) => [m[1], { label: m[2], keys: m[3] }]))

describe('manual and menu follow the pages', () => {
  it.each(pages)('%s is in the menu with search words', (p) => {
    expect(menu.has(p), `${p} has no menu entry in Shell.tsx`).toBe(true)
    expect(menu.get(p)!.keys.split(' ').length, `${p} has too few search words`).toBeGreaterThanOrEqual(2)
  })
  it.each(pages)('%s has a manual article', (p) => {
    expect(manual.some((a) => a.route === p), `${p} has no article in manual.ts`).toBe(true)
  })
  it('articles are unique and link to real pages', () => {
    const ids = manual.map((a) => a.id)
    expect(new Set(ids).size).toBe(ids.length)
    for (const a of manual) if (a.route) expect(pages.includes(a.route) || a.route === '/help', `${a.id} points at ${a.route}`).toBe(true)
  })
})
