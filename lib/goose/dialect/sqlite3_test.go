package dialect_test

import (
	"strings"
	"testing"

	"sharmiaalono/goose/lib/goose/dialect"
)

func TestValidateSQLiteStatement_Transactional(t *testing.T) {
	tests := []struct {
		name      string
		stmt      string
		inTx      bool
		shouldErr bool
	}{
		{
			name:      "journal_mode in transaction",
			stmt:      "PRAGMA journal_mode = WAL;",
			inTx:      true,
			shouldErr: true,
		},
		{
			name:      "journal_mode outside transaction",
			stmt:      "PRAGMA journal_mode = WAL;",
			inTx:      false,
			shouldErr: false,
		},
		{
			name:      "foreign_keys in transaction",
			stmt:      "PRAGMA foreign_keys = ON;",
			inTx:      true,
			shouldErr: true,
		},
		{
			name:      "foreign_keys function syntax in transaction",
			stmt:      "PRAGMA foreign_keys(1);",
			inTx:      true,
			shouldErr: true,
		},
		{
			name:      "schema qualified pragma in transaction",
			stmt:      "PRAGMA main.auto_vacuum = FULL;",
			inTx:      true,
			shouldErr: true,
		},
		{
			name:      "vacuum in transaction",
			stmt:      "VACUUM;",
			inTx:      true,
			shouldErr: true,
		},
		{
			name:      "standard table creation in transaction",
			stmt:      "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);",
			inTx:      true,
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := dialect.ValidateSQLiteStatement(tt.stmt, tt.inTx)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("expected error for statement %q, got nil", tt.stmt)
				}
				if !strings.Contains(err.Error(), "-- +goose NO TRANSACTION") {
					t.Errorf("expected error to mention '-- +goose NO TRANSACTION', got: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for statement %q: %v", tt.stmt, err)
				}
			}
		})
	}
}

func TestValidateSQLiteMigration(t *testing.T) {
	stmts := []string{
		"CREATE TABLE accounts (id INTEGER PRIMARY KEY);",
		"PRAGMA journal_mode = WAL;",
	}

	err := dialect.ValidateSQLiteMigration(stmts, true)
	if err == nil {
		t.Fatal("expected validation failure for migration containing PRAGMA journal_mode in transaction")
	}

	err = dialect.ValidateSQLiteMigration(stmts, false)
	if err != nil {
		t.Fatalf("expected validation success for non-transactional migration, got: %v", err)
	}
}
