import { clsx } from 'clsx'
import { AudioLines, Languages, Music, Play, Plus, Shuffle, Sparkles, Trash2, Upload, Radio } from 'lucide-react'
import { useRef, useState } from 'react'
import { toast } from 'sonner'
import {
  useAddMusicMutation, useDeleteLangPromptMutation, useDeleteMusicMutation, useGeneratePromptsMutation, useMusicQuery,
  usePromptsQuery, useSaveLangPromptMutation, useSaveSettingsMutation, useSettingsQuery, type MusicClass, type Settings, type ApiErrorBody, useMusicStreamsQuery,
} from './api'
import { attempt, useIsAdmin } from './Editor'
import { AudioRecording, toTelephoneWav } from './Greeting'
import { Empty, Field, Page, problemFor, Spinner, Toggle } from './ui'

/** Sounds: the prompts callers hear, in each language, and the hold music. */
export function Sounds() {
  return (
    <Page icon={AudioLines} title="Sounds" accent="from-violet-400 to-teal-400"
      intro="What callers hear: spoken prompts in each language (directory, dial-in, wake-up calls, queue announcements) and the hold music played on hold, when parked and while waiting in a queue.">
      <PromptsSection />
      <MusicSection />
    </Page>
  )
}

function useSaveSystem() {
  const { data } = useSettingsQuery()
  const [save] = useSaveSettingsMutation()
  return { data, saveSystem: (v: Partial<Settings['system']>, done: string) => data && attempt(() => save({ ...data, system: { ...data.system, ...v } }).unwrap(), done) }
}

function PromptsSection() {
  const admin = useIsAdmin()
  const { data } = usePromptsQuery()
  const { data: settings, saveSystem } = useSaveSystem()
  const [lang, setLang] = useState('')
  const [open, setOpen] = useState('')
  const [newLang, setNewLang] = useState('')
  const [overwrite, setOverwrite] = useState(false)
  const [generate, { isLoading: generating }] = useGeneratePromptsMutation()
  const [savePrompt] = useSaveLangPromptMutation()
  const [deletePrompt] = useDeleteLangPromptMutation()
  if (!data || !settings) return <Spinner />
  const cur = lang || data.language || 'en'
  const info = data.languages.find((l) => l.code === cur)
  const done = new Set(data.recorded[cur] ?? [])
  const groups = [...new Set(data.prompts.map((p) => p.group))]
  const added = settings.system.promptLanguages ?? []
  const run = async () => {
    const r = await generate({ lang: cur, overwrite }).unwrap().catch((e) => { toast.error(e?.data?.error ?? 'The speech service did not answer'); return null })
    if (!r) return
    if (r.failed?.length) toast.warning(`Made ${r.made} prompts; ${r.failed.length} failed: ${r.failed.join(', ')}`)
    else toast.success(`Made ${r.made} prompts${r.skipped ? `, kept ${r.skipped} already recorded` : ''}`)
  }
  return (
    <section className="card mb-6 space-y-4 p-5" aria-labelledby="prompts-h">
      <div className="flex flex-wrap items-center gap-3">
        <Languages size={18} className="text-violet-300" aria-hidden />
        <h2 id="prompts-h" className="font-semibold text-slate-100">Prompts</h2>
        <span className="text-sm text-slate-400"><span className="text-emerald-300">{done.size}</span> of {data.prompts.length} recorded in {info?.name ?? cur}</span>
        {admin && (
          <label className="ml-auto flex items-center gap-2 text-sm text-slate-300">Callers hear
            <select className="input w-44" aria-label="Default prompt language" value={settings.system.language}
              onChange={(e) => saveSystem({ language: e.target.value }, 'Prompt language saved')}>
              {data.languages.map((l) => <option key={l.code} value={l.code}>{l.name}</option>)}
            </select>
          </label>
        )}
      </div>
      <div className="flex flex-wrap gap-1" role="tablist" aria-label="Prompt language">
        {data.languages.map((l) => {
          const n = data.recorded[l.code]?.length ?? 0
          return (
            <button key={l.code} type="button" role="tab" aria-selected={l.code === cur} onClick={() => setLang(l.code)}
              className={clsx('rounded-md border px-3 py-1 text-sm', l.code === cur ? 'border-violet-400 bg-violet-500/15 text-violet-200' : 'border-slate-700 text-slate-300 hover:border-slate-500')}>
              {l.name} <span className={clsx('font-mono text-xs', n === data.prompts.length ? 'text-emerald-300' : n ? 'text-amber-300' : 'text-slate-500')}>{n}/{data.prompts.length}</span>
            </button>
          )
        })}
      </div>
      <p className="text-sm text-slate-400">
        A prompt with no recording is replaced by a short tone, so features work before any are recorded. A missing prompt in one language is played in the
        default language, then in English.
      </p>
      {admin && (
        <div className="flex flex-wrap items-end gap-3">
          {info?.shipped && (data.speech
            ? <>
                <button type="button" className="btn btn-primary" onClick={run} disabled={generating}>
                  <Sparkles size={14} aria-hidden /> {generating ? 'Making prompts…' : `Make the ${info.name} prompts with the speech service`}
                </button>
                <label className="flex items-center gap-2 pb-2 text-sm text-slate-300"><Toggle checked={overwrite} onChange={setOverwrite} label="Replace recorded prompts" /> Replace ones already recorded</label>
              </>
            : <p className="text-sm text-amber-300">Set up a speech model in Settings, AI to make prompts from their words, or record and upload them below.</p>)}
          <Field label="Add a language" htmlFor="nl" hint="A code such as sv, ja or pt-BR. Its prompts are recorded or uploaded.">
            <span className="flex gap-2">
              <input id="nl" className="input w-32 font-mono" value={newLang} onChange={(e) => setNewLang(e.target.value.trim())} placeholder="sv" />
              <button type="button" className="btn" disabled={!newLang}
                onClick={async () => { if (!await saveSystem({ promptLanguages: [...added, newLang] }, `Language ${newLang} added`)) { setLang(newLang); setNewLang('') } }}>
                <Plus size={14} aria-hidden /> Add
              </button>
            </span>
          </Field>
          {added.includes(cur) && (
            <button type="button" className="btn btn-danger" onClick={async () => { if (!await saveSystem({ promptLanguages: added.filter((l) => l !== cur) }, `Language ${cur} removed`)) setLang('') }}>
              <Trash2 size={14} aria-hidden /> Remove {cur}
            </button>
          )}
        </div>
      )}
      {groups.map((g) => (
        <div key={g}>
          <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-slate-400">{g}</h3>
          <ul className="divide-y divide-slate-800 rounded-md border border-slate-800">
            {data.prompts.filter((p) => p.group === g).map((p) => {
              const has = done.has(p.key)
              const words = p.text[cur] ?? p.text.en
              return (
                <li key={p.key} className="p-2">
                  <button type="button" className="flex w-full items-center gap-3 text-left" aria-expanded={open === p.key} onClick={() => setOpen(open === p.key ? '' : p.key)}>
                    <span className={clsx('h-2 w-2 shrink-0 rounded-full', has ? 'bg-emerald-400' : 'bg-amber-400')} aria-hidden />
                    <span className="w-36 shrink-0 font-mono text-xs text-sky-300">{p.key}</span>
                    <span className={clsx('text-sm', p.text[cur] ? 'text-slate-200' : 'italic text-slate-500')}>{words}</span>
                    <span className={clsx('ml-auto shrink-0 text-xs', has ? 'text-emerald-300' : 'text-amber-300')}>{has ? 'recorded' : 'tone'}</span>
                  </button>
                  {open === p.key && (
                    <div className="mt-2 pl-5">
                      <AudioRecording label={`${p.key} (${info?.name ?? cur})`} missing="Not recorded: callers hear a tone here." has={has}
                        src={`./api/prompts/${cur}/${p.key}`} saved="Prompt saved"
                        save={(wav) => savePrompt({ lang: cur, key: p.key, wav }).unwrap()} reset={() => deletePrompt({ lang: cur, key: p.key }).unwrap()} />
                    </div>
                  )}
                </li>
              )
            })}
          </ul>
        </div>
      ))}
    </section>
  )
}

/** A track name from a file name: letters, digits, - and _ only. */
function trackName(file: string) {
  return file.replace(/\.[^.]*$/, '').replace(/[^A-Za-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40) || 'track'
}

function MusicSection() {
  const admin = useIsAdmin()
  const { data: settings, saveSystem } = useSaveSystem()
  const { data: tracks } = useMusicQuery()
  const [name, setName] = useState('')
  if (!settings) return null
  const classes = settings.system.music ?? []
  const add = async () => {
    const id = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 30) || 'music'
    if (classes.some((c) => c.id === id)) { toast.error(`There is already hold music called ${id}`); return }
    if (!await saveSystem({ music: [...classes, { id, name: name.trim(), shuffle: false }], holdMusic: settings.system.holdMusic || id }, `Hold music ${name} added`)) setName('')
  }
  return (
    <section className="card space-y-4 p-5" aria-labelledby="music-h">
      <div className="flex flex-wrap items-center gap-3">
        <Music size={18} className="text-teal-300" aria-hidden />
        <h2 id="music-h" className="font-semibold text-slate-100">Hold music</h2>
        <span className="text-sm text-slate-400">
          Callers hear {settings.system.holdMusic
            ? <span className="text-teal-300">{classes.find((c) => c.id === settings.system.holdMusic)?.name}</span>
            : <span className="text-amber-300">the hold tone</span>} unless a queue or person chooses other music.
        </span>
      </div>
      {admin && (
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Default for everyone" htmlFor="hm">
            <select id="hm" className="input w-56" value={settings.system.holdMusic ?? ''} onChange={(e) => saveSystem({ holdMusic: e.target.value }, 'Hold music saved')}>
              <option value="">The hold tone</option>
              {classes.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </Field>
          <Field label="New hold music" htmlFor="mn" hint="For example Calm, Jazz or Sales offers.">
            <span className="flex gap-2">
              <input id="mn" className="input w-48" value={name} onChange={(e) => setName(e.target.value)} placeholder="Calm" />
              <button type="button" className="btn" disabled={!name.trim()} onClick={add}><Plus size={14} aria-hidden /> Add</button>
            </span>
          </Field>
        </div>
      )}
      {!classes.length ? <Empty icon={Music} title="No hold music yet">Add some, then upload tracks: callers hear them in turn, from the start or shuffled.</Empty>
        : classes.map((c) => <MusicClassCard key={c.id} c={c} tracks={tracks?.[c.id] ?? []} classes={classes} saveSystem={saveSystem} isDefault={settings.system.holdMusic === c.id} />)}
      <p className="text-xs text-slate-500">Only play music you have the rights to play to callers: most recorded music needs a licence for on-hold use.</p>
    </section>
  )
}

function MusicClassCard({ c, tracks, classes, saveSystem, isDefault }: {
  c: MusicClass; tracks: { name: string; seconds: number }[]; classes: MusicClass[]; isDefault: boolean
  saveSystem: (v: Partial<Settings['system']>, done: string) => Promise<unknown> | undefined
}) {
  const admin = useIsAdmin()
  const file = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState('')
  const [playing, setPlaying] = useState('')
  const [addMusic] = useAddMusicMutation()
  const [deleteMusic] = useDeleteMusicMutation()
  const upload = async (files: FileList) => {
    for (const f of [...files]) {
      setBusy(f.name)
      try {
        // The browser converts what it can; an MP3 it cannot (a very long one, say) goes as it is, and
        // BitVoice decodes it itself.
        const wav = await toTelephoneWav(f).catch((e) => { if (/\.mp3$/i.test(f.name) || f.type === 'audio/mpeg') return f; throw e })
        await attempt(() => addMusic({ cls: c.id, name: trackName(f.name), wav }).unwrap(), `${f.name} added`)
      } catch { toast.error(`${f.name} is not audio this browser can read`) }
    }
    setBusy('')
  }
  const total = tracks.reduce((s, t) => s + t.seconds, 0)
  const [stream, setStream] = useState(c.stream ?? '')
  const [streamErr, setStreamErr] = useState('')
  const { data: streams } = useMusicStreamsQuery(undefined, { pollingInterval: 10000, skip: !c.stream })
  const st = streams?.find((s) => s.class === c.id)
  return (
    <div className="rounded-md border border-slate-800 p-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-semibold text-slate-100">{c.name}</span>
        <span className="font-mono text-xs text-slate-500">{c.id}</span>
        {isDefault && <span className="rounded bg-teal-500/15 px-2 py-0.5 text-xs text-teal-300">default</span>}
        <span className="text-xs text-slate-400">{tracks.length} tracks, {Math.round(total / 60)} min</span>
        {admin && (
          <span className="ml-auto flex items-center gap-2">
            <label className="flex items-center gap-2 text-sm text-slate-300">
              <Toggle checked={c.shuffle} label={`Shuffle ${c.name}`}
                onChange={(v) => saveSystem({ music: classes.map((x) => x.id === c.id ? { ...x, shuffle: v } : x) }, v ? 'Shuffled' : 'In order')} />
              <Shuffle size={13} className="text-violet-300" aria-hidden /> Shuffle
            </label>
            <button type="button" className="btn" disabled={!!busy} onClick={() => file.current?.click()}><Upload size={14} className="text-sky-300" aria-hidden /> {busy ? `Adding ${busy}…` : 'Upload tracks'}</button>
            <input ref={file} type="file" accept="audio/*" multiple className="hidden" aria-label={`${c.name} tracks`}
              onChange={(e) => { if (e.target.files?.length) upload(e.target.files); e.target.value = '' }} />
            <button type="button" className="btn btn-danger" aria-label={`Remove ${c.name}`}
              onClick={() => saveSystem({ music: classes.filter((x) => x.id !== c.id) }, `${c.name} removed`)?.then(async (err) => {
                if (!err) for (const t of tracks) await deleteMusic({ cls: c.id, name: t.name })
              })}>
              <Trash2 size={14} aria-hidden />
            </button>
          </span>
        )}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-2 text-sm">
        <Radio size={14} className="text-amber-300" aria-hidden />
        <label htmlFor={`st-${c.id}`} className="text-slate-400">Internet radio</label>
        <input id={`st-${c.id}`} className="input max-w-md flex-1 font-mono text-xs" disabled={!admin} value={stream} placeholder="https://radio.example.com/stream.mp3 (optional)" onChange={(e) => setStream(e.target.value.trim())} />
        {admin && stream !== (c.stream ?? '') && <button type="button" className="btn" onClick={async () => {
          const e = (await saveSystem({ music: classes.map((x) => x.id === c.id ? { ...x, stream } : x) }, stream ? 'Stream set' : 'Stream removed')) as ApiErrorBody | null | undefined
          setStreamErr(problemFor(e?.problems, `system.music.${c.id}.stream`) ?? '')
        }}>Set stream</button>}
        {c.stream && (st ? (st.live ? <span className="text-emerald-300">playing to {st.listening} on hold</span> : <span className="text-amber-300">{st.error ? `not reachable: ${st.error}` : 'connecting'}</span>)
          : <span className="text-slate-500">connects when someone is put on hold</span>)}
      </div>
      {streamErr && <p className="mt-1 text-sm text-rose-300">{streamErr}</p>}
      {c.stream && <p className="mt-1 text-xs text-slate-500">The stream plays instead of the tracks; the tracks (or the hold tone) play while it cannot be reached. Music on hold needs a licence for playing to callers: use a stream you are allowed to.</p>}
      {tracks.length > 0 && (
        <ul className="mt-2 space-y-1">
          {tracks.map((t) => (
            <li key={t.name} className="flex items-center gap-2 text-sm">
              <button type="button" className="btn px-2 py-1" aria-label={`Play ${t.name}`} onClick={() => setPlaying(playing === t.name ? '' : t.name)}><Play size={12} className="text-emerald-300" aria-hidden /></button>
              <span className="font-mono text-slate-200">{t.name}</span>
              <span className="text-xs text-slate-500">{Math.floor(t.seconds / 60)}:{String(t.seconds % 60).padStart(2, '0')}</span>
              {playing === t.name && <audio autoPlay controls className="h-8" src={`./api/music/${c.id}/${t.name}`} aria-label={`${t.name} audio`} />}
              {admin && <button type="button" className="btn ml-auto px-2 py-1" aria-label={`Delete ${t.name}`} onClick={() => attempt(() => deleteMusic({ cls: c.id, name: t.name }).unwrap(), `${t.name} deleted`)}><Trash2 size={12} className="text-rose-300" aria-hidden /></button>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
