# NFC Populator — admin-gated mode (NOT a separate app)

The NFC **Populator** is an **admin-gated (RBAC)** mode **inside this Expo mobile
app** — it is **not** a separate repository, PWA, or deployable. There is no
`od-amrita-nfc-populator` project (AR16).

## What it is

An admin/authorized operator, gated by **role-based access control (RBAC)**,
enters Populator mode to write student card data: it reads the real section
roster from the DB and uses NDEF **read + write** over NFC to encode cards.

## Why it lives here (mode, not repo)

Collapsing the populator into the mobile app makes the NFC read/write and the
HMAC encode/decode a **genuinely shared module** with the coordinator scanner:

- `mobile/src/lib/nfc` — shared NFC (NDEF read/write) module.
- `mobile/src/lib/encode` — shared HMAC encode/decode module.

Both the scanner (`src/features/scan`) and this populator mode consume the same
`src/lib/{nfc,encode}` code. This is why the populator is a **feature folder**,
not a second runtime.

## Status

Skeleton placeholder only — this folder reserves the mode. The RBAC gate, roster
read, and NDEF encode flow are not built yet.

_See [`../../../../CONVENTIONS.md`](../../../../CONVENTIONS.md) for the full rationale._
