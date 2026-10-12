import { javascript } from '@codemirror/lang-javascript'
import { StreamLanguage } from '@codemirror/language'
import { lua } from '@codemirror/legacy-modes/mode/lua'
import { EditorState } from '@codemirror/state'
import { oneDark } from '@codemirror/theme-one-dark'
import { EditorView } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useEffect, useRef } from 'react'

/** A code editor (CodeMirror 6) for routing scripts, with JavaScript or Lua highlighting. */
export function CodeEditor({ value, onChange, language, label, readOnly }: {
  value: string; onChange: (v: string) => void; language: 'js' | 'lua'; label: string; readOnly?: boolean
}) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const change = useRef(onChange)
  change.current = onChange

  useEffect(() => {
    if (!host.current) return
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          basicSetup, oneDark,
          language === 'lua' ? StreamLanguage.define(lua) : javascript(),
          EditorState.readOnly.of(!!readOnly),
          EditorView.contentAttributes.of({ 'aria-label': label }),
          EditorView.updateListener.of((u) => { if (u.docChanged) change.current(u.state.doc.toString()) }),
          EditorView.theme({ '&': { fontSize: '13px', borderRadius: '0.5rem' }, '.cm-scroller': { minHeight: '16rem', maxHeight: '28rem' } }),
        ],
      }),
    })
    view.current = v
    return () => v.destroy()
  }, [language, readOnly]) // eslint-disable-line react-hooks/exhaustive-deps

  // Outside changes (a template picked) replace the text.
  useEffect(() => {
    const v = view.current
    if (v && v.state.doc.toString() !== value) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } })
  }, [value])

  return <div ref={host} className="overflow-hidden rounded-lg border border-slate-800" />
}
