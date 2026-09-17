package goose

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SQLMigration represents a SQL migration.
type SQLMigration struct {
	Statements []string
	NoTx       bool
	RawContent string // New field: Stores the original raw content of the SQL migration file.
}

// String returns the string representation of the SQL migration.
func (sm *SQLMigration) String() string {
	return strings.Join(sm.Statements, "\n")
}

// ParseSQLMigration parses a SQL migration file.
func ParseSQLMigration(path string, version int64) (*Migration, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQL migration file %q: %w", path, err)
	}
	defer file.Close()

	// Read the entire file content to store in RawContent
	contentBytes, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read SQL migration file %q: %w", path, err)
	}
	rawContent := string(contentBytes)

	// Reset file reader to parse statements and directives
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("failed to seek to start of SQL migration file %q: %w", path, err)
	}

	scanner := bufio.NewScanner(file)
	var statements []string
	var noTx bool
	var currentStatement bytes.Buffer

	for scanner.Scan() {
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)

		// Check for goose directives
		if strings.HasPrefix(trimmedLine, "--- +goose") || strings.HasPrefix(trimmedLine, "-- +goose") {
			directive := strings.TrimPrefix(trimmedLine, "--- +goose")
			directive = strings.TrimPrefix(directive, "-- +goose")
			directive = strings.TrimSpace(directive)

			switch directive {
			case "NoTransaction":
				noTx = true
			case "Up": // Ignore Up/Down directives for now, they are for multi-statement files
			case "Down":
			default:
				// Unknown directive, ignore for now
			}
			continue // Don't add directives to statements
		}

		// If line is a SQL comment, ignore it for statement parsing
		if strings.HasPrefix(trimmedLine, "--") {
			continue
		}

		// If line is empty, and we have a current statement, it might be a separator
		if trimmedLine == "" {
			if currentStatement.Len() > 0 {
				statements = append(statements, currentStatement.String())
				currentStatement.Reset()
			}
			continue
		}

		// Append line to current statement
		if currentStatement.Len() > 0 {
			currentStatement.WriteString("\n")
		}
		currentStatement.WriteString(line)

		// Check for statement terminator (semicolon)
		if strings.HasSuffix(trimmedLine, ";") {
			statements = append(statements, currentStatement.String())
			currentStatement.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan SQL migration file %q: %w", path, err)
	}

	// Add any remaining statement
	if currentStatement.Len() > 0 {
		statements = append(statements, currentStatement.String())
	}

	// Filter out empty statements that might result from parsing
	filteredStatements := make([]string, 0, len(statements))
	for _, stmt := range statements {
		if strings.TrimSpace(stmt) != "" {
			filteredStatements = append(filteredStatements, stmt)
		}
	}

	sqlMigration := &SQLMigration{
		Statements: filteredStatements,
		NoTx:       noTx,
		RawContent: rawContent, // Assign the raw content
	}

	return &Migration{
		Version:    version,
		SourceFile: filepath.Base(path),
		Type:       SQLMigration,
		Source:     sqlMigration,
	}, nil
}

// ExtractGooseVersion extracts the goose version from a filename.
func ExtractGooseVersion(filename string) (int64, error) {
	base := filepath.Base(filename)
	if !strings.HasSuffix(base, ".sql") && !strings.HasSuffix(base, ".go") {
		return 0, fmt.Errorf("unrecognized migration file type: %s", filename)
	}

	v, err := strconv.ParseInt(strings.SplitN(base, "_", 2)[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse migration version from filename %q: %w", filename, err)
	}

	return v, nil
}

// CollectMigrations collects all migrations from a directory.
func CollectMigrations(dir string, currentVersion int64) ([]*Migration, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration directory %q: %w", dir, err)
	}

	var migrations []*Migration
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		version, err := ExtractGooseVersion(file.Name())
		if err != nil {
			// Ignore files that don't match the migration naming convention
			continue
		}

		// Only collect migrations newer than the current version
		if version > currentVersion {
			path := filepath.Join(dir, file.Name())
			var migration *Migration
			if strings.HasSuffix(file.Name(), ".sql") {
				migration, err = ParseSQLMigration(path, version)
			} else if strings.HasSuffix(file.Name(), ".go") {
				migration, err = ParseGoMigration(path, version)
			} else {
				continue // Should not happen due to ExtractGooseVersion check
			}

			if err != nil {
				return nil, fmt.Errorf("failed to parse migration file %q: %w", file.Name(), err)
			}
			migrations = append(migrations, migration)
		}
	}

	// Sort migrations by version
	SortMigrations(migrations)

	return migrations, nil
}
