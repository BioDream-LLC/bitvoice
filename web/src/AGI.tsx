import { clsx } from 'clsx'
import { Database, FileAudio, FolderCog, PlugZap, Plus, ScrollText, Trash2, Upload, Workflow } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import {
  errorOf, useAgiDBQuery, useAgiProgramsQuery, useAgiSoundsQuery, useDeleteAgiDBMutation, useDeleteAgiSoundMutation, usePutAgiDBMutation,
  useTestAgiMutation, useUploadAgiSoundMutation, type AGIRun, type AGIScript, type Settings,
} from './api'
import { DestinationPicker } from './Extensions'
import { onServerEvent } from './store'
import { Field, ListInput, Toggle, relTime } from './ui'

type Compat = Settings['system']['compat']

/** isProgram says whether an AGI address names a program on this server (not a FastAGI server). */
export function isProgram(address: string) {
  return address.trim() !== '' && !address.includes('://')
}

/** AGI scripts: the Asterisk Gateway Interface, so IVRs and integrations written for Asterisk keep working. */
export function AGISettings({ value, onChange, error, admin }: {
  value: Compat; onChange: (v: Partial<Compat>) => void; error: (f: string) => string | undefined; admin: boolean
}) {
  const scripts = value.agi ?? []
  const set = (i: number, v: Partial<AGIScript>) => onChange({ agi: scripts.map((a, j) => (j === i ? { ...a, ...v } : a)) })
  const [test, { isLoading: testing }] = useTestAgiMutation()
  const [results, setResults] = useState<Record<number, { ok: boolean; message: string }>>({})
  const tryIt = (i: number) => test(scripts[i].address).unwrap()
    .then((r) => setResults((x) => ({ ...x, [i]: r })))
    .catch((e) => setResults((x) => ({ ...x, [i]: { ok: false, message: errorOf(e).message } })))
  const [runs, setRuns] = useState<AGIRun[]>([])
  useEffect(() => onServerEvent((type, d) => { if (type === 'agi.ran') setRuns((r) => [d as AGIRun, ...r].slice(0, 20)) }), [])
  return (
    <div className="space-y-4" aria-labelledby="agi-h">
      <div className="flex items-center justify-between">
        <h3 id="agi-h" className="flex items-center gap-2 text-sm font-medium text-slate-200"><Workflow size={15} className="text-sky-300" aria-hidden /> AGI scripts</h3>
        {admin && (
          <button type="button" className="btn" onClick={() => onChange({ agi: [...scripts, { name: '', address: 'agi://127.0.0.1:4573/', number: '', fallback: { type: 'hangup', target: '' } }] })}>
            <Plus size={14} aria-hidden /> Add AGI script
          </button>
        )}
      </div>
      <p className="text-xs text-slate-500">
        A call sent to an AGI script (as Asterisk&apos;s <span className="font-mono">AGI()</span>) is answered and run by the script, command by command:
        <span className="font-mono text-sky-300"> STREAM FILE</span>, <span className="font-mono text-sky-300">GET DATA</span>, <span className="font-mono text-sky-300">RECORD FILE</span>,
        <span className="font-mono text-sky-300"> DATABASE</span>, <span className="font-mono text-sky-300">EXEC Dial</span> and the rest. Give a FastAGI server
        (<span className="font-mono">agi://host:4573/script?x=1</span>, or <span className="font-mono">hagi://domain/script</span> for the servers in its SRV records) or a program&apos;s
        file name in the AGI programs folder. When the script ends, the call goes to the number it chose (<span className="font-mono">SET EXTENSION</span>, <span className="font-mono">EXEC Goto</span>), else to the fallback; it goes to the fallback too when the script cannot be reached.
      </p>
      {scripts.length === 0 ? <p className="text-sm text-slate-500">No AGI scripts yet.</p> : (
        <ul className="space-y-2">
          {scripts.map((a, i) => (
            <li key={i} className="space-y-3 rounded-lg border border-slate-800 p-3">
              <div className="grid items-start gap-3 md:grid-cols-[1fr_2fr_0.6fr_auto]">
                <Field label="Script name" htmlFor={`agn${i}`} error={error(`system.compat.agi[${i}].name`)}>
                  <input id={`agn${i}`} className="input font-mono" value={a.name} autoComplete="off" onChange={(e) => set(i, { name: e.target.value.trim() })} />
                </Field>
                <Field label="Address" htmlFor={`aga${i}`} error={error(`system.compat.agi[${i}].address`)}
                  hint={isProgram(a.address) ? <span className="text-amber-300">A program in the AGI programs folder, run for each call.</span> : 'A FastAGI server: BitVoice connects to it for each call.'}>
                  <div className="flex gap-2">
                    <input id={`aga${i}`} className="input font-mono" value={a.address} autoComplete="off" spellCheck={false}
                      onChange={(e) => { set(i, { address: e.target.value.trim(), eagi: isProgram(e.target.value) ? a.eagi : false }); setResults((x) => ({ ...x, [i]: undefined as never })) }} />
                    {admin && <button type="button" className="btn shrink-0" disabled={testing || !a.address} onClick={() => tryIt(i)}><PlugZap size={14} aria-hidden /> Test</button>}
                  </div>
                </Field>
                <Field label="Number to dial it" htmlFor={`agnum${i}`} error={error(`system.compat.agi[${i}].number`)} hint="Optional.">
                  <input id={`agnum${i}`} className="input font-mono" inputMode="numeric" value={a.number ?? ''} onChange={(e) => set(i, { number: e.target.value.trim() })} />
                </Field>
                {admin && <button type="button" className="btn mt-6 text-rose-300" aria-label={`Remove AGI script ${a.name || i + 1}`} onClick={() => onChange({ agi: scripts.filter((_, j) => j !== i) })}><Trash2 size={14} aria-hidden /></button>}
              </div>
              {results[i] && (
                <p role="status" className={clsx('text-sm', results[i].ok ? 'text-emerald-300' : 'text-rose-300')}>{results[i].message}</p>
              )}
              <div className="grid items-start gap-3 md:grid-cols-[2fr_1.4fr_0.6fr]">
                <Field label="Arguments" htmlFor={`agarg${i}`} error={error(`system.compat.agi[${i}].args`)} hint="agi_arg_1, agi_arg_2 … in this order, as AGI(script,one,two) passes them.">
                  <ListInput id={`agarg${i}`} mono value={a.args ?? []} onChange={(v) => set(i, { args: v })} placeholder="Type one and press Enter" />
                </Field>
                <DestinationPicker label="Fallback" required value={a.fallback} onChange={(d) => d && set(i, { fallback: d })} error={error(`system.compat.agi[${i}].fallback`)} />
                <Field label="Longest run (minutes)" htmlFor={`agmax${i}`} error={error(`system.compat.agi[${i}].maxMinutes`)} hint="0 for an hour.">
                  <input id={`agmax${i}`} className="input" type="number" min={0} max={1440} value={a.maxMinutes ?? 0} onChange={(e) => set(i, { maxMinutes: Number(e.target.value) || 0 })} />
                </Field>
              </div>
              {isProgram(a.address) && (
                <label className="flex items-center gap-3 text-sm text-slate-300">
                  <Toggle checked={!!a.eagi} onChange={(v) => set(i, { eagi: v })} label={`EAGI for ${a.name || 'this script'}`} />
                  <span>EAGI: give the program the caller&apos;s audio on file descriptor 3 (8 kHz, 16-bit, Linux and macOS)</span>
                </label>
              )}
              {error(`system.compat.agi[${i}].eagi`) && <p className="field-error text-sm text-rose-300">{error(`system.compat.agi[${i}].eagi`)}</p>}
            </li>
          ))}
        </ul>
      )}
      <Field label="Keep the recordings scripts make for (days)" htmlFor="agikeep" error={error('system.compat.agiKeepDays')} hint="RECORD FILE and Record() keep their files in the sound library; older ones are removed. 0 for 30 days.">
        <input id="agikeep" className="input w-32" type="number" min={0} max={3650} value={value.agiKeepDays ?? 0} onChange={(e) => onChange({ agiKeepDays: Number(e.target.value) || 0 })} />
      </Field>
      {runs.length > 0 && (
        <div>
          <h4 className="mb-1 flex items-center gap-2 text-sm font-medium text-slate-200"><ScrollText size={14} className="text-sky-300" aria-hidden /> Last runs</h4>
          <ul className="space-y-1 text-sm" aria-label="Last AGI runs">
            {runs.map((r, i) => (
              <li key={i} className="rounded border border-slate-800 px-2 py-1">
                <span className="font-mono text-sky-300">{r.script}</span> <span className="text-slate-400">{r.from} → {r.to}, {r.commands} commands, {Math.round(r.millis)} ms:</span>{' '}
                <span className={r.error ? 'text-rose-300' : 'text-emerald-300'}>{r.decision}</span>
                {r.error && <span className="text-rose-300"> ({r.error})</span>}
                {(r.log ?? []).map((l, j) => <div key={j} className="font-mono text-xs text-slate-400">{l}</div>)}
              </li>
            ))}
          </ul>
        </div>
      )}
      <AGISounds admin={admin} />
      {admin && <AGIPrograms />}
      {admin && <AGIDatabase />}
    </div>
  )
}

/** The sound library AGI scripts play from (STREAM FILE custom/welcome), with the recordings they made. */
function AGISounds({ admin }: { admin: boolean }) {
  const { data } = useAgiSoundsQuery()
  const [upload, { isLoading }] = useUploadAgiSoundMutation()
  const [remove] = useDeleteAgiSoundMutation()
  const [folder, setFolder] = useState('custom')
  const [playing, setPlaying] = useState('')
  const items = data?.items ?? []
  const add = async (files: FileList | null) => {
    if (!files) return
    for (const f of Array.from(files)) {
      const base = f.name.replace(/\.[^.]+$/, '')
      const name = [folder.trim().replace(/^\/+|\/+$/g, ''), base].filter(Boolean).join('/')
      try {
        await upload({ name, file: f, filename: f.name }).unwrap()
        toast.success(`Added ${name}`)
      } catch (e) { toast.error(`${f.name}: ${errorOf(e).message}`) }
    }
  }
  return (
    <div>
      <h4 className="mb-1 flex items-center gap-2 text-sm font-medium text-slate-200"><FileAudio size={14} className="text-violet-300" aria-hidden /> Sounds for AGI scripts</h4>
      <p className="mb-2 text-xs text-slate-500">
        A script plays these by name, as from Asterisk&apos;s sounds folder: <span className="font-mono">custom/welcome</span>, or <span className="font-mono">en/custom/welcome</span> for one language.
        WAV, MP3 and Asterisk&apos;s <span className="font-mono">.gsm .ulaw .alaw .sln .sln16</span> files are taken. Asterisk&apos;s own <span className="font-mono">digits/0</span>–<span className="font-mono">9</span>,
        <span className="font-mono"> beep</span>, <span className="font-mono">silence/N</span> and <span className="font-mono">vm-goodbye</span> play BitVoice&apos;s prompts and tones.
      </p>
      {admin && (
        <div className="mb-2 flex flex-wrap items-end gap-2">
          <Field label="Folder" htmlFor="agifolder" hint="Optional: custom, en/custom …">
            <input id="agifolder" className="input w-40 font-mono" value={folder} onChange={(e) => setFolder(e.target.value)} />
          </Field>
          <label className={clsx('btn cursor-pointer', isLoading && 'opacity-60')}>
            <Upload size={14} aria-hidden /> Add sounds
            <input type="file" multiple className="sr-only" aria-label="Sound files for AGI scripts" accept=".wav,.mp3,.gsm,.ulaw,.ul,.alaw,.al,.sln,.sln16,.raw,audio/*"
              onChange={(e) => { add(e.target.files); e.target.value = '' }} />
          </label>
        </div>
      )}
      {items.length === 0 ? <p className="text-sm text-slate-500">No sounds yet.</p> : (
        <ul className="divide-y divide-slate-800 rounded-lg border border-slate-800 text-sm" aria-label="AGI sounds">
          {items.map((s) => (
            <li key={(s.recorded ? 'r:' : 's:') + s.name} className="flex flex-wrap items-center gap-3 px-3 py-1.5">
              <span className="font-mono text-slate-200">{s.name}</span>
              <span className={s.recorded ? 'text-amber-300' : 'text-violet-300'}>{s.recorded ? 'recorded by a script' : 'uploaded'}</span>
              <span className="text-slate-500">{s.seconds.toFixed(1)} s, {relTime(s.modified)}</span>
              <span className="ml-auto flex items-center gap-2">
                <button type="button" className="btn" onClick={() => setPlaying(playing === s.name ? '' : s.name)}>{playing === s.name ? 'Hide' : 'Listen'}</button>
                {admin && <button type="button" className="btn text-rose-300" aria-label={`Remove sound ${s.name}`} onClick={() => remove({ name: s.name, recorded: s.recorded }).unwrap().catch((e) => toast.error(errorOf(e).message))}><Trash2 size={14} aria-hidden /></button>}
              </span>
              {playing === s.name && <audio className="w-full" controls autoPlay src={`./api/agi/sounds/${s.name}${s.recorded ? '?recorded=1' : ''}`} />}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** The AGI programs folder: programs are put there on the server itself, so the screen only lists them. */
function AGIPrograms() {
  const { data } = useAgiProgramsQuery()
  return (
    <div>
      <h4 className="mb-1 flex items-center gap-2 text-sm font-medium text-slate-200"><FolderCog size={14} className="text-amber-300" aria-hidden /> AGI programs</h4>
      <p className="mb-2 text-xs text-slate-500">
        Programs run on this server with BitVoice&apos;s rights, so they are never uploaded through the web: copy them into
        <span className="font-mono text-amber-300"> {data?.folder ?? 'DATA/agi-bin'}</span> on the server and make them executable (<span className="font-mono">chmod +x</span>).
        {data && !data.eagi && <span className="text-amber-300"> EAGI is not available on Windows.</span>}
      </p>
      {(data?.items.length ?? 0) === 0 ? <p className="text-sm text-slate-500">No programs in the folder.</p> : (
        <ul className="divide-y divide-slate-800 rounded-lg border border-slate-800 text-sm" aria-label="AGI programs">
          {data!.items.map((p) => (
            <li key={p.name} className="flex items-center gap-3 px-3 py-1.5">
              <span className="font-mono text-slate-200">{p.name}</span>
              <span className={p.executable ? 'text-emerald-300' : 'text-rose-300'}>{p.executable ? 'executable' : 'not executable: chmod +x'}</span>
              <span className="ml-auto text-slate-500">{(p.bytes / 1024).toFixed(1)} KB, {relTime(p.modified)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** Asterisk's database (AstDB), as AGI scripts read and write it with DATABASE GET and PUT. */
function AGIDatabase() {
  const { data } = useAgiDBQuery()
  const [put] = usePutAgiDBMutation()
  const [del] = useDeleteAgiDBMutation()
  const [row, setRow] = useState({ family: '', key: '', value: '' })
  const items = data?.items ?? []
  const save = () => put(row).unwrap().then(() => { setRow({ family: row.family, key: '', value: '' }); toast.success('Saved') }).catch((e) => toast.error(errorOf(e).message))
  return (
    <div>
      <h4 className="mb-1 flex items-center gap-2 text-sm font-medium text-slate-200"><Database size={14} className="text-emerald-300" aria-hidden /> AGI database</h4>
      <p className="mb-2 text-xs text-slate-500">Values scripts keep with <span className="font-mono">DATABASE PUT family key value</span> (Asterisk&apos;s AstDB). They stay across restarts.</p>
      <div className="mb-2 grid items-end gap-2 md:grid-cols-[1fr_1fr_2fr_auto]">
        <Field label="Family" htmlFor="adbf"><input id="adbf" className="input font-mono" value={row.family} onChange={(e) => setRow({ ...row, family: e.target.value })} /></Field>
        <Field label="Key" htmlFor="adbk"><input id="adbk" className="input font-mono" value={row.key} onChange={(e) => setRow({ ...row, key: e.target.value })} /></Field>
        <Field label="Value" htmlFor="adbv"><input id="adbv" className="input font-mono" value={row.value} onChange={(e) => setRow({ ...row, value: e.target.value })} /></Field>
        <button type="button" className="btn" disabled={!row.family.trim() || !row.key.trim()} onClick={save}><Plus size={14} aria-hidden /> Set value</button>
      </div>
      {items.length === 0 ? <p className="text-sm text-slate-500">The database is empty.</p> : (
        <table className="w-full text-sm" aria-label="AGI database">
          <thead><tr className="text-left text-slate-400"><th className="py-1">Family</th><th>Key</th><th>Value</th><th><span className="sr-only">Remove</span></th></tr></thead>
          <tbody>
            {items.map((x) => (
              <tr key={x.family + '/' + x.key} className="border-t border-slate-800">
                <td className="py-1 font-mono text-sky-300">{x.family}</td><td className="font-mono text-slate-200">{x.key}</td><td className="font-mono text-emerald-300">{x.value}</td>
                <td className="text-right"><button type="button" className="btn text-rose-300" aria-label={`Remove ${x.family}/${x.key}`} onClick={() => del(x).unwrap().catch((e) => toast.error(errorOf(e).message))}><Trash2 size={14} aria-hidden /></button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}
