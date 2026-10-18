import { FindingList } from './Judges'
import { Check, Sparkles, X } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useApplyAskMutation, useAskMutation, useSettingsQuery } from './api'
import { useIsAdmin } from './Editor'
import { ChangeList } from './Revisions'
import { Spinner } from './ui'

/**
 * Ask in plain words: describe a change, see exactly what it would do, then apply it or not. The model
 * only proposes; BitVoice checks the proposal like any other change.
 */
export function Ask() {
  const admin = useIsAdmin()
  const { data: settings } = useSettingsQuery()
  const [ask, { isLoading, data, error, reset }] = useAskMutation()
  const [apply, { isLoading: applying }] = useApplyAskMutation()
  const [text, setText] = useState('')
  if (!admin) return null
  const on = settings?.system.ai.enabled
  const fail = error ? errorOf(error) as { message: string; explanation?: string } : null
  return (
    <section className="card mb-6 space-y-3 p-5" aria-labelledby="ask-h">
      <h2 id="ask-h" className="flex items-center gap-2 font-semibold text-slate-100"><Sparkles size={17} className="text-violet-300" aria-hidden /> Ask in plain words</h2>
      {!on ? <p className="text-sm text-slate-500">Switch on AI in Settings to describe changes in plain words.</p> : (
        <>
          <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); if (text.trim()) ask({ request: text }) }}>
            <label htmlFor="askq" className="sr-only">What do you want to change?</label>
            <input id="askq" className="input" value={text} onChange={(e) => { setText(e.target.value); reset() }} placeholder="Forward the sales group to voicemail after 5 pm" maxLength={1000} />
            <button className="btn btn-primary shrink-0" disabled={isLoading || !text.trim()}>{isLoading ? <Spinner label="Thinking" /> : <Sparkles size={15} aria-hidden />} Propose</button>
          </form>
          <p className="text-xs text-slate-500">The request and the configuration (without passwords) go to the AI service in Settings. Nothing changes until you apply it.</p>
          {fail && <div role="alert" className="rounded-lg border border-rose-800 p-3 text-sm text-rose-200">{fail.message}{fail.explanation && <span className="block text-slate-400">{fail.explanation}</span>}</div>}
          {data && (
            <div className="space-y-3 rounded-lg border border-violet-900 p-3" role="status">
              {data.explanation && <p className="text-sm text-violet-100">{data.explanation}</p>}
              {data.changes.length === 0 ? <p className="text-sm text-slate-400">Nothing to change.</p> : (
                <>
                  <ChangeList items={data.changes.filter((c) => !data.changes.some((o) => o.path.startsWith(c.path + '.') && o.before === c.before && o.after === c.after))} />
                  {data.check && data.check.length > 0 && (
                    <div className="space-y-2" role="alert">
                      <p className="text-sm font-semibold text-amber-300">Checked in every situation, this change would cause:</p>
                      <FindingList items={data.check} />
                    </div>
                  )}
                  <div className="flex gap-2">
                    <button className="btn btn-primary" disabled={applying} onClick={async () => {
                      try { await apply(data.id).unwrap(); toast.success('Changed'); reset(); setText('') } catch (e) { toast.error(errorOf(e).message) }
                    }}><Check size={15} aria-hidden /> Apply</button>
                    <button className="btn" onClick={() => reset()}><X size={15} aria-hidden /> Discard</button>
                  </div>
                </>
              )}
            </div>
          )}
        </>
      )}
    </section>
  )
}
