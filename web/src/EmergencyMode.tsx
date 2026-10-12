import { Siren } from 'lucide-react'
import { toast } from 'sonner'
import { errorOf, useEmergencyModeMutation, useSettingsQuery, type Settings } from './api'
import { useIsAdmin } from './Editor'
import { DestinationPicker } from './Extensions'
import { Field, ListInput, relTime } from './ui'

type EM = Settings['system']['emergencyMode']

const where = (em: EM) => em.destination?.type ? `${em.destination.type} ${em.destination.target}`.trim() : 'nowhere yet'

/** Turns emergency mode on or off, asking first. */
function useSwitch() {
  const [set, { isLoading }] = useEmergencyModeMutation()
  const flip = async (on: boolean, em: EM) => {
    const ask = on ? `Send every incoming call to ${where(em)}? Normal routes are ignored until it is turned off.` : 'Turn emergency mode off? Calls go back to their normal routes.'
    if (!confirm(ask)) return
    try {
      await set({ on }).unwrap()
      toast[on ? 'warning' : 'success'](on ? 'Emergency mode is on' : 'Emergency mode is off')
    } catch (e) { toast.error(errorOf(e).message) }
  }
  return { flip, isLoading }
}

/** The red bar on every screen while emergency mode is on. */
export function EmergencyBanner() {
  const { data } = useSettingsQuery()
  const admin = useIsAdmin()
  const { flip, isLoading } = useSwitch()
  const em = data?.system.emergencyMode
  if (!em?.on) return null
  return (
    <div role="alert" className="flex flex-wrap items-center gap-3 bg-rose-700 px-4 py-2 text-sm font-medium text-white">
      <Siren size={16} className="animate-pulse" aria-hidden />
      <span className="flex-1">Emergency mode is on: every incoming call goes to {where(em)}{em.since ? `, since ${relTime(em.since)}` : ''}{em.by ? ` (by ${em.by})` : ''}.</span>
      {admin && <button className="rounded-md bg-white/15 px-3 py-1 hover:bg-white/25" disabled={isLoading} onClick={() => flip(false, em)}>Turn off</button>}
    </div>
  )
}

/** The switch on the dashboard. */
export function EmergencySwitch() {
  const { data } = useSettingsQuery()
  const admin = useIsAdmin()
  const { flip, isLoading } = useSwitch()
  const em = data?.system.emergencyMode
  if (!em || (!admin && !em.on)) return null
  const ready = !!em.destination?.type
  return (
    <section className={`card flex flex-wrap items-center gap-4 p-4 ${em.on ? 'border-rose-700' : ''}`} aria-labelledby="em-h">
      <Siren size={22} className={em.on ? 'text-rose-300' : 'text-slate-500'} aria-hidden />
      <div className="min-w-0 flex-1">
        <h2 id="em-h" className="font-semibold text-slate-100">Emergency mode</h2>
        <p className="text-sm text-slate-400">
          {ready ? <>Sends every incoming call to <span className="text-amber-300">{where(em)}</span>{em.code ? <>; dial <span className="font-mono text-sky-300">{em.code}</span> from an allowed phone to switch it</> : null}.</>
            : <>Set where calls go in Settings first.</>}
        </p>
      </div>
      {admin && (
        <button className={em.on ? 'btn' : 'btn btn-danger'} disabled={isLoading || (!em.on && !ready)} aria-pressed={em.on} onClick={() => flip(!em.on, em)}>
          {em.on ? 'Turn off' : 'Turn on'}
        </button>
      )}
    </section>
  )
}

/** Where calls go in emergency mode, and who may switch it from a phone. */
export function EmergencySettings({ value, onChange, error }: { value: EM; onChange: (v: Partial<EM>) => void; error: (f: string) => string | undefined }) {
  return (
    <section className="card space-y-4 p-5" aria-labelledby="emset-h">
      <h2 id="emset-h" className="flex items-center gap-2 font-semibold text-slate-100"><Siren size={17} className="text-rose-300" aria-hidden /> Emergency mode</h2>
      <p className="text-sm text-slate-400">One switch that sends every incoming call to one place: an evacuation, a closure, an incident. Switch it on the Dashboard, or by dialling the code.</p>
      <DestinationPicker label="Send every incoming call to" emptyLabel="Choose where calls go" value={value.destination?.type ? value.destination : null}
        onChange={(d) => onChange({ destination: d ?? ({ type: '', target: '' } as unknown as EM['destination']) })} error={error('system.emergencyMode.destination')} />
      <div className="grid gap-3 sm:grid-cols-[8rem_1fr]">
        <Field label="Code" htmlFor="emc" error={error('system.emergencyMode.code')} hint="Optional, such as *55.">
          <input id="emc" className="input font-mono" value={value.code ?? ''} onChange={(e) => onChange({ code: e.target.value.replace(/[^0-9*]/g, '') })} />
        </Field>
        <Field label="Extensions that may dial it" htmlFor="ema" error={error('system.emergencyMode.allowed')}>
          <ListInput id="ema" mono value={value.allowed ?? []} onChange={(v) => onChange({ allowed: v })} placeholder="100" />
        </Field>
      </div>
    </section>
  )
}
