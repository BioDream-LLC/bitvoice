import { useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'

/** Opens the item named by ?open= (a number or an id) once the list has loaded, then clears the parameter,
 * so links from the call flow, Health and search land in the right editor. */
export function useOpenParam<T extends { id?: string; number?: string }>(items: T[] | undefined, open: (item: T) => void) {
  const [params, setParams] = useSearchParams()
  const want = params.get('open')
  useEffect(() => {
    if (!want || !items) return
    const hit = items.find((i) => i.number === want || i.id === want)
    if (hit) open(hit)
    const next = new URLSearchParams(params)
    next.delete('open')
    setParams(next, { replace: true })
  }, [want, items]) // eslint-disable-line react-hooks/exhaustive-deps
}
