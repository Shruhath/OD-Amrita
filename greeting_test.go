package main

import "testing"

func TestGreeting(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "default greeting", want: "Hello World"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Greeting(); got != tt.want {
				t.Errorf("Greeting() = %q, want %q", got, tt.want)
			}
		})
	}
}
