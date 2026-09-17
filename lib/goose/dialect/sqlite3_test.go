package dialect_test

import (
	"strings"
	"testing"

	"github.com/sharmiaalono/goose/lib/goose/dialect"
)

// TestSqlite3ValidateStatements verifies PRAGMA restriction enforcement inside transactions.
func TestSqlite3ValidateStatements(t *testing.T) {
	d := dialect.NewSqlite3()

	testCases := []struct {
		name          string
		statements    []string
		inTransaction bool
		expectErr     bool
		errContains   string
	}{
		{
			name:          "Journal mode inside transaction",
			statements:    []string{"PRAGMA journal_mode = WAL;"},
			inTransaction: true,
			expectErr:     true,
			errContains:   "-- +goose NO TRANSACTION",
		},
		{
			name:          "Journal mode without transaction",
			statements:    []string{"PRAGMA journal_mode = WAL;"},
			inTransaction: false,
			expectErr:     false,
		},
		{
			name:          "Vacuum inside transaction",
			statements:    []string{"PRAGMA vacuum;"},
			inTransaction: true,
			expectErr:     true,
			errContains:   "-- +goose NO TRANSACTION",
		},
		{
			name:          "Auto vacuum inside transaction",
			statements:    []string{"PRAGMA auto_vacuum = FULL;"},
			inTransaction: true,
			expectErr:     true,
			errContains:   "-- +goose NO TRANSACTION",
		},
		{
			name:          "Standard DDL inside transaction",
			statements:    []string{"CREATE TABLE users (id INTEGER PRIMARY KEY);"},
			inTransaction: true,
			expectErr:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := d.ValidateStatements(tc.statements, tc.inTransaction)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, received nil")
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error to contain %q, received %q", tc.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, received %v", err)
				}
			}
		})
	}
}

// TestSqlite3ConnectionWarnings validates connection-scoped DSN notices.
func TestSqlite3ConnectionWarnings(t *testing.T) {
	d := dialect.NewSqlite3()

	statements := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA synchronous = NORMAL;",
		"CREATE TABLE items (id INTEGER);",
	}

	warnings := d.GetConnectionWarnings(statements)
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, received %d: %v", len(warnings), warnings)
	}

	hasFKWarning := false
	for _, w := range warnings {
		if strings.Contains(w, "_pragma=foreign_keys(ON)") && strings.Contains(w, "_foreign_keys=1") {
			hasFKWarning = true
		}
	}
	if !hasFKWarning {
		t.Fatalf("expected foreign_keys DSN warning in %v", warnings)
	}
}
