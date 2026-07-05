import { createRootRouteWithContext, Outlet, useLocation } from "@tanstack/react-router"
import { GitHubLogoIcon } from "@radix-ui/react-icons"
import { LanguageSwitcher } from "@/components/language-switcher"
import { REPOSITORY_URL } from "@/lib/constants"

interface RouterContext {
  auth: undefined
}

function RootLayout() {
  // The admin page renders its own language switcher inline in the header, so
  // the global floating one is omitted there to avoid a duplicate/overlap.
  const isAdmin = useLocation({ select: (location) => location.pathname.startsWith("/admin") })

  return (
    <>
      {!isAdmin && (
        <div className="fixed top-4 right-4 z-50 flex items-center gap-3">
          <a
            href={REPOSITORY_URL}
            target="_blank"
            rel="noopener noreferrer"
            aria-label="GitHub repository"
            className="text-foreground transition-colors hover:text-muted-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-md"
          >
            <GitHubLogoIcon className="size-6" />
          </a>
          <LanguageSwitcher />
        </div>
      )}
      <Outlet />
    </>
  )
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
})
