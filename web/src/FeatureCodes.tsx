import { Asterisk, Save } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { useSaveSettingsMutation, useSettingsQuery, type ApiErrorBody, type FeatureCodes, type SpeedDial } from './api'
import { useIsAdmin } from './Editor'
import { SpeedDialList } from './Extensions'
import { useDraft } from './useDraft'
import { Field, Page, StaleBanner, problemFor } from './ui'

const defaults: FeatureCodes = {
  forwardAll: '*72', forwardAllOff: '*73', forwardBusy: '*90', forwardBusyOff: '*91', forwardNoAnswer: '*52', forwardNoAnswerOff: '*53',
  dndOn: '*78', dndOff: '*79', dndToggle: '*76', followMe: '*21', pickup: '*8', directedPickup: '**', intercom: '*80', speedDial: '*0', wakeUp: '*62', roomStatus: '*68', toggle: '*28',
}

const groups: { title: string; color: string; codes: { key: keyof FeatureCodes; label: string; how: string }[] }[] = [
  { title: 'Forwarding', color: 'text-sky-300', codes: [
    { key: 'forwardAll', label: 'Forward every call', how: 'Dial it, hear a tone, dial the number and #. Or dial the number straight after it: *72 2125550199.' },
    { key: 'forwardAllOff', label: 'Stop forwarding every call', how: 'Rising tone: done.' },
    { key: 'forwardBusy', label: 'Forward when on a call', how: 'Same as forwarding every call, for calls that come while you talk.' },
    { key: 'forwardBusyOff', label: 'Stop forwarding when on a call', how: '' },
    { key: 'forwardNoAnswer', label: 'Forward when not answered', how: 'Calls ring your phones first, for their ring time.' },
    { key: 'forwardNoAnswerOff', label: 'Stop forwarding when not answered', how: '' },
  ] },
  { title: 'Do not disturb and follow-me', color: 'text-rose-300', codes: [
    { key: 'dndOn', label: 'Do not disturb on', how: 'Calls go to voicemail (or the extension\u2019s no-answer destination). Emergency call-backs still ring.' },
    { key: 'dndOff', label: 'Do not disturb off', how: '' },
    { key: 'dndToggle', label: 'Switch do not disturb', how: 'Rising tone: now on. Falling tone: now off.' },
    { key: 'followMe', label: 'Switch follow-me', how: 'Only for extensions with follow-me numbers set.' },
  ] },
  { title: 'Answering and calling', color: 'text-teal-300', codes: [
    { key: 'pickup', label: 'Answer a call ringing in your pickup group', how: 'The oldest ringing call in the group.' },
    { key: 'directedPickup', label: 'Answer a call ringing one extension', how: 'Followed by the extension: **101.' },
    { key: 'intercom', label: 'Intercom', how: 'Followed by the extension: *80101. Its phone answers on the speaker, both ways.' },
    { key: 'speedDial', label: 'Speed dial', how: 'Followed by the code: *05.' },
    { key: 'toggle', label: 'Switch a day/night toggle', how: 'Followed by the toggle\u2019s code: *281. Rising tone: switched over; falling tone: back to normal.' },
  ] },
  { title: 'Wake-up calls and hotel', color: 'text-amber-300', codes: [
    { key: 'wakeUp', label: 'Wake-up call', how: 'Hear the one set, enter four digits (0630) to book one, or star to cancel it.' },
    { key: 'roomStatus', label: 'Room status (housekeeping)', how: 'Followed by a digit from the room\u2019s phone: *683. Sent to the hotel system.' },
  ] },
]

/** Feature codes: what people dial on their phones, and the company's speed dials. */
export function FeatureCodesPage() {
  const admin = useIsAdmin()
  const { data } = useSettingsQuery()
  const [save, { isLoading }] = useSaveSettingsMutation()
  const initial = data ? { codes: { ...defaults, ...(data.system.codes ?? {}) }, speedDials: data.system.speedDials ?? [] } : undefined
  const { draft, setDraft, reset, stale } = useDraft<{ codes: FeatureCodes; speedDials: SpeedDial[] }>(initial)
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  if (!draft || !data) return null
  const sys = data.system
  const p = (f: string) => problemFor(err?.problems, 'system.' + f)
  const doSave = async () => {
    try {
      const s = await save({ ...data, system: { ...sys, codes: draft.codes, speedDials: draft.speedDials } }).unwrap()
      reset({ codes: { ...defaults, ...(s.system.codes ?? {}) }, speedDials: s.system.speedDials ?? [] }); setErr(null); toast.success('Feature codes saved')
    } catch (e) { setErr((e as { data?: ApiErrorBody }).data ?? { message: 'The feature codes could not be saved.' }) }
  }
  const fixed: [string, string, string][] = [
    [sys.voicemailNumber, 'Your voicemail', 'text-violet-300'], [sys.echoNumber, 'Echo test', 'text-emerald-300'],
    [sys.parkNumber, 'Park a call (transfer to it); 1 to 9 after it are the slots', 'text-amber-300'],
    [sys.agentLogin ?? '*45', 'Queue agents: log in or out', 'text-orange-300'], [sys.agentPause ?? '*46', 'Queue agents: pause or resume', 'text-orange-300'],
    [sys.hotDesk?.login ?? '*33', 'Sign in at a hot desk', 'text-sky-300'], [sys.hotDesk?.logout ?? '*34', 'Sign out of a hot desk', 'text-sky-300'],
  ]
  return (
    <Page icon={Asterisk} title="Feature codes" accent="from-sky-400 to-teal-400"
      intro="What people dial on their phones to forward calls, set do not disturb, pick up a colleague's call, use the intercom and speed dials. Every code works on every phone; type off to switch one off.">
      {stale && <StaleBanner onReload={() => { reset(); setErr(null) }} />}
      <div className="grid gap-4 lg:grid-cols-3">
        {groups.map((g) => (
          <section key={g.title} className="card space-y-3 p-4" aria-labelledby={`fc-${g.title}`}>
            <h2 id={`fc-${g.title}`} className={`font-semibold ${g.color}`}>{g.title}</h2>
            {g.codes.map((c) => (
              <Field key={c.key} label={c.label} htmlFor={`fc-${c.key}`} error={p(`codes.${c.key}`)} hint={c.how || undefined}>
                <input id={`fc-${c.key}`} className="input w-28 font-mono text-amber-300" disabled={!admin} value={draft.codes[c.key]}
                  onChange={(e) => { const v = e.target.value.trim(); setDraft((d) => ({ ...d, codes: { ...d.codes, [c.key]: v } })) }} />
              </Field>
            ))}
          </section>
        ))}
      </div>
      <section className="card mt-4 space-y-3 p-4" aria-labelledby="sd-h">
        <h2 id="sd-h" className="font-semibold text-amber-300">Company speed dials</h2>
        <fieldset disabled={!admin}>
          <SpeedDialList label="Speed dials" hint={`Anyone dials ${draft.codes.speedDial} and the code. A person's own speed dial (on their extension) wins over one here with the same code.`}
            field="system.speedDials" list={draft.speedDials} onChange={(v) => setDraft((d) => ({ ...d, speedDials: v }))} p={(f) => problemFor(err?.problems, f)} />
        </fieldset>
      </section>
      {admin && <div className="mt-4"><button type="button" className="btn btn-primary" disabled={isLoading} onClick={doSave}><Save size={14} aria-hidden /> Save</button></div>}
      {err && !err.problems?.length && <p className="mt-2 text-sm text-rose-300">{err.message}</p>}
      <section className="card mt-6 p-4" aria-labelledby="other-h">
        <h2 id="other-h" className="font-semibold text-slate-200">Other numbers people dial</h2>
        <p className="text-xs text-slate-500">Set in Settings, Queues and Phones.</p>
        <table className="mt-3 text-sm"><tbody>
          {fixed.filter(([n]) => n && n !== 'off').map(([n, what, color]) => <tr key={what}><td className={`py-1 pr-6 font-mono ${color}`}>{n}</td><td className="text-slate-300">{what}</td></tr>)}
          {(sys.emergencyNumbers ?? []).map((n) => <tr key={n}><td className="py-1 pr-6 font-mono text-rose-300">{n}</td><td className="text-slate-300">Emergency: always works, from every extension</td></tr>)}
        </tbody></table>
      </section>
    </Page>
  )
}
