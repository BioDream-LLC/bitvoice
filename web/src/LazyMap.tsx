import { lazy, Suspense } from 'react'
import type { MapPoint } from './MapView'

// The map engine (MapLibre) is large: it loads only when a map is first shown.
const MapView = lazy(() => import('./MapView').then((m) => ({ default: m.MapView })))

export function LazyMap(props: { points: MapPoint[]; label: string; height?: number }) {
  return <Suspense fallback={null}><MapView {...props} /></Suspense>
}
