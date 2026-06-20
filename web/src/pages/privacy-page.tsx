import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

export default function PrivacyPage() {
  const { t } = useTranslation(["privacy", "common"])

  return (
    <main className="min-h-screen flex flex-col items-center p-8 pt-16">
      <div className="w-full max-w-2xl flex flex-col gap-10">
        <div className="flex flex-col gap-2">
          <Link to="/" className="text-sm text-muted-foreground hover:text-foreground transition-colors w-fit">
            {t("common:back")}
          </Link>
          <h1 className="text-3xl font-bold tracking-tight">{t("privacy:title")}</h1>
          <p className="text-muted-foreground">{t("privacy:subtitle")}</p>
        </div>

        <div className="flex flex-col gap-8 text-sm text-muted-foreground leading-relaxed">
          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("privacy:sections.anonymity.title")}</h2>
            <p>{t("privacy:sections.anonymity.body")}</p>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("privacy:sections.logging.title")}</h2>
            <p>{t("privacy:sections.logging.body")}</p>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("privacy:sections.storage.title")}</h2>
            <p>{t("privacy:sections.storage.body")}</p>
          </section>
        </div>
      </div>
    </main>
  )
}
