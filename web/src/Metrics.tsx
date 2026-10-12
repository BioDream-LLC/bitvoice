import { Activity, Download, Send } from 'lucide-react'
import { useState } from 'react'
import { errorOf, useMetricsTestMutation, type Metrics } from './api'
import { Field } from './ui'

/** MetricsSettings: Prometheus scraping, an OpenTelemetry push and a ready-made Grafana dashboard. */
export function MetricsSettings({ value, onChange, error }: { value: Metrics; onChange: (v: Partial<Metrics>) => void; error: (f: string) => string | undefined }) {
  const [test, { isLoading }] = useMetricsTestMutation()
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null)
  const run = async () => {
    try {
      await test().unwrap()
      setResult({ ok: true, message: 'The collector took the measurements.' })
    } catch (e) {
      setResult({ ok: false, message: errorOf(e).message })
    }
  }
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="met-h">
      <h2 id="met-h" className="flex items-center gap-2 font-semibold text-slate-100"><Activity size={17} className="text-orange-300" aria-hidden /> Metrics</h2>
      <p className="text-sm text-slate-400">
        <span className="text-orange-300">Prometheus</span> scrapes <span className="font-mono text-sky-300">{location.origin}/metrics</span> with an
        API token (a viewer token is enough) as its bearer token: calls in progress and ended, talk time, phones registered, trunks up, callers waiting,
        sound lost and call quality. BitVoice can also push the same measurements to an <span className="text-teal-300">OpenTelemetry</span> collector.
      </p>
      <a className="btn w-fit" href="./api/metrics/dashboard" download><Download size={14} className="text-sky-300" aria-hidden /> Grafana dashboard</a>
      <div className="grid gap-4 md:grid-cols-2">
        <Field label="OpenTelemetry collector" htmlFor="otlu" error={error('system.metrics.otlpUrl')} hint="Its OTLP/HTTP address; /v1/metrics is added. Empty: no push.">
          <input id="otlu" className="input font-mono" placeholder="https://otel.example.com:4318" value={value.otlpUrl} onChange={(e) => onChange({ otlpUrl: e.target.value.trim() })} />
        </Field>
        <Field label="Every (seconds)" htmlFor="otli" error={error('system.metrics.intervalSeconds')} hint="15 to 3600; 60 when empty.">
          <input id="otli" className="input w-32 font-mono" inputMode="numeric" value={value.intervalSeconds || ''} onChange={(e) => onChange({ intervalSeconds: Number(e.target.value.replace(/\D/g, '')) || 0 })} />
        </Field>
        <Field label="Header" htmlFor="otlh" error={error('system.metrics.otlpHeader')} hint="Sent with each push, such as Authorization.">
          <input id="otlh" className="input font-mono" value={value.otlpHeader} onChange={(e) => onChange({ otlpHeader: e.target.value.trim() })} />
        </Field>
        <Field label="Header value" htmlFor="otls" error={error('system.metrics.otlpSecret')} hint="Such as Bearer and the collector's key. A vault reference works too.">
          <input id="otls" className="input font-mono" type="password" autoComplete="off" value={value.otlpSecret} onChange={(e) => onChange({ otlpSecret: e.target.value })} />
        </Field>
      </div>
      <button type="button" className="btn" disabled={isLoading || !value.otlpUrl} onClick={run}><Send size={14} className="text-teal-300" aria-hidden /> Push now</button>
      <p className="text-xs text-slate-500">Push now uses the saved settings: save first.</p>
      {result && <p role="status" className={`text-sm ${result.ok ? 'text-emerald-300' : 'text-rose-300'}`}>{result.message}</p>}
    </section>
  )
}
