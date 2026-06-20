import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

export default function AbusePage() {
  const { t } = useTranslation(["abuse", "common"])
  const prohibitedItems = t("abuse:sections.prohibited.items", { returnObjects: true }) as string[]

  return (
    <main className="min-h-screen flex flex-col items-center p-8 pt-16">
      <div className="w-full max-w-2xl flex flex-col gap-10">
        <div className="flex flex-col gap-2">
          <Link to="/" className="text-sm text-muted-foreground hover:text-foreground transition-colors w-fit">
            {t("common:back")}
          </Link>
          <h1 className="text-3xl font-bold tracking-tight">{t("abuse:title")}</h1>
          <p className="text-muted-foreground">{t("abuse:subtitle")}</p>
        </div>

        <div className="flex flex-col gap-8 text-sm text-muted-foreground leading-relaxed">
          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("abuse:sections.prohibited.title")}</h2>
            <p>{t("abuse:sections.prohibited.intro")}</p>
            <ul className="list-disc list-inside space-y-1 mt-1">
              {prohibitedItems.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("abuse:sections.enforcement.title")}</h2>
            <p>{t("abuse:sections.enforcement.body")}</p>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("abuse:sections.reporting.title")}</h2>
            <p>{t("abuse:sections.reporting.body")}</p>
          </section>
        </div>
      </div>
    </main>
  )
}
