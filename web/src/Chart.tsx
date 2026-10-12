import * as echarts from 'echarts/core'
import { BarChart, LineChart, PieChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { useEffect, useMemo, useRef } from 'react'

echarts.use([BarChart, LineChart, PieChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

/** A thin wrapper around Apache ECharts that follows the theme's colour ramp. */
export function Chart({ option, height = 240, label }: { option: echarts.EChartsCoreOption; height?: number; label: string }) {
  const el = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)
  useEffect(() => {
    if (!el.current) return
    chart.current = echarts.init(el.current, undefined, { renderer: 'canvas' })
    const ro = new ResizeObserver(() => chart.current?.resize())
    ro.observe(el.current)
    return () => { ro.disconnect(); chart.current?.dispose() }
  }, [])
  const themed = useMemo(() => {
    const css = getComputedStyle(document.documentElement)
    const text = css.getPropertyValue('--color-slate-400').trim() || '#94a3b8'
    const line = css.getPropertyValue('--color-slate-800').trim() || '#1e293b'
    return { textStyle: { color: text }, backgroundColor: 'transparent', grid: { left: 36, right: 12, top: 24, bottom: 28 },
      ...option, _line: line }
  }, [option])
  useEffect(() => {
    const { _line, ...o } = themed as Record<string, unknown>
    void _line
    chart.current?.setOption(o, true)
  }, [themed])
  return <div ref={el} style={{ height }} role="img" aria-label={label} />
}
