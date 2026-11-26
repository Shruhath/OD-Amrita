package main

import (
	"strings"
	"testing"
)

func TestRequireLoopbackDSN(t *testing.T) {
	// PGHOST would otherwise fill in a host for the DSNs that omit one.
	t.Setenv("PGHOST", "")

	cases := []struct {
		name string
		dsn  string
		ok   bool
	}{
		{"localhost url", "postgres://od_runtime:pw@localhost:5432/od_amrita?sslmode=disable", true},
		{"ipv4 loopback url", "postgres://od_runtime:pw@127.0.0.1:5432/od_amrita", true},
		{"ipv6 loopback url", "postgres://od_runtime:pw@[::1]:5432/od_amrita", true},
		{"other 127/8 address", "postgres://u:pw@127.0.0.2/db", true},
		{"uppercase localhost", "postgres://u:pw@LOCALHOST/db", true},
		{"unix socket keyword form", "host=/var/run/postgresql user=od_runtime dbname=od_amrita", true},
		{"unix socket url form", "postgres://u:pw@/db?host=/tmp", true},
		{"keyword form loopback", "host=127.0.0.1 port=5432 user=u password=pw dbname=db", true},
		{"remote hostname", "postgres://u:pw@db.example.com:5432/db?sslmode=require", false},
		{"remote ip", "postgres://u:pw@10.0.0.5:5432/db", false},
		{"all interfaces", "postgres://u:pw@0.0.0.0:5432/db", false},
		{"name that merely starts with localhost", "postgres://u:pw@localhost.example.com/db", false},
		{"multi-host with one remote", "postgres://u:pw@localhost:5432,db.example.com:5432/db", false},
		{"keyword form remote", "host=db.example.com user=u password=pw dbname=db", false},
		{"unparseable", "postgres://u:pw@localhost:notaport/db", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireLoopbackDSN("DATABASE_URL_RUNTIME", tc.dsn)
			if tc.ok && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("want refused, got nil")
				}
				if !strings.Contains(err.Error(), "DATABASE_URL_RUNTIME") {
					t.Errorf("error must name the variable: %v", err)
				}
				if strings.Contains(err.Error(), "pw") {
					t.Errorf("error must not echo the password: %v", err)
				}
			}
		})
	}
}
