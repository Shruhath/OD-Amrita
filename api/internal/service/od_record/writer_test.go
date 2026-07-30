package odrecord

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapPgErrorMapsEventForeignKeyRaceToEventNotFound(t *testing.T) {
	err := mapPgError(&pgconn.PgError{
		Code:           sqlstateForeignKeyViolation,
		ConstraintName: "od_records_event_id_fkey",
	})
	if !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("error = %v, want ErrEventNotFound", err)
	}
}
