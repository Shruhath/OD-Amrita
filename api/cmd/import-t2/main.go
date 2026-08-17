package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type rosterRow struct {
	Roll    string
	Name    string
	Branch  string
	Section string
	Batch   string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn := os.Getenv("DATABASE_URL_RUNTIME")
	if dsn == "" {
		panic("DATABASE_URL_RUNTIME is not set")
	}

	rows, err := readRows("../t2.csv")
	if err != nil {
		panic(err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		panic(err)
	}
	defer tx.Rollback(ctx)

	// Branches first, in ONE query for the whole file rather than one per distinct
	// code. There are a handful of branches and the map already cached them, so
	// this is a small win — but it is the same round-trip-per-row shape, and
	// leaving one instance of it here is how it comes back.
	branchCodes := make([]string, 0, len(rows))
	seenCodes := map[string]struct{}{}
	for _, row := range rows {
		if _, seen := seenCodes[row.Branch]; !seen {
			seenCodes[row.Branch] = struct{}{}
			branchCodes = append(branchCodes, row.Branch)
		}
	}
	branchIDs := map[string]string{}
	branchRows, err := tx.Query(ctx, `
		SELECT lower(code), id::text
		FROM branches
		WHERE lower(code) = ANY($1::text[])
	`, lowered(branchCodes))
	if err != nil {
		panic(fmt.Sprintf("resolve branches: %v", err))
	}
	for branchRows.Next() {
		var code, id string
		if err := branchRows.Scan(&code, &id); err != nil {
			branchRows.Close()
			panic(fmt.Sprintf("scan branch: %v", err))
		}
		branchIDs[code] = id
	}
	branchRows.Close()
	if err := branchRows.Err(); err != nil {
		panic(fmt.Sprintf("resolve branches: %v", err))
	}

	// Build the five positional arrays. Position i of each is one student, so
	// they must stay the same length and in the same order.
	rollNos := make([]string, 0, len(rows))
	names := make([]string, 0, len(rows))
	rowBranchIDs := make([]string, 0, len(rows))
	sections := make([]string, 0, len(rows))
	batches := make([]string, 0, len(rows))
	for _, row := range rows {
		branchID, ok := branchIDs[strings.ToLower(row.Branch)]
		if !ok {
			panic(fmt.Sprintf("resolve branch %s: no such branch", row.Branch))
		}
		rollNos = append(rollNos, row.Roll)
		names = append(names, row.Name)
		rowBranchIDs = append(rowBranchIDs, branchID)
		sections = append(sections, row.Section)
		batches = append(batches, row.Batch)
	}

	// THE WHOLE FILE IN ONE STATEMENT. This was one round trip per student, which
	// against the production database — on another continent, roughly half a
	// second away — made a 2,000-row import a sixteen-minute one that held this
	// transaction open throughout.
	//
	// Deliberately the same shape as the UpsertStudentsBulk query the API's own
	// roster import uses; this tool is a one-off, so it carries its SQL inline
	// rather than through sqlc, but the semantics must not diverge.
	//
	// An unchanged row returns nothing (the IS DISTINCT FROM guard), so unchanged
	// is counted as the gap between what was sent and what came back.
	inserted, updated := 0, 0
	upsertRows, err := tx.Query(ctx, `
		WITH input AS (
			SELECT
				roll.roll_no, nm.name, br.branch_id, sec.section, bat.batch
			FROM unnest($1::text[]) WITH ORDINALITY AS roll(roll_no, ord)
			JOIN unnest($2::text[]) WITH ORDINALITY AS nm(name, ord)      ON nm.ord = roll.ord
			JOIN unnest($3::uuid[]) WITH ORDINALITY AS br(branch_id, ord) ON br.ord = roll.ord
			JOIN unnest($4::text[]) WITH ORDINALITY AS sec(section, ord)  ON sec.ord = roll.ord
			JOIN unnest($5::text[]) WITH ORDINALITY AS bat(batch, ord)    ON bat.ord = roll.ord
		)
		INSERT INTO students (roll_no, name, branch_id, section, batch)
		SELECT input.roll_no, input.name, input.branch_id, input.section, input.batch
		FROM input
		ON CONFLICT (roll_no) DO UPDATE
		SET name = EXCLUDED.name,
			branch_id = EXCLUDED.branch_id,
			section = EXCLUDED.section,
			batch = EXCLUDED.batch,
			updated_at = now()
		WHERE students.name IS DISTINCT FROM EXCLUDED.name
		   OR students.branch_id IS DISTINCT FROM EXCLUDED.branch_id
		   OR students.section IS DISTINCT FROM EXCLUDED.section
		   OR students.batch IS DISTINCT FROM EXCLUDED.batch
		RETURNING (xmax = 0) AS inserted
	`, rollNos, names, rowBranchIDs, sections, batches)
	if err != nil {
		panic(fmt.Sprintf("bulk upsert: %v", err))
	}
	for upsertRows.Next() {
		var wasInserted bool
		if err := upsertRows.Scan(&wasInserted); err != nil {
			upsertRows.Close()
			panic(fmt.Sprintf("scan upsert: %v", err))
		}
		if wasInserted {
			inserted++
		} else {
			updated++
		}
	}
	upsertRows.Close()
	if err := upsertRows.Err(); err != nil {
		panic(fmt.Sprintf("bulk upsert: %v", err))
	}
	unchanged := len(rows) - inserted - updated

	var databaseCount int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM students`).Scan(&databaseCount); err != nil {
		panic(err)
	}

	var importID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO student_roster_imports (
			source_filename,
			total_rows,
			inserted_rows,
			updated_rows,
			unchanged_rows,
			invalid_rows,
			database_count,
			actor_email
		)
		VALUES ('t2.csv', $1, $2, $3, $4, 0, $5, 'side-import@local')
		RETURNING id::text
	`, len(rows), inserted, updated, unchanged, databaseCount).Scan(&importID); err != nil {
		panic(err)
	}

	payload, _ := json.Marshal(map[string]any{
		"source_filename": "t2.csv",
		"total_rows":      len(rows),
		"valid_rows":      len(rows),
		"inserted_rows":   inserted,
		"updated_rows":    updated,
		"unchanged_rows":  unchanged,
		"invalid_rows":    0,
		"database_count":  databaseCount,
	})
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (action, actor_account, actor_role, target_table, target_id, payload)
		VALUES ('student_roster.imported', 'side-import@local', 'admin', 'student_roster_imports', $1, $2::jsonb)
	`, importID, string(payload)); err != nil {
		panic(err)
	}

	if err := tx.Commit(ctx); err != nil {
		panic(err)
	}

	fmt.Printf("import_id=%s total=%d inserted=%d updated=%d unchanged=%d database_count=%d\n", importID, len(rows), inserted, updated, unchanged, databaseCount)
	for _, row := range rows {
		fmt.Printf("%s | %s | %s | %s | %s\n", row.Roll, row.Name, row.Branch, row.Section, row.Batch)
	}
}

func readRows(path string) ([]rosterRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, err
	}

	rows := make([]rosterRow, 0, len(records))
	for index, record := range records {
		if len(record) != 5 {
			return nil, fmt.Errorf("row %d has %d fields, want 5", index+1, len(record))
		}
		row := rosterRow{
			Roll:    strings.ToUpper(strings.TrimSpace(record[0])),
			Name:    strings.TrimSpace(record[1]),
			Branch:  strings.ToUpper(strings.TrimSpace(record[2])),
			Section: strings.TrimSpace(record[3]),
			Batch:   strings.TrimSpace(record[4]),
		}
		if row.Roll == "" || row.Name == "" || row.Branch == "" || row.Section == "" || row.Batch == "" {
			return nil, fmt.Errorf("row %d has a blank required field", index+1)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// lowered returns the codes lowercased, matching the branches lookup's
// lower(code) comparison so the map key and the query agree.
func lowered(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.ToLower(value))
	}
	return out
}
