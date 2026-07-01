# OD-Amrita — Web (`od-amrita-web`)

React + Vite SPA surface of the OD-Amrita monorepo. Two route trees per the
architecture (AR15): `src/routes/portal` (authenticated admin/teacher/coordinator
portal) and `src/routes/public` (public dashboard).

Consumes the generated contract as a workspace package:
`"@od-amrita/contracts": "workspace:*"` (path/workspace reference, not published).

## Scripts

- `pnpm --filter od-amrita-web dev` — Vite dev server
- `pnpm --filter od-amrita-web build` — `tsc -b && vite build`
- `pnpm --filter od-amrita-web typecheck` — `tsc --noEmit`

This is the skeleton scaffold; real routes, components, and the deploy step land
in later stories.
