import i18n from "./index"

// adminLocales is statically analysable by the bundler, so each admin.json is
// emitted as its own chunk and only fetched on demand from the admin route.
// This keeps admin translations out of the home-page bundle.
const adminLocales = import.meta.glob("./locales/*/admin.json")

async function loadBundle(lang: string): Promise<void> {
  if (i18n.hasResourceBundle(lang, "admin")) return

  const factory = adminLocales[`./locales/${lang}/admin.json`]
  if (!factory) return

  const mod = (await factory()) as { default: Record<string, unknown> }
  i18n.addResourceBundle(lang, "admin", mod.default, true, true)
}

// ensureAdminI18n loads the admin namespace for the active language and the
// English fallback. It is called from the admin route before rendering.
export async function ensureAdminI18n(): Promise<void> {
  await Promise.all([loadBundle("en"), loadBundle(i18n.language)])
}
