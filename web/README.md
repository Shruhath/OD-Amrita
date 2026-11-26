# OD-Amrita — Web (`od-amrita-web`)

The browser app for OD-Amrita, on-duty attendance for Amrita Vishwa Vidyapeetham.
A React 19 + Vite single-page app, installable as a PWA, deployed to Vercel.

- **Public** — `/attendance`: students look up their OD records by roll number
  and report a missed slot. No sign-in.
- **Marking** — `/mark`: coordinators mark attendance at an event by NFC card
  tap (Web NFC, Android Chrome), camera barcode scan, or search.
- **Portal** — `/portal/*`: administrators and teachers manage events,
  categories, coordinators, teachers, the student roster, OD records and
  disputes, watch the live board, write NFC cards, and export attendance.

Sign-in is Microsoft, brokered by Supabase Auth; the browser sends the Supabase
access token to the Go API, which runs on its own origin and enforces roles. The
typed API client comes from the spec-first contract, consumed as the workspace
package `@od-amrita/contracts`.

## Scripts

Run from the repository root with `pnpm --filter od-amrita-web <script>`:

| Script | What it does |
| --- | --- |
| `dev` | Vite dev server |
| `build` | `tsc -b && vite build` into `dist/` |
| `preview` | Serve the production build locally |
| `typecheck` | `tsc -b --force` |
| `lint` | oxlint |
| `test` | Vitest component and unit tests (`src/**/*.test.ts(x)`, jsdom + MSW) |
| `test:e2e` | Playwright browser journeys against the dev server with a mocked API (`tests/e2e`) |
| `test:api` | API journeys against a running test API on `127.0.0.1:8081` (`tests/api`) |
| `test:e2e:integration` | Browser journeys against the real API and Postgres (`tests/integration`) |

`test:e2e` starts its own dev server on port 4173. The two real-stack suites
need the deterministic API from `api/test/portal` running on port 8081 against a
migrated database; `.github/workflows/web-deploy.yml` shows the full setup.

## Environment

Copy `.env.example` to `.env.local` for local development. Every `VITE_*` value
is inlined into the public bundle, so none of them may be a secret.

| Variable | Purpose |
| --- | --- |
| `VITE_SUPABASE_URL` | Supabase project URL (required) |
| `VITE_SUPABASE_ANON_KEY` | Supabase publishable / anon key (required) |
| `VITE_API_BASE_URL` | Origin of the Go API. Falls back to the page's own origin |
| `VITE_SOURCE_URL` | Target of the footer's "Source code" link (AGPL source offer). Defaults to `https://github.com/Shruhath/OD-Amrita` |

`.env.production` holds the production API origin. The Supabase values are set
as Vercel project environment variables.

## Deployment and security headers

`vercel.json` serves the SPA with a fallback rewrite to `index.html` and sends
the security headers, including an enforcing Content-Security-Policy. The inline
theme script in `index.html` is allowed by its SHA-256 hash, so **editing that
script means updating the hash in `vercel.json`**. `src/securityHeaders.test.ts`
fails with the new hash if they disagree. A new API or auth origin must also be
added to `connect-src`.
