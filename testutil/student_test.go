package testutil

import "testing"

func TestNewStudent_Defaults(t *testing.T) {
	s := NewStudent()

	if s.Section != "CSE-A" {
		t.Errorf("Section = %q, want default %q", s.Section, "CSE-A")
	}
	if s.ID == "" {
		t.Error("ID should not be empty")
	}
}

func TestNewStudent_Overrides(t *testing.T) {
	s := NewStudent(func(s *Student) {
		s.Section = "ECE-B"
		s.Name = "Jane Doe"
	})

	if s.Section != "ECE-B" {
		t.Errorf("Section = %q, want %q", s.Section, "ECE-B")
	}
	if s.Name != "Jane Doe" {
		t.Errorf("Name = %q, want %q", s.Name, "Jane Doe")
	}
}

func TestNewStudent_Uniqueness(t *testing.T) {
	a := NewStudent()
	b := NewStudent()

	if a.ID == b.ID {
		t.Error("expected distinct IDs across factory calls")
	}
	if a.Email == b.Email {
		t.Error("expected distinct emails across factory calls")
	}
}
