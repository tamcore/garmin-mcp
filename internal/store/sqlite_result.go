package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// Result and constraint interpretation, in one place so every statement in the
// package reacts to a failure the same way.
//
// The driver does not export typed constraint errors, so a constraint has to be
// recognized from the message SQLite produces. That is a weakness, and it is
// contained deliberately: a missed match turns a specific sentinel into a wrapped
// generic error, which is noisy but never silent and never permissive. Nothing in
// this package treats an unrecognized error as success.

// isUniqueViolation reports whether err is a UNIQUE or PRIMARY KEY constraint
// failure.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") ||
		strings.Contains(message, "PRIMARY KEY must be unique")
}

// isForeignKeyViolation reports whether err is a foreign key constraint failure,
// which in this schema always means the referenced principal or client is absent.
func isForeignKeyViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

// requireOneRow turns an UPDATE or DELETE that matched nothing into absent, which
// is what the caller's sentinel describes.
func requireOneRow(result sql.Result, absent error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: count affected rows: %w", err)
	}
	if affected == 0 {
		return absent
	}
	return nil
}

// affectedRows reports how many rows a statement changed, for the cascade counters.
func affectedRows(result sql.Result) (int, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: count affected rows: %w", err)
	}
	return int(affected), nil
}

// countRows runs a bound COUNT(*) query. It exists for the post-condition checks
// that make a cascade fail closed: after a delete, the count of what must be gone
// has to be zero, and asking is cheaper than trusting the row counts.
func countRows(tx *sql.Tx, query string, args ...any) (int, error) {
	var count int
	if err := tx.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count rows: %w", err)
	}
	return count, nil
}
