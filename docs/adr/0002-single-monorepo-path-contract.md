# 2. Single monorepo with a path/workspace contract

- Status: proposed (placeholder — expand from architecture#Repository-Strategy)
- Date: 2026-07-01

## Context
Four coupled surfaces (contracts, api, mobile, web) must share one atomic base;
the contract must stay in lockstep with its consumers.

## Decision
One monorepo. The contract is a **path/workspace reference, not a published
package** — Go via `go.work`, TS via pnpm `workspace:*`. The drift-gate is a
single atomic CI job. No polyrepo. (AR1)

## Consequences
Atomic cross-surface changes; one drift-gate as SSOT enforcer. Placeholder ADR —
to be expanded later.
