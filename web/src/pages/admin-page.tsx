import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { ThemeSwitcher } from "@/components/theme-switcher"
import { LanguageSwitcher } from "@/components/language-switcher"
import { LoginForm } from "@/components/admin/login-form"
import { FilesDashboard } from "@/components/admin/files-dashboard"
import { ensureAdminI18n } from "@/i18n/admin"
import { getSession, logout } from "@/lib/admin-api"

export default function AdminPage() {
  const { t, i18n } = useTranslation("admin")
  const queryClient = useQueryClient()
  // Tracks the language whose admin translations are loaded. It is updated
  // whenever the active language changes so the admin namespace is (re)loaded
  // on demand and dependent UI (e.g. memoized table headers) recomputes.
  const [loadedLanguage, setLoadedLanguage] = useState("")

  useEffect(() => {
    let active = true
    ensureAdminI18n().then(() => {
      if (active) setLoadedLanguage(i18n.language)
    })
    return () => {
      active = false
    }
  }, [i18n.language])

  const session = useQuery({
    queryKey: ["admin", "session"],
    queryFn: getSession,
    retry: false,
  })

  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["admin", "session"] }),
  })

  if (loadedLanguage === "" || session.isLoading) {
    return (
      <main className="min-h-screen flex items-center justify-center">
        <p className="text-sm text-muted-foreground">{t("loading")}</p>
      </main>
    )
  }

  const authenticated = session.data?.authenticated ?? false

  return (
    <main className="flex h-screen w-full flex-col gap-4 p-4">
      <header className="flex items-center justify-between gap-4">
        <h1 className="text-2xl font-bold tracking-tight">
          <Link to="/" className="transition-colors hover:text-muted-foreground">
            {t("title")}
          </Link>
        </h1>
        <div className="flex items-center gap-3">
          {authenticated && session.data?.username && (
            <span className="text-sm text-muted-foreground">{session.data.username}</span>
          )}
          <LanguageSwitcher />
          <ThemeSwitcher />
          {authenticated && (
            <button
              type="button"
              onClick={() => logoutMutation.mutate()}
              className="rounded-md border border-border px-3 py-1.5 text-sm transition-colors hover:bg-muted"
            >
              {t("logout")}
            </button>
          )}
        </div>
      </header>

      {authenticated ? (
        <FilesDashboard localeKey={loadedLanguage} />
      ) : (
        <LoginForm />
      )}
    </main>
  )
}
