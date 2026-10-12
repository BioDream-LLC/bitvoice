import { useEffect, useRef, useState } from 'react'

/** An editable copy of server data. It follows the server until the person changes something; after that
 * a newer server copy does not overwrite their edits but marks the draft stale, so the page can say so. */
export function useDraft<T>(server: T | undefined) {
  const [draft, setDraftRaw] = useState<T | undefined>(server)
  const dirty = useRef(false)
  const [stale, setStale] = useState(false)
  const base = useRef<T | undefined>(server)
  // Compared by value: a caller may build its copy afresh each render (a default for a setting not
  // saved yet), and only a real change on the server may mark the draft stale.
  const key = server === undefined ? undefined : JSON.stringify(server)
  const baseKey = useRef(key)
  useEffect(() => {
    if (server === undefined || key === baseKey.current) return
    base.current = server
    baseKey.current = key
    if (!dirty.current) setDraftRaw(server)
    else setStale(true)
  }, [key]) // eslint-disable-line react-hooks/exhaustive-deps
  const setDraft = (f: (d: T) => T) => { dirty.current = true; setDraftRaw((d) => (d === undefined ? d : f(d))) }
  /** Take the server's copy, dropping unsaved edits (after a save, or when the person chooses to). */
  const reset = (to?: T) => { dirty.current = false; setStale(false); const v = to ?? base.current; base.current = v; baseKey.current = v === undefined ? undefined : JSON.stringify(v); setDraftRaw(v) }
  return { draft, setDraft, reset, stale, dirty: dirty.current }
}
