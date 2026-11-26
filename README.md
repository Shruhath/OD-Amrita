<div align="center">

# OD-Amrita

### The attendance nervous system for a university that never stops moving.

**One tap. Zero paper. Impossible to fake. Works even when the internet doesn't.**

Barcode and NFC ID-card scanning — offline-first, cryptographically signed, real-time, audit-proof.

</div>

---

Every year, universities lose thousands of hours and tens of thousands of attendance records to paper sheets, proxy signatures, and dead Wi-Fi in crowded halls.

**OD-Amrita replaces all of it with a single scan — and makes that scan provable, instant, and tamper-proof across an entire campus running dozens of simultaneous events.**

---

## A day at the fest, told twice

### Before OD-Amrita

It is 9:40 AM. **Google Estimate 2026** is starting. Three coordinators stand at the gates of the CSE procession with clipboards and printed roll-number sheets.

- A line of **400 students** snakes across the ground. Each one calls out a roll number; a coordinator squints, scrolls a paper column, and ticks a box. Twelve seconds per student. The line does not move.
- Ravi was already marked at Gate A. He walks to Gate B and is marked **again**. Nobody knows. Two ODs, one student.
- Priya never came. Her friend whispers her roll number at the gate. Tick. **A proxy OD that no one can ever trace.**
- The auditorium has **no signal**. The coordinator inside is writing on paper, because the shared spreadsheet has not loaded since morning.
- At midnight, a teacher sits with six clipboards, hand-typing 1,100 roll numbers into the university ERP. She miskeys a digit. A student who attended is marked absent, and finds out at grading.
- No one — not one person — knows in real time how many students are actually at the event. The number will not exist until tomorrow. Maybe.

Multiply that by every category, every event, every branch, every day. **This is the status quo at every large campus on earth.**

### After OD-Amrita

It is 9:40 AM. The same three coordinators open the app. Each sees **only the events they were assigned to** — nothing else to get lost in.

- A student holds up their ID card. **Tap.** A cryptographically signed NFC read fires, the slot is selected from the clock, and the screen turns green in **under half a second.** The line evaporates.
- Ravi taps at Gate B. The phone rejects it: **"Already marked present for Slot 2."** The duplicate is caught before it exists.
- There is no proxy. The card carries an **HMAC-signed payload** — you cannot mark a student who is not physically holding their card.
- The auditorium still has no signal. It does not matter. Scans are captured **on-device**, and **sync automatically** the instant a bar of signal returns. The coordinator never notices.
- A **live counter** climbs on every coordinator's screen — and a **cumulative total across all three coordinators** updates in real time, so the true headcount is visible as it happens.
- The teacher opens her portal — and runs **the entire operation** from one screen. She creates the event, splits it into categories, and **assigns coordinators** to each gate. Every OD is already there, live, sorted by slot, category, and section. A bad mark is **overridden in one tap.** She clicks **Export**, the ERP-ready file downloads, and she is home by 9 PM.
- Underneath all of it, an **immutable audit log** records which coordinator marked which student, at what millisecond, from which device. Proxy ODs are not discouraged. **They are impossible.**

**The same day — minus the chaos, minus the fraud, minus the midnight data entry — and it holds even when the network does not.**

---

## The features that are hard to believe are real

| Capability | Why it is hard to believe |
|---|---|
| **Tamper-proof cryptographic cards** | Every ID card carries an **HMAC-signed** identity payload written over NFC. A scan is verified mathematically — forged or cloned cards are rejected on contact. Proxy attendance becomes cryptographically unforgeable. |
| **Offline-first, zero-loss sync** | Scans are captured and queued **on-device with no connection at all**, then reconciled the moment a signal returns — exactly once, no duplicates, no lost records. Built for auditoriums, basements, and open grounds. |
| **Real-time cumulative counters** | Many coordinators, one category, **one shared live number** — updating across devices in real time as a venue fills. |
| **Clock-aware slot mapping** | Scan at 9:40 and the platform knows it is Slot 2. No dropdowns, no mistakes — the time is the input. |
| **In-flight duplicate detection** | A student marked twice is stopped **before the second write commits** — instant rejection at the gate. |
| **Forensic audit trail** | Coordinator, student, millisecond, device — every mark, immutable. The end of "who marked my friend present?" |
| **Sessions that never wall a coordinator** | A **single-flight silent token refresh** renews a coordinator's session transparently mid-scan. They never hit a login screen during a live event. |
| **Teacher command center** | A live, branch-wide view of every event and category as it happens — not a next-day report. |
| **Bulk import and one-click ERP export** | An entire semester of students and sections imported from one CSV; any OD list exported to the university ERP in a single click. |
| **Public proof-of-attendance** | A student searches their own name on a public dashboard and sees every OD they have ever earned — no login, full transparency. |

---

## The power sits with the teacher

OD-Amrita has no separate administrator caste. **The teacher holds the authority.** From a single portal, a teacher:

- **Creates events** and splits them into categories.
- **Appoints and revokes coordinators** for any gate, at any time.
- **Adds, edits, or overrides any OD** — one tap corrects a mistake a coordinator made hours ago.
- **Sees everything, live** — every scan across every event she owns, sorted by slot, category, and section.
- **Imports an entire semester** of students and sections from one CSV.
- **Exports to the university ERP** in a single click.
- **Reads the full audit trail** — every mark, every coordinator, every device, immutable.

Coordinators execute at the gate. Students carry a signed identity and can verify their own record. **Teachers run the whole machine.**

---

## How it is engineered

OD-Amrita is not an app. It is a **contract-first, multi-surface distributed platform** built as a single atomic monorepo — engineered to survive campus-scale traffic spikes where **thousands of scans land per minute** during peak processions.

```
                         ┌────────────────────────────────────────────┐
                         │              CONTRACT (SSOT)                │
                         │   OpenAPI spec → generated Go + TS clients  │
                         │    drift-gated in CI · one source of truth  │
                         └───────────────┬────────────────────────────┘
                                         │ (types flow everywhere)
                     ┌───────────────────┴───────────────────┐
                     ▼                                       ▼
           ┌──────────────────┐                    ┌───────────────┐
           │      BACKEND      │◀───── portal ──────│      WEB      │
           │  Go · gin · sqlc  │                    │ React + Vite  │
           │  goose · no ORM   │                    │    Teacher    │
           │                   │                    │   + Public    │
           │  ┌─────────────┐  │                    └───────────────┘
           │  │ River worker │  │        ┌─────────────────────┐
           │  │  queue: high │  │        │      PostgreSQL     │
           │  │  throughput  │──┼───────▶│  audit · ODs · RBAC  │
           │  └─────────────┘  │        └─────────────────────┘
           └─────────┬─────────┘
                     │
          ┌──────────▼─────────────┐
          │     Hybrid Identity     │
          │ Supabase brokers MS     │
          │ OAuth · backend verifies│
          │ JWT · @amrita.edu gate  │
          └─────────────────────────┘
```

**Every piece is deliberate:**

- **Contract-first SSOT.** A single OpenAPI spec generates *both* the Go server types and the TypeScript client types. Frontend and backend **cannot drift** — CI fails the build if they do. The API is provably in sync with every client, always.
- **Go, gin, sqlc, goose — no ORM.** Raw, compiled, hand-tuned SQL with zero reflection overhead. Two always-on entrypoints: an HTTP server and a **persistent River worker** that absorbs scan bursts without dropping a record.
- **Hybrid, hardened identity.** Supabase brokers Microsoft OAuth; the Go backend independently **verifies every JWT**, enforces the `@amrita.edu` domain, and honors **server-side session revocation** — a forced logout takes effect on the next request.
- **TLS 1.2+ only.** Plain HTTP is **rejected, not redirected.** No downgrade path exists.
- **Per-surface, path-filtered CI.** Independent pipelines; a change to one surface spins up only its own gates. Fast, isolated, drift-guarded.

---

## Who it is for

| Role | What they hold |
|---|---|
| **Teacher** | The whole machine. Create events, appoint coordinators, override any OD, bulk-import, ERP-export, and read the full audit trail — live. |
| **Coordinator** | The gate. Open, see only your events, and scan — frictionless, offline-proof, sub-second. |
| **Student** | A signed identity and full transparency — look up your own OD history any time, from anywhere. |

---

<div align="center">

## OD-Amrita turns a clipboard, a queue, and a leap of faith into one tap that cannot be faked and cannot be lost.

**Attendance, finally solved.**

</div>

---

## Repository Structure

```
├── api/                  # Go backend (HTTP server + persistent River worker)
│   ├── cmd/              # Entrypoints for the API server and River worker
│   ├── db/               # DB initialization and goose migration scripts
│   ├── internal/         # Shared business logic and handlers
│   └── scripts/          # Administration utilities
├── contracts/            # OpenAPI specification and code generation configs
│   └── generated/        # Generated Go and TS models (do not edit manually)
├── docs/                 # Engineering overviews, architectural diagrams, and ADRs
├── mobile/               # React Native (Expo) app — Microsoft sign-in and an authenticated API round trip (early stage)
├── test/                 # Test suites
│   ├── contract/         # OpenAPI compliance and drift-gate validations
│   └── skeleton/         # Workspace wiring and structural verification tests
└── web/                  # React + Vite web portal for admins and teachers
```

## Core Architectural Rules and Conventions

The development team adheres to strict architectural boundaries:
*   **Dual-Role Database Access**: The backend separates database access using two distinct roles. The `od_migrator` has full schema control to run migrations, while `od_runtime` is the application's connection pool, restricted to DML operations with absolutely no DDL permissions.
*   **Append-Only Audit Log**: Immutability of audit trails is guaranteed via database triggers that explicitly reject updates, deletes, or truncations.
*   **Centralized Registry Discipline**: All error codes must be defined inside `contracts/openapi.yaml` first, and all audit event types must be registered in the backend codebase before use.
*   **Transport Security**: Standard HTTP ports are blocked; the application runs strictly on TLS 1.2+.

---

## Getting Started

To get the codebase up and running locally, run the initial setup commands:

```bash
pnpm install
go work sync
make -C contracts generate
```

For full environment configuration, TLS setup, database migrations, and execution steps, refer to the [Developer Operations Guide (HOWTO.md)](./HOWTO.md).
To run your own deployment, follow the [self-hosting guide](./docs/deployment.md).

---

## License

**Copyright © 2026 The Amrita-UN Team. All rights reserved under the [GNU AGPL-3.0](./LICENSE).**

OD-Amrita is source-available under the **GNU Affero General Public License v3.0**. You may read, run, and self-host it — but the AGPL is deliberate: **anyone who deploys this software, or a modified version of it, over a network must release their full source under the same license.** There is no closed fork. The work stays open, and it stays ours.

---

<sub>**For engineers:** setup and running instructions live in [`HOWTO.md`](./HOWTO.md). The precise, no-hype technical breakdown — workspace wiring, CI topology, transport-security assertions, and conventions — lives in [`docs/engineering-overview.md`](./docs/engineering-overview.md) and [`CONVENTIONS.md`](./CONVENTIONS.md).</sub>
