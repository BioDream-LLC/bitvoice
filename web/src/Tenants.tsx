import { clsx } from 'clsx'
import { Building2, ExternalLink, PauseCircle, Plus } from 'lucide-react'
import { useState } from 'react'
import { useCreateTenantMutation, useDeleteTenantMutation, useOpenTenantMutation, useTenantsQuery, useUpdateTenantMutation, type ApiErrorBody, type TenantView } from './api'
import { attempt, DeleteButton, Drawer } from './Editor'
import { Empty, Field, Page, Toggle, problemFor } from './ui'

const blank = { id: '', name: '', domain: '', suspended: false, note: '', limits: { extensions: 0, calls: 0, storageMB: 0, aiPerDay: 0 } }

function mb(bytes: number) {
  return bytes < 1 << 20 ? `${Math.round(bytes / 1024)} KB` : `${(bytes / (1 << 20)).toFixed(1)} MB`
}

/** Organisations hosted on this BitVoice: each a phone system of its own, sharing this server. */
export function Tenants() {
  const { data } = useTenantsQuery(undefined, { pollingInterval: 5000 })
  const [edit, setEdit] = useState<TenantView | null>(null)
  const [adding, setAdding] = useState(false)
  const [open] = useOpenTenantMutation()
  const go = async (id: string) => {
    try { const r = await open(id).unwrap(); location.href = r.url } catch { /* the toast in attempt is not needed: the page stays */ }
  }
  return (
    <Page icon={Building2} title="Organisations" accent="from-violet-400 to-sky-400"
      intro="Host other organisations on this server. Each one is a phone system of its own: its own extensions, numbers, trunks, administrators, recordings and call history, which no other organisation can see. They share only this server's SIP port and media ports."
      actions={<button className="btn btn-primary" onClick={() => setAdding(true)}><Plus size={15} aria-hidden /> Add organisation</button>}>
      {!data?.items.length ? <Empty icon={Building2} title="No hosted organisations">This server runs one phone system. Add an organisation to host another one beside it.</Empty> : (
        <div className="grid gap-3 lg:grid-cols-2">
          {data.items.map((t) => (
            <div key={t.id} className={clsx('card p-4', t.suspended && 'border-amber-900/70')}>
              <div className="flex items-center gap-2">
                <Building2 size={16} className="text-violet-300" aria-hidden />
                <button className="font-semibold text-slate-100 hover:underline" onClick={() => setEdit(t)}>{t.name}</button>
                <span className="font-mono text-xs text-slate-500">{t.id}</span>
                {t.suspended && <span className="flex items-center gap-1 rounded-full bg-amber-950 px-2 py-0.5 text-xs text-amber-300"><PauseCircle size={12} aria-hidden /> suspended</span>}
                {!t.running && <span className="rounded-full bg-rose-950 px-2 py-0.5 text-xs text-rose-300">not running</span>}
                <button className="btn btn-ghost ml-auto text-sm" onClick={() => go(t.id)}><ExternalLink size={14} aria-hidden /> Open</button>
              </div>
              <dl className="mt-3 grid grid-cols-4 gap-2 text-center text-xs">
                {[['Extensions', t.extensions, t.limits.extensions, 'text-sky-300'], ['Phones', t.phones, 0, 'text-emerald-300'], ['Calls now', t.calls, t.limits.calls, 'text-teal-300'], ['Administrators', t.admins, 0, 'text-fuchsia-300']].map(([l, v, max, c]) => (
                  <div key={l as string} className="rounded-lg bg-slate-900 p-2"><dt className="text-slate-500">{l}</dt><dd className={clsx('font-mono text-lg', c as string)}>{v}{max ? <span className="text-xs text-slate-500"> / {max}</span> : null}</dd></div>
                ))}
              </dl>
              <div className="mt-3 flex flex-wrap gap-3 text-xs text-slate-400">
                <span>Web: <a className="font-mono text-sky-300 underline" href={t.url}>{t.domain ? `https://${t.domain}/` : t.url}</a></span>
                <span>Phones sign in as <span className="font-mono text-amber-300">{t.domain ? `<extension>@${t.domain}` : t.sipUser}</span></span>
                <span>Storage {mb(t.storageBytes)}{t.limits.storageMB ? ` of ${t.limits.storageMB} MB` : ''}</span>
              </div>
            </div>
          ))}
        </div>
      )}
      <section className="card mt-6 space-y-2 p-4 text-sm text-slate-400" aria-labelledby="how-h">
        <h2 id="how-h" className="font-semibold text-slate-200">How phones and providers reach an organisation</h2>
        <p>A phone signing in to the organisation's own domain belongs to it. Without a domain, it signs in with the organisation's short name in front of the extension: <span className="font-mono text-amber-300">acme.100</span>. A call from a provider goes to the organisation whose trunk it comes from and whose incoming route has the number dialled.</p>
        <p>Emergency calls always go through: a suspended organisation, or one at its call limit, can still call 911, and be called back.</p>
      </section>
      {adding && <TenantEditor onClose={() => setAdding(false)} />}
      {edit && <TenantEditor t={edit} onClose={() => setEdit(null)} />}
    </Page>
  )
}

function TenantEditor({ t: initial, onClose }: { t?: TenantView; onClose: () => void }) {
  const isNew = !initial
  const [t, setT] = useState(() => {
    const { id, name, domain, suspended, note, limits } = { ...blank, ...(initial ?? {}) }
    return { id, name, domain, suspended, note, limits } // only the organisation's own fields go back to the server
  })
  const [admin, setAdmin] = useState({ username: '', name: '', password: '' })
  const [country, setCountry] = useState('us')
  const [err, setErr] = useState<ApiErrorBody | null>(null)
  const [create] = useCreateTenantMutation()
  const [update] = useUpdateTenantMutation()
  const [remove] = useDeleteTenantMutation()
  const p = (f: string) => problemFor(err?.problems, f)
  const lim = (k: keyof typeof blank.limits, v: string) => setT((t0) => ({ ...t0, limits: { ...t0.limits, [k]: Number(v) || 0 } }))
  const save = async () => {
    const e = await attempt(() => (isNew ? create({ ...t, admin, country }) : update({ ...t })).unwrap(), `${t.name || 'Organisation'} saved`)
    setErr(e)
    if (!e) onClose()
  }
  return (
    <Drawer open onClose={onClose} title={isNew ? 'New organisation' : t.name} icon={<Building2 className="text-violet-300" aria-hidden />} onSave={save} error={err}
      footer={!isNew && <DeleteButton what={t.name} onDelete={async () => { if (!await attempt(() => remove(t.id).unwrap(), `${t.name} removed`)) onClose() }} />}>
      <div className="space-y-5">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name" htmlFor="tn" error={p('name')}><input id="tn" className="input" value={t.name} onChange={(e) => setT({ ...t, name: e.target.value })} /></Field>
          <Field label="Short name" htmlFor="tid" error={p('id')} hint={isNew ? 'Lowercase letters, digits and dashes. In its web address (/t/acme/) and phone sign-ins (acme.100). Cannot change later.' : 'Cannot change.'}>
            <input id="tid" className="input font-mono text-violet-300" disabled={!isNew} value={t.id} onChange={(e) => setT({ ...t, id: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '') })} />
          </Field>
        </div>
        <Field label="Own domain" htmlFor="tdom" error={p('domain')} hint="Optional. Point it at this server: phones register to it and its administrators open it in a browser.">
          <input id="tdom" className="input font-mono text-sky-300" placeholder="pbx.acme.example" value={t.domain} onChange={(e) => setT({ ...t, domain: e.target.value.trim().toLowerCase() })} />
        </Field>
        <fieldset className="space-y-3 rounded-lg border border-slate-800 p-3">
          <legend className="px-1 text-sm font-medium text-amber-300">Limits</legend>
          <p className="text-xs text-slate-500">0 is no limit. Emergency calls are never refused.</p>
          <div className="grid gap-3 sm:grid-cols-4">
            <Field label="Extensions" htmlFor="tle" error={p('limits.extensions')}><input id="tle" type="number" min={0} className="input" value={t.limits.extensions} onChange={(e) => lim('extensions', e.target.value)} /></Field>
            <Field label="Calls at once" htmlFor="tlc" error={p('limits.calls')}><input id="tlc" type="number" min={0} className="input" value={t.limits.calls} onChange={(e) => lim('calls', e.target.value)} /></Field>
            <Field label="Storage (MB)" htmlFor="tls" error={p('limits.storageMB')}><input id="tls" type="number" min={0} className="input" value={t.limits.storageMB} onChange={(e) => lim('storageMB', e.target.value)} /></Field>
            <Field label="AI requests a day" htmlFor="tla" error={p('limits.aiPerDay')}><input id="tla" type="number" min={0} className="input" value={t.limits.aiPerDay} onChange={(e) => lim('aiPerDay', e.target.value)} /></Field>
          </div>
        </fieldset>
        {isNew ? (
          <fieldset className="space-y-3 rounded-lg border border-slate-800 p-3">
            <legend className="px-1 text-sm font-medium text-fuchsia-300">Its first administrator</legend>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="Username" htmlFor="tau" error={p('admin.username')}><input id="tau" className="input" autoComplete="off" value={admin.username} onChange={(e) => setAdmin({ ...admin, username: e.target.value })} /></Field>
              <Field label="Their name" htmlFor="tan"><input id="tan" className="input" value={admin.name} onChange={(e) => setAdmin({ ...admin, name: e.target.value })} /></Field>
            </div>
            <Field label="Password" htmlFor="tap" error={p('admin.password')} hint="At least 12 characters. They can change it after signing in.">
              <input id="tap" type="password" autoComplete="new-password" className="input" value={admin.password} onChange={(e) => setAdmin({ ...admin, password: e.target.value })} />
            </Field>
            <Field label="Country" htmlFor="tco" hint="Sets its emergency numbers and tones."><input id="tco" className="input w-20 font-mono uppercase" maxLength={2} value={country} onChange={(e) => setCountry(e.target.value.toLowerCase())} /></Field>
          </fieldset>
        ) : (
          <label className="flex items-center gap-3 text-sm text-slate-300">
            <Toggle checked={t.suspended} onChange={(v) => setT({ ...t, suspended: v })} label="Suspended" />
            Suspended <span className="text-xs text-slate-500">(nobody signs in and only emergency calls go through; nothing is deleted)</span>
          </label>
        )}
        <Field label="Note" htmlFor="tno"><textarea id="tno" className="input" rows={2} value={t.note} onChange={(e) => setT({ ...t, note: e.target.value })} /></Field>
      </div>
    </Drawer>
  )
}
