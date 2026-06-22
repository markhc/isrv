import { useEffect, useMemo, useState } from "react"
import {
  useQuery,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query"
import {
  createColumnHelper,
  flexRender,
  getCoreRowModel,
  useReactTable,
  type SortingState,
} from "@tanstack/react-table"
import { useTranslation } from "react-i18next"
import { ArrowDown, ArrowUp } from "lucide-react"
import { toast } from "sonner"
import {
  listFiles,
  type AdminFile,
  type ListFilesParams,
} from "@/lib/admin-api"
import { formatBytes, formatDate } from "@/lib/format"
import { FileDetailPanel } from "@/components/admin/file-detail-panel"
import { DeleteFileDialog } from "@/components/admin/delete-file-dialog"

const PAGE_SIZE = 25

// Maps a react-table column id to the API sort key. Columns absent here are not sortable.
const SORT_KEYS: Record<string, string> = {
  name: "name",
  size: "size",
  createdAt: "created_at",
}

function useDebounced<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const handle = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(handle)
  }, [value, delay])
  return debounced
}

interface FilesDashboardProps {
  // localeKey changes when the loaded admin translations change; it forces the
  // memoized column definitions (whose headers are translated) to recompute.
  localeKey: string
}

export function FilesDashboard({ localeKey }: FilesDashboardProps) {
  const { t, i18n } = useTranslation("admin")
  const queryClient = useQueryClient()

  const [search, setSearch] = useState("")
  const [ip, setIp] = useState("")
  const [sorting, setSorting] = useState<SortingState>([{ id: "createdAt", desc: true }])
  const [pageIndex, setPageIndex] = useState(0)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminFile | null>(null)

  const debouncedSearch = useDebounced(search, 300)
  const debouncedIp = useDebounced(ip, 300)

  // Reset to the first page whenever filters change.
  useEffect(() => {
    setPageIndex(0)
  }, [debouncedSearch, debouncedIp, sorting])

  const sort = sorting[0]
  const params: ListFilesParams = {
    search: debouncedSearch || undefined,
    ip: debouncedIp || undefined,
    sortBy: sort ? SORT_KEYS[sort.id] : undefined,
    sortDir: sort ? (sort.desc ? "desc" : "asc") : undefined,
    limit: PAGE_SIZE,
    offset: pageIndex * PAGE_SIZE,
  }

  const filesQuery = useQuery({
    queryKey: ["admin", "files", params],
    queryFn: () => listFiles(params),
    placeholderData: keepPreviousData,
  })

  useEffect(() => {
    if (filesQuery.isError) toast.error(t("errors.loadFailed"))
  }, [filesQuery.isError, t])

  const data = filesQuery.data?.items ?? []
  const total = filesQuery.data?.total ?? 0
  const selectedFile = data.find((f) => f.id === selectedId) ?? null

  const columns = useMemo(() => {
    const helper = createColumnHelper<AdminFile>()
    return [
      helper.accessor("id", {
        header: t("table.id"),
        cell: (info) => (
          <span className="block max-w-[16rem] truncate font-mono" title={info.getValue()}>
            {info.getValue()}
          </span>
        ),
      }),
      helper.accessor("name", {
        header: t("table.name"),
        cell: (info) => (
          <span className="block max-w-[16rem] truncate font-mono" title={info.getValue()}>
            {info.getValue()}
          </span>
        ),
      }),
      helper.accessor("size", {
        header: t("table.size"),
        cell: (info) => <span className="whitespace-nowrap">{formatBytes(info.getValue())}</span>,
      }),
      helper.accessor("contentType", {
        header: t("table.type"),
        enableSorting: false,
        cell: (info) => (
          <span className="block max-w-[10rem] truncate text-xs text-muted-foreground" title={info.getValue()}>
            {info.getValue() || "-"}
          </span>
        ),
      }),
      helper.accessor("createdAt", {
        header: t("table.created"),
        cell: (info) => (
          <span className="whitespace-nowrap text-muted-foreground">
            {formatDate(info.getValue(), i18n.language)}
          </span>
        ),
      }),
    ]
    // localeKey forces a recompute once translations for a new language load.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t, localeKey])

  const table = useReactTable({
    data,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    manualSorting: true,
    manualPagination: true,
    getCoreRowModel: getCoreRowModel(),
  })

  const from = total === 0 ? 0 : pageIndex * PAGE_SIZE + 1
  const to = Math.min((pageIndex + 1) * PAGE_SIZE, total)
  const hasNext = to < total

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex min-h-0 flex-1 flex-col gap-4 lg:flex-row">
        {/* Left pane: file list */}
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border border-border">
          <div className="min-h-0 flex-1 overflow-auto">
            <table className="w-full text-left text-sm">
              <thead className="sticky top-0 z-10 border-b border-border bg-muted/95 backdrop-blur">
                {table.getHeaderGroups().map((headerGroup) => (
                  <tr key={headerGroup.id}>
                    {headerGroup.headers.map((header) => {
                      const canSort = header.column.getCanSort()
                      const sorted = header.column.getIsSorted()
                      return (
                        <th key={header.id} className="px-3 py-2 font-medium text-muted-foreground">
                          {canSort ? (
                            <button
                              type="button"
                              onClick={header.column.getToggleSortingHandler()}
                              className="flex items-center gap-1 transition-colors hover:text-foreground"
                            >
                              {flexRender(header.column.columnDef.header, header.getContext())}
                              {sorted === "asc" && <ArrowUp className="size-3" />}
                              {sorted === "desc" && <ArrowDown className="size-3" />}
                            </button>
                          ) : (
                            flexRender(header.column.columnDef.header, header.getContext())
                          )}
                        </th>
                      )
                    })}
                  </tr>
                ))}
              </thead>
              <tbody>
                {data.length === 0 ? (
                  <tr>
                    <td colSpan={columns.length} className="px-3 py-8 text-center text-muted-foreground">
                      {t("table.empty")}
                    </td>
                  </tr>
                ) : (
                  table.getRowModel().rows.map((row) => {
                    const isSelected = row.original.id === selectedId
                    return (
                      <tr
                        key={row.id}
                        onClick={() => setSelectedId(row.original.id)}
                        className={
                          "cursor-pointer border-b border-border last:border-0 " +
                          (isSelected ? "bg-primary/15" : "hover:bg-muted/40")
                        }
                      >
                        {row.getVisibleCells().map((cell) => (
                          <td key={cell.id} className="px-3 py-2 align-middle">
                            {flexRender(cell.column.columnDef.cell, cell.getContext())}
                          </td>
                        ))}
                      </tr>
                    )
                  })
                )}
              </tbody>
            </table>
          </div>

          <div className="flex items-center justify-between gap-3 border-t border-border px-3 py-2">
            <div className="flex flex-wrap items-center gap-3">
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t("search.query")}
                className="w-56 rounded-md border border-input bg-background px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
              <input
                type="text"
                value={ip}
                onChange={(e) => setIp(e.target.value)}
                placeholder={t("search.ip")}
                className="w-48 rounded-md border border-input bg-background px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </div>
            <p className="text-xs text-muted-foreground">
              {t("pagination.showing", { from, to, total })}
            </p>
            <div className="flex gap-2">
              <button
                type="button"
                disabled={pageIndex === 0}
                onClick={() => setPageIndex((p) => Math.max(0, p - 1))}
                className="rounded-md border border-border px-3 py-1.5 text-sm transition-colors hover:bg-muted disabled:opacity-50"
              >
                {t("pagination.previous")}
              </button>
              <button
                type="button"
                disabled={!hasNext}
                onClick={() => setPageIndex((p) => p + 1)}
                className="rounded-md border border-border px-3 py-1.5 text-sm transition-colors hover:bg-muted disabled:opacity-50"
              >
                {t("pagination.next")}
              </button>
            </div>
          </div>
        </div>

        {/* Right pane: selected file details */}
        <div className="flex min-h-0 w-full shrink-0 flex-col overflow-auto rounded-lg border border-border lg:w-[26rem] xl:w-[32rem]">
          <FileDetailPanel file={selectedFile} onDelete={setDeleteTarget} />
        </div>
      </div>

      <DeleteFileDialog
        file={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onDeleted={() => {
          if (deleteTarget?.id === selectedId) setSelectedId(null)
          queryClient.invalidateQueries({ queryKey: ["admin", "files"] })
        }}
      />
    </div>
  )
}
