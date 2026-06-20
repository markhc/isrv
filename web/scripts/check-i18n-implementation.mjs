#!/usr/bin/env node
// Validates that the i18n implementation is wired up correctly.
import fs from "fs"
import path from "path"
import { fileURLToPath } from "url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const srcDir = path.join(__dirname, "../src")

let failures = 0

function check(condition, message) {
  if (condition) {
    console.log(`OK  ${message}`)
  } else {
    console.error(`FAIL  ${message}`)
    failures++
  }
}

const i18nFile = path.join(srcDir, "i18n/index.ts")
check(fs.existsSync(i18nFile), "src/i18n/index.ts exists")

if (fs.existsSync(i18nFile)) {
  const content = fs.readFileSync(i18nFile, "utf8")
  check(content.includes("export") && content.includes("supportedLanguages"), "supportedLanguages exported")
  check(content.includes("export async function initI18n"), "initI18n exported")
  check(content.includes("export async function changeLanguage"), "changeLanguage exported")
  check(content.includes("languageNames"), "languageNames exported")

  const match = content.match(/supportedLanguages\s*=\s*\[([^\]]+)\]/)
  if (match) {
    const langs = [...match[1].matchAll(/"([^"]+)"/g)].map((m) => m[1])
    const localesDir = path.join(srcDir, "i18n/locales")
    for (const lang of langs) {
      check(fs.existsSync(path.join(localesDir, lang)), `locale directory exists: ${lang}`)
    }
  }
}

const mainFile = path.join(srcDir, "main.tsx")
if (fs.existsSync(mainFile)) {
  const content = fs.readFileSync(mainFile, "utf8")
  check(content.includes("initI18n"), "main.tsx calls initI18n")
}

if (failures > 0) {
  console.error(`\ncheck:i18n-implementation FAILED (${failures} failure(s))`)
  process.exit(1)
} else {
  console.log("\ncheck:i18n-implementation PASSED")
}
