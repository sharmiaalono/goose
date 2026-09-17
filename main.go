package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/sharmiaalono/goose/lib/goose"
	"github.com/sharmiaalono/goose/lib/goose/dialect"
)

func main() {
	tmpDir, err := os.MkdirTemp("", "goose_run_*")
	if err != nil {
		log.Fatalf("failed creating temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbFile := filepath.Join(tmpDir, "demo.db")
	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		log.Fatalf("failed opening sqlite db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	sqliteDialect := dialect.NewSqlite3()
	runner := goose.NewRunner(db, sqliteDialect)
	runner.SetWarningHandler(func(w string) {
		fmt.Printf("[Notice] %s\n", w)
	})

	if err := runner.Init(ctx); err != nil {
		log.Fatalf("failed initializing version table: %v", err)
	}

	validMigration := `-- +goose NO TRANSACTION
-- +goose Up
PRAGMA journal_mode = WAL;
CREATE TABLE sample (id INTEGER PRIMARY KEY);

-- +goose Down
DROP TABLE sample;
`
	parsed, err := goose.ParseMigration(strings.NewReader(validMigration))
	if err != nil {
		log.Fatalf("failed parsing migration: %v", err)
	}

	if err := runner.Apply(ctx, 1, parsed, goose.DirectionUp); err != nil {
		log.Fatalf("failed applying migration: %v", err)
	}

	v, err := runner.Version(ctx)
	if err != nil {
		log.Fatalf("failed getting version: %v", err)
	}

	fmt.Printf("Successfully initialized and migrated to version: %d\n", v)
}
