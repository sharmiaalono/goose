package goose_test

import (
	"strings"
	"testing"

	"github.com/sharmiaalono/goose/lib/goose"
	"github.com/sharmiaalono/goose/lib/goose/dialect"
)

// TestParserStandardMigration verifies parsing of standard Up/Down scripts.
func TestParserStandardMigration(t *testing.T) {
	script := `-- +goose Up
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT);

-- +goose Down
DROP TABLE posts;
DROP TABLE users;
`
	parsed, err := goose.ParseMigration(strings.NewReader(script))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if len(parsed.UpStatements) != 2 {
		t.Fatalf("expected 2 Up statements, got %d", len(parsed.UpStatements))
	}
	if len(parsed.DownStatements) != 2 {
		t.Fatalf("expected 2 Down statements, got %d", len(parsed.DownStatements))
	}
	if !parsed.UpUseTx || !parsed.DownUseTx {
		t.Fatalf("expected transactions enabled by default")
	}
}

// TestParserNoTransaction verifies detection of NO TRANSACTION directive.
func TestParserNoTransaction(t *testing.T) {
	script := `-- +goose NO TRANSACTION
-- +goose Up
PRAGMA journal_mode = WAL;

-- +goose Down
PRAGMA journal_mode = DELETE;
`
	parsed, err := goose.ParseMigration(strings.NewReader(script))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if parsed.UpUseTx {
		t.Fatalf("expected UpUseTx to be false")
	}
	if len(parsed.UpStatements) != 1 {
		t.Fatalf("expected 1 Up statement, got %d", len(parsed.UpStatements))
	}

	info, ok := dialect.ParsePragma(parsed.UpStatements[0])
	if !ok {
		t.Fatalf("expected statement to be recognized as PRAGMA")
	}
	if info.Name != "journal_mode" || info.Value != "WAL" {
		t.Fatalf("unexpected pragma parsed: %s=%s", info.Name, info.Value)
	}
}

// TestParserStatementBlocks verifies StatementBegin and StatementEnd parsing.
func TestParserStatementBlocks(t *testing.T) {
	script := `-- +goose Up
-- +goose StatementBegin
CREATE TRIGGER update_customer_address UPDATE OF address ON customers
  BEGIN
    UPDATE orders SET address = new.address WHERE customer_id = old.id;
  END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER update_customer_address;
`
	parsed, err := goose.ParseMigration(strings.NewReader(script))
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if len(parsed.UpStatements) != 1 {
		t.Fatalf("expected 1 compound Up statement, got %d", len(parsed.UpStatements))
	}
	if !strings.Contains(parsed.UpStatements[0], "CREATE TRIGGER") {
		t.Fatalf("expected trigger body in statement, got: %s", parsed.UpStatements[0])
	}
}
