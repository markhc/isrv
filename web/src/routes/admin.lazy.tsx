import { createLazyFileRoute } from "@tanstack/react-router"
import AdminPage from "@/pages/admin-page"

export const Route = createLazyFileRoute("/admin")({
  component: AdminPage,
})
