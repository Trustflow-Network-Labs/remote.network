package database

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// mockLogger is a simple test logger that doesn't write to files
type mockLogger struct{}

func (m *mockLogger) Error(msg, category string) {}
func (m *mockLogger) Info(msg, category string)  {}
func (m *mockLogger) Warn(msg, category string)  {}

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) (*sql.DB, *mockLogger) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// Create a test table
	_, err = db.Exec(`
		CREATE TABLE test_table (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			value INTEGER,
			description TEXT
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create test table: %v", err)
	}

	logger := &mockLogger{}
	return db, logger
}

// TestQueryRowSingle tests the QueryRowSingle generic function
func TestQueryRowSingle(t *testing.T) {
	db, logger := setupTestDB(t)
	defer db.Close()

	// Insert test data
	_, err := db.Exec("INSERT INTO test_table (name, value) VALUES (?, ?)", "test1", 100)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	t.Run("successful query", func(t *testing.T) {
		type TestRow struct {
			ID    int64
			Name  string
			Value int
		}

		query := "SELECT id, name, value FROM test_table WHERE name = ?"
		result, err := QueryRowSingle(db, query,
			func(row *sql.Row) (*TestRow, error) {
				var tr TestRow
				err := row.Scan(&tr.ID, &tr.Name, &tr.Value)
				return &tr, err
			},
			logger, "test", "test1")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if result == nil {
			t.Error("Expected result, got nil")
		}
		if result != nil && result.Name != "test1" {
			t.Errorf("Expected name 'test1', got '%s'", result.Name)
		}
		if result != nil && result.Value != 100 {
			t.Errorf("Expected value 100, got %d", result.Value)
		}
	})

	t.Run("no rows found returns nil", func(t *testing.T) {
		type TestRow struct {
			ID   int64
			Name string
		}

		query := "SELECT id, name FROM test_table WHERE name = ?"
		result, err := QueryRowSingle(db, query,
			func(row *sql.Row) (*TestRow, error) {
				var tr TestRow
				err := row.Scan(&tr.ID, &tr.Name)
				return &tr, err
			},
			logger, "test", "nonexistent")

		if err != nil {
			t.Errorf("Expected no error for ErrNoRows, got: %v", err)
		}
		if result != nil {
			t.Error("Expected nil result for ErrNoRows")
		}
	})

	t.Run("scan error is returned", func(t *testing.T) {
		type TestRow struct {
			ID   int64
			Name string
		}

		// Query returns 3 columns but we only scan 2 - should cause error
		query := "SELECT id, name, value FROM test_table WHERE name = ?"
		result, err := QueryRowSingle(db, query,
			func(row *sql.Row) (*TestRow, error) {
				var tr TestRow
				err := row.Scan(&tr.ID, &tr.Name) // Missing value column
				return &tr, err
			},
			logger, "test", "test1")

		if err == nil {
			t.Error("Expected error for scan mismatch, got nil")
		}
		if result != nil {
			t.Error("Expected nil result on error")
		}
	})
}

// TestQueryRows tests the QueryRows generic function
func TestQueryRows(t *testing.T) {
	db, logger := setupTestDB(t)
	defer db.Close()

	// Insert test data
	testData := []struct {
		name  string
		value int
	}{
		{"test1", 100},
		{"test2", 200},
		{"test3", 300},
	}

	for _, td := range testData {
		_, err := db.Exec("INSERT INTO test_table (name, value) VALUES (?, ?)", td.name, td.value)
		if err != nil {
			t.Fatalf("Failed to insert test data: %v", err)
		}
	}

	t.Run("successful multi-row query", func(t *testing.T) {
		type TestRow struct {
			ID    int64
			Name  string
			Value int
		}

		query := "SELECT id, name, value FROM test_table ORDER BY name"
		results, err := QueryRows(db, query,
			func(rows *sql.Rows) (*TestRow, error) {
				var tr TestRow
				err := rows.Scan(&tr.ID, &tr.Name, &tr.Value)
				return &tr, err
			},
			logger, "test")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if len(results) != 3 {
			t.Errorf("Expected 3 results, got %d", len(results))
		}
		if len(results) > 0 && results[0].Name != "test1" {
			t.Errorf("Expected first result name 'test1', got '%s'", results[0].Name)
		}
	})

	t.Run("empty result set returns empty slice", func(t *testing.T) {
		type TestRow struct {
			ID   int64
			Name string
		}

		query := "SELECT id, name FROM test_table WHERE name = ?"
		results, err := QueryRows(db, query,
			func(rows *sql.Rows) (*TestRow, error) {
				var tr TestRow
				err := rows.Scan(&tr.ID, &tr.Name)
				return &tr, err
			},
			logger, "test", "nonexistent")

		if err != nil {
			t.Errorf("Expected no error for empty result, got: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("Expected empty slice, got %d results", len(results))
		}
	})

	t.Run("scan error continues processing", func(t *testing.T) {
		type TestRow struct {
			ID   int64
			Name string
		}

		query := "SELECT id, name, value FROM test_table ORDER BY name"
		results, err := QueryRows(db, query,
			func(rows *sql.Rows) (*TestRow, error) {
				var tr TestRow
				// Only scan 2 columns when 3 are returned - will cause errors
				err := rows.Scan(&tr.ID, &tr.Name)
				return &tr, err
			},
			logger, "test")

		// Should complete without error but skip bad rows
		if err != nil {
			t.Errorf("Expected no error (bad rows skipped), got: %v", err)
		}
		// All rows should be skipped due to scan errors
		if len(results) != 0 {
			t.Errorf("Expected 0 results (all skipped), got %d", len(results))
		}
	})
}

// TestExecWithLogging tests the ExecWithLogging function
func TestExecWithLogging(t *testing.T) {
	db, logger := setupTestDB(t)
	defer db.Close()

	t.Run("successful insert", func(t *testing.T) {
		query := "INSERT INTO test_table (name, value) VALUES (?, ?)"
		result, err := ExecWithLogging(db, query, logger, "test", "test1", 100)

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if result == nil {
			t.Error("Expected result, got nil")
		}

		id, err := result.LastInsertId()
		if err != nil {
			t.Errorf("Expected to get last insert ID, got error: %v", err)
		}
		if id <= 0 {
			t.Errorf("Expected positive ID, got %d", id)
		}
	})

	t.Run("syntax error is returned", func(t *testing.T) {
		query := "INVALID SQL SYNTAX"
		result, err := ExecWithLogging(db, query, logger, "test")

		if err == nil {
			t.Error("Expected error for invalid SQL, got nil")
		}
		if result != nil {
			t.Error("Expected nil result on error")
		}
	})
}

// TestExecWithAffectedRowsCheck tests the ExecWithAffectedRowsCheck function
func TestExecWithAffectedRowsCheck(t *testing.T) {
	db, logger := setupTestDB(t)
	defer db.Close()

	// Insert test data
	_, err := db.Exec("INSERT INTO test_table (name, value) VALUES (?, ?)", "test1", 100)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	t.Run("successful update", func(t *testing.T) {
		query := "UPDATE test_table SET value = ? WHERE name = ?"
		rowsAffected, err := ExecWithAffectedRowsCheck(db, query, logger, "test", 200, "test1")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if rowsAffected != 1 {
			t.Errorf("Expected 1 row affected, got %d", rowsAffected)
		}
	})

	t.Run("no rows affected returns ErrNoRows", func(t *testing.T) {
		query := "UPDATE test_table SET value = ? WHERE name = ?"
		rowsAffected, err := ExecWithAffectedRowsCheck(db, query, logger, "test", 300, "nonexistent")

		if err != sql.ErrNoRows {
			t.Errorf("Expected sql.ErrNoRows, got: %v", err)
		}
		if rowsAffected != 0 {
			t.Errorf("Expected 0 rows affected, got %d", rowsAffected)
		}
	})

	t.Run("successful delete", func(t *testing.T) {
		query := "DELETE FROM test_table WHERE name = ?"
		rowsAffected, err := ExecWithAffectedRowsCheck(db, query, logger, "test", "test1")

		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if rowsAffected != 1 {
			t.Errorf("Expected 1 row affected, got %d", rowsAffected)
		}
	})
}

// TestScanNullableHelpers tests the nullable scanning helper functions
func TestScanNullableHelpers(t *testing.T) {
	t.Run("ScanNullableString", func(t *testing.T) {
		valid := ScanNullableString(sql.NullString{String: "test", Valid: true})
		if valid != "test" {
			t.Errorf("Expected 'test', got '%s'", valid)
		}

		invalid := ScanNullableString(sql.NullString{String: "", Valid: false})
		if invalid != "" {
			t.Errorf("Expected empty string, got '%s'", invalid)
		}
	})

	t.Run("ScanNullableInt64", func(t *testing.T) {
		valid := ScanNullableInt64(sql.NullInt64{Int64: 123, Valid: true})
		if valid == nil {
			t.Error("Expected non-nil pointer")
		} else if *valid != 123 {
			t.Errorf("Expected 123, got %d", *valid)
		}

		invalid := ScanNullableInt64(sql.NullInt64{Int64: 0, Valid: false})
		if invalid != nil {
			t.Errorf("Expected nil, got %v", invalid)
		}
	})

	t.Run("ScanNullableBool", func(t *testing.T) {
		validTrue := ScanNullableBool(sql.NullBool{Bool: true, Valid: true})
		if !validTrue {
			t.Error("Expected true")
		}

		validFalse := ScanNullableBool(sql.NullBool{Bool: false, Valid: true})
		if validFalse {
			t.Error("Expected false")
		}

		invalid := ScanNullableBool(sql.NullBool{Bool: false, Valid: false})
		if invalid {
			t.Error("Expected false for invalid")
		}
	})

	t.Run("ScanNullableFloat64", func(t *testing.T) {
		valid := ScanNullableFloat64(sql.NullFloat64{Float64: 123.45, Valid: true})
		if valid == nil {
			t.Error("Expected non-nil pointer")
		} else if *valid != 123.45 {
			t.Errorf("Expected 123.45, got %f", *valid)
		}

		invalid := ScanNullableFloat64(sql.NullFloat64{Float64: 0, Valid: false})
		if invalid != nil {
			t.Errorf("Expected nil, got %v", invalid)
		}
	})
}
