import {
  useConferencesQuery, useExtensionsQuery, useFaxBoxesQuery, useMenusQuery, usePageGroupsQuery, useQueuesQuery, useRingGroupsQuery, useSettingsQuery } from './api'

/** Every number something already answers on: extensions, groups, queues, menus and the system's own
 * (echo test, voicemail, park slots, emergency numbers). */
export function useTakenNumbers(): Set<string> {
  const { data: x } = useExtensionsQuery()
  const { data: g } = useRingGroupsQuery()
  const { data: q } = useQueuesQuery()
  const { data: pg } = usePageGroupsQuery()
  const { data: m } = useMenusQuery()
  const { data: fx } = useFaxBoxesQuery()
  const { data: cf } = useConferencesQuery()
  const { data: s } = useSettingsQuery()
  const taken = new Set<string>()
  for (const list of [x?.items, g?.items, q?.items, pg?.items, m?.items, fx?.items, cf?.items]) list?.forEach((i: { number?: string }) => i.number && taken.add(i.number))
  const sys = s?.system
  if (sys) {
    ;[sys.echoNumber, sys.voicemailNumber, ...(sys.emergencyNumbers ?? [])].forEach((n) => n && taken.add(n))
    const park = Number(sys.parkNumber)
    if (sys.parkNumber && Number.isFinite(park)) for (let i = 0; i <= 9; i++) taken.add(String(park + i))
  }
  return taken
}

/** The first free number from start upwards. */
export function nextFree(taken: Set<string>, start: number): string {
  let n = start
  while (taken.has(String(n))) n++
  return String(n)
}
