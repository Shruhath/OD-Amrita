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

	branchIDs := map[string]string{}
	inserted, updated, unchanged := 0, 0, 0
	for _, row := range rows {
		branchID, ok := branchIDs[row.Branch]
		if !ok {
			if err := tx.QueryRow(ctx, `
				SELECT id::text
				FROM branches
				WHERE lower(code) = lower($1)
				ORDER BY id
				LIMIT 1
			`, row.Branch).Scan(&branchID); err != nil {
				panic(fmt.Sprintf("resolve branch %s: %v", row.Branch, err))
			}
			branchIDs[row.Branch] = branchID
		}

		var studentID string
		var wasInserted bool
		err := tx.QueryRow(ctx, `
			WITH upsert AS (
				INSERT INTO students (roll_no, name, branch_id, section, batch)
				VALUES ($1, $2, $3::uuid, $4, $5)
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
				RETURNING id::text, (xmax = 0) AS inserted
			)
			SELECT id, inserted
			FROM upsert
		`, row.Roll, row.Name, branchID, row.Section, row.Batch).Scan(&studentID, &wasInserted)
		if err == pgx.ErrNoRows {
			unchanged++
			continue
		}
		if err != nil {
			panic(fmt.Sprintf("upsert %s: %v", row.Roll, err))
		}
		if wasInserted {
			inserted++
		} else {
			updated++
		}
	}

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
