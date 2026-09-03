# Administrator Live Marking Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable administrators to use the live marking interface (`/mark`) without crashing on page load (`GET /api/v1/mark/status`) or card tap (`POST /api/v1/mark`), while mapping database constraint violations cleanly and adding structured logging for unhandled errors.

**Architecture:** 
1. In `StatusService.MarkStatus`, conditionally bypass the coordinator credit query when `actor.Role == "admin"`, returning an unconstrained fallback credit state to prevent `pgx.ErrNoRows` from bubbling up as an HTTP 500.
2. In `SQLCWriter.mapPgError`, map PostgreSQL check violation `od_records_manual_reason_note_ck` to a structured HTTP 400 validation error rather than letting it escape as an unhandled 500.
3. In `errormw.Handler()`, log unhandled errors to standard error/logger with HTTP method, request path, and root error before returning HTTP 500.

**Tech Stack:** Go 1.24, Gin Web Framework, Jackc PGX v5, PostgreSQL.

**Spec:** [`docs/admin-live-marking-investigation-report.md`](../admin-live-marking-investigation-report.md)

## Global Constraints

- Never execute database DDL or migrations without explicit user approval.
- Preserve zero-allocation / defensive validation invariants across all marking service boundaries.
- Adhere strictly to the domain error contract: non-domain errors must not leak internal database error messages to HTTP clients.
- Every task must end in an independently runnable test cycle and commit.

---

### Task 1: Exempt Administrator Role from Coordinator Credit Queries in `status.go`
- [x] **Step 1: Write the failing unit test**
- [x] **Step 2: Run test to verify it fails**
- [x] **Step 3: Write minimal implementation in `status.go`**
- [x] **Step 4: Run test to verify it passes**
- [x] **Step 5: Commit** (`72a47e8`)

### Task 2: Map `od_records_manual_reason_note_ck` to Validation Error in `writer.go`
- [x] **Step 1: Write the failing unit test**
- [x] **Step 2: Run test to verify it fails**
- [x] **Step 3: Write minimal implementation in `writer.go`**
- [x] **Step 4: Run test to verify it passes**
- [x] **Step 5: Commit** (`904c037`)

### Task 3: Add Structured Logging for Unhandled 500 Errors in `errormw`
- [x] **Step 1: Write the unit test**
- [x] **Step 2: Run test to verify it fails**
- [x] **Step 3: Implement logging in `error.go`**
- [x] **Step 4: Run test to verify it passes**
- [x] **Step 5: Commit** (`f525a0d`)

### Task 4: Full Suite Verification & Regression Testing
- [x] **Step 1: Run marking and status unit tests**
- [x] **Step 2: Run router and error middleware tests**
- [x] **Step 3: Verify git working tree status**
