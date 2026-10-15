import { CircleDot, Copy, Cpu, KeyRound, Network, Phone, PhoneIncoming, Plus, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useCardsStatusQuery, useExtensionsQuery, useSaveSettingsMutation, useSettingsQuery, useSystemQuery, useTrunksQuery, type ApiErrorBody, type Cards, type CardSpan } from './api'
import { attempt, useIsAdmin } from './Editor'
import { Field, Page, problemFor, Toggle } from './ui'

const defaults: Cards = { enabled: false, token: '', linePort: 5091, alaw: false, channels: [], spans: [] }

function newToken() {
  const b = new Uint8Array(24)
  crypto.getRandomValues(b)
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

function StateText({ s }: { s?: string }) {
  if (!s) return <span className="text-xs text-slate-500">no report</span>
  const ok = s === 'idle' || s === 'in a call' || s === 'ringing' || s === 'dial tone' || s === 'dialling' || s === 'calling' || s.startsWith('D-channel up')
  return <span className={ok ? 'text-xs text-emerald-300' : 'text-xs text-amber-300'}><CircleDot size={11} className="mr-1 inline" aria-hidden />{s}</span>
}

/** Telephony cards: this computer's analogue ports (DAHDI), joined to BitVoice by bitvoice-dahdi. */
export function TelephonyCards() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const { data: exts } = useExtensionsQuery()
  const { data: trunks } = useTrunksQuery()
  const { data: sys } = useSystemQuery()
  const { data: status } = useCardsStatusQuery(undefined, { pollingInterval: 5000 })
  const [save, { isLoading }] = useSaveSettingsMutation()
  const [edit, setEdit] = useState<Cards | null>(null)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!data) return null
  const k: Cards = edit ?? { ...defaults, ...data.system.cards, channels: data.system.cards?.channels ?? [], spans: data.system.cards?.spans ?? [] }
  const spans = k.spans ?? []
  const setSpan = (i: number, v: Partial<CardSpan>) => set({ spans: spans.map((s, j) => (j === i ? { ...s, ...v } : s)) })
  const set = (v: Partial<Cards>) => setEdit({ ...k, ...v })
  const setCh = (i: number, v: Partial<Cards['channels'][number]>) => set({ channels: k.channels.map((c, j) => (j === i ? { ...c, ...v } : c)) })
  const p = (f: string) => problemFor(err?.problems, 'system.cards.' + f)
  const hasTrunk = trunks?.items.some((t) => t.protocol === 'dahdi')
  const lines = k.channels.filter((c) => c.kind === 'line').length + spans.length
  const cmd = `bitvoice-dahdi -server https://127.0.0.1:${sys?.httpsPort || 8443} -token-file /etc/bitvoice/cards.token`
  const submit = async () => {
    const e = await attempt(() => save({ ...data, system: { ...data.system, cards: k } }).unwrap(), 'Telephony cards saved')
    setErr(e)
    if (!e) setEdit(null)
  }
  return (
    <Page icon={Cpu} title="Telephony cards" accent="from-amber-400 to-rose-400"
      intro="Analogue phones and phone company lines plugged into a telephony card in this computer (Digium, Sangoma, OpenVox and other cards with Linux DAHDI drivers). A separate program, bitvoice-dahdi, drives the card and joins its ports to BitVoice; BitVoice itself never touches the card.">
      <fieldset disabled={!admin} className="space-y-6">
        <section className="card space-y-4 p-5" aria-labelledby="cards-h">
          <h2 id="cards-h" className="flex items-center gap-2 font-semibold text-slate-100"><Cpu size={17} className="text-amber-300" aria-hidden /> The card</h2>
          <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={k.enabled} onChange={(v) => set({ enabled: v, token: v && !k.token ? newToken() : k.token })} label="Telephony cards" />
            Use telephony cards on this computer <span className="text-xs text-slate-500">(Linux, with the DAHDI drivers installed and the ports set up in /etc/dahdi/system.conf)</span></label>
          {k.enabled && (
            <>
              <div className={status?.running ? 'text-sm text-emerald-300' : 'text-sm text-amber-300'}>
                {status?.running ? `bitvoice-dahdi is running${status.version ? ` (${status.version})` : ''}.` : 'bitvoice-dahdi is not running, or has not reported in the last 45 seconds.'}
              </div>
              <Field label="Token" htmlFor="ck" error={p('token')} hint="bitvoice-dahdi signs in with it and learns the extensions' passwords with it: keep it in a file only root can read.">
                <div className="flex gap-2">
                  <input id="ck" className="input font-mono" value={k.token} onChange={(e) => set({ token: e.target.value.trim() })} />
                  <button type="button" className="btn" onClick={() => set({ token: newToken() })}><KeyRound size={14} aria-hidden /> New token</button>
                  <button type="button" className="btn" aria-label="Copy the token" onClick={() => { void navigator.clipboard?.writeText(k.token); toast.success('Token copied') }}><Copy size={14} aria-hidden /></button>
                </div>
              </Field>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Card trunk port" htmlFor="clp" error={p('linePort')} hint="On this computer only; the card trunk reaches bitvoice-dahdi here.">
                  <input id="clp" type="number" className="input w-32" value={k.linePort || ''} placeholder="5091" onChange={(e) => set({ linePort: Number(e.target.value) })} />
                </Field>
                <div>
                  <span className="label">Sound encoding</span>
                  <label className="flex items-center gap-3 pt-2 text-sm text-slate-300"><Toggle checked={k.alaw} onChange={(v) => set({ alaw: v })} label="A-law" />
                    A-law <span className="text-xs text-slate-500">({k.alaw ? 'Europe and most of the world' : 'off: µ-law, North America and Japan'})</span></label>
                </div>
              </div>
              <div className="rounded-lg border border-slate-800 bg-slate-950/50 p-3 text-xs text-slate-400">
                <p className="mb-1 font-semibold text-slate-300">Start bitvoice-dahdi on this computer (it comes in the Linux download):</p>
                <code className="block break-all font-mono text-sky-300">{cmd}</code>
                <p className="mt-1">Put the token in that file first. Run it as a service user that may open /dev/dahdi; it reads its ports from here and picks up changes within ten seconds.</p>
              </div>
            </>
          )}
        </section>

        {k.enabled && (
          <section className="card space-y-4 p-5" aria-labelledby="cports-h">
            <h2 id="cports-h" className="flex items-center gap-2 font-semibold text-slate-100"><Phone size={17} className="text-sky-300" aria-hidden /> Ports</h2>
            <p className="text-sm text-slate-400">Each port by its DAHDI channel number. A <span className="text-sky-300">phone</span> port (FXS) has an analogue phone, fax or door phone plugged in and is a device of an extension. A <span className="text-amber-300">line</span> port (FXO) has a phone company line plugged in and is a line of the card trunk.</p>
            {k.channels.map((c, i) => (
              <div key={i} className="grid items-center gap-2 sm:grid-cols-[6rem_9rem_1fr_10rem_auto]">
                <input type="number" className="input" aria-label={`Port ${i + 1} channel`} value={c.channel || ''} min={1} max={1024} onChange={(e) => setCh(i, { channel: Number(e.target.value) })} />
                <select className="input" aria-label={`Port ${i + 1} kind`} value={c.kind} onChange={(e) => setCh(i, { kind: e.target.value as 'phone' | 'line' })}>
                  <option value="phone">Phone (FXS)</option><option value="line">Line (FXO)</option>
                </select>
                {c.kind === 'phone' ? (
                  <select className="input" aria-label={`Port ${i + 1} extension`} value={c.extension} onChange={(e) => setCh(i, { extension: e.target.value })}>
                    <option value="">Extension…</option>
                    {exts?.items.map((x) => <option key={x.id} value={x.number}>{x.number} {x.name}</option>)}
                  </select>
                ) : (
                  <input className="input font-mono" aria-label={`Port ${i + 1} line number`} value={c.number} placeholder="Its phone number, for inbound routes" onChange={(e) => setCh(i, { number: e.target.value.trim() })} />
                )}
                <StateText s={status?.channels[String(c.channel)]} />
                <button type="button" className="btn" aria-label={`Remove port ${i + 1}`} onClick={() => set({ channels: k.channels.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
                {(p(`channels[${i}].channel`) || p(`channels[${i}].kind`) || p(`channels[${i}].extension`) || p(`channels[${i}].number`)) && (
                  <p className="text-sm text-rose-300 sm:col-span-5">{p(`channels[${i}].channel`) ?? p(`channels[${i}].kind`) ?? p(`channels[${i}].extension`) ?? p(`channels[${i}].number`)}</p>
                )}
              </div>
            ))}
            <button type="button" className="btn" onClick={() => set({ channels: [...k.channels, { channel: (k.channels.at(-1)?.channel ?? 0) + 1, kind: 'phone', extension: '', number: '' }] })}><Plus size={14} aria-hidden /> Add a port</button>
            {lines > 0 && !hasTrunk && (
              <p className="flex items-center gap-2 text-sm text-amber-300"><PhoneIncoming size={14} aria-hidden /> Lines need the card trunk: add a trunk on Trunks with the protocol Telephony card lines, then route numbers to it.</p>
            )}
            <p className="text-xs text-slate-500">Phones: lift the handset for dial tone and dial; the number goes after a pause or #. Incoming calls ring the phone (no caller ID display yet). Lines: a ringing line calls its number here, for the inbound routes; calls out are dialled as tones and count as answered once the number is dialled, as analogue lines do not say when the far end answers.</p>
          </section>
        )}
        {k.enabled && (
          <section className="card space-y-4 p-5" aria-labelledby="cspan-h">
            <h2 id="cspan-h" className="flex items-center gap-2 font-semibold text-slate-100"><Network size={17} className="text-violet-300" aria-hidden /> Digital lines (T1 and E1 PRI)</h2>
            <p className="text-sm text-slate-400">A T1 (23 calls) or E1 (30 calls) line from the phone company, run as primary rate ISDN: its calls come in with the number dialled (for the inbound routes) and the caller ID, and go out with yours. Set the span up as PRI in /etc/dahdi/system.conf, then give its first DAHDI channel here.</p>
            {spans.map((s, i) => {
              const last = s.first + (s.type === 'e1' ? 30 : 23)
              const d = s.first + (s.type === 'e1' ? 15 : 23)
              const sp = (f: string) => p(`spans[${i}].${f}`)
              return (
                <div key={i} className="space-y-1 rounded-lg border border-slate-800 p-3">
                  <div className="grid items-end gap-2 sm:grid-cols-[7rem_7rem_11rem_10rem_auto]">
                    <Field label="Line" htmlFor={`sp-t-${i}`}>
                      <select id={`sp-t-${i}`} className="input" value={s.type} onChange={(e) => setSpan(i, { type: e.target.value as CardSpan['type'], switch: e.target.value === 'e1' ? 'euroisdn' : 'ni2' })}>
                        <option value="t1">T1</option><option value="e1">E1</option>
                      </select>
                    </Field>
                    <Field label="First channel" htmlFor={`sp-f-${i}`} error={sp('first')}>
                      <input id={`sp-f-${i}`} type="number" min={1} className="input" value={s.first || ''} onChange={(e) => setSpan(i, { first: Number(e.target.value) })} />
                    </Field>
                    <Field label="BitVoice is" htmlFor={`sp-r-${i}`} error={sp('role')}>
                      <select id={`sp-r-${i}`} className="input" value={s.role} onChange={(e) => setSpan(i, { role: e.target.value as CardSpan['role'] })}>
                        <option value="user">the PBX (user side)</option><option value="network">the network</option>
                      </select>
                    </Field>
                    <Field label="Switch type" htmlFor={`sp-s-${i}`} error={sp('switch')}>
                      <select id={`sp-s-${i}`} className="input" value={s.switch} onChange={(e) => setSpan(i, { switch: e.target.value })}>
                        <option value="ni2">National ISDN-2</option><option value="5ess">AT&amp;T 5ESS</option><option value="dms100">Nortel DMS-100</option><option value="euroisdn">EuroISDN</option><option value="qsig">QSIG</option>
                      </select>
                    </Field>
                    <button type="button" className="btn" aria-label={`Remove span ${i + 1}`} onClick={() => set({ spans: spans.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
                  </div>
                  <p className="text-xs text-slate-500">Channels {s.first} to {last}, D-channel {d}. <StateText s={status?.channels[`span${i + 1}`]} /></p>
                </div>
              )
            })}
            <button type="button" className="btn" onClick={() => set({ spans: [...spans, { type: 't1', first: Math.max(1, ...k.channels.map((c) => c.channel + 1), ...spans.map((s) => s.first + (s.type === 'e1' ? 31 : 24))), role: 'user', switch: 'ni2' }] })}><Plus size={14} aria-hidden /> Add a T1 or E1</button>
          </section>
        )}
        <button type="button" className="btn btn-primary" disabled={isLoading || !edit} onClick={submit}><Save size={14} aria-hidden /> Save</button>
      </fieldset>
    </Page>
  )
}
