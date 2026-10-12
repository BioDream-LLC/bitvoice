import { RotateCcw, TriangleAlert } from 'lucide-react'
import { Component, type ErrorInfo, type ReactNode } from 'react'

/** Catches a page that fails to draw, so one broken screen never blanks the whole interface. */
export class Crash extends Component<{ children: ReactNode; resetKey: string }, { error: Error | null }> {
  state = { error: null as Error | null }

  static getDerivedStateFromError(error: Error) { return { error } }

  componentDidCatch(error: Error, info: ErrorInfo) { console.error('BitVoice page error', error, info.componentStack) }

  componentDidUpdate(prev: { resetKey: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null })
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <div role="alert" className="m-8 max-w-2xl rounded-xl border border-rose-800/60 bg-rose-950/40 p-6">
        <h1 className="flex items-center gap-2 text-lg font-semibold text-rose-200"><TriangleAlert size={20} aria-hidden /> This page could not be shown</h1>
        <p className="mt-2 text-sm text-slate-300">The rest of BitVoice still works: pick another page on the left, or try this one again.
          Calls are not affected.</p>
        <pre className="mt-3 overflow-auto rounded bg-slate-950/70 p-3 font-mono text-xs text-rose-300">{this.state.error.message}</pre>
        <button type="button" className="btn mt-4" onClick={() => this.setState({ error: null })}><RotateCcw size={14} className="text-sky-300" aria-hidden /> Try again</button>
      </div>
    )
  }
}
