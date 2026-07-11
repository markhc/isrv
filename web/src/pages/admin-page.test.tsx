// Integration tests for the admin area: login, file dashboard, detail panel
// and delete flow, exercised through the real router/i18n/react-query stack
// with MSW mocking the admin API.
import { screen, waitFor, within } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { renderApp } from "@/test/render-app"
import { adminApiState, seedFiles } from "@/test/server"

async function openAdmin(path = "/admin") {
  const app = await renderApp(path)
  return app
}

async function loginAs(user: Awaited<ReturnType<typeof renderApp>>["user"], username: string, password: string) {
  await screen.findByRole("heading", { name: "Administrator Sign In" })
  await user.type(screen.getByLabelText("Username"), username)
  await user.type(screen.getByLabelText("Password"), password)
  await user.click(screen.getByRole("button", { name: "Sign In" }))
}

describe("admin login", () => {
  it("shows the login form when unauthenticated", async () => {
    await openAdmin()

    expect(
      await screen.findByRole("heading", { name: "Administrator Sign In" })
    ).toBeInTheDocument()
    expect(screen.getByLabelText("Username")).toBeInTheDocument()
    expect(screen.getByLabelText("Password")).toBeInTheDocument()
    // No dashboard or logout affordances while signed out.
    expect(screen.queryByRole("button", { name: "Log out" })).not.toBeInTheDocument()
  })

  it("shows an error for invalid credentials and stays on the form", async () => {
    const { user } = await openAdmin()

    await loginAs(user, "admin", "wrong-password")

    expect(await screen.findByText("Invalid username or password")).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: "Administrator Sign In" })).toBeInTheDocument()
  })

  it("logs in with valid credentials and shows the files dashboard", async () => {
    const { user } = await openAdmin()

    await loginAs(user, "admin", "secret")

    expect(await screen.findByText("readme.txt")).toBeInTheDocument()
    expect(screen.getByText("photo.jpg")).toBeInTheDocument()
    expect(screen.getByText("Showing 1-3 of 3")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Log out" })).toBeInTheDocument()
  })

  it("logs out back to the login form", async () => {
    adminApiState.authenticated = true
    const { user } = await openAdmin()
    await screen.findByText("readme.txt")

    await user.click(screen.getByRole("button", { name: "Log out" }))

    expect(
      await screen.findByRole("heading", { name: "Administrator Sign In" })
    ).toBeInTheDocument()
  })
})

describe("admin files dashboard", () => {
  it("shows file details and a text preview when a row is selected", async () => {
    adminApiState.authenticated = true
    const { user } = await openAdmin()

    expect(await screen.findByText("Select a file to view its details")).toBeInTheDocument()

    await user.click(await screen.findByText("readme.txt"))

    // Detail panel renders the public URL, uploader IP and file metadata.
    expect(
      await screen.findByRole("link", { name: "http://localhost:3000/d/f1" })
    ).toBeInTheDocument()
    expect(screen.getByText("10.0.0.1")).toBeInTheDocument()
    // "Yes (v1)" renders both in the table row and in the detail panel.
    expect(screen.getAllByText("Yes (v1)")).toHaveLength(2)
    // Text preview is fetched from the download route.
    expect(await screen.findByText("hello from readme")).toBeInTheDocument()
  })

  it("filters files with the search input", async () => {
    adminApiState.authenticated = true
    const { user } = await openAdmin()
    await screen.findByText("readme.txt")

    await user.type(screen.getByPlaceholderText("Search name or type"), "photo")

    await waitFor(() => expect(screen.queryByText("readme.txt")).not.toBeInTheDocument())
    expect(screen.getByText("photo.jpg")).toBeInTheDocument()
    expect(screen.getByText("Showing 1-1 of 1")).toBeInTheDocument()
  })

  it("shows the empty state when nothing matches", async () => {
    adminApiState.authenticated = true
    const { user } = await openAdmin()
    await screen.findByText("readme.txt")

    await user.type(screen.getByPlaceholderText("Search name or type"), "does-not-exist")

    expect(await screen.findByText("No files found")).toBeInTheDocument()
    expect(screen.getByText("Showing 0-0 of 0")).toBeInTheDocument()
  })

  it("paginates through files", async () => {
    adminApiState.authenticated = true
    adminApiState.files = seedFiles(30)
    const { user } = await openAdmin()

    expect(await screen.findByText("Showing 1-25 of 30")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled()

    await user.click(screen.getByRole("button", { name: "Next" }))

    expect(await screen.findByText("Showing 26-30 of 30")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled()
    expect(screen.getByRole("button", { name: "Previous" })).toBeEnabled()
  })

  it("deletes a file through the confirmation dialog", async () => {
    adminApiState.authenticated = true
    const { user } = await openAdmin()

    await user.click(await screen.findByText("readme.txt"))
    await screen.findByRole("link", { name: "http://localhost:3000/d/f1" })

    await user.click(screen.getByRole("button", { name: "Delete" }))

    const dialog = await screen.findByRole("alertdialog")
    expect(dialog).toHaveTextContent("readme.txt")

    await user.click(within(dialog).getByRole("button", { name: "Delete" }))

    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
    expect(await screen.findByText("File deleted")).toBeInTheDocument()
    // The list refetches without the deleted file and the selection clears.
    await waitFor(() => expect(screen.queryByText("readme.txt")).not.toBeInTheDocument())
    expect(screen.getByText("Showing 1-2 of 2")).toBeInTheDocument()
    expect(screen.getByText("Select a file to view its details")).toBeInTheDocument()
  })

  it("keeps the file when the delete dialog is cancelled", async () => {
    adminApiState.authenticated = true
    const { user } = await openAdmin()

    await user.click(await screen.findByText("readme.txt"))
    await screen.findByRole("link", { name: "http://localhost:3000/d/f1" })
    await user.click(screen.getByRole("button", { name: "Delete" }))

    const dialog = await screen.findByRole("alertdialog")
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }))

    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument())
    // Still present in both the table row and the (still selected) detail panel.
    expect(screen.getAllByText("readme.txt").length).toBeGreaterThan(0)
    expect(screen.getByText("Showing 1-3 of 3")).toBeInTheDocument()
  })
})
