# Plan: Fix Stale Worker References and Complete Web Offline Delivery

## Scope

Fix BUG-01 and the web-backed portion of BUG-02 only. Do not modify `mobile/`.

## BUG-01 — reconcile the embedded worker topology

1. Remove the deleted `cmd/worker` build target from `api/Dockerfile`.
2. Remove the obsolete `run-worker` Make target and update stale entrypoint comments.
3. Keep one API image entrypoint, since River startup/shutdown already belongs to `cmd/api`.
4. Verify the API build and ensure no tracked Docker/Make references to `cmd/worker` remain.

## BUG-02 — make the web queue safely retryable

1. Define the wire contract used by the existing web queue: preserve each local scan ID, send queued records in bounded batches, and return per-item outcomes.
2. Add server-side idempotency persistence keyed by the client scan ID, with a unique constraint and safe duplicate response. Process accepted marks through the existing audited mark service; do not create an unaudited write path.
3. Add the authenticated API route/service seam and map permanent versus transient failures so the client can dead-letter only permanent items.
4. Update the web queue to call the batch endpoint, preserve FIFO order, remove only applied/duplicate items, dead-letter permanent rejections, and retry transient failures with bounded jitter/backoff.
5. Clear/expire sensitive queued material on terminal outcomes and document the remaining storage threat model.
6. Add focused API and web tests for idempotent duplicate submission, mixed batch outcomes, ordering, retry behavior, and dead-letter handling.

## Verification

- Run API unit tests and web typecheck/unit tests where tooling is available.
- Build the API image path or, if Docker is unavailable, verify its referenced source/artifacts statically.
- Confirm `mobile/` and unrelated production surfaces are unchanged.
