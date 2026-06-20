import { useCallback, useState } from "react"
import { useDropzone } from "react-dropzone"
import { useMutation } from "@tanstack/react-query"
import { toast } from "sonner"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { ThemeSwitcher } from "@/components/theme-switcher"

interface UploadResponse {
  status: string
  filename: string
  short_url: string
  expiration: string
}

interface UploadResult {
  shortURL: string
  expiration: Date
  filename: string
}

async function uploadFile(file: File): Promise<UploadResult> {
  const form = new FormData()
  form.append("file", file)

  const res = await fetch("/", {
    method: "POST",
    body: form,
  })

  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || `upload failed: ${res.status}`)
  }

  const data = (await res.json()) as UploadResponse
  return {
    shortURL: data.short_url,
    expiration: new Date(data.expiration),
    filename: file.name,
  }
}

export default function HomePage() {
  const { t, i18n } = useTranslation(["home", "common"])
  const [result, setResult] = useState<UploadResult | null>(null)
  const [copied, setCopied] = useState(false)

  const mutation = useMutation({
    mutationFn: uploadFile,
    onSuccess: (data) => {
      setResult(data)
    },
    onError: (err: Error) => {
      toast.error(err.message)
    },
  })

  const onDrop = useCallback(
    (accepted: File[]) => {
      if (accepted.length === 0) return
      setResult(null)
      mutation.mutate(accepted[0])
    },
    [mutation],
  )

  const { getRootProps, getInputProps, isDragActive } = useDropzone({
    onDrop,
    multiple: false,
    disabled: mutation.isPending,
  })

  async function copyLink() {
    if (!result) return
    await navigator.clipboard.writeText(result.shortURL)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <main className="min-h-screen flex flex-col items-center justify-center gap-8 p-8">
      <div className="flex flex-col items-center gap-2 text-center">
        <h1 className="text-4xl font-bold tracking-tight">isrv</h1>
        <p className="text-muted-foreground">{t("home:tagline")}</p>
      </div>

      <div
        {...getRootProps()}
        className={[
          "w-full max-w-lg rounded-xl border-2 border-dashed px-8 py-14 text-center cursor-pointer transition-colors",
          isDragActive
            ? "border-primary bg-primary/5"
            : "border-border hover:border-primary/50 hover:bg-muted/40",
          mutation.isPending ? "pointer-events-none opacity-60" : "",
        ].join(" ")}
      >
        <input {...getInputProps()} />
        {mutation.isPending ? (
          <p className="text-sm text-muted-foreground">{t("home:upload.uploading")}</p>
        ) : isDragActive ? (
          <p className="text-sm text-primary font-medium">{t("home:upload.dropToUpload")}</p>
        ) : (
          <p className="text-sm text-muted-foreground">
            {t("home:upload.dragOrClick")}{" "}
            <span className="text-primary underline underline-offset-2">{t("home:upload.clickToBrowse")}</span>
          </p>
        )}
      </div>

      <div className="w-full max-w-lg flex flex-col gap-1.5">
        <p className="text-xs text-muted-foreground font-medium uppercase tracking-wide">{t("home:upload.orViaHTTP")}</p>
        <pre className="rounded-lg bg-muted px-4 py-3 text-xs text-muted-foreground overflow-x-auto leading-relaxed">
          <span className="select-none text-muted-foreground/50">$ </span>
          {`curl -F file=@example.txt ${window.location.origin}/`}
        </pre>
        <Link
          to="/faq"
          hash="command-line"
          className="text-xs text-muted-foreground hover:text-foreground transition-colors w-fit"
        >
          {t("home:upload.moreExamples")}
        </Link>
      </div>

      {result && (
        <div className="w-full max-w-lg rounded-lg border border-border bg-card px-5 py-4 flex flex-col gap-3">
          <p className="text-sm font-medium">{result.filename}</p>
          <div className="flex items-center gap-2">
            <a
              href={result.shortURL}
              target="_blank"
              rel="noreferrer"
              className="flex-1 truncate text-sm text-primary underline underline-offset-2"
            >
              {result.shortURL}
            </a>
            <button
              type="button"
              onClick={copyLink}
              className="shrink-0 rounded-md border border-border px-3 py-1 text-xs hover:bg-muted transition-colors"
            >
              {copied ? t("common:copied") : t("common:copy")}
            </button>
          </div>
          <p className="text-xs text-muted-foreground">
            {t("home:result.expires", {
              date: result.expiration.toLocaleString(i18n.language, {
                dateStyle: "medium",
                timeStyle: "short",
              }),
            })}
          </p>
        </div>
      )}

      <div className="flex items-center gap-6">
        <ThemeSwitcher />
        <nav className="flex gap-4 text-sm text-muted-foreground">
          <Link to="/faq" className="hover:text-foreground transition-colors">
            {t("common:nav.faq")}
          </Link>
          <Link to="/about" className="hover:text-foreground transition-colors">
            {t("common:nav.about")}
          </Link>
          <Link to="/abuse" className="hover:text-foreground transition-colors">
            {t("common:nav.abuse")}
          </Link>
          <Link to="/privacy" className="hover:text-foreground transition-colors">
            {t("common:nav.privacy")}
          </Link>
        </nav>
      </div>
    </main>
  )
}
