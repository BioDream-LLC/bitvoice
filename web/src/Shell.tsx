import { Command } from 'cmdk'
import { clsx } from 'clsx'
import {
  Activity, BookOpen, BookOpenText, Braces, CloudUpload, Megaphone, Workflow, CalendarClock, FileUp, Grid3x3, ListOrdered, Smartphone, Voicemail, ChevronRight, History, LayoutDashboard, LogOut, Monitor, Moon, Phone, PhoneCall, PhoneForwarded, Signal, Coins, BookUser, MessageSquare, ScrollText, Link2, BadgeCheck, Server as ServerIcon,
  Gauge, MapPin, PackageCheck, Route, Scale, Search, Server, Settings, Shield, Stethoscope, Sun, Users, UserCog, Waypoints,
  Bot, Printer,
  Siren, Network, RadioTower, Users2, Asterisk, Building2, FlaskConical, AudioLines, BookA, KeyRound, BedDouble, Router, Cpu, ToggleRight, Contact, Inbox as InboxIcon } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { NavLink, useLocation, useNavigate } from 'react-router-dom'
import { Crash } from './Crash'
import { radioKeys } from './ui'
import { manual } from './manual'
import { EmergencyBanner } from './EmergencyMode'
import { Toaster, toast } from 'sonner'
import { api, useExtensionsQuery, useLogoutMutation, useSystemQuery, useMenusQuery, useQueuesQuery, useRingGroupsQuery, useStatsQuery, useTrunksQuery, type Me } from './api'
import { onServerEvent, store, useLiveEvents } from './store'
import { PhoneDock } from './PhoneDock'

const labRoutes = ['/psap', '/radio', '/core']

/** On the public-safety screens: what they are (a lab track), and how to show them in the menu. */
function LabNotice() {
  const { pathname } = useLocation()
  const { data: sys } = useSystemQuery()
  if (!labRoutes.includes(pathname)) return null
  return (
    <div role="note" className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-amber-500/30 bg-amber-950/40 px-6 py-2 text-sm text-amber-200">
      <FlaskConical size={16} className="shrink-0 text-amber-300" aria-hidden />
      <span><b>Lab and design-partner track.</b> Not certified for live 911 or radio dispatch, and not yet tried with a live ESInet, P25 system or CAD.</span>
      {sys && !sys.publicSafety && <NavLink to="/settings" className="text-amber-300 underline">Hidden from the menu: show it in Settings</NavLink>}
    </div>
  )
}

/** Which screens show: administrators' screens to administrators, the server's own screens not inside
 * a hosted organisation, Organisations only on the main system, and the public-safety profiles (a lab
 * track) only once they are switched on in Settings or set up. */
export function useVisible(me: { role: string }) {
  const { data: sys } = useSystemQuery()
  const hosted = !!sys?.tenant
  return (n: { admin?: boolean; system?: boolean; hostOnly?: boolean; safety?: boolean }) =>
    (!n.admin || me.role === 'admin') && !(n.system && hosted) && !(n.hostOnly && (hosted || sys?.hosts === false)) &&
    !(n.safety && !sys?.publicSafety)
}

type NavItem = { to: string; label: string; icon: typeof Users; color: string; keys: string; group: string; admin?: boolean; system?: boolean; hostOnly?: boolean; safety?: boolean }

export const nav: NavItem[] = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard, color: 'text-sky-300', keys: 'home overview', group: 'Today' },
  { to: '/calls', label: 'Live calls', icon: PhoneCall, color: 'text-emerald-300', keys: 'active now', group: 'Today' },
  { to: '/queues', label: 'Queues', icon: ListOrdered, color: 'text-orange-300', keys: 'acd agents contact centre wallboard', group: 'Today' },
  { to: '/voicemail', label: 'Voicemail', icon: Voicemail, color: 'text-violet-300', keys: 'messages greeting mailbox', group: 'Today' },
  { to: '/callbacks', label: 'Callbacks', icon: PhoneForwarded, color: 'text-emerald-300', keys: 'call back ring back request message', group: 'Today' },
  { to: '/inbox', label: 'Inbox', icon: InboxIcon, color: 'text-emerald-300', keys: 'inbox omnichannel whatsapp messenger facebook web chat live chat website widget conversations messages', group: 'Today' },
  { to: '/texts', label: 'Texts', icon: MessageSquare, color: 'text-sky-300', keys: 'sms text message texting mms textmagic twilio telnyx bandwidth vonage plivo signalwire flowroute voip.ms 10dlc reply', group: 'Today' },
  { to: '/faxes', label: 'Faxes', icon: Printer, color: 'text-violet-300', keys: 'fax t38 t.38 pdf received send sent outbox fax to email email to fax fax number', group: 'Today' },
  { to: '/reputation', label: 'Caller ID reputation', icon: BadgeCheck, color: 'text-emerald-300', keys: 'reputation spam likely labelled caller id branded calling rich call data rcd cnam logo answer rate robocall', group: 'Calls' },
  { to: '/call-links', label: 'Call links', icon: Link2, color: 'text-emerald-300', keys: 'call link website call button qr code web call click to call browser', group: 'Calls' },
  { to: '/timeline', label: 'Timeline', icon: ScrollText, color: 'text-sky-300', keys: 'timeline customer history notes everything one view', group: 'Today' },
  { to: '/contacts', label: 'Contacts', icon: BookUser, color: 'text-teal-300', keys: 'contacts directory phonebook address book people suppliers clients csv import', group: 'Today' },
  { to: '/history', label: 'Call history', icon: BookOpen, color: 'text-fuchsia-300', keys: 'cdr records log', group: 'Today' },
  { to: '/flow', label: 'Call flow', icon: Workflow, color: 'text-amber-300', keys: 'diagram map how calls flow simulate simulator try test call path where would a call go', group: 'Calls' },
  { to: '/extensions', label: 'Extensions', icon: Phone, color: 'text-sky-300', keys: 'users phones people', group: 'Calls' },
  { to: '/ring-groups', label: 'Ring groups', icon: Users, color: 'text-violet-300', keys: 'hunt group', group: 'Calls' },
  { to: '/paging', label: 'Paging', icon: Megaphone, color: 'text-amber-300', keys: 'intercom announce overhead speaker', group: 'Calls' },
  { to: '/conferences', label: 'Conference rooms', icon: Users2, color: 'text-teal-300', keys: 'conference meeting bridge confbridge meetme dial-in pin leader moderator mute room', group: 'Calls' },
  { to: '/codes', label: 'Feature codes', icon: Asterisk, color: 'text-sky-300', keys: 'feature codes star codes forward call forwarding do not disturb dnd follow me find me pickup call pickup intercom speed dial *72 *8', group: 'Calls' },
  { to: '/menus', label: 'Menus', icon: Grid3x3, color: 'text-sky-300', keys: 'ivr auto attendant press 1 prompt', group: 'Calls' },
  { to: '/receptionists', label: 'AI receptionists', icon: Bot, color: 'text-violet-300', keys: 'ai assistant virtual receptionist answer questions', group: 'Calls' },
  { to: '/sounds', label: 'Sounds', icon: AudioLines, color: 'text-violet-300', keys: 'sounds prompts recordings languages spanish french german translate speech tts music on hold moh hold music queue music announcements', group: 'Calls' },
  { to: '/directories', label: 'Dial by name', icon: BookA, color: 'text-sky-300', keys: 'directory dial by name company directory spell name 411 staff directory', group: 'Calls' },
  { to: '/dial-in', label: 'Dial-in', icon: KeyRound, color: 'text-amber-300', keys: 'disa dial in direct inward system access call in pin call out through the office road warrior', group: 'Calls' },
  { to: '/hotel', label: 'Hotel', icon: BedDouble, color: 'text-violet-300', keys: 'hotel wake up wake-up alarm morning call guest room check in check out pms property management fias opera housekeeping room status minibar charges hospitality', group: 'Calls' },
  { to: '/dialer', label: 'Outbound dialer', icon: PhoneForwarded, color: 'text-emerald-300', keys: 'outbound dialer campaign predictive power progressive preview autodialer vicidial telemarketing collections do not call dnc list amd answering machine', group: 'Calls' },
  { to: '/voice-bots', label: 'Voice bots', icon: AudioLines, color: 'text-fuchsia-300', keys: 'voice bot audiosocket ai agent speech stream external media ari llm conversational ivr asterisk', group: 'Calls' },
  { to: '/toggles', label: 'Day/night toggles', icon: ToggleRight, color: 'text-indigo-300', keys: 'day night toggle call flow control switch override night service closed early holiday *28 busy lamp blf', group: 'Calls' },
  { to: '/schedules', label: 'Schedules', icon: CalendarClock, color: 'text-amber-300', keys: 'opening hours holidays time night closed', group: 'Calls' },
  { to: '/scripts', label: 'Scripts', icon: Braces, color: 'text-fuchsia-300', keys: 'javascript lua code route logic', group: 'Calls' },
  { to: '/pin-sets', label: 'PIN sets', icon: KeyRound, color: 'text-amber-300', keys: 'pin set pinset account code long distance outbound route authorisation authorization billing client code', group: 'Calls' },
  { to: '/lookups', label: 'Caller ID lookup', icon: Contact, color: 'text-sky-300', keys: 'caller id lookup cnam opencnam caller name superfecta crm name callers', group: 'Calls' },
  { to: '/routes', label: 'Call routing', icon: Route, color: 'text-amber-300', keys: 'inbound outbound did dial plan', group: 'Calls' },
  { to: '/trunks', label: 'Trunks', icon: Server, color: 'text-teal-300', keys: 'carrier provider line sip', group: 'Calls' },
  { to: '/cards', label: 'Telephony cards', icon: Cpu, color: 'text-amber-300', keys: 'telephony card dahdi digium sangoma openvox fxs fxo analogue analog phone line pstn pots tdm wildcard tdm400 a200 bitvoice-dahdi chan_dahdi zaptel', group: 'Calls' },
  { to: '/desk-phones', label: 'Skinny and MGCP', icon: Router, color: 'text-teal-300', keys: 'skinny sccp cisco 7940 7960 7941 7942 7945 7961 7962 7965 7970 7975 8945 6941 mgcp gateway call agent ata analogue analog fxs port aaln line card chan_skinny chan_mgcp legacy desk phones', group: 'Calls' },
  { to: '/phones', label: 'Phones', icon: Smartphone, color: 'text-emerald-300', keys: 'devices provisioning yealink polycom grandstream snom fanvil cisco 3pcc spa mitel aastra sangoma algo cyberdata mac', group: 'Calls' },
  { to: '/sites', label: 'Sites', icon: MapPin, color: 'text-amber-300', keys: 'building office address 911 location remote', group: 'Calls' },
  { to: '/servers', label: 'Servers', icon: ServerIcon, color: 'text-sky-300', keys: 'servers cluster failover high availability redundancy second server backup shared database several servers', group: 'System' , system: true },
  { to: '/health', label: 'Health', icon: Stethoscope, color: 'text-emerald-300', keys: 'check status problems', group: 'System' },
  { to: '/costs', label: 'Costs', icon: Coins, color: 'text-amber-300', keys: 'cost costs spend money budget rates prices toll fraud billing department', group: 'System' },
  { to: '/quality', label: 'Call quality', icon: Signal, color: 'text-emerald-300', keys: 'mos jitter packet loss sound choppy audio quality diagnosis site provider', group: 'System' },
  { to: '/trace', label: 'SIP trace', icon: Waypoints, color: 'text-orange-300', keys: 'debug ladder messages', admin: true, group: 'System' , system: true },
  { to: '/security', label: 'Security', icon: Shield, color: 'text-rose-300', keys: 'ban fraud audit', group: 'System' },
  { to: '/loadtest', label: 'Call generator', icon: Gauge, color: 'text-orange-300', keys: 'load test stress regression capacity calls per second', admin: true, group: 'System' , system: true },
  { to: '/updates', label: 'Updates', icon: PackageCheck, color: 'text-emerald-300', keys: 'upgrade version install rollback release', admin: true, group: 'System' , system: true },
  { to: '/changes', label: 'Changes', icon: History, color: 'text-violet-300', keys: 'revisions undo restore backup export import', group: 'System' },
  { to: '/holds', label: 'Legal holds', icon: Scale, color: 'text-amber-300', keys: 'retention keep delete litigation ediscovery compliance', admin: true, group: 'System' },
  { to: '/archive', label: 'Archive', icon: CloudUpload, color: 'text-teal-300', keys: 'backup s3 aws azure blob cloud offsite', group: 'System' },
  { to: '/import', label: 'Import', icon: FileUp, color: 'text-sky-300', keys: 'freepbx asterisk 3cx cisco cucm call manager avaya ip office communication manager mitel mivoice migrate pjsip csv move hunt pilot line group route pattern', admin: true, group: 'System' },
  { to: '/tenants', label: 'Organisations', icon: Building2, color: 'text-violet-300', keys: 'tenants organisations organizations customers multi-tenant hosted hosting reseller provider domain', admin: true, hostOnly: true, group: 'System' },
  { to: '/users', label: 'Administrators', icon: UserCog, color: 'text-fuchsia-300', keys: 'accounts login', admin: true, group: 'System' },
  { to: '/settings', label: 'Settings', icon: Settings, color: 'text-slate-300', keys: 'system ports 911 emergency chat presence staff chat messages team rooms snmp monitoring traps syslog csv cdr postgres', group: 'System' },
  { to: '/psap', label: '911 calls', icon: Siren, color: 'text-rose-300', keys: '911 psap ng911 i3 esinet location pidf ali eido cad call-taker answer time rebid nena log', group: 'Public safety · lab', safety: true },
  { to: '/radio', label: 'Radio console', icon: RadioTower, color: 'text-amber-300', keys: 'radio console dispatch roip ptt push to talk channel talkgroup patch p25 issi cssi rfss lmr gateway encryption private call alert tone radio log', group: 'Public safety · lab', safety: true },
  { to: '/core', label: 'Core services', icon: Network, color: 'text-violet-300', keys: 'ng911 core services ecrf lost esrp routing boundaries gis geojson psap overflow policy rules bcf border lvf address validation', admin: true, group: 'Public safety · lab', safety: true },
  { to: '/help', label: 'Help', icon: BookOpenText, color: 'text-sky-300', keys: 'manual how to documentation guide', group: 'System' },
]

type Theme = 'midnight' | 'dark' | 'light'
const themes: { id: Theme; label: string; icon: typeof Moon }[] = [
  { id: 'midnight', label: 'Midnight', icon: Moon },
  { id: 'dark', label: 'Dark', icon: Monitor },
  { id: 'light', label: 'Light', icon: Sun },
]

export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, set] = useState<Theme>(() => (localStorage.getItem('bitvoice.theme') as Theme) || 'midnight')
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('bitvoice.theme', theme)
  }, [theme])
  return [theme, set]
}

export function Logo({ size = 32 }: { size?: number }) {
  return <img src="./favicon.svg" width={size} height={size} alt="" aria-hidden />
}

/** Every typed word must appear in the item (no scattered-letter matches: "dr lee" is not "Trunks");
 * words at the start of a word rank higher. */
export function paletteFilter(value: string, search: string): number {
  const v = value.toLowerCase()
  const words = search.toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return 1
  let score = 0
  for (const w of words) {
    const i = v.indexOf(w)
    if (i < 0) return 0
    score += i === 0 || v[i - 1] === ' ' ? 2 : 1
  }
  // An item whose text starts with what was typed (its name, before its keywords) comes before one that only
  // has it among its keywords: "Phones" opens Phones, not Extensions (whose keywords include phones).
  return (score / (words.length * 2)) * (v.startsWith(search.toLowerCase().trim()) ? 1 : 0.9)
}

function Palette({ open, setOpen, me }: { open: boolean; setOpen: (v: boolean) => void; me: Me }) {
  const navigate = useNavigate()
  const visible = useVisible(me)
  const skip = { skip: !open }
  const { data: exts } = useExtensionsQuery(undefined, skip)
  const { data: groups } = useRingGroupsQuery(undefined, skip)
  const { data: queues } = useQueuesQuery(undefined, skip)
  const { data: menus } = useMenusQuery(undefined, skip)
  const { data: trunks } = useTrunksQuery(undefined, skip)
  if (!open) return null
  // Everything with a name or number, so typing "lee" or "101" opens that editor directly.
  const things: { key: string; label: string; search?: string; detail: string; to: string; icon: typeof Phone; color: string }[] = [
    ...(exts?.items ?? []).map((x) => ({ key: 'x' + x.id, label: `${x.number} ${x.name}`, detail: 'Extension', to: `/extensions?open=${x.number}`, icon: Phone, color: 'text-sky-300' })),
    ...(groups?.items ?? []).map((g) => ({ key: 'g' + g.id, label: `${g.number} ${g.name}`, detail: 'Ring group', to: `/ring-groups?open=${g.number}`, icon: Users, color: 'text-violet-300' })),
    ...(queues?.items ?? []).map((q) => ({ key: 'q' + q.id, label: `${q.number} ${q.name}`, detail: 'Queue', to: `/queues?open=${q.number}`, icon: Users, color: 'text-amber-300' })),
    ...(menus?.items ?? []).map((m) => ({ key: 'm' + m.id, label: `${m.number ? m.number + ' ' : ''}${m.name}`, detail: 'Menu', to: `/menus?open=${m.id}`, icon: Phone, color: 'text-emerald-300' })),
    ...(trunks?.items ?? []).map((tr) => ({ key: 't' + tr.id, label: tr.name, detail: 'Trunk ' + tr.host, to: `/trunks?open=${tr.id}`, icon: Server, color: 'text-teal-300' })),
    ...manual.map((m) => ({ key: 'h' + m.id, label: m.title, search: m.title + ' ' + m.keys, detail: 'Help', to: `/help?a=${m.id}`, icon: BookOpenText, color: 'text-sky-300' })),
  ]
  const go = (to: string) => { setOpen(false); navigate(to) }
  return (
    <div className="fixed inset-0 z-50 grid place-items-start bg-black/50 pt-[15vh] backdrop-blur-sm" onClick={() => setOpen(false)}>
      <Command label="Go to" filter={paletteFilter} role="dialog" aria-modal="true" aria-label="Search" className="card mx-auto w-full max-w-xl overflow-hidden" onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => { if (e.key === 'Escape') setOpen(false) }}>
        <div className="flex items-center gap-2 border-b border-slate-800 px-4">
          <Search size={18} className="text-slate-500" aria-hidden />
          <Command.Input autoFocus placeholder="Go to a screen or do something…" className="w-full bg-transparent py-3 text-slate-100 outline-none" />
        </div>
        <Command.List className="max-h-80 overflow-auto p-2">
          <Command.Empty className="p-4 text-sm text-slate-500">Nothing matches.</Command.Empty>
          <Command.Group heading="Screens" className="text-xs text-slate-500">
            {nav.filter(visible).map((n) => (
              <Command.Item key={n.to} value={`${n.label} ${n.keys}`} onSelect={() => go(n.to)}
                className="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm text-slate-200 data-[selected=true]:bg-slate-800">
                <n.icon size={16} className={n.color} aria-hidden /> {n.label}
              </Command.Item>
            ))}
          </Command.Group>
          {things.length > 0 && (
            <Command.Group heading="Phone system" className="text-xs text-slate-500">
              {things.map((x) => (
                <Command.Item key={x.key} value={`${x.search ?? x.label} ${x.detail}`} onSelect={() => go(x.to)}
                  className="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm text-slate-200 data-[selected=true]:bg-slate-800">
                  <x.icon size={16} className={x.color} aria-hidden /> {x.label} <span className="ml-auto text-xs text-slate-500">{x.detail}</span>
                </Command.Item>
              ))}
            </Command.Group>
          )}
          {me.role === 'admin' && (
            <Command.Group heading="Actions" className="text-xs text-slate-500">
              {[
                ['Add an extension', '/extensions?new=1', Phone, 'text-sky-300'],
                ['Add a trunk', '/trunks?new=1', Server, 'text-teal-300'],
                ['Add a ring group', '/ring-groups?new=1', Users, 'text-violet-300'],
              ].map(([label, to, Icon, c]) => {
                const I = Icon as typeof Phone
                return (
                  <Command.Item key={to as string} value={label as string} onSelect={() => go(to as string)}
                    className="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm text-slate-200 data-[selected=true]:bg-slate-800">
                    <I size={16} className={c as string} aria-hidden /> {label as string}
                  </Command.Item>
                )
              })}
            </Command.Group>
          )}
        </Command.List>
      </Command>
    </div>
  )
}

export function Shell({ me, children }: { me: Me; children: ReactNode }) {
  const [theme, setTheme] = useTheme()
  const visible = useVisible(me)
  const location = useLocation()
  const [palette, setPalette] = useState(false)
  const [logout] = useLogoutMutation()
  const { data: stats } = useStatsQuery()
  useLiveEvents(true)

  useEffect(() => {
    const k = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette((v) => !v) }
    }
    window.addEventListener('keydown', k)
    return () => window.removeEventListener('keydown', k)
  }, [])

  useEffect(() => onServerEvent((type, data) => {
    if (type === 'emergency.call') {
      const d = data as { number: string; extension: string; name: string; location: string; place?: { remote: boolean; confirmed: boolean } }
      toast.error(`Emergency call: ${d.number} from ${d.extension} ${d.name}`, {
        description: d.location || 'No location set for this extension', duration: 120000,
        action: { label: 'Show the call', onClick: () => { location.hash = '#/calls' } } })
    }
    if (type === 'voicemail.urgent') {
      const d = data as { box: string; from: string; fromName: string; summary: string }
      toast.warning(`Urgent voicemail for ${d.box} from ${d.fromName || d.from}`, { description: d.summary, duration: 30000,
        action: { label: 'Listen', onClick: () => { location.hash = '#/voicemail' } } })
    }
    if (type === 'callback.new') {
      const d = data as { name: string; number: string; when: string; reason: string; receptionist?: string }
      toast.info(`Callback wanted: ${d.name || d.number}`, { description: [d.reason, d.when].filter(Boolean).join(' · '), duration: 20000,
        action: { label: 'Open', onClick: () => { location.hash = '#/callbacks' } } })
    }
    if (type === 'text.new') {
      const d = data as { other: string; body: string }
      toast.info(`Text from ${d.other}`, { description: d.body.slice(0, 140), duration: 15000, action: { label: 'Open', onClick: () => { location.hash = '#/texts' } } })
    }
    if (type === 'fax.received') {
      const d = data as { from: string; pages: number; status: string; boxName: string }
      const what = d.status === 'failed' ? 'A fax could not be received' : `Fax from ${d.from}, ${d.pages} page${d.pages === 1 ? '' : 's'}`
      ;(d.status === 'received' ? toast.info : toast.warning)(what, { description: d.boxName, duration: 15000, action: { label: 'Open', onClick: () => { location.hash = '#/faxes' } } })
    }
    if (type === 'fax.sent') {
      const d = data as { to: string; sent: number; pages: number; status: string; error?: string }
      if (d.status === 'sent') toast.success(`Fax to ${d.to} sent`, { description: `${d.sent} page${d.sent === 1 ? '' : 's'}` })
      else toast.error(`Fax to ${d.to} not sent`, { description: d.error, duration: 30000, action: { label: 'Open', onClick: () => { location.hash = '#/faxes' } } })
    }
    if (type === 'cost.alert') {
      const d = data as { level: string; title: string; message: string }
      ;(d.level === 'fail' ? toast.error : toast.warning)(d.title, { description: d.message, duration: 60000,
        action: { label: 'Costs', onClick: () => { location.hash = '#/costs' } } })
    }
    if (type === 'security.banned') {
      const d = data as { ip: string; reason: string }
      toast.warning(`Blocked ${d.ip}`, { description: d.reason })
    }
  }), [])

  return (
    <div className="flex h-full">
      <aside className="flex w-60 shrink-0 flex-col border-r border-slate-800 bg-slate-950">
        <div className="flex items-center gap-2 px-4 py-4">
          <Logo />
          <div>
            <div className="bg-gradient-to-r from-sky-300 to-violet-300 bg-clip-text text-lg font-bold text-transparent">BitVoice</div>
            <div className="text-xs text-slate-500">Phone system</div>
          </div>
        </div>
        <button onClick={() => setPalette(true)} className="mx-3 mb-3 flex items-center gap-2 rounded-lg border border-slate-800 bg-slate-900 px-3 py-2 text-sm text-slate-500 hover:border-slate-700">
          <Search size={14} aria-hidden /> Search <kbd className="ml-auto rounded border border-slate-700 px-1 text-[10px]">⌘K</kbd>
        </button>
        <nav aria-label="Main" className="flex-1 overflow-auto px-2">
          {nav.filter(visible).map((n, i, all) => (
            <div key={n.to}>
            {(i === 0 || all[i - 1].group !== n.group) && (
              <div className="px-3 pb-1 pt-3 text-[10px] font-semibold uppercase tracking-wider text-slate-600">{n.group}</div>
            )}
            <NavLink to={n.to} end={n.to === '/'}
              className={({ isActive }) => clsx('group mb-0.5 flex items-center gap-3 rounded-lg px-3 py-1.5 text-sm font-medium',
                isActive ? 'bg-slate-800 text-slate-50' : 'text-slate-400 hover:bg-slate-900 hover:text-slate-200')}>
              <n.icon size={17} className={n.color} aria-hidden />
              <span className="flex-1">{n.label}</span>
              {n.to === '/calls' && !!stats?.calls && (
                <span className="rounded-full bg-emerald-500/20 px-1.5 text-xs font-bold text-emerald-300">{stats.calls}</span>
              )}
              <ChevronRight size={14} className="opacity-0 group-hover:opacity-50" aria-hidden />
            </NavLink>
            </div>
          ))}
        </nav>
        <div className="border-t border-slate-800 p-3">
          <div className="mb-2 flex rounded-lg border border-slate-800 p-0.5" role="radiogroup" aria-label="Theme">
            {themes.map((t, i) => (
              <button key={t.id} role="radio" aria-checked={theme === t.id} title={t.label} aria-label={t.label}
                {...radioKeys(i, themes.map((x) => x.id), theme, setTheme)}
                onClick={() => setTheme(t.id)}
                className={clsx('flex flex-1 justify-center rounded-md py-1', theme === t.id ? 'bg-slate-800 text-amber-300' : 'text-slate-500')}>
                <t.icon size={15} aria-hidden />
              </button>
            ))}
          </div>
          <div className="flex items-center gap-2 text-sm">
            <span className="grid h-8 w-8 place-items-center rounded-full bg-fuchsia-950 text-xs font-bold text-fuchsia-300">
              {(me.name || me.username).slice(0, 2).toUpperCase()}
            </span>
            <div className="min-w-0 flex-1">
              <div className="truncate font-medium text-slate-200">{me.name || me.username}</div>
              <div className="text-xs text-slate-500">{me.role === 'admin' ? 'Administrator' : 'Viewer'}</div>
            </div>
            <button className="rounded p-1.5 text-slate-500 hover:bg-slate-800 hover:text-rose-300" title="Sign out" aria-label="Sign out"
              onClick={async () => { await logout(); store.dispatch(api.util.resetApiState()) }}><LogOut size={16} aria-hidden /></button>
          </div>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-auto">
        <EmergencyBanner />
        <LabNotice />
        <Crash resetKey={location.pathname}>{children}</Crash>
      </main>
      <Palette open={palette} setOpen={setPalette} me={me} />
      <Toaster theme={theme === 'light' ? 'light' : 'dark'} richColors position="bottom-center" />
      <PhoneDock />
      <span className="sr-only" aria-live="polite"><Activity aria-hidden /></span>
    </div>
  )
}
