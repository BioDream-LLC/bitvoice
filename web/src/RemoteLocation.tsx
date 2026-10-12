import { MapPin } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import type { Address } from './api'
import { Field } from './ui'

const empty: Address = { country: 'US', state: '', city: '', street: '', number: '', postalCode: '', detail: '' }

/**
 * On the phone page: when the phone is away from its site (a remote worker), ask where the person is,
 * for emergency calls, and let them correct it later. A confirmation older than 30 days is asked again.
 */
export function RemoteLocation({ phoneId }: { phoneId: string }) {
  const [info, setInfo] = useState<{ remote: boolean; address?: Address; confirmed?: string } | null>(null)
  const [open, setOpen] = useState(false)
  const [a, setA] = useState<Address>(empty)
  const [problems, setProblems] = useState<Record<string, string>>({})
  useEffect(() => {
    fetch(`./api/phone/${phoneId}/location`).then((r) => r.ok ? r.json() : null).then((d) => {
      if (!d) return
      setInfo(d)
      if (d.address) setA({ ...empty, ...d.address })
      const stale = !d.confirmed || Date.now() - Date.parse(d.confirmed) > 30 * 864e5
      if (d.remote && stale) setOpen(true)
    }).catch(() => {})
  }, [phoneId])
  if (!info?.remote) return null
  const set = (k: keyof Address) => (e: React.ChangeEvent<HTMLInputElement>) => { const v = e.target.value; setA((a0) => ({ ...a0, [k]: v })) }
  const save = async () => {
    const r = await fetch(`./api/phone/${phoneId}/location`, { method: 'PUT', headers: { 'Content-Type': 'application/json', 'X-BitVoice': '1' }, body: JSON.stringify(a) })
    const d = await r.json().catch(() => ({}))
    if (!r.ok) {
      setProblems(Object.fromEntries((d.problems ?? []).map((p: { field: string; message: string }) => [p.field, p.message])))
      return
    }
    setProblems({})
    setInfo((i) => i && { ...i, address: a, confirmed: new Date().toISOString() })
    setOpen(false)
    toast.success('Thank you: 911 calls from here will give this address')
  }
  if (!open) {
    return (
      <button className="mb-3 flex w-full items-center gap-2 rounded-lg border border-amber-800 px-3 py-2 text-left text-xs text-amber-200" onClick={() => setOpen(true)}>
        <MapPin size={13} aria-hidden /> Working remotely{info.address ? `: ${info.address.number} ${info.address.street}, ${info.address.city}` : ''}. Change
      </button>
    )
  }
  return (
    <form className="mb-4 space-y-2 rounded-lg border border-amber-700 bg-amber-950/30 p-3" onSubmit={(e) => { e.preventDefault(); save() }} aria-labelledby="where-h">
      <h2 id="where-h" className="flex items-center gap-2 text-sm font-semibold text-amber-200"><MapPin size={14} aria-hidden /> Where are you working from?</h2>
      <p className="text-xs text-amber-100/80">This phone is away from the office. If you call 911, this address is sent to them.</p>
      <div className="grid grid-cols-[5rem_1fr] gap-2">
        <Field label="House number" htmlFor="rn" error={problems.number}><input id="rn" className="input" value={a.number} onChange={set('number')} /></Field>
        <Field label="Street" htmlFor="rs" error={problems.street}><input id="rs" className="input" value={a.street} onChange={set('street')} /></Field>
      </div>
      <Field label="Apartment, floor" htmlFor="rd"><input id="rd" className="input" value={a.detail} onChange={set('detail')} /></Field>
      <div className="grid grid-cols-2 gap-2">
        <Field label="City" htmlFor="rc" error={problems.city}><input id="rc" className="input" value={a.city} onChange={set('city')} /></Field>
        <Field label="State" htmlFor="rst"><input id="rst" className="input" value={a.state} onChange={set('state')} /></Field>
        <Field label="Postal code" htmlFor="rp"><input id="rp" className="input" value={a.postalCode} onChange={set('postalCode')} /></Field>
        <Field label="Country" htmlFor="rco" error={problems.country}><input id="rco" className="input uppercase" maxLength={2} value={a.country} onChange={set('country')} /></Field>
      </div>
      <div className="flex gap-2">
        <button className="btn btn-primary">Confirm</button>
        {info.address && <button type="button" className="btn" onClick={() => setOpen(false)}>Cancel</button>}
      </div>
    </form>
  )
}
