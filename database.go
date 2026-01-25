package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql" // MySQL driver
	_ "github.com/lib/pq"              // PostgreSQL driver
)

// DatabaseStorage implements Storage interface for database-based audit logging
// Supports PostgreSQL and MySQL
type DatabaseStorage struct {
	db        *sql.DB
	dbType    string // "postgres" or "mysql"
	tableName string
}

// DatabaseConfig holds configuration for database storage
type DatabaseConfig struct {
	TableName string // Custom table name (default: "audit_logs")
}

// DefaultDatabaseConfig returns default database configuration
func DefaultDatabaseConfig() *DatabaseConfig {
	return &DatabaseConfig{
		TableName: "audit_logs",
	}
}

// NewDatabaseStorage creates a new database storage instance
func NewDatabaseStorage(databaseURL string) (*DatabaseStorage, error) {
	return NewDatabaseStorageWithConfig(databaseURL, nil)
}

// NewDatabaseStorageWithConfig creates a new database storage instance with config
func NewDatabaseStorageWithConfig(databaseURL string, config *DatabaseConfig) (*DatabaseStorage, error) {
	if config == nil {
		config = DefaultDatabaseConfig()
	}

	// Detect database type from URL
	var dbType string
	var driver string
	var dsn string
	if len(databaseURL) >= 10 && databaseURL[:10] == "postgres://" {
		dbType = "postgres"
		driver = "postgres"
		dsn = databaseURL
	} else if len(databaseURL) >= 8 && databaseURL[:8] == "mysql://" {
		dbType = "mysql"
		driver = "mysql"
		// Convert mysql:// to DSN format
		dsn = databaseURL[8:]
	} else {
		return nil, fmt.Errorf("unsupported database URL format, must start with postgres:// or mysql://")
	}

	// Open database connection
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
		tableName: config.TableName,
	}

	// Create table if it doesn't exist
	if err := storage.createTable(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	return storage, nil
}

// NewDatabaseStorageFromDB creates a new database storage from existing *sql.DB
func NewDatabaseStorageFromDB(db *sql.DB, dbType string, config *DatabaseConfig) (*DatabaseStorage, error) {
	if config == nil {
		config = DefaultDatabaseConfig()
	}

	if dbType != "postgres" && dbType != "mysql" && dbType != "sqlite" {
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	storage := &DatabaseStorage{
		db:        db,
		dbType:    dbType,
		tableName: config.TableName,
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
