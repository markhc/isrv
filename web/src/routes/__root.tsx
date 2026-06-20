import { createRootRouteWithContext, Outlet } from "@tanstack/react-router"
import { LanguageSwitcher } from "@/components/language-switcher"

interface RouterContext {
  auth: undefined
}

function RootLayout() {
  return (
    <>
      <div className="fixed top-4 right-4 z-50">
        <LanguageSwitcher />
      </div>
      <Outlet />
    </>
  )
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
})
