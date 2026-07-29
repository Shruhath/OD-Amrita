//go:build integration

package db

import (
	"sort"
	"strings"
	"testing"
)

// The machine guard behind the no-hard-delete posture.
//
// Two things are asserted here, and they protect different moments in time:
//
//  1. Every table that exists TODAY grants od_runtime exactly the privileges it
//     is supposed to — no more. A REVOKE that silently stops working, or a grant
//     widened by a careless migration, fails here rather than in production.
//
//  2. The DEFAULT privilege that will be applied to tables created TOMORROW does
//     not include DELETE. 0001 originally granted all four by default, which made
//     every no-hard-delete table safe only if its author remembered a REVOKE;
//     0007 flipped that. Without this assertion nothing would notice it being
//     flipped back.
//
// The declared map is deliberately exhaustive: a new table that nobody adds here
// fails the test. That is the point — it forces the "may the app delete these
// rows?" question to be answered once, in review, instead of being inherited by
// accident from a default.

// runtimePrivileges is the intended od_runtime grant for every application table.
// Adding a table to the schema without adding it here is a test failure by design.
var runtimePrivileges = map[string][]string{
	// Append-only ledger. The immutability trigger is the real enforcement (it
	// fires for every role including the owner); these grants are the outer skin.
	"audit_log": {"SELECT", "INSERT"},

	// Identity and session rows are genuinely disposable.
	"users":                    {"SELECT", "INSERT", "UPDATE", "DELETE"},
	"auth_session_revocations": {"SELECT", "INSERT", "UPDATE", "DELETE"},

	// Reference data the admin surface curates directly.
	"branches": {"SELECT", "INSERT", "UPDATE", "DELETE"},

	// Everything an OD record will eventually point at: archived, never deleted.
	"events":         {"SELECT", "INSERT", "UPDATE"},
	"event_branches": {"SELECT", "INSERT", "UPDATE"},
	"categories":     {"SELECT", "INSERT", "UPDATE"},

	// A teacher's branch associations: no-hard-delete, same posture as
	// event_branches/categories, inherited entirely from 0007's default
	// privileges (0008 writes no explicit GRANT/REVOKE).
	"user_branches": {"SELECT", "INSERT", "UPDATE"},

	// Imported student roster and its import summaries are stable references for
	// attendance, dashboard, and correction flows.
	"students":               {"SELECT", "INSERT", "UPDATE"},
	"student_roster_imports": {"SELECT", "INSERT", "UPDATE"},
}

// gooseBookkeeping is goose's own version table. It is created by od_migrator and
// so picks up the default privilege like any other table, but it is migration
// bookkeeping rather than application schema and the app never touches it.
const gooseBookkeeping = "goose_db_version"

var allPrivileges = []string{"SELECT", "INSERT", "UPDATE", "DELETE"}

func TestRuntimePrivileges_MatchTheDeclaredPostureForEveryTable(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	rows, err := pool.Query(ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
	if err != nil {
		t.Fatalf("list public tables: %v", err)
	}
	defer rows.Close()

	var present []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		if name == gooseBookkeeping {
			continue
		}
		present = append(present, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate public tables: %v", err)
	}
	if len(present) == 0 {
		t.Fatal("[P0] no application tables found — migrations did not run")
	}

	for _, table := range present {
		want, declared := runtimePrivileges[table]
		if !declared {
			t.Errorf("[P0] table %q has no declared od_runtime posture — add it to "+
				"runtimePrivileges and decide deliberately whether the app may DELETE its rows", table)
			continue
		}

		wanted := map[string]bool{}
		for _, privilege := range want {
			wanted[privilege] = true
		}

		var got []string
		for _, privilege := range allPrivileges {
			var held bool
			if err := pool.QueryRow(ctx,
				`SELECT has_table_privilege('od_runtime', $1, $2)`, table, privilege,
			).Scan(&held); err != nil {
				t.Fatalf("check %s on %s: %v", privilege, table, err)
			}
			if held {
				got = append(got, privilege)
			}
			if held && !wanted[privilege] {
				t.Errorf("[P0] od_runtime holds %s on %q but must not", privilege, table)
			}
			if !held && wanted[privilege] {
				t.Errorf("[P0] od_runtime is missing %s on %q", privilege, table)
			}
		}

		sort.Strings(got)
		sorted := append([]string(nil), want...)
		sort.Strings(sorted)
		if strings.Join(got, ",") != strings.Join(sorted, ",") {
			t.Logf("table %q: want [%s], got [%s]", table, strings.Join(sorted, ","), strings.Join(got, ","))
		}
	}

	for table := range runtimePrivileges {
		found := false
		for _, name := range present {
			if name == table {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("[P0] declared table %q does not exist — runtimePrivileges is stale", table)
		}
	}
}

// The forward-looking half: what a table created tomorrow inherits.
func TestRuntimeDefaultPrivileges_DoNotIncludeDelete(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, envMigratorDSN))

	var acl string
	err := pool.QueryRow(ctx, `
		SELECT coalesce(array_to_string(d.defaclacl::text[], ' '), '')
		FROM pg_default_acl d
		JOIN pg_roles r ON r.oid = d.defaclrole
		WHERE r.rolname = 'od_migrator'
		  AND d.defaclnamespace = 'public'::regnamespace
		  AND d.defaclobjtype = 'r'`).Scan(&acl)
	if err != nil {
		t.Fatalf("read default ACL for od_migrator tables: %v", err)
	}

	// aclitem text is "grantee=privileges/grantor"; the privilege letters are
	// r=SELECT, a=INSERT, w=UPDATE, d=DELETE.
	var entry string
	for _, item := range strings.Fields(acl) {
		if strings.HasPrefix(item, "od_runtime=") {
			entry = item
			break
		}
	}
	if entry == "" {
		t.Fatalf("[P0] no default privilege entry for od_runtime (got %q) — new tables would "+
			"reach the app with no grant at all", acl)
	}

	privileges := entry[strings.Index(entry, "=")+1:]
	if slash := strings.Index(privileges, "/"); slash >= 0 {
		privileges = privileges[:slash]
	}

	if strings.Contains(privileges, "d") {
		t.Errorf("[P0] default privileges still grant DELETE to od_runtime (%q). Every new table "+
			"would inherit it, making no-hard-delete depend on remembering a REVOKE", entry)
	}
	for letter, name := range map[string]string{"r": "SELECT", "a": "INSERT", "w": "UPDATE"} {
		if !strings.Contains(privileges, letter) {
			t.Errorf("[P0] default privileges no longer grant %s to od_runtime (%q)", name, entry)
		}
	}
}
