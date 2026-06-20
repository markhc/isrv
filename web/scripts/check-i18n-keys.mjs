#!/usr/bin/env node
// Validates that all locale files have the same key structure as English.
import fs from "fs"
import path from "path"
import { fileURLToPath } from "url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const localesDir = path.join(__dirname, "../src/i18n/locales")

function compareStructure(enVal, langVal, location) {
  const errors = []

  if (Array.isArray(enVal)) {
    if (!Array.isArray(langVal)) {
      errors.push(`${location}: expected array, got ${typeof langVal}`)
      return errors
    }
    if (enVal.length !== langVal.length) {
      errors.push(`${location}: array length mismatch (en: ${enVal.length}, lang: ${langVal.length})`)
    }
    const len = Math.min(enVal.length, langVal.length)
    for (let i = 0; i < len; i++) {
      errors.push(...compareStructure(enVal[i], langVal[i], `${location}[${i}]`))
    }
  } else if (typeof enVal === "object" && enVal !== null) {
    if (typeof langVal !== "object" || langVal === null || Array.isArray(langVal)) {
      errors.push(`${location}: expected object`)
      return errors
    }
    for (const key of Object.keys(enVal)) {
      if (!(key in langVal)) {
        errors.push(`${location}.${key}: missing key`)
      } else {
        errors.push(...compareStructure(enVal[key], langVal[key], `${location}.${key}`))
      }
    }
  } else if (typeof enVal === "string") {
    if (typeof langVal !== "string") {
      errors.push(`${location}: expected string, got ${typeof langVal}`)
    } else if (langVal.trim() === "") {
      errors.push(`${location}: empty string`)
    }
  }

  return errors
}

const langs = fs
  .readdirSync(localesDir)
  .filter((d) => fs.statSync(path.join(localesDir, d)).isDirectory())

const enDir = path.join(localesDir, "en")
const namespaces = fs
  .readdirSync(enDir)
  .filter((f) => f.endsWith(".json"))
  .map((f) => f.replace(".json", ""))

let totalErrors = 0

for (const lang of langs) {
  if (lang === "en") continue

  for (const ns of namespaces) {
    const enFile = path.join(localesDir, "en", `${ns}.json`)
    const langFile = path.join(localesDir, lang, `${ns}.json`)

    if (!fs.existsSync(langFile)) {
      console.error(`MISSING  ${lang}/${ns}.json`)
      totalErrors++
      continue
    }

    let enData, langData
    try {
      enData = JSON.parse(fs.readFileSync(enFile, "utf8"))
      langData = JSON.parse(fs.readFileSync(langFile, "utf8"))
    } catch (err) {
      console.error(`INVALID JSON  ${lang}/${ns}.json: ${err.message}`)
      totalErrors++
      continue
    }

    const errors = compareStructure(enData, langData, `${lang}/${ns}`)
    for (const err of errors) {
      console.error(`ERROR  ${err}`)
      totalErrors++
    }

    if (errors.length === 0) {
      console.log(`OK  ${lang}/${ns}.json`)
    }
  }
}

if (totalErrors > 0) {
  console.error(`\ncheck:i18n-keys FAILED (${totalErrors} error(s))`)
  process.exit(1)
} else {
  console.log("\ncheck:i18n-keys PASSED")
}
