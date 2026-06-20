#!/usr/bin/env node
// Checks pt-BR locale coverage against English.
// Validates: key presence, array structure, empty strings, JSON validity.
import fs from "fs"
import path from "path"
import { fileURLToPath } from "url"

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const localesDir = path.join(__dirname, "../src/i18n/locales")
const lang = "pt-BR"

function extractPlaceholders(str) {
  return new Set(str.match(/\{\{[^}]+\}\}/g) ?? [])
}

function compareStructure(enVal, langVal, location, errors) {
  if (Array.isArray(enVal)) {
    if (!Array.isArray(langVal)) {
      errors.push(`${location}: expected array`)
      return
    }
    if (enVal.length !== langVal.length) {
      errors.push(`${location}: array length mismatch (en: ${enVal.length}, ${lang}: ${langVal.length})`)
    }
    const len = Math.min(enVal.length, langVal.length)
    for (let i = 0; i < len; i++) {
      compareStructure(enVal[i], langVal[i], `${location}[${i}]`, errors)
    }
  } else if (typeof enVal === "object" && enVal !== null) {
    if (typeof langVal !== "object" || langVal === null || Array.isArray(langVal)) {
      errors.push(`${location}: expected object`)
      return
    }
    for (const key of Object.keys(enVal)) {
      if (!(key in langVal)) {
        errors.push(`${location}.${key}: missing key`)
      } else {
        compareStructure(enVal[key], langVal[key], `${location}.${key}`, errors)
      }
    }
    for (const key of Object.keys(langVal)) {
      if (!(key in enVal)) {
        errors.push(`${location}.${key}: extra key not in English`)
      }
    }
  } else if (typeof enVal === "string") {
    if (typeof langVal !== "string") {
      errors.push(`${location}: expected string`)
    } else if (langVal.trim() === "") {
      errors.push(`${location}: empty string`)
    } else {
      const enPlaceholders = extractPlaceholders(enVal)
      const langPlaceholders = extractPlaceholders(langVal)
      const missing = [...enPlaceholders].filter((p) => !langPlaceholders.has(p))
      const extra = [...langPlaceholders].filter((p) => !enPlaceholders.has(p))
      if (missing.length > 0) {
        errors.push(`${location}: missing interpolation placeholder(s) ${missing.join(", ")}`)
      }
      if (extra.length > 0) {
        errors.push(`${location}: unexpected interpolation placeholder(s) ${extra.join(", ")}`)
      }
    }
  }
}

const enDir = path.join(localesDir, "en")
const langDir = path.join(localesDir, lang)

if (!fs.existsSync(langDir)) {
  console.error(`FAIL  ${lang} locale directory not found: ${langDir}`)
  process.exit(1)
}

const namespaces = fs
  .readdirSync(enDir)
  .filter((f) => f.endsWith(".json"))
  .map((f) => f.replace(".json", ""))

let totalErrors = 0

for (const ns of namespaces) {
  const enFile = path.join(enDir, `${ns}.json`)
  const langFile = path.join(langDir, `${ns}.json`)

  if (!fs.existsSync(langFile)) {
    console.error(`MISSING  ${lang}/${ns}.json`)
    totalErrors++
    continue
  }

  let enData, langData
  try {
    enData = JSON.parse(fs.readFileSync(enFile, "utf8"))
  } catch (err) {
    console.error(`INVALID JSON  en/${ns}.json: ${err.message}`)
    totalErrors++
    continue
  }
  try {
    langData = JSON.parse(fs.readFileSync(langFile, "utf8"))
  } catch (err) {
    console.error(`INVALID JSON  ${lang}/${ns}.json: ${err.message}`)
    totalErrors++
    continue
  }

  const errors = []
  compareStructure(enData, langData, `${lang}/${ns}`, errors)
  for (const err of errors) {
    console.error(`ERROR  ${err}`)
    totalErrors++
  }

  if (errors.length === 0) {
    console.log(`OK  ${lang}/${ns}.json`)
  }
}

if (totalErrors > 0) {
  console.error(`\ncheck:${lang}-coverage FAILED (${totalErrors} error(s))`)
  process.exit(1)
} else {
  console.log(`\ncheck:${lang}-coverage PASSED`)
}
