package database

import (
	"database/sql"
)

// Logger interface for query helpers - compatible with utils.LogsManager
type Logger interface {
	Error(msg, category string)
	Info(msg, category string)
	Warn(msg, category string)
}

// QueryRowSingle executes a single-row query with consistent error handling.
// Returns nil if no rows found (sql.ErrNoRows), logs and returns error for other failures.
// Uses Go generics for type safety while reducing boilerplate code.
func QueryRowSingle[T any](
	db *sql.DB,
	query string,
	scanFunc func(*sql.Row) (*T, error),
	logger Logger,
	logContext string,
	args ...interface{},
) (*T, error) {
	row := db.QueryRow(query, args...)
	result, err := scanFunc(row)

	if err != nil {
		if err == sql.ErrNoRows {
			// No rows found is not an error condition - return nil
			return nil, nil
		}
		// Log actual errors
		logger.Error("Failed to query row", logContext)
		return nil, err
	}

	return result, nil
}

// QueryRows executes a multi-row query with consistent error handling.
// Returns empty slice if no rows found, logs and returns error for failures.
// Uses Go generics for type safety while reducing boilerplate code.
func QueryRows[T any](
	db *sql.DB,
	query string,
	scanFunc func(*sql.Rows) (*T, error),
	logger Logger,
	logContext string,
	args ...interface{},
) ([]*T, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		logger.Error("Failed to query rows", logContext)
		return nil, err
	}
	defer rows.Close()

	var results []*T
	for rows.Next() {
		result, err := scanFunc(rows)
		if err != nil {
			logger.Error("Failed to scan row", logContext)
			// Continue processing other rows instead of failing completely
			continue
		}
		results = append(results, result)
	}

	// Check for errors during iteration
	if err := rows.Err(); err != nil {
		logger.Error("Error iterating rows", logContext)
		return nil, err
	}

	return results, nil
}

// ExecWithLogging executes a query with logging on error.
// Returns the sql.Result for further processing (e.g., LastInsertId, RowsAffected).
func ExecWithLogging(
	db *sql.DB,
	query string,
	logger Logger,
	logContext string,
	args ...interface{},
) (sql.Result, error) {
	result, err := db.Exec(query, args...)
	if err != nil {
		logger.Error("Failed to execute query", logContext)
		return nil, err
	}
	return result, nil
}

// ExecWithAffectedRowsCheck executes a query and verifies rows were affected.
// Returns sql.ErrNoRows if no rows were affected, otherwise returns the count.
// Useful for UPDATE and DELETE operations where you expect to modify rows.
func ExecWithAffectedRowsCheck(
	db *sql.DB,
	query string,
	logger Logger,
	logContext string,
	args ...interface{},
) (int64, error) {
	result, err := db.Exec(query, args...)
	if err != nil {
		logger.Error("Failed to execute query", logContext)
		return 0, err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	if rowsAffected == 0 {
		return 0, sql.ErrNoRows
	}

	return rowsAffected, nil
}

// ScanNullableString converts sql.NullString to string.
// Returns empty string if null.
func ScanNullableString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// ScanNullableInt64 converts sql.NullInt64 to *int64.
// Returns nil if null, otherwise returns pointer to value.
func ScanNullableInt64(ni sql.NullInt64) *int64 {
	if ni.Valid {
		return &ni.Int64
	}
	return nil
}

// ScanNullableBool converts sql.NullBool to bool.
// Returns false if null.
func ScanNullableBool(nb sql.NullBool) bool {
	if nb.Valid {
		return nb.Bool
	}
	return false
}

// ScanNullableFloat64 converts sql.NullFloat64 to *float64.
// Returns nil if null, otherwise returns pointer to value.
func ScanNullableFloat64(nf sql.NullFloat64) *float64 {
	if nf.Valid {
		return &nf.Float64
	}
	return nil
}
