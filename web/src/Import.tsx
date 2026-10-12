import { AlertTriangle, ArrowDownLeft, ArrowUpRight, CheckCircle2, FileUp, Import as ImportIcon, Server, Users } from 'lucide-react'
import { useRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import { api, errorOf, type Extension, type InboundRoute, type OutboundRoute, type RingGroup, type Trunk } from './api'
import { store } from './store'
import { Chip, Page } from './ui'

interface Result {
  format: string; extensions: Extension[]; trunks: Trunk[]; ringGroups: RingGroup[]
  inboundRoutes: InboundRoute[]; outboundRoutes: OutboundRoute[]; warnings: string[]
}
interface Preview { result: Result; exists: Record<string, boolean> }

async function post<T>(url: string, body: BodyInit, type?: string): Promise<T> {
  const headers: Record<string, string> = { 'X-BitVoice': '1' }
  if (type) headers['Content-Type'] = type
  const r = await fetch(url, { method: 'POST', body, headers })
  const data = await r.json().catch(() => ({}))
  if (!r.ok) throw { data }
  return data as T
}

// Each item's key in the selection, and its key in "exists".
const keys = {
  x: (x: Extension) => ['x:' + x.number, x.number],
  t: (t: Trunk) => ['t:' + t.name, 'trunk:' + t.name],
  g: (g: RingGroup) => ['g:' + g.number, 'group:' + g.number],
  i: (r: InboundRoute) => ['i:' + r.did, 'in:' + r.did],
  o: (r: OutboundRoute) => ['o:' + r.patterns[0], 'out:' + r.patterns[0]],
}

export function ImportPage() {
  const [preview, setPreview] = useState<Preview | null>(null)
  const [pick, setPick] = useState<Set<string>>(new Set())
  const [over, setOver] = useState(false)
  const [busy, setBusy] = useState(false)
  const [notes, setNotes] = useState<string[]>([])
  const file = useRef<HTMLInputElement>(null)

  const read = async (files: File[]) => {
    if (!files.length) return
    setBusy(true)
    setNotes([])
    try {
      const form = new FormData()
      files.forEach((f) => form.append('file', f, f.name))
      const p = await post<Preview>('./api/import/preview', form)
      setPreview(p)
      const all = new Set<string>()
      const add = ([k, e]: string[]) => { if (!p.exists[e]) all.add(k) }
      p.result.extensions.forEach((x) => add(keys.x(x)))
      p.result.trunks.forEach((t) => add(keys.t(t)))
      p.result.ringGroups.forEach((g) => add(keys.g(g)))
      p.result.inboundRoutes.forEach((r) => add(keys.i(r)))
      p.result.outboundRoutes.forEach((r) => add(keys.o(r)))
      setPick(all)
    } catch (e) {
      toast.error(errorOf(e).message)
    }
    setBusy(false)
  }
  const apply = async () => {
    if (!preview) return
    setBusy(true)
    try {
      const r0 = preview.result
      const body = {
        extensions: r0.extensions.filter((x) => pick.has(keys.x(x)[0])),
        trunks: r0.trunks.filter((t) => pick.has(keys.t(t)[0])),
        ringGroups: r0.ringGroups.filter((g) => pick.has(keys.g(g)[0])),
        inboundRoutes: r0.inboundRoutes.filter((r) => pick.has(keys.i(r)[0])),
        outboundRoutes: r0.outboundRoutes.filter((r) => pick.has(keys.o(r)[0])),
      }
      const r = await post<{ added: number; skipped: number; notes: string[] }>('./api/import/apply', JSON.stringify(body), 'application/json')
      toast.success(`Imported ${r.added}${r.skipped ? `, skipped ${r.skipped}` : ''}`)
      store.dispatch(api.util.invalidateTags(['Extensions', 'Trunks', 'RingGroups', 'Inbound', 'Outbound', 'History', 'Health']))
      setNotes(r.notes)
      setPreview(null)
    } catch (e) {
      toast.error(errorOf(e).message)
    }
    setBusy(false)
  }
  const toggle = (k: string) => setPick((s) => { const n = new Set(s); if (n.has(k)) n.delete(k); else n.add(k); return n })
  const row = (k: string, taken: boolean, label: string, children: ReactNode, takenText = 'already here') => (
    <label key={k} className={`flex flex-wrap items-center gap-3 px-4 py-2 text-sm ${taken ? 'opacity-60' : ''}`}>
      <input type="checkbox" disabled={taken} checked={pick.has(k)} onChange={() => toggle(k)} aria-label={label} />
      {children}
      {taken && <span className="ml-auto text-xs text-rose-300">{takenText}</span>}
    </label>
  )
  const res = preview?.result
  const ext = (n: string) => res?.extensions.find((x) => x.number === n)?.name

  return (
    <Page icon={ImportIcon} title="Import" accent="from-sky-400 to-emerald-400"
      intro="Bring extensions, trunks, hunt and ring groups and call routes over from FreePBX, Asterisk, 3CX, Cisco, Avaya or Mitel. Nothing changes until you choose what to add.">
      <button type="button" onClick={() => file.current?.click()}
        onDragOver={(e) => { e.preventDefault(); setOver(true) }} onDragLeave={() => setOver(false)}
        onDrop={(e) => { e.preventDefault(); setOver(false); read(Array.from(e.dataTransfer.files)) }}
        className={`card flex w-full flex-col items-center gap-2 border-2 border-dashed p-10 text-center ${over ? 'border-sky-400 bg-sky-950/30' : 'border-slate-700'}`}>
        <FileUp size={32} className="text-sky-300" aria-hidden />
        <span className="font-semibold text-slate-100">{busy ? 'Reading…' : 'Drop files here, or choose them'}</span>
        <span className="text-sm text-slate-400">
          Several files go together: the people file with the groups file, so members are found. A .zip or .tar of CSV files works too.
        </span>
        <span className="text-sm text-slate-400">
          FreePBX: Admin → Bulk Handler → Export → Extensions (CSV). Asterisk: <span className="font-mono text-amber-300">pjsip.conf</span> or <span className="font-mono text-amber-300">sip.conf</span>. 3CX: Users → Export (CSV).
          Cisco Unified CM: Bulk Administration → Users (or Phones) → Export, and Import/Export → Export with Line Group, Hunt List, Hunt Pilot, SIP Trunk and Route Pattern ticked (the .tar as it comes).
          Avaya IP Office: Manager → File → Import/Export → Export, Users and Hunt Group. Avaya Communication Manager: list station and list hunt-group, saved as CSV.
          Mitel MiVoice Business: User and Device Configuration, Ring Groups and Ring Group Assignment, each exported.
        </span>
      </button>
      <input ref={file} type="file" multiple className="hidden" aria-label="Files to import" accept=".csv,.conf,.txt,.tar,.tgz,.gz,.zip,text/plain"
        onChange={(e) => { read(Array.from(e.target.files ?? [])); e.target.value = '' }} />

      {notes.length > 0 && (
        <ul className="card mt-6 space-y-1 border-amber-900/60 p-4 text-sm" aria-label="What was not added">
          {notes.map((w, i) => <li key={i} className="flex gap-2 text-amber-300"><AlertTriangle size={14} className="mt-0.5 shrink-0" aria-hidden />{w}</li>)}
        </ul>
      )}

      {preview && res && (
        <div className="mt-6 space-y-4">
          <p className="text-sm text-slate-400">Read as <span className="text-emerald-300">{res.format}</span>.</p>
          {res.warnings.length > 0 && (
            <ul className="card space-y-1 border-amber-900/60 p-4 text-sm">
              {res.warnings.map((w, i) => <li key={i} className="flex gap-2 text-amber-300"><AlertTriangle size={14} className="mt-0.5 shrink-0" aria-hidden />{w}</li>)}
            </ul>
          )}
          {res.extensions.length > 0 && (
            <section className="card divide-y divide-slate-800/70">
              <h2 className="px-4 py-3 font-semibold text-slate-100">Extensions</h2>
              {res.extensions.map((x) => {
                const [k, e] = keys.x(x)
                return row(k, preview.exists[e], `Import extension ${x.number}`, <>
                  <Chip kind="extension">{x.number}</Chip>
                  <span className="text-slate-200">{x.name}</span>
                  {x.voicemail.enabled && <span className="text-xs text-violet-300">voicemail</span>}
                  {!x.secret && <span className="text-xs text-amber-300">new SIP password</span>}
                </>, 'number already used here')
              })}
            </section>
          )}
          {res.ringGroups.length > 0 && (
            <section className="card divide-y divide-slate-800/70">
              <h2 className="flex items-center gap-2 px-4 py-3 font-semibold text-slate-100"><Users size={16} className="text-violet-300" aria-hidden /> Hunt and ring groups</h2>
              {res.ringGroups.map((g) => {
                const [k, e] = keys.g(g)
                return row(k, preview.exists[e], `Import group ${g.number}`, <>
                  <Chip kind="ringgroup">{g.number}</Chip>
                  <span className="text-slate-200">{g.name}</span>
                  <span className="text-xs text-sky-300">{g.strategy === 'all' ? 'everyone at once' : 'one after another'}, {g.ringSeconds} s each</span>
                  <span className="text-xs text-slate-400">{g.members.map((m) => ext(m) ? `${m} ${ext(m)}` : m).join(', ')}</span>
                </>, 'number already used here')
              })}
            </section>
          )}
          {res.trunks.length > 0 && (
            <section className="card divide-y divide-slate-800/70">
              <h2 className="px-4 py-3 font-semibold text-slate-100">Trunks</h2>
              {res.trunks.map((t) => {
                const [k, e] = keys.t(t)
                return row(k, preview.exists[e], `Import trunk ${t.name}`, <>
                  <Server size={14} className="text-teal-300" aria-hidden />
                  <Chip kind="trunk">{t.name}</Chip>
                  <span className="font-mono text-xs text-amber-300">{t.host}{t.port ? `:${t.port}` : ''}</span>
                  {t.register && <span className="text-xs text-emerald-300">registers</span>}
                  {!t.enabled && <span className="text-xs text-slate-500">comes in switched off</span>}
                </>)
              })}
            </section>
          )}
          {(res.inboundRoutes.length > 0 || res.outboundRoutes.length > 0) && (
            <section className="card divide-y divide-slate-800/70">
              <h2 className="px-4 py-3 font-semibold text-slate-100">Call routes</h2>
              {res.inboundRoutes.map((r) => {
                const [k, e] = keys.i(r)
                return row(k, preview.exists[e], `Import incoming route ${r.did}`, <>
                  <ArrowDownLeft size={14} className="text-sky-300" aria-hidden />
                  <span className="font-mono text-amber-300">{r.did}</span>
                  <span className="text-slate-400">to</span>
                  <span className="font-mono text-emerald-300">{r.destination.target}</span>
                </>)
              })}
              {res.outboundRoutes.map((r) => {
                const [k, e] = keys.o(r)
                return row(k, preview.exists[e], `Import outbound route ${r.patterns[0]}`, <>
                  <ArrowUpRight size={14} className="text-emerald-300" aria-hidden />
                  <span className="font-mono text-amber-300">{r.patterns[0]}</span>
                  {r.strip > 0 && <span className="text-xs text-slate-400">drops the first {r.strip}</span>}
                  {r.prepend && <span className="text-xs text-slate-400">adds {r.prepend}</span>}
                  <span className="text-slate-400">out on</span><Chip kind="trunk">{r.trunks[0]}</Chip>
                </>, 'pattern already routed here')
              })}
            </section>
          )}
          <button className="btn btn-primary" disabled={busy || pick.size === 0} onClick={apply}>
            <CheckCircle2 size={15} aria-hidden /> Add {pick.size} {pick.size === 1 ? 'item' : 'items'}
          </button>
        </div>
      )}
    </Page>
  )
}
