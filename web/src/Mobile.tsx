import qrcode from 'qrcode-generator'
import { BellRing, BellOff, Smartphone, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { errorOf, useMobileDevicesQuery, usePairMobileMutation, useUnpairMobileMutation, type Settings } from './api'
import { Field, radioKeys, relTime, Toggle } from './ui'

function qrSvg(text: string) {
  const q = qrcode(0, 'M')
  q.addData(text)
  q.make()
  return q.createSvgTag({ cellSize: 4, margin: 2, scalable: true })
}

/** An extension's paired phones running the BitVoice app, and pairing a new one with a QR code. */
export function MobileDevices({ ext, admin }: { ext: string; admin: boolean }) {
  const { data } = useMobileDevicesQuery(ext, { pollingInterval: 4000 })
  const [pair, { isLoading, data: code, reset }] = usePairMobileMutation()
  const [unpair] = useUnpairMobileMutation()
  const devices = data?.items ?? []
  return (
    <section className="card space-y-3 border-sky-900/50 p-4" aria-labelledby="mob-h">
      <div className="flex items-center justify-between">
        <h3 id="mob-h" className="flex items-center gap-2 text-sm font-semibold text-sky-300"><Smartphone size={15} aria-hidden /> Mobile app</h3>
        {admin && !code && <button type="button" className="btn text-xs" disabled={isLoading} onClick={() => pair(ext).unwrap().catch((e) => toast.error(errorOf(e).message))}>Add a phone</button>}
      </div>
      {code && (
        <div className="flex flex-wrap items-start gap-4 rounded-lg border border-slate-800 p-3">
          <div className="w-40 rounded-lg bg-white p-1" role="img" aria-label="QR code to pair the BitVoice app" dangerouslySetInnerHTML={{ __html: qrSvg(code.link) }} />
          <div className="min-w-0 flex-1 space-y-1 text-sm text-slate-300">
            <p>In the <span className="text-sky-300">BitVoice</span> app, tap <span className="font-semibold">Pair</span> and scan this code. It works once, for ten minutes.</p>
            <p className="text-xs text-slate-500">Server <span className="font-mono text-slate-300">{code.server}</span></p>
            {code.local && <p className="text-xs text-amber-300">This is a plain http address: phones away from this network will not reach it. Set an https address under Settings, Mobile apps.</p>}
            {!code.push && <p className="text-xs text-amber-300">Push is off (Settings, Mobile apps): the app rings only while it is open.</p>}
            <button type="button" className="btn mt-1 text-xs" onClick={reset}>Done</button>
          </div>
        </div>
      )}
      {devices.length === 0 ? <p className="text-xs text-slate-500">No phones paired.</p> : (
        <ul className="divide-y divide-slate-800 text-sm">
          {devices.map((d) => (
            <li key={d.id} className="flex items-center gap-3 py-2">
              {d.canWake ? <BellRing size={15} className="text-emerald-300" aria-label="Can be woken for calls" /> : <BellOff size={15} className="text-amber-300" aria-label="Cannot be woken for calls" />}
              <div className="min-w-0 flex-1">
                <div className="text-slate-100">{d.name} <span className="text-xs text-slate-500">{d.platform === 'ios' ? 'iPhone' : 'Android'}</span></div>
                <div className={d.pushError ? 'text-xs text-rose-300' : 'text-xs text-slate-500'}>
                  {d.pushError || (d.lastSeen ? `seen ${relTime(d.lastSeen)}` : 'not seen yet')}{d.lastPush && !d.pushError ? `, woken ${relTime(d.lastPush)}` : ''}
                </div>
              </div>
              {admin && <button type="button" className="btn px-2 py-1 text-rose-300" aria-label={`Unpair ${d.name}`}
                onClick={() => { if (confirm(`Unpair ${d.name}? It stops ringing for this extension.`)) unpair(d.id).unwrap().then(() => toast.success('Phone unpaired')).catch((e) => toast.error(errorOf(e).message)) }}><Trash2 size={13} aria-hidden /></button>}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

type Mobile = Settings['system']['mobile']

/** How BitVoice wakes its apps: off, BioDream's relay, or this server's own Apple and Google credentials. */
export function MobileSettings({ value, onChange, error }: { value: Mobile; onChange: (v: Partial<Mobile>) => void; error: (f: string) => string | undefined }) {
  const modes = [
    ['', 'Off', 'The apps ring only while open.'],
    ['relay', 'Push relay', 'BioDream’s relay (with hosting or support), or your own.'],
    ['direct', 'Own apps', 'Apple and Google credentials of apps you build yourself.'],
  ] as const
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="mobset-h">
      <h2 id="mobset-h" className="flex items-center gap-2 font-semibold text-slate-100"><Smartphone size={17} className="text-sky-300" aria-hidden /> Mobile apps</h2>
      <p className="text-sm text-slate-400">The BitVoice apps for iPhone and Android ring like a desk phone, even when closed: an incoming call sends a push that wakes the app, which then connects and rings.</p>
      <div className="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Push">
        {modes.map(([id, t, d], i) => (
          <button type="button" key={id} role="radio" aria-checked={value.push === id} onClick={() => onChange({ push: id })}
            {...radioKeys(i, modes.map((m) => m[0]), value.push, (v) => onChange({ push: v }))}
            className={`card p-3 text-left ${value.push === id ? 'ring-2 ring-sky-400' : ''}`}>
            <div className={`text-sm font-semibold ${id ? 'text-sky-300' : 'text-slate-300'}`}>{t}</div>
            <div className="text-xs text-slate-500">{d}</div>
          </button>
        ))}
      </div>
      {error('system.mobile.push') && <p className="field-error text-sm text-rose-300">{error('system.mobile.push')}</p>}
      <Field label="Address the apps use" htmlFor="mserver" error={error('system.mobile.serverUrl')} hint="This server's https address from anywhere (your domain and port). Empty uses the address in the browser when a phone is paired.">
        <input id="mserver" className="input font-mono" placeholder="https://pbx.example.com:8443" value={value.serverUrl} onChange={(e) => onChange({ serverUrl: e.target.value.trim() })} />
      </Field>
      {value.push === 'relay' && (
        <div className="grid gap-3 md:grid-cols-3">
          <Field label="Relay address" htmlFor="mrel" error={error('system.mobile.relayUrl')}><input id="mrel" className="input font-mono" placeholder="https://push.example.com" value={value.relayUrl} onChange={(e) => onChange({ relayUrl: e.target.value.trim() })} /></Field>
          <Field label="Key ID" htmlFor="mrk" error={error('system.mobile.relayKey')}><input id="mrk" className="input font-mono" value={value.relayKey} onChange={(e) => onChange({ relayKey: e.target.value.trim() })} /></Field>
          <Field label="Secret" htmlFor="mrs" error={error('system.mobile.relaySecret')} hint="Leave empty to keep the current one."><input id="mrs" type="password" autoComplete="off" className="input font-mono" value={value.relaySecret} onChange={(e) => onChange({ relaySecret: e.target.value })} /></Field>
        </div>
      )}
      {value.push === 'direct' && (
        <div className="grid gap-4 md:grid-cols-2">
          <div className="space-y-3">
            <h3 className="text-sm font-medium text-slate-200">Apple (iPhone)</h3>
            <div className="grid grid-cols-3 gap-2">
              <Field label="Key ID" htmlFor="mak" error={error('system.mobile.apns.keyId')}><input id="mak" className="input font-mono" value={value.apns.keyId} onChange={(e) => onChange({ apns: { ...value.apns, keyId: e.target.value.trim() } })} /></Field>
              <Field label="Team ID" htmlFor="mat"><input id="mat" className="input font-mono" value={value.apns.teamId} onChange={(e) => onChange({ apns: { ...value.apns, teamId: e.target.value.trim() } })} /></Field>
              <Field label="Bundle ID" htmlFor="mab"><input id="mab" className="input font-mono" value={value.apns.topic} onChange={(e) => onChange({ apns: { ...value.apns, topic: e.target.value.trim() } })} /></Field>
            </div>
            <Field label="Key (.p8)" htmlFor="map" error={error('system.mobile.apns.key')} hint="Leave empty to keep the current one.">
              <textarea id="map" rows={4} className="input font-mono text-xs" spellCheck={false} value={value.apns.key} onChange={(e) => onChange({ apns: { ...value.apns, key: e.target.value } })} placeholder="-----BEGIN PRIVATE KEY-----" />
            </Field>
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={value.apns.sandbox} onChange={(v) => onChange({ apns: { ...value.apns, sandbox: v } })} label="Development builds" /> Development builds (sandbox)</label>
          </div>
          <div className="space-y-3">
            <h3 className="text-sm font-medium text-slate-200">Google (Android)</h3>
            <Field label="Firebase service account (JSON)" htmlFor="mfcm" error={error('system.mobile.fcm')} hint="Leave empty to keep the current one.">
              <textarea id="mfcm" rows={7} className="input font-mono text-xs" spellCheck={false} value={value.fcm} onChange={(e) => onChange({ fcm: e.target.value })} placeholder='{"type": "service_account", ...}' />
            </Field>
          </div>
        </div>
      )}
    </section>
  )
}
