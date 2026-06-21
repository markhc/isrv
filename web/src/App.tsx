import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { useEffect } from "react"
import { initializeTheme } from "./utils/theme"
import { TooltipProvider } from "@radix-ui/react-tooltip"
import { Toaster } from "@/components/ui/sonner"
import { RouterProvider } from "@tanstack/react-router"
import { router } from "./router"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 1000,
      refetchOnWindowFocus: false,
    },
  },
})

function App() {
  useEffect(() => {
    return initializeTheme()
  }, [])

  return (
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>
        <RouterProvider router={router} />
        <Toaster />
      </TooltipProvider>
    </QueryClientProvider>
  )
}

export default App
