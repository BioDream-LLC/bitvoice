import { describe, expect, it } from 'vitest'
import { loadCatalog, tr } from './i18n'

describe('screens in other languages', () => {
  loadCatalog({
    'Save': 'Speichern',
    'Video room {0} {1}': 'Videoraum {0} {1}',
    '{0} is sharing their screen': '{0} teilt den Bildschirm',
    'Calls to {0} go to {1}': 'Anrufe an {1} kommen von {0}', // placeholders moved by the grammar
    'full size': 'volle Größe',
    'Size: {0}': 'Größe: {0}',
  })
  it('translates whole pieces, keeping the spaces around them', () => {
    expect(tr('Save')).toBe('Speichern')
    expect(tr('  Save ')).toBe(' Speichern ')
    expect(tr('Nothing like it')).toBe('Nothing like it')
  })
  it('fills patterns, in the order the translation needs', () => {
    expect(tr('Video room 500 Screen review')).toBe('Videoraum 500 Screen review')
    expect(tr('Dr Lee is sharing their screen')).toBe('Dr Lee teilt den Bildschirm')
    expect(tr('Calls to 100 go to 200')).toBe('Anrufe an 200 kommen von 100')
  })
  it('translates what a pattern puts in only when it is the screens own text', () => {
    expect(tr('Size: full size')).toBe('Größe: volle Größe')
    expect(tr('Size: Dr Lee')).toBe('Größe: Dr Lee')
  })
  it('leaves numbers and empty text alone', () => {
    expect(tr('42')).toBe('42')
    expect(tr('   ')).toBe('   ')
  })
})

// Every language's translations: only pieces the screens show, each with the same placeholders.
import { readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
describe('the translation files', () => {
  const dir = resolve(__dirname, '..', 'i18n')
  const strings = new Set<string>(JSON.parse(readFileSync(resolve(dir, 'strings.json'), 'utf8')))
  const files = readdirSync(dir).filter((f) => f !== 'strings.json' && f.endsWith('.json'))
  it('covers every language the picker offers', async () => {
    const { LANGUAGES } = await import('./i18n')
    expect(files.map((f) => f.replace('.json', '')).sort()).toEqual(Object.keys(LANGUAGES).filter((l) => l !== 'en').sort())
  })
  it.each(files)('%s keeps placeholders', (f) => {
    const cat: Record<string, string> = JSON.parse(readFileSync(resolve(dir, f), 'utf8'))
    const ph = (s: string) => (s.match(/\{\d+\}/g) ?? []).sort().join()
    for (const [en, t] of Object.entries(cat)) {
      expect(ph(t), `${f}: ${en}`).toBe(ph(en))
    }
    // Text newer than the translations stays English; most must be there (make ui-strings, then translate).
    expect(Object.keys(cat).filter((k) => strings.has(k)).length).toBeGreaterThan(strings.size * 0.5)
  })
})
