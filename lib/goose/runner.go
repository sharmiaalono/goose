package goose

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sharmiaalono/goose/lib/goose/dialect"
)

// WarningHandler is a callback function invoked when a migration produces operational notices.
type WarningHandler func(warning string)

// Runner manages and executes database migrations against a target database.
type Runner struct {
	db             *sql.DB
	dialect        dialect.Dialect
	tableName      string
	warningHandler WarningHandler
}

// NewRunner constructs a new migration runner for a database connection and dialect.
func NewRunner(db *sql.DB, d dialect.Dialect) *Runner {
	return &Runner{
		db:        db,
		dialect:   d,
		tableName: "goose_db_version",
	}
}

// SetTableName overrides the default version tracking table name.
func (r *Runner) SetTableName(name string) {
	r.tableName = name
}

// SetWarningHandler registers a callback to receive migration warnings.
func (r *Runner) SetWarningHandler(handler WarningHandler) {
	r.warningHandler = handler
}

// Init creates the version tracking table if it does not already exist.
func (r *Runner) Init(ctx context.Context) error {
	query := r.dialect.CreateTableSQL(r.tableName)
	if _, err := r.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed creating version table: %w", err)
	}
	return nil
}

// Version retrieves the current database version.
func (r *Runner) Version(ctx context.Context) (int64, error) {
	query := r.dialect.GetLatestVersionSQL(r.tableName)
	var currentVersion int64
	if err := r.db.QueryRowContext(ctx, query).Scan(&currentVersion); err != nil {
		return 0, fmt.Errorf("failed retrieving current version: %w", err)
	}
	return currentVersion, nil
}

// Apply executes a parsed migration in the specified direction.
func (r *Runner) Apply(ctx context.Context, version int64, migration *ParsedMigration, direction Direction) error {
	var statements []string
	var useTx bool

	if direction == DirectionUp {
		statements = migration.UpStatements
		useTx = migration.UpUseTx
	} else {
		statements = migration.DownStatements
		useTx = migration.DownUseTx
	}

	if err := r.dialect.ValidateStatements(statements, useTx); err != nil {
		return fmt.Errorf("migration validation failed: %w", err)
	}

	warnings := r.dialect.GetConnectionWarnings(statements)
	if r.warningHandler != nil {
		for _, w := range warnings {
			r.warningHandler(w)
		}
	}

	if useTx {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed beginning transaction: %w", err)
		}

		for _, stmt := range statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed executing statement %q: %w", stmt, err)
			}
		}

		if direction == DirectionUp {
			insertSQL := r.dialect.InsertVersionSQL(r.tableName)
			if _, err := tx.ExecContext(ctx, insertSQL, version, 1); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed recording migration version: %w", err)
			}
		} else {
			deleteSQL := r.dialect.DeleteVersionSQL(r.tableName)
			if _, err := tx.ExecContext(ctx, deleteSQL, version); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("failed removing migration version: %w", err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed committing transaction: %w", err)
		}

		return nil
	}

	for _, stmt := range statements {
		if _, err := r.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed executing statement %q: %w", stmt, err)
		}

		if err := r.dialect.VerifyStatement(ctx, r.db, stmt); err != nil {
			return fmt.Errorf("post-execution verification failed: %w", err)
		}
	}

	if direction == DirectionUp {
		insertSQL := r.dialect.InsertVersionSQL(r.tableName)
		if _, err := r.db.ExecContext(ctx, insertSQL, version, 1); err != nil {
			return fmt.Errorf("failed recording migration version: %w", err)
		}
	} else {
		deleteSQL := r.dialect.DeleteVersionSQL(r.tableName)
		if _, err := r.db.ExecContext(ctx, deleteSQL, version); err != nil {
			return fmt.Errorf("failed removing migration version: %w", err)
		}
	}

	return nil
}
