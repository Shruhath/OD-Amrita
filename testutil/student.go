// Package testutil holds test-only helpers shared across the OD-Amrita
// backend: data factories, fixtures, and (later) DB/HTTP test setup.
package testutil

import (
	"fmt"
	"math/rand"
	"time"
)

// Student is a lightweight test-data shape mirroring the eventual
// students table; it deliberately does not import any app package so
// it has zero coupling to the domain model that hasn't been built yet.
type Student struct {
	ID        string
	Email     string
	Name      string
	Section   string
	CreatedAt time.Time
}

// NewStudent returns a Student populated with unique defaults suitable
// for parallel test runs. Pass overrides to customize only the fields
// that matter for a given test, e.g. NewStudent(func(s *Student) { s.Section = "ECE-B" }).
func NewStudent(overrides ...func(*Student)) *Student {
	id := uniqueSuffix()
	s := &Student{
		ID:        id,
		Email:     fmt.Sprintf("student-%s@amrita.edu", id),
		Name:      "Test Student",
		Section:   "CSE-A",
		CreatedAt: time.Now(),
	}
	for _, o := range overrides {
		o(s)
	}
	return s
}

func uniqueSuffix() string {
	return fmt.Sprintf("%d%04d", time.Now().UnixNano(), rand.Intn(10000))
}
