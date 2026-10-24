import { useState } from "react"
import { Link, useLocation, useParams } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import {
  decryptContainer,
  E2EEAuthenticationError,
  E2EEFormatError,
  E2EEKeyError,
  isE2EESupported,
  maxEncryptedContainerSize,
  validateKeyToken
} from "@/features/e2ee/crypto"
import { getServerConfig } from "@/lib/config"

type DownloadStatus = "idle" | "working" | "complete" | "error"

export default function DownloadPage() {
  const { t } = useTranslation(["download", "common"])
  const { id: fileId } = useParams({ from: "/d/$id" })
  const hash = useLocation({ select: (location) => location.hash })
  const [status, setStatus] = useState<DownloadStatus>("idle")
  const [message, setMessage] = useState("")

  const supported = isE2EESupported()
  const keyToken = hash.startsWith("#") ? hash.slice(1) : hash
  const canDownload = supported && keyToken.length > 0 && status !== "working"

  const download = async () => {
    if (!supported) {
      setStatus("error")
      setMessage(t("download:errors.unsupportedBrowser"))
      return
    }
    if (!keyToken) {
      setStatus("error")
      setMessage(t("download:errors.missingKey"))
      return
    }

    try {
      validateKeyToken(keyToken)
    } catch (error) {
      if (error instanceof E2EEKeyError) {
        setStatus("error")
        setMessage(t("download:errors.invalidKey"))
        return
      }
      throw error
    }

    setStatus("working")
    setMessage("")

    try {
      const response = await fetch(`/api/e2ee/files/${encodeURIComponent(fileId)}/content`, {
        headers: { Accept: "application/octet-stream" },
      })
      if (!response.ok) {
        throw new DownloadRequestError()
      }

      const maxSize = maxEncryptedContainerSize(
        getServerConfig().e2eeMaxFileSizeMb * 1024 * 1024
      )
      const contentLength = response.headers.get("Content-Length")
      if (contentLength !== null) {
        const declaredSize = Number(contentLength)
        if (!Number.isSafeInteger(declaredSize) || declaredSize < 0 || declaredSize > maxSize) {
          throw new E2EEFormatError("encrypted file has an invalid length")
        }
      }

      const ciphertext = await response.blob()
      if (ciphertext.size > maxSize) {
        throw new E2EEFormatError("encrypted file exceeds the configured size limit")
      }

      const decrypted = await decryptContainer(ciphertext, keyToken)
      saveBlob(decrypted.plaintext, safeDownloadName(decrypted.metadata.name))
      setStatus("complete")
      setMessage(t("download:complete", { name: decrypted.metadata.name }))
    } catch (error) {
      setStatus("error")
      setMessage(downloadErrorMessage(error, t))
    }
  }

  const initialMessage = !supported
    ? t("download:errors.unsupportedBrowser")
    : !keyToken
      ? t("download:errors.missingKey")
      : t("download:ready")

  return (
    <main className="min-h-screen flex flex-col items-center justify-center p-8">
      <section className="w-full max-w-xl rounded-xl border border-border bg-card p-8 shadow-sm">
        <div className="flex flex-col gap-3">
          <Link to="/" className="text-sm text-muted-foreground hover:text-foreground transition-colors w-fit">
            {t("common:back")}
          </Link>
          <h1 className="text-3xl font-bold tracking-tight">{t("download:title")}</h1>
          <p className="text-sm text-muted-foreground" aria-live="polite">
            {status === "idle" ? initialMessage : message}
          </p>
        </div>

        <div className="mt-8 flex flex-col gap-3">
          <button
            type="button"
            onClick={download}
            disabled={!canDownload}
            className="rounded-lg bg-primary px-5 py-3 text-sm font-medium text-primary-foreground transition-opacity disabled:cursor-not-allowed disabled:opacity-50"
          >
            {status === "working" ? t("download:working") : t("download:action")}
          </button>
          <p className="text-xs text-muted-foreground">{t("download:privacyNotice")}</p>
        </div>
      </section>
    </main>
  )
}

class DownloadRequestError extends Error {}

function downloadErrorMessage(
  error: unknown,
  t: (key: string, options?: Record<string, unknown>) => string
): string {
  if (error instanceof E2EEAuthenticationError) {
    return t("download:errors.authentication")
  }
  if (error instanceof E2EEKeyError) {
    return t("download:errors.invalidKey")
  }
  if (error instanceof E2EEFormatError) {
    return t("download:errors.damagedFile")
  }
  return t("download:errors.fetchFailed")
}

function safeDownloadName(name: string): string {
  const withoutControlCharacters = [...name]
    .filter((character) => {
      const codePoint = character.codePointAt(0) ?? 0
      return codePoint >= 32 && codePoint !== 127
    })
    .join("")
  const sanitized = withoutControlCharacters
    .replaceAll("/", "_")
    .replaceAll("\\", "_")
    .trim()
  return [...sanitized].slice(0, 255).join("") || "download"
}

function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = filename
  anchor.style.display = "none"
  document.body.append(anchor)
  anchor.click()
  anchor.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
