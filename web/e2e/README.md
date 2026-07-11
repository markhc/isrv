# Visual regression tests (Playwright)

Screenshot-based tests that fail when a page's rendered appearance changes
unexpectedly. Backend APIs are mocked in-browser with `page.route`, so no Go
backend is needed; the Vite dev server is started automatically.

## Running

```bash
pnpm test:e2e          # compare against committed baselines
pnpm test:e2e:update   # re-record baselines after an intentional UI change
```

First-time setup: 

```
sudo playwright install-deps
pnpm exec playwright install chromium
```

Baselines live in `e2e/*-snapshots/` and are committed. They are rendered on
Linux/Chromium; the `-chromium-linux` suffix keeps them platform-scoped. When a
test fails, the actual/expected/diff images are written under `test-results/`.
