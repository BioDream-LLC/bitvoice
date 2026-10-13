// Figures for the manual: vector illustrations drawn from a few shapes so they all look alike. Each is
// self-contained SVG (its own dark panel), so it reads the same in Help, on the web page and in the PDF.
// An article shows one with a line `::figure name`.

type Tone = 'sky' | 'violet' | 'emerald' | 'amber' | 'rose' | 'teal' | 'slate' | 'fuchsia' | 'orange'

const tones: Record<Tone, { fill: string; stroke: string; text: string }> = {
  sky: { fill: '#0c2a43', stroke: '#38bdf8', text: '#bae6fd' },
  violet: { fill: '#22184a', stroke: '#a78bfa', text: '#ddd6fe' },
  emerald: { fill: '#082f26', stroke: '#34d399', text: '#a7f3d0' },
  amber: { fill: '#3a2606', stroke: '#fbbf24', text: '#fde68a' },
  rose: { fill: '#3b0f1c', stroke: '#fb7185', text: '#fecdd3' },
  teal: { fill: '#062f30', stroke: '#2dd4bf', text: '#99f6e4' },
  slate: { fill: '#162033', stroke: '#64748b', text: '#e2e8f0' },
  fuchsia: { fill: '#36103a', stroke: '#e879f9', text: '#f5d0fe' },
  orange: { fill: '#3a1a06', stroke: '#fb923c', text: '#fed7aa' },
}

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

/** A rounded box with a bold title and an optional line under it. */
function box(x: number, y: number, w: number, h: number, title: string, sub = '', tone: Tone = 'slate') {
  const t = tones[tone]
  const cy = sub ? y + h / 2 - 3 : y + h / 2 + 5
  return `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="10" fill="${t.fill}" stroke="${t.stroke}" stroke-width="1.5"/>` +
    `<text x="${x + w / 2}" y="${cy}" text-anchor="middle" class="t" fill="${t.text}">${esc(title)}</text>` +
    (sub ? `<text x="${x + w / 2}" y="${cy + 17}" text-anchor="middle" class="s">${esc(sub)}</text>` : '')
}

/** A small round badge (a step number, a key). */
function badge(x: number, y: number, label: string, tone: Tone = 'sky') {
  const t = tones[tone]
  return `<circle cx="${x}" cy="${y}" r="13" fill="${t.fill}" stroke="${t.stroke}" stroke-width="1.5"/><text x="${x}" y="${y + 5}" text-anchor="middle" class="t" fill="${t.text}">${esc(label)}</text>`
}

/** A line with an arrow head; dashed for "if" paths. */
function arrow(d: string, tone: Tone = 'slate', dashed = false) {
  const c = tone === 'slate' ? '#94a3b8' : tones[tone].stroke
  return `<path d="${d}" fill="none" stroke="${c}" stroke-width="1.6"${dashed ? ' stroke-dasharray="5 4"' : ''} marker-end="url(#tip-${tone})"/>`
}

/** Text placed freely: a label on an arrow, a note. */
function label(x: number, y: number, s: string, anchor: 'start' | 'middle' | 'end' = 'middle', cls = 's') {
  // A bold label needs its own colour: the boxes give theirs as an attribute, which the class would override.
  return `<text x="${x}" y="${y}" text-anchor="${anchor}" class="${cls}"${cls === 't' ? ' fill="#e2e8f0"' : ''}>${esc(s)}</text>`
}

/** A phone, drawn small: handset and screen. */
function phone(x: number, y: number, tone: Tone = 'sky') {
  const t = tones[tone]
  return `<g transform="translate(${x},${y})"><rect width="34" height="44" rx="6" fill="${t.fill}" stroke="${t.stroke}" stroke-width="1.5"/>` +
    `<rect x="6" y="6" width="22" height="12" rx="2" fill="${t.stroke}" opacity="0.35"/>` +
    `<g fill="${t.text}" opacity="0.8">${[0, 1, 2].map((r) => [0, 1, 2].map((c) => `<circle cx="${10 + c * 7}" cy="${25 + r * 6}" r="1.6"/>`).join('')).join('')}</g></g>`
}

/** A server: three stacked bars. */
function server(x: number, y: number, tone: Tone = 'violet') {
  const t = tones[tone]
  return `<g transform="translate(${x},${y})">${[0, 1, 2].map((i) => `<rect y="${i * 14}" width="44" height="11" rx="3" fill="${t.fill}" stroke="${t.stroke}" stroke-width="1.3"/><circle cx="37" cy="${i * 14 + 5.5}" r="1.8" fill="${t.stroke}"/>`).join('')}</g>`
}

/** A cloud (a carrier, the internet). */
function cloud(x: number, y: number, w: number, title: string, tone: Tone = 'teal') {
  const t = tones[tone]
  const h = w * 0.5
  return `<path d="M${x + w * 0.22} ${y + h} h${w * 0.6} a${w * 0.16} ${w * 0.16} 0 0 0 0 -${w * 0.32} a${w * 0.2} ${w * 0.2} 0 0 0 -${w * 0.3} -${w * 0.1} a${w * 0.22} ${w * 0.22} 0 0 0 -${w * 0.38} ${w * 0.1} a${w * 0.14} ${w * 0.14} 0 0 0 ${w * 0.08} ${w * 0.32} z" fill="${t.fill}" stroke="${t.stroke}" stroke-width="1.5"/>` +
    `<text x="${x + w * 0.5}" y="${y + h * 0.72}" text-anchor="middle" class="t" fill="${t.text}">${esc(title)}</text>`
}

/** The panel every figure sits on, with its arrow heads and fonts. */
function frame(w: number, h: number, alt: string, body: string) {
  const tips = (Object.keys(tones) as Tone[]).map((k) => {
    const c = k === 'slate' ? '#94a3b8' : tones[k].stroke
    return `<marker id="tip-${k}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" fill="${c}"/></marker>`
  }).join('')
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${w} ${h}" role="img" aria-label="${esc(alt)}" class="figure-svg">` +
    `<defs>${tips}<linearGradient id="panel" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#0f1a2e"/><stop offset="1" stop-color="#070d1a"/></linearGradient>` +
    `<style>.t{font:600 13px system-ui,-apple-system,"Segoe UI",sans-serif}.s{font:11.5px system-ui,-apple-system,"Segoe UI",sans-serif;fill:#94a3b8}.h{font:700 12px system-ui,sans-serif;fill:#c4b5fd;letter-spacing:.06em}.m{font:12px ui-monospace,Menlo,monospace;fill:#fcd34d}</style></defs>` +
    `<rect width="${w}" height="${h}" rx="14" fill="url(#panel)" stroke="#1e293b"/>${body}</svg>`
}

export interface Figure { title: string; svg: string }

export const figures: Record<string, Figure> = {
  // --- Getting started -------------------------------------------------------------------------------
  overview: {
    title: 'Where BitVoice sits: phones and carriers on one side, people and systems on the other',
    svg: frame(820, 300, 'Phones, browsers, apps and trunks connect to BitVoice, which routes, queues, records and converts, and connects to staff, CAD, CRMs and Teams.',
      label(140, 34, 'WHO CONNECTS', 'middle', 'h') + label(680, 34, 'WHERE CALLS GO', 'middle', 'h') +
      box(40, 52, 200, 44, 'Desk phones', 'Yealink, Poly, Grandstream, Snom', 'sky') +
      box(40, 106, 200, 44, 'Browsers and apps', 'WebRTC, iOS, Android', 'sky') +
      box(40, 160, 200, 44, 'Phone companies', 'SIP and IAX2 trunks', 'teal') +
      box(40, 214, 200, 44, 'ESInet and radio', '911 calls, P25, RoIP', 'rose') +
      `<rect x="300" y="60" width="220" height="190" rx="16" fill="#1a1440" stroke="#a78bfa" stroke-width="2"/>` +
      label(410, 92, 'BitVoice', 'middle', 't') +
      ['Routes and menus', 'Queues and rooms', 'Records and logs', 'Converts codecs', 'Keeps 911 working'].map((s, i) => label(410, 120 + i * 24, s)).join('') +
      [74, 128, 182, 236].map((y) => arrow(`M240 ${y} H296`, 'violet')).join('') +
      [74, 128, 182, 236].map((y) => arrow(`M520 ${y} H576`, 'violet')).join('') +
      box(580, 52, 200, 44, 'People who answer', 'extensions, ring groups', 'emerald') +
      box(580, 106, 200, 44, 'Voicemail and AI', 'transcripts, receptionist', 'violet') +
      box(580, 160, 200, 44, 'CAD, CRM, Teams', 'events, caller cards', 'amber') +
      box(580, 214, 200, 44, 'Records', 'history, recordings, faxes', 'fuchsia')),
  },
  'first-hour': {
    title: 'The first hour: five steps from a new install to a working phone system',
    svg: frame(820, 170, 'Five steps: extensions, phones, a trunk, call routing, then check Health.',
      ['Extensions', 'Phones', 'Trunk', 'Call routing', 'Health'].map((s, i) => {
        const x = 30 + i * 158
        const subs = ['one per person', 'connect each one', 'your carrier', 'where calls go', 'what is missing']
        const tone: Tone[] = ['sky', 'emerald', 'teal', 'amber', 'rose']
        return box(x, 60, 130, 56, s, subs[i], tone[i]) + badge(x + 14, 60, String(i + 1), tone[i]) + (i < 4 ? arrow(`M${x + 130} 88 H${x + 156}`) : '')
      }).join('') + label(410, 150, 'Every step has a page in the menu; Health lists anything still missing, emergency calling first.')),
  },
  // --- Calls -------------------------------------------------------------------------------------
  'call-path': {
    title: 'How a call from outside finds its way',
    svg: frame(820, 260, 'A call arrives on a trunk, matches an incoming route, then a schedule, a menu, a ring group, and finally voicemail if nobody answers.',
      cloud(20, 70, 140, 'Carrier') +
      arrow('M150 110 H190', 'teal') +
      box(192, 86, 120, 48, 'Incoming route', 'by number dialled', 'amber') + arrow('M312 110 H340') +
      box(342, 86, 110, 48, 'Schedule', 'open or closed?', 'violet') +
      arrow('M452 100 H484', 'emerald') + label(468, 92, 'open') +
      arrow('M397 134 V190 H484', 'rose', true) + label(440, 184, 'closed') +
      box(486, 76, 110, 48, 'Menu', '1 sales, 2 support', 'sky') + arrow('M596 100 H624') +
      box(626, 76, 170, 48, 'Ring group', 'rings everyone for 25 s', 'emerald') +
      box(486, 166, 110, 48, 'Voicemail', 'after hours', 'fuchsia') +
      arrow('M711 124 V230 H596', 'fuchsia', true) + label(700, 248, 'if nobody answers', 'end')),
  },
  'internal-call': {
    title: 'A call between two extensions',
    svg: frame(820, 200, 'Extension 100 dials 101: BitVoice checks forwarding and do not disturb, then rings every phone of 101, then follow-me, then the no-answer destination.',
      phone(40, 70, 'sky') + label(57, 136, '100 dials 101') + arrow('M90 92 H150', 'sky') +
      box(152, 64, 150, 56, 'Forwarding?', 'all, busy, no answer', 'amber') + arrow('M302 92 H330') +
      box(332, 64, 150, 56, 'Do not disturb?', 'then voicemail', 'rose') + arrow('M482 92 H510') +
      box(512, 64, 130, 56, 'Rings 101', 'every phone, apps', 'emerald') + arrow('M642 92 H670') +
      box(672, 64, 120, 56, 'Follow-me', 'mobile, other desk', 'teal') +
      arrow('M732 120 V160 H560', 'fuchsia', true) + box(420, 140, 140, 44, 'No-answer', 'voicemail or other', 'fuchsia')),
  },
  'ring-strategies': {
    title: 'Ring groups: all at once, or one after another',
    svg: frame(820, 230, 'All: every member rings together, the first to answer takes the call. Sequential: each member rings in turn.',
      label(205, 34, 'ALL AT ONCE', 'middle', 'h') + label(615, 34, 'ONE AFTER ANOTHER', 'middle', 'h') +
      box(130, 54, 150, 40, 'Ring group 600', '', 'violet') +
      [0, 1, 2].map((i) => phone(90 + i * 80, 140, 'emerald') + arrow(`M205 94 L${107 + i * 80} 136`, 'emerald')).join('') +
      label(205, 206, 'the first to answer takes it') +
      box(540, 54, 150, 40, 'Ring group 601', '', 'violet') +
      [0, 1, 2].map((i) => phone(500 + i * 80, 140, i === 0 ? 'emerald' : 'slate') + label(517 + i * 80, 200, `${i * 25}–${i * 25 + 25} s`)).join('') +
      arrow('M615 94 L517 136', 'emerald') + arrow('M534 162 H578', 'slate', true) + arrow('M614 162 H658', 'slate', true)),
  },
  queue: {
    title: 'A queue: callers wait in order, agents take the next one',
    svg: frame(820, 230, 'Callers wait in a queue hearing music and their place; VIPs go to the front; free agents are offered the longest-waiting caller.',
      label(160, 34, 'CALLERS WAITING', 'middle', 'h') + label(640, 34, 'AGENTS', 'middle', 'h') +
      [0, 1, 2, 3].map((i) => box(40 + i * 62, 70, 52, 52, i === 0 ? 'VIP' : String(i + 1), '', i === 0 ? 'amber' : 'sky')).join('') +
      arrow('M290 96 H360', 'violet') + box(362, 66, 120, 60, 'Queue 700', 'longest-idle agent', 'violet') + arrow('M482 96 H540', 'violet') +
      ['available', 'on a call', 'wrap-up', 'paused'].map((s, i) => {
        const tone: Tone[] = ['emerald', 'orange', 'amber', 'slate']
        return box(544, 50 + i * 40, 200, 32, `Agent ${i + 1}`, '', tone[i]) + label(760, 70 + i * 40, s, 'start')
      }).join('') + label(160, 160, 'they hear music and their place in line') + label(160, 180, 'a VIP joins at the front')),
  },
  menu: {
    title: 'A menu (auto attendant): each key goes somewhere',
    svg: frame(820, 230, 'A menu plays a prompt; key 1 goes to sales, 2 to support, 0 to reception, and an extension number dials it directly; no key repeats then times out.',
      box(40, 90, 150, 56, 'Menu', '"Press 1 for sales"', 'sky') +
      [['1', 'Sales ring group', 'emerald'], ['2', 'Support queue', 'violet'], ['0', 'Reception', 'amber'], ['101', 'dial an extension', 'teal']].map(([k, s, tone], i) =>
        arrow(`M190 118 L330 ${44 + i * 46}`, tone as Tone) + badge(352, 44 + i * 46, k, tone as Tone) + box(376, 26 + i * 46, 200, 36, s, '', tone as Tone)).join('') +
      arrow('M115 146 V196 H376', 'rose', true) + box(376, 178, 200, 36, 'No key: timeout destination', '', 'rose') +
      label(700, 110, 'Prompts: record in the', 'middle') + label(700, 128, 'browser, upload, or', 'middle') + label(700, 146, 'let the AI voice read it', 'middle')),
  },
  schedule: {
    title: 'A schedule: open hours, holidays, and where calls go in each',
    svg: frame(820, 210, 'A week with open hours Monday to Friday 9 to 5, a holiday closed all day, and two destinations: open and closed.',
      ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'].map((d, i) => {
        const x = 60 + i * 72
        const open = i < 5 && i !== 3
        return label(x + 30, 46, d) + `<rect x="${x}" y="56" width="60" height="110" rx="8" fill="#0b1220" stroke="#1e293b"/>` +
          (open ? `<rect x="${x + 4}" y="84" width="52" height="50" rx="6" fill="${tones.emerald.fill}" stroke="${tones.emerald.stroke}"/>` : '') +
          (i === 3 ? `<rect x="${x + 4}" y="60" width="52" height="102" rx="6" fill="${tones.rose.fill}" stroke="${tones.rose.stroke}"/>` + label(x + 30, 116, 'holiday') : '') +
          (open ? label(x + 30, 113, '9–17') : '')
      }).join('') +
      box(600, 66, 190, 40, 'Open: reception', '', 'emerald') + box(600, 120, 190, 40, 'Closed: voicemail', '', 'rose') + label(410, 192, 'Times are in the system time zone; a holiday closes the whole day.')),
  },
  // --- People's own calls -----------------------------------------------------------------------
  forwarding: {
    title: 'Forwarding: every call, when busy, when not answered',
    svg: frame(820, 230, 'Forward all sends every call on at once; forward busy only while on a call; forward no answer after the ring time. Codes *72, *90, *52 and *73, *91, *53 to cancel.',
      phone(40, 92, 'sky') + label(57, 156, 'a call to 101') +
      arrow('M90 108 L250 52', 'amber') + box(252, 30, 180, 44, 'Every call', '*72 · cancel *73', 'amber') + arrow('M432 52 H520', 'amber') +
      arrow('M90 114 L250 114', 'orange') + box(252, 92, 180, 44, 'When on a call', '*90 · cancel *91', 'orange') + arrow('M432 114 H520', 'orange') +
      arrow('M90 120 L250 176', 'teal') + box(252, 154, 180, 44, 'When not answered', '*52 · cancel *53', 'teal') + arrow('M432 176 H520', 'teal') +
      box(522, 30, 260, 44, 'Another extension', 'its own settings apply', 'emerald') +
      box(522, 92, 260, 44, 'An outside number', 'through outgoing routes', 'sky') +
      box(522, 154, 260, 44, 'A group, queue or room', '', 'violet')),
  },
  followme: {
    title: 'Follow-me: the desk first, then the mobile, then voicemail',
    svg: frame(820, 220, 'The desk phone rings for its ring time, then each follow-me number in turn; on an outside number the person presses 1 to take the call; then voicemail.',
      phone(40, 80, 'sky') + label(57, 146, 'desk, 25 s') + arrow('M90 102 H180') +
      `<g transform="translate(184,74)"><rect width="30" height="54" rx="6" fill="${tones.teal.fill}" stroke="${tones.teal.stroke}" stroke-width="1.5"/><rect x="5" y="7" width="20" height="34" rx="2" fill="${tones.teal.stroke}" opacity="0.3"/></g>` +
      label(199, 146, 'mobile, 20 s') + arrow('M220 102 H300') +
      box(302, 70, 210, 64, 'Press 1 to take the call', 'so the mobile\'s voicemail never does', 'amber') +
      arrow('M512 102 H600', 'emerald') + label(556, 94, 'pressed 1') + box(602, 76, 180, 52, 'Connected', '', 'emerald') +
      arrow('M407 134 V184 H600', 'fuchsia', true) + label(500, 178, 'no 1 in 15 s') + box(602, 160, 180, 44, 'Voicemail', '', 'fuchsia')),
  },
  pickup: {
    title: 'Call pickup: answer a colleague\'s ringing phone',
    svg: frame(820, 210, 'A call rings 101; 102 in the same pickup group dials *8 and takes it; anyone can dial ** and 101 to take it directly.',
      phone(60, 70, 'emerald') + label(77, 136, '101 rings') +
      `<circle cx="77" cy="92" r="34" fill="none" stroke="${tones.emerald.stroke}" stroke-dasharray="3 5" opacity="0.6"/>` +
      phone(340, 70, 'sky') + label(357, 136, '102 dials *8') +
      arrow('M330 92 H124', 'sky') + label(227, 84, 'takes the call') +
      `<rect x="40" y="40" width="380" height="120" rx="12" fill="none" stroke="#a78bfa" stroke-dasharray="6 4"/>` + label(230, 180, 'pickup group "Front office"') +
      box(500, 60, 280, 50, '** then the extension', 'answers that one extension\'s call', 'teal') +
      box(500, 124, 280, 50, '*8', 'answers the oldest in your group', 'violet')),
  },
  conference: {
    title: 'A conference room: PIN, leaders, and who is talking',
    svg: frame(820, 250, 'People dial the room number and enter the PIN; leaders enter the leader PIN; the room mixes everyone; members can be muted; the live view shows who is talking.',
      [0, 1, 2, 3, 4].map((i) => {
        const a = (i / 5) * Math.PI * 2 - Math.PI / 2
        const x = 260 + Math.cos(a) * 120 - 17, y = 125 + Math.sin(a) * 90 - 22
        return phone(x, y, i === 0 ? 'violet' : i === 2 ? 'slate' : 'emerald') + `<line x1="${x + 17}" y1="${y + 22}" x2="260" y2="125" stroke="#334155"/>`
      }).join('') +
      `<circle cx="260" cy="125" r="36" fill="${tones.teal.fill}" stroke="${tones.teal.stroke}" stroke-width="2"/>` + label(260, 122, 'Room', 'middle', 't') + label(260, 138, '500') +
      label(260, 30, 'violet: leader · grey: muted', 'middle') +
      box(470, 40, 310, 46, 'Dial 500, enter the PIN and #', 'three wrong PINs and the call ends', 'sky') +
      box(470, 98, 310, 46, 'Leader PIN', 'joins as a leader; 2 locks the room', 'violet') +
      box(470, 156, 310, 46, 'Wait for the leader', 'others hear hold music until then', 'amber')),
  },
  intercom: {
    title: 'Intercom: the called phone answers on its speaker',
    svg: frame(820, 160, '100 dials *80 101; 101 answers by itself on the speaker and both talk; an extension can refuse intercom.',
      phone(60, 50, 'sky') + label(77, 116, '100 dials *80101') + arrow('M110 72 H330', 'teal') + label(220, 64, 'auto-answer') +
      phone(340, 50, 'teal') + label(357, 116, '101 answers itself') +
      box(470, 46, 310, 56, 'An extension can refuse intercom', 'then the call rings as usual', 'slate')),
  },
  portal: {
    title: 'The staff portal: everyone looks after their own calls',
    svg: frame(820, 200, 'Staff sign in at #/me with their extension and voicemail PIN to set do not disturb, forwarding, follow-me, speed dials, their greeting and PIN, and see their own calls.',
      `<rect x="40" y="30" width="300" height="140" rx="12" fill="#0b1220" stroke="#334155"/>` + label(190, 56, 'Your phone', 'middle', 't') +
      label(60, 84, 'Extension', 'start') + `<rect x="150" y="72" width="170" height="20" rx="5" fill="#111a2e" stroke="#334155"/>` + label(160, 87, '101', 'start', 'm') +
      label(60, 116, 'Voicemail PIN', 'start') + `<rect x="150" y="104" width="170" height="20" rx="5" fill="#111a2e" stroke="#334155"/>` + label(160, 119, '••••', 'start', 'm') +
      `<rect x="150" y="136" width="100" height="22" rx="6" fill="#0369a1"/>` + label(200, 151, 'Sign in', 'middle', 't') +
      arrow('M340 100 H400', 'teal') +
      ['Do not disturb', 'Forwarding', 'Follow-me', 'Speed dials', 'Voicemail and greeting', 'Your calls'].map((s, i) => box(404 + (i % 2) * 200, 30 + Math.floor(i / 2) * 50, 186, 40, s, '', (['rose', 'sky', 'teal', 'amber', 'violet', 'fuchsia'] as Tone[])[i])).join('')),
  },
  // --- Phones and trunks -------------------------------------------------------------------------
  provisioning: {
    title: 'Provisioning: a desk phone fetches its settings by itself',
    svg: frame(820, 190, 'Add the phone\'s MAC address and extension; the phone is pointed at BitVoice by DHCP option 66 or its own web page, fetches its file over HTTPS and signs in.',
      phone(50, 60, 'sky') + label(67, 126, 'Yealink, Poly,') + label(67, 142, 'Grandstream, Snom') +
      arrow('M100 72 H300', 'amber') + label(200, 64, '1. where is my file? (DHCP 66)') +
      arrow('M300 96 H100', 'emerald') + label(200, 112, '2. HTTPS: extension, password, keys') +
      server(306, 62, 'violet') + label(328, 126, 'BitVoice') +
      arrow('M360 84 H470', 'sky') + label(415, 76, '3. registers') +
      box(472, 56, 310, 56, 'Signed in as its extension', 'BLF keys, time zone, codecs, all set', 'emerald')),
  },
  trunk: {
    title: 'A trunk: your account with a phone company',
    svg: frame(820, 200, 'BitVoice registers to the carrier with a username and password; calls out go through outgoing routes to the trunk; calls in arrive on it and follow incoming routes.',
      server(60, 70, 'violet') + label(82, 132, 'BitVoice') +
      arrow('M120 78 H330', 'teal') + label(225, 70, 'REGISTER every few minutes') +
      arrow('M120 96 H330', 'sky') + label(225, 112, 'calls out: number, caller ID') +
      arrow('M330 128 H120', 'emerald') + label(225, 146, 'calls in: the number dialled') +
      cloud(336, 50, 170, 'Carrier') +
      box(560, 40, 220, 40, 'Username and password', '', 'slate') + box(560, 90, 220, 40, 'Allowed addresses', '', 'slate') + box(560, 140, 220, 40, 'Codecs, channels, rates', '', 'slate')),
  },
  'outbound-routes': {
    title: 'Outgoing routes: the first pattern that matches, its trunks in order',
    svg: frame(820, 220, 'A dialled number is compared with each route\'s patterns in order; the first match tries its trunks one after another; emergency numbers always go out.',
      box(30, 90, 140, 48, 'Dialled', '2125550199', 'sky') + arrow('M170 114 H210') +
      [['911', 'Emergency', 'rose'], ['1NXXNXXXXXX', 'Long distance', 'amber'], ['NXXNXXXXXX', 'Local', 'emerald'], ['011.', 'International', 'slate']].map(([p, n, tone], i) =>
        box(212, 26 + i * 46, 230, 38, `${n}  ${p}`, '', tone as Tone)).join('') +
      arrow('M442 137 H500', 'emerald') + label(470, 129, 'match') +
      box(502, 104, 130, 36, 'Trunk A', 'first', 'teal') + arrow('M632 122 H660', 'teal', true) + box(662, 104, 130, 36, 'Trunk B', 'if A fails', 'teal') +
      label(410, 210, 'N is 2–9, X is any digit, . is anything more; emergency numbers never fail for want of a pattern.')),
  },
  iax2: {
    title: 'An IAX2 trunk to an Asterisk or FreePBX server',
    svg: frame(820, 190, 'BitVoice and Asterisk talk IAX2 on UDP port 4569 with a shared username and password: calls both ways, registration either way, audio passed through.',
      server(60, 60, 'violet') + label(82, 124, 'BitVoice') +
      arrow('M120 70 H610', 'teal') + arrow('M610 98 H120', 'emerald') +
      label(365, 62, 'one UDP port, 4569: signalling and audio together') + label(365, 116, 'MD5 challenge with the shared password; call tokens') +
      server(616, 60, 'orange') + label(638, 124, 'Asterisk or FreePBX') +
      box(240, 140, 250, 34, 'iax.conf: type=friend, secret=…', '', 'slate')),
  },
  codecs: {
    title: 'Codecs: each side speaks its own, BitVoice converts in between',
    svg: frame(820, 210, 'A browser speaking Opus calls a desk phone speaking G.722 and a carrier speaking G.711; BitVoice converts between any two of its codecs.',
      box(30, 40, 170, 44, 'Browser', 'Opus', 'violet') + box(30, 100, 170, 44, 'Desk phone', 'G.722 HD', 'emerald') + box(30, 160, 170, 44, 'DECT base', 'G.726', 'teal') +
      [62, 122, 182].map((y) => arrow(`M200 ${y} H296`, 'slate')).join('') +
      `<rect x="300" y="40" width="220" height="164" rx="14" fill="#1a1440" stroke="#a78bfa" stroke-width="2"/>` + label(410, 70, 'Converts', 'middle', 't') +
      label(410, 96, 'G.711 µ-law and A-law · G.722') + label(410, 116, 'Opus · G.729 · G.726 · GSM') + label(410, 146, 'only when the two sides differ:') + label(410, 164, 'same codec, passed untouched') +
      [62, 122, 182].map((y) => arrow(`M520 ${y} H616`, 'slate')).join('') +
      box(620, 40, 170, 44, 'Carrier', 'G.711', 'sky') + box(620, 100, 170, 44, 'Thin link', 'G.729', 'amber') + box(620, 160, 170, 44, 'Old softphone', 'GSM', 'fuchsia')),
  },
  // --- Running it --------------------------------------------------------------------------------
  cluster: {
    title: 'Several servers as one phone system',
    svg: frame(820, 220, 'Two or more BitVoice servers share a PostgreSQL database: phones register to any of them, calls reach a phone whichever server it is on, and a shared address moves to a running server.',
      server(140, 50, 'violet') + label(162, 112, 'Server A') + server(340, 50, 'violet') + label(362, 112, 'Server B') +
      `<ellipse cx="262" cy="170" rx="70" ry="16" fill="${tones.sky.fill}" stroke="${tones.sky.stroke}"/><path d="M192 170 v20 a70 16 0 0 0 140 0 v-20" fill="${tones.sky.fill}" stroke="${tones.sky.stroke}"/>` + label(262, 196, 'PostgreSQL') +
      arrow('M162 98 L230 158', 'sky') + arrow('M362 98 L294 158', 'sky') + arrow('M190 72 H334', 'emerald') + arrow('M334 82 H190', 'emerald') + label(262, 64, 'calls cross') +
      box(500, 40, 290, 44, 'A shared address', 'moves to a running server', 'amber') +
      box(500, 96, 290, 44, 'Phones register anywhere', 'calls find them', 'emerald') +
      box(500, 152, 290, 44, 'A server stops', 'its calls carry on elsewhere', 'teal')),
  },
  tenants: {
    title: 'Hosted organisations: one server, separate phone systems',
    svg: frame(820, 250, 'One BitVoice hosts the main system and other organisations; each has its own data, users, numbers and web address; they share only the SIP and media ports.',
      `<rect x="30" y="30" width="520" height="190" rx="16" fill="none" stroke="#a78bfa" stroke-width="2"/>` + label(290, 54, 'ONE BITVOICE SERVER', 'middle', 'h') +
      [['Main system', '/', 'violet'], ['Acme Dental', '/t/acme/', 'emerald'], ['Riverside Clinic', 'pbx.riverside.example', 'sky']].map(([n, u, tone], i) =>
        box(50 + i * 168, 72, 150, 80, n, u, tone as Tone) + label(125 + i * 168, 172, ['own data', 'own users', 'own numbers'][i])).join('') +
      box(170, 182, 240, 26, 'shared: SIP 5060, IAX2 4569, media ports', '', 'slate') +
      box(580, 40, 210, 50, 'By domain', 'pbx.riverside.example', 'sky') +
      box(580, 100, 210, 50, 'By user name', 'acme.100', 'emerald') +
      box(580, 160, 210, 50, 'By trunk and number', 'a shared provider', 'teal')),
  },
  security: {
    title: 'Layers of protection',
    svg: frame(820, 230, 'From the outside in: trusted networks and bans, strong SIP passwords, TLS and SRTP, two-step sign-in and single sign-on, an audit of every change, and limits on outside calls.',
      [['Trusted networks, bans after failed sign-ins', 'rose', 760], ['Strong SIP passwords, one per extension', 'orange', 680], ['TLS for SIP, SRTP for calls', 'amber', 600], ['Two-step sign-in, single sign-on, roles', 'emerald', 520], ['Audit of every change, legal holds', 'sky', 440], ['Limits: outside calls, premium numbers, spend', 'violet', 360]].map(([s, tone, w], i) =>
        `<rect x="${410 - (w as number) / 2}" y="${24 + i * 32}" width="${w}" height="${200 - i * 64 > 30 ? 28 : 28}" rx="8" fill="${tones[tone as Tone].fill}" stroke="${tones[tone as Tone].stroke}"/>` + label(410, 43 + i * 32, s as string)).join('')),
  },
  changes: {
    title: 'Every change is a revision you can compare and undo',
    svg: frame(820, 170, 'Each save makes a numbered revision with who and what; any two can be compared and an old one restored, which is itself a new revision.',
      [1, 2, 3, 4, 5].map((n, i) => `<circle cx="${90 + i * 130}" cy="74" r="18" fill="${tones.violet.fill}" stroke="${tones.violet.stroke}" stroke-width="1.6"/>` + label(90 + i * 130, 79, String(n), 'middle', 't') +
        label(90 + i * 130, 112, ['setup', 'extensions', 'trunk', 'menu', 'restored 2'][i]) + (i < 4 ? `<line x1="${108 + i * 130}" y1="74" x2="${202 + i * 130}" y2="74" stroke="#475569" stroke-width="2"/>` : '')).join('') +
      arrow('M610 52 C560 10, 300 10, 230 52', 'amber', true) + label(420, 22, 'restore revision 2: a new revision 5') + label(410, 150, 'Compare any two side by side before restoring; nothing is lost.')),
  },
  updates: {
    title: 'Updates: install, check, and fall back on their own',
    svg: frame(820, 170, 'An update is checked for its signature, installed beside the running version, started; if it fails to stay up for a minute BitVoice goes back to the version before.',
      [['Upload or fetch', 'signed release', 'sky'], ['Check signature', 'refuse a bad one', 'amber'], ['Start new version', 'calls hand over', 'emerald'], ['Up for a minute?', 'then it is confirmed', 'violet']].map(([a, b, tone], i) =>
        box(30 + i * 196, 50, 170, 56, a, b, tone as Tone) + (i < 3 ? arrow(`M${200 + i * 196} 78 H${224 + i * 196}`) : '')).join('') +
      arrow('M703 106 V140 H115 V110', 'rose', true) + label(410, 156, 'it crashed: back to the version before, automatically')),
  },
  // --- 911, radio and core ----------------------------------------------------------------------
  'office-911': {
    title: 'Office 911: location goes with the call, people on site are told',
    svg: frame(820, 230, 'An extension dials 911; the call goes out on the emergency route with the site\'s civic address; the security desk and email list are alerted at once; a call back is never blocked.',
      phone(40, 80, 'rose') + label(57, 146, 'dials 911') + arrow('M90 102 H180', 'rose') +
      box(182, 70, 170, 64, 'Emergency route', 'never fails for want of one', 'rose') + arrow('M352 102 H430', 'rose') +
      cloud(432, 60, 150, 'Carrier') + arrow('M582 102 H640', 'rose') + box(642, 76, 140, 52, 'The PSAP', 'answers', 'rose') +
      box(182, 160, 170, 50, 'Site, floor, room', 'sent as PIDF-LO', 'amber') + arrow('M267 160 V134', 'amber', true) +
      box(432, 160, 350, 50, 'Alerts on site: security desk, email list', 'Kari\'s Law', 'emerald') + arrow('M310 134 L480 160', 'emerald', true)),
  },
  psap: {
    title: 'The 911 profile: an NG911 call reaches a call-taker',
    svg: frame(820, 240, 'A 911 call arrives from the ESInet with its location and additional data; the BCF checks it; the queue offers it to a call-taker whose screen shows the map and data; ALI and EIDO go to CAD.',
      cloud(20, 80, 150, 'ESInet') + arrow('M160 118 H200', 'rose') +
      box(202, 90, 110, 56, 'BCF', 'checks the source', 'amber') + arrow('M312 118 H350') +
      box(352, 90, 120, 56, '911 queue', 'by priority', 'violet') + arrow('M472 118 H510') +
      box(512, 70, 270, 96, 'Call-taker position', '', 'emerald') +
      label(647, 112, 'location on the map, callback number,', 'middle') + label(647, 130, 'additional data, rebid, transfer', 'middle') +
      arrow('M647 166 V200 H470', 'sky') + box(270, 182, 200, 40, 'CAD: ALI spill, EIDO', '', 'sky') + box(40, 182, 200, 40, 'NENA i3 log', 'every event', 'fuchsia')),
  },
  'core-services': {
    title: 'NG911 Core Services: routing a call by where the caller is',
    svg: frame(820, 230, 'The ESRP receives the call, asks the ECRF which PSAP serves the caller\'s location using LoST, checks policy rules, and sends the call there; the LVF validates addresses in advance.',
      box(30, 90, 140, 56, 'Incoming 911', 'with location', 'rose') + arrow('M170 118 H220', 'rose') +
      box(222, 84, 150, 68, 'ESRP', 'routes the call', 'violet') +
      arrow('M297 84 V40 H430', 'sky') + box(432, 20, 200, 44, 'ECRF (LoST)', 'which PSAP serves here?', 'sky') +
      arrow('M432 52 H330 V80', 'sky', true) +
      arrow('M297 152 V196 H430', 'amber') + box(432, 176, 200, 44, 'Policy rules', 'overflow, time of day', 'amber') +
      arrow('M372 118 H640', 'emerald') + box(642, 92, 150, 52, 'The right PSAP', '', 'emerald') +
      box(660, 20, 140, 44, 'LVF', 'validates addresses', 'teal')),
  },
  radio: {
    title: 'The radio console: talkgroups beside the phone',
    svg: frame(820, 230, 'The console joins P25 talkgroups over ISSI or CSSI and analogue channels through RoIP gateways; the dispatcher keys up, patches channels, sends alert tones and hears emergency calls.',
      box(30, 40, 200, 44, 'P25 system', 'ISSI or CSSI', 'amber') + box(30, 100, 200, 44, 'RoIP gateway', 'analogue channels', 'orange') + box(30, 160, 200, 44, 'Encrypted talkgroup', 'AES-256 keys', 'rose') +
      [62, 122, 182].map((y) => arrow(`M230 ${y} H330`, 'amber')).join('') +
      `<rect x="334" y="34" width="250" height="176" rx="14" fill="#2a1b05" stroke="#fbbf24" stroke-width="2"/>` + label(459, 62, 'Radio console', 'middle', 't') +
      ['push to talk (hold space)', 'patch two channels', 'alert tones', 'emergency acknowledge', 'unit-to-unit calls'].map((s, i) => label(459, 88 + i * 22, s)).join('') +
      arrow('M584 122 H640', 'amber') + box(642, 70, 150, 50, 'Dispatcher', 'beside the phone', 'emerald') + box(642, 134, 150, 50, 'Radio log', 'and CAD events', 'fuchsia')),
  },
  // --- Insight -----------------------------------------------------------------------------------
  quality: {
    title: 'Call quality: from packets to a plain diagnosis',
    svg: frame(820, 200, 'Each call\'s jitter, loss and delay become a MOS score per leg; grouped by site and carrier they give a plain-words diagnosis.',
      box(30, 70, 150, 56, 'Each call', 'jitter, loss, delay', 'sky') + arrow('M180 98 H230') +
      box(232, 70, 130, 56, 'MOS score', '1 bad · 4.4 best', 'amber') + arrow('M362 98 H412') +
      box(414, 70, 150, 56, 'By site, carrier', 'and hour of day', 'violet') + arrow('M564 98 H614') +
      box(616, 60, 180, 76, 'Diagnosis', '"loss on the office link 2–3 pm"', 'emerald') +
      [1, 2, 3, 4, 5].map((i) => `<rect x="${236 + i * 22}" y="${170 - [20, 30, 26, 34, 38][i - 1]}" width="14" height="${[20, 30, 26, 34, 38][i - 1]}" rx="3" fill="${tones.amber.stroke}" opacity="0.7"/>`).join('')),
  },
  timeline: {
    title: 'One customer timeline: everything about a number in one place',
    svg: frame(820, 210, 'Calls, recordings, voicemails, texts, callbacks and notes for a phone number in time order, shared by the team.',
      `<line x1="80" y1="40" x2="80" y2="190" stroke="#334155" stroke-width="2"/>` +
      [['Call answered by Front desk', 'sky'], ['Voicemail: "please call back"', 'violet'], ['Text: "after 3 is fine"', 'teal'], ['Callback booked by the AI receptionist', 'amber'], ['Note: prefers mornings', 'emerald']].map(([s, tone], i) =>
        `<circle cx="80" cy="${50 + i * 34}" r="8" fill="${tones[tone as Tone].fill}" stroke="${tones[tone as Tone].stroke}" stroke-width="2"/>` + box(104, 36 + i * 34, 420, 28, s as string, '', tone as Tone)).join('') +
      box(570, 70, 220, 70, '+1 212 555 0199', 'Lee, Acme Corp', 'slate')),
  },
  voicemail: {
    title: 'Voicemail: from a missed call to a message you can read',
    svg: frame(820, 200, 'Nobody answers; the caller hears the greeting and leaves a message; it lights the phone, is emailed, and with AI on is written out, summarised and sorted by urgency.',
      phone(40, 70, 'sky') + label(57, 136, 'not answered') + arrow('M90 92 H160', 'violet') +
      box(162, 64, 150, 56, 'Greeting', 'yours, or a beep', 'violet') + arrow('M312 92 H360', 'violet') +
      box(362, 64, 130, 56, 'Message', 'up to 3 minutes', 'fuchsia') +
      arrow('M492 80 L560 46', 'amber') + box(562, 26, 220, 36, 'Message light on the phone', '', 'amber') +
      arrow('M492 92 H560', 'sky') + box(562, 74, 220, 36, 'Email with the sound file', '', 'sky') +
      arrow('M492 104 L560 138', 'emerald') + box(562, 122, 220, 36, 'Transcript, summary, urgency', '', 'emerald') +
      label(410, 186, 'Dial *97 to listen from any phone, or use the Voicemail page or the staff portal.')),
  },
  receptionist: {
    title: 'The AI receptionist: answers, helps, and always hands over to a person',
    svg: frame(820, 220, 'The AI receptionist answers in natural language, says it is automated, answers from approved information, takes messages and books callbacks; pressing 0 or asking for a person reaches the person destination.',
      phone(40, 80, 'sky') + label(57, 146, 'a caller') + arrow('M90 102 H160', 'violet') +
      box(162, 70, 190, 64, 'AI receptionist', '"I\u2019m the automated assistant"', 'violet') +
      arrow('M352 86 L430 46', 'emerald') + box(432, 26, 180, 40, 'Answers a question', 'from approved facts', 'emerald') +
      arrow('M352 102 H430', 'amber') + box(432, 82, 180, 40, 'Takes a message', 'or books a callback', 'amber') +
      arrow('M352 118 L430 158', 'rose') + box(432, 138, 180, 40, 'Hands over', '0, "a person", or a fault', 'rose') +
      arrow('M612 158 H660', 'rose') + box(662, 136, 130, 44, 'A person', '', 'emerald') +
      label(410, 206, 'It never stands between a caller and a person: any failure goes to the person destination.')),
  },
  recording: {
    title: 'Recording with consent: announce, beep, or not at all',
    svg: frame(820, 190, 'Recording consent rules by region decide for each call whether to announce, beep or not record; the recording can be paused for card payments.',
      box(30, 60, 170, 60, 'A call is answered', 'recording is on', 'sky') + arrow('M200 90 H250') +
      box(252, 54, 170, 72, 'Consent rule', 'by the other party\u2019s region', 'amber') +
      arrow('M422 70 L500 40', 'emerald') + box(502, 22, 280, 36, 'Announce, then record', 'two-party consent regions', 'emerald') +
      arrow('M422 90 H500', 'sky') + box(502, 72, 280, 36, 'Record with a beep every 15 s', '', 'sky') +
      arrow('M422 110 L500 140', 'slate') + box(502, 122, 280, 36, 'Do not record', '', 'slate') +
      label(330, 172, '*7 pauses and *8 resumes the recording while a card number is read out')),
  },
  costs: {
    title: 'Costs: every call priced, budgets watched',
    svg: frame(820, 190, 'Each call out is priced by the trunk\u2019s rates for its number prefix and rounded by the billing increment; costs add up by user, department and trunk; budgets and spikes raise alerts.',
      box(30, 64, 150, 56, 'A call out', '+44 20 …, 7 min', 'sky') + arrow('M180 92 H230') +
      box(232, 64, 170, 56, 'Trunk rate', '+44: 0.02 a minute', 'amber') + arrow('M402 92 H452') +
      box(454, 64, 140, 56, 'Cost', '0.14', 'emerald') + arrow('M594 92 H640') +
      box(642, 34, 150, 36, 'By person', '', 'violet') + box(642, 78, 150, 36, 'By department', '', 'violet') + box(642, 122, 150, 36, 'By trunk', '', 'violet') +
      label(330, 168, 'Alerts: a budget passed, or far more calls to one place than usual (toll fraud).')),
  },
  teams: {
    title: 'Microsoft Teams Direct Routing: BitVoice is the SBC',
    svg: frame(820, 200, 'Teams users call outside numbers and desk phones through BitVoice, which connects to Microsoft over TLS with SRTP using a public certificate for its name.',
      box(30, 70, 170, 56, 'Teams users', 'app and Teams phones', 'violet') + arrow('M200 98 H260', 'violet') +
      cloud(262, 54, 170, 'Microsoft') + arrow('M432 98 H490', 'violet') + label(461, 90, 'TLS 5061') + label(461, 116, 'SRTP') +
      server(494, 78, 'violet') + label(516, 140, 'BitVoice (SBC)') +
      arrow('M542 86 H610', 'teal') + box(612, 50, 180, 40, 'Carriers', '', 'teal') +
      arrow('M542 110 H610', 'sky') + box(612, 100, 180, 40, 'Desk phones', '', 'sky') +
      label(410, 186, 'Needs: this server\u2019s public name, a certificate for it, and the PowerShell BitVoice shows.')),
  },
  sites: {
    title: 'Sites and remote workers: where a phone is, for 911',
    svg: frame(820, 210, 'A site has a street address and its networks; a phone on those networks is at the site; a phone elsewhere is a remote worker who confirms their own address.',
      `<rect x="30" y="40" width="380" height="150" rx="14" fill="none" stroke="#fbbf24" stroke-dasharray="6 4"/>` + label(220, 62, 'Head office · 1 Main St · 10.0.0.0/16', 'middle', 't') +
      phone(70, 90, 'emerald') + phone(150, 90, 'emerald') + phone(230, 90, 'emerald') + label(190, 166, 'floor and room from each extension') +
      phone(560, 90, 'amber') + label(577, 156, 'at home') + arrow('M410 112 H550', 'amber', true) +
      box(620, 70, 170, 80, 'Asked once', 'confirms the address', 'amber')),
  },
  fax: {
    title: 'Fax: machines, email and PDFs',
    svg: frame(820, 200, 'Faxes arrive on a fax number over T.38 or sound and become PDFs, emailed and kept; faxes are sent from the page or by email to fax; old fax machines connect through analog adapters.',
      box(30, 40, 160, 44, 'Fax machine', 'on an adapter port', 'slate') + box(30, 110, 160, 44, 'Carrier', 'T.38 or G.711', 'teal') +
      arrow('M190 62 H280') + arrow('M190 132 H280') +
      box(282, 70, 170, 60, 'Fax number', 'receives and sends', 'violet') + arrow('M452 100 H520') +
      box(522, 30, 260, 40, 'PDF on the Faxes page', '', 'emerald') + box(522, 80, 260, 40, 'Emailed to a list', '', 'sky') + box(522, 130, 260, 40, 'Email to fax: send by email', '', 'amber') +
      label(410, 190, 'Pages, size (letter or A4) and the far station\u2019s ID are kept with each fax.')),
  },
  texts: {
    title: 'Business texting: the same numbers, one conversation view',
    svg: frame(820, 190, 'Texts to and from the business numbers go through a texting provider; conversations show in Texts and on the customer timeline; desk phones get them as messages.',
      `<g transform="translate(40,60)"><rect width="34" height="60" rx="7" fill="${tones.teal.fill}" stroke="${tones.teal.stroke}" stroke-width="1.5"/></g>` + label(57, 140, 'a customer') +
      arrow('M90 90 H170', 'teal') + box(172, 64, 170, 52, 'Texting provider', 'TextMagic, Twilio, …', 'teal') + arrow('M342 90 H410', 'teal') +
      box(412, 50, 180, 80, 'Texts page', 'one thread per number', 'sky') + arrow('M592 76 H650') + arrow('M592 104 H650') +
      box(652, 56, 140, 36, 'Timeline', '', 'violet') + box(652, 96, 140, 36, 'Desk phones', '', 'emerald') +
      label(410, 176, 'US numbers need 10DLC registration, guided on the Texts page.')),
  },
  scripts: {
    title: 'A routing script: code decides, a fallback is always there',
    svg: frame(820, 190, 'A JavaScript or Lua script is given the call and returns a destination; it runs in a sandbox with a time limit, and any error or timeout uses the fallback.',
      box(30, 70, 150, 50, 'A call', 'from, to, time', 'sky') + arrow('M180 95 H230') +
      `<rect x="232" y="50" width="250" height="90" rx="12" fill="#0b1220" stroke="#e879f9"/>` + label(357, 74, 'function route(call) {', 'middle', 'm') + label(357, 94, "  if (vip(call.from))", 'middle', 'm') + label(357, 114, "    return queue('700') }", 'middle', 'm') +
      arrow('M482 80 H560', 'emerald') + box(562, 60, 220, 40, 'Its answer', '', 'emerald') +
      arrow('M482 120 H560', 'rose', true) + box(562, 110, 220, 40, 'Fallback', 'on error or after 200 ms', 'rose') +
      label(410, 176, 'Scripts can never place calls, reach the network or the disk: they only choose.')),
  },
  history: {
    title: 'Live calls and history: every call, while it happens and after',
    svg: frame(820, 190, 'Live calls show each call as it happens with its legs, codecs and quality; when it ends it becomes a call record with its recording, transcript, cost and quality.',
      box(30, 50, 300, 100, 'Live call', '', 'emerald') + label(180, 96, '100 Front desk → +1 212 555 0199') + label(180, 116, 'G.722 / G.711 · MOS 4.3 · 02:14') +
      arrow('M330 100 H410', 'slate') + label(370, 92, 'ends') +
      box(412, 40, 380, 120, 'Call record', '', 'violet') + ['who and when, how long, answered by', 'recording and transcript', 'cost, quality per leg, why it ended'].map((s, i) => label(602, 86 + i * 20, s)).join('')),
  },
}
