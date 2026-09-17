package goose

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Direction indicates the migration direction (Up or Down).
type Direction bool

const (
	// DirectionUp denotes an UP migration.
	DirectionUp Direction = true
	// DirectionDown denotes a DOWN migration.
	DirectionDown Direction = false
)

// ParsedMigration contains the parsed migration statements and execution metadata.
type ParsedMigration struct {
	UpStatements   []string
	DownStatements []string
	UpUseTx        bool
	DownUseTx      bool
}

type parserState int

const (
	stateStart parserState = iota
	stateUp
	stateDown
	stateStatementBeginUp
	stateStatementBeginDown
	stateStatementEndUp
	stateStatementEndDown
)

// ParseMigrationFile parses a migration file from the specified path.
func ParseMigrationFile(path string) (*ParsedMigration, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open migration file %q: %w", path, err)
	}
	defer file.Close()
	return ParseMigration(file)
}

// ParseMigration parses a migration script from an io.Reader.
func ParseMigration(r io.Reader) (*ParsedMigration, error) {
	scanner := bufio.NewScanner(r)
	parsed := &ParsedMigration{
		UpUseTx:   true,
		DownUseTx: true,
	}

	state := stateStart
	currentDirection := DirectionUp
	var buf bytes.Buffer

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "-- +goose") {
			cmd := strings.TrimSpace(strings.TrimPrefix(trimmed, "-- +goose"))
			switch strings.ToUpper(cmd) {
			case "UP":
				if buf.Len() > 0 && strings.TrimSpace(buf.String()) != "" {
					return nil, fmt.Errorf("unexpected unfinished statement before '-- +goose Up'")
				}
				buf.Reset()
				state = stateUp
				currentDirection = DirectionUp
				continue
			case "DOWN":
				if buf.Len() > 0 && strings.TrimSpace(buf.String()) != "" {
					return nil, fmt.Errorf("unexpected unfinished statement before '-- +goose Down'")
				}
				buf.Reset()
				state = stateDown
				currentDirection = DirectionDown
				continue
			case "NO TRANSACTION":
				if currentDirection == DirectionUp {
					parsed.UpUseTx = false
				} else {
					parsed.DownUseTx = false
				}
				continue
			case "STATEMENTBEGIN":
				if state == stateUp || state == stateStatementEndUp {
					state = stateStatementBeginUp
				} else if state == stateDown || state == stateStatementEndDown {
					state = stateStatementBeginDown
				} else {
					return nil, fmt.Errorf("'-- +goose StatementBegin' must appear after Up or Down directive")
				}
				continue
			case "STATEMENTEND":
				if state == stateStatementBeginUp {
					stmt := cleanStatement(buf.String())
					if stmt != "" {
						parsed.UpStatements = append(parsed.UpStatements, stmt)
					}
					buf.Reset()
					state = stateStatementEndUp
				} else if state == stateStatementBeginDown {
					stmt := cleanStatement(buf.String())
					if stmt != "" {
						parsed.DownStatements = append(parsed.DownStatements, stmt)
					}
					buf.Reset()
					state = stateStatementEndDown
				} else {
					return nil, fmt.Errorf("'-- +goose StatementEnd' must follow '-- +goose StatementBegin'")
				}
				continue
			default:
				continue
			}
		}

		if buf.Len() == 0 {
			if strings.HasPrefix(trimmed, "--") || trimmed == "" {
				continue
			}
		}

		switch state {
		case stateStatementBeginUp, stateStatementBeginDown:
			buf.WriteString(line)
			buf.WriteString("\n")
		case stateUp, stateStatementEndUp:
			if currentDirection == DirectionUp {
				buf.WriteString(line)
				buf.WriteString("\n")
				if endsWithSemicolon(line) {
					stmt := cleanStatement(buf.String())
					if stmt != "" {
						parsed.UpStatements = append(parsed.UpStatements, stmt)
					}
					buf.Reset()
					state = stateUp
				}
			}
		case stateDown, stateStatementEndDown:
			if currentDirection == DirectionDown {
				buf.WriteString(line)
				buf.WriteString("\n")
				if endsWithSemicolon(line) {
					stmt := cleanStatement(buf.String())
					if stmt != "" {
						parsed.DownStatements = append(parsed.DownStatements, stmt)
					}
					buf.Reset()
					state = stateDown
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed scanning migration content: %w", err)
	}

	if state == stateStatementBeginUp || state == stateStatementBeginDown {
		return nil, errors.New("migration file terminated with unclosed '-- +goose StatementBegin'")
	}

	if buf.Len() > 0 && strings.TrimSpace(buf.String()) != "" {
		remaining := cleanStatement(buf.String())
		if remaining != "" {
			if currentDirection == DirectionUp {
				parsed.UpStatements = append(parsed.UpStatements, remaining)
			} else {
				parsed.DownStatements = append(parsed.DownStatements, remaining)
			}
		}
	}

	return parsed, nil
}

func endsWithSemicolon(line string) bool {
	trimmed := strings.TrimSpace(line)
	if idx := strings.Index(trimmed, "--"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	return strings.HasSuffix(trimmed, ";")
}

func cleanStatement(s string) string {
	lines := strings.Split(s, "\n")
	var kept []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		if trimmed == "" && len(kept) == 0 {
			continue
		}
		kept = append(kept, l)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
