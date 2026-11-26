# Contributing

Thanks for your interest in OD-Amrita. This guide covers what you need to get a change
merged.

## Getting set up

Follow [HOWTO.md](HOWTO.md) to install the toolchain, generate the contract types, start
the local database and run each surface. Every password in this repository is a
local-development placeholder; never reuse one anywhere shared.

## Ground rules

- **Contract first.** `contracts/openapi.yaml` is the single source of truth for the API.
  Change it first, then run `make -C contracts generate`. Never edit anything under
  `contracts/generated/`; CI fails on drift.
- **No ORM.** The API uses gin, sqlc and goose. Schema changes are new goose migrations in
  `api/db/migrations/`, and every migration must apply and revert cleanly.
- **Every write is audited.** State-changing writes go through `store.WithAudit`, and new
  audit actions and error codes are registered as described in
  [CONVENTIONS.md](CONVENTIONS.md).
- **Tests come with the change.** Go tests sit beside the code (`*_test.go`); web and mobile
  tests sit beside components (`*.test.ts(x)`). Run the checks for every surface you touch
  before opening a pull request.
- **Conventional commits**, for example `fix(marking): reject a slot outside the event`.

## Pull requests

Keep each pull request focused on one change and explain why it is needed. CI must be
green before review.

## Security issues

Do not open public issues for vulnerabilities; see [SECURITY.md](SECURITY.md).

## Licence

OD-Amrita is licensed under the [GNU AGPL-3.0](LICENSE). By submitting a contribution you
agree that it is licensed under the same terms.

## Code of conduct

Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).
