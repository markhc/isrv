import i18n from "i18next"
import { initReactI18next } from "react-i18next"

import commonEn from "./locales/en/common.json"
import homeEn from "./locales/en/home.json"
import aboutEn from "./locales/en/about.json"
import faqEn from "./locales/en/faq.json"
import abuseEn from "./locales/en/abuse.json"
import privacyEn from "./locales/en/privacy.json"

export const supportedLanguages = ["en", "pt-BR"] as const
export type SupportedLanguage = (typeof supportedLanguages)[number]

export const languageNames: Record<SupportedLanguage, string> = {
  en: "EN",
  "pt-BR": "PT",
}

const namespaces = ["common", "home", "about", "faq", "abuse", "privacy"] as const

const lazyLocales = import.meta.glob("./locales/!(en)/**/*.json")

async function loadLanguage(lang: SupportedLanguage): Promise<void> {
  if (lang === "en") return

  await Promise.all(
    namespaces.map(async (ns) => {
      const key = `./locales/${lang}/${ns}.json`
      const factory = lazyLocales[key]
      if (!factory) return
      const mod = (await factory()) as { default: Record<string, unknown> }
      i18n.addResourceBundle(lang, ns, mod.default, true, true)
    }),
  )
}

function getStoredLanguage(): SupportedLanguage {
  const stored = localStorage.getItem("i18n-lang")
  if (stored && supportedLanguages.includes(stored as SupportedLanguage)) {
    return stored as SupportedLanguage
  }
  const browserLang = navigator.language
  if (supportedLanguages.includes(browserLang as SupportedLanguage)) {
    return browserLang as SupportedLanguage
  }
  const base = browserLang.split("-")[0]
  const match = supportedLanguages.find((l) => l.split("-")[0] === base)
  return match ?? "en"
}

export async function initI18n(): Promise<void> {
  await i18n.use(initReactI18next).init({
    lng: "en",
    fallbackLng: "en",
    // "admin" is declared but not in `namespaces`: it is loaded on demand by the
    // admin route (see i18n/admin.ts) so its strings stay out of the home bundle.
    ns: [...namespaces, "admin"],
    defaultNS: "common",
    resources: {
      en: {
        common: commonEn,
        home: homeEn,
        about: aboutEn,
        faq: faqEn,
        abuse: abuseEn,
        privacy: privacyEn,
      },
    },
    interpolation: { escapeValue: false },
  })

  const lang = getStoredLanguage()
  if (lang !== "en") {
    await loadLanguage(lang)
    await i18n.changeLanguage(lang)
  }
}

export async function changeLanguage(lang: SupportedLanguage): Promise<void> {
  await loadLanguage(lang)
  await i18n.changeLanguage(lang)
  localStorage.setItem("i18n-lang", lang)
}

export default i18n
