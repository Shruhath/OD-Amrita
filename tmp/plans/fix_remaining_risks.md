# Plan: Fix Remaining Offline, River, and TLS Risks

These are intentionally separate commits so each fix can be cherry-picked independently.

## RISK-SEC-03 — protect offline queue material

1. Encrypt NFC card payloads at rest in IndexedDB with a non-extractable per-browser AES-GCM key.
2. Add a bounded retention window for pending scans and clear payload material on expiry/dead-letter.
3. Clear the queue and encryption key during sign-out/session loss.
4. Add focused storage/auth tests and document the remaining same-origin compromise limitation.

## RISK-REL-02 — remove empty River runtime

1. Remove the unregistered River client/worker lifecycle from `cmd/api`.
2. Remove now-unused River dependencies and stale environment comments.
3. Keep the scan API synchronous and audited until a real async job contract exists.
4. Verify API build/tests and confirm no River bootstrap remains.

## RISK-REL-03 — enforce production ingress acknowledgment

1. Add an explicit `TLS_TERMINATED_BY_TRUSTED_PROXY` configuration gate.
2. Reject `TLS_ENABLED=false` outside development unless that acknowledgment is true.
3. Set the flag in the Fly deployment configuration and update examples/runbook/tests.
4. Verify secure TLS mode remains the default and plaintext mode cannot start accidentally in production.
