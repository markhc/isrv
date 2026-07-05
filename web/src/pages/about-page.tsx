import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { REPOSITORY_URL } from "@/lib/constants"

export default function AboutPage() {
  const { t } = useTranslation(["about", "common"])

  return (
    <main className="min-h-screen flex flex-col items-center p-8 pt-16">
      <div className="w-full max-w-4xl flex flex-col gap-10">
        <div className="flex flex-col gap-2">
          <Link to="/" className="text-sm text-muted-foreground hover:text-foreground transition-colors w-fit">
            {t("common:back")}
          </Link>
          <h1 className="text-3xl font-bold tracking-tight">{t("about:title")}</h1>
          <p className="text-muted-foreground">{t("about:subtitle")}</p>
        </div>

        <div className="flex flex-col gap-8 text-sm text-muted-foreground leading-relaxed">
          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("about:sections.whatIs.title")}</h2>
            <p>{t("about:sections.whatIs.body")}</p>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("about:sections.howItWorks.title")}</h2>
            <p>{t("about:sections.howItWorks.body")}</p>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("about:sections.techStack.title")}</h2>
            <ul className="list-disc list-inside space-y-1">
              <li>{t("about:sections.techStack.backend")}</li>
              <li>{t("about:sections.techStack.frontend")}</li>
              <li>{t("about:sections.techStack.storage")}</li>
              <li>{t("about:sections.techStack.database")}</li>
            </ul>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("about:sections.selfHosting.title")}</h2>
            <p>{t("about:sections.selfHosting.body")}</p>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="text-base font-semibold text-foreground">{t("about:sections.sourceCode.title")}</h2>
            <p>{t("about:sections.sourceCode.body")}</p>
            <a
              href={REPOSITORY_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="text-foreground underline underline-offset-4 hover:text-muted-foreground transition-colors w-fit"
            >
              {t("about:sections.sourceCode.link")}
            </a>
          </section>
        </div>
      </div>
    </main>
  )
}
