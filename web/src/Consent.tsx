import { Plus, Trash2 } from 'lucide-react'
import { useDeleteNoticeMutation, usePutNoticeMutation, useVoicemailQuery, type ConsentRule, type SystemSettings } from './api'
import { AudioRecording } from './Greeting'

type Rec = SystemSettings['recording']
const actions: [ConsentRule['action'], string][] = [['announce', 'Announce'], ['beep', 'Beep'], ['off', 'No notice'], ['never', 'Never record']]

/** ConsentSettings: how people on a recorded call are told, and rules by where the other party is. */
export function ConsentSettings({ rec, set, p }: { rec: Rec; set: (r: Rec) => void; p: (f: string) => string | undefined }) {
  const { data: vm } = useVoicemailQuery()
  const [put, { isLoading }] = usePutNoticeMutation()
  const [del] = useDeleteNoticeMutation()
  const rules = rec.rules ?? []
  const setRule = (i: number, v: Partial<ConsentRule>) => set({ ...rec, rules: rules.map((r, j) => (j === i ? { ...r, ...v } : r)) })
  return (
    <fieldset className="space-y-3 border-t border-slate-800 pt-4">
      <legend className="text-sm font-semibold text-slate-200">Telling people the call is recorded</legend>
      <div className="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Recording notice">
        {([['off', 'No notice', 'For places where one party’s consent is enough'], ['announce', 'Announce', 'Both sides hear the notice as the call is answered; recording starts after it'], ['beep', 'Beep', 'A short tone to both sides every 15 seconds']] as const).map(([id, t, d]) => (
          <button type="button" role="radio" key={id} aria-checked={(rec.notice || 'off') === id} onClick={() => set({ ...rec, notice: id })}
            className={`card p-3 text-left ${(rec.notice || 'off') === id ? 'ring-2 ring-amber-400' : ''}`}>
            <div className={`text-sm font-semibold ${id === 'off' ? 'text-slate-300' : 'text-amber-300'}`}>{t}</div>
            <div className="text-xs text-slate-500">{d}</div>
          </button>
        ))}
      </div>
      {p('system.recording.notice') && <p className="field-error text-sm text-rose-300">{p('system.recording.notice')}</p>}
      <AudioRecording label="Notice" missing="No notice recorded: callers hear three beeps instead. Record the words, such as “This call is recorded for training and quality.”"
        has={!!vm?.greetings?.includes('recording-notice')} src="./api/recording/notice" busy={isLoading} saved="Notice saved"
        save={(wav) => put(wav).unwrap()} reset={() => del().unwrap()} />
      <div className="space-y-2">
        <p className="text-sm text-slate-400">Rules by the outside number, for places whose law asks for more (every party must agree) or less. The longest matching start wins; calls between extensions follow the notice above.</p>
        {rules.length > 0 && (
          <table className="w-full text-sm" aria-label="Recording rules">
            <thead className="text-left text-xs text-slate-500"><tr><th className="font-normal">Numbers starting</th><th className="font-normal">Where</th><th className="font-normal">Then</th><th /></tr></thead>
            <tbody>
              {rules.map((r, i) => (
                <tr key={i} className="align-top">
                  <td className="pr-2 pb-2"><input aria-label={`Rule ${i + 1}: numbers starting`} className="input w-28 font-mono" inputMode="numeric" placeholder="1415" value={r.prefix} onChange={(e) => setRule(i, { prefix: e.target.value.trim() })} />
                    {p(`system.recording.rules[${i}].prefix`) && <p className="field-error mt-1 text-xs text-rose-300">{p(`system.recording.rules[${i}].prefix`)}</p>}</td>
                  <td className="pr-2 pb-2"><input aria-label={`Rule ${i + 1}: where`} className="input" placeholder="California" value={r.name} onChange={(e) => setRule(i, { name: e.target.value })} /></td>
                  <td className="pr-2 pb-2"><select aria-label={`Rule ${i + 1}: then`} className="input" value={r.action} onChange={(e) => setRule(i, { action: e.target.value as ConsentRule['action'] })}>
                    {actions.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></td>
                  <td className="pb-2"><button type="button" className="btn-ghost p-2" aria-label={`Remove rule ${i + 1}`} onClick={() => set({ ...rec, rules: rules.filter((_, j) => j !== i) })}><Trash2 size={14} className="text-rose-300" aria-hidden /></button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <button type="button" className="btn" onClick={() => set({ ...rec, rules: [...rules, { prefix: '', name: '', action: 'announce' }] })}><Plus size={14} className="text-emerald-300" aria-hidden /> Add a rule</button>
      </div>
      <p className="text-xs text-slate-500">BitVoice cannot give legal advice: check the recording laws where you and your callers are.</p>
    </fieldset>
  )
}
