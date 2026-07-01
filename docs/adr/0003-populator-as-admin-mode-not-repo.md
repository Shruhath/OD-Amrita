# 3. NFC Populator is an admin-gated mode, not a separate repo

- Status: accepted (placeholder — expand from architecture#Surface-4-SUPERSEDED)
- Date: 2026-07-01

## Context
An earlier design had a separate `od-amrita-nfc-populator` deployable/PWA. That
duplicated the NFC read/write + HMAC encode/decode logic across runtimes.

## Decision
The populator is an **admin-RBAC-gated mode inside the mobile app** (AR16). It
shares `mobile/src/lib/{nfc,encode}` with the coordinator scanner. There is no
separate populator repo or deployable.

## Consequences
Three NFC runtimes collapse to two; encode/decode becomes a genuinely shared
module. Placeholder ADR — expand in Epic 6.
