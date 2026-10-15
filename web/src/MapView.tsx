import 'maplibre-gl/dist/maplibre-gl.css'
import * as maplibregl from 'maplibre-gl'
import workerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?url'
import { useEffect, useRef } from 'react'
import { useMapSourceQuery } from './api'

// MapLibre 6 runs its tile worker from its own file, served here with the rest of the interface.
maplibregl.setWorkerUrl(workerUrl)

export interface MapPoint { id: string; lat: number; lon: number; radius?: number; label?: string; color?: string }

/** A circle of radius metres around a point, as a GeoJSON ring (64 sides). */
function circle(lat: number, lon: number, radius: number): [number, number][] {
  const out: [number, number][] = []
  const dLat = radius / 111_320, dLon = radius / (111_320 * Math.cos((lat * Math.PI) / 180))
  for (let i = 0; i <= 64; i++) {
    const a = (i / 64) * 2 * Math.PI
    out.push([lon + dLon * Math.cos(a), lat + dLat * Math.sin(a)])
  }
  return out
}

/** A map of points with their uncertainty circles, from the tiles chosen in the 911 settings. It shows
 * nothing when no map source is set (the position is still given in figures beside it). */
export function MapView({ points, label, height = 220 }: { points: MapPoint[]; label: string; height?: number }) {
  const { data: src } = useMapSourceQuery()
  const box = useRef<HTMLDivElement>(null)
  const map = useRef<maplibregl.Map | null>(null)
  const markers = useRef<maplibregl.Marker[]>([])
  const tiles = src?.tiles
  const has = points.length > 0

  useEffect(() => {
    if (!box.current || !tiles || !has) return
    const url = tiles.startsWith('./') ? new URL(tiles.replace(/{([xyz])}/g, '__$1__'), window.location.href).href.replace(/__([xyz])__/g, '{$1}') : tiles
    const m = new maplibregl.Map({
      container: box.current,
      style: { version: 8, sources: { base: { type: 'raster', tiles: [url], tileSize: 256, maxzoom: src?.maxZoom || 19, attribution: src?.attribution || '' } },
        layers: [{ id: 'base', type: 'raster', source: 'base' }] },
      center: points[0] ? [points[0].lon, points[0].lat] : [0, 0], zoom: 15, attributionControl: { compact: true },
    })
    m.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right')
    m.addControl(new maplibregl.ScaleControl({ unit: 'metric' }), 'bottom-left')
    map.current = m
    return () => { markers.current.forEach((x) => x.remove()); markers.current = []; m.remove(); map.current = null }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tiles, has, src?.maxZoom, src?.attribution])

  const key = points.map((p) => `${p.id}:${p.lat}:${p.lon}:${p.radius ?? 0}`).join('|')
  useEffect(() => {
    const m = map.current
    if (!m) return
    const draw = () => {
      markers.current.forEach((x) => x.remove())
      markers.current = points.map((p) => {
        const el = document.createElement('div')
        el.className = 'h-3 w-3 rounded-full border-2 border-white shadow'
        el.style.background = p.color || '#f43f5e'
        el.title = p.label || ''
        return new maplibregl.Marker({ element: el }).setLngLat([p.lon, p.lat]).addTo(m)
      })
      const fc = { type: 'FeatureCollection' as const, features: points.filter((p) => p.radius).map((p) => ({ type: 'Feature' as const, properties: {}, geometry: { type: 'Polygon' as const, coordinates: [circle(p.lat, p.lon, p.radius!)] } })) }
      const s = m.getSource('uncertainty') as maplibregl.GeoJSONSource | undefined
      if (s) s.setData(fc)
      else {
        m.addSource('uncertainty', { type: 'geojson', data: fc })
        m.addLayer({ id: 'uncertainty-fill', type: 'fill', source: 'uncertainty', paint: { 'fill-color': '#f43f5e', 'fill-opacity': 0.15 } })
        m.addLayer({ id: 'uncertainty-line', type: 'line', source: 'uncertainty', paint: { 'line-color': '#f43f5e', 'line-width': 1.5 } })
      }
      if (points.length === 1) {
        const p = points[0]
        const r = p.radius ?? 0
        const z = r > 2000 ? 12 : r > 500 ? 14 : r > 100 ? 16 : 17
        m.jumpTo({ center: [p.lon, p.lat], zoom: Math.min(z, src?.maxZoom || 19) })
      } else if (points.length > 1) {
        const b = new maplibregl.LngLatBounds()
        points.forEach((p) => b.extend([p.lon, p.lat]))
        m.fitBounds(b, { padding: 40, maxZoom: Math.min(16, src?.maxZoom || 19), duration: 0 })
      }
    }
    if (m.isStyleLoaded()) draw()
    else m.once('load', draw)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, tiles, has])

  if (!tiles || !points.length) return null
  return <div ref={box} role="img" aria-label={label} className="w-full overflow-hidden rounded-lg border border-slate-800" style={{ height }} />
}
