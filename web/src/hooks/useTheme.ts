import { useCallback, useSyncExternalStore } from "react"
import { getCurrentMode, setMode, type ThemeMode } from "@/utils/theme"

function subscribe(cb: () => void): () => void {
  window.addEventListener("storage", cb)
  return () => window.removeEventListener("storage", cb)
}

export function useTheme() {
  const mode = useSyncExternalStore(subscribe, getCurrentMode, getCurrentMode)

  const changeMode = useCallback((m: ThemeMode) => {
    setMode(m)
    window.dispatchEvent(new Event("storage"))
  }, [])

  return {
    mode: mode as ThemeMode,
    setMode: changeMode,
  }
}
