/** Opens the microphone, or throws an Error whose message tells the person what to do about it. */
export async function openMicrophone(audio: MediaTrackConstraints | boolean = true): Promise<MediaStream> {
  if (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia) {
    throw new Error('Browsers only allow the microphone on https:// pages or on this computer. Open BitVoice over https to use it.')
  }
  try {
    return await navigator.mediaDevices.getUserMedia({ audio })
  } catch (e) {
    throw new Error(micProblem(e))
  }
}

/** Says in plain words why getUserMedia failed. */
export function micProblem(e: unknown): string {
  switch ((e as DOMException)?.name) {
    case 'NotAllowedError':
    case 'SecurityError':
      return 'The browser was not allowed to use the microphone. Allow it in the address bar and try again.'
    case 'NotFoundError':
    case 'OverconstrainedError':
      return 'No microphone was found. Plug one in or choose one in the system sound settings.'
    case 'NotReadableError':
    case 'AbortError':
      return 'The microphone is in use by another program or could not be started.'
    default:
      return (e as Error)?.message || 'The microphone could not be opened.'
  }
}
