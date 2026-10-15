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
const r2Variants: [string, string][] = [['itu', 'ITU-T (Q.421, Q.441)'], ['ar', 'Argentina'], ['br', 'Brazil'], ['co', 'Colombia'], ['cz', 'Czech Republic'], ['ec', 'Ecuador'], ['mx', 'Mexico'], ['ph', 'Philippines'], ['ve', 'Venezuela']]

const emHelp: Record<string, string> = {
  wink: 'Wink start: the far end winks when it is ready for the number, and BitVoice winks back at a call coming in. Answered when the far end goes off hook.',
  immediate: 'Immediate start: the number follows the seizure at once. Some channel banks and older PBXs.',
  delay: 'Delay dial: the far end goes off hook while it gets ready, then on hook when the number may be sent.',
  fgd: 'Feature Group D: wink start with MF numbers, the caller\'s number (ANI) first: KP 00 ANI ST, then KP number ST. Long-distance carriers\' trunks.',
  cama: 'CAMA: a 911 trunk from the selective router. Wink start, then the caller\'s number (ANI) in MF: KP, information digits, the number, ST. Each call is a 911 call (shown on the 911 page, a lab-track screen), located by its number from the location server set there.',
}

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
            <h2 id="cspan-h" className="flex items-center gap-2 font-semibold text-slate-100"><Network size={17} className="text-violet-300" aria-hidden /> Digital lines (T1, E1 and BRI)</h2>
            <p className="text-sm text-slate-400">A T1 (23 calls) or E1 (30 calls) line from the phone company run as primary rate ISDN, or a basic rate ISDN line (BRI, 2 calls): its calls come in with the number dialled (for the inbound routes), the caller ID and name, and the number they were forwarded from, and go out with yours. Set the span up in /etc/dahdi/system.conf, then give its first DAHDI channel here.</p>
            {spans.map((s, i) => {
              const sig = s.signalling || 'isdn'
              const last = s.first + (s.type === 'e1' ? 30 : s.type === 'bri' ? 2 : 23)
              const d = s.first + (s.type === 'e1' ? 15 : s.type === 'bri' ? 2 : 23)
              const sp = (f: string) => p(`spans[${i}].${f}`)
              const chans = sig === 'ss7' ? `Voice circuits ${s.first} to ${last} (CIC ${s.firstCic || 1} up), signalling link on channel ${d}.` : sig === 'em' ? `Channels ${s.first} to ${last}, each an E&M trunk.` : sig === 'r2' ? `Channels ${s.first} to ${last} (timeslot 16 carries the signalling bits), each an MFC-R2 circuit.` : `Channels ${s.first} to ${last}, D-channel ${d}.`
              return (
                <div key={i} className="space-y-1 rounded-lg border border-slate-800 p-3">
                  <div className="grid items-end gap-2 sm:grid-cols-[7rem_7rem_11rem_10rem_auto]">
                    <Field label="Line" htmlFor={`sp-t-${i}`}>
                      <select id={`sp-t-${i}`} className="input" value={s.type} onChange={(e) => setSpan(i, { type: e.target.value as CardSpan['type'], switch: e.target.value === 't1' ? 'ni2' : 'euroisdn', multipoint: false, signalling: '' })}>
                        <option value="t1">T1</option><option value="e1">E1</option><option value="bri">BRI</option>
                      </select>
                    </Field>
                    <Field label="First channel" htmlFor={`sp-f-${i}`} error={sp('first')}>
                      <input id={`sp-f-${i}`} type="number" min={1} className="input" value={s.first || ''} onChange={(e) => setSpan(i, { first: Number(e.target.value) })} />
                    </Field>
                    {s.type !== 'bri' && (
                      <Field label="Signalling" htmlFor={`sp-g-${i}`} error={sp('signalling')}>
                        <select id={`sp-g-${i}`} className="input" value={sig} onChange={(e) => setSpan(i, { signalling: e.target.value === 'isdn' ? '' : e.target.value, networkTransfer: false, charges: false })}>
                          <option value="isdn">ISDN (PRI)</option>
                          {s.type === 't1' ? <option value="em">E&amp;M (robbed bit)</option> : <option value="r2">MFC-R2</option>}
                          <option value="ss7">SS7 (ISUP)</option>
                        </select>
                      </Field>
                    )}
                    {sig === 'em' && (
                      <Field label="Start signal" htmlFor={`sp-st-${i}`} error={sp('start')}>
                        <select id={`sp-st-${i}`} className="input" value={s.start || 'wink'} onChange={(e) => setSpan(i, { start: e.target.value })}>
                          <option value="wink">Wink start</option><option value="immediate">Immediate start</option><option value="delay">Delay dial</option><option value="fgd">Feature Group D</option><option value="cama">CAMA (911 trunk)</option>
                        </select>
                      </Field>
                    )}
                    {sig === 'em' && s.start === 'cama' && (
                      <Field label="Area codes for NPD 0 to 3" htmlFor={`sp-npd-${i}`} error={sp('npd')} hint="Traditional CAMA sends 7 digits and a numbering plan digit; list the area code each digit stands for, separated by spaces. Enhanced CAMA (10 digits) needs none.">
                        <input id={`sp-npd-${i}`} className="input font-mono" value={(s.npd ?? []).join(' ')} placeholder="717 570" onChange={(e) => setSpan(i, { npd: e.target.value.split(/[\s,]+/).filter(Boolean) })} />
                      </Field>
                    )}
                    {sig === 'em' && s.start !== 'fgd' && s.start !== 'cama' && (
                      <Field label="Numbers sent as" htmlFor={`sp-ad-${i}`} error={sp('address')}>
                        <select id={`sp-ad-${i}`} className="input" value={s.address || 'dtmf'} onChange={(e) => setSpan(i, { address: e.target.value })}>
                          <option value="dtmf">DTMF</option><option value="mf">MF (R1)</option>
                        </select>
                      </Field>
                    )}
                    {sig === 'r2' && (
                      <Field label="Country variant" htmlFor={`sp-v-${i}`} error={sp('variant')}>
                        <select id={`sp-v-${i}`} className="input" value={s.variant || 'itu'} onChange={(e) => setSpan(i, { variant: e.target.value })}>
                          {r2Variants.map(([v, n]) => <option key={v} value={v}>{n}</option>)}
                        </select>
                      </Field>
                    )}
                    {sig === 'ss7' && <>
                      <Field label="SS7 variant" htmlFor={`sp-sv-${i}`} error={sp('variant')}>
                        <select id={`sp-sv-${i}`} className="input" value={s.variant || (s.type === 't1' ? 'ansi' : 'itu')} onChange={(e) => setSpan(i, { variant: e.target.value })}>
                          <option value="ansi">ANSI (North America)</option><option value="itu">ITU-T</option>
                        </select>
                      </Field>
                      <Field label="Our point code" htmlFor={`sp-opc-${i}`} error={sp('opc')}>
                        <input id={`sp-opc-${i}`} className="input font-mono" placeholder={(s.variant || (s.type === 't1' ? 'ansi' : 'itu')) === 'ansi' ? '245-16-1' : '1-10-2'} value={s.opc ?? ''} onChange={(e) => setSpan(i, { opc: e.target.value })} />
                      </Field>
                      <Field label="Adjacent point code" htmlFor={`sp-apc-${i}`} error={sp('apc')}>
                        <input id={`sp-apc-${i}`} className="input font-mono" value={s.apc ?? ''} onChange={(e) => setSpan(i, { apc: e.target.value })} />
                      </Field>
                      <Field label="Network" htmlFor={`sp-ni-${i}`} error={sp('ni')}>
                        <select id={`sp-ni-${i}`} className="input" value={s.ni || 'national'} onChange={(e) => setSpan(i, { ni: e.target.value })}>
                          <option value="national">National</option><option value="international">International</option>
                        </select>
                      </Field>
                      <Field label="Link code (SLC)" htmlFor={`sp-slc-${i}`} error={sp('slc')}>
                        <input id={`sp-slc-${i}`} type="number" min={0} max={15} className="input" value={s.slc ?? 0} onChange={(e) => setSpan(i, { slc: Number(e.target.value) })} />
                      </Field>
                      <Field label="First CIC" htmlFor={`sp-cic-${i}`} error={sp('firstCic')}>
                        <input id={`sp-cic-${i}`} type="number" min={1} className="input" value={s.firstCic || 1} onChange={(e) => setSpan(i, { firstCic: Number(e.target.value) })} />
                      </Field>
                    </>}
                    {(sig === 'em' || sig === 'r2') && (
                      <Field label="Number when none is sent" htmlFor={`sp-n-${i}`} error={sp('number')}>
                        <input id={`sp-n-${i}`} className="input" inputMode="tel" placeholder="optional" value={s.number ?? ''} onChange={(e) => setSpan(i, { number: e.target.value })} />
                      </Field>
                    )}
                    {sig === 'isdn' && <>
                    <Field label="BitVoice is" htmlFor={`sp-r-${i}`} error={sp('role')}>
                      <select id={`sp-r-${i}`} className="input" value={s.role} onChange={(e) => setSpan(i, { role: e.target.value as CardSpan['role'], networkTransfer: e.target.value === 'user' && s.networkTransfer, charges: e.target.value === 'user' && s.charges })}>
                        <option value="user">the PBX (user side)</option><option value="network">the network</option>
                      </select>
                    </Field>
                    <Field label="Switch type" htmlFor={`sp-s-${i}`} error={sp('switch')}>
                      <select id={`sp-s-${i}`} className="input" value={s.switch} onChange={(e) => setSpan(i, { switch: e.target.value, networkTransfer: ['ni2', '5ess', 'dms100', 'euroisdn'].includes(e.target.value) && s.networkTransfer, charges: e.target.value === 'euroisdn' && s.charges })}>
                        {s.type === 'bri'
                          ? <><option value="euroisdn">EuroISDN</option><option value="ni2">National ISDN</option></>
                          : <><option value="ni2">National ISDN-2</option><option value="5ess">AT&amp;T 5ESS</option><option value="dms100">Nortel DMS-100</option><option value="euroisdn">EuroISDN</option><option value="qsig">QSIG</option></>}
                      </select>
                    </Field>
                    </>}
                    <button type="button" className="btn" aria-label={`Remove span ${i + 1}`} onClick={() => set({ spans: spans.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button>
                  </div>
                  {sig === 'em' && <p className="text-xs text-slate-500">{emHelp[s.start || 'wink']}</p>}
                  {sig === 'ss7' && <p className="text-xs text-slate-500">ISUP calls on the span&apos;s voice circuits, signalled on one link (the D-channel&apos;s timeslot, set up as <code>mtp2</code> or <code>hardhdlc</code> in system.conf) to the adjacent signalling point: the carrier&apos;s STP or switch, whose point codes and CIC numbering it gives you. One link per span; the link set comes up with the link test and a reset of every circuit.</p>}
                  {sig === 'r2' && <p className="text-xs text-slate-500">Line signalling on the ABCD bits of timeslot 16 (Q.421), the number and the caller&apos;s category and number by compelled multifrequency tones (Q.441), in the country&apos;s variant. Calls go both ways on every circuit.</p>}
                  {s.type === 'bri' && (
                    <label className="flex items-center gap-3 pt-1 text-sm text-slate-300">
                      <Toggle checked={!!s.multipoint} onChange={(v) => setSpan(i, { multipoint: v })} label={`Multipoint on span ${i + 1}`} />
                      Several devices on the line (multipoint) <span className="text-xs text-slate-500">({s.role === 'user'
                        ? 'a "multi-device" line: BitVoice asks the network for its own TEI and answers calls to it beside any other devices; off for a PBX line (point to point)'
                        : 'ISDN phones on BitVoice\'s S-bus: each gets a TEI, calls to the line ring them all and the first to answer takes the call, numbers dialled digit by digit are collected'})</span>
                    </label>
                  )}
                  {sig === 'isdn' && s.role === 'user' && ['ni2', '5ess', 'dms100', 'euroisdn'].includes(s.switch) && (
                    <label className="flex items-center gap-3 pt-1 text-sm text-slate-300">
                      <Toggle checked={!!s.networkTransfer} onChange={(v) => setSpan(i, { networkTransfer: v })} label={`Transfers through the phone company on span ${i + 1}`} />
                      Transfers through the phone company <span className="text-xs text-slate-500">({s.switch === 'dms100'
                        ? 'Release Link Trunk: when one of the two calls is one BitVoice placed, the DMS-100 joins them and frees both channels; needs RLT on the line'
                        : 'a caller transferred to another number on this line is joined by the network, freeing both channels; needs the service on the line'})</span>
                    </label>
                  )}
                  {sp('networkTransfer') && <p className="text-xs text-rose-300">{sp('networkTransfer')}</p>}
                  {sig === 'isdn' && s.role === 'user' && s.switch === 'euroisdn' && (
                    <label className="flex items-center gap-3 pt-1 text-sm text-slate-300">
                      <Toggle checked={!!s.charges} onChange={(v) => setSpan(i, { charges: v })} label={`Advice of charge on span ${i + 1}`} />
                      Advice of charge <span className="text-xs text-slate-500">(asks the phone company what each call out costs, during the call and at its end; its figure goes on the call&apos;s record, and is the call&apos;s cost when it is in your currency)</span>
                    </label>
                  )}
                  {sp('charges') && <p className="text-xs text-rose-300">{sp('charges')}</p>}
                  <p className="text-xs text-slate-500">{chans} <StateText s={status?.channels[`span${i + 1}`]} /></p>
                </div>
              )
            })}
            <button type="button" className="btn" onClick={() => set({ spans: [...spans, { type: 't1', first: Math.max(1, ...k.channels.map((c) => c.channel + 1), ...spans.map((s) => s.first + (s.type === 'e1' ? 31 : s.type === 'bri' ? 3 : 24))), role: 'user', switch: 'ni2' }] })}><Plus size={14} aria-hidden /> Add a T1, E1 or BRI</button>
          </section>
        )}
        <button type="button" className="btn btn-primary" disabled={isLoading || !edit} onClick={submit}><Save size={14} aria-hidden /> Save</button>
      </fieldset>
    </Page>
  )
}
