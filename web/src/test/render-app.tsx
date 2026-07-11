// renderApp mounts the real application tree (router, query client, i18n,
// toaster) at a given route so tests can drive full user flows.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { render } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "@/components/ui/sonner"
import i18n, { initI18n } from "@/i18n"
import { routeTree } from "@/routeTree.gen"

export async function renderApp(initialPath: string = "/") {
  if (!i18n.isInitialized) {
    await initI18n()
  }

  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [initialPath] }),
    context: {
      auth: undefined!,
    },
  })

  const user = userEvent.setup()

  const view = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster />
    </QueryClientProvider>
  )

  return { ...view, router, queryClient, user }
}
