// The screens in other languages. The text the code shows is listed by scripts/i18n-ui.mjs and machine-translated
// into public/i18n/<lang>.json; this puts the translations in as the screens are drawn (a MutationObserver over the
// page), so no screen needs changing and a new screen is covered once its text is listed and translated. Text joined
// from pieces (a template literal) is matched as a pattern with {0}, {1} ... for what it puts in. Anything without a
// translation stays English. Code, editors, addresses and anything marked translate="no" are never touched.

/** The languages, each named in itself. English is the original. */
export const LANGUAGES: Record<string, string> = {
  en: 'English', de: 'Deutsch', fr: 'Français', es: 'Español', it: 'Italiano', pt: 'Português', nl: 'Nederlands', pl: 'Polski',
  sv: 'Svenska', da: 'Dansk', nb: 'Norsk bokmål', fi: 'Suomi', ja: '日本語', zh: '简体中文',
}

const KEY = 'bitvoice.language'

// Each language's translations, a chunk of its own (loaded only when chosen, cached as the other assets).
const catalogs = import.meta.glob<Record<string, string>>(['../i18n/*.json', '!../i18n/strings.json'], { import: 'default' })

/** The language chosen on this browser, else the browser's own when it is one of ours, else English. */
export function currentLanguage(): string {
  try {
    const saved = localStorage.getItem(KEY)
    if (saved && LANGUAGES[saved]) return saved
  } catch { /* storage blocked: the browser's language */ }
  for (const l of navigator.languages ?? [navigator.language]) {
    const base = l.toLowerCase().split('-')[0]
    if (base === 'no' || base === 'nn') return 'nb'
    if (LANGUAGES[base]) return base
  }
  return 'en'
}

/** Changes this browser's language; the page is drawn again in it. */
export function setLanguage(lang: string) {
  try { localStorage.setItem(KEY, lang) } catch { /* not kept: this visit only */ }
  location.reload()
}

interface Pattern { re: RegExp; out: string }
const exact = new Map<string, string>()
// Patterns by a word they always contain (their longest literal word), so a text only tries the few that could match.
const byWord = new Map<string, Pattern[]>()
let active = false

const norm = (s: string) => s.replace(/\s+/g, ' ').trim()
const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

/** Loads a language's translations (for tests, a catalog itself). */
export function loadCatalog(cat: Record<string, string>) {
  exact.clear()
  byWord.clear()
  for (const [en, t] of Object.entries(cat)) {
    if (!/\{\d+\}/.test(en)) { exact.set(en, t); continue }
    const parts = en.split(/(\{\d+\})/)
    const order: number[] = []
    let src = '^'
    parts.forEach((p, i) => {
      const m = /^\{(\d+)\}$/.exec(p)
      if (m) { order.push(Number(m[1])); src += i === parts.length - 2 && parts[parts.length - 1] === '' ? '(.+)' : '(.+?)' } else src += escapeRe(p)
    })
    const re = new RegExp(src + '$', 's')
    // The placeholders of the translation, numbered as in the English, are filled by position in the English.
    const out = t.replace(/\{(\d+)\}/g, (_, n) => `\u0000${order.indexOf(Number(n))}\u0000`)
    const word = (en.replace(/\{\d+\}/g, ' ').match(/[\p{L}]{2,}/gu) ?? []).sort((a, b) => b.length - a.length)[0]
    if (!word) continue
    const k = word.toLowerCase()
    if (!byWord.has(k)) byWord.set(k, [])
    byWord.get(k)!.push({ re, out })
  }
}

/** The translation of a piece of text, or the text itself. Spaces around it are kept. */
export function tr(text: string): string {
  if (!active && exact.size === 0) return text
  const core = norm(text)
  if (!core || !/\p{L}/u.test(core)) return text
  let t = exact.get(core)
  if (t === undefined) {
    const seen = new Set<Pattern>()
    for (const w of core.match(/[\p{L}]{2,}/gu) ?? []) {
      for (const p of byWord.get(w.toLowerCase()) ?? []) {
        if (seen.has(p)) continue
        seen.add(p)
        const m = p.re.exec(core)
        // What the pattern puts in is translated only when it is a whole piece of the screens' own text (a word a
        // ternary chose); a name or a number stays as it is.
        if (m) { t = p.out.replace(/\u0000(\d+)\u0000/g, (_, i) => exact.get(norm(m[Number(i) + 1])) ?? m[Number(i) + 1]); break }
      }
      if (t !== undefined) break
    }
  }
  if (t === undefined) return text
  const lead = /^\s*/.exec(text)![0], trail = /\s*$/.exec(text)![0]
  return (lead ? ' ' : '') + t + (trail ? ' ' : '')
}

// Never translated: code, editors, typed text, and whatever says so.
const SKIP = 'script,style,code,pre,kbd,samp,textarea,input,[contenteditable="true"],[translate="no"],.cm-editor,.notranslate'
const ATTRS = ['placeholder', 'title', 'aria-label', 'alt']

const textDone = new WeakMap<Text, string>() // the translation last put in each text
const attrDone = new WeakMap<Element, Map<string, string>>()

// A part marked translate="yes" inside one marked "no" is translated again, as in HTML.
function skipped(el: Element | null) {
  if (!el) return true
  const no = el.closest(SKIP)
  if (!no) return false
  const yes = el.closest('[translate="yes"]')
  return !yes || !no.contains(yes) || yes === no
}

function doText(n: Text) {
  const v = n.nodeValue ?? ''
  if (textDone.get(n) === v) return // our own change
  if (skipped(n.parentElement)) return
  const t = tr(v)
  if (t !== v) { textDone.set(n, t); n.nodeValue = t }
}

function doAttrs(el: Element) {
  if (skipped(el) && !(el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement)) return
  if (el.closest('[translate="no"],.cm-editor')) return
  let done = attrDone.get(el)
  for (const a of ATTRS) {
    const v = el.getAttribute(a)
    if (v === null || done?.get(a) === v) continue
    const t = tr(v)
    if (t !== v) {
      if (!done) { done = new Map(); attrDone.set(el, done) }
      done.set(a, t)
      el.setAttribute(a, t)
    }
  }
}

function doTree(root: Node) {
  if (root.nodeType === Node.TEXT_NODE) { doText(root as Text); return }
  if (root.nodeType !== Node.ELEMENT_NODE) return
  const el = root as Element
  doAttrs(el)
  const w = document.createTreeWalker(el, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT)
  for (let n = w.nextNode(); n; n = w.nextNode()) {
    if (n.nodeType === Node.TEXT_NODE) doText(n as Text)
    else doAttrs(n as Element)
  }
}

/** Starts putting translations in, in the chosen language; nothing happens for English. */
export async function startTranslation(): Promise<void> {
  const lang = currentLanguage()
  document.documentElement.lang = lang
  if (lang === 'en') return
  const load = catalogs[`../i18n/${lang}.json`]
  if (!load) return
  try { loadCatalog(await load()) } catch { return }
  active = true
  // Dialogs the browser draws itself carry the same messages.
  const { confirm, alert, prompt } = window
  window.confirm = (m?: string) => confirm.call(window, m === undefined ? m : tr(String(m)))
  window.alert = (m?: string) => alert.call(window, m === undefined ? m : tr(String(m)))
  window.prompt = (m?: string, d?: string) => prompt.call(window, m === undefined ? m : tr(String(m)), d)
  const obs = new MutationObserver((list) => {
    for (const m of list) {
      if (m.type === 'characterData') doText(m.target as Text)
      else if (m.type === 'attributes') doAttrs(m.target as Element)
      else m.addedNodes.forEach(doTree)
    }
  })
  obs.observe(document.documentElement, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ATTRS })
  doTree(document.documentElement)
}
