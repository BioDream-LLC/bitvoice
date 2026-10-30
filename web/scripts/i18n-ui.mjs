// Lists every piece of text the web interface shows, for translation of its screens.
//
//   node web/scripts/i18n-ui.mjs extract        writes web/i18n/strings.json from src/**/*.tsx and *.ts
//   OPENROUTER_API_KEY=... node web/scripts/i18n-ui.mjs translate de fr ...
//                                               writes web/i18n/<lang>.json (only what is new)
//   node web/scripts/i18n-ui.mjs check          says how much of each language is translated
//
// Text is read from the code with the TypeScript compiler: JSX text, and string and template literals that read as words
// (not class names, addresses, identifiers or codes). A template literal becomes a pattern with {0}, {1} ... for what it
// puts in. The interface (src/i18n.ts) puts the translations in as the screens are drawn, so no screen needs changing; a
// translation is kept only when it keeps the same placeholders, and anything untranslated stays English.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const src = path.join(web, 'src')
const catalogPath = path.join(web, 'i18n', 'strings.json')
const outDir = path.join(web, 'i18n')
// The manual's languages (scripts/i18n-manual.mjs), so the screens and their manual come in the same ones.
export const LANGUAGES = { de: 'German', fr: 'French', es: 'Spanish', it: 'Italian', pt: 'Portuguese (Portugal)', nl: 'Dutch', pl: 'Polish',
  sv: 'Swedish', da: 'Danish', nb: 'Norwegian Bokmål', fi: 'Finnish', ja: 'Japanese', zh: 'Simplified Chinese' }

// Files that are not screens' text: the manual (translated on its own), figures, tests.
const skipFile = (f) => /(^|\/)(manual|figures)\.ts$|\.test\.tsx?$/.test(f)
// JSX attributes whose values are never shown.
const codeAttr = new Set(['className', 'class', 'key', 'id', 'htmlFor', 'type', 'role', 'href', 'src', 'name', 'autoComplete', 'inputMode',
  'rel', 'target', 'method', 'accept', 'value', 'defaultValue', 'to', 'path', 'icon', 'status', 'accent', 'variant', 'size', 'side',
  'align', 'mode', 'kind', 'lang', 'dir', 'pattern', 'step', 'min', 'max', 'download', 'form', 'sizes', 'loading', 'enterKeyHint',
  'aria-labelledby', 'aria-describedby', 'aria-controls', 'aria-hidden', 'aria-live', 'aria-current', 'aria-haspopup', 'spellCheck', 'testId'])
// Calls whose string arguments are never shown.
const codeCall = /^(fetch|console\.\w+|new URL|URL|encodeURIComponent|decodeURIComponent|document\.\w+|\w+\.querySelector(All)?|\w*[sS]torage\.\w+|\w+\.addEventListener|\w+\.removeEventListener|JSON\.\w+|RegExp|new RegExp|\w+\.startsWith|\w+\.endsWith|\w+\.includes|\w+\.split|\w+\.replace(All)?|\w+\.match|\w+\.test|\w+\.indexOf|\w+\.get|\w+\.set|\w+\.has|\w+\.delete|\w+\.append|\w+\.getAttribute|\w+\.setAttribute|clsx|cn|lazy|import|require|\w+\.dispatchEvent|new CustomEvent|new Event|\w+\.postMessage|\w+\.join|new Intl\.\w+|\w+\.toLocale\w*String|\w+\.padStart|\w+\.padEnd|\w+\.at|onServerEvent|\w+\.emit|\w+\.on|navigate|useParam|\w+\.searchParams\.\w+|new URLSearchParams|\w+\.createElement|setHash|\w+\.click|\w+\.focus)$/

const words = (s) => (s.match(/[\p{L}]{2,}/gu) ?? []).length
// readsAsText: words a person reads, not a class list, an address, an identifier, a CSS value or a code.
export function readsAsText(s, jsx = false) {
  const t = s.trim()
  if (!t || words(t) === 0 || t.length > 2000) return false
  if (!/\s/.test(t)) {
    // One token. JSX text is shown as written; a string is a word only if it reads as one (Save, Voicemail), never a path
    // (system.ldap.bindDn), an address piece, a CSS value or a lower-case key.
    if (/[./\\=_?&#@{}<>[\]()]/.test(t.replace(/[.!?:…]$/, '').replace(/\{\d+\}/g, '')) && !jsx) return false
    if (!jsx && !/^[\p{Lu}][\p{L}'’-]*[.!?:…]?$/u.test(t.replace(/\{\d+\}/g, ''))) return false
  }
  if (/\b\d+(px|em|rem|vh|vw|ms)\b|#[0-9a-f]{3,8}\b|\b(solid|dashed|rgba?|hsla?|var)\(?/i.test(t) && !jsx) return false
  if (/^(https?:|wss?:|mailto:|tel:|data:|blob:|\.\/|\/|#\/|\.\.\/)/.test(t) || /^[\w.+-]+@[\w-]+\.[\w.]+$/.test(t)) return false
  if (/^[#.]?[\w-]+$/.test(t) && !/^[\p{Lu}][\p{Ll}]+$/u.test(t) && !/^[\p{Lu}][\p{Ll}]+[.!?:]?$/u.test(t)) {
    // One token: kept only as a capitalised word (Save, Extensions); not identifiers, codes or keys.
    return false
  }
  const tokens = t.split(/\s+/)
  const codeish = tokens.filter((x) => /[:[\]]|^-?[a-z]+(-[a-z0-9./[\]%]+)+$|^\w+=|^[a-z]+\d+$|[{};<>]/.test(x)).length
  if (codeish * 2 >= tokens.length) return false
  if (/^[A-Z0-9_]{2,}$/.test(t)) return false // a constant or an acronym alone
  if (/^\d[\d\s.,:/-]*$/.test(t)) return false
  return true
}

function callName(n) {
  const e = n.expression
  if (ts.isNewExpression(n)) return 'new ' + (e.getText?.() ?? '')
  return e.getText()
}

function shownString(node) {
  // A string literal in a place whose text is never shown is skipped.
  let p = node.parent
  for (let depth = 0; p && depth < 6; depth++, p = p.parent) {
    if (ts.isJsxAttribute(p)) return !codeAttr.has(p.name.getText()) && !p.name.getText().startsWith('data-')
    if (ts.isImportDeclaration(p) || ts.isExportDeclaration(p) || ts.isLiteralTypeNode(p) || ts.isTypeNode(p)) return false
    if (ts.isElementAccessExpression(p) && p.argumentExpression === node) return false
    if (ts.isPropertyAssignment(p) && p.name === node) return false
    if (ts.isPropertyAssignment(p) && depth === 0) {
      const k = p.name.getText().replace(/['"]/g, '')
      if (/^(id|key|value|type|kind|to|path|href|icon|status|className|url|method|mode|route|tag|field|code|ids|keys|accent|colour|color|pattern|prefix|format|unit|name)$/.test(k) && k !== 'name') return false
    }
    if (ts.isBinaryExpression(p) && [ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken, ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken].includes(p.operatorToken.kind)) return false
    if (ts.isCaseClause(p) && p.expression === node) return false
    if ((ts.isCallExpression(p) || ts.isNewExpression(p)) && codeCall.test(callName(p))) return false
  }
  return true
}

export function extract() {
  const files = []
  const walk = (d) => {
    for (const f of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, f.name)
      if (f.isDirectory()) walk(p)
      else if (/\.tsx?$/.test(f.name) && !skipFile(path.relative(src, p))) files.push(p)
    }
  }
  walk(src)
  const found = new Map()
  const add = (s, file, jsx = false) => {
    s = s.replace(/\s+/g, ' ').trim()
    if (!readsAsText(s.replace(/\{\d+\}/g, ' x '), jsx) || !readsAsText(s, jsx)) return
    if (!found.has(s)) found.set(s, new Set())
    found.get(s).add(path.relative(src, file))
  }
  for (const file of files) {
    const sf = ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
    const visit = (n) => {
      if (ts.isJsxText(n)) {
        add(n.getText(), file, true)
      } else if ((ts.isStringLiteral(n) || ts.isNoSubstitutionTemplateLiteral(n)) && shownString(n)) {
        add(n.text, file)
      } else if (ts.isTemplateExpression(n) && shownString(n)) {
        let s = n.head.text
        n.templateSpans.forEach((sp, i) => { s += `{${i}}` + sp.literal.text })
        if (!/^\{0\}$/.test(s.trim())) add(s, file)
        // What a template puts in can be text too (a ternary's strings): those are visited below.
      }
      ts.forEachChild(n, visit)
    }
    visit(sf)
  }
  // Messages the server sends to the screens (field problems, refusals, errors), written in Go: a literal, or literals
  // joined with what they put in ("no extension "+ext becomes "no extension {0}").
  const goRoot = path.join(web, '..', 'internal')
  const goFiles = []
  const walkGo = (d) => {
    for (const f of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, f.name)
      if (f.isDirectory() && !['testdata', 'web'].includes(f.name)) walkGo(p)
      else if (f.name.endsWith('.go') && !f.name.endsWith('_test.go')) goFiles.push(p)
    }
  }
  for (const d of ['api', 'config', 'pbx', 'store']) walkGo(path.join(goRoot, d))
  const lit = '"((?:[^"\\\\]|\\\\.)*)"'
  const expr = `${lit}(?:\\s*\\+\\s*(?:[\\w.()\\[\\]]+)\\s*(?:\\+\\s*${lit})?)*`
  const sites = [
    new RegExp(`\\bp\\.add\\([^,]+,\\s*(${expr})\\)`, 'g'),
    new RegExp(`\\bwriteError\\(w,\\s*[^,]+,\\s*(${expr})\\)`, 'g'),
    new RegExp(`\\bMessage:\\s*(${expr})`, 'g'),
    new RegExp(`\\berrors\\.New\\((${expr})\\)`, 'g'),
  ]
  for (const file of goFiles) {
    const text = fs.readFileSync(file, 'utf8')
    for (const re of sites) {
      for (const m of text.matchAll(re)) {
        // The joined expression as a pattern: literals kept, everything else a placeholder in order.
        let n = 0
        let out = ''
        for (const [part] of m[1].matchAll(/"(?:[^"\\]|\\.)*"|[^\s+"][^+"]*/g)) {
          if (part.startsWith('"')) { try { out += JSON.parse(part) } catch { out += part.slice(1, -1) } } else out += `{${n++}}`
        }
        out = out.replace(/^(\w+: )?/, (x) => (/^(store|config|pbx|api): $/.test(x) ? '' : x))
        add(out, file)
      }
    }
  }
  const list = [...found.keys()].sort()
  fs.mkdirSync(path.dirname(catalogPath), { recursive: true })
  fs.writeFileSync(catalogPath, JSON.stringify(list, null, 1) + '\n')
  return list
}

const placeholders = (s) => (s.match(/\{\d+\}/g) ?? []).sort().join(',')
// keeps: a translation is kept only with the same placeholders, on one line, and of a sensible length.
// Pieces that must come through a translation as they are: addresses, paths, file names, codes, options in braces.
const literal = (s) => (s.replace(/\{\d+\}/g, ' ').match(/\S*[/@:\\_]\S*|\S+\.\S+|\{[a-z0-9_]+\}/gi) ?? [])
  .map((x) => x.replace(/^[(\[“"']+|[)\]”"'.,;:!?]+$/g, '')).filter((x) => x.length > 1 && /[/@\\_.{]/.test(x) && !/^\p{L}+\.$/u.test(x))
export function keeps(en, t) {
  if (typeof t !== 'string' || t.trim() === '' || placeholders(en) !== placeholders(t) || t.includes('\n') || t.length > en.length * 4 + 40) return false
  return literal(en).every((x) => t.includes(x))
}

const rules = `You translate the screens of BitVoice, a phone system (PBX), from English into LANG.
Each value is a piece of text on a screen: a button, a label, a heading, a hint, a message, or a part of a sentence that
the screen joins with numbers or names. Keep exactly as they are: placeholders such as {0} and {1} (each must appear in the
translation, in the place the grammar of LANG needs), leading and trailing spaces and punctuation, and these names:
BitVoice, BioDream, SIP, RTP, SRTP, TLS, WebRTC, ICE, STUN, TURN, DTLS, IAX2, AGI, FastAGI, EAGI, ARI, AMI, Asterisk, FreePBX,
3CX, LDAP, Active Directory, OpenID Connect, MCP, NG911, PSAP, ESInet, ESRP, EIDO, ALI, TTY, RTT, P25, PRI, BRI, ISDN, SS7,
T.38, G.711, G.722, Opus, G.729, AMR, whisper, OpenRouter, PostgreSQL, SQLite, AWS, Azure, Microsoft 365, Google Workspace,
Entra ID, Teams, JavaScript, Lua, JSON, CSV, API, URL, PIN, DTMF and other product, protocol, codec, file and key names, and
anything in code style. Use the words a phone system in LANG uses (extension, trunk, ring group, queue, voicemail, call
forwarding, do not disturb), the formal form of address where LANG has one, the imperative for buttons, and the same term for
the same thing throughout. Keep it as short as the English. Answer with a JSON object only: each key is the number given,
each value the translation.`

async function batch(lang, items, key) {
  const body = {
    model: process.env.I18N_MODEL || 'meta-llama/llama-3.3-70b-instruct', temperature: 0, provider: { sort: 'throughput' },
    // A ceiling on each answer: the service holds back credit for the longest answer a request could get.
    max_tokens: 6000,
    response_format: { type: 'json_object' },
    messages: [{ role: 'system', content: rules.replaceAll('LANG', `${LANGUAGES[lang]} (${lang})`) },
      { role: 'user', content: JSON.stringify(Object.fromEntries(items.map((u, i) => [String(i), u]))) }],
  }
  for (let attempt = 0; attempt < 4; attempt++) {
    try {
      const ctl = new AbortController()
      const timer = setTimeout(() => ctl.abort(), 120_000)
      const res = await fetch('https://openrouter.ai/api/v1/chat/completions', { method: 'POST', signal: ctl.signal,
        headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json', 'X-Title': 'BitVoice screens' }, body: JSON.stringify(body) })
      clearTimeout(timer)
      if (!res.ok) throw new Error(`${res.status} ${(await res.text()).slice(0, 200)}`)
      const j = await res.json()
      const text = j.choices?.[0]?.message?.content ?? ''
      const out = JSON.parse(text.slice(text.indexOf('{'), text.lastIndexOf('}') + 1))
      return items.map((u, i) => (keeps(u, out[String(i)]) ? out[String(i)] : null))
    } catch (e) {
      if (attempt === 3) { console.error(`${lang}: a batch failed: ${e.message}`); return items.map(() => null) }
      await new Promise((r) => setTimeout(r, 2000 * (attempt + 1)))
    }
  }
}

async function translate(langs) {
  const key = process.env.OPENROUTER_API_KEY
  if (!key) throw new Error('set OPENROUTER_API_KEY')
  const list = JSON.parse(fs.readFileSync(catalogPath, 'utf8'))
  fs.mkdirSync(outDir, { recursive: true })
  // One language at a time, a few requests at once: gentle on the service and on the account's credit.
  for (const lang of langs) await (async () => {
    if (!LANGUAGES[lang]) throw new Error(`no language ${lang}`)
    const file = path.join(outDir, `${lang}.json`)
    const have = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : {}
    for (const k of Object.keys(have)) if (!keeps(k, have[k])) delete have[k] // a rule tightened since: asked again
    const todo = list.filter((s) => !have[s])
    const size = 60
    const chunks = []
    for (let i = 0; i < todo.length; i += size) chunks.push(todo.slice(i, i + size))
    let done = 0
    const workers = Array.from({ length: Number(process.env.I18N_WORKERS || 3) }, async () => {
      while (chunks.length) {
        const c = chunks.shift()
        const t = await batch(lang, c, key)
        c.forEach((u, i) => { if (t[i]) have[u] = t[i] })
        done += c.length
        if (done % 600 < size) console.error(`${lang}: ${done}/${todo.length}`)
      }
    })
    await Promise.all(workers)
    // Only what the screens still show is kept, in the catalog's order.
    const out = Object.fromEntries(list.filter((s) => have[s]).map((s) => [s, have[s]]))
    fs.writeFileSync(file, JSON.stringify(out, null, 0) + '\n')
    console.log(`${lang}: ${Object.keys(out).length} of ${list.length}`)
  })()
}

function check() {
  const list = JSON.parse(fs.readFileSync(catalogPath, 'utf8'))
  for (const lang of Object.keys(LANGUAGES)) {
    const file = path.join(outDir, `${lang}.json`)
    const have = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : {}
    const n = list.filter((s) => have[s] && keeps(s, have[s])).length
    console.log(`${lang}: ${n} of ${list.length} (${Math.round((100 * n) / list.length)}%)`)
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [cmd, ...rest] = process.argv.slice(2)
  if (cmd === 'extract') console.log(`${extract().length} pieces of text`)
  else if (cmd === 'translate') await translate(rest.length ? rest : Object.keys(LANGUAGES))
  else if (cmd === 'check') check()
  else { console.error('extract | translate [lang ...] | check'); process.exit(2) }
}
