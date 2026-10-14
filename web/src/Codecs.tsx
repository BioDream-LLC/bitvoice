import { ArrowDown, ArrowUp, AudioLines } from 'lucide-react'
import { clsx } from 'clsx'
import { Toggle } from './ui'

/** What each codec is, for people who do not know the names. */
export const codecInfo: Record<string, { label: string; detail: string; color: string; converts: boolean }> = {
  G722: { label: 'G.722', detail: 'HD voice, twice the sound range of a normal call', color: 'text-emerald-300', converts: true },
  PCMU: { label: 'G.711 µ-law', detail: 'Standard phone quality; North America and Japan', color: 'text-sky-300', converts: true },
  PCMA: { label: 'G.711 A-law', detail: 'Standard phone quality; the rest of the world', color: 'text-sky-300', converts: true },
  OPUS: { label: 'Opus', detail: 'HD voice for apps and browsers; copes well with a bad network', color: 'text-violet-300', converts: true },
  G729: { label: 'G.729', detail: 'Low bandwidth (8 kbit/s) for thin links and some carriers', color: 'text-amber-300', converts: true },
  'G726-32': { label: 'G.726 (32 kbit/s)', detail: 'Half the bandwidth of G.711 at near its quality; DECT phones and older gateways', color: 'text-teal-300', converts: true },
  'AAL2-G726-32': { label: 'G.726 (AAL2 order)', detail: 'G.726 packed the other way round, for equipment that needs it', color: 'text-teal-300', converts: true },
  'G726-40': { label: 'G.726 (40 kbit/s)', detail: 'Close to G.711 quality, carries modem and fax tones better than lower rates', color: 'text-teal-300', converts: true },
  'G726-24': { label: 'G.726 (24 kbit/s)', detail: 'Between 32 and 16 kbit/s; some DECT systems', color: 'text-teal-300', converts: true },
  'G726-16': { label: 'G.726 (16 kbit/s)', detail: 'Quarter of G.711\u2019s bandwidth, noticeably rougher sound', color: 'text-orange-300', converts: true },
  AMR: { label: 'AMR', detail: 'Mobile networks\u2019 narrowband codec (4.75 to 12.2 kbit/s); some mobile carriers and gateways', color: 'text-amber-300', converts: true },
  'AMR-WB': { label: 'AMR-WB (HD)', detail: 'HD voice of mobile networks (G.722.2, up to 23.85 kbit/s); VoLTE interconnects', color: 'text-emerald-300', converts: true },
  ILBC: { label: 'iLBC', detail: 'Low bandwidth (13.3 to 15.2 kbit/s) that copes well with lost packets; older softphones and apps', color: 'text-violet-300', converts: true },
  SPEEX: { label: 'Speex', detail: 'Open narrowband codec (15 kbit/s, 2.15 to 24.6); older softphones, Asterisk links', color: 'text-sky-300', converts: true },
  GSM: { label: 'GSM', detail: 'GSM 06.10 full rate (13 kbit/s); some older softphones and gateways', color: 'text-fuchsia-300', converts: true },
}
export const allCodecs = ['G722', 'PCMU', 'PCMA', 'OPUS', 'AMR-WB', 'G729', 'AMR', 'G726-40', 'G726-32', 'AAL2-G726-32', 'G726-24', 'G726-16', 'ILBC', 'SPEEX', 'GSM']

/**
 * An ordered list of codecs: switch each on or off and move it up or down (buttons, or Alt+arrow keys on
 * the row). The first one switched on is preferred.
 */
export function CodecList({ value, onChange, id, error }: { value: string[]; onChange: (v: string[]) => void; id: string; error?: string }) {
  const rows = [...value, ...allCodecs.filter((c) => !value.includes(c))]
  const move = (i: number, by: number) => {
    const j = i + by
    if (j < 0 || j >= rows.length || !value.includes(rows[i]) || !value.includes(rows[j])) return
    const next = [...value]
    ;[next[i], next[j]] = [next[j], next[i]]
    onChange(next)
    requestAnimationFrame(() => document.getElementById(`${id}-${next[j]}`)?.focus())
  }
  return (
    <ul id={id} className={clsx('divide-y divide-slate-800 rounded-lg border', error ? 'border-rose-700' : 'border-slate-800')} aria-label="Codecs, best first">
      {rows.map((c, i) => {
        const on = value.includes(c)
        const info = codecInfo[c]
        return (
          <li key={c} id={`${id}-${c}`} tabIndex={-1} className="flex items-center gap-3 px-3 py-2 outline-none focus:bg-slate-900"
            onKeyDown={(e) => { if (e.altKey && e.key === 'ArrowUp') { e.preventDefault(); move(i, -1) } if (e.altKey && e.key === 'ArrowDown') { e.preventDefault(); move(i, 1) } }}>
            <Toggle checked={on} label={`Use ${info.label}`} onChange={(v) => onChange(v ? [...value, c] : value.filter((x) => x !== c))} />
            <AudioLines size={15} className={on ? info.color : 'text-slate-600'} aria-hidden />
            <div className="min-w-0 flex-1">
              <div className={clsx('text-sm font-medium', on ? info.color : 'text-slate-500')}>
                {on && <span className="mr-2 font-mono text-xs text-slate-500">{i + 1}.</span>}{info.label}
              </div>
              <div className="text-xs text-slate-500">{info.detail}</div>
            </div>
            {on && (
              <div className="flex gap-1">
                <button type="button" className="btn px-1.5 py-1" aria-label={`Prefer ${info.label} more`} disabled={i === 0} onClick={() => move(i, -1)}><ArrowUp size={14} aria-hidden /></button>
                <button type="button" className="btn px-1.5 py-1" aria-label={`Prefer ${info.label} less`} disabled={i >= value.length - 1} onClick={() => move(i, 1)}><ArrowDown size={14} aria-hidden /></button>
              </div>
            )}
          </li>
        )
      })}
    </ul>
  )
}
