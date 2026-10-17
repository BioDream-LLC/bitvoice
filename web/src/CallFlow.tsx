import { Simulator } from './Simulator'
import { Background, Controls, Position, ReactFlow, type Edge, type Node } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Plus, Workflow } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  useExtensionsQuery, useInboundRoutesQuery, useMenusQuery, useQueuesQuery, useRingGroupsQuery, useSchedulesQuery, useTogglesQuery, useAudioStreamsQuery,
  useScriptsQuery, useTrunksQuery, useReceptionistsQuery, useSettingsQuery, type Destination,
} from './api'
import { Empty, Page } from './ui'
import { useIsAdmin } from './Editor'
import { FlowEdit, NewRoute, type FlowBox } from './FlowEdit'

// Colours match the rest of the interface: each kind of thing has one colour.
const colours: Record<string, [string, string]> = {
  route: ['#fbbf24', 'Incoming'], extension: ['#7dd3fc', 'Extension'], ringgroup: ['#c4b5fd', 'Ring group'],
  queue: ['#fdba74', 'Queue'], menu: ['#7dd3fc', 'Menu'], schedule: ['#fcd34d', 'Schedule'], toggle: ['#a5b4fc', 'Day/night toggle'], audiosocket: ['#f0abfc', 'Voice bot'], script: ['#f0abfc', 'Script'], ariapp: ['#7dd3fc', 'ARI application'], receptionist: ['#c4b5fd', 'AI receptionist'],
  voicemail: ['#c4b5fd', 'Voicemail'], fax: ['#a78bfa', 'Fax'], esrp: ['#fb7185', 'ESRP'], echo: ['#94a3b8', 'Echo test'], hangup: ['#fda4af', 'Hang up'],
}
const pages: Record<string, string> = {
  route: '/routes', extension: '/extensions', ringgroup: '/ring-groups', queue: '/queues', menu: '/menus',
  schedule: '/schedules', script: '/scripts', voicemail: '/voicemail', fax: '/faxes', receptionist: '/receptionists',
}

/** A read-only map of how calls flow, built from the configuration. Click a box to edit it. */
export function CallFlow() {
  const { data: routes } = useInboundRoutesQuery()
  const { data: exts } = useExtensionsQuery()
  const { data: groups } = useRingGroupsQuery()
  const { data: queues } = useQueuesQuery()
  const { data: menus } = useMenusQuery()
  const { data: schedules } = useSchedulesQuery()
  const { data: toggles } = useTogglesQuery()
  const { data: streams } = useAudioStreamsQuery()
  const { data: scripts } = useScriptsQuery()
  const { data: trunks } = useTrunksQuery()
  const { data: receptionists } = useReceptionistsQuery()
  const { data: settings } = useSettingsQuery()
  const navigate = useNavigate()
  // A box opens its own editor: /extensions?open=101.
  const go = (data: unknown) => {
    const { kind, target } = data as { kind: string; target?: string }
    const to = pages[kind]
    if (to) navigate(target ? `${to}?open=${encodeURIComponent(target)}` : to)
  }
  // Administrators edit where calls go right on the diagram; others open the item's page.
  const admin = useIsAdmin()
  const [box, setBox] = useState<FlowBox | null>(null)
  const [adding, setAdding] = useState(false)
  const [from, setFrom] = useState('')
  const pick = (id: string, data: unknown) => { if (!admin) return go(data); setFrom(id); setBox(data as FlowBox) }
  // Closing the panel puts focus back on the box it was opened from.
  const close = () => {
    setBox(null)
    setTimeout(() => (document.querySelector(`.react-flow__node[data-id="${CSS.escape(from)}"]`) as HTMLElement | null)?.focus(), 0)
  }

  const { nodes, edges } = useMemo(() => {
    const nodes: Node[] = []
    const edges: Edge[] = []
    const seen = new Map<string, number>() // node id -> column
    const rows = new Map<number, number>()
    const place = (id: string, kind: string, label: string, col: number, target = '') => {
      if (seen.has(id)) return
      seen.set(id, col)
      const row = rows.get(col) ?? 0
      rows.set(col, row + 1)
      const [colour, what] = colours[kind] ?? ['#94a3b8', kind]
      nodes.push({
        id, position: { x: col * 250, y: row * 90 }, sourcePosition: Position.Right, targetPosition: Position.Left, data: { label: <div><div style={{ fontSize: 10, opacity: 0.7 }}>{what}</div><div style={{ fontWeight: 600 }}>{label}</div></div>, kind, target },
        style: { background: '#0f172a', color: colour, border: `1px solid ${colour}66`, borderRadius: 10, width: 200, fontSize: 12 },
      })
    }
    const link = (from: string, to: string, label?: string) =>
      edges.push({ id: `${from}->${to}:${label ?? ''}`, source: from, target: to, label, style: { stroke: '#475569' },
        labelStyle: { fill: '#cbd5e1', fontSize: 10 }, labelBgStyle: { fill: '#020617' } })

    const visit = (d: Destination | null | undefined, from: string, col: number, label?: string, depth = 0) => {
      if (!d || depth > 6) return
      const key = `${d.type}:${d.target}`
      let name = d.target
      const next: [Destination | null | undefined, string?][] = []
      switch (d.type) {
        case 'extension': {
          const x = exts?.items.find((e) => e.number === d.target)
          name = `${d.target} ${x?.name ?? ''}`
          next.push([x?.noAnswer, 'no answer'])
          break
        }
        case 'ringgroup': {
          const g = groups?.items.find((e) => e.number === d.target)
          name = `${d.target} ${g?.name ?? ''}`
          g?.members.forEach((m) => next.push([{ type: 'extension', target: m }]))
          next.push([g?.noAnswer, 'no answer'])
          break
        }
        case 'queue': {
          const q = queues?.items.find((e) => e.number === d.target)
          name = `${d.target} ${q?.name ?? ''}`
          q?.agents.forEach((m) => next.push([{ type: 'extension', target: m }, 'agent']))
          next.push([q?.timeout, 'waited too long'])
          break
        }
        case 'menu': {
          const m = menus?.items.find((e) => e.id === d.target)
          name = m?.name ?? 'Menu'
          m?.options.forEach((o) => next.push([o.destination, `key ${o.key}${o.label ? ' ' + o.label : ''}`]))
          next.push([m?.timeout, 'no key'])
          break
        }
        case 'schedule': {
          const s = schedules?.items.find((e) => e.id === d.target)
          name = s?.name ?? 'Schedule'
          next.push([s?.open, 'open'], [s?.closed, 'closed'])
          break
        }
        case 'audiosocket': {
          const s = streams?.items.find((e) => e.id === d.target)
          name = s?.name ?? 'Voice bot'
          next.push([s?.fallback, 'if the bot fails'])
          break
        }
        case 'toggle': {
          const s = toggles?.items.find((e) => e.id === d.target)
          name = s?.name ?? 'Toggle'
          next.push([s?.normal, 'normal'], [s?.override, 'switched over'])
          break
        }
        case 'script': {
          const s = scripts?.items.find((e) => e.id === d.target)
          name = s?.name ?? 'Script'
          next.push([s?.fallback, 'fallback'])
          break
        }
        case 'receptionist': {
          const r = receptionists?.items.find((e) => e.id === d.target)
          name = r?.name ?? 'Receptionist'
          next.push([r?.person, 'a person'])
          if (r?.messageBox) next.push([{ type: 'voicemail', target: r.messageBox }, 'message'])
          break
        }
        case 'ariapp': {
          const a = settings?.system.compat.apps?.find((x) => x.name === d.target)
          name = d.target
          next.push([a?.fallback, 'no program / let go'])
          break
        }
        case 'voicemail': name = `of ${d.target}`; break
        case 'fax': name = `${d.target} (to PDF)`; break
        case 'esrp': name = 'PSAP by location'; break
        case 'echo': name = '*43'; break
        case 'hangup': name = 'Goodbye'; break
      }
      const fresh = !seen.has(key)
      place(key, d.type, name, col, d.type === 'voicemail' ? '' : d.target)
      link(from, key, label)
      if (fresh) next.forEach(([n, l]) => visit(n, key, col + 1, l, depth + 1))
    }
    routes?.items.forEach((r) => {
      const id = 'route:' + r.id
      const trunk = trunks?.items.find((t) => t.id === r.trunk)?.name
      place(id, 'route', `${r.name || 'Route'}: ${r.did || 'any number'}${trunk ? ` on ${trunk}` : ''}`, 0, r.id)
      visit(r.destination, id, 1)
    })
    return { nodes, edges }
  }, [routes, exts, groups, queues, menus, schedules, scripts, trunks, receptionists, settings])

  return (
    <Page icon={Workflow} title="Call flow" accent="from-amber-400 to-sky-400"
      intro={admin ? 'Where calls from outside go, step by step. Click a box to change where calls go from it.' : 'Where calls from outside go, step by step. Click a box to see it.'}
      actions={admin && <button className="btn btn-primary" onClick={() => setAdding(true)}><Plus size={15} aria-hidden /> Add incoming route</button>}>
      <Simulator />
      {!nodes.length ? <Empty icon={Workflow} title="No incoming routes yet">Add an incoming route to see its flow here.</Empty> : (
        <div className="card h-[70vh] overflow-hidden" aria-label="Call flow diagram"
          onKeyDown={(e) => {
            // Keyboard users open a box with Enter or Space, as a click would.
            if (e.key !== 'Enter' && e.key !== ' ') return
            const id = (e.target as HTMLElement).closest('.react-flow__node')?.getAttribute('data-id')
            const n = id && nodes.find((x) => x.id === id)
            if (n) { e.preventDefault(); pick(n.id, n.data) }
          }}>
          <ReactFlow nodes={nodes} edges={edges} fitView fitViewOptions={{ maxZoom: 1.1 }} colorMode="dark" style={{ background: 'transparent' }} nodesDraggable nodesConnectable={false} proOptions={{ hideAttribution: true }}
            onNodeClick={(_, n) => pick(n.id, n.data)}>
            <Background color="#1e293b" gap={20} />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
      )}
      {box && <FlowEdit box={box} onClose={close} onOpen={() => { const b = box; setBox(null); go(b) }} />}
      {adding && <NewRoute onClose={() => setAdding(false)} />}
    </Page>
  )
}
