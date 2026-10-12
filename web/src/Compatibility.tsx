import { BadgeCheck, CircleCheck, Radio, Smartphone } from 'lucide-react'
import { useCompatibilityQuery } from './api'
import { Badge, relTime } from './ui'

const kinds: Record<string, string> = {
  phone: 'Desk phones', adapter: 'Adapters and speakers', softphone: 'Softphones', browser: 'Browsers', door: 'Door stations', provider: 'Providers and tools',
}

/** What BitVoice works with: the families it is built and tested for, and what has worked here. */
export function Compatibility() {
  const { data } = useCompatibilityQuery()
  if (!data) return null
  return (
    <section className="card mt-6 space-y-5 p-5" aria-labelledby="compat-list-h">
      <h2 id="compat-list-h" className="flex items-center gap-2 font-semibold text-slate-100"><BadgeCheck size={17} className="text-emerald-300" aria-hidden /> Works with</h2>
      <div className="grid gap-3 md:grid-cols-2">
        {data.tested.map((t, i) => (
          <div key={i} className="rounded-lg border border-slate-800 p-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium text-slate-100">{t.vendor}</span>
              <span className="text-xs text-slate-500">{kinds[t.kind] ?? t.kind}</span>
              {t.provision && <Badge status="ok">Set up automatically</Badge>}
            </div>
            <div className="text-xs text-sky-300">{t.models}</div>
            <p className="mt-1 text-xs text-slate-400">{t.notes}</p>
          </div>
        ))}
      </div>
      <div>
        <h3 className="mb-2 flex items-center gap-2 text-sm font-medium text-slate-200"><Smartphone size={15} className="text-sky-300" aria-hidden /> Seen working here</h3>
        {data.seen.length === 0 ? <p className="text-sm text-slate-500">No phone has registered yet.</p> : (
          <table className="w-full text-sm">
            <thead><tr className="text-left text-xs text-slate-500"><th className="py-1 font-normal">Phone or app</th><th className="font-normal">Extensions</th><th className="font-normal">Last seen</th></tr></thead>
            <tbody>
              {data.seen.map((d) => (
                <tr key={d.userAgent} className="border-t border-slate-800">
                  <td className="py-1.5"><CircleCheck size={13} className="mr-1.5 inline text-emerald-300" aria-hidden />{d.vendor && <><span className="mr-1 text-slate-100">{d.vendor}</span>{' '}</>}<span className="font-mono text-xs text-slate-400">{d.userAgent}</span></td>
                  <td className="font-mono text-xs text-sky-300">{d.extensions.join(', ')}</td>
                  <td className="text-xs text-slate-500">{relTime(d.last)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {data.trunks.length > 0 && (
        <div>
          <h3 className="mb-2 flex items-center gap-2 text-sm font-medium text-slate-200"><Radio size={15} className="text-teal-300" aria-hidden /> Providers here</h3>
          <ul className="space-y-1 text-sm">
            {data.trunks.map((t) => (
              <li key={t.id} className="flex items-center gap-2"><span className="text-slate-100">{t.name}</span><Badge status={t.state === 'registered' || t.state === 'up' ? 'ok' : t.state === 'disabled' ? 'idle' : 'warn'}>{t.state}</Badge>{t.error && <span className="text-xs text-amber-300">{t.error}</span>}</li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}
