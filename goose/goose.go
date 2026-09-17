package goose

import (
	"database/sql"
	"fmt"
	"log"
	"regexp" // Added for PRAGMA detection
	"sort"
	"time"
)

// MigrationType represents the type of a migration (SQL or Go).
type MigrationType int

const (
	SQLMigration MigrationType = iota
	GoMigration
)

// Migration represents a single database migration.
type Migration struct {
	Version    int64
	SourceFile string
	Type       MigrationType
	Source     interface{} // *SQLMigration or *GoMigration
}

// SortMigrations sorts migrations by version.
func SortMigrations(migrations []*Migration) {
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
}

// Goose represents the goose migration tool instance.
type Goose struct {
	db     *sql.DB
	driver Driver
	dir    string
}

// NewGoose creates a new Goose instance.
func NewGoose(db *sql.DB, driver Driver, dir string) *Goose {
	return &Goose{
		db:     db,
		driver: driver,
		dir:    dir,
	}
}

// Up runs all pending migrations.
func (g *Goose) Up() error {
	currentVersion, err := g.driver.GetCurrentVersion(g.db)
	if err != nil {
		return fmt.Errorf("failed to get current DB version: %w", err)
	}

	migrations, err := CollectMigrations(g.dir, currentVersion)
	if err != nil {
		return fmt.Errorf("failed to collect migrations: %w", err)
	}

	if len(migrations) == 0 {
		log.Println("No new migrations to apply.")
		return nil
	}

	log.Printf("Applying %d new migrations...", len(migrations))

	for _, m := range migrations {
		log.Printf("Applying migration %d: %s", m.Version, m.SourceFile)
		if err := g.runMigration(m, currentVersion, m.Version, true); err != nil {
			return fmt.Errorf("failed to apply migration %d (%s): %w", m.Version, m.SourceFile, err)
		}
	}

	log.Println("Migrations applied successfully.")
	return nil
}

// runMigration applies a single migration.
func (g *Goose) runMigration(m *Migration, currentVersion int64, targetVersion int64, verbose bool) (err error) {
	// Determine if the migration should run in a transaction.
	// For SQL migrations, this is determined by the presence of `--- +goose NoTransaction` directive.
	// For Go migrations, this is determined by the `NoTx` field of the GoMigration struct.
	runInTransaction := true
	if m.Type == SQLMigration {
		sqlMigration, ok := m.Source.(*SQLMigration)
		if !ok {
			return fmt.Errorf("expected SQLMigration source for SQL migration type")
		}
		runInTransaction = !sqlMigration.NoTx
	} else if m.Type == GoMigration {
		goMigration, ok := m.Source.(*GoMigration)
		if !ok {
			return fmt.Errorf("expected GoMigration source for Go migration type")
		}
		runInTransaction = !goMigration.NoTx
	}

	// New logic for SQLite PRAGMA warning
	if m.Type == SQLMigration && runInTransaction && g.driver.Name() == "sqlite3" {
		sqlMigration, _ := m.Source.(*SQLMigration)
		if containsPragma(sqlMigration.RawContent) {
			log.Printf("WARNING: Migration '%s' (%s) contains PRAGMA statements and is running within a transaction on SQLite. "+
				"Many PRAGMA statements (e.g., journal_mode, foreign_keys) are silently ignored or rejected inside transactions by SQLite. "+
				"Consider adding '--- +goose NoTransaction' to the top of your SQL migration file if this is unintended.", m.SourceFile, m.SourceFile)
		}
	}

	// Execute the migration
	if runInTransaction {
		tx, err := g.db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer func() {
			if r := recover(); r != nil {
				_ = tx.Rollback()
				panic(r)
			}
			if err != nil {
				_ = tx.Rollback()
			}
		}()

		if m.Type == SQLMigration {
			sqlMigration := m.Source.(*SQLMigration)
			for _, stmt := range sqlMigration.Statements {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("failed to execute statement in transaction: %w", err)
				}
			}
		} else if m.Type == GoMigration {
			goMigration := m.Source.(*GoMigration)
			if err := goMigration.UpFn(tx); err != nil {
				return fmt.Errorf("failed to execute Go migration in transaction: %w", err)
			}
		}

		if err := g.driver.InsertVersion(tx, m.Version, true); err != nil {
			return fmt.Errorf("failed to insert version into DB: %w", err)
		}
		return tx.Commit()
	} else {
		// No transaction
		if m.Type == SQLMigration {
			sqlMigration := m.Source.(*SQLMigration)
			for _, stmt := range sqlMigration.Statements {
				if _, err := g.db.Exec(stmt); err != nil {
					return fmt.Errorf("failed to execute statement without transaction: %w", err)
				}
			}
		} else if m.Type == GoMigration {
			goMigration := m.Source.(*GoMigration)
			if err := goMigration.UpFn(g.db); err != nil {
				return fmt.Errorf("failed to execute Go migration without transaction: %w", err)
			}
		}

		if err := g.driver.InsertVersion(g.db, m.Version, true); err != nil {
			return fmt.Errorf("failed to insert version into DB: %w", err)
		}
		return nil
	}
}

// containsPragma checks if the SQL content contains PRAGMA statements.
// It uses a case-insensitive regex to find "PRAGMA" followed by one or more whitespace characters.
func containsPragma(sqlContent string) bool {
	re := regexp.MustCompile(`(?i)PRAGMA\s+`)
	return re.MatchString(sqlContent)
}

// InitDB ensures the goose_db_version table exists.
func (g *Goose) InitDB() error {
	return g.driver.CreateVersionTable(g.db)
}

// GetCurrentVersion retrieves the current database version.
func (g *Goose) GetCurrentVersion() (int64, error) {
	return g.driver.GetCurrentVersion(g.db)
}

// Create creates a new migration file.
func (g *Goose) Create(name string, migrationType MigrationType) (string, error) {
	version := time.Now().Unix()
	filename := fmt.Sprintf("%d_%s", version, name)

	var content string
	switch migrationType {
	case SQLMigration:
		filename += ".sql"
		content = `-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';
-- +goose StatementEnd
`
	case GoMigration:
		filename += ".go"
		content = `package main

import (
	"database/sql"
	"fmt"
)

// Up is the UP migration function.
func Up(tx *sql.Tx) error {
	// This migration is run in a transaction.
	fmt.Println("Applying Go migration UP")
	return nil
}

// Down is the DOWN migration function.
func Down(tx *sql.Tx) error {
	// This migration is run in a transaction.
	fmt.Println("Applying Go migration DOWN")
	return nil
}
`
	default:
		return "", fmt.Errorf("unsupported migration type: %v", migrationType)
	}

	path := filepath.Join(g.dir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to create migration file: %w", err)
	}

	return path, nil
}
