package dialect

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// NonTransactionalPragmas lists PRAGMAs that cannot be run inside transactions
// or are no-ops when executed within an active transaction in SQLite.
var NonTransactionalPragmas = map[string]string{
	"journal_mode":   "PRAGMA journal_mode cannot be changed while a transaction is open.",
	"foreign_keys":   "PRAGMA foreign_keys is a no-op inside multi-statement transactions. Set via DSN (_pragma=foreign_keys(ON) for mattn/go-sqlite3 or _foreign_keys=1 for modernc.org/sqlite).",
	"auto_vacuum":    "PRAGMA auto_vacuum can only be changed outside a transaction prior to table creation or with VACUUM.",
	"vacuum":         "VACUUM cannot run within a transaction.",
	"page_size":      "PRAGMA page_size cannot be modified inside a transaction.",
	"locking_mode":   "PRAGMA locking_mode takes effect on the next transaction and cannot be set inside an active transaction.",
	"wal_checkpoint": "PRAGMA wal_checkpoint cannot run inside an active transaction.",
	"mmap_size":      "PRAGMA mmap_size cannot be changed inside an active transaction.",
	"synchronous":    "PRAGMA synchronous changes should be executed outside transactions to ensure persistent effect.",
}

var (
	pragmaRegex = regexp.MustCompile(`(?i)^\s*PRAGMA\s+(?:[a-zA-Z0-9_]+\.)?([a-zA-Z0-9_]+)(?:\s*=\s*|\s*\(\s*)([^;\)]+)?`)
	vacuumRegex = regexp.MustCompile(`(?i)^\s*VACUUM\b`)
)

// PragmaInfo contains parsed details of a PRAGMA statement.
type PragmaInfo struct {
	Name  string
	Value string
	Raw   string
}

// ParsePragma attempts to extract PRAGMA details from a SQL statement.
func ParsePragma(statement string) (*PragmaInfo, bool) {
	trimmed := strings.TrimSpace(statement)
	if vacuumRegex.MatchString(trimmed) {
		return &PragmaInfo{
			Name: "vacuum",
			Raw:  trimmed,
		}, true
	}

	matches := pragmaRegex.FindStringSubmatch(trimmed)
	if len(matches) < 2 {
		return nil, false
	}

	name := strings.ToLower(matches[1])
	val := ""
	if len(matches) >= 3 {
		val = strings.TrimSpace(matches[2])
		val = strings.Trim(val, "';\"")
	}

	return &PragmaInfo{
		Name:  name,
		Value: val,
		Raw:   trimmed,
	}, true
}

// ValidateSQLiteStatement checks if a statement contains a restricted PRAGMA inside a transaction.
func ValidateSQLiteStatement(stmt string, inTx bool) error {
	if !inTx {
		return nil
	}

	info, ok := ParsePragma(stmt)
	if !ok {
		return nil
	}

	if reason, restricted := NonTransactionalPragmas[info.Name]; restricted {
		return fmt.Errorf(
			"sqlite: statement %q cannot be executed safely inside a transaction: %s\nAction required: Add '-- +goose NO TRANSACTION' annotation to the migration file",
			strings.TrimSpace(stmt),
			reason,
		)
	}

	return nil
}

// ValidateSQLiteMigration checks all statements in a migration for PRAGMA issues.
func ValidateSQLiteMigration(statements []string, inTx bool) error {
	for _, stmt := range statements {
		if err := ValidateSQLiteStatement(stmt, inTx); err != nil {
			return err
		}
	}
	return nil
}

// Queryer represents standard database query interface.
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// VerifyPragma checks whether a SQLite PRAGMA value matches expected state post-execution.
func VerifyPragma(ctx context.Context, db Queryer, pragmaName string, expectedValue string) error {
	query := fmt.Sprintf("PRAGMA %s;", pragmaName)
	var actualValue string
	err := db.QueryRowContext(ctx, query).Scan(&actualValue)
	if err != nil {
		return fmt.Errorf("failed to query PRAGMA %s: %w", pragmaName, err)
	}

	if !strings.EqualFold(strings.TrimSpace(actualValue), strings.TrimSpace(expectedValue)) {
		return fmt.Errorf("PRAGMA %s verification failed: expected %q, got %q", pragmaName, expectedValue, actualValue)
	}
	return nil
}
