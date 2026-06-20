import { useTranslation } from "react-i18next"
import { useTheme } from "@/hooks/useTheme"
import type { ThemeMode } from "@/utils/theme"

export function ThemeSwitcher() {
  const { t } = useTranslation("common")
  const { mode, setMode } = useTheme()

  const modes: { value: ThemeMode; label: string }[] = [
    { value: "light", label: t("theme.light") },
    { value: "dark", label: t("theme.dark") },
    { value: "auto", label: t("theme.system") },
  ]

  return (
    <div className="flex rounded-md border border-border overflow-hidden text-sm">
      {modes.map((m) => (
        <button
          key={m.value}
          type="button"
          onClick={() => setMode(m.value)}
          className={[
            "px-3 py-1.5 transition-colors",
            mode === m.value
              ? "bg-primary text-primary-foreground"
              : "bg-background text-foreground hover:bg-muted",
          ].join(" ")}
        >
          {m.label}
        </button>
      ))}
    </div>
  )
}
