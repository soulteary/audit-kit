//go:build integration

package integrationtest

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	// The drivers a real program would register. Blank-imported here and
	// nowhere else in the module, so they stay out of every consumer's
	// dependency graph.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	audit "github.com/soulteary/audit-kit/v2"
)

// Integration tests for real database connections.
// Run with: go test -tags=integration -v ./integrationtest/...

func TestIntegration_PostgreSQL(t *testing.T) {
	// Skip if no PostgreSQL connection string
	connStr := os.Getenv("TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("TEST_POSTGRES_URL not set, skipping PostgreSQL integration test")
	}

	// Create storage
	storage, err := audit.NewDatabaseStorage(connStr)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Test write
	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithUserID("integration_test_user").
		WithIP("192.168.1.1").
		WithMetadata("test", "integration")

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Test query
	filter := audit.DefaultQueryFilter().WithUserID("integration_test_user")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1)

	// Verify database type
	assert.Equal(t, "postgres", storage.DBType())
}

func TestIntegration_MySQL(t *testing.T) {
	// Skip if no MySQL connection string
	connStr := os.Getenv("TEST_MYSQL_URL")
	if connStr == "" {
		t.Skip("TEST_MYSQL_URL not set, skipping MySQL integration test")
	}

	// Create storage
	storage, err := audit.NewDatabaseStorage(connStr)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	// Test write
	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithUserID("integration_test_user").
		WithIP("192.168.1.1")

	err = storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Verify database type
	assert.Equal(t, "mysql", storage.DBType())
}

func TestIntegration_PostgreSQL_AllOperations(t *testing.T) {
	connStr := os.Getenv("TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}

	storage, err := audit.NewDatabaseStorage(connStr)
	require.NoError(t, err)
	defer func() { _ = storage.Close() }()

	now := time.Now().Unix()
	testUserID := "integration_test_" + time.Now().Format("20060102150405")

	// Write multiple records
	for i := 0; i < 5; i++ {
		record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
			WithUserID(testUserID).
			WithIP("192.168.1." + string(rune('1'+i))).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query with various filters
	filter := audit.DefaultQueryFilter().
		WithUserID(testUserID).
		WithEventType("login_success").
		WithResult("success").
		WithLimit(10)

	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 5)

	// Query with time range
	filter = audit.DefaultQueryFilter().
		WithUserID(testUserID).
		WithTimeRange(now+2, now+4)

	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestIntegration_PostgreSQL_FromDB(t *testing.T) {
	connStr := os.Getenv("TEST_POSTGRES_URL")
	if connStr == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	storage, err := audit.NewDatabaseStorageFromDB(db, "postgres", &audit.DatabaseConfig{
		TableName: "audit_logs_integration",
	})
	require.NoError(t, err)

	record := audit.NewRecord(audit.EventCustom, audit.ResultSuccess).WithUserID("test")
	err = storage.Write(context.Background(), record)
	require.NoError(t, err)
}
