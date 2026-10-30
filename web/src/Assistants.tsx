import { Bot, Check, Copy, KeyRound, ShieldCheck, ShieldX } from 'lucide-react'
import { useMemo, useState } from 'react'
import { toast } from 'sonner'
import { errorOf, useApproveAssistantMutation, useMcpInfoQuery, useOauthRequestQuery, type Settings } from './api'
import { copyText } from './Setup'
import { Badge, ErrorBox, Page, Spinner, Toggle } from './ui'

type MCP = Settings['system']['mcp']

function Snippet({ label, text }: { label: string; text: string }) {
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium text-slate-300">{label}</span>
        <button type="button" className="btn px-2 py-0.5 text-xs" aria-label={`Copy: ${label}`}
          onClick={async () => { if (await copyText(text)) toast.success('Copied'); else toast.error('Copying is blocked here; select the text instead') }}>
          <Copy size={12} aria-hidden /> Copy
        </button>
      </div>
      <pre className="overflow-x-auto rounded-md border border-slate-800 bg-slate-950 p-2 text-xs text-sky-200">{text}</pre>
    </div>
  )
}

/** Settings: AI assistants (Claude, ChatGPT, Gemini, Cursor ...) using BitVoice over MCP. */
export function MCPSettings({ value, onChange }: { value: MCP; onChange: (v: Partial<MCP>) => void }) {
  const v = value ?? { enabled: false, act: false }
  const { data } = useMcpInfoQuery()
  const address = data?.address ?? `${location.origin}/mcp`
  return (
    <section className="card space-y-4 p-5 lg:col-span-2" aria-labelledby="mcp-h">
      <h2 id="mcp-h" className="flex items-center gap-2 font-semibold text-slate-100"><Bot size={17} className="text-violet-300" aria-hidden /> AI assistants (MCP)</h2>
      <p className="text-sm text-slate-400">
        Let an AI assistant such as Claude, ChatGPT, Gemini or Cursor use the phone system through the Model Context Protocol: ask it who called this morning,
        what a caller said, whether a line is down, or have it place a call. It sees what its token may see, and only reads unless you let it act below.
      </p>
      <div className="flex flex-wrap gap-6">
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={v.enabled} onChange={(x) => onChange({ enabled: x })} label="Let AI assistants connect" /> Let AI assistants connect</label>
        <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={v.act} onChange={(x) => onChange({ act: x })} label="Let assistants act" />
          <span>Let assistants act <span className="text-xs text-slate-500">(place calls, send texts, hang up calls; only with a token that may change things)</span></span></label>
      </div>
      {v.enabled && (
        <div className="space-y-4">
          <p className="text-sm text-slate-300">The assistants&apos; address: <span className="font-mono text-emerald-300">{address}</span></p>
          <div className="grid gap-4 lg:grid-cols-2">
            <div className="space-y-3">
              <h3 className="text-sm font-medium text-slate-200">Claude.ai, ChatGPT and others that sign in</h3>
              <p className="text-xs text-slate-400">Add a custom connector with the address above. The assistant opens BitVoice&apos;s sign-in; whoever signs in chooses whether it may only read. Its access then shows on <a className="text-sky-300 underline" href="#/users">Administrators</a>, API tokens, where it can be revoked. This needs a certificate the assistant&apos;s service trusts (Let&apos;s Encrypt, in Certificate), and the address reachable from the internet.</p>
              <h3 className="pt-2 text-sm font-medium text-slate-200">With an API token</h3>
              <p className="text-xs text-slate-400">Make one on <a className="text-sky-300 underline" href="#/users">Administrators</a>, API tokens: <span className="text-slate-300">only read</span> unless the assistant should act.</p>
              <Snippet label="Claude Code" text={`claude mcp add --transport http bitvoice ${address} \\\n  --header "Authorization: Bearer bv_..."`} />
              <Snippet label="Cursor, VS Code and others (mcp.json)" text={JSON.stringify({ mcpServers: { bitvoice: { url: address, headers: { Authorization: 'Bearer bv_...' } } } }, null, 2)} />
            </div>
            <div className="space-y-3">
              <h3 className="text-sm font-medium text-slate-200">Claude Desktop and other apps that start a program</h3>
              <p className="text-xs text-slate-400">The BitVoice program carries a bridge: <span className="font-mono">bitvoice mcp</span>. With BitVoice&apos;s own certificate, run it once without <span className="font-mono">-pin</span>: it prints the pin to add.</p>
              <Snippet label="claude_desktop_config.json" text={JSON.stringify({ mcpServers: { bitvoice: { command: 'bitvoice', args: ['mcp', '-url', address.replace(/\/mcp$/, ''), '-pin', 'SHA256-OF-THE-CERTIFICATE'], env: { BITVOICE_TOKEN: 'bv_...' } } } }, null, 2)} />
            </div>
          </div>
          {data && (
            <details className="rounded-lg border border-slate-800 p-3 text-sm">
              <summary className="cursor-pointer text-slate-200">What assistants can do ({data.tools.length} tools)</summary>
              <ul className="mt-2 space-y-1">
                {data.tools.map((t) => (
                  <li key={t.name} className="flex flex-wrap items-baseline gap-2">
                    <span className="font-mono text-xs text-sky-300">{t.name}</span>
                    {t.act ? <Badge status={v.act ? 'warn' : 'idle'}>{v.act ? 'acts' : 'acts: off'}</Badge> : <Badge status="ok">reads</Badge>}
                    <span className="text-xs text-slate-400">{t.description}</span>
                  </li>
                ))}
              </ul>
            </details>
          )}
        </div>
      )}
    </section>
  )
}

/** #/connect-assistant: an assistant asked to sign in (OAuth); the person signed in allows it, or not. */
export function ConnectAssistant() {
  const q = useMemo(() => new URLSearchParams(location.hash.split('?')[1] ?? ''), [])
  const client = q.get('client_id') ?? '', redirect = q.get('redirect_uri') ?? ''
  const { data, error, isLoading } = useOauthRequestQuery({ client_id: client, redirect_uri: redirect }, { skip: !client || !redirect })
  const [approve, { isLoading: busy }] = useApproveAssistantMutation()
  const [readOnly, setReadOnly] = useState(true)
  const answer = async (allow: boolean) => {
    try {
      const r = await approve({ client_id: client, redirect_uri: redirect, state: q.get('state') ?? '', code_challenge: q.get('code_challenge') ?? '', allow, readOnly }).unwrap()
      location.assign(r.redirect)
    } catch (e) { toast.error(errorOf(e).message) }
  }
  const admin = data?.role === 'admin'
  return (
    <Page icon={Bot} title="Connect an AI assistant" accent="from-violet-400 to-sky-400" intro="An assistant asks to use BitVoice as you.">
      {(!client || !redirect) && <ErrorBox message="This page is opened by an assistant's sign-in; this link is incomplete." />}
      {isLoading && <Spinner />}
      {error && <ErrorBox message={errorOf(error).message} />}
      {data && (
        <section className="card max-w-xl space-y-4 p-5">
          <p className="text-base text-slate-100"><span className="font-semibold text-violet-200">{data.client}</span> wants to use this phone system as you.</p>
          <p className="text-sm text-slate-400">After you allow it, you return to <span className="font-mono text-slate-200">{data.returnsTo}</span>. It may:</p>
          <ul className="space-y-1 text-sm">
            <li className="flex items-center gap-2 text-slate-300"><Check size={14} className="text-emerald-300" aria-hidden /> read calls, call history, voicemail, transcripts, queues, lines and health</li>
            {admin && data.act && !readOnly
              ? <li className="flex items-center gap-2 text-amber-200"><KeyRound size={14} aria-hidden /> place calls, send texts and hang up calls</li>
              : <li className="flex items-center gap-2 text-slate-500"><ShieldCheck size={14} aria-hidden /> change nothing</li>}
          </ul>
          {admin && data.act && (
            <label className="flex items-center gap-3 text-sm text-slate-300"><Toggle checked={readOnly} onChange={setReadOnly} label="Only let it read" /> Only let it read</label>
          )}
          {!admin && <p className="text-xs text-slate-500">You are a viewer, so it can only read.</p>}
          {admin && !data.act && <p className="text-xs text-slate-500">Acting is off in Settings, AI assistants: it can only read.</p>}
          <p className="text-xs text-slate-500">Its access lasts until it is revoked on Administrators, API tokens (it renews itself for up to 30 days of not being used).</p>
          <div className="flex gap-2">
            <button type="button" className="btn btn-primary" disabled={busy} onClick={() => answer(true)}><ShieldCheck size={15} aria-hidden /> Allow</button>
            <button type="button" className="btn" disabled={busy} onClick={() => answer(false)}><ShieldX size={15} aria-hidden /> Do not allow</button>
          </div>
        </section>
      )}
    </Page>
  )
}
