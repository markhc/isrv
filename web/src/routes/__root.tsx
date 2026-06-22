import { createRootRouteWithContext, Outlet, useLocation } from "@tanstack/react-router"
import { LanguageSwitcher } from "@/components/language-switcher"

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
        <div className="fixed top-4 right-4 z-50">
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
