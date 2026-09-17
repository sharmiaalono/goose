package dialect

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// QueryExecutor defines the query interface required for post-execution verification.
type QueryExecutor interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Dialect represents a database dialect abstraction for Goose migrations.
type Dialect interface {
	Name() string
	CreateTableSQL(tableName string) string
	InsertVersionSQL(tableName string) string
	DeleteVersionSQL(tableName string) string
	GetLatestVersionSQL(tableName string) string
	ValidateStatements(statements []string, inTransaction bool) error
	VerifyStatement(ctx context.Context, db QueryExecutor, statement string) error
	GetConnectionWarnings(statements []string) []string
}

// PragmaInfo contains parsed details of an SQLite PRAGMA statement.
type PragmaInfo struct {
	Raw           string
	Name          string
	Value         string
	HasAssignment bool
}

var pragmaRegex = regexp.MustCompile(`(?i)^\s*PRAGMA\s+(?:[a-zA-Z0-9_]+\.)?([a-zA-Z0-9_]+)(?:\s*(?:=\s*|\(\s*)([^);]+)\)?)?\s*;?\s*$`)

var nonTransactionalPragmas = map[string]struct{}{
	"journal_mode":        {},
	"vacuum":              {},
	"auto_vacuum":         {},
	"incremental_vacuum": {},
	"wal_checkpoint":      {},
	"locking_mode":        {},
	"legacy_file_format":  {},
}

var connectionScopedPragmas = map[string]string{
	"foreign_keys":     "PRAGMA foreign_keys is connection-scoped and does not persist across database/sql connection pools; configure driver DSN using '_pragma=foreign_keys(ON)' (for mattn/go-sqlite3) or '_foreign_keys=1' (for modernc.org/sqlite)",
	"synchronous":      "PRAGMA synchronous is connection-scoped; consider configuring via driver DSN parameter '_synchronous=...'",
	"busy_timeout":     "PRAGMA busy_timeout is connection-scoped; configure via driver DSN parameter '_busy_timeout=...'",
	"cache_size":       "PRAGMA cache_size is connection-scoped; configure via driver DSN parameter '_cache_size=...'",
	"temp_store":       "PRAGMA temp_store is connection-scoped; configure via driver DSN parameter '_temp_store=...'",
	"query_only":       "PRAGMA query_only is connection-scoped; configure via driver DSN parameter '_query_only=...'",
	"read_uncommitted": "PRAGMA read_uncommitted is connection-scoped; configure via driver DSN parameter '_read_uncommitted=...'",
}

// Sqlite3 implements the Dialect interface for SQLite.
type Sqlite3 struct{}

// NewSqlite3 constructs a new Sqlite3 dialect.
func NewSqlite3() *Sqlite3 {
	return &Sqlite3{}
}

// Name returns the dialect identifier.
func (s *Sqlite3) Name() string {
	return "sqlite3"
}

// CreateTableSQL returns SQL statement to create the goose version table.
func (s *Sqlite3) CreateTableSQL(tableName string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied INTEGER NOT NULL,
		tstamp TIMESTAMP DEFAULT (datetime('now'))
	);`, tableName)
}

// InsertVersionSQL returns SQL statement to record an applied migration.
func (s *Sqlite3) InsertVersionSQL(tableName string) string {
	return fmt.Sprintf(`INSERT INTO %s (version_id, is_applied) VALUES (?, ?);`, tableName)
}

// DeleteVersionSQL returns SQL statement to remove an unapplied migration.
func (s *Sqlite3) DeleteVersionSQL(tableName string) string {
	return fmt.Sprintf(`DELETE FROM %s WHERE version_id = ?;`, tableName)
}

// GetLatestVersionSQL returns SQL query to retrieve the current database version.
func (s *Sqlite3) GetLatestVersionSQL(tableName string) string {
	return fmt.Sprintf(`SELECT coalesce(max(version_id), 0) FROM %s;`, tableName)
}

// ParsePragma inspects a SQL statement and returns parsed PragmaInfo if it is a PRAGMA directive.
func ParsePragma(statement string) (*PragmaInfo, bool) {
	trimmed := strings.TrimSpace(statement)
	matches := pragmaRegex.FindStringSubmatch(trimmed)
	if len(matches) < 2 {
		return nil, false
	}

	name := strings.ToLower(strings.TrimSpace(matches[1]))
	val := ""
	hasAssign := false
	if len(matches) >= 3 && matches[2] != "" {
		val = strings.Trim(strings.TrimSpace(matches[2]), `'"`)
		hasAssign = true
	}

	return &PragmaInfo{
		Raw:           statement,
		Name:          name,
		Value:         val,
		HasAssignment: hasAssign,
	}, true
}

// ValidateStatements inspects statements and returns an error if restricted PRAGMAs are executed in a transaction.
func (s *Sqlite3) ValidateStatements(statements []string, inTransaction bool) error {
	for _, stmt := range statements {
		info, ok := ParsePragma(stmt)
		if !ok {
			continue
		}

		if inTransaction {
			if _, restricted := nonTransactionalPragmas[info.Name]; restricted {
				return fmt.Errorf("sqlite pragma %q cannot be executed within a transaction; use '-- +goose NO TRANSACTION' annotation in your migration file", info.Name)
			}
		}
	}
	return nil
}

// GetConnectionWarnings analyzes statements and returns notices for connection-scoped PRAGMAs.
func (s *Sqlite3) GetConnectionWarnings(statements []string) []string {
	var warnings []string
	for _, stmt := range statements {
		info, ok := ParsePragma(stmt)
		if !ok {
			continue
		}

		if warning, found := connectionScopedPragmas[info.Name]; found {
			warnings = append(warnings, fmt.Sprintf("warning: %s", warning))
		}
	}
	return warnings
}

// VerifyStatement verifies that a PRAGMA statement that returns state was applied accurately.
func (s *Sqlite3) VerifyStatement(ctx context.Context, db QueryExecutor, statement string) error {
	info, ok := ParsePragma(statement)
	if !ok || !info.HasAssignment {
		return nil
	}

	switch info.Name {
	case "journal_mode":
		var actualMode string
		query := "PRAGMA journal_mode;"
		if err := db.QueryRowContext(ctx, query).Scan(&actualMode); err != nil {
			return fmt.Errorf("failed to query pragma journal_mode: %w", err)
		}
		if !strings.EqualFold(actualMode, info.Value) {
			return fmt.Errorf("pragma verification failed for journal_mode: expected %q, got %q", strings.ToLower(info.Value), strings.ToLower(actualMode))
		}
	case "foreign_keys":
		var actualFK int
		query := "PRAGMA foreign_keys;"
		if err := db.QueryRowContext(ctx, query).Scan(&actualFK); err != nil {
			return fmt.Errorf("failed to query pragma foreign_keys: %w", err)
		}
		expectedFK := 0
		valLower := strings.ToLower(info.Value)
		if valLower == "on" || valLower == "1" || valLower == "yes" || valLower == "true" {
			expectedFK = 1
		}
		if actualFK != expectedFK {
			return fmt.Errorf("pragma verification failed for foreign_keys: expected %d, got %d", expectedFK, actualFK)
		}
	}

	return nil
}
