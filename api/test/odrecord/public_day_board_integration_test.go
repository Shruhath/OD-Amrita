//go:build integration

// Real-Postgres tests for the PUBLIC ATTENDANCE BOARD — one campus day's marked
// students, served without a search and without a credential.
//
// This is the widest disclosure on the public surface, so the assertions here
// are mostly about what must NOT come back, and they run against real Postgres
// rather than a stub: the boundary is the query's select list, and a stub would
// only prove that the Go code copies the fields it was handed.
//
// In the EXISTING api/test/odrecord package on purpose — ./test/odrecord/... is
// already wired into api/Makefile's INTEGRATION_PKGS and api-ci.yml, and a new
// package here would silently never run in CI the way three earlier stories'
// packages did.
package odrecordtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
)

// boardDay is a day of its own, so these tests cannot be perturbed by — or
// perturb — the fixtures every other file in this package seeds on 2026-08-03.
const boardDay = "2026-09-14"

// BOARD-INT-001 — the board groups a day's rows by student, ordered by roll
// number, with each student's slots ascending.
//
// The order is the product requirement: a visitor scans for their own roll
// number, and one student's marks must read "6, 7, 10" rather than in whatever
// order the rows happen to come back.
func TestPublicDayBoard_GroupsByStudentOrderedByRollNumber(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	fixture := seedMarkFixture(t, ctx, migrator)
	event := seedBoardEvent(t, ctx, migrator, boardDay)
	// A SECOND student whose roll number sorts BEFORE the fixture's. With one
	// student, "ordered by roll number" and "whatever came back" are the same
	// list and the assertion cannot fail.
	// Unique per run: the suite shares one database and never cleans up, so a
	// fixed roll number collides with the previous run on the unique index. The
	// AA prefix is what makes it sort first, which is the property under test.
	secondRoll := "AA.SC.U4BRD" + strings.ToUpper(uuid.NewString()[:8])
	second := seedBoardStudent(t, ctx, migrator, secondRoll)

	// Inserted deliberately out of order — the later slot first, and the
	// later-sorting student first — so an already-sorted fixture cannot pass
	// this by accident.
	seedPublicRecord(t, ctx, migrator, fixture.studentID, event.eventID, event.categoryID, 10, fixture.coordinatorID, "coordinator", boardDay)
	seedPublicRecord(t, ctx, migrator, fixture.studentID, event.eventID, event.categoryID, 6, fixture.coordinatorID, "coordinator", boardDay)
	seedPublicRecord(t, ctx, migrator, second, event.eventID, event.categoryID, 7, fixture.coordinatorID, "coordinator", boardDay)

	board, err := publicattendance.NewSQLCService(runtime).Day(ctx, publicattendance.DayFilter{ODDate: boardDay})
	if err != nil {
		t.Fatalf("[P0] Day: %v", err)
	}

	if len(board.Students) < 2 {
		t.Fatalf("[P0] the board shows %d students, want at least the 2 seeded", len(board.Students))
	}
	// RELATIVE order, not "leads the whole board". The suite shares one database
	// and never cleans up, so a previous run's students are on this day too and
	// an absolute position would fail on leftovers rather than on the ordering.
	// The property under test is the same either way: the lower roll number
	// comes first.
	lower, higher := boardIndexOf(t, board, secondRoll), boardIndexOf(t, board, fixture.rollNo)
	if lower > higher {
		t.Errorf("[P0] %q is at position %d and %q at %d — the board must be ordered by roll number, which is "+
			"what a visitor scans it for", secondRoll, lower, fixture.rollNo, higher)
	}

	marks := boardMarksFor(t, board, fixture.rollNo)
	if len(marks) != 2 {
		t.Fatalf("[P0] the fixture student has %d marks, want the 2 seeded", len(marks))
	}
	if marks[0].Slot != 6 || marks[1].Slot != 10 {
		t.Errorf("[P0] slots came back %d then %d, want 6 then 10 — one student's marks must read in slot order",
			marks[0].Slot, marks[1].Slot)
	}
	if marks[0].CategoryName == "" || marks[0].EventName == "" {
		t.Errorf("[P0] a mark carries category %q and event %q; the board's whole payload per mark is which "+
			"slot and what it was for", marks[0].CategoryName, marks[0].EventName)
	}
}

// BOARD-INT-002 — A REMOVED RECORD IS NOT ON THE BOARD.
//
// The authenticated attendance read deliberately shows removed rows so staff can
// see what happened. Publishing them would show a student as marked after a
// teacher had removed the record — worse than showing nothing, and exactly the
// kind of divergence that follows from reusing the admin query with a wider
// audience.
func TestPublicDayBoard_ExcludesRemovedRecords(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	fixture := seedMarkFixture(t, ctx, migrator)
	day := "2026-09-15"
	event := seedBoardEvent(t, ctx, migrator, day)
	seedPublicRecord(t, ctx, migrator, fixture.studentID, event.eventID, event.categoryID, 4, fixture.coordinatorID, "coordinator", day)
	seedPublicRecord(t, ctx, migrator, fixture.studentID, event.eventID, event.categoryID, 5, fixture.coordinatorID, "coordinator", day)

	// Removed AFTER the fact, which is how it happens: a teacher corrects a
	// mistake. The row stays in the table — attendance history is never deleted.
	if _, err := migrator.Exec(ctx,
		`UPDATE od_records SET status = 'removed' WHERE student_id = $1 AND od_date = $2::date AND slot = 5`,
		fixture.studentID, day); err != nil {
		t.Fatalf("remove seed record: %v", err)
	}

	board, err := publicattendance.NewSQLCService(runtime).Day(ctx, publicattendance.DayFilter{ODDate: day})
	if err != nil {
		t.Fatalf("[P0] Day: %v", err)
	}

	marks := boardMarksFor(t, board, fixture.rollNo)
	for _, mark := range marks {
		if mark.Slot == 5 {
			t.Fatalf("[P0] slot 5 was REMOVED and is still on the public board. The board would be telling the " +
				"campus a student was marked for something a teacher had struck off")
		}
	}
	// The other half: removing one mark must not hide the rest.
	if len(marks) != 1 || marks[0].Slot != 4 {
		t.Errorf("[P0] after removing slot 5 the student shows %d marks, want only slot 4", len(marks))
	}
}

// BOARD-INT-003 — the slot and category filters narrow the SERVER's answer, and
// the category options are built from the UNFILTERED day.
//
// Filtering in the browser would mean the whole day's roster is sent regardless
// of what was asked for, which on this surface is the difference between
// publishing one slot and publishing everything.
func TestPublicDayBoard_FiltersNarrowTheReadAndOptionsSurviveThem(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	fixture := seedMarkFixture(t, ctx, migrator)
	day := "2026-09-16"
	event := seedBoardEvent(t, ctx, migrator, day)
	other := seedPublicEvent(t, ctx, migrator, "Board Filter Other Event "+uuid.NewString()[:8], day)

	seedPublicRecord(t, ctx, migrator, fixture.studentID, event.eventID, event.categoryID, 3, fixture.coordinatorID, "coordinator", day)
	seedPublicRecord(t, ctx, migrator, fixture.studentID, other.eventID, other.categoryID, 9, fixture.coordinatorID, "coordinator", day)

	service := publicattendance.NewSQLCService(runtime)

	bySlot, err := service.Day(ctx, publicattendance.DayFilter{ODDate: day, Slot: 9})
	if err != nil {
		t.Fatalf("[P0] Day(slot=9): %v", err)
	}
	for _, student := range bySlot.Students {
		for _, mark := range student.Marks {
			if mark.Slot != 9 {
				t.Fatalf("[P0] the slot filter returned slot %d; it must narrow the QUERY, not the rendering",
					mark.Slot)
			}
		}
	}

	byCategory, err := service.Day(ctx, publicattendance.DayFilter{ODDate: day, CategoryID: other.categoryID})
	if err != nil {
		t.Fatalf("[P0] Day(category): %v", err)
	}
	for _, student := range byCategory.Students {
		for _, mark := range student.Marks {
			if mark.CategoryID != other.categoryID {
				t.Fatalf("[P0] the category filter returned category %s, want only %s",
					mark.CategoryID, other.categoryID)
			}
		}
	}

	// THE OPTIONS MUST SURVIVE THE FILTER. Derived from the filtered rows, the
	// control would collapse to the one option already chosen and a visitor
	// would have no way back to the others.
	if len(byCategory.Categories) < 2 {
		t.Errorf("[P0] with a category filter applied the board offers %d category options, want every category "+
			"with a mark that day. A filter control that narrows itself is a filter you cannot undo",
			len(byCategory.Categories))
	}
}

// BOARD-INT-004 — THE DISCLOSURE ASSERTION.
//
// Nothing on the board may name a member of staff, quote a correction note, or
// hand out a student id. The first two are in od_records and in the query the
// admin view uses; the third is what turns a list into a directory, because an
// id walks straight into the per-student history for everyone on the board in
// one pass.
//
// Asserted over the SERVICE's own types by reflection-free inspection of the
// values it returns, plus a direct check that the seeded reason note — which is
// definitely in the row — is nowhere in the response.
func TestPublicDayBoard_CarriesNoStaffIdentityNoteOrStudentID(t *testing.T) {
	ctx := testCtx(t)
	migrator := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_MIGRATOR"))
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	fixture := seedMarkFixture(t, ctx, migrator)
	day := "2026-09-17"
	event := seedBoardEvent(t, ctx, migrator, day)
	// seedPublicRecord writes a reason_note of its own; this is the string that
	// must not escape. It is a stand-in for the real thing, which is a teacher
	// explaining why they struck a student's attendance off.
	seedPublicRecord(t, ctx, migrator, fixture.studentID, event.eventID, event.categoryID, 8, fixture.coordinatorID, "coordinator", day)

	board, err := publicattendance.NewSQLCService(runtime).Day(ctx, publicattendance.DayFilter{ODDate: day})
	if err != nil {
		t.Fatalf("[P0] Day: %v", err)
	}

	// Everything the board actually says, flattened, so one scan covers it.
	var rendered []string
	for _, student := range board.Students {
		rendered = append(rendered, student.RollNo, student.DisplayName)
		for _, mark := range student.Marks {
			rendered = append(rendered, mark.CategoryID, mark.CategoryName, mark.EventName)
		}
	}
	haystack := strings.ToLower(strings.Join(rendered, "\x00"))

	for _, forbidden := range []struct{ value, why string }{
		{"public attendance integration seed", "the record's reason_note — a teacher's free-text justification " +
			"for a correction, which must never be public"},
		{"@", "an email address; no public shape may carry one"},
		{fixture.coordinatorID, "the acting staff member's user id"},
		{fixture.studentID, "the student's id — a list that hands out ids is a list that can be walked into " +
			"every student's full history in one pass"},
		{fixture.cardToken, "card material"},
	} {
		if forbidden.value == "" {
			continue
		}
		if strings.Contains(haystack, strings.ToLower(forbidden.value)) {
			t.Errorf("[P0] the public board leaked %q — that is %s", forbidden.value, forbidden.why)
		}
	}

	// And the positive half: it did return the mark, so the scan above was over
	// a real response rather than an empty one.
	if len(boardMarksFor(t, board, fixture.rollNo)) == 0 {
		t.Fatal("[P0] the board returned no marks for the seeded student, so the leak assertions above scanned " +
			"nothing and would pass against a broken read")
	}
}

// BOARD-INT-005 — an empty day is a SUCCESSFUL read with no students.
//
// "Nobody has been marked yet" and "something went wrong" must not arrive
// looking alike — the same rule the per-student view has always been held to.
func TestPublicDayBoard_AnEmptyDayIsASuccessfulRead(t *testing.T) {
	ctx := testCtx(t)
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	board, err := publicattendance.NewSQLCService(runtime).Day(ctx, publicattendance.DayFilter{ODDate: "1999-01-04"})
	if err != nil {
		t.Fatalf("[P0] a day with no marks must be a successful empty read, got: %v", err)
	}
	if len(board.Students) != 0 {
		t.Errorf("[P0] a day nobody was marked on returned %d students", len(board.Students))
	}
	if board.Truncated {
		t.Error("[P0] an empty board reported itself truncated")
	}
	if board.ODDate != "1999-01-04" {
		t.Errorf("[P0] the board echoed od_date %q, want the day that was asked for", board.ODDate)
	}
}

// BOARD-INT-006 — omitting the day defaults to TODAY ON CAMPUS.
//
// This is what makes the board answer with no input at all, which is the whole
// product requirement. Asserted against the campus wall clock rather than the
// process's: Fly runs this in Singapore while the students are in Kerala, and a
// UTC "today" would roll the board over at 05:30 IST, in the middle of a
// morning slot.
func TestPublicDayBoard_DefaultsToTodayOnCampus(t *testing.T) {
	ctx := testCtx(t)
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))

	board, err := publicattendance.NewSQLCService(runtime).Day(ctx, publicattendance.DayFilter{})
	if err != nil {
		t.Fatalf("[P0] Day with no date: %v", err)
	}

	campus, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("load campus location: %v", err)
	}
	want := time.Now().In(campus).Format(time.DateOnly)
	if board.ODDate != want {
		t.Errorf("[P0] an unfiltered board answered for %q, want today on campus (%q). Defaulting to the "+
			"server's own day rolls the board over at 05:30 IST, mid-slot", board.ODDate, want)
	}
}

// BOARD-INT-007 — a malformed day or category is a typed validation refusal, not
// a 500 and not a silent empty board.
func TestPublicDayBoard_RefusesMalformedInput(t *testing.T) {
	ctx := testCtx(t)
	runtime := connect(t, ctx, dsnOrSkip(t, "DATABASE_URL_RUNTIME"))
	service := publicattendance.NewSQLCService(runtime)

	for _, tc := range []struct {
		name   string
		filter publicattendance.DayFilter
	}{
		{"a day that is not a date", publicattendance.DayFilter{ODDate: "not-a-date"}},
		{"a category that is not a uuid", publicattendance.DayFilter{ODDate: boardDay, CategoryID: "nope"}},
	} {
		if _, err := service.Day(ctx, tc.filter); err == nil {
			t.Errorf("[P0] %s was accepted. A malformed filter must be a typed refusal — an empty board would "+
				"read to a visitor as 'nobody was marked'", tc.name)
		}
	}
}

// boardIndexOf is where a roll number sits on the board.
func boardIndexOf(t *testing.T, board publicattendance.DayAttendance, rollNo string) int {
	t.Helper()
	for index, student := range board.Students {
		if student.RollNo == rollNo {
			return index
		}
	}
	t.Fatalf("[P0] %q is not on the board at all, across %d students", rollNo, len(board.Students))
	return -1
}

// boardMarksFor pulls one student's marks out of the board by roll number.
func boardMarksFor(t *testing.T, board publicattendance.DayAttendance, rollNo string) []publicattendance.DayMark {
	t.Helper()
	for _, student := range board.Students {
		if student.RollNo == rollNo {
			return student.Marks
		}
	}
	return nil
}

// seedBoardEvent gives a board day an event of its own.
//
// od_records carries a CHECK that od_date falls inside its event's range, so a
// day these tests invent needs an event that covers it. Each call makes a fresh
// one: the suite shares a database and never cleans up, so reusing a fixed name
// would collide with the previous run.
func seedBoardEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, day string) publicEventFixture {
	t.Helper()
	return seedPublicEvent(t, ctx, pool, "Public Board "+day+" "+uuid.NewString()[:8], day)
}

// seedBoardStudent adds a student in the fixture's own branch, so the board has
// more than one row to order.
func seedBoardStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, rollNo string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO students (id, roll_no, name, branch_id, section, batch)
		SELECT $1, $2, 'Board Order Student', b.id, 'A', '2024'
		FROM branches b
		ORDER BY b.code
		LIMIT 1`, id, rollNo); err != nil {
		t.Fatalf("seed board student %s: %v", rollNo, err)
	}
	return id
}
