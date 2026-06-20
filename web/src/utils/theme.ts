export type ThemeMode = "light" | "dark" | "auto"

const MODE_KEY = "isrv-theme-mode"

function getStoredMode(): ThemeMode {
  const v = localStorage.getItem(MODE_KEY)
  if (v === "light" || v === "dark" || v === "auto") return v
  return "auto"
}

function setStoredMode(mode: ThemeMode): void {
  localStorage.setItem(MODE_KEY, mode)
}

function applyMode(mode: ThemeMode): void {
  const isDark =
    mode === "dark" ||
    (mode === "auto" && window.matchMedia("(prefers-color-scheme: dark)").matches)

  if (isDark) {
    document.documentElement.classList.add("dark")
  } else {
    document.documentElement.classList.remove("dark")
  }
}

export function getCurrentMode(): ThemeMode {
  return getStoredMode()
}

export function initializeTheme(): () => void {
  applyMode(getStoredMode())

  const mq = window.matchMedia("(prefers-color-scheme: dark)")
  const listener = () => {
    if (getStoredMode() === "auto") {
      applyMode("auto")
    }
  }
  mq.addEventListener("change", listener)
  return () => mq.removeEventListener("change", listener)
}

export function setMode(mode: ThemeMode): void {
  setStoredMode(mode)
  applyMode(mode)
}
