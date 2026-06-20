import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { initI18n } from "./i18n"
import App from "./App.tsx"
import "./index.css"

initI18n().then(() => {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
})
