import { Phone } from 'lucide-react'
import { Toaster } from 'sonner'
import { Softphone } from './Softphone'
import { usePhone } from './usePhone'
import { useNativeBridge } from './native'
import { RemoteLocation } from './RemoteLocation'

/** The page staff open to use their browser as their phone: no administrator sign-in, just the extension. */
export function PhoneApp() {
  const api = usePhone()
  useNativeBridge(api)
  return (
    <main className="grid min-h-full place-items-center p-6">
      <section className="card w-full max-w-sm p-6">
        <h1 className="mb-4 flex items-center gap-2 text-xl font-semibold text-slate-100"><Phone size={20} className="text-emerald-300" aria-hidden /> BitVoice phone</h1>
        {api.phone && <RemoteLocation phoneId={api.phone.id} />}
        <Softphone phoneApi={api} />
      </section>
      <Toaster theme="dark" richColors position="bottom-center" />
    </main>
  )
}
