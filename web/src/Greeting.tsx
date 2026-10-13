import { Mic, RotateCcw, Square, Upload } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useResetGreetingMutation, useSaveGreetingMutation, useVoicemailQuery } from './api'
import { attempt, useIsAdmin } from './Editor'
import { toast } from 'sonner'
import { openMicrophone } from './mic'

/** The longest recording the browser keeps; menus and greetings longer than this lose callers anyway. */
const MAX_SECONDS = 120

/** Converts any audio the browser can decode to 8 kHz mono 16-bit WAV, the format BitVoice plays. */
export async function toTelephoneWav(blob: Blob): Promise<Blob> {
  const ctx = new AudioContext()
  const decoded = await ctx.decodeAudioData(await blob.arrayBuffer()).finally(() => ctx.close())
  if (decoded.duration < 0.3) throw new Error('too short')
  const frames = Math.ceil(decoded.duration * 8000)
  const off = new OfflineAudioContext(1, frames, 8000)
  const src = off.createBufferSource()
  src.buffer = decoded
  src.connect(off.destination)
  src.start()
  const pcm = (await off.startRendering()).getChannelData(0)
  const out = new DataView(new ArrayBuffer(44 + pcm.length * 2))
  const str = (o: number, s: string) => [...s].forEach((c, i) => out.setUint8(o + i, c.charCodeAt(0)))
  str(0, 'RIFF'); out.setUint32(4, 36 + pcm.length * 2, true); str(8, 'WAVEfmt ')
  out.setUint32(16, 16, true); out.setUint16(20, 1, true); out.setUint16(22, 1, true)
  out.setUint32(24, 8000, true); out.setUint32(28, 16000, true); out.setUint16(32, 2, true); out.setUint16(34, 16, true)
  str(36, 'data'); out.setUint32(40, pcm.length * 2, true)
  pcm.forEach((v, i) => out.setInt16(44 + i * 2, Math.max(-1, Math.min(1, v)) * 0x7fff, true))
  return new Blob([out], { type: 'audio/wav' })
}

/** A mailbox greeting: hear it, record one in the browser, upload a file, or go back to the beep. */
export function Greeting({ box }: { box: string }) {
  const { data } = useVoicemailQuery()
  const [save, { isLoading }] = useSaveGreetingMutation()
  const [reset] = useResetGreetingMutation()
  return (
    <AudioRecording label="Greeting" missing="No greeting recorded: callers hear a beep only." has={!!data?.greetings?.includes(box)}
      src={`./api/voicemail/${box}/greeting`} busy={isLoading} saved="Greeting saved"
      save={(wav) => save({ box, wav }).unwrap()} reset={() => reset(box).unwrap()} />
  )
}

/** A recording BitVoice plays to callers (a greeting or a menu prompt): hear it, record it in the browser,
 * upload any audio file (converted to telephone audio here), or remove it. */
export function AudioRecording({ label, missing, has, src, busy, saved, save, reset, canEdit }: {
  label: string; missing: string; has: boolean; src: string; busy?: boolean; saved: string
  save: (wav: Blob) => Promise<unknown>; reset: () => Promise<unknown>; canEdit?: boolean
}) {
  const isAdmin = useIsAdmin()
  const admin = canEdit ?? isAdmin
  const [recording, setRecording] = useState(false)
  const [seconds, setSeconds] = useState(0)
  const [version, setVersion] = useState(0)
  const rec = useRef<MediaRecorder | null>(null)
  const file = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!recording) return
    const t = setInterval(() => setSeconds((s) => s + 1), 1000)
    return () => clearInterval(t)
  }, [recording])
  // Stop at the limit, and release the microphone if the drawer closes mid-recording.
  useEffect(() => { if (recording && seconds >= MAX_SECONDS) stop() }, [recording, seconds])
  useEffect(() => () => { const r = rec.current; if (r && r.state !== 'inactive') { r.onstop = null; r.stop(); r.stream.getTracks().forEach((t) => t.stop()) } }, [])

  const upload = async (b: Blob, recorded = false) => {
    let wav: Blob
    try { wav = await toTelephoneWav(b) } catch (e) {
      toast.error((e as Error).message === 'too short' ? 'That recording is too short to use.'
        : recorded ? 'Nothing was recorded. Check that the right microphone is chosen and not muted.'
        : 'That file is not audio this browser can read.')
      return
    }
    if (!await attempt(() => save(wav), saved)) setVersion((v) => v + 1)
  }
  const start = async () => {
    let stream: MediaStream
    try { stream = await openMicrophone() } catch (e) { toast.error((e as Error).message); return }
    const chunks: Blob[] = []
    const r = new MediaRecorder(stream)
    r.ondataavailable = (e) => chunks.push(e.data)
    r.onstop = () => { stream.getTracks().forEach((t) => t.stop()); upload(new Blob(chunks, { type: r.mimeType }), true) }
    r.start()
    rec.current = r
    setSeconds(0)
    setRecording(true)
  }
  const stop = () => { if (rec.current?.state === 'recording') rec.current.stop(); setRecording(false) }

  return (
    <div className="space-y-2">
      <span className="label">{label}</span>
      {has
        ? <audio key={version} controls preload="none" className="h-9 w-full" src={`${src}?v=${version}`} aria-label={`Current ${label.toLowerCase()}`} />
        : <p className="text-sm text-amber-300">{missing}</p>}
      {admin && (
        <div className="flex flex-wrap gap-2">
          {recording
            ? <button type="button" className="btn btn-danger" onClick={stop}><Square size={14} aria-hidden /> Stop <span className="font-mono tabular-nums">{seconds}s / {MAX_SECONDS}s</span></button>
            : <button type="button" className="btn" onClick={start} disabled={busy}><Mic size={14} className="text-rose-300" aria-hidden /> Record in the browser</button>}
          <button type="button" className="btn" onClick={() => file.current?.click()} disabled={recording || busy}><Upload size={14} className="text-sky-300" aria-hidden /> Upload a file</button>
          <input ref={file} type="file" accept="audio/*" className="hidden" aria-label={`${label} file`} onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = '' }} />
          {has && <button type="button" className="btn" onClick={() => attempt(reset, `${label} removed`)}><RotateCcw size={14} className="text-amber-300" aria-hidden /> Remove</button>}
        </div>
      )}
    </div>
  )
}
