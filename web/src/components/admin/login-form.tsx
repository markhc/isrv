import { useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { login } from "@/lib/admin-api"

export function LoginForm() {
  const { t } = useTranslation("admin")
  const queryClient = useQueryClient()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")

  const mutation = useMutation({
    mutationFn: () => login(username, password),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["admin", "session"] })
    },
  })

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    mutation.mutate()
  }

  return (
    <div className="flex flex-1 items-center justify-center">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm flex flex-col gap-4 rounded-lg border border-border bg-card p-6"
      >
        <h2 className="text-lg font-semibold">{t("login.title")}</h2>

        <label className="flex flex-col gap-1.5 text-sm">
          <span className="text-muted-foreground">{t("login.username")}</span>
          <input
            type="text"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            required
            className="rounded-md border border-input bg-background px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </label>

        <label className="flex flex-col gap-1.5 text-sm">
          <span className="text-muted-foreground">{t("login.password")}</span>
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            className="rounded-md border border-input bg-background px-3 py-2 text-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </label>

        {mutation.isError && (
          <p className="text-sm text-destructive">{t("login.invalidCredentials")}</p>
        )}

        <button
          type="submit"
          disabled={mutation.isPending}
          className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-60"
        >
          {mutation.isPending ? t("login.submitting") : t("login.submit")}
        </button>
      </form>
    </div>
  )
}
