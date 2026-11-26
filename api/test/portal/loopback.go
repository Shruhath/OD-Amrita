package main

import (
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// defaultListenHost keeps the harness off every non-loopback interface. It
// answers to fixed bearer tokens (see the *Token constants in main.go), so
// anything that can reach the port can act as an administrator.
const defaultListenHost = "127.0.0.1"

// requireLoopbackDSN refuses a DSN whose host is anything other than the local
// machine. The harness DELETEs fixture-shaped students and their records on
// every start and reads the same variable names as the production API, so a
// shell that still has a deployed database's DSN exported must not be able to
// run it.
//
// Accepted hosts: "localhost", any loopback IP literal (127.0.0.0/8, ::1) and a
// unix-socket directory (an absolute path). Every host a multi-host DSN lists
// is checked, including the implicit default pgconn picks when none is given.
//
// The DSN itself never appears in the returned error: it carries a password.
func requireLoopbackDSN(envName, dsn string) error {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("%s is not a valid Postgres connection string; the browser-test harness only runs against a local database", envName)
	}
	hosts := []string{cfg.Host}
	for _, fb := range cfg.Fallbacks {
		hosts = append(hosts, fb.Host)
	}
	for _, host := range hosts {
		if !isLoopbackHost(host) {
			return fmt.Errorf("%s points at host %q, which is not loopback; the browser-test harness deletes and reseeds fixture data, so it only runs against localhost, 127.0.0.1, ::1 or a unix socket", envName, host)
		}
	}
	return nil
}

// isLoopbackHost reports whether host names the local machine without any
// name resolution: a DNS lookup could be answered by anything.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.HasPrefix(host, "/") {
		return true // unix-socket directory
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
