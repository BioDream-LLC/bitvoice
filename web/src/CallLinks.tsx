import qrcode from 'qrcode-generator'
import { Code, Copy, Link2, Plus, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useSaveSettingsMutation, useSettingsQuery, type ApiErrorBody, type CallLink } from './api'
import { useIsAdmin } from './Editor'
import { DestinationPicker } from './Extensions'
import { useDraft } from './useDraft'
import { Empty, Field, Page, StaleBanner, Toggle, problemFor } from './ui'

function qrSvg(text: string) {
  const q = qrcode(0, 'M')
  q.addData(text)
  q.make()
  return q.createSvgTag({ cellSize: 4, margin: 2, scalable: true })
}

const copy = (t: string, what: string) => { navigator.clipboard?.writeText(t); toast.success(`${what} copied`) }

/** CallLinks: addresses, QR codes and website buttons that call a destination from a browser. */
export function CallLinks() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const { draft, setDraft, reset, stale } = useDraft<CallLink[]>(data ? (data.system.callLinks ?? []) : undefined)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!draft || !data) return null
  const p = (f: string) => problemFor(err?.problems, 'system.callLinks' + f)
  const set = (i: number, v: Partial<CallLink>) => setDraft((d) => d.map((x, j) => (j === i ? { ...x, ...v } : x)))
  const doSave = async () => {
    try { const s = await save({ ...data, system: { ...data.system, callLinks: draft } }).unwrap(); reset(s.system.callLinks ?? []); setErr(null); toast.success('Call links saved') }
    catch (e) { setErr((e as { data?: ApiErrorBody }).data ?? { message: 'The call links could not be saved.' }) }
  }
  const base = location.origin
  return (
    <Page icon={Link2} title="Call links" accent="from-emerald-300 to-sky-300"
      intro="A web address that rings your business from any browser, with no phone number or app: put it in emails, print its QR code, or add a Call us button to your website."
      actions={admin && <div className="flex gap-2">
        <button className="btn" onClick={() => setDraft((d) => [...d, { id: '', name: '', slug: '', enabled: true, title: '', intro: '', askName: true, destination: { type: '', target: '' } as unknown as CallLink['destination'], maxCalls: 0 }])}><Plus size={15} aria-hidden /> Add link</button>
        <button className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={15} aria-hidden /> Save</button>
      </div>}>
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      {err && !err.problems?.length && <p className="mb-3 text-sm text-rose-300">{err.message}</p>}
      {draft.length === 0 ? <Empty icon={Link2} title="No call links yet">Add one, choose where its calls go, and save to get its address and QR code.</Empty> : (
        <ul className="space-y-4" aria-label="Call links">
          {draft.map((l, i) => {
            const url = l.slug && l.id ? `${base}/call/${l.slug}` : ''
            const button = url ? `<a href="${url}" target="_blank" rel="noopener" style="display:inline-block;padding:12px 20px;border-radius:8px;background:#059669;color:#fff;font:600 16px system-ui;text-decoration:none">📞 ${l.title || 'Call us'}</a>` : ''
            return (
              <li key={l.id || `new-${i}`} className="card grid gap-4 p-5 lg:grid-cols-[1fr_12rem]">
                <div className="space-y-3">
                  <div className="grid gap-3 md:grid-cols-2">
                    <Field label="Name" htmlFor={`cln${i}`} error={p(`[${i}].name`)} hint="For you: Support line."><input id={`cln${i}`} className="input" disabled={!admin} value={l.name} onChange={(e) => set(i, { name: e.target.value })} /></Field>
                    <DestinationPicker label="Calls go to" required value={l.destination?.type ? l.destination : null} onChange={(d) => d && set(i, { destination: d })} error={p(`[${i}].destination`)} />
                    <Field label="Title the caller sees" htmlFor={`clt${i}`} error={p(`[${i}].title`)}><input id={`clt${i}`} className="input" disabled={!admin} placeholder={`Call ${data.system.name}`} value={l.title} onChange={(e) => set(i, { title: e.target.value })} /></Field>
                    <Field label="A few words under it" htmlFor={`cli${i}`}><input id={`cli${i}`} className="input" disabled={!admin} placeholder="We answer 9 to 5, Monday to Friday." value={l.intro} onChange={(e) => set(i, { intro: e.target.value })} /></Field>
                    <Field label="Calls at once" htmlFor={`clm${i}`} error={p(`[${i}].maxCalls`)} hint="5 when empty."><input id={`clm${i}`} type="number" min={0} max={100} className="input w-28" disabled={!admin} value={l.maxCalls || ''} onChange={(e) => set(i, { maxCalls: Number(e.target.value) || 0 })} /></Field>
                  </div>
                  <div className="flex flex-wrap gap-6 text-sm text-slate-300">
                    <label className="flex items-center gap-2"><Toggle checked={l.enabled} onChange={(v) => set(i, { enabled: v })} label="Open" /> Open (off: the page says the line is closed)</label>
                    <label className="flex items-center gap-2"><Toggle checked={l.askName} onChange={(v) => set(i, { askName: v })} label="Ask for their name" /> Ask for their name (shown to whoever answers)</label>
                  </div>
                  {url ? (
                    <div className="space-y-2 text-sm">
                      <p><span className="text-slate-400">Address: </span><a href={url} target="_blank" rel="noopener" className="break-all font-mono text-emerald-300 underline">{url}</a>
                        <button className="btn-ghost ml-1 p-1" aria-label="Copy the address" onClick={() => copy(url, 'Address')}><Copy size={13} aria-hidden /></button></p>
                      <button className="btn" onClick={() => copy(button, 'Website button')}><Code size={14} className="text-sky-300" aria-hidden /> Copy a website button</button>
                    </div>
                  ) : <p className="text-sm text-slate-500">Save to get the address and QR code.</p>}
                  {admin && <button className="btn" onClick={() => setDraft((d) => d.filter((_, j) => j !== i))}><Trash2 size={14} className="text-rose-300" aria-hidden /> Remove</button>}
                </div>
                {url && <figure className="space-y-1 text-center">
                  <div className="rounded-lg bg-white p-2" role="img" aria-label={`QR code for ${url}`} dangerouslySetInnerHTML={{ __html: qrSvg(url) }} />
                  <figcaption className="text-xs text-slate-500">Scan to call</figcaption>
                </figure>}
              </li>
            )
          })}
        </ul>
      )}
    </Page>
  )
}
