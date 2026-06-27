import { createFileRoute } from "@tanstack/react-router"

// Route definition only. The component lives in admin.lazy.tsx so the admin
// panel code is emitted as a separate chunk and never bundled with the home
// page.
export const Route = createFileRoute("/admin")({})
