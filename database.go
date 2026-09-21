package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const maxTableNameLen = 64

// validateTableName ensures table name is safe for SQL identifier use (no injection).
// Only allows [a-zA-Z0-9_] and max 64 characters.
func validateTableName(name string) error {
	if name == "" {
		return fmt.Errorf("table name cannot be empty")
	}
	if len(name) > maxTableNameLen {
		return fmt.Errorf("table name too long: max %d characters", maxTableNameLen)
	}
	for _, r := range name {
		// ASCII only, matching the documented [a-zA-Z0-9_]. unicode.IsLetter
		// and unicode.IsNumber accept Cyrillic letters, full-width digits and
		// much else, which is not an injection risk here but does produce
		// identifiers that several engines require quoting for.
		isASCIILetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isASCIIDigit := r >= '0' && r <= '9'
		if !isASCIILetter && !isASCIIDigit && r != '_' {
			return fmt.Errorf("invalid table name: only alphanumeric and underscore allowed")
		}
	}
	return nil
}

// driverImportHint names the driver package a dialect is usually served by,
// so an unregistered driver produces an error the caller can act on.
var driverImportHint = map[string]string{
	"postgres": `_ "github.com/lib/pq"`,
	"pgx":      `_ "github.com/jackc/pgx/v5/stdlib"`,
	"mysql":    `_ "github.com/go-sql-driver/mysql"`,
	"sqlite":   `_ "modernc.org/sqlite"`,
	"sqlite3":  `_ "github.com/mattn/go-sqlite3"`,
}

// parseDatabaseURL maps a URL scheme to the SQL dialect this package generates,
// the database/sql driver name that dialect is registered under by default, and
// the DSN to hand that driver.
func parseDatabaseURL(databaseURL string) (dbType, driver, dsn string, err error) {
	switch {
	// lib/pq and pgx both take the URL as-is, scheme included.
	case strings.HasPrefix(databaseURL, "postgres://"):
		return "postgres", "postgres", databaseURL, nil
	case strings.HasPrefix(databaseURL, "postgresql://"):
		return "postgres", "postgres", databaseURL, nil
	// go-sql-driver/mysql takes a bare DSN, so the scheme is stripped.
	case strings.HasPrefix(databaseURL, "mysql://"):
		return "mysql", "mysql", strings.TrimPrefix(databaseURL, "mysql://"), nil
	case strings.HasPrefix(databaseURL, "sqlite://"):
		return "sqlite", "sqlite", strings.TrimPrefix(databaseURL, "sqlite://"), nil
	default:
		return "", "", "", fmt.Errorf("unsupported database URL format, must start with postgres://, postgresql://, mysql:// or sqlite://")
	}
}

// checkDriverRegistered reports whether the program has registered driver with
// database/sql, and if not, which import registers it.
func checkDriverRegistered(driver string) error {
	registered := sql.Drivers()
	if slices.Contains(registered, driver) {
		return nil
	}
	hint := driverImportHint[driver]
	if hint == "" {
		hint = "the driver package for " + driver
	}
	return fmt.Errorf(
		"database/sql driver %q is not registered: this package imports no driver, "+
			"so the program must do it, for example with a blank import of the driver package (import %s); "+
			"registered drivers: %v",
		driver, hint, registered)
}

// DatabaseStorage implements Storage interface for database-based audit logging.
// It generates PostgreSQL, MySQL and SQLite flavoured SQL, and works with any
// database/sql driver the program registers for one of those dialects.
type DatabaseStorage struct {
	db        *sql.DB
	dbType    string // "postgres", "mysql" or "sqlite"
	tableName string
}

// DatabaseConfig holds configuration for database storage
type DatabaseConfig struct {
	TableName string // Custom table name (default: "audit_logs")

	// DriverName overrides the database/sql driver name derived from the URL
	// scheme ("postgres", "mysql" or "sqlite"). Set it when the driver the
	// program registers is not named after its dialect: "pgx" for
	// jackc/pgx's stdlib driver, "sqlite3" for mattn/go-sqlite3,
	// "mysql+instrumented" for a wrapped driver, and so on. The dialect --
	// which SQL this package generates -- still comes from the URL scheme.
	DriverName string
}

// DefaultDatabaseConfig returns default database configuration
func DefaultDatabaseConfig() *DatabaseConfig {
	return &DatabaseConfig{
		TableName: "audit_logs",
	}
}

// NewDatabaseStorage creates a new database storage instance from a URL.
//
// This package imports no database driver, so the program must register one
// itself -- a blank import of github.com/lib/pq, github.com/go-sql-driver/mysql
// or modernc.org/sqlite is the usual way. Without it the call fails with a
// message naming the missing import; nothing is dialled. Use
// [NewDatabaseStorageFromDB] to hand over a *sql.DB the program already has.
func NewDatabaseStorage(databaseURL string) (*DatabaseStorage, error) {
	return NewDatabaseStorageWithConfig(databaseURL, nil)
}

// NewDatabaseStorageWithConfig creates a new database storage instance with config.
//
// The URL scheme picks the dialect and the default driver name: postgres:// and
// postgresql:// use "postgres", mysql:// uses "mysql", sqlite:// uses "sqlite".
// Set [DatabaseConfig.DriverName] when the registered driver goes by another
// name, such as "pgx" or "sqlite3". As with [NewDatabaseStorage], the driver
// must already be registered with database/sql by the program.
func NewDatabaseStorageWithConfig(databaseURL string, config *DatabaseConfig) (*DatabaseStorage, error) {
	if config == nil {
		config = DefaultDatabaseConfig()
	}
	tableName := config.TableName
	if tableName == "" {
		tableName = "audit_logs"
	}
	if err := validateTableName(tableName); err != nil {
		return nil, err
	}

	// Detect dialect, driver and DSN from the URL scheme.
	dbType, driver, dsn, err := parseDatabaseURL(databaseURL)
	if err != nil {
		return nil, err
	}
	if config.DriverName != "" {
		driver = config.DriverName
	}

	// This package registers no driver of its own, so say plainly which import
	// is missing rather than leaving the caller with database/sql's
	// "unknown driver" message.
	if err := checkDriverRegistered(driver); err != nil {
		return nil, err
	}

	// Open database connection. When logging errors from this package, do not
	// log error.Error() verbatim—drivers may include DSN/password in the message.
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	storage := &DatabaseStorage{
		db:        db,
		dbType:    dbType,
		tableName: tableName,
	}

	// Create table if it doesn't exist
	if err := storage.createTable(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	return storage, nil
}

// NewDatabaseStorageFromDB creates a new database storage from an existing
// *sql.DB. dbType selects the SQL flavour and must be "postgres", "mysql" or
// "sqlite"; the driver behind db is the caller's choice, so a wrapped or
// instrumented driver works as long as it speaks one of those dialects.
//
// Prefer this constructor in a service that already owns a connection pool:
// it shares the pool and its lifecycle, and needs no URL parsing. Note that
// [DatabaseStorage.Close] closes the pool it is given.
func NewDatabaseStorageFromDB(db *sql.DB, dbType string, config *DatabaseConfig) (*DatabaseStorage, error) {
	if config == nil {
		config = DefaultDatabaseConfig()
	}
	tableName := config.TableName
	if tableName == "" {
		tableName = "audit_logs"
	}
	if err := validateTableName(tableName); err != nil {
		return nil, err
	}

	if dbType != "postgres" && dbType != "mysql" && dbType != "sqlite" {
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	storage := &DatabaseStorage{
		db:        db,
		dbType:    dbType,
		tableName: tableName,
	}

	// Create table if it doesn't exist
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := storage.createTable(ctx); err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	return storage, nil
}

// createTable creates the audit_logs table if it doesn't exist
func (s *DatabaseStorage) createTable(ctx context.Context) error {
	var createTableSQL string

	switch s.dbType {
	case "postgres":
		createTableSQL = fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id BIGSERIAL PRIMARY KEY,
			event_type VARCHAR(50) NOT NULL,
			event_id VARCHAR(100),
			user_id VARCHAR(100),
			challenge_id VARCHAR(100),
			session_id VARCHAR(100),
			channel VARCHAR(20),
			destination VARCHAR(255),
			purpose VARCHAR(50),
			resource VARCHAR(255),
			result VARCHAR(20),
			reason VARCHAR(255),
			provider VARCHAR(50),
			provider_message_id VARCHAR(255),
			ip VARCHAR(45),
			user_agent TEXT,
			request_id VARCHAR(100),
			trace_id VARCHAR(100),
			timestamp BIGINT NOT NULL,
			duration_ms BIGINT,
			metadata JSONB,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_%s_user_id ON %s(user_id);
		CREATE INDEX IF NOT EXISTS idx_%s_challenge_id ON %s(challenge_id);
		CREATE INDEX IF NOT EXISTS idx_%s_session_id ON %s(session_id);
		CREATE INDEX IF NOT EXISTS idx_%s_event_type ON %s(event_type);
		CREATE INDEX IF NOT EXISTS idx_%s_timestamp ON %s(timestamp);
		CREATE INDEX IF NOT EXISTS idx_%s_created_at ON %s(created_at);
		`, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName)

	case "mysql":
		createTableSQL = fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			event_type VARCHAR(50) NOT NULL,
			event_id VARCHAR(100),
			user_id VARCHAR(100),
			challenge_id VARCHAR(100),
			session_id VARCHAR(100),
			channel VARCHAR(20),
			destination VARCHAR(255),
			purpose VARCHAR(50),
			resource VARCHAR(255),
			result VARCHAR(20),
			reason VARCHAR(255),
			provider VARCHAR(50),
			provider_message_id VARCHAR(255),
			ip VARCHAR(45),
			user_agent TEXT,
			request_id VARCHAR(100),
			trace_id VARCHAR(100),
			timestamp BIGINT NOT NULL,
			duration_ms BIGINT,
			metadata JSON,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			INDEX idx_%s_user_id (user_id),
			INDEX idx_%s_challenge_id (challenge_id),
			INDEX idx_%s_session_id (session_id),
			INDEX idx_%s_event_type (event_type),
			INDEX idx_%s_timestamp (timestamp),
			INDEX idx_%s_created_at (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
		`, s.tableName,
			s.tableName, s.tableName, s.tableName,
			s.tableName, s.tableName, s.tableName)

	case "sqlite":
		createTableSQL = fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type VARCHAR(50) NOT NULL,
			event_id VARCHAR(100),
			user_id VARCHAR(100),
			challenge_id VARCHAR(100),
			session_id VARCHAR(100),
			channel VARCHAR(20),
			destination VARCHAR(255),
			purpose VARCHAR(50),
			resource VARCHAR(255),
			result VARCHAR(20),
			reason VARCHAR(255),
			provider VARCHAR(50),
			provider_message_id VARCHAR(255),
			ip VARCHAR(45),
			user_agent TEXT,
			request_id VARCHAR(100),
			trace_id VARCHAR(100),
			timestamp BIGINT NOT NULL,
			duration_ms BIGINT,
			metadata TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_%s_user_id ON %s(user_id);
		CREATE INDEX IF NOT EXISTS idx_%s_challenge_id ON %s(challenge_id);
		CREATE INDEX IF NOT EXISTS idx_%s_session_id ON %s(session_id);
		CREATE INDEX IF NOT EXISTS idx_%s_event_type ON %s(event_type);
		CREATE INDEX IF NOT EXISTS idx_%s_timestamp ON %s(timestamp);
		CREATE INDEX IF NOT EXISTS idx_%s_created_at ON %s(created_at);
		`, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName,
			s.tableName, s.tableName)

	default:
		return fmt.Errorf("unsupported database type: %s", s.dbType)
	}

	// Execute each statement separately for SQLite
	statements := strings.Split(createTableSQL, ";")
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return nil
}

// Write writes an audit record to the database
func (s *DatabaseStorage) Write(ctx context.Context, record *Record) error {
	// Marshal metadata to JSON
	var metadataJSON []byte
	var err error
	if record.Metadata != nil {
		metadataJSON, err = json.Marshal(record.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	var query string
	var args []interface{}

	switch s.dbType {
	case "postgres":
		query = fmt.Sprintf(`
		INSERT INTO %s (
			event_type, event_id, user_id, challenge_id, session_id,
			channel, destination, purpose, resource, result, reason,
			provider, provider_message_id, ip, user_agent, request_id,
			trace_id, timestamp, duration_ms, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		`, s.tableName)
		args = []interface{}{
			string(record.EventType), record.EventID, record.UserID,
			record.ChallengeID, record.SessionID, record.Channel,
			record.Destination, record.Purpose, record.Resource,
			string(record.Result), record.Reason, record.Provider,
			record.ProviderMessageID, record.IP, record.UserAgent,
			record.RequestID, record.TraceID, record.Timestamp,
			record.DurationMS, metadataJSON,
		}

	case "mysql", "sqlite":
		query = fmt.Sprintf(`
		INSERT INTO %s (
			event_type, event_id, user_id, challenge_id, session_id,
			channel, destination, purpose, resource, result, reason,
			provider, provider_message_id, ip, user_agent, request_id,
			trace_id, timestamp, duration_ms, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, s.tableName)
		args = []interface{}{
			string(record.EventType), record.EventID, record.UserID,
			record.ChallengeID, record.SessionID, record.Channel,
			record.Destination, record.Purpose, record.Resource,
			string(record.Result), record.Reason, record.Provider,
			record.ProviderMessageID, record.IP, record.UserAgent,
			record.RequestID, record.TraceID, record.Timestamp,
			record.DurationMS, string(metadataJSON),
		}

	default:
		return fmt.Errorf("unsupported database type: %s", s.dbType)
	}

	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to insert audit record: %w", err)
	}

	return nil
}

// Query queries audit records from the database
func (s *DatabaseStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	if filter == nil {
		filter = DefaultQueryFilter()
	}
	filter.Normalize()

	// Build WHERE clause
	var whereClauses []string
	var args []interface{}
	argIndex := 1

	addCondition := func(column, value string) {
		if value == "" {
			return
		}
		if s.dbType == "postgres" {
			whereClauses = append(whereClauses, fmt.Sprintf("%s = $%d", column, argIndex))
		} else {
			whereClauses = append(whereClauses, fmt.Sprintf("%s = ?", column))
		}
		args = append(args, value)
		argIndex++
	}

	addCondition("event_type", filter.EventType)
	addCondition("user_id", filter.UserID)
	addCondition("challenge_id", filter.ChallengeID)
	addCondition("session_id", filter.SessionID)
	addCondition("channel", filter.Channel)
	addCondition("result", filter.Result)
	addCondition("ip", filter.IP)

	if filter.StartTime > 0 {
		if s.dbType == "postgres" {
			whereClauses = append(whereClauses, fmt.Sprintf("timestamp >= $%d", argIndex))
		} else {
			whereClauses = append(whereClauses, "timestamp >= ?")
		}
		args = append(args, filter.StartTime)
		argIndex++
	}

	if filter.EndTime > 0 {
		if s.dbType == "postgres" {
			whereClauses = append(whereClauses, fmt.Sprintf("timestamp <= $%d", argIndex))
		} else {
			whereClauses = append(whereClauses, "timestamp <= ?")
		}
		args = append(args, filter.EndTime)
		argIndex++
	}

	// Build query
	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	var query string
	if s.dbType == "postgres" {
		query = fmt.Sprintf(`
		SELECT event_type, event_id, user_id, challenge_id, session_id,
		       channel, destination, purpose, resource, result, reason,
		       provider, provider_message_id, ip, user_agent, request_id,
		       trace_id, timestamp, duration_ms, metadata
		FROM %s
		%s
		ORDER BY timestamp DESC
		LIMIT $%d OFFSET $%d
		`, s.tableName, whereClause, argIndex, argIndex+1)
		args = append(args, filter.Limit, filter.Offset)
	} else {
		query = fmt.Sprintf(`
		SELECT event_type, event_id, user_id, challenge_id, session_id,
		       channel, destination, purpose, resource, result, reason,
		       provider, provider_message_id, ip, user_agent, request_id,
		       trace_id, timestamp, duration_ms, metadata
		FROM %s
		%s
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?
		`, s.tableName, whereClause)
		args = append(args, filter.Limit, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []*Record
	for rows.Next() {
		record := &Record{}
		var eventType, result string
		var eventID, userID, challengeID, sessionID sql.NullString
		var channel, destination, purpose, resource sql.NullString
		var reason, provider, providerMessageID sql.NullString
		var ip, userAgent, requestID, traceID sql.NullString
		var durationMS sql.NullInt64
		var metadataJSON sql.NullString

		err := rows.Scan(
			&eventType, &eventID, &userID, &challengeID, &sessionID,
			&channel, &destination, &purpose, &resource, &result, &reason,
			&provider, &providerMessageID, &ip, &userAgent, &requestID,
			&traceID, &record.Timestamp, &durationMS, &metadataJSON,
		)
		if err != nil {
			continue
		}

		record.EventType = EventType(eventType)
		record.Result = Result(result)
		record.EventID = eventID.String
		record.UserID = userID.String
		record.ChallengeID = challengeID.String
		record.SessionID = sessionID.String
		record.Channel = channel.String
		record.Destination = destination.String
		record.Purpose = purpose.String
		record.Resource = resource.String
		record.Reason = reason.String
		record.Provider = provider.String
		record.ProviderMessageID = providerMessageID.String
		record.IP = ip.String
		record.UserAgent = userAgent.String
		record.RequestID = requestID.String
		record.TraceID = traceID.String
		record.DurationMS = durationMS.Int64

		if metadataJSON.Valid && metadataJSON.String != "" {
			_ = json.Unmarshal([]byte(metadataJSON.String), &record.Metadata)
		}

		results = append(results, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return results, nil
}

// Close closes the database connection
func (s *DatabaseStorage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// DB returns the underlying database connection
func (s *DatabaseStorage) DB() *sql.DB {
	return s.db
}

// DBType returns the database type
func (s *DatabaseStorage) DBType() string {
	return s.dbType
}
