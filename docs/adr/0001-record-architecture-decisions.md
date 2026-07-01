# 1. Record architecture decisions

- Status: accepted
- Date: 2026-07-01

## Context

We need a lightweight, durable record of the significant architecture decisions
on OD-Amrita (AR18). Decisions made verbally or only in chat get lost.

## Decision

We use Architecture Decision Records (ADRs), one Markdown file per decision under
`docs/adr/NNNN-title.md`, in the style of Michael Nygard's ADRs.

## Consequences

Decisions are versioned with the code and reviewable in PRs. This ADR bootstraps
the log; subsequent ADRs (monorepo strategy, populator-as-mode, no-ORM, etc.) are
filled in as they are formalized.
