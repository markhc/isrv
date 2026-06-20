import * as Select from "@radix-ui/react-select"
import { ChevronDownIcon, CheckIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { changeLanguage, languageNames, supportedLanguages, type SupportedLanguage } from "@/i18n"

export function LanguageSwitcher() {
  const { i18n } = useTranslation()
  const currentLang = i18n.language as SupportedLanguage

  return (
    <Select.Root value={currentLang} onValueChange={(v) => changeLanguage(v as SupportedLanguage)}>
      <Select.Trigger className="flex items-center gap-1.5 rounded-md border border-border bg-background px-3 py-1.5 text-sm text-foreground transition-colors hover:bg-muted focus:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <Select.Value />
        <Select.Icon asChild>
          <ChevronDownIcon className="size-3.5 text-muted-foreground" />
        </Select.Icon>
      </Select.Trigger>

      <Select.Portal>
        <Select.Content
          position="popper"
          sideOffset={4}
          className="z-50 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-md"
        >
          <Select.Viewport className="p-1">
            {supportedLanguages.map((lang) => (
              <Select.Item
                key={lang}
                value={lang}
                className="relative flex cursor-pointer select-none items-center rounded-sm px-3 py-1.5 text-sm outline-none transition-colors hover:bg-accent hover:text-accent-foreground data-[state=checked]:font-medium"
              >
                <Select.ItemText>{languageNames[lang]}</Select.ItemText>
                <Select.ItemIndicator className="absolute right-2">
                  <CheckIcon className="size-3.5" />
                </Select.ItemIndicator>
              </Select.Item>
            ))}
          </Select.Viewport>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  )
}
