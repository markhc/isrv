import { createFileRoute } from "@tanstack/react-router"
import AbusePage from "@/pages/abuse-page"

export const Route = createFileRoute("/abuse")({
  component: AbusePage,
})
