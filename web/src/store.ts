import { configureStore } from '@reduxjs/toolkit'
import { useEffect } from 'react'
import { api } from './api'

export const store = configureStore({
  reducer: { [api.reducerPath]: api.reducer },
  middleware: (gdm) => gdm().concat(api.middleware),
})

export type AppDispatch = typeof store.dispatch

// Which cached data each server event makes stale. The interface never polls: the server says what
// changed and only that is refetched.
const eventTags: Record<string, Parameters<typeof api.util.invalidateTags>[0]> = {
  'call.started': ['Calls', 'Stats', 'QueueStatus'],
  'call.updated': ['Calls'],
  'call.ended': ['Calls', 'Stats', 'CDR', 'QueueStatus', 'Reputation'],
  'registrations.changed': ['Registrations', 'Stats', 'Health', 'QueueStatus'],
  'trunks.changed': ['TrunkStatus', 'Stats', 'Health'],
  'speech.changed': ['Speech', 'Health'],
  'security.banned': ['Bans', 'Health', 'Stats'],
  // Any configuration change (from another screen, another administrator or the API) refreshes every list.
  'config.applied': ['Stats', 'Health', 'TrunkStatus', 'Settings', 'History', 'Extensions', 'RingGroups', 'Trunks', 'Inbound',
    'Outbound', 'Queues', 'Devices', 'Schedules', 'Menus', 'Scripts', 'PageGroups', 'QueueStatus'],
  'voicemail.new': ['Voicemail'],
  'queue.changed': ['QueueStatus'],
  'provision.fetched': ['Audit'],
  'audit.added': ['Audit'],
  'voicemail.changed': ['Voicemail'],
  'emergency.mode': ['Settings', 'Audit'],
  'callback.new': ['Callbacks'],
  'transcript.changed': ['Transcripts'],
  'call.logged': ['Quality', 'Costs', 'CDR', 'Timeline'],
  'call.blocked': ['CDR'],
  'call.briefing': ['Calls'],
  'text.new': ['Texts', 'Timeline'],
  'radio.updated': ['Calls'], 'radio.transmission': ['Calls'], 'bcf.event': ['Calls'], 'fax.received': ['Faxes'], 'fax.queued': ['Faxes'], 'fax.sending': ['Faxes'], 'fax.sent': ['Faxes'],
  'timeline.changed': ['Timeline'],
  'presence.changed': ['Away', 'QueueStatus'],
  'text.changed': ['Texts'],
  'cost.alert': ['Costs', 'Health'],
  'directory.changed': ['Directory'],
  'hotdesk.in': ['HotDesks'],
  'hotdesk.out': ['HotDesks'],
  'callback.changed': ['Callbacks'],
}

export type Listener = (type: string, data: unknown) => void
const listeners = new Set<Listener>()
export function onServerEvent(l: Listener) {
  listeners.add(l)
  return () => { listeners.delete(l) }
}

/** Opens the server's event stream while signed in, reconnecting on its own. */
export function useLiveEvents(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return
    let es: EventSource | null = null
    let retry: ReturnType<typeof setTimeout> | undefined
    const connect = () => {
      es = new EventSource('./api/events')
      es.addEventListener('hello', () => {
        store.dispatch(api.util.invalidateTags(['Calls', 'Registrations', 'TrunkStatus', 'Stats']))
      })
      es.addEventListener('resync', () => store.dispatch(api.util.resetApiState()))
      for (const type of [...Object.keys(eventTags), 'emergency.call', 'voicemail.urgent', 'script.ran', 'agi.ran', 'call.transcript']) {
        es.addEventListener(type, (ev) => {
          const tags = eventTags[type]
          if (tags) store.dispatch(api.util.invalidateTags(tags))
          let data: unknown = null
          try { data = JSON.parse((ev as MessageEvent).data).data } catch { /* ignore */ }
          listeners.forEach((l) => l(type, data))
        })
      }
      es.onerror = () => {
        es?.close()
        retry = setTimeout(connect, 3000)
      }
    }
    connect()
    return () => { es?.close(); clearTimeout(retry) }
  }, [enabled])
}
