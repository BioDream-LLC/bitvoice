import { lazy, StrictMode, Suspense, useEffect, useState } from 'react'
import { Receptionists } from './Receptionists'
import { Callbacks } from './Callbacks'
import { Quality } from './Quality'
import { Costs } from './Costs'
import { Contacts } from './Contacts'
import { Texts } from './Texts'
import { Timeline } from './Timeline'
import { CallLinks } from './CallLinks'
import { Reputation } from './Reputation'
import { Servers } from './Servers'
import { CallPage } from './CallPage'
import { Updates } from './Updates'
import { LoadTest } from './LoadTest'
import { Holds } from './Holds'
import { Help } from './Help'
import { Sites } from './Sites'
import { createRoot } from 'react-dom/client'
import { Provider } from 'react-redux'
import { HashRouter, Route, Routes as R } from 'react-router-dom'
import { Changes, Security, Settings, Users } from './Admin'
import { useMeQuery } from './api'
import { CallsPage, Dashboard } from './Dashboard'
import { Extensions } from './Extensions'
import './index.css'
import { Login } from './Login'
import { Health, History, Trace } from './Monitor'
import { ArchivePage } from './Archive'
import { ImportPage } from './Import'
import { PhoneApp } from './PhoneApp'
import { Paging } from './Paging'
import { Faxes } from './Faxes'
import { PSAP } from './PSAP'
import { CoreServices } from './CoreServices'
import { RadioConsole } from './Radio'
import { Phones } from './Phones'
import { Queues } from './Queues'
import { Schedules } from './Schedules'
import { Sounds } from './Sounds'
import { DialIns, Directories } from './DialIn'
import { Lookups, PINSets, Toggles, VoiceBots } from './Flow'
import { Hotel } from './Hotel'
import { DeskPhones } from './DeskPhones'
import { TelephonyCards } from './TelephonyCards'
import { RingGroups } from './RingGroups'
import { Conferences } from './Conferences'
import { Portal } from './Portal'
import { Tenants } from './Tenants'
import { FeatureCodesPage } from './FeatureCodes'
import { Routes } from './Routes'
import { Setup } from './Setup'
import { Shell } from './Shell'
import { store } from './store'
import { Trunks } from './Trunks'
import { Spinner } from './ui'

// Pages with large editors (CodeMirror, waveforms) load on first visit, keeping the first screen quick.
const Scripts = lazy(() => import('./Scripts').then((m) => ({ default: m.Scripts })))
const VoicemailPage = lazy(() => import('./Voicemail').then((m) => ({ default: m.VoicemailPage })))
const CallFlow = lazy(() => import('./CallFlow').then((m) => ({ default: m.CallFlow })))
const Menus = lazy(() => import('./Menus').then((m) => ({ default: m.Menus })))

/** #/phone (and #/phone?ext=...) is the staff phone; #/phones is the administrators' Phones page. */
const isPhoneApp = (hash: string) => /^#\/phone(?:[/?]|$)/.test(hash)
/** #/c/<address> is a call link's public page: anyone may open it. */
const callSlug = (hash: string) => /^#\/c\/([a-z0-9-]{8,64})$/.exec(hash)?.[1] ?? ''

function App() {
  // The staff phone page needs no administrator account, nor does a call link's page.
  const [hash, setHash] = useState(location.hash)
  useEffect(() => {
    const on = () => setHash(location.hash)
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  const slug = callSlug(hash)
  if (slug) return <CallPage slug={slug} />
  if (/^#\/me(?:[/?]|$)/.test(hash)) return <Portal />
  return isPhoneApp(hash) ? <PhoneApp /> : <Admin />
}

function Admin() {
  const { data: cached, error, isLoading, isError } = useMeQuery()
  const me = isError ? undefined : cached // a failed refetch (signed out, session expired) is not signed in
  if (isLoading) return <div className="grid h-full place-items-center"><Spinner /></div>
  const body = (error as { data?: { setupComplete?: boolean } } | undefined)?.data
  if (!me) {
    if (body && body.setupComplete === false) return <Setup />
    return <Login />
  }
  return (
    <Shell me={me}>
      <Suspense fallback={<div className="grid h-64 place-items-center"><Spinner /></div>}>
      <R>
        <Route path="/" element={<Dashboard />} />
        <Route path="/calls" element={<CallsPage />} />
        <Route path="/extensions" element={<Extensions />} />
        <Route path="/ring-groups" element={<RingGroups />} />
        <Route path="/phones" element={<Phones />} />
        <Route path="/menus" element={<Menus />} />
        <Route path="/receptionists" element={<Receptionists />} />
        <Route path="/callbacks" element={<Callbacks />} />
        <Route path="/quality" element={<Quality />} />
        <Route path="/costs" element={<Costs />} />
        <Route path="/contacts" element={<Contacts />} />
        <Route path="/texts" element={<Texts />} />
        <Route path="/timeline" element={<Timeline />} />
        <Route path="/call-links" element={<CallLinks />} />
        <Route path="/reputation" element={<Reputation />} />
        <Route path="/servers" element={<Servers />} />
        <Route path="/scripts" element={<Scripts />} />
        <Route path="/schedules" element={<Schedules />} />
        <Route path="/sounds" element={<Sounds />} />
        <Route path="/directories" element={<Directories />} />
        <Route path="/dial-in" element={<DialIns />} />
        <Route path="/toggles" element={<Toggles />} />
        <Route path="/voice-bots" element={<VoiceBots />} />
        <Route path="/pin-sets" element={<PINSets />} />
        <Route path="/lookups" element={<Lookups />} />
        <Route path="/hotel" element={<Hotel />} />
        <Route path="/desk-phones" element={<DeskPhones />} />
        <Route path="/cards" element={<TelephonyCards />} />
        <Route path="/paging" element={<Paging />} />
        <Route path="/conferences" element={<Conferences />} />
        <Route path="/codes" element={<FeatureCodesPage />} />
        <Route path="/faxes" element={<Faxes />} />
        <Route path="/psap" element={<PSAP />} />
        <Route path="/core" element={<CoreServices />} />
        <Route path="/radio" element={<RadioConsole />} />
        <Route path="/queues" element={<Queues />} />
        <Route path="/trunks" element={<Trunks />} />
        <Route path="/flow" element={<CallFlow />} />
        <Route path="/routes" element={<Routes />} />
        <Route path="/voicemail" element={<VoicemailPage />} />
        <Route path="/history" element={<History />} />
        <Route path="/health" element={<Health />} />
        <Route path="/trace" element={<Trace />} />
        <Route path="/security" element={<Security />} />
        <Route path="/changes" element={<Changes />} />
        <Route path="/updates" element={<Updates />} />
        <Route path="/loadtest" element={<LoadTest />} />
        <Route path="/holds" element={<Holds />} />
        <Route path="/help" element={<Help />} />
        <Route path="/sites" element={<Sites />} />
        <Route path="/archive" element={<ArchivePage />} />
        <Route path="/import" element={<ImportPage />} />
        <Route path="/tenants" element={<Tenants />} />
        <Route path="/users" element={<Users />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="*" element={<Dashboard />} />
      </R>
      </Suspense>
    </Shell>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Provider store={store}>
      <HashRouter><App /></HashRouter>
    </Provider>
  </StrictMode>,
)
