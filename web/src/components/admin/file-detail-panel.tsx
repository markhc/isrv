import { useQuery } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { Copy, Download, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { downloadUrl, type AdminFile } from "@/lib/admin-api"
import { formatBytes, formatDate } from "@/lib/format"

// Maximum number of bytes to fetch when previewing a text file.
const MAX_TEXT_PREVIEW_BYTES = 256 * 1024

function isImage(contentType: string): boolean {
  return contentType.startsWith("image/")
}

function isVideo(contentType: string): boolean {
  return contentType.startsWith("video/")
}

function isAudio(contentType: string): boolean {
  return contentType.startsWith("audio/")
}

function isText(contentType: string): boolean {
  if (contentType.startsWith("text/")) return true
  return [
    "application/json",
    "application/xml",
    "application/javascript",
    "application/x-yaml",
  ].includes(contentType)
}

interface FileDetailPanelProps {
  file: AdminFile | null
  onDelete: (file: AdminFile) => void
}

export function FileDetailPanel({ file, onDelete }: FileDetailPanelProps) {
  const { t, i18n } = useTranslation("admin")

  const textPreview = file !== null && isText(file.contentType)
  const textQuery = useQuery({
    queryKey: ["admin", "preview", file?.id],
    enabled: textPreview,
    retry: false,
    queryFn: async () => {
      const res = await fetch(downloadUrl(file!.id), { credentials: "same-origin" })
      if (!res.ok) throw new Error(`preview failed: ${res.status}`)
      const text = await res.text()
      return text.slice(0, MAX_TEXT_PREVIEW_BYTES)
    },
  })

  if (!file) {
    return (
      <div className="flex h-full items-center justify-center p-6 text-center text-sm text-muted-foreground">
        {t("detail.empty")}
      </div>
    )
  }

  const fileUrl = `${window.location.origin}${downloadUrl(file.id)}`
  const showImage = isImage(file.contentType)
  const showVideo = isVideo(file.contentType)
  const showAudio = isAudio(file.contentType)
  const showOther = !showImage && !showVideo && !showAudio && !textPreview

  async function copyUrl() {
    try {
      await navigator.clipboard.writeText(fileUrl)
      toast.success(t("detail.urlCopied"))
    } catch {
      // clipboard unavailable; ignore
    }
  }

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between gap-2 border-b border-border p-4">
        <h2 className="truncate text-sm font-semibold" title={file.name}>
          {file.name}
        </h2>
        <div className="flex shrink-0 items-center gap-1">
          <a
            href={`${downloadUrl(file.id)}/${encodeURIComponent(file.name)}`}
            target="_blank"
            rel="noreferrer"
            aria-label={t("actions.download")}
            className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <Download className="size-4" />
          </a>
          <button
            type="button"
            aria-label={t("actions.delete")}
            onClick={() => onDelete(file)}
            className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
          >
            <Trash2 className="size-4" />
          </button>
        </div>
      </div>

      <dl className="grid grid-cols-[7.5rem_1fr] gap-x-3 gap-y-2 p-4 text-sm">
        <dt className="text-muted-foreground">{t("table.id")}</dt>
        <dd className="font-mono text-xs break-all">{file.id}</dd>

        <dt className="text-muted-foreground">{t("detail.url")}</dt>
        <dd className="flex items-start gap-2">
          <a
            href={fileUrl}
            target="_blank"
            rel="noreferrer"
            className="break-all text-primary underline underline-offset-2"
          >
            {fileUrl}
          </a>
          <button
            type="button"
            aria-label={t("detail.copyUrl")}
            onClick={copyUrl}
            className="shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <Copy className="size-3.5" />
          </button>
        </dd>

        <dt className="text-muted-foreground">{t("table.size")}</dt>
        <dd>{formatBytes(file.size)}</dd>

        <dt className="text-muted-foreground">{t("table.type")}</dt>
        <dd className="break-all">{file.contentType || "-"}</dd>

        <dt className="text-muted-foreground">{t("detail.uploadedBy")}</dt>
        <dd className="font-mono text-xs">{file.ipAddress || "-"}</dd>

        <dt className="text-muted-foreground">{t("table.downloads")}</dt>
        <dd>{file.downloads}</dd>

        <dt className="text-muted-foreground">{t("table.encryptionVersion")}</dt>
        <dd>
          {file.encryptionVersion > 0
            ? t("table.fileEncrypted", { version: file.encryptionVersion })
            : t("table.fileNotEncrypted")}
        </dd>

        <dt className="text-muted-foreground">{t("table.created")}</dt>
        <dd>{formatDate(file.createdAt, i18n.language)}</dd>

        <dt className="text-muted-foreground">{t("table.expires")}</dt>
        <dd>{formatDate(file.expiration, i18n.language)}</dd>
      </dl>

      <div className="min-h-0 flex-1 overflow-auto border-t border-border p-4">
        <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {t("detail.preview")}
        </p>

        {showImage && (
          <img
            src={downloadUrl(file.id)}
            alt={file.name}
            className="max-h-[50vh] max-w-full rounded-md object-contain"
          />
        )}

        {showVideo && (
          <video src={downloadUrl(file.id)} controls className="max-h-[50vh] max-w-full rounded-md" />
        )}

        {showAudio && <audio src={downloadUrl(file.id)} controls className="w-full" />}

        {textPreview && (
          <>
            {textQuery.isLoading && (
              <p className="text-sm text-muted-foreground">{t("preview.loading")}</p>
            )}
            {textQuery.isError && <p className="text-sm text-destructive">{t("preview.failed")}</p>}
            {textQuery.data != null && (
              <pre className="whitespace-pre-wrap break-words rounded-md bg-muted p-3 text-xs leading-relaxed">
                {textQuery.data}
              </pre>
            )}
          </>
        )}

        {showOther && <p className="text-sm text-muted-foreground">{t("preview.notSupported")}</p>}
      </div>
    </div>
  )
}
