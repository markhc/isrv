# Visual regression tests (Playwright)

Screenshot-based tests that fail when a page's rendered appearance changes
unexpectedly. Backend APIs are mocked in-browser with `page.route`, so no Go
backend is needed; the Vite dev server is started automatically.

> Note: Requires Chromium headless libraries to be installed

## Running

```bash
pnpm test:e2e          # compare against committed baselines
pnpm test:e2e:update   # re-record baselines after an intentional UI change
```

First-time setup: `pnpm exec playwright install chromium`.

Baselines live in `e2e/*-snapshots/` and are committed. They are rendered on
Linux/Chromium; the `-chromium-linux` suffix keeps them platform-scoped. When a
test fails, the actual/expected/diff images are written under `test-results/`.

## Chromium system libraries without root

`playwright install chromium` downloads the browser but not its system
libraries (normally installed with `sudo playwright install-deps`). On hosts
without root (e.g. WSL2 dev machines), extract the missing libs locally:

```bash
mkdir -p /tmp/pw-debs && cd /tmp/pw-debs
apt-get download libnspr4 libnss3 libasound2t64
for d in *.deb; do dpkg -x "$d" extracted; done
mkdir -p ~/.cache/isrv-playwright-libs
cp -a extracted/usr/lib/x86_64-linux-gnu/* ~/.cache/isrv-playwright-libs/
```

`playwright.config.ts` automatically adds `~/.cache/isrv-playwright-libs` to
`LD_LIBRARY_PATH` when the directory exists.
