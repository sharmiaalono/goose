package main

import (
	"fmt"

	"sharmiaalono/goose/lib/goose/dialect"
)

func main() {
	fmt.Println("Goose SQLite PRAGMA Validator initialized.")
	stmt := "PRAGMA journal_mode = WAL;"
	err := dialect.ValidateSQLiteStatement(stmt, true)
	if err != nil {
		fmt.Println("Detected invalid transactional PRAGMA:", err)
	}
}
