// Shared pieces for configuration screens: a slide-over editor, a searchable table, confirm-delete.
import * as Dialog from '@radix-ui/react-dialog'
import {
  flexRender, getCoreRowModel, getFilteredRowModel, getSortedRowModel, useReactTable, type ColumnDef, type SortingState,
} from '@tanstack/react-table'
import { ArrowDown, ArrowUp, Search, Save, Trash2, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import { errorOf, useMeQuery, type ApiErrorBody } from './api'
import { ErrorBox } from './ui'

export function useIsAdmin() {
  const { data } = useMeQuery()
  return data?.role === 'admin'
}

/** Runs a save or delete, toasts the outcome and returns the error body for the form to show. */
export async function attempt(fn: () => Promise<unknown>, ok: string, opts?: { problemsInToast?: boolean }): Promise<ApiErrorBody | null> {
  try {
    await fn()
    toast.success(ok)
    return null
  } catch (e) {
    const err = errorOf(e)
    // A form shows field problems under its fields; anything else (removing, say) has nowhere to show them.
    if (!err.problems?.length) toast.error(err.message)
    else if (opts?.problemsInToast) toast.error(err.problems.map((x) => x.message).join('; '))
    return err
  }
}

export function Drawer({ open, onClose, title, icon, children, onSave, saving, error, footer, saveLabel = 'Save' }: {
  open: boolean; onClose: () => void; title: string; icon: ReactNode; children: ReactNode
  onSave?: () => void; saving?: boolean; error?: ApiErrorBody | null; footer?: ReactNode; saveLabel?: string
}) {
  return (
    <Dialog.Root open={open} onOpenChange={(v) => { if (!v) onClose() }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/50 backdrop-blur-sm" />
        <Dialog.Content aria-describedby={undefined} className="fixed inset-y-0 right-0 z-50 flex w-full max-w-xl flex-col border-l border-slate-800 bg-slate-950 shadow-2xl">
          <form noValidate className="flex h-full flex-col" onSubmit={(e) => { e.preventDefault(); onSave?.() }}>
            <header className="flex items-center gap-3 border-b border-slate-800 px-6 py-4">
              {icon}
              <Dialog.Title className="flex-1 text-lg font-bold text-slate-50">{title}</Dialog.Title>
              <Dialog.Close className="rounded p-1.5 text-slate-500 hover:bg-slate-800" aria-label="Close"><X size={18} aria-hidden /></Dialog.Close>
            </header>
            <div className="flex-1 space-y-5 overflow-auto p-6">
              {error && <ErrorBox message={error.message} problems={error.problems} />}
              {children}
            </div>
            <footer className="flex items-center gap-2 border-t border-slate-800 px-6 py-4">
              {footer}
              <span className="flex-1" />
              <button type="button" className="btn" onClick={onClose}>{onSave ? 'Cancel' : 'Close'}</button>
              {onSave && <button className="btn btn-primary" disabled={saving}><Save size={15} aria-hidden /> {saveLabel}</button>}
            </footer>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

export function DeleteButton({ what, onDelete }: { what: string; onDelete: () => void }) {
  const [sure, setSure] = useState(false)
  return sure ? (
    <span className="flex items-center gap-2 text-sm text-rose-300">
      Remove {what}?
      <button type="button" className="btn btn-danger" onClick={onDelete}><Trash2 size={14} aria-hidden /> Yes, remove</button>
      <button type="button" className="btn" onClick={() => setSure(false)}>No</button>
    </span>
  ) : (
    <button type="button" className="btn btn-danger" onClick={() => setSure(true)}><Trash2 size={14} aria-hidden /> Remove</button>
  )
}

export function Table<T>({ data, columns, onRow, empty, search = true }: {
  data: T[]; columns: ColumnDef<T, any>[]; onRow?: (row: T) => void; empty?: ReactNode; search?: boolean
}) {
  const [sorting, setSorting] = useState<SortingState>([])
  const [filter, setFilter] = useState('')
  const table = useReactTable({
    data, columns, state: { sorting, globalFilter: filter }, onSortingChange: setSorting, onGlobalFilterChange: setFilter,
    getCoreRowModel: getCoreRowModel(), getSortedRowModel: getSortedRowModel(), getFilteredRowModel: getFilteredRowModel(),
  })
  if (!data.length && empty) return <>{empty}</>
  return (
    <div className="card overflow-hidden">
      {search && (
        <div className="flex items-center gap-2 border-b border-slate-800 px-4 py-2.5">
          <Search size={15} className="text-slate-500" aria-hidden />
          <input className="w-full bg-transparent text-sm text-slate-100 outline-none placeholder:text-slate-600" placeholder="Filter…"
            aria-label="Filter rows" value={filter} onChange={(e) => setFilter(e.target.value)} />
          <span className="whitespace-nowrap text-xs text-slate-500">{table.getRowModel().rows.length} of {data.length}</span>
        </div>
      )}
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead className="bg-slate-900/60 text-left text-xs uppercase tracking-wide text-slate-500">
            {table.getHeaderGroups().map((hg) => (
              <tr key={hg.id}>
                {hg.headers.map((h) => (
                  <th key={h.id} className="px-4 py-2.5 font-semibold"
                    aria-sort={h.column.getIsSorted() === 'asc' ? 'ascending' : h.column.getIsSorted() === 'desc' ? 'descending' : undefined}>
                    {h.isPlaceholder ? null : !h.column.getCanSort() ? flexRender(h.column.columnDef.header, h.getContext()) : (
                      <button type="button" className="inline-flex items-center gap-1 uppercase tracking-wide" onClick={h.column.getToggleSortingHandler()}>
                        {flexRender(h.column.columnDef.header, h.getContext())}
                        {h.column.getIsSorted() === 'asc' ? <ArrowUp size={12} aria-hidden /> : h.column.getIsSorted() === 'desc' ? <ArrowDown size={12} aria-hidden /> : null}
                      </button>
                    )}
                  </th>
                ))}
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.length === 0 && (
              <tr><td colSpan={columns.length} className="px-4 py-6 text-center text-slate-500">Nothing matches “{filter}”.</td></tr>
            )}
            {table.getRowModel().rows.map((r) => (
              <tr key={r.id} onClick={() => onRow?.(r.original)}
                tabIndex={onRow ? 0 : undefined}
                onKeyDown={onRow ? (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onRow(r.original) } } : undefined}
                className={onRow ? 'cursor-pointer border-t border-slate-800/70 hover:bg-slate-900/70 focus:bg-slate-900/70 focus:outline-none focus-visible:ring-1 focus-visible:ring-sky-400' : 'border-t border-slate-800/70'}>
                {r.getVisibleCells().map((c) => <td key={c.id} className="px-4 py-2.5 align-middle">{flexRender(c.column.columnDef.cell, c.getContext())}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
