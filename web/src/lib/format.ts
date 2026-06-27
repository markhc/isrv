// formatBytes renders a byte count as a human-readable string.
export function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B"
  const units = ["B", "KB", "MB", "GB", "TB"]
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / Math.pow(1024, exponent)
  return `${value.toFixed(exponent === 0 ? 0 : 1)} ${units[exponent]}`
}

// formatDate renders an ISO timestamp using the given locale, or "-" when empty.
export function formatDate(iso: string | undefined, locale: string): string {
  if (!iso) return "-"
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return "-"
  return date.toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" })
}
