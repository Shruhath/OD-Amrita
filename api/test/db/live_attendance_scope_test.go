//go:build integration

package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/google/uuid"

	"github.com/Shruhath/OD-Amrita/api/internal/service/marking"
)

// The branch scope on the teacher-facing live attendance read.
//
// A teacher may only MARK students in the branches they teach, and this read is
// held to the same rule — a teacher who cannot mark a student has no reason to
// read that student's record either. That is a disclosure boundary, so it is
// asserted against real Postgres rather than against a stub: the scope lives in
// the SQL, and a stub would prove only that the Go code passes an argument.
//
// The pairing that matters is the LIST and the COUNT. They are separate queries
// over the same predicate, and a scope applied to one but not the other pages a
// teacher through their own rows while reporting somebody else's total — a leak
// that is invisible on page one.
func TestLiveAttendanceIsScopedToTheTeachersOwnBranches(t *testing.T) {
	ctx := testCtx(t)
	pool := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	defer pool.Close()

	fixture := seedTwoBranchAttendance(t, ctx, pool)
	service := marking.NewPortalMarkReadService(pool)

	t.Run("a teacher sees only their own branch", func(t *testing.T) {
		page, err := service.Attendance(ctx, marking.Actor{
			UserID: fixture.teacherID.String(),
			Email:  "scope-teacher@amrita.edu",
			Role:   "teacher",
		}, fixture.eventID.String(), nil, nil, nil, 1, 50)
		if err != nil {
			t.Fatalf("teacher attendance read: %v", err)
		}

		for _, record := range page.Data {
			if record.Student.Branch != fixture.taughtBranchCode {
				t.Fatalf("teacher was shown a record from branch %q; the scope leaked", record.Student.Branch)
			}
		}
		if len(page.Data) != 1 {
			t.Fatalf("teacher saw %d records, want the 1 in their own branch", len(page.Data))
		}
		// The count must agree with the list, or pagination walks a teacher
		// through rows the list refuses to show.
		if page.Total != 1 {
			t.Fatalf("teacher was told there are %d records, but may see 1", page.Total)
		}
	})

	t.Run("an admin still sees the whole event", func(t *testing.T) {
		// The other half: scoping the teacher must not narrow the admin view,
		// which is the same query with the scope switched off.
		page, err := service.Attendance(ctx, marking.Actor{
			UserID: fixture.adminID.String(),
			Email:  "scope-admin@amrita.edu",
			Role:   "admin",
		}, fixture.eventID.String(), nil, nil, nil, 1, 50)
		if err != nil {
			t.Fatalf("admin attendance read: %v", err)
		}
		if len(page.Data) != 2 || page.Total != 2 {
			t.Fatalf("admin saw %d records (total %d), want both", len(page.Data), page.Total)
		}
	})

	t.Run("a coordinator is refused outright", func(t *testing.T) {
		if _, err := service.Attendance(ctx, marking.Actor{
			UserID: fixture.teacherID.String(),
			Email:  "scope-coordinator@amrita.edu",
			Role:   "coordinator",
		}, fixture.eventID.String(), nil, nil, nil, 1, 50); err == nil {
			t.Fatal("a coordinator read the attendance review")
		}
	})
}

type twoBranchFixture struct {
	eventID          uuid.UUID
	teacherID        uuid.UUID
	adminID          uuid.UUID
	taughtBranchCode string
}

// seedTwoBranchAttendance builds one event with a marked student in each of two
// branches, and a teacher who teaches exactly one of them.
func seedTwoBranchAttendance(t *testing.T, ctx context.Context, pool *pgxpool.Pool) twoBranchFixture {
	t.Helper()

	// Unique per run. These rows are never cleaned up — the suite shares one
	// database and other tests assert exact counts — so a fixed code collides
	// with the previous run's branch on the unique index.
	// Uppercase: branches.code carries a check constraint requiring it.
	token := strings.ToUpper(uuid.NewString()[:8])
	fixture := twoBranchFixture{
		eventID:          uuid.New(),
		teacherID:        uuid.New(),
		adminID:          uuid.New(),
		taughtBranchCode: "SCA" + token,
	}
	taughtBranch, otherBranch := uuid.New(), uuid.New()
	category := uuid.New()
	taughtStudent, otherStudent := uuid.New(), uuid.New()

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed (%s): %v", sql, err)
		}
	}

	exec(`INSERT INTO branches (id, code, name) VALUES ($1,$2,$3), ($4,$5,$6)`,
		taughtBranch, fixture.taughtBranchCode, "Scope A "+token, otherBranch, "SCB"+token, "Scope B "+token)
	exec(`INSERT INTO users (id, supabase_subject, email, name, role)
	      VALUES ($1,$2,$3,$4,'teacher'), ($5,$6,$7,$8,'admin')`,
		fixture.teacherID, "scope-teacher-"+token, "scope-teacher-"+token+"@amrita.edu", "Scope Teacher",
		fixture.adminID, "scope-admin-"+token, "scope-admin-"+token+"@amrita.edu", "Scope Admin")
	// The teacher teaches ONE of the two branches. This single row is what the
	// whole scope turns on.
	exec(`INSERT INTO user_branches (user_id, branch_id) VALUES ($1,$2)`, fixture.teacherID, taughtBranch)

	exec(`INSERT INTO events (id, name, event_date, end_date, status)
	      VALUES ($1,'Scope Fixture '||$2, CURRENT_DATE, CURRENT_DATE, 'active')`,
		fixture.eventID, token)
	exec(`INSERT INTO event_branches (event_id, branch_id) VALUES ($1,$2), ($1,$3)`,
		fixture.eventID, taughtBranch, otherBranch)
	exec(`INSERT INTO categories (id, event_id, name) VALUES ($1,$2,'Scope Category '||$3)`,
		category, fixture.eventID, token)
	exec(`INSERT INTO user_category_assignments (user_id, category_id, created_by_user_id)
	      VALUES ($1,$2,$3)`, fixture.teacherID, category, fixture.adminID)

	exec(`INSERT INTO students (id, roll_no, name, branch_id, section, batch)
	      VALUES ($1,$2,'Taught Student',$3,'A','2024'),
	             ($4,$5,'Other Student',$6,'A','2024')`,
		taughtStudent, "CB.SC.U4A"+token, taughtBranch,
		otherStudent, "CB.SC.U4B"+token, otherBranch)

	exec(`INSERT INTO od_records (id, student_id, event_id, category_id, slot, od_date, status, capture_method, actor_user_id, actor_role, reason_note)
	      VALUES (gen_random_uuid(),$1,$2,$3,1,CURRENT_DATE,'present','search',$4,'teacher','scope test seed'),
	             (gen_random_uuid(),$5,$2,$3,1,CURRENT_DATE,'present','search',$4,'teacher','scope test seed')`,
		taughtStudent, fixture.eventID, category, fixture.adminID, otherStudent)

	return fixture
}
