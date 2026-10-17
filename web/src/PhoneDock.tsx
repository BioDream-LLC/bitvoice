import { clsx } from 'clsx'
import { Phone, PhoneIncoming, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { useExtensionsQuery } from './api'
import { useIsAdmin } from './Editor'
import { Softphone } from './Softphone'
import { usePhone } from './usePhone'

/** The browser phone, docked at the bottom right of every screen so a call survives moving between pages. */
export function PhoneDock() {
  const api = usePhone()
  const { data } = useExtensionsQuery()
  // Administrators pick any extension; everyone else signs the phone in with the extension's password.
  const admin = useIsAdmin()
  const [open, setOpen] = useState(false)
  const ringing = api.calls.some((c) => c.state === 'incoming')
  const live = api.calls.some((c) => c.state === 'connected')
  // Any page can ask the browser phone to call a number: window.dispatchEvent(new CustomEvent('bitvoice:dial', { detail: '800' })).
  useEffect(() => {
    const on = (e: Event) => {
      const number = (e as CustomEvent<string>).detail
      setOpen(true)
      if (api.phone) api.dial(number)
      else toast.info('Choose your extension in the browser phone first', { description: `then it calls ${number}` })
    }
    window.addEventListener('bitvoice:dial', on)
    return () => window.removeEventListener('bitvoice:dial', on)
  }, [api])
  return (
    <div className="fixed bottom-5 right-5 z-40 flex flex-col items-end gap-3">
      {(open || ringing) && (
        <section className="card w-80 p-4 shadow-2xl" aria-label="Browser phone">
          <div className="mb-3 flex items-center">
            <h2 className="flex items-center gap-2 font-semibold text-slate-100"><Phone size={16} className="text-emerald-300" aria-hidden /> Browser phone</h2>
            <button className="btn ml-auto px-2 py-1" aria-label="Close the phone panel" onClick={() => setOpen(false)}><X size={13} aria-hidden /></button>
          </div>
          <Softphone phoneApi={api} extensions={admin ? data?.items.map((x) => ({ number: x.number, name: x.name })) : undefined} />
        </section>
      )}
      <button onClick={() => setOpen(!open)} aria-label={ringing ? 'Incoming call' : 'Browser phone'}
        className={clsx('grid h-12 w-12 place-items-center rounded-full shadow-xl',
          ringing ? 'ringing bg-emerald-500 text-white' : live ? 'bg-emerald-600 text-white' : api.phone ? 'bg-sky-600 text-white' : 'bg-slate-800 text-slate-300 hover:bg-slate-700')}>
        {ringing ? <PhoneIncoming size={20} aria-hidden /> : <Phone size={20} aria-hidden />}
      </button>
    </div>
  )
}
