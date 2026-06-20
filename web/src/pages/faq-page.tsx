import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { getServerConfig } from "@/lib/config"

interface FAQItem {
  id?: string
  question: string
  answer: string
  code?: string
}

export default function FAQPage() {
  const { t } = useTranslation(["faq", "common"])
  const origin = window.location.origin
  const config = getServerConfig()
  const items = t("faq:items", { 
    config,
    returnObjects: true }) as FAQItem[]

  function buildCode(codeKey: string): string {
    if (codeKey === "commandLineUpload") {
      return [
        t("faq:code.simpleUpload"),
        `$ curl -F file=@photo.jpg ${origin}/`,
        "",
        t("faq:code.expiresInHours"),
        `$ curl -F file=@document.pdf -F expires=24 ${origin}/`,
        "",
        t("faq:code.expiresAsTimestamp"),
        `$ curl -F file=@archive.zip -F expires=1767225600 ${origin}/`,
      ].join("\n")
    } else if (codeKey === "commandLineDelete") {
      return [
        t("faq:code.uploadReturnsToken"),
        `$ curl -F file=@photo.jpg ${origin}/`,
        `{ "shortURL": "${origin}/abc123", "fileToken": "def456", ... }`,
        "",
        t("faq:code.deleteFileHeaderToken"),
        `$ curl -X DELETE ${origin}/abc123 -H "Authorization: Bearer def456"`,
        "",
        t("faq:code.deleteFileQueryToken"),
        `$ curl -X DELETE ${origin}/abc123?token=def456`,
      ].join("\n")
    } else {
      return codeKey
    }
  }

  return (
    <main className="min-h-screen flex flex-col items-center p-8 pt-16">
      <div className="w-full max-w-4xl flex flex-col gap-10">
        <div className="flex flex-col gap-2">
          <Link to="/" className="text-sm text-muted-foreground hover:text-foreground transition-colors w-fit">
            {t("common:back")}
          </Link>
          <h1 className="text-3xl font-bold tracking-tight">{t("faq:title")}</h1>
          <p className="text-muted-foreground">{t("faq:subtitle")}</p>
        </div>

        <div className="flex flex-col gap-6">
          {items.map((item) => (
            <div key={item.question} id={item.id} className="flex flex-col gap-1.5">
              <h2 className="font-semibold">{item.question}</h2>
              <p className="text-sm text-muted-foreground leading-relaxed">{item.answer}</p>
              {item.code && (
                <pre className="rounded-lg bg-muted px-4 py-3 mt-1 text-xs text-muted-foreground overflow-x-auto leading-relaxed">
                  {buildCode(item.code)}
                </pre>
              )}
            </div>
          ))}
        </div>
      </div>
    </main>
  )
}
