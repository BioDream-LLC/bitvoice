import { Languages } from 'lucide-react'
import { LANGUAGES, currentLanguage, setLanguage } from './i18n'

/** Chooses this browser's language for the screens; each language is named in itself (never translated). */
export function LanguagePicker({ className = '' }: { className?: string }) {
  const lang = currentLanguage()
  return (
    <label className={`flex items-center gap-2 text-xs text-slate-400 ${className}`}>
      <Languages size={14} className="text-sky-300" aria-hidden />
      <span className="sr-only">Language</span>
      <select translate="no" aria-label="Language" className="input py-1 text-xs" value={lang} onChange={(e) => setLanguage(e.target.value)}>
        {Object.entries(LANGUAGES).map(([id, name]) => <option key={id} value={id}>{name}</option>)}
      </select>
    </label>
  )
}
