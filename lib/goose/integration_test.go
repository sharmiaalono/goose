package goose_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	_ "modernc.org/sqlite"

	"github.com/sharmiaalono/goose/lib/goose"
	"github.com/sharmiaalono/goose/lib/goose/dialect"
)

var testDrivers = []string{"sqlite", "sqlite3"}

// TestIntegrationPragmaJournalModeFailsInTransaction verifies that journal_mode in transaction fails with explicit error and does not update version.
func TestIntegrationPragmaJournalModeFailsInTransaction(t *testing.T) {
	for _, driver := range testDrivers {
		t.Run(driver, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test_journal_fail.db")
			db, err := sql.Open(driver, dbPath)
			if err != nil {
				t.Fatalf("failed opening sqlite database: %v", err)
			}
			defer db.Close()

			ctx := context.Background()
			sqliteDialect := dialect.NewSqlite3()
			runner := goose.NewRunner(db, sqliteDialect)

			if err := runner.Init(ctx); err != nil {
				t.Fatalf("failed initializing version table: %v", err)
			}

			migrationScript := `-- +goose Up
PRAGMA journal_mode = WAL;

-- +goose Down
PRAGMA journal_mode = DELETE;
`
			parsed, err := goose.ParseMigration(strings.NewReader(migrationScript))
			if err != nil {
				t.Fatalf("failed parsing migration: %v", err)
			}

			err = runner.Apply(ctx, 1, parsed, goose.DirectionUp)
			if err == nil {
				t.Fatalf("expected error running journal_mode in transaction, got nil")
			}
			if !strings.Contains(err.Error(), "-- +goose NO TRANSACTION") {
				t.Fatalf("expected error to instruct user about '-- +goose NO TRANSACTION', got: %v", err)
			}

			version, err := runner.Version(ctx)
			if err != nil {
				t.Fatalf("failed retrieving version: %v", err)
			}
			if version != 0 {
				t.Fatalf("expected version to remain 0 after rejected migration, got %d", version)
			}
		})
	}
}

// TestIntegrationPragmaJournalModeSuccessWithNoTransaction verifies journal_mode success with NO TRANSACTION and validates mode post-migration.
func TestIntegrationPragmaJournalModeSuccessWithNoTransaction(t *testing.T) {
	for _, driver := range testDrivers {
		t.Run(driver, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test_journal_success.db")
			db, err := sql.Open(driver, dbPath)
			if err != nil {
				t.Fatalf("failed opening sqlite database: %v", err)
			}
			defer db.Close()

			ctx := context.Background()
			sqliteDialect := dialect.NewSqlite3()
			runner := goose.NewRunner(db, sqliteDialect)

			if err := runner.Init(ctx); err != nil {
				t.Fatalf("failed initializing version table: %v", err)
			}

			migrationScript := `-- +goose NO TRANSACTION
-- +goose Up
PRAGMA journal_mode = WAL;

-- +goose Down
PRAGMA journal_mode = DELETE;
`
			parsed, err := goose.ParseMigration(strings.NewReader(migrationScript))
			if err != nil {
				t.Fatalf("failed parsing migration: %v", err)
			}

			if err := runner.Apply(ctx, 1, parsed, goose.DirectionUp); err != nil {
				t.Fatalf("unexpected error applying migration: %v", err)
			}

			version, err := runner.Version(ctx)
			if err != nil {
				t.Fatalf("failed retrieving version: %v", err)
			}
			if version != 1 {
				t.Fatalf("expected version 1, got %d", version)
			}

			var activeJournalMode string
			if err := db.QueryRowContext(ctx, "PRAGMA journal_mode;").Scan(&activeJournalMode); err != nil {
				t.Fatalf("failed querying active journal mode: %v", err)
			}
			if !strings.EqualFold(activeJournalMode, "wal") {
				t.Fatalf("expected journal mode 'wal', got %q", activeJournalMode)
			}
		})
	}
}

// TestIntegrationForeignKeysHandling verifies warnings and constraint enforcement across downstream migration steps.
func TestIntegrationForeignKeysHandling(t *testing.T) {
	for _, driver := range testDrivers {
		t.Run(driver, func(t *testing.T) {
			var dsn string
			tmpDir := t.TempDir()
			dbFile := filepath.Join(tmpDir, "test_fk.db")

			if driver == "sqlite3" {
				dsn = fmt.Sprintf("file:%s?_pragma=foreign_keys(ON)", dbFile)
			} else {
				dsn = fmt.Sprintf("file:%s?_foreign_keys=1", dbFile)
			}

			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatalf("failed opening sqlite database: %v", err)
			}
			defer db.Close()

			ctx := context.Background()
			sqliteDialect := dialect.NewSqlite3()
			runner := goose.NewRunner(db, sqliteDialect)

			var capturedWarnings []string
			runner.SetWarningHandler(func(w string) {
				capturedWarnings = append(capturedWarnings, w)
			})

			if err := runner.Init(ctx); err != nil {
				t.Fatalf("failed initializing version table: %v", err)
			}

			migrationScript := `-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = ON;
CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE books (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL REFERENCES authors(id));

-- +goose Down
DROP TABLE books;
DROP TABLE authors;
`
			parsed, err := goose.ParseMigration(strings.NewReader(migrationScript))
			if err != nil {
				t.Fatalf("failed parsing migration: %v", err)
			}

			if err := runner.Apply(ctx, 1, parsed, goose.DirectionUp); err != nil {
				t.Fatalf("failed applying foreign keys migration: %v", err)
			}

			if len(capturedWarnings) == 0 {
				t.Fatalf("expected connection warning regarding foreign_keys DSN, got none")
			}

			hasExpectedWarning := false
			for _, w := range capturedWarnings {
				if strings.Contains(w, "foreign_keys is connection-scoped") {
					hasExpectedWarning = true
				}
			}
			if !hasExpectedWarning {
				t.Fatalf("expected warning regarding connection-scoped foreign_keys, got: %v", capturedWarnings)
			}

			_, err = db.ExecContext(ctx, "INSERT INTO books (id, author_id) VALUES (1, 999);")
			if err == nil {
				t.Fatalf("expected foreign key constraint violation error, got nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
				t.Fatalf("expected foreign key error, got: %v", err)
			}
		})
	}
}

// TestIntegrationStandardMigrationsCompatibility verifies backward compatibility for migrations without transactional PRAGMAs.
func TestIntegrationStandardMigrationsCompatibility(t *testing.T) {
	for _, driver := range testDrivers {
		t.Run(driver, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test_standard.db")
			db, err := sql.Open(driver, dbPath)
			if err != nil {
				t.Fatalf("failed opening sqlite database: %v", err)
			}
			defer db.Close()

			ctx := context.Background()
			sqliteDialect := dialect.NewSqlite3()
			runner := goose.NewRunner(db, sqliteDialect)

			if err := runner.Init(ctx); err != nil {
				t.Fatalf("failed initializing version table: %v", err)
			}

			migrationScript := `-- +goose Up
CREATE TABLE accounts (id INTEGER PRIMARY KEY, balance NUMERIC);
INSERT INTO accounts (id, balance) VALUES (1, 100.50);

-- +goose Down
DROP TABLE accounts;
`
			parsed, err := goose.ParseMigration(strings.NewReader(migrationScript))
			if err != nil {
				t.Fatalf("failed parsing migration: %v", err)
			}

			if err := runner.Apply(ctx, 1, parsed, goose.DirectionUp); err != nil {
				t.Fatalf("failed applying standard migration: %v", err)
			}

			version, err := runner.Version(ctx)
			if err != nil {
				t.Fatalf("failed retrieving version: %v", err)
			}
			if version != 1 {
				t.Fatalf("expected version 1, got %d", version)
			}

			var balance float64
			if err := db.QueryRowContext(ctx, "SELECT balance FROM accounts WHERE id = 1;").Scan(&balance); err != nil {
				t.Fatalf("failed querying balance: %v", err)
			}
			if balance != 100.50 {
				t.Fatalf("expected balance 100.50, got %f", balance)
			}
		})
	}
}

func init() {
	_ = os.Setenv("GOOSE_TEST", "1")
}
