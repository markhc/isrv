#!/usr/bin/env node
// Scans TSX/TS source files for likely hardcoded user-visible strings.
// Flags JSX text content and translatable attributes that are not routed through t().
import fs from "fs"
import path from "path"
import { fileURLToPath } from "url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const srcDir = path.join(__dirname, "../src")

// Files and directories to skip entirely.
const SKIP_PATTERNS = [
  /node_modules/,
  /\.gen\.ts$/,
  /routeTree/,
  /i18n\/locales/,
  /i18n\/index\.ts/,
  /\.css$/,
  /vite-env\.d\.ts$/,
]

// Translatable HTML/JSX attributes that should use t().
const TRANSLATABLE_ATTRS = ["title", "placeholder", "aria-label", "aria-description", "alt"]

// Minimum word count to flag a string as a suspected hardcoded literal.
const MIN_WORDS = 2

function collectFiles(dir) {
  const files = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (SKIP_PATTERNS.some((p) => p.test(full))) continue
    if (entry.isDirectory()) {
      files.push(...collectFiles(full))
    } else if (/\.(tsx|ts)$/.test(entry.name)) {
      files.push(full)
    }
  }
  return files
}

function isSuspiciousString(str) {
  const trimmed = str.trim()
  if (!trimmed) return false
  if (!/[a-zA-Z]/.test(trimmed)) return false
  // Allow short single tokens (e.g., className values, event names)
  const words = trimmed.split(/\s+/).filter(Boolean)
  if (words.length < MIN_WORDS) return false
  // Ignore strings that look like code, paths, CSS classes, or placeholders
  if (/^[a-z-]+$/.test(trimmed)) return false // kebab-case
  if (/^\$\{/.test(trimmed)) return false // template literal start
  if (trimmed.startsWith("curl ") || trimmed.startsWith("#")) return false // shell commands/comments
  return true
}

const files = collectFiles(srcDir)
let findings = 0

for (const file of files) {
  const rel = path.relative(path.join(__dirname, ".."), file)
  const content = fs.readFileSync(file, "utf8")
  const lines = content.split("\n")

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const lineNum = i + 1

    // Skip lines that already use t() or are comments
    if (line.includes("t(") || line.trimStart().startsWith("//") || line.trimStart().startsWith("*")) {
      continue
    }

    // Check JSX text content: text between > and < that is not in {}
    const jsxTextMatches = line.matchAll(/>([^<{}\n]+)</g)
    for (const m of jsxTextMatches) {
      if (isSuspiciousString(m[1])) {
        console.warn(`WARN  ${rel}:${lineNum}  JSX text: "${m[1].trim()}"`)
        findings++
      }
    }

    // Check translatable attributes
    for (const attr of TRANSLATABLE_ATTRS) {
      const attrRegex = new RegExp(`${attr}="([^"]+)"`, "g")
      for (const m of line.matchAll(attrRegex)) {
        if (isSuspiciousString(m[1])) {
          console.warn(`WARN  ${rel}:${lineNum}  ${attr}="${m[1]}"`)
          findings++
        }
      }
    }
  }
}

if (findings > 0) {
  console.error(`\nfind-hardcoded-literals: ${findings} potential hardcoded string(s) found`)
  process.exit(1)
} else {
  console.log("find-hardcoded-literals: no hardcoded strings detected")
}
