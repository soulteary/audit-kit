package audit_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	// A program using the SQL backend registers its own driver. This example
	// uses SQLite; a service would blank-import github.com/lib/pq or
	// github.com/go-sql-driver/mysql instead.
	_ "modernc.org/sqlite"

	audit "github.com/soulteary/audit-kit/v2"
)

// The common case: an asynchronous logger writing JSON Lines to a file.
func Example() {
	dir, err := os.MkdirTemp("", "audit-example")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	storage, err := audit.NewFileStorage(filepath.Join(dir, "audit.log"))
	if err != nil {
		panic(err)
	}

	logger := audit.NewLoggerWithWriter(storage, audit.DefaultConfig())

	logger.LogAuth(context.Background(), audit.EventLoginSuccess, "user-42", audit.ResultSuccess,
		audit.WithRecordIP("203.0.113.9"),
		audit.WithRecordChannel("email"),
		audit.WithRecordDestination("user@example.com"),
	)

	// Stop drains the queue, so every record is on disk after it returns.
	if err := logger.Stop(); err != nil {
		panic(err)
	}

	records, err := storage.Query(context.Background(), audit.DefaultQueryFilter().WithUserID("user-42"))
	if err != nil {
		panic(err)
	}

	for _, r := range records {
		// The destination is masked; the caller's own record was not modified.
		fmt.Println(r.EventType, r.UserID, r.Result, r.Destination)
	}
	// Output: login_success user-42 success u***@example.com
}

// The SQL backend takes a *sql.DB the program already owns, so the connection
// pool, its settings and its lifecycle stay in one place.
func ExampleNewDatabaseStorageFromDB() {
	dir, err := os.MkdirTemp("", "audit-example")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	db, err := sql.Open("sqlite", filepath.Join(dir, "audit.db"))
	if err != nil {
		panic(err)
	}

	storage, err := audit.NewDatabaseStorageFromDB(db, "sqlite", &audit.DatabaseConfig{
		TableName: "audit_logs",
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = storage.Close() }()

	logger := audit.NewLogger(storage, audit.DefaultConfig())
	logger.LogAccess(context.Background(), audit.EventAccessDenied, "user-42", "/admin", audit.ResultFailure,
		audit.WithRecordReason("not an admin"),
	)

	records, err := storage.Query(context.Background(), audit.DefaultQueryFilter().WithResult(string(audit.ResultFailure)))
	if err != nil {
		panic(err)
	}
	fmt.Println(records[0].EventType, records[0].Resource, records[0].Reason)
	// Output: access_denied /admin not an admin
}

// Without a registered driver the URL constructors fail before dialling, and
// the error names the import to add.
func ExampleNewDatabaseStorage_driverNotRegistered() {
	_, err := audit.NewDatabaseStorage("postgres://user:pass@localhost:5432/app")
	fmt.Println(err != nil)
	// Output: true
}

// A record can be filtered in memory with the same rules a Storage applies,
// which is what a custom backend uses to finish a query its query language
// cannot express.
func ExampleQueryFilter_Matches() {
	record := audit.NewRecord(audit.EventLoginFailed, audit.ResultFailure).
		WithUserID("user-42").
		WithChannel("sms")

	fmt.Println(audit.DefaultQueryFilter().WithUserID("user-42").Matches(record))
	fmt.Println(audit.DefaultQueryFilter().WithChannel("email").Matches(record))
	// Output:
	// true
	// false
}

// Masking helpers are usable on their own, for a log line that is not an
// audit record.
func ExampleMaskDestination() {
	fmt.Println(audit.MaskDestination("13812345678", "sms"))
	fmt.Println(audit.MaskDestination("user@example.com", "email"))
	fmt.Println(audit.MaskDestination("somewhere-else", "push"))
	// Output:
	// 138****5678
	// u***@example.com
	// ****
}

// MaskIP keeps an IPv4 address's first and last octet, which is enough to tell
// two clients apart in a log without storing the address itself.
func ExampleMaskIP() {
	fmt.Println(audit.MaskIP("192.168.1.5"))
	fmt.Println(audit.MaskIP("::ffff:192.168.1.5"))
	// Output:
	// 192.***.5
	// 192.***.5
}
