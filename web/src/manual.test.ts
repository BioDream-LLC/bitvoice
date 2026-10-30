import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { manual } from './manual'
import { freshName } from './ui'
import { paletteFilter } from './Shell'

// The manual must follow the code: every page has a place in the menu with search words, and every
// page in the menu has an article in the manual that opens from it.
const main = readFileSync(resolve(__dirname, 'main.tsx'), 'utf8')
const shell = readFileSync(resolve(__dirname, 'Shell.tsx'), 'utf8')
const pages = [...main.matchAll(/<Route path="(\/[^"]*)"/g)].map((m) => m[1])
  // /connect-assistant is opened only by an assistant's sign-in (its chapter is AI assistants).
  .filter((p) => !p.includes(':') && !['/phone', '/call', '/help', '/connect-assistant'].includes(p))
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

describe('the manual is complete', () => {
  it('every article is in exactly one part', async () => {
    const { parts } = await import('./manual')
    const seen = parts.flatMap((p) => p.ids)
    expect(new Set(seen).size, 'an article is in two parts').toBe(seen.length)
    for (const a of manual) expect(seen.includes(a.id), `${a.id} is in no part`).toBe(true)
    for (const id of seen) expect(manual.some((a) => a.id === id), `part lists ${id}, which is no article`).toBe(true)
  })
  it('figures and links point at things that exist', async () => {
    const { figures } = await import('./figures')
    for (const a of manual) {
      for (const m of a.body.matchAll(/^::figure (\S+)/gm)) expect(figures[m[1]], `${a.id} shows figure ${m[1]}`).toBeTruthy()
      for (const m of a.body.matchAll(/\]\(#([a-z0-9-]+)\)/g)) expect(manual.some((x) => x.id === m[1]), `${a.id} links to chapter ${m[1]}`).toBe(true)
    }
  })
})

it('numbers a default name already taken', () => {
  expect(freshName('Main menu', [])).toBe('Main menu')
  expect(freshName('Main menu', ['Main menu'])).toBe('Main menu 2')
  expect(freshName('Main menu', ['main menu', 'Main menu 2'])).toBe('Main menu 3')
})

it('ranks a page named what was typed above one with it among its keywords', () => {
  expect(paletteFilter('Phones devices provisioning', 'phones')).toBeGreaterThan(paletteFilter('Extensions users phones people', 'phones'))
  expect(paletteFilter('Trunks carrier', 'dr lee')).toBe(0)
})
