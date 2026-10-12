import { Building2, RadioTower, Signal } from 'lucide-react'
import { useState } from 'react'
import { useInsightQuery, useQualityQuery, type QualityPlace } from './api'
import { Chart } from './Chart'
import { Badge, Empty, Page } from './ui'

const statusWord = { ok: 'Good', warn: 'Look at this', fail: 'Poor', idle: 'Too few calls' } as const

/** Quality: how calls sound at each site and on each provider's line, with what is wrong in plain words. */
export function Quality() {
  const [days, setDays] = useState(7)
  const { data } = useQualityQuery(days)
  return (
    <Page icon={Signal} title="Call quality" accent="from-emerald-300 to-sky-300"
      intro="How calls sounded at each site and on each provider's line, from the sound BitVoice received, with what is wrong in plain words."
      actions={
        <select className="input w-40" aria-label="Time" value={days} onChange={(e) => setDays(Number(e.target.value))}>
          <option value={1}>Last day</option><option value={7}>Last 7 days</option><option value={30}>Last 30 days</option><option value={90}>Last 90 days</option>
        </select>
      }>
      {!data ? null : data.calls === 0 ? <Empty icon={Signal} title="No calls to judge">Quality appears once calls with sound have ended.</Empty> : (
        <>
          <p className="mb-4 text-sm text-slate-300"><span className="text-slate-500">{data.calls} calls: </span>{data.overall}</p>
          <ul className="grid gap-4 lg:grid-cols-2" aria-label="Quality by place">
            {data.places.map((p) => <PlaceCard key={p.kind + p.name} p={p} />)}
          </ul>
        </>
      )}
      <Conversations days={days} />
    </Page>
  )
}

/** Conversations: how analysed calls went, by the person who took them. Measures to look at, not a ranking. */
function Conversations({ days }: { days: number }) {
  const { data } = useInsightQuery(days)
  if (!data?.agents.length) return null
  return (
    <section className="card mt-6 p-4" aria-labelledby="conv-h">
      <h2 id="conv-h" className="font-semibold text-slate-100">Conversations, by who took them</h2>
      <p className="mb-3 text-sm text-slate-400">From transcribed calls: how much of the talking each person did, their longest stretch, how often they started over the caller, and the moments flagged. These point to calls worth listening to; they are not a score.</p>
      <table className="w-full text-sm">
        <thead className="text-left text-xs text-slate-500"><tr><th className="font-normal">Person</th><th className="font-normal">Calls</th><th className="font-normal">Their share of talk</th><th className="font-normal">Longest stretch</th><th className="font-normal">Talked over the caller</th><th className="font-normal">Quiet</th><th className="font-normal">Ended upset</th><th className="font-normal">To look at</th><th className="font-normal">Went well</th></tr></thead>
        <tbody>
          {data.agents.map((a) => (
            <tr key={a.ext || a.name} className="border-t border-slate-800">
              <td className="py-1 text-slate-200">{a.name}</td><td className="text-slate-300">{a.calls}</td>
              <td className={a.talkShare > 70 ? 'text-amber-300' : 'text-slate-300'}>{a.talkShare}%</td>
              <td className="font-mono text-slate-300">{Math.round(a.longest)} s</td>
              <td className={a.cutIns >= 2 ? 'text-amber-300' : 'text-slate-300'}>{a.cutIns} a call</td>
              <td className="text-slate-300">{a.silenceShare}%</td>
              <td className={a.endedUpset ? 'text-rose-300' : 'text-slate-300'}>{a.endedUpset}</td>
              <td className={a.lookAt ? 'text-amber-300' : 'text-slate-300'}>{a.lookAt}</td>
              <td className="text-emerald-300">{a.wentWell}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}

function PlaceCard({ p }: { p: QualityPlace }) {
  const Icon = p.kind === 'provider' ? RadioTower : Building2
  const lossColour = p.lossPct >= 5 ? 'text-rose-300' : p.lossPct >= 2 ? 'text-amber-300' : 'text-emerald-300'
  const jitColour = p.jitterMs >= 30 ? 'text-amber-300' : 'text-emerald-300'
  const busy = p.hours.some((h) => h.calls > 0)
  return (
    <li className="card space-y-3 p-4">
      <div className="flex items-center gap-2">
        <Icon size={16} className={p.kind === 'provider' ? 'text-teal-300' : 'text-amber-300'} aria-hidden />
        <h2 className="flex-1 font-semibold text-slate-100">{p.name} <span className="text-xs font-normal text-slate-500">{p.kind === 'provider' ? 'provider line' : 'site'}</span></h2>
        <Badge status={p.status}>{statusWord[p.status]}</Badge>
      </div>
      <dl className="grid grid-cols-4 gap-2 text-center text-xs">
        <div><dt className="text-slate-500">Calls</dt><dd className="font-mono text-base text-slate-100">{p.calls}</dd></div>
        <div><dt className="text-slate-500">Score</dt><dd className={`font-mono text-base ${p.mos >= 4 ? 'text-emerald-300' : p.mos >= 3.6 ? 'text-amber-300' : 'text-rose-300'}`}>{p.mos.toFixed(1)}</dd></div>
        <div><dt className="text-slate-500">Lost</dt><dd className={`font-mono text-base ${lossColour}`}>{p.lossPct.toFixed(1)}%</dd></div>
        <div><dt className="text-slate-500">Jitter</dt><dd className={`font-mono text-base ${jitColour}`}>{p.jitterMs.toFixed(0)} ms</dd></div>
      </dl>
      <p className="text-sm text-slate-200">{p.diagnosis}</p>
      {p.advice && <p className="text-sm text-slate-400">{p.advice}</p>}
      {busy && p.status !== 'idle' && (
        <Chart height={110} label={`Sound lost by hour of day at ${p.name}`} option={{
          grid: { left: 30, right: 6, top: 8, bottom: 20 },
          tooltip: { trigger: 'axis', valueFormatter: (v: number) => `${v}% lost` },
          xAxis: { type: 'category', data: p.hours.map((_, h) => String(h).padStart(2, '0')), axisLabel: { interval: 3 } },
          yAxis: { type: 'value', axisLabel: { formatter: '{value}%' } },
          series: [{ type: 'bar', data: p.hours.map((h) => ({ value: h.lossPct, itemStyle: { color: h.lossPct >= 5 ? '#fb7185' : h.lossPct >= 2 ? '#fbbf24' : '#34d399' } })) }],
        }} />
      )}
    </li>
  )
}
