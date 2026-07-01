// Package store owns database access: the sqlc-generated queries plus the
// transaction and audit helpers that wrap them. Story 1.1 reserves the homes;
// logic lands in Stories 1.3–1.4.
package store

// tx.go ★ — reserved home for the same-transaction helper (`WithTx`) that runs
// a business write and its immutable audit-log write in ONE transaction
// (architecture: audit is written in the same tx as the change it records).
// Intentionally empty in the skeleton — implemented in Story 1.4.
