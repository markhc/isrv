import { createRootRouteWithContext } from "@tanstack/react-router"
import { RootLayout } from "@/components/root-layout"

interface RouterContext {
  auth: undefined
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
})
