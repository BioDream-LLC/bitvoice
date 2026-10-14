// The single place the interface talks to the server: RTK Query over the same public API scripts use.
// Every state-changing request carries X-BitVoice, which the server requires (CSRF protection).
import { createApi, fetchBaseQuery } from '@reduxjs/toolkit/query/react'

export type Role = 'admin' | 'viewer' | 'extension'
export interface Me { username: string; name: string; role: Role; setupComplete: boolean; mfa?: boolean }

export interface Destination { type: 'extension' | 'ringgroup' | 'queue' | 'menu' | 'schedule' | 'script' | 'receptionist' | 'voicemail' | 'fax' | 'esrp' | 'conference' | 'directory' | 'dialin' | 'echo' | 'hangup'; target: string }
export interface Receptionist { id: string; name: string; greeting: string; knowledge: string; person: Destination; messageBox: string }

export interface Extension {
  id: string; number: string; name: string; email: string; secret: string; enabled: boolean
  location: string; callerId: string; ringSeconds: number; noAnswer?: Destination | null; outbound: boolean
  voicemail: { enabled: boolean; pin: string; maxSeconds: number; email: boolean }; record: boolean; secureMedia: boolean
  kind: '' | 'door' | 'speaker' | 'analog'
  site: string; floor: string; room: string; department?: string; hotDesk?: boolean
  calendar?: { secret: string; whenBusy: 'voicemail' | 'forward' | 'ring' | ''; forwardTo: string; ignoreMeetings?: boolean }
  door: { code: string; url: string; method: string; username: string; password: string }
  followMe?: FollowMe; pickupGroup?: string; noIntercom?: boolean; speedDials?: SpeedDial[] | null
  language?: string; holdMusic?: string; guestRoom?: boolean
}
export interface FollowMe { enabled: boolean; numbers: string[] | null; strategy: 'sequential' | 'together' | ''; ringSeconds: number; confirm: boolean }
export interface SpeedDial { code: string; number: string; name: string }
export interface FeatureCodes {
  forwardAll: string; forwardAllOff: string; forwardBusy: string; forwardBusyOff: string; forwardNoAnswer: string; forwardNoAnswerOff: string
  dndOn: string; dndOff: string; dndToggle: string; followMe: string; pickup: string; directedPickup: string; intercom: string; speedDial: string
  wakeUp?: string; roomStatus?: string
}
export interface Directory { id: string; number: string; name: string; match: 'last' | 'first' | 'any'; members: string[] | null; language?: string }
export interface DialIn { id: string; number: string; name: string; pin: string; allowedCallers: string[] | null; as: string; language?: string }
export interface Hotel {
  wakeUpTries: number; wakeUpRetryMinutes: number; notify: string; barVacant: boolean; checkOutClear: boolean
  pms: { kind: '' | 'fias' | 'api'; host: string; port: number; chargeCalls: boolean; chargeUrl: string; markup: number }
}
export interface WakeUp { id: string; extension: string; at: string; daily?: boolean; by: string; tries: number; last?: string; lastAt?: string }
export interface Guest { room: string; name: string; account?: string; language?: string; since: string; status?: string }
export interface HotelView {
  rooms: { extension: string; name: string; guest?: Guest; status?: string; statusAt?: string; wakeUp?: WakeUp }[]
  wakeUps: WakeUp[]; link?: { up: boolean; lastSeen?: string; waiting: number }
}
export interface Conference {
  id: string; number: string; name: string; pin: string; leaderPin: string; waitForLeader: boolean; muteMembers: boolean
  maxMembers: number; record: boolean; quiet: boolean; hd?: boolean
}
export interface ConferenceMember { id: string; number: string; name: string; leader: boolean; muted: boolean; hand: boolean; speaking: boolean; waiting: boolean; joined: string; hd?: boolean }
export interface ConferenceRoom { number: string; name: string; started: string; locked: boolean; members: ConferenceMember[] | null; waiting: number; records: boolean; hd?: boolean }
export interface Myself {
  number: string; name: string; email: string; voicemail: boolean; followMe: FollowMe; speedDials: SpeedDial[]; companySpeedDials: SpeedDial[]
  pickupGroup: string; noIntercom: boolean; outbound: boolean; divert: { all?: string; busy?: string; noAnswer?: string; followMe?: boolean | null }
  dnd: boolean; followMeOn: boolean; messages: { new: number; old: number }; codes: FeatureCodes; voicemailNumber: string
}
export interface Divert { ext: string; all?: string; busy?: string; noAnswer?: string; followMe?: boolean | null; dnd: boolean; followMeOn: boolean }
export interface Voicemail { id: string; box: string; from: string; fromName: string; at: string; seconds: number; heard: boolean; transcript?: string; summary?: string; aiError?: string; urgency?: 'urgent' | 'normal' | 'low'; category?: string }
export interface RingGroup {
  id: string; number: string; name: string; strategy: 'all' | 'sequential'; members: string[]
  ringSeconds: number; noAnswer?: Destination | null
}
export interface Address { country: string; state: string; city: string; street: string; number: string; postalCode: string; detail: string }
export interface Site { id: string; name: string; country: string; state: string; county: string; city: string; street: string; number: string; postalCode: string; networks: string[] }
export interface FaxBox { id: string; number: string; name: string; email: string[]; station: string; senders?: string[] | null }
export interface FaxMail { host: string; port: number; username: string; password: string; folder: string; anySender: boolean }
export interface FaxKinds { kinds: string[]; paper: 'letter' | 'a4'; mail: { checked: string; error: string } }
export interface FaxRecord {
  id: string; box: string; boxName: string; from: string; station: string; at: string; pages: number
  status: 'received' | 'partial' | 'failed' | 'queued' | 'sending' | 'sent'; error?: string; emailed?: string[]
  direction?: 'in' | 'out'; to?: string; by?: string; subject?: string; tries?: number; sent?: number; rate?: number; remoteId?: string; sentAt?: string
}
export interface PageGroup { id: string; number: string; name: string; members: string[]; multicast: string }
export interface Queue {
  id: string; number: string; name: string; agents: string[]; strategy: QueueStrategy
  ringSeconds: number; wrapUpSeconds: number; maxWaitSeconds: number; timeout?: Destination | null; sticky?: boolean; stickyDays?: number
  language?: string; music?: string; announceSeconds?: number; announcePosition?: boolean; announceWait?: boolean
  penalties?: Record<string, number> | null; weight?: number
}
export type QueueStrategy = 'longest-idle' | 'round-robin' | 'ring-all' | 'fewest-calls' | 'linear' | 'random' | 'weighted-random'
export interface MusicClass { id: string; name: string; shuffle: boolean }
export interface PromptInfo { key: string; group: string; text: Record<string, string> }
export interface PromptsList {
  language: string; languages: { code: string; name: string; shipped: boolean }[]; prompts: PromptInfo[]
  recorded: Record<string, string[]>; speech: boolean
}
export type AgentState = 'available' | 'ringing' | 'busy' | 'wrap-up' | 'paused' | 'away' | 'logged-out' | 'offline'
export interface QueueStatus {
  number: string; name: string
  waiting: { callId: string; from: string; name: string; since: string; vip?: boolean }[]
  agents: { extension: string; name: string; state: AgentState; reason?: string; lastCall?: string }[]
  totals: { answered: number; abandoned: number; timedOut: number; longestWait: number }
}
export interface Script { id: string; name: string; language: 'js' | 'lua'; source: string; fallback: Destination }
export interface ScriptCall { from: string; fromName: string; to: string; direction: string; trunk: string; weekday: number; hour: number; minute: number; date: string }
export interface ScriptTest { type: string; target: string; log?: string[]; error?: string; millis: number }
export interface Menu {
  id: string; number: string; name: string; dialExtensions: boolean; timeoutSeconds: number; repeats: number
  options: { key: string; label: string; destination: Destination }[]; timeout?: Destination | null
}
export interface Schedule {
  id: string; name: string; hours: { days: number[]; start: string; end: string }[]
  holidays: { date: string; name: string }[]; open: Destination; closed: Destination
}
export type Vendor = 'yealink' | 'polycom' | 'grandstream' | 'snom'
export interface Device { id: string; mac: string; vendor: Vendor; model: string; extension: string; label: string }
export interface Civic {
  country?: string; state?: string; county?: string; city?: string; number?: string; numberSuffix?: string; preDir?: string; street?: string
  streetType?: string; postDir?: string; landmark?: string; location?: string; name?: string; postalCode?: string; building?: string
  unit?: string; floor?: string; room?: string
}
export interface Location {
  civic?: Civic; lat?: number; lon?: number; alt?: number; radius?: number; shape?: string; confidence?: number; method?: string
  providedBy?: string; time?: string; device?: string
}
export interface Additional {
  providers?: { name?: string; id?: string; type?: string; contact?: string; language?: string }[]
  service?: { environment?: string; types?: string[]; mobility?: string }
  device?: { class?: string; maker?: string; model?: string; id?: string; idType?: string }
  subscriber?: { name?: string; phones?: string[]; address?: string; language?: string }
  comments?: string[]; references?: string[]
}
export interface Emergency { location?: Location; locationUri?: string; rebid?: string; additional: Additional; service?: string; problems?: string[]; esinet: boolean; takenBy?: string; takenName?: string; takenAt?: string }
export interface CorePSAP { uri: string; name: string; trunk: string; overflow: string }
export interface CoreRule {
  id: string; name: string; services: string[]; psaps: string[]; schedule: string; whenOpen: boolean
  noLocation: boolean; suspicious: boolean; trunks: string[]; action: 'route' | 'reject'; to: string
}
export interface BCFSettings { enabled: boolean; perMinute: number; concurrent: number; deny: string[]; mark: boolean }
export interface CoreSettings { ecrf: boolean; source: string; allowFrom: string[]; psaps: CorePSAP[]; default: string; lostUrl: string; rules: CoreRule[]; bcf: BCFSettings }
export interface Validation { valid: string[]; invalid: string[]; unchecked: string[]; matched?: string; found?: Record<string, string> }
export interface BCFEvent { at: string; source: string; from: string; action: 'refused' | 'marked'; why: string }
export interface Boundary { id: string; service: string; name: string; uri: string; number: string; cities?: string[] | null; counties?: string[] | null; states?: string[] | null; corners: number }
export interface RadioChannel { id: string; name: string; port: number; gateway: string; color: string; kind: 'roip' | 'p25'; group: number; home: string; homeRfss: number; txKey: string }
export interface P25Key { id: string; name: string; alg: 'aes256' | 'des' | 'tdes'; keyId: number; key: string }
export interface P25Console {
  wacn: string; system: string; rfss: number; unit: number; sipPort: number; console: boolean; priority: number
  keys: P25Key[]; unitHome: string; unitHomeRfss: number; unitTxKey: string
}
export interface RadioSettings { console: string; channels: RadioChannel[]; p25: P25Console; record: boolean; cadUrl: string; cadSecret: string }
export interface ConsoleState {
  channels: {
    id: string; name: string; color: string; receive: boolean; lastRx?: string; txBy?: string; patched: string[]; problem?: string; kind: 'roip' | 'p25'
    linked: boolean; talker?: string; waiting?: boolean; notice?: string; encryption?: string; noKey?: boolean; txEncryption?: string
    private?: boolean; unit?: number; incoming?: boolean; emergency?: boolean; tone?: boolean
  }[]
  positions: { call: string; ext: string; name: string; monitor: string[]; ptt: string[] }[]
  tones: Record<string, string>
}
export interface Transmission {
  id: string; channel: string; channelName: string; direction: 'rx' | 'tx'; start: string; end: string; seconds: number
  unit?: string; dispatcher?: string; tone?: string; emergency?: boolean; encryption?: string; private?: boolean; recording?: string
}
export interface PSAPSettings { agencyId: string; elementId: string; aliAddress: string; eidoUrl: string; eidoToken: string; answerTarget: number }
export interface AnswerReport {
  target: number; calls: number; answered: number; abandoned: number; withinTarget: number; within20: number; average: number; longest: number
  buckets: number[]; byTaker: { ext: string; name: string; calls: number; average: number }[]; byHour: number[]
}
export interface I3Event { logEventType: string; timestamp: string; agencyAgentId?: string; callId?: string; from?: string; to?: string; extension?: Record<string, unknown> }
export interface Trunk {
  id: string; name: string; enabled: boolean; host: string; port: number; transport: 'udp' | 'tcp' | 'tls'; protocol?: '' | 'iax2'
  username: string; authUser: string; password: string; register: boolean; fromDomain: string
  callerId: string; maxChannels: number; allowFrom: string[]; secureMedia: boolean; richCallData?: boolean; teams?: boolean; sbcName?: string; codecs: string[]
  shared?: '' | 'one'; esinet?: boolean
  rates?: Rate[] | null; inboundPerMinute?: number
  shaken?: Shaken
}
export interface Shaken { sign: boolean; cert: string; key: string; x5u: string; attest: '' | 'A' | 'B' | 'C'; origId: string }
export interface Rate { prefix: string; name: string; perMinute: number; setup: number; increment: number; minimum: number }
export interface InboundRoute { id: string; name: string; did: string; trunk: string; destination: Destination }
export interface OutboundRoute { id: string; name: string; patterns: string[]; strip: number; prepend: string; trunks: string[] }

export interface ClusterSettings { name: string; sharedAddress: string; interface: string }
export interface SharedAddress { address: string; held: boolean; holder: string; error?: string; since?: string }
export interface DnsCheck {
  configured: boolean
  check?: {
    name: string; error?: string; missing: string[]; extra: string[]; suggested: string[]; at: string
    records: { service: string; target: string; port: number; priority: number; weight: number; addrs: string[] }[]
  }
}
export interface SystemSettings {
  name: string; purpose: 'office' | 'contact-centre' | '911'; country: string; timezone: string; language: string
  promptLanguages?: string[] | null; music?: MusicClass[] | null; holdMusic?: string; hotel?: Hotel
  publicAddress: string; cluster?: ClusterSettings; sipPort: number; rtpMin: number; rtpMax: number; codecs: string[]; echoNumber: string; voicemailNumber: string; parkNumber: string; parkSeconds: number
  codes?: FeatureCodes; speedDials?: SpeedDial[] | null
  callLinks?: CallLink[] | null; brands?: Brand[] | null; texting: Texting; screening: Screening; vips?: VIP[] | null; agentLogin: string; agentPause: string; hotDesk: { login: string; logout: string; signOutAt: string }
  emergencyNumbers: string[]; emergencyNotify: string[]
  recording: { mode: 'off' | 'all' | 'selected'; keepDays: number; pauseCode: string; resumeCode: string; notice?: 'off' | 'announce' | 'beep'; rules?: ConsentRule[] | null }
  retention: { callRecordDays: number; voicemailDays: number }
  audio: { voicemail: boolean; calls: boolean }
  gitops: { url: string; token: string; everyMinutes: number; apply: boolean }
  vault: { awsAccessKey: string; awsSecretKey: string; azureTenant: string; azureClientId: string; azureClientSecret: string }
  emergencyEmail: string[]; sendLocation: boolean; videoOff?: boolean; publicSafety?: boolean
  faxMail?: FaxMail; faxPaper?: '' | 'letter' | 'a4'; psap?: PSAPSettings; core?: CoreSettings; radio?: RadioSettings
  emergencyMode: { on: boolean; destination: Destination; code: string; allowed: string[]; since?: string; by?: string }
  mobile: { push: '' | 'relay' | 'direct'; relayUrl: string; relayKey: string; relaySecret: string; serverUrl: string; fcm: string
    apns: { keyId: string; teamId: string; topic: string; key: string; sandbox: boolean } }
  provisioning: { enabled: boolean; username: string; password: string; transport: 'udp' | 'tcp' | 'tls'; httpsOnly?: boolean }
  email: { host: string; port: number; username: string; password: string; from: string }
  ai: { enabled: boolean; baseUrl: string; key: string; model: string; voicemail: boolean; calls: boolean; briefings?: boolean; insights?: boolean; audioModel: string; speechModel: string; voice: string }
  crm: CRM
  metrics: Metrics
  costs: Costs
  sso: { enabled: boolean; label: string; issuer: string; clientId: string; clientSecret: string; redirectUrl: string; groupsClaim: string; adminGroups: string[]; viewerGroups: string[]; passwordSignIn: boolean }
  compat: { ami: boolean; amiListen: string; ari: boolean; users: { username: string; secret: string; write: boolean }[] }
  archive: {
    target: '' | 's3' | 'azure'; recordings: boolean; voicemail: boolean
    s3: { region: string; bucket: string; prefix: string; accessKey: string; secretKey: string; endpoint: string }
    azure: { containerSas: string; prefix: string }
  }
}
export interface Security {
  failBanThreshold: number; failBanWindowSecs: number; failBanMinutes: number; trustedNetworks: string[]
  blockPremium: boolean; maxCallsPerExtension: number
}
export interface Settings { system: SystemSettings; security: Security; rev: number }

export interface MediaStats { packets: number; lost: number; jitterMs: number; mos: number; lastSeen: number }
export interface LegInfo { party: string; peer: string; codec: string; media: MediaStats; onHold: boolean; ringing: boolean; transport: string; secure: boolean; video?: boolean; fax?: boolean }
export interface Call {
  id: string; state: 'ringing' | 'answered' | 'held' | 'parked'; direction: string; from: string; fromName: string
  to: string; toName: string; trunk?: string; screen?: { attest?: string; verified?: string; why?: string }; briefing?: string; start: string; answer?: string; mos: number; legs: LegInfo[]
; door?: string ; recording?: 'recording' | 'paused' ; supervision?: 'listen' | 'whisper' | 'barge'; emergency?: Emergency }
export interface Registration {
  extension: string; name: string; contact: string; source: string; transport: string; userAgent: string
  expires: string; since: string
}
export interface TrunkStatus { id: string; name: string; state: string; error?: string; addresses: string[]; latencyMs: number; since?: string; heldBy?: string }
export interface Stats {
  uptimeSeconds: number; calls: number; registrations: number; extensions: number; online: number
  trunks: number; trunksUp: number; banned: number; sipPort: number
}
export interface CDR {
  id: string; start: string; answer?: string; end: string; direction: string; from: string; fromName: string
  to: string; toName: string; answeredBy: string; trunk: string; disposition: string; cause: string
  duration: number; billsec: number; hangupBy: string; mos: number; recording?: string
  paused?: { from: number; to: number }[]; held?: boolean; attest?: string; signed?: string; verified?: string; screened?: string; ext?: string; department?: string; cost?: number; rate?: string
}
export interface Check { id: string; status: 'ok' | 'warn' | 'fail'; title: string; detail: string; fix?: string }
export interface Revision { rev: number; at: string; actor: string; summary: string }
export interface Ban { ip: string; until: string; reason: string }
export interface AuditEntry { at: string; actor: string; action: string; detail: string; from: string }
export interface User { username: string; name: string; role: Role; created: string; lastLogin?: string; sso?: string }
export interface TraceEntry { at: string; dir: 'in' | 'out'; peer: string; callId: string; summary: string; message: string }
export interface MobileDevice { id: string; extension: string; name: string; platform: 'ios' | 'android'; canWake: boolean; created: string; lastSeen?: string; lastPush?: string; pushError?: string }
export interface Change { path: string; before: string; after: string }
export interface ApiToken { id: string; name: string; role: 'admin' | 'viewer'; prefix: string; created: string; by: string; lastUsed?: string; expires?: string }
export interface Hold { id: string; name: string; reason: string; numbers: string[]; since?: string; until?: string; created: string; by: string; released?: string; releasedBy?: string }
export interface Callback { id: string; created: string; receptionist?: string; callId?: string; name: string; number: string; when: string; reason: string; status: 'open' | 'done' | 'cancelled'; by?: string; closed?: string; note?: string }
export interface Budget { id: string; department: string; monthly: number; email: string }
export interface Costs { currency: string; budgets: Budget[] | null; hourlyLimit: number; alertEmail: string }
export interface CostLine { key: string; name: string; calls: number; minutes: number; cost: number }
export interface CostReport { month: string; currency: string; total: number; calls: number; unpriced: number; departments: CostLine[]; people: CostLine[]; trunks: CostLine[]; days: CostLine[]; top: CDR[]; budgets: (Budget & { spent: number; percent: number })[] }
export interface DirNumber { kind: 'work' | 'mobile' | 'home' | 'other'; number: string }
export interface DirContact { id: string; name: string; company?: string; numbers: DirNumber[]; email?: string; notes?: string; updated?: string }
export interface HotDesk { desk: string; deskName: string; user: string; userName: string; since: string }
export interface ConsentRule { prefix: string; name: string; action: 'announce' | 'beep' | 'off' | 'never' }
export interface CallLink { id: string; name: string; slug: string; enabled: boolean; title: string; intro: string; askName: boolean; destination: Destination; maxCalls: number }
export interface SMSProvider { id: string; name: string; kind: string; username: string; key: string; hookSecret: string; baseUrl?: string; account?: string; app?: string }
export interface SMSKind { id: string; name: string; username?: string; key: string; account?: string; app?: string; base?: string; baseRequired?: boolean; help: string }
export interface SMSNumber { number: string; provider: string; name: string; notify: string[] | null }
export interface Texting { providers: SMSProvider[] | null; numbers: SMSNumber[] | null; tenDlc: { brand: string; campaign: string; useCase: string; sample: string } }
export interface TextMsg { id: string; at: string; direction: 'in' | 'out'; our: string; other: string; body: string; status: 'received' | 'sent' | 'failed'; error?: string; by?: string }
export interface Conversation { our: string; other: string; last: TextMsg; count: number; unread: number; optedOut: boolean }
export interface TimelineItem { kind: 'call' | 'voicemail' | 'text' | 'callback' | 'note'; at: string; id: string; title: string; detail?: string; direction?: string; recording?: string; box?: string; by?: string; status?: string }
export interface Timeline { number: string; contact: Contact | null; items: TimelineItem[] }
export interface ScreenNumber { number: string; note: string; added?: string; by?: string }
export interface Screening { failed: 'allow' | 'label' | 'block'; unverified: 'allow' | 'label'; anonymous: 'allow' | 'label' | 'block'; label?: string; blocked?: ScreenNumber[] | null; allowed?: ScreenNumber[] | null; stiRoots?: string }
export interface VIP { number: string; name: string }
export interface ClusterCall { id: string; from: string; fromName: string; to: string; toName: string; state: string; start: string }
export interface ClusterNode {
  id: string; name: string; sipAddr: string; version: string; started: string; seen: string; alive: boolean; self: boolean
  info: { phones: number; calls?: ClusterCall[] | null; busy?: string[] | null; tlsPort?: number; cert?: string; unreachable?: Record<string, string> | null }
}
export interface Brand { number: string; name: string; logo: string; reason: string; watch: boolean }
export interface NumberReputation {
  number: string; brand?: string; calls: number; answered: number; answerRate: number; short: number; shortRate: number
  unwanted: number; rejected: number; peakDay: number; prevRate: number; status: 'good' | 'watch' | 'labelled' | 'few'; why: string[]; watch: boolean
}
export interface Metrics { otlpUrl: string; otlpHeader: string; otlpSecret: string; intervalSeconds: number }
export interface CRM { lookupUrl: string; lookupHeader: string; lookupSecret: string; openUrl: string; logUrl: string; logSecret: string }
export interface Contact { name: string; company?: string; url?: string; notes?: string; source: 'crm' | 'extension' }
export interface CallerCard { number: string; contact: Contact | null; lookupError?: string; openUrl?: string; calls: number; lastCall?: string; recent?: CDR[]; callbacks: Callback[]; lastSummary?: string }
export interface SimBranch { when: string; taken: boolean; step: SimStep }
export interface SimStep { kind: string; target?: string; name: string; says: string; status: 'ok' | 'warn' | 'fail'; branches?: SimBranch[] }
export interface SimResult { direction: string; at: string; start: SimStep; outcome: string }
export type SimInput = { from: string; to: string; trunk: string; at: string; assumePhones: boolean }
export interface QualityPlace { kind: 'site' | 'provider'; name: string; calls: number; mos: number; poor: number; lossPct: number; jitterMs: number
  hours: { calls: number; lossPct: number; poor: number }[]; status: 'ok' | 'warn' | 'fail' | 'idle'; diagnosis: string; advice?: string }
export interface QualityReport { days: number; calls: number; places: QualityPlace[]; overall: string }
export interface TranscriptLine { second: number; side: 'caller' | 'callee'; text: string }
export interface CallTranscript { recording: string; callId: string; at: string; from: string; fromName: string; to: string; toName: string
  status: 'pending' | 'done' | 'failed'; error?: string; lines: TranscriptLine[]; summary: string; actionItems: string[]; done?: string
  talk?: Talk; insight?: Insight; answeredExt?: string }
export interface Talk { seconds: number; callerTalk: number; calleeTalk: number; overlap: number; silence: number; longestCaller: number; longestCallee: number; callerCutIn: number; calleeCutIn: number; longestSilence: number }
export interface Insight { moodStart: string; moodEnd: string; observations: { second: number; kind: 'went-well' | 'look-at' | 'unanswered' | 'promise'; note: string; quote: string }[] }
export interface AgentTalk { name: string; ext?: string; calls: number; talkShare: number; longest: number; cutIns: number; silenceShare: number; endedUpset: number; lookAt: number; wentWell: number }
export type CallbackInput = { name: string; number: string; when: string; reason: string }
type HoldInput = { name: string; reason: string; numbers: string[]; since: string; until: string }
export interface LoadRun {
  running: boolean; started: string; finished?: string; stopped: boolean
  plan: { from: string; to: string; answerAs: string; calls: number; rate: number; concurrent: number; holdSeconds: number }
  placed: number; answered: number; failed: number; active: number; failures: Record<string, number>
  setupMs: { p50: number; p95: number; max: number }; sent: number; received: number; lossPct: number; mos: number
}
export interface LoadStatus { run: LoadRun | null; limits: { calls: number; concurrent: number; rate: number; holdSeconds: number } }
export interface UpgradeStatus {
  running: string; signedBuild: boolean; waiting: '' | 'switch' | 'rollback'; since?: string; calls: number; possible: boolean
  state: { currentVersion: string; previousVersion: string; stagedVersion: string; stagedSigned: boolean; pending: boolean; history: { at: string; what: string }[] }
}
export interface SystemInfo { version: string; name: string; rev: number; addresses: string[]; sipPort: number; tlsPort: number; httpsPort: number; amiAddress: string; uptimeSeconds: number; tenant?: Tenant | null; hosts?: boolean; publicSafety?: boolean; publicSafetyInUse?: string }
export interface TenantLimits { extensions: number; calls: number; storageMB: number; aiPerDay: number }
export interface Tenant { id: string; name: string; domain: string; suspended: boolean; limits: TenantLimits; note: string; created?: string }
export interface TenantView extends Tenant { running: boolean; extensions: number; phones: number; calls: number; storageBytes: number; url: string; sipUser: string; admins: number }
export interface SetupStatus { complete: boolean; allowed: boolean; addresses: string[]; sipPort: number; version: string }

export interface Problem { field: string; message: string }
export interface ApiErrorBody { message: string; problems?: Problem[] }

interface List<T> { items: T[]; rev?: number }
interface Item<T> { item: T; rev: number }

// The setup code, when the wizard runs from another computer, rides on every request.
let setupCode = ''
export function setSetupCode(code: string) { setupCode = code }

const baseQuery = fetchBaseQuery({
  baseUrl: './api/',
  credentials: 'same-origin',
  prepareHeaders: (h) => {
    h.set('X-BitVoice', '1')
    if (setupCode) h.set('X-Setup-Code', setupCode)
    return h
  },
})

const tags = ['Me', 'Extensions', 'RingGroups', 'Trunks', 'Inbound', 'Outbound', 'Settings', 'Users', 'Calls',
  'Registrations', 'TrunkStatus', 'Stats', 'CDR', 'Health', 'History', 'Bans', 'Audit', 'Trace', 'Setup', 'Voicemail', 'Queues', 'QueueStatus', 'Devices', 'Schedules', 'Menus', 'Scripts', 'PageGroups', 'Upgrade', 'LoadTest', 'Holds', 'Mobile', 'Tokens', 'GitOps', 'Sites', 'Receptionists', 'Callbacks', 'Transcripts', 'Quality', 'Costs', 'Directory', 'HotDesks', 'Texts', 'Timeline', 'Away', 'Reputation', 'Certificate', 'Database', 'Servers', 'FaxBoxes', 'Faxes', 'Conferences', 'ConferenceLive', 'Diverts', 'Myself', 'MyVoicemail', 'Tenants', 'System', 'Prompts', 'Music', 'Directories', 'DialIns', 'Hotel', 'WakeUps'] as const

export const api = createApi({
  reducerPath: 'api',
  baseQuery,
  tagTypes: tags,
  endpoints: (b) => ({
      me: b.query<Me, void>({ query: () => 'auth/me', providesTags: ['Me'] }),
      authOptions: b.query<{ password: boolean; sso: { enabled: boolean; label: string } }, void>({ query: () => 'auth/options' }),
      login: b.mutation<Me, { username: string; password: string; code?: string }>({
        query: (body) => ({ url: 'auth/login', method: 'POST', body }), invalidatesTags: ['Me'] }),
      logout: b.mutation<void, void>({ query: () => ({ url: 'auth/logout', method: 'POST' }), invalidatesTags: ['Me'] }),
      setupStatus: b.query<SetupStatus, void>({ query: () => 'setup/status', providesTags: ['Setup'] }),
      completeSetup: b.mutation<Me, unknown>({
        query: (body) => ({ url: 'setup/complete', method: 'POST', body }), invalidatesTags: ['Me', 'Setup'] }),

      extensions: b.query<List<Extension>, void>({ query: () => 'extensions', providesTags: ['Extensions'] }),
      createExtension: b.mutation<Item<Extension>, Partial<Extension>>({
        query: (body) => ({ url: 'extensions', method: 'POST', body }), invalidatesTags: ['Extensions', 'History', 'Health', 'HotDesks'] }),
      updateExtension: b.mutation<Item<Extension>, Extension>({
        query: (body) => ({ url: `extensions/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Extensions', 'History', 'Health', 'HotDesks'] }),
      deleteExtension: b.mutation<void, string>({
        query: (id) => ({ url: `extensions/${id}`, method: 'DELETE' }), invalidatesTags: ['Extensions', 'History', 'Health', 'HotDesks'] }),
      ringGroups: b.query<List<RingGroup>, void>({ query: () => 'ring-groups', providesTags: ['RingGroups'] }),
      createRingGroup: b.mutation<Item<RingGroup>, Partial<RingGroup>>({
        query: (body) => ({ url: 'ring-groups', method: 'POST', body }), invalidatesTags: ['RingGroups', 'History', 'Health'] }),
      updateRingGroup: b.mutation<Item<RingGroup>, RingGroup>({
        query: (body) => ({ url: `ring-groups/${body.id}`, method: 'PUT', body }), invalidatesTags: ['RingGroups', 'History', 'Health'] }),
      deleteRingGroup: b.mutation<void, string>({
        query: (id) => ({ url: `ring-groups/${id}`, method: 'DELETE' }), invalidatesTags: ['RingGroups', 'History', 'Health'] }),
      receptionists: b.query<List<Receptionist>, void>({ query: () => 'receptionists', providesTags: ['Receptionists'] }),
      createReceptionist: b.mutation<Item<Receptionist>, Partial<Receptionist>>({ query: (body) => ({ url: 'receptionists', method: 'POST', body }), invalidatesTags: ['Receptionists', 'History', 'Health'] }),
      updateReceptionist: b.mutation<Item<Receptionist>, Receptionist>({ query: (body) => ({ url: `receptionists/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Receptionists', 'History', 'Health'] }),
      deleteReceptionist: b.mutation<void, string>({ query: (id) => ({ url: `receptionists/${id}`, method: 'DELETE' }), invalidatesTags: ['Receptionists', 'History', 'Health'] }),
      sites: b.query<List<Site>, void>({ query: () => 'sites', providesTags: ['Sites'] }),
      createSite: b.mutation<Item<Site>, Partial<Site>>({ query: (body) => ({ url: 'sites', method: 'POST', body }), invalidatesTags: ['Sites', 'History', 'Health'] }),
      updateSite: b.mutation<Item<Site>, Site>({ query: (body) => ({ url: `sites/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Sites', 'History', 'Health'] }),
      deleteSite: b.mutation<void, string>({ query: (id) => ({ url: `sites/${id}`, method: 'DELETE' }), invalidatesTags: ['Sites', 'History', 'Health'] }),
      remoteAddress: b.query<{ address?: Address; confirmed?: string }, string>({ query: (ext) => `extensions/${ext}/remote` }),
      faxBoxes: b.query<List<FaxBox>, void>({ query: () => 'fax-boxes', providesTags: ['FaxBoxes'] }),
      createFaxBox: b.mutation<Item<FaxBox>, Partial<FaxBox>>({
        query: (body) => ({ url: 'fax-boxes', method: 'POST', body }), invalidatesTags: ['FaxBoxes', 'History'] }),
      updateFaxBox: b.mutation<Item<FaxBox>, FaxBox>({
        query: (body) => ({ url: `fax-boxes/${body.id}`, method: 'PUT', body }), invalidatesTags: ['FaxBoxes', 'History'] }),
      deleteFaxBox: b.mutation<void, string>({
        query: (id) => ({ url: `fax-boxes/${id}`, method: 'DELETE' }), invalidatesTags: ['FaxBoxes', 'History'] }),
      faxes: b.query<List<FaxRecord>, void>({ query: () => 'faxes', providesTags: ['Faxes'] }),
      deleteFax: b.mutation<void, string>({ query: (id) => ({ url: `faxes/${id}`, method: 'DELETE' }), invalidatesTags: ['Faxes'] }),
      faxKinds: b.query<FaxKinds, void>({ query: () => 'faxes/kinds', providesTags: ['Faxes'] }),
      sendFax: b.mutation<FaxRecord, FormData>({ query: (body) => ({ url: 'faxes/send', method: 'POST', body }), invalidatesTags: ['Faxes'] }),
      retryFax: b.mutation<FaxRecord, string>({ query: (id) => ({ url: `faxes/${id}/retry`, method: 'POST' }), invalidatesTags: ['Faxes'] }),
      psapReport: b.query<AnswerReport, { from?: string; to?: string }>({ query: ({ from, to }) => `psap/report?from=${from ?? ''}&to=${to ?? ''}`, providesTags: ['CDR'] }),
      psapLog: b.query<{ day: string; days: string[]; items: I3Event[] }, string>({ query: (day) => `psap/log?day=${day}`, providesTags: ['CDR'] }),
      rebid: b.mutation<{ location: Location }, string>({ query: (id) => ({ url: `calls/${id}/rebid`, method: 'POST' }), invalidatesTags: ['Calls'] }),
      boundaries: b.query<List<Boundary>, void>({ query: () => 'core/boundaries', providesTags: ['Settings'] }),
      saveBoundary: b.mutation<void, Boundary>({ query: (x) => ({ url: `core/boundaries/${x.id}`, method: 'PUT', body: x }), invalidatesTags: ['Settings'] }),
      deleteBoundary: b.mutation<void, string>({ query: (id) => ({ url: `core/boundaries/${id}`, method: 'DELETE' }), invalidatesTags: ['Settings'] }),
      addressData: b.query<{ streets: number; points: number; updated?: string }, void>({ query: () => 'core/addresses', providesTags: ['Settings'] }),
      deleteAddresses: b.mutation<void, void>({ query: () => ({ url: 'core/addresses', method: 'DELETE' }), invalidatesTags: ['Settings'] }),
      validateAddress: b.mutation<Validation, Record<string, string>>({ query: (body) => ({ url: 'core/validate', method: 'POST', body }) }),
      bcfLog: b.query<List<BCFEvent>, void>({ query: () => 'core/bcf', providesTags: ['Calls'] }),
      findArea: b.query<{ found: boolean; why?: string; area?: Boundary }, Record<string, string>>({ query: (q) => `core/find?${new URLSearchParams(q)}` }),
      radio: b.query<ConsoleState, void>({ query: () => 'radio', providesTags: ['Calls'] }),
      ptt: b.mutation<void, { call: string; channel: string; on: boolean }>({ query: (body) => ({ url: 'radio/ptt', method: 'POST', body }) }),
      monitor: b.mutation<void, { call: string; channels: string[] | null }>({ query: (body) => ({ url: 'radio/monitor', method: 'POST', body }) }),
      patch: b.mutation<void, { a: string; b: string; on: boolean }>({ query: (body) => ({ url: 'radio/patch', method: 'POST', body }) }),
      alertTone: b.mutation<void, { call: string; channel: string; tone: string }>({ query: (body) => ({ url: 'radio/tone', method: 'POST', body }) }),
      unitCall: b.mutation<{ channel: string }, { call: string; unit: number; emergency: boolean }>({ query: (body) => ({ url: 'radio/unitcall', method: 'POST', body }) }),
      hangUp: b.mutation<void, { channel: string }>({ query: (body) => ({ url: 'radio/hangup', method: 'POST', body }) }),
      radioLog: b.query<{ items: Transmission[] }, { channel?: string; q?: string }>({ query: (a) => ({ url: 'radio/log', params: { channel: a.channel || undefined, q: a.q || undefined, limit: 200 } }), providesTags: ['Calls'] }),
      testFaxMail: b.mutation<{ result: string }, FaxMail>({ query: (body) => ({ url: 'faxes/mail/test', method: 'POST', body }) }),
      pageGroups: b.query<List<PageGroup>, void>({ query: () => 'page-groups', providesTags: ['PageGroups'] }),
      conferences: b.query<List<Conference>, void>({ query: () => 'conferences', providesTags: ['Conferences'] }),
      createConference: b.mutation<Item<Conference>, Partial<Conference>>({
        query: (body) => ({ url: 'conferences', method: 'POST', body }), invalidatesTags: ['Conferences', 'History'] }),
      updateConference: b.mutation<Item<Conference>, Conference>({
        query: (body) => ({ url: `conferences/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Conferences', 'History'] }),
      deleteConference: b.mutation<void, string>({
        query: (id) => ({ url: `conferences/${id}`, method: 'DELETE' }), invalidatesTags: ['Conferences', 'History'] }),
      directories: b.query<List<Directory>, void>({ query: () => 'directories', providesTags: ['Directories'] }),
      createDirectory: b.mutation<Item<Directory>, Partial<Directory>>({ query: (body) => ({ url: 'directories', method: 'POST', body }), invalidatesTags: ['Directories', 'History'] }),
      updateDirectory: b.mutation<Item<Directory>, Directory>({ query: (body) => ({ url: `directories/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Directories', 'History'] }),
      deleteDirectory: b.mutation<void, string>({ query: (id) => ({ url: `directories/${id}`, method: 'DELETE' }), invalidatesTags: ['Directories', 'History'] }),
      dialIns: b.query<List<DialIn>, void>({ query: () => 'dial-ins', providesTags: ['DialIns'] }),
      createDialIn: b.mutation<Item<DialIn>, Partial<DialIn>>({ query: (body) => ({ url: 'dial-ins', method: 'POST', body }), invalidatesTags: ['DialIns', 'History'] }),
      updateDialIn: b.mutation<Item<DialIn>, DialIn>({ query: (body) => ({ url: `dial-ins/${body.id}`, method: 'PUT', body }), invalidatesTags: ['DialIns', 'History'] }),
      deleteDialIn: b.mutation<void, string>({ query: (id) => ({ url: `dial-ins/${id}`, method: 'DELETE' }), invalidatesTags: ['DialIns', 'History'] }),
      hotel: b.query<HotelView, void>({ query: () => 'hotel', providesTags: ['Hotel', 'WakeUps', 'Extensions'] }),
      checkIn: b.mutation<void, { room: string; name: string; account: string; language: string }>({
        query: ({ room, ...body }) => ({ url: `hotel/rooms/${room}/checkin`, method: 'POST', body }), invalidatesTags: ['Hotel'] }),
      checkOut: b.mutation<void, string>({ query: (room) => ({ url: `hotel/rooms/${room}/checkout`, method: 'POST' }), invalidatesTags: ['Hotel', 'WakeUps'] }),
      setRoomStatus: b.mutation<void, { room: string; status: string }>({
        query: ({ room, status }) => ({ url: `hotel/rooms/${room}/status`, method: 'PUT', body: { status } }), invalidatesTags: ['Hotel'] }),
      wakeUps: b.query<List<WakeUp>, void>({ query: () => 'wakeups', providesTags: ['WakeUps'] }),
      setWakeUp: b.mutation<WakeUp, { ext: string; at: string; daily: boolean }>({
        query: ({ ext, ...body }) => ({ url: `wakeups/${ext}`, method: 'PUT', body }), invalidatesTags: ['WakeUps', 'Hotel'] }),
      cancelWakeUp: b.mutation<void, string>({ query: (ext) => ({ url: `wakeups/${ext}`, method: 'DELETE' }), invalidatesTags: ['WakeUps', 'Hotel'] }),
      saveNameAudio: b.mutation<{ seconds: number }, { ext: string; wav: Blob }>({
        query: ({ ext, wav }) => ({ url: `extensions/${ext}/name-audio`, method: 'PUT', body: wav, headers: { 'Content-Type': 'audio/wav' } }), invalidatesTags: ['Voicemail'] }),
      deleteNameAudio: b.mutation<void, string>({ query: (ext) => ({ url: `extensions/${ext}/name-audio`, method: 'DELETE' }), invalidatesTags: ['Voicemail'] }),
      conferenceLive: b.query<{ items: ConferenceRoom[] }, void>({ query: () => 'conferences/live', providesTags: ['ConferenceLive'] }),
      lockConference: b.mutation<void, { number: string; locked: boolean }>({
        query: ({ number, locked }) => ({ url: `conferences/live/${number}`, method: 'PUT', body: { locked } }), invalidatesTags: ['ConferenceLive'] }),
      muteConferenceMember: b.mutation<void, { number: string; id: string; muted: boolean }>({
        query: ({ number, id, muted }) => ({ url: `conferences/live/${number}/members/${id}`, method: 'PUT', body: { muted } }), invalidatesTags: ['ConferenceLive'] }),
      removeConferenceMember: b.mutation<void, { number: string; id: string }>({
        query: ({ number, id }) => ({ url: `conferences/live/${number}/members/${id}`, method: 'DELETE' }), invalidatesTags: ['ConferenceLive'] }),
      diverts: b.query<{ items: Divert[] }, void>({ query: () => 'diverts', providesTags: ['Diverts'] }),
      divert: b.query<Divert, string>({ query: (ext) => `diverts/${ext}`, providesTags: ['Diverts'] }),
      setDivert: b.mutation<void, Partial<Divert> & { ext: string }>({
        query: ({ ext, ...body }) => ({ url: `diverts/${ext}`, method: 'PUT', body }), invalidatesTags: ['Diverts', 'Away'] }),
      createPageGroup: b.mutation<Item<PageGroup>, Partial<PageGroup>>({
        query: (body) => ({ url: 'page-groups', method: 'POST', body }), invalidatesTags: ['PageGroups', 'History'] }),
      updatePageGroup: b.mutation<Item<PageGroup>, PageGroup>({
        query: (body) => ({ url: `page-groups/${body.id}`, method: 'PUT', body }), invalidatesTags: ['PageGroups', 'History'] }),
      deletePageGroup: b.mutation<void, string>({
        query: (id) => ({ url: `page-groups/${id}`, method: 'DELETE' }), invalidatesTags: ['PageGroups', 'History'] }),
      queues: b.query<List<Queue>, void>({ query: () => 'queues', providesTags: ['Queues'] }),
      createQueue: b.mutation<Item<Queue>, Partial<Queue>>({
        query: (body) => ({ url: 'queues', method: 'POST', body }), invalidatesTags: ['Queues', 'QueueStatus', 'History', 'Health'] }),
      updateQueue: b.mutation<Item<Queue>, Queue>({
        query: (body) => ({ url: `queues/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Queues', 'QueueStatus', 'History', 'Health'] }),
      deleteQueue: b.mutation<void, string>({
        query: (id) => ({ url: `queues/${id}`, method: 'DELETE' }), invalidatesTags: ['Queues', 'QueueStatus', 'History', 'Health'] }),
      queueStatus: b.query<List<QueueStatus>, void>({ query: () => 'queues/status', providesTags: ['QueueStatus'] }),
      scripts: b.query<List<Script>, void>({ query: () => 'scripts', providesTags: ['Scripts'] }),
      createScript: b.mutation<Item<Script>, Partial<Script>>({
        query: (body) => ({ url: 'scripts', method: 'POST', body }), invalidatesTags: ['Scripts', 'History', 'Health'] }),
      updateScript: b.mutation<Item<Script>, Script>({
        query: (body) => ({ url: `scripts/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Scripts', 'History', 'Health'] }),
      deleteScript: b.mutation<void, string>({
        query: (id) => ({ url: `scripts/${id}`, method: 'DELETE' }), invalidatesTags: ['Scripts', 'History', 'Health'] }),
      testScript: b.mutation<ScriptTest, { language: string; source: string; call: ScriptCall }>({
        query: (body) => ({ url: 'scripts/test', method: 'POST', body }) }),
      menus: b.query<List<Menu>, void>({ query: () => 'menus', providesTags: ['Menus'] }),
      createMenu: b.mutation<Item<Menu>, Partial<Menu>>({
        query: (body) => ({ url: 'menus', method: 'POST', body }), invalidatesTags: ['Menus', 'History', 'Health'] }),
      updateMenu: b.mutation<Item<Menu>, Menu>({
        query: (body) => ({ url: `menus/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Menus', 'History', 'Health'] }),
      deleteMenu: b.mutation<void, string>({
        query: (id) => ({ url: `menus/${id}`, method: 'DELETE' }), invalidatesTags: ['Menus', 'History', 'Health'] }),
      savePrompt: b.mutation<{ seconds: number }, { id: string; wav: Blob }>({
        query: ({ id, wav }) => ({ url: `menus/${id}/prompt`, method: 'PUT', body: wav, headers: { 'Content-Type': 'audio/wav' } }),
        invalidatesTags: ['Voicemail'] }),
      putNotice: b.mutation<{ seconds: number }, Blob>({ query: (wav) => ({ url: 'recording/notice', method: 'PUT', body: wav, headers: { 'Content-Type': 'audio/wav' } }), invalidatesTags: ['Voicemail', 'Health'] }),
      deleteNotice: b.mutation<void, void>({ query: () => ({ url: 'recording/notice', method: 'DELETE' }), invalidatesTags: ['Voicemail', 'Health'] }),
      resetPrompt: b.mutation<void, string>({ query: (id) => ({ url: `menus/${id}/prompt`, method: 'DELETE' }), invalidatesTags: ['Voicemail'] }),
      schedules: b.query<List<Schedule>, void>({ query: () => 'schedules', providesTags: ['Schedules'] }),
      createSchedule: b.mutation<Item<Schedule>, Partial<Schedule>>({
        query: (body) => ({ url: 'schedules', method: 'POST', body }), invalidatesTags: ['Schedules', 'History', 'Health'] }),
      updateSchedule: b.mutation<Item<Schedule>, Schedule>({
        query: (body) => ({ url: `schedules/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Schedules', 'History', 'Health'] }),
      deleteSchedule: b.mutation<void, string>({
        query: (id) => ({ url: `schedules/${id}`, method: 'DELETE' }), invalidatesTags: ['Schedules', 'History', 'Health'] }),
      devices: b.query<List<Device>, void>({ query: () => 'devices', providesTags: ['Devices'] }),
      createDevice: b.mutation<Item<Device>, Partial<Device>>({
        query: (body) => ({ url: 'devices', method: 'POST', body }), invalidatesTags: ['Devices', 'History'] }),
      updateDevice: b.mutation<Item<Device>, Device>({
        query: (body) => ({ url: `devices/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Devices', 'History'] }),
      deleteDevice: b.mutation<void, string>({
        query: (id) => ({ url: `devices/${id}`, method: 'DELETE' }), invalidatesTags: ['Devices', 'History'] }),
      trunks: b.query<List<Trunk>, void>({ query: () => 'trunks', providesTags: ['Trunks'] }),
      createTrunk: b.mutation<Item<Trunk>, Partial<Trunk>>({
        query: (body) => ({ url: 'trunks', method: 'POST', body }), invalidatesTags: ['Trunks', 'History', 'Health'] }),
      updateTrunk: b.mutation<Item<Trunk>, Trunk>({
        query: (body) => ({ url: `trunks/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Trunks', 'History', 'Health'] }),
      deleteTrunk: b.mutation<void, string>({
        query: (id) => ({ url: `trunks/${id}`, method: 'DELETE' }), invalidatesTags: ['Trunks', 'History', 'Health'] }),
      inboundRoutes: b.query<List<InboundRoute>, void>({ query: () => 'inbound-routes', providesTags: ['Inbound'] }),
      createInbound: b.mutation<Item<InboundRoute>, Partial<InboundRoute>>({
        query: (body) => ({ url: 'inbound-routes', method: 'POST', body }), invalidatesTags: ['Inbound', 'History', 'Health'] }),
      updateInbound: b.mutation<Item<InboundRoute>, InboundRoute>({
        query: (body) => ({ url: `inbound-routes/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Inbound', 'History', 'Health'] }),
      deleteInbound: b.mutation<void, string>({
        query: (id) => ({ url: `inbound-routes/${id}`, method: 'DELETE' }), invalidatesTags: ['Inbound', 'History', 'Health'] }),
      outboundRoutes: b.query<List<OutboundRoute>, void>({ query: () => 'outbound-routes', providesTags: ['Outbound'] }),
      createOutbound: b.mutation<Item<OutboundRoute>, Partial<OutboundRoute>>({
        query: (body) => ({ url: 'outbound-routes', method: 'POST', body }), invalidatesTags: ['Outbound', 'History', 'Health'] }),
      updateOutbound: b.mutation<Item<OutboundRoute>, OutboundRoute>({
        query: (body) => ({ url: `outbound-routes/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Outbound', 'History', 'Health'] }),
      deleteOutbound: b.mutation<void, string>({
        query: (id) => ({ url: `outbound-routes/${id}`, method: 'DELETE' }), invalidatesTags: ['Outbound', 'History', 'Health'] }),
      nextExtension: b.query<{ number: string; secret: string }, void>({ query: () => 'extensions/next-number', keepUnusedDataFor: 0 }),


      trunkStatus: b.query<List<TrunkStatus>, void>({ query: () => 'trunks/status', providesTags: ['TrunkStatus'] }),


      reorderOutbound: b.mutation<void, string[]>({
        query: (body) => ({ url: 'outbound-routes/order', method: 'POST', body }), invalidatesTags: ['Outbound', 'History'] }),

      settings: b.query<Settings, void>({ query: () => 'settings', providesTags: ['Settings'] }),
      saveSettings: b.mutation<Settings, Settings>({
        query: (body) => ({ url: 'settings', method: 'PUT', body }), invalidatesTags: ['Settings', 'History', 'Health', 'Costs', 'HotDesks', 'System'] }),

      users: b.query<List<User>, void>({ query: () => 'users', providesTags: ['Users'] }),
      saveUser: b.mutation<User, { username: string; name: string; role: Role; password: string; existing: boolean }>({
        query: ({ existing, ...body }) => existing
          ? { url: `users/${body.username}`, method: 'PUT', body }
          : { url: 'users', method: 'POST', body },
        invalidatesTags: ['Users'] }),
      deleteUser: b.mutation<void, string>({ query: (u) => ({ url: `users/${u}`, method: 'DELETE' }), invalidatesTags: ['Users'] }),
      changePassword: b.mutation<void, { current: string; new: string }>({
        query: (body) => ({ url: 'auth/password', method: 'PUT', body }) }),

      calls: b.query<List<Call> & { elsewhere?: (ClusterCall & { server: string })[] }, void>({ query: () => 'calls', providesTags: ['Calls'] }),
      hangup: b.mutation<void, string>({ query: (id) => ({ url: `calls/${id}`, method: 'DELETE' }), invalidatesTags: ['Calls'] }),
      openDoor: b.mutation<void, string>({ query: (id) => ({ url: `calls/${id}/door`, method: 'POST' }) }),
      registrations: b.query<List<Registration>, void>({ query: () => 'registrations', providesTags: ['Registrations'] }),
      stats: b.query<Stats, void>({ query: () => 'stats', providesTags: ['Stats'] }),
      cdr: b.query<List<CDR>, { search?: string; direction?: string }>({
        query: (p) => ({ url: 'cdr', params: { limit: 1000, ...p } }), providesTags: ['CDR'] }),
      health: b.query<List<Check>, void>({ query: () => 'health', providesTags: ['Health'] }),
      history: b.query<List<Revision>, void>({ query: () => 'history', providesTags: ['History'] }),
      restore: b.mutation<void, number>({
        query: (rev) => ({ url: `history/${rev}/restore`, method: 'POST' }),
        invalidatesTags: ['History', 'Extensions', 'RingGroups', 'Trunks', 'Inbound', 'Outbound', 'Settings', 'Health'] }),
      importConfig: b.mutation<void, unknown>({
        query: (body) => ({ url: 'config/import', method: 'POST', body }),
        invalidatesTags: ['History', 'Extensions', 'RingGroups', 'Trunks', 'Inbound', 'Outbound', 'Settings', 'Health'] }),
      bans: b.query<List<Ban>, void>({ query: () => 'security/bans', providesTags: ['Bans'] }),
      unban: b.mutation<void, string>({ query: (ip) => ({ url: `security/bans/${ip}`, method: 'DELETE' }), invalidatesTags: ['Bans', 'Health'] }),
      audit: b.query<List<AuditEntry>, void>({ query: () => 'audit', providesTags: ['Audit'] }),
      trace: b.query<List<TraceEntry>, string>({ query: (q) => ({ url: 'sip-trace', params: { q } }), providesTags: ['Trace'] }),
      system: b.query<SystemInfo, void>({ query: () => 'system', providesTags: ['System'] }),
      mobileDevices: b.query<List<MobileDevice>, string>({ query: (ext) => `extensions/${ext}/devices`, providesTags: ['Mobile'] }),
      pairMobile: b.mutation<{ code: string; server: string; link: string; expires: string; push: boolean; local: boolean }, string>({ query: (ext) => ({ url: `extensions/${ext}/pair`, method: 'POST' }) }),
      unpairMobile: b.mutation<void, string>({ query: (id) => ({ url: `mobile/devices/${id}`, method: 'DELETE' }), invalidatesTags: ['Mobile'] }),
      emergencyMode: b.mutation<unknown, { on: boolean }>({ query: (body) => ({ url: 'emergency-mode', method: 'POST', body }), invalidatesTags: ['Settings', 'Audit'] }),
      tokens: b.query<List<ApiToken>, void>({ query: () => 'tokens', providesTags: ['Tokens'] }),
      addToken: b.mutation<{ token: ApiToken; secret: string }, { name: string; role: string; days: number }>({ query: (body) => ({ url: 'tokens', method: 'POST', body }), invalidatesTags: ['Tokens', 'Audit'] }),
      revokeToken: b.mutation<void, string>({ query: (id) => ({ url: `tokens/${id}`, method: 'DELETE' }), invalidatesTags: ['Tokens', 'Audit'] }),
      gitOps: b.query<{ last?: string; ok: boolean; error?: string; changes: Change[]; appliedRev?: number }, void>({ query: () => 'gitops', providesTags: ['GitOps'] }),
      syncGitOps: b.mutation<unknown, void>({ query: () => ({ url: 'gitops/sync', method: 'POST' }), invalidatesTags: ['GitOps', 'Settings', 'History'] }),
      setAgent: b.mutation<unknown, { ext: string; loggedOut: boolean; paused: boolean; reason?: string }>({ query: ({ ext, ...body }) => ({ url: `agents/${ext}`, method: 'POST', body }), invalidatesTags: ['QueueStatus'] }),
      supervise: b.mutation<unknown, { id: string; supervisor: string; mode: string }>({ query: ({ id, ...body }) => ({ url: `calls/${id}/supervise`, method: 'POST', body }) }),
      setSupervision: b.mutation<unknown, { id: string; mode: string }>({ query: ({ id, ...body }) => ({ url: `calls/${id}/supervise`, method: 'PUT', body }) }),
      compatibility: b.query<{ tested: { kind: string; vendor: string; models: string; provision: boolean; notes: string }[]; seen: { userAgent: string; vendor: string; extensions: string[]; first: string; last: string; count: number }[]; trunks: TrunkStatus[] }, void>({ query: () => 'compatibility', providesTags: ['Registrations'] }),
      checkVault: b.mutation<{ ok: boolean; length: number }, { ref: string }>({ query: (body) => ({ url: 'vault/check', method: 'POST', body }) }),
      ask: b.mutation<{ id: string; explanation: string; changes: Change[] }, { request: string }>({ query: (body) => ({ url: 'ask', method: 'POST', body }) }),
      applyAsk: b.mutation<{ rev: number }, string>({ query: (id) => ({ url: `ask/${id}/apply`, method: 'POST' }), invalidatesTags: ['History', 'Settings', 'Extensions', 'RingGroups', 'Queues', 'Menus', 'Schedules', 'Scripts', 'Inbound', 'Outbound', 'PageGroups', 'Health'] }),
      transcript: b.query<CallTranscript, string>({ query: (name) => `recordings/${name}/transcript`, providesTags: (_r, _e, name) => [{ type: 'Transcripts', id: name }] }),
      makeTranscript: b.mutation<{ status: string }, string>({ query: (name) => ({ url: `recordings/${name}/transcript`, method: 'POST' }), invalidatesTags: (_r, _e, name) => [{ type: 'Transcripts', id: name }] }),
      searchTranscripts: b.query<List<CallTranscript>, string>({ query: (q) => `transcripts?q=${encodeURIComponent(q)}`, providesTags: ['Transcripts'] }),
      quality: b.query<QualityReport, number>({ query: (days) => `quality?days=${days}`, providesTags: ['Quality'] }),
      simulate: b.mutation<SimResult, SimInput>({ query: (body) => ({ url: 'simulate', method: 'POST', body }) }),
      caller: b.query<CallerCard, string>({ query: (n) => `callers/${encodeURIComponent(n)}`, keepUnusedDataFor: 60 }),
      costs: b.query<CostReport, string>({ query: (month) => `costs?month=${month}`, providesTags: ['Costs'] }),
      price: b.mutation<{ found: boolean; rate?: Rate; cost?: number }, { trunk: Trunk; number: string; seconds: number }>({ query: (body) => ({ url: 'costs/price', method: 'POST', body }) }),
      directory: b.query<List<DirContact>, void>({ query: () => 'directory', providesTags: ['Directory'] }),
      addContact: b.mutation<DirContact, DirContact>({ query: (body) => ({ url: 'directory', method: 'POST', body }), invalidatesTags: ['Directory'] }),
      saveContact: b.mutation<DirContact, DirContact>({ query: (body) => ({ url: `directory/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Directory'] }),
      deleteContact: b.mutation<void, string>({ query: (id) => ({ url: `directory/${id}`, method: 'DELETE' }), invalidatesTags: ['Directory'] }),
      hotDesks: b.query<List<{ number: string; name: string; in: HotDesk | null }>, void>({ query: () => 'hotdesks', providesTags: ['HotDesks'] }),
      hotDeskSignIn: b.mutation<HotDesk, { desk: string; user: string }>({ query: ({ desk, user }) => ({ url: `hotdesks/${desk}`, method: 'POST', body: { user } }), invalidatesTags: ['HotDesks'] }),
      hotDeskSignOut: b.mutation<void, string>({ query: (desk) => ({ url: `hotdesks/${desk}`, method: 'DELETE' }), invalidatesTags: ['HotDesks'] }),
      textingKinds: b.query<SMSKind[], void>({ query: () => 'texting/kinds', transformResponse: (r: { items: SMSKind[] }) => r.items }),
      conversations: b.query<List<Conversation>, void>({ query: () => 'texts', providesTags: ['Texts'] }),
      thread: b.query<{ items: TextMsg[]; optedOut: boolean }, { our: string; other: string }>({ query: ({ our, other }) => `texts/thread?our=${encodeURIComponent(our)}&other=${encodeURIComponent(other)}`, providesTags: ['Texts'] }),
      sendText: b.mutation<TextMsg, { from: string; to: string; text: string }>({ query: (body) => ({ url: 'texts', method: 'POST', body }), invalidatesTags: ['Texts'] }),
      markRead: b.mutation<void, { our: string; other: string }>({ query: (body) => ({ url: 'texts/read', method: 'POST', body }), invalidatesTags: ['Texts'] }),
      timeline: b.query<Timeline, string>({ query: (n) => `timeline?number=${encodeURIComponent(n)}`, providesTags: ['Timeline'] }),
      addNote: b.mutation<unknown, { number: string; text: string }>({ query: (body) => ({ url: 'timeline/notes', method: 'POST', body }), invalidatesTags: ['Timeline'] }),
      deleteNote: b.mutation<void, string>({ query: (id) => ({ url: `timeline/notes/${id}`, method: 'DELETE' }), invalidatesTags: ['Timeline'] }),
      makeInsight: b.mutation<CallTranscript, string>({ query: (name) => ({ url: `recordings/${name}/insight`, method: 'POST' }), invalidatesTags: ['Transcripts'] }),
      insight: b.query<{ days: number; agents: AgentTalk[] }, number>({ query: (d) => `insight?days=${d}`, providesTags: ['Transcripts'] }),
      reputation: b.query<{ days: number; numbers: NumberReputation[] }, number>({ query: (days) => `reputation?days=${days}`, providesTags: ['Reputation', 'Settings'] }),
      certificate: b.query<{ subject: string; issuer: string; names: string[]; notAfter: string; selfSigned: boolean }, void>({ query: () => 'certificate', providesTags: ['Certificate'] }),
      installCertificate: b.mutation<{ subject: string; issuer: string; names: string[]; notAfter: string; selfSigned: boolean }, { cert: string; key: string }>({ query: (body) => ({ url: 'certificate', method: 'PUT', body }), invalidatesTags: ['Certificate', 'Health'] }),
      database: b.query<{ kind: 'sqlite' | 'postgres'; where: string; bytes?: number; fixed: boolean; counts?: Record<string, number> }, void>({ query: () => 'database', providesTags: ['Database'] }),
      testDatabase: b.mutation<{ where: string; empty: boolean }, { url: string }>({ query: (body) => ({ url: 'database/test', method: 'POST', body }) }),
      moveDatabase: b.mutation<{ where: string; counts: Record<string, number>; restarting: boolean }, { url: string }>({ query: (body) => ({ url: 'database/move', method: 'POST', body }), invalidatesTags: ['Database'] }),
      servers: b.query<{ shared: boolean; postgres: boolean; self?: ClusterNode; nodes?: ClusterNode[]; address?: SharedAddress }, void>({ query: () => 'servers', providesTags: ['Servers'] }),
      serversDns: b.query<DnsCheck, void>({ query: () => 'servers/dns', providesTags: ['Servers'] }),
      setServer: b.mutation<ClusterNode, { name: string; sipAddr: string }>({ query: (body) => ({ url: 'servers/self', method: 'PUT', body }), invalidatesTags: ['Servers'] }),
      forgetServer: b.mutation<void, string>({ query: (id) => ({ url: `servers/${id}`, method: 'DELETE' }), invalidatesTags: ['Servers', 'Health'] }),
      away: b.query<{ items: { ext: string; reason: string; until?: string; source: 'calendar' | 'manual'; offSite?: boolean }[]; problems: Record<string, string> }, void>({ query: () => 'away', providesTags: ['Away'] }),
      setAway: b.mutation<void, { ext: string; on: boolean; reason?: string; until?: string }>({ query: ({ ext, ...body }) => ({ url: `away/${ext}`, method: 'PUT', body }), invalidatesTags: ['Away', 'QueueStatus'] }),
      checkCalendar: b.mutation<{ events: number; next: { start: string; end: string; summary: string; away: boolean }[] }, { secret: string; ignoreMeetings?: boolean }>({ query: (body) => ({ url: 'away/check', method: 'POST', body }) }),
      metricsTest: b.mutation<{ ok: boolean }, void>({ query: () => ({ url: 'metrics/test', method: 'POST' }) }),
      crmTest: b.mutation<{ ok: boolean; message: string }, { crm: CRM; number?: string; what: 'lookup' | 'log' }>({ query: (body) => ({ url: 'crm/test', method: 'POST', body }) }),
      callbacks: b.query<List<Callback>, void>({ query: () => 'callbacks', providesTags: ['Callbacks'] }),
      addCallback: b.mutation<Callback, CallbackInput>({ query: (body) => ({ url: 'callbacks', method: 'POST', body }), invalidatesTags: ['Callbacks'] }),
      setCallback: b.mutation<Callback, { id: string; status: Callback['status']; note: string }>({ query: ({ id, ...body }) => ({ url: `callbacks/${id}`, method: 'PUT', body }), invalidatesTags: ['Callbacks'] }),
      deleteCallback: b.mutation<void, string>({ query: (id) => ({ url: `callbacks/${id}`, method: 'DELETE' }), invalidatesTags: ['Callbacks'] }),
      ringBack: b.mutation<{ call: string }, { id: string; extension: string }>({ query: ({ id, extension }) => ({ url: `callbacks/${id}/call`, method: 'POST', body: { extension } }) }),
      holds: b.query<List<Hold>, void>({ query: () => 'holds', providesTags: ['Holds'] }),
      addHold: b.mutation<Hold, HoldInput>({ query: (body) => ({ url: 'holds', method: 'POST', body }), invalidatesTags: ['Holds', 'CDR'] }),
      updateHold: b.mutation<Hold, HoldInput & { id: string }>({ query: ({ id, ...body }) => ({ url: `holds/${id}`, method: 'PUT', body }), invalidatesTags: ['Holds', 'CDR'] }),
      releaseHold: b.mutation<void, string>({ query: (id) => ({ url: `holds/${id}/release`, method: 'POST' }), invalidatesTags: ['Holds', 'CDR'] }),
      pauseRecording: b.mutation<void, { id: string; paused: boolean }>({ query: ({ id, paused }) => ({ url: `calls/${id}/recording`, method: 'POST', body: { paused } }) }),
      loadTest: b.query<LoadStatus, void>({ query: () => 'loadtest', providesTags: ['LoadTest'] }),
      startLoadTest: b.mutation<LoadStatus, unknown>({ query: (body) => ({ url: 'loadtest', method: 'POST', body }), invalidatesTags: ['LoadTest'] }),
      stopLoadTest: b.mutation<LoadStatus, void>({ query: () => ({ url: 'loadtest/stop', method: 'POST' }), invalidatesTags: ['LoadTest'] }),
      upgrade: b.query<UpgradeStatus, void>({ query: () => 'system/upgrade', providesTags: ['Upgrade'] }),
      uploadUpgrade: b.mutation<UpgradeStatus, FormData>({ query: (body) => ({ url: 'system/upgrade', method: 'POST', body }), invalidatesTags: ['Upgrade'] }),
      applyUpgrade: b.mutation<UpgradeStatus, { when: 'now' | 'idle' }>({ query: (body) => ({ url: 'system/upgrade/apply', method: 'POST', body }), invalidatesTags: ['Upgrade'] }),
      rollbackUpgrade: b.mutation<UpgradeStatus, { when: 'now' | 'idle' }>({ query: (body) => ({ url: 'system/upgrade/rollback', method: 'POST', body }), invalidatesTags: ['Upgrade'] }),
      cancelUpgrade: b.mutation<UpgradeStatus, void>({ query: () => ({ url: 'system/upgrade/cancel', method: 'POST' }), invalidatesTags: ['Upgrade'] }),
      voicemail: b.query<List<Voicemail> & { greetings: string[] }, void>({ query: () => 'voicemail', providesTags: ['Voicemail'] }),
      markHeard: b.mutation<Voicemail, { box: string; id: string; heard: boolean }>({
        query: ({ box, id, heard }) => ({ url: `voicemail/${box}/${id}`, method: 'PATCH', body: { heard } }), invalidatesTags: ['Voicemail'] }),
      deleteVoicemail: b.mutation<void, { box: string; id: string }>({
        query: ({ box, id }) => ({ url: `voicemail/${box}/${id}`, method: 'DELETE' }), invalidatesTags: ['Voicemail'] }),
      saveGreeting: b.mutation<{ seconds: number }, { box: string; wav: Blob }>({
        query: ({ box, wav }) => ({ url: `voicemail/${box}/greeting`, method: 'PUT', body: wav, headers: { 'Content-Type': 'audio/wav' } }),
        invalidatesTags: ['Voicemail'] }),
      prompts: b.query<PromptsList, void>({ query: () => 'prompts', providesTags: ['Prompts', 'Settings'] }),
      // music lists the tracks of the classes in the settings, so a saved class shows at once
      saveLangPrompt: b.mutation<{ seconds: number }, { lang: string; key: string; wav: Blob }>({
        query: ({ lang, key, wav }) => ({ url: `prompts/${lang}/${key}`, method: 'PUT', body: wav, headers: { 'Content-Type': 'audio/wav' } }),
        invalidatesTags: ['Prompts'] }),
      deleteLangPrompt: b.mutation<void, { lang: string; key: string }>({
        query: ({ lang, key }) => ({ url: `prompts/${lang}/${key}`, method: 'DELETE' }), invalidatesTags: ['Prompts'] }),
      generatePrompts: b.mutation<{ made: number; skipped: number; failed: string[] | null }, { lang: string; overwrite: boolean }>({
        query: ({ lang, overwrite }) => ({ url: `prompts/${lang}/generate`, method: 'POST', body: { overwrite } }), invalidatesTags: ['Prompts'] }),
      music: b.query<Record<string, { name: string; seconds: number }[]>, void>({ query: () => 'music', providesTags: ['Music', 'Settings'] }),
      addMusic: b.mutation<{ seconds: number }, { cls: string; name: string; wav: Blob }>({
        query: ({ cls, name, wav }) => ({ url: `music/${cls}/${name}`, method: 'PUT', body: wav, headers: { 'Content-Type': 'audio/wav' } }),
        invalidatesTags: ['Music'] }),
      deleteMusic: b.mutation<void, { cls: string; name: string }>({
        query: ({ cls, name }) => ({ url: `music/${cls}/${name}`, method: 'DELETE' }), invalidatesTags: ['Music'] }),
      resetGreeting: b.mutation<void, string>({
        query: (box) => ({ url: `voicemail/${box}/greeting`, method: 'DELETE' }), invalidatesTags: ['Voicemail'] }),
      portalLogin: b.mutation<Me, { extension: string; pin: string }>({
        query: (body) => ({ url: 'portal/login', method: 'POST', body }), invalidatesTags: ['Me', 'Myself'] }),
      myself: b.query<Myself, void>({ query: () => 'me', providesTags: ['Myself'] }),
      setMyDivert: b.mutation<void, Partial<Divert>>({ query: (body) => ({ url: 'me/divert', method: 'PUT', body }), invalidatesTags: ['Myself'] }),
      saveMySettings: b.mutation<{ rev: number }, { followMe?: FollowMe; speedDials?: SpeedDial[]; noIntercom?: boolean }>({
        query: (body) => ({ url: 'me/settings', method: 'PUT', body }), invalidatesTags: ['Myself'] }),
      changeMyPin: b.mutation<void, { old: string; new: string }>({ query: (body) => ({ url: 'me/pin', method: 'PUT', body }) }),
      myVoicemail: b.query<{ items: Voicemail[]; greeting: boolean }, void>({ query: () => 'me/voicemail', providesTags: ['MyVoicemail'] }),
      markMyVoicemail: b.mutation<Voicemail, { id: string; heard: boolean }>({
        query: ({ id, heard }) => ({ url: `me/voicemail/${id}`, method: 'PATCH', body: { heard } }), invalidatesTags: ['MyVoicemail', 'Myself'] }),
      deleteMyVoicemail: b.mutation<void, string>({ query: (id) => ({ url: `me/voicemail/${id}`, method: 'DELETE' }), invalidatesTags: ['MyVoicemail', 'Myself'] }),
      saveMyGreeting: b.mutation<{ seconds: number }, Blob>({
        query: (wav) => ({ url: 'me/greeting', method: 'PUT', body: wav, headers: { 'Content-Type': 'audio/wav' } }), invalidatesTags: ['MyVoicemail'] }),
      resetMyGreeting: b.mutation<void, void>({ query: () => ({ url: 'me/greeting', method: 'DELETE' }), invalidatesTags: ['MyVoicemail'] }),
      myCalls: b.query<{ items: CDR[] }, void>({ query: () => 'me/calls' }),
      tenants: b.query<{ items: TenantView[] }, void>({ query: () => 'tenants', providesTags: ['Tenants'] }),
      createTenant: b.mutation<TenantView, Partial<Tenant> & { admin: { username: string; name: string; password: string }; country?: string }>({
        query: (body) => ({ url: 'tenants', method: 'POST', body }), invalidatesTags: ['Tenants'] }),
      updateTenant: b.mutation<TenantView, Tenant>({ query: (body) => ({ url: `tenants/${body.id}`, method: 'PUT', body }), invalidatesTags: ['Tenants'] }),
      deleteTenant: b.mutation<void, string>({ query: (id) => ({ url: `tenants/${id}`, method: 'DELETE' }), invalidatesTags: ['Tenants'] }),
      openTenant: b.mutation<{ url: string }, string>({ query: (id) => ({ url: `tenants/${id}/open`, method: 'POST' }) }),
  }),
})

export const {
  useFaxBoxesQuery, useCreateFaxBoxMutation, useUpdateFaxBoxMutation, useDeleteFaxBoxMutation, useFaxesQuery, useDeleteFaxMutation, useFaxKindsQuery, useSendFaxMutation, useRetryFaxMutation, useTestFaxMailMutation, usePsapReportQuery, usePsapLogQuery, useRebidMutation, useBoundariesQuery, useSaveBoundaryMutation, useDeleteBoundaryMutation, useLazyFindAreaQuery, useAddressDataQuery, useDeleteAddressesMutation, useValidateAddressMutation, useBcfLogQuery, useRadioQuery, usePttMutation, useMonitorMutation, usePatchMutation, useAlertToneMutation, useUnitCallMutation, useHangUpMutation, useRadioLogQuery,
  useTextingKindsQuery,
  useMeQuery, useAuthOptionsQuery, useLoginMutation, useLogoutMutation, useSetupStatusQuery, useCompleteSetupMutation,
  useExtensionsQuery, useCreateExtensionMutation, useUpdateExtensionMutation, useDeleteExtensionMutation, useLazyNextExtensionQuery,
  useRingGroupsQuery, useCreateRingGroupMutation, useUpdateRingGroupMutation, useDeleteRingGroupMutation,
  useTrunksQuery, useCreateTrunkMutation, useUpdateTrunkMutation, useDeleteTrunkMutation, useTrunkStatusQuery,
  useInboundRoutesQuery, useCreateInboundMutation, useUpdateInboundMutation, useDeleteInboundMutation,
  useOutboundRoutesQuery, useCreateOutboundMutation, useUpdateOutboundMutation, useDeleteOutboundMutation, useReorderOutboundMutation,
  useSettingsQuery, useSaveSettingsMutation, useUsersQuery, useSaveUserMutation, useDeleteUserMutation, useChangePasswordMutation,
  useCallsQuery, useHangupMutation, useRegistrationsQuery, useStatsQuery, useCdrQuery, useHealthQuery, useHistoryQuery,
  useRestoreMutation, useImportConfigMutation, useBansQuery, useUnbanMutation, useAuditQuery, useTraceQuery, useSystemQuery, useOpenDoorMutation, useEmergencyModeMutation, useReceptionistsQuery, useCallbacksQuery, useCallerQuery, useSimulateMutation, useMetricsTestMutation, useAwayQuery, useReputationQuery, useCertificateQuery, useInstallCertificateMutation, useDatabaseQuery, useServersQuery, useServersDnsQuery, useSetServerMutation, useForgetServerMutation, useTestDatabaseMutation, useMoveDatabaseMutation, useSetAwayMutation, useCheckCalendarMutation, useMakeInsightMutation, useInsightQuery, useTimelineQuery, useAddNoteMutation, useDeleteNoteMutation, useConversationsQuery, useThreadQuery, useSendTextMutation, useMarkReadMutation, usePutNoticeMutation, useDeleteNoticeMutation, useHotDesksQuery, useHotDeskSignInMutation, useHotDeskSignOutMutation, useDirectoryQuery, useAddContactMutation, useSaveContactMutation, useDeleteContactMutation, useCostsQuery, usePriceMutation, useQualityQuery, useCrmTestMutation, useTranscriptQuery, useMakeTranscriptMutation, useSearchTranscriptsQuery, useAddCallbackMutation, useSetCallbackMutation, useDeleteCallbackMutation, useRingBackMutation, useCreateReceptionistMutation, useUpdateReceptionistMutation, useDeleteReceptionistMutation, useAskMutation, useApplyAskMutation, useCheckVaultMutation, useSitesQuery, useCreateSiteMutation, useUpdateSiteMutation, useDeleteSiteMutation, useRemoteAddressQuery, useCompatibilityQuery, useSetAgentMutation, useSuperviseMutation, useSetSupervisionMutation, useTokensQuery, useAddTokenMutation, useRevokeTokenMutation, useGitOpsQuery, useSyncGitOpsMutation, useMobileDevicesQuery, usePairMobileMutation, useUnpairMobileMutation, useHoldsQuery, useAddHoldMutation, useUpdateHoldMutation, useReleaseHoldMutation, usePauseRecordingMutation, useLoadTestQuery, useStartLoadTestMutation, useStopLoadTestMutation, useUpgradeQuery, useUploadUpgradeMutation, useApplyUpgradeMutation, useRollbackUpgradeMutation, useCancelUpgradeMutation,
  useScriptsQuery, useCreateScriptMutation, useUpdateScriptMutation, useDeleteScriptMutation, useTestScriptMutation,
  useMenusQuery, useCreateMenuMutation, useUpdateMenuMutation, useDeleteMenuMutation, useSavePromptMutation, useResetPromptMutation,
  useSchedulesQuery, useCreateScheduleMutation, useUpdateScheduleMutation, useDeleteScheduleMutation,
  useDevicesQuery, useCreateDeviceMutation, useUpdateDeviceMutation, useDeleteDeviceMutation,
  usePageGroupsQuery, useCreatePageGroupMutation, useUpdatePageGroupMutation, useDeletePageGroupMutation,
  useConferencesQuery, useCreateConferenceMutation, useUpdateConferenceMutation, useDeleteConferenceMutation,
  useConferenceLiveQuery, useLockConferenceMutation, useMuteConferenceMemberMutation, useRemoveConferenceMemberMutation,
  useDivertsQuery, useDivertQuery, useSetDivertMutation,
  usePortalLoginMutation, useMyselfQuery, useSetMyDivertMutation, useSaveMySettingsMutation, useChangeMyPinMutation, useMyVoicemailQuery,
  useMarkMyVoicemailMutation, useDeleteMyVoicemailMutation, useSaveMyGreetingMutation, useResetMyGreetingMutation, useMyCallsQuery,
  useTenantsQuery, useCreateTenantMutation, useUpdateTenantMutation, useDeleteTenantMutation, useOpenTenantMutation,
  useQueuesQuery, useCreateQueueMutation, useUpdateQueueMutation, useDeleteQueueMutation, useQueueStatusQuery,
  useVoicemailQuery, useMarkHeardMutation, useDeleteVoicemailMutation, useSaveGreetingMutation, useResetGreetingMutation,
  useDirectoriesQuery, useCreateDirectoryMutation, useUpdateDirectoryMutation, useDeleteDirectoryMutation,
  useDialInsQuery, useCreateDialInMutation, useUpdateDialInMutation, useDeleteDialInMutation,
  useHotelQuery, useCheckInMutation, useCheckOutMutation, useSetRoomStatusMutation, useWakeUpsQuery, useSetWakeUpMutation, useCancelWakeUpMutation,
  useSaveNameAudioMutation, useDeleteNameAudioMutation,
  usePromptsQuery, useSaveLangPromptMutation, useDeleteLangPromptMutation, useGeneratePromptsMutation, useMusicQuery, useAddMusicMutation, useDeleteMusicMutation,
} = api

/** Pulls the message and field problems out of whatever an RTK Query call rejected with. */
export function errorOf(err: unknown): ApiErrorBody {
  const e = err as { data?: ApiErrorBody; status?: number | string; error?: string }
  if (e?.data && typeof e.data === 'object' && 'message' in e.data) return e.data
  if (e?.error) return { message: e.error }
  return { message: 'Something went wrong. Check that BitVoice is running.' }
}
