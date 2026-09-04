package store

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type reviewedWriteQuery struct {
	file   string
	name   string
	reason string
}

var reviewedWriteQueries = []reviewedWriteQuery{
	{"app_settings.sql", "UpsertAppSetting", "audited by app_setting.updated"},
	{"audit.sql", "InsertAuditLog", "the ledger insert itself"},
	{"auth_session_revocations.sql", "InsertRevocation", "audited by auth.session_revoked"},
	{"capture_credits.sql", "DebitCaptureCredit", "co-committed with od.created for fallback marks"},
	{"capture_credits.sql", "ResetCaptureCreditUsage", "audited by capture_credit.reset"},
	{"capture_credits.sql", "UpdateCoordinatorCaptureCreditLimit", "audited by capture_credit.cap_changed"},
	{"card_tokens.sql", "MarkStudentCardTokenVerified", "audited by card_token.verified"},
	{"card_tokens.sql", "UpsertStudentCardToken", "audited by card_token.sealed"},
	{"categories.sql", "AssignUserCategoriesBulk", "audited by teacher/coordinator category access updates"},
	{"categories.sql", "AssignUserCategory", "audited by coordinator.assigned"},
	{"categories.sql", "CreateCategory", "audited by category.created"},
	{"categories.sql", "DeleteUserCategoriesExcept", "audited by teacher category access updates"},
	{"categories.sql", "DeleteUserCategoriesForEventExcept", "audited by coordinator category access updates"},
	{"categories.sql", "SetCategoryPublicVisibility", "audited by category.visibility_updated"},
	{"disputes.sql", "CreateODRecordFromApprovedDispute", "extra od.created row in dispute resolution tx"},
	{"disputes.sql", "GetDisputeForResolution", "row lock used before dispute.resolved audit snapshot"},
	{"disputes.sql", "InsertDispute", "audited by dispute.submitted"},
	{"disputes.sql", "ResolveDispute", "audited by dispute.resolved"},
	{"event_coordinator_assignments.sql", "InsertEventCoordinatorAssignment", "audited by coordinator.assigned"},
	{"event_coordinator_assignments.sql", "LockCoordinatorAssignmentUser", "row lock used before coordinator assignment audit snapshot"},
	{"event_coordinator_assignments.sql", "PromoteUserToCoordinator", "audited by coordinator.assigned"},
	{"events.sql", "AddEventBranch", "audited as part of event.created"},
	{"events.sql", "ArchiveEvent", "audited by event.archived"},
	{"events.sql", "CreateEvent", "audited by event.created"},
	{"od_records.sql", "CorrectODRecord", "audited by od.overridden"},
	{"od_records.sql", "InsertODPresentRecord", "audited by od.created"},
	{"od_records.sql", "LockODRecordByID", "row lock used before OD management audit snapshot"},
	{"od_records.sql", "RemoveODRecord", "audited by od.removed"},
	{"students.sql", "InsertStudentRosterImport", "audited by student_roster.imported"},
	{"students.sql", "UpsertStudent", "audited as part of student_roster.imported"},
	{"students.sql", "UpsertStudentsBulk", "audited as part of student_roster.imported"},
	{"teachers.sql", "AddUserBranch", "audited by teacher.profile_created"},
	{"teachers.sql", "AddUserBranchesBulk", "audited by teacher.profile_created"},
	{"teachers.sql", "AssignTeacherRole", "audited by teacher.profile_created"},
	{"users.sql", "ProvisionUser", "audited by user.provisioned"},
}

func TestEveryWriteQueryIsReviewedForAuditCoverage(t *testing.T) {
	got := findWriteQueries(t)
	want := make(map[string]reviewedWriteQuery, len(reviewedWriteQueries))
	for _, query := range reviewedWriteQueries {
		if query.reason == "" {
			t.Fatalf("reviewed write query %s:%s has no audit reason", query.file, query.name)
		}
		key := query.file + ":" + query.name
		if _, exists := want[key]; exists {
			t.Fatalf("duplicate reviewed write query %s", key)
		}
		want[key] = query
	}

	for _, key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("write query %s is not reviewed for audit coverage", key)
		}
	}
	for key := range want {
		if !slices.Contains(got, key) {
			t.Errorf("reviewed write query %s no longer exists in api/db/queries", key)
		}
	}
}

func findWriteQueries(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob("../../db/queries/*.sql")
	if err != nil {
		t.Fatalf("glob query files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no sqlc query files found")
	}

	var out []string
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, query := range splitSQLCQueries(string(src)) {
			body := stripSQLLineComments(query.body)
			if writeSQL.MatchString(body) {
				out = append(out, filepath.Base(file)+":"+query.name)
			}
		}
	}
	slices.Sort(out)
	return out
}

var (
	sqlcQueryStart = regexp.MustCompile(`^-- name:\s+([A-Za-z0-9_]+)\s+:[A-Za-z0-9_]+\s*$`)
	writeSQL       = regexp.MustCompile(`(?is)\b(INSERT|UPDATE|DELETE)\b|\bFOR\s+UPDATE\b`)
	sqlLineComment = regexp.MustCompile(`(?m)^\s*--.*$`)
)

func stripSQLLineComments(src string) string {
	return strings.TrimSpace(sqlLineComment.ReplaceAllString(src, ""))
}

type sqlcQuery struct {
	name string
	body string
}

func splitSQLCQueries(src string) []sqlcQuery {
	var queries []sqlcQuery
	var current *sqlcQuery

	for _, line := range strings.Split(src, "\n") {
		if match := sqlcQueryStart.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			if current != nil {
				queries = append(queries, *current)
			}
			current = &sqlcQuery{name: match[1]}
			continue
		}
		if current != nil {
			current.body += line + "\n"
		}
	}
	if current != nil {
		queries = append(queries, *current)
	}
	return queries
}
