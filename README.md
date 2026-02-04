# audit-kit

[![Go Reference](https://pkg.go.dev/badge/github.com/soulteary/audit-kit.svg)](https://pkg.go.dev/github.com/soulteary/audit-kit)
[![Go Report Card](https://goreportcard.com/badge/github.com/soulteary/audit-kit)](https://goreportcard.com/report/github.com/soulteary/audit-kit)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![codecov](https://codecov.io/gh/soulteary/audit-kit/graph/badge.svg)](https://codecov.io/gh/soulteary/audit-kit)

[中文文档](README_CN.md)

A unified audit logging toolkit for Go services. This package provides audit log interfaces, multiple storage backends (file, database, Redis), async writing with worker pools, and sensitive data masking.

## Features

- **Storage Interface**: Unified interface for all storage backends
- **Multiple Backends**: File (JSON Lines), Database (PostgreSQL/MySQL/SQLite), Redis
- **Async Writing**: Worker pool for non-blocking audit logging
- **Multi-Storage**: Write to multiple backends simultaneously
- **Data Masking**: Automatic masking of sensitive data (email, phone, IP)
- **Fluent API**: Builder pattern for constructing audit records
- **Query Support**: Filter and paginate audit records
- **Extensible**: Custom event types and metadata support

## Installation

```bash
go get github.com/soulteary/audit-kit
```

## Usage

### Basic Audit Logging

```go
import (
    audit "github.com/soulteary/audit-kit"
)

// Create a file storage (filePath must be from trusted config, not user input)
storage, err := audit.NewFileStorage("/var/log/audit.log")
if err != nil {
    log.Fatal(err)
}

// Create a logger with default config
logger := audit.NewLogger(storage, nil)
defer logger.Stop()

// Log an event
record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
    WithUserID("user123").
    WithIP("192.168.1.1").
    WithUserAgent("Mozilla/5.0")

logger.Log(context.Background(), record)
```

### Async Writing (Recommended for Production)

```go
// Create logger with async writer
config := audit.DefaultConfig()
config.Writer = &audit.WriterConfig{
    QueueSize: 1000,
    Workers:   4,
}

logger := audit.NewLoggerWithWriter(storage, config)
defer logger.Stop()

// Logging is now non-blocking
logger.Log(ctx, record)
```

### Database Storage

```go
// PostgreSQL
storage, err := audit.NewDatabaseStorage("postgres://user:pass@localhost/db")

// MySQL
storage, err := audit.NewDatabaseStorage("mysql://user:pass@tcp(localhost:3306)/db")

// SQLite (for testing)
db, _ := sql.Open("sqlite", ":memory:")
storage, err := audit.NewDatabaseStorageFromDB(db, "sqlite", nil)
```

### Redis Storage

```go
import "github.com/redis/go-redis/v9"

client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

storage := audit.NewRedisStorageWithConfig(client, &audit.RedisConfig{
    KeyPrefix: "myapp:audit:",
    TTL:       7 * 24 * time.Hour,
})
```

### Multi-Storage (Write to Multiple Backends)

```go
fileStorage, _ := audit.NewFileStorage("/var/log/audit.log")
redisStorage := audit.NewRedisStorage(redisClient)

multiStorage := audit.NewMultiStorage(fileStorage, redisStorage)
logger := audit.NewLogger(multiStorage, nil)
```

### Querying Audit Records

```go
// Build a filter
filter := audit.DefaultQueryFilter().
    WithEventType("login_success").
    WithUserID("user123").
    WithTimeRange(startTime, endTime).
    WithLimit(50)

// Query records
records, err := logger.Query(ctx, filter)
```

### Convenience Logging Methods

```go
// Log challenge events (OTP/verification)
logger.LogChallenge(ctx, audit.EventChallengeCreated, "ch_123", "user123", audit.ResultSuccess,
    audit.WithRecordChannel("email"),
    audit.WithRecordDestination("test@example.com"),
)

// Log authentication events
logger.LogAuth(ctx, audit.EventLoginSuccess, "user123", audit.ResultSuccess,
    audit.WithRecordIP("192.168.1.1"),
    audit.WithRecordUserAgent("Mozilla/5.0"),
)

// Log access control events
logger.LogAccess(ctx, audit.EventAccessGranted, "user123", "/api/users", audit.ResultSuccess)
```

### Custom Event Types

```go
const (
    EventPasswordChange audit.EventType = "password_change"
    EventAPIKeyCreated  audit.EventType = "api_key_created"
)

record := audit.NewRecord(EventPasswordChange, audit.ResultSuccess).
    WithUserID("user123").
    WithMetadata("changed_by", "admin")
```

### Data Masking

```go
// Automatic masking when logging
config := audit.DefaultConfig()
config.MaskDestination = true // Enabled by default

logger := audit.NewLogger(storage, config)

// Manual masking
masked := audit.MaskEmail("user@example.com")    // u***@example.com
masked = audit.MaskPhone("13800138000")          // 138****8000
masked = audit.MaskIP("192.168.1.100")           // 192.***.100
```

### Log Callback (for Standard Logging)

```go
logger.SetLogCallback(func(record *audit.Record) {
    log.Printf("[AUDIT] %s user=%s result=%s",
        record.EventType, record.UserID, record.Result)
})
```

## Security & Operational Notes

- **File storage paths must be trusted**. The file backend refuses symlinks and creates files with `0600` permissions to limit exposure. Ensure the log directory is secured and not user-writable.
- **Async writer drops on backpressure**. If the queue is full, records are dropped. Monitor queue/worker capacity in production.
- **Redis index TTL**. The index key uses the same TTL as records to avoid long-lived metadata; plan cleanup/retention accordingly.
- **Untrusted JSON**. `RecordFromJSON` enforces size limits but does not cap nested metadata depth. Avoid decoding untrusted deeply nested JSON.

## Event Types

### Built-in Event Types

| Category | Event Type | Description |
|----------|------------|-------------|
| Challenge | `challenge_created` | OTP challenge created |
| Challenge | `challenge_verified` | OTP verification successful |
| Challenge | `challenge_revoked` | Challenge manually revoked |
| Challenge | `challenge_expired` | Challenge expired |
| Send | `send_success` | Message sent successfully |
| Send | `send_failed` | Message send failed |
| Verification | `verification_success` | Verification successful |
| Verification | `verification_failed` | Verification failed |
| Authentication | `login_success` | Login successful |
| Authentication | `login_failed` | Login failed |
| Authentication | `logout` | User logged out |
| Session | `session_create` | Session created |
| Session | `session_expire` | Session expired |
| Authorization | `access_granted` | Access granted |
| Authorization | `access_denied` | Access denied |
| User | `user_created` | User created |
| User | `user_updated` | User updated |
| User | `user_deleted` | User deleted |
| User | `user_locked` | User account locked |
| User | `user_unlocked` | User account unlocked |
| Rate Limit | `rate_limited` | Rate limit triggered |
| Custom | `custom` | Custom event |

## Configuration

```go
config := &audit.Config{
    Enabled:         true,                    // Enable/disable logging
    MaskDestination: true,                    // Mask phone/email in logs
    TTL:             7 * 24 * time.Hour,      // TTL for Redis storage
    Writer: &audit.WriterConfig{
        QueueSize:   1000,                    // Async queue size
        Workers:     2,                       // Number of workers
        StopTimeout: 10 * time.Second,        // Graceful shutdown timeout
    },
}
```

## Project Structure

```
audit-kit/
├── types.go           # Record types and event definitions
├── storage.go         # Storage interface and query filter
├── logger.go          # Logger with async support
├── writer.go          # Async writer with worker pool
├── file.go            # File storage (JSON Lines)
├── database.go        # Database storage (PostgreSQL/MySQL/SQLite)
├── redis.go           # Redis storage
├── factory.go         # Storage factory and multi-storage
├── mask.go            # Data masking utilities
└── *_test.go          # Comprehensive tests
```

## Integration Examples

### Herald (OTP Service)

```go
package main

import (
    "context"
    audit "github.com/soulteary/audit-kit"
)

func main() {
    storage, _ := audit.NewFileStorage("/var/log/herald-audit.log")
    logger := audit.NewLoggerWithWriter(storage, nil)
    defer logger.Stop()

    // Log OTP challenge created
    logger.LogChallenge(ctx, audit.EventChallengeCreated, challengeID, userID, audit.ResultSuccess,
        audit.WithRecordChannel("sms"),
        audit.WithRecordDestination(phone),
        audit.WithRecordIP(clientIP),
        audit.WithRecordProvider("aliyun", messageID),
    )

    // Log verification result
    logger.LogChallenge(ctx, audit.EventChallengeVerified, challengeID, userID, audit.ResultSuccess)
}
```

### Stargate (Auth Gateway)

```go
package main

import (
    audit "github.com/soulteary/audit-kit"
)

func main() {
    storage, _ := audit.NewDatabaseStorage("postgres://...")
    logger := audit.NewLoggerWithWriter(storage, nil)
    defer logger.Stop()

    // Log successful login
    logger.LogAuth(ctx, audit.EventLoginSuccess, userID, audit.ResultSuccess,
        audit.WithRecordIP(clientIP),
        audit.WithRecordUserAgent(userAgent),
        audit.WithRecordMetadata("auth_method", "otp"),
    )

    // Log access control
    logger.LogAccess(ctx, audit.EventAccessGranted, userID, requestPath, audit.ResultSuccess)
}
```

### Warden (User Service)

```go
package main

import (
    audit "github.com/soulteary/audit-kit"
)

func main() {
    storage := audit.NewRedisStorage(redisClient)
    logger := audit.NewLogger(storage, nil)
    defer logger.Stop()

    // Log user lookup
    logger.Log(ctx, audit.NewRecord(audit.EventCustom, audit.ResultSuccess).
        WithUserID(userID).
        WithMetadata("action", "user_lookup").
        WithMetadata("query_type", "email"),
    )
}
```

## Security and operational notes

- **File storage**: Pass only trusted paths to `NewFileStorage`; do not use user-controlled paths (path traversal or symlinks could write logs elsewhere).
- **Database errors**: When logging errors from database storage, avoid logging `error.Error()` verbatim—drivers may include DSN or passwords. Use fixed messages or error type checks instead.
- **Async queue full**: When the writer queue is full, records are dropped (non-blocking). Use `OnEnqueueFailed` to alert or write to a fallback; size the queue appropriately for your load.
- **Redis**: Prefer setting `EventID` or `ChallengeID` on records so keys are unique. The index key has no TTL; call `Cleanup()` periodically or run a job to remove expired key references from the index.
- **Metadata**: After JSON round-trip, numeric metadata values become `float64`; document this if your code type-asserts metadata.

## Requirements

- Go 1.25 or later
- Optional: github.com/redis/go-redis/v9 (for Redis storage)
- Optional: github.com/go-sql-driver/mysql or github.com/lib/pq (for database storage)

## Test Coverage

Run tests:

```bash
go test ./... -v

# With coverage
go test ./... -coverprofile=coverage.out -covermode=atomic
go tool cover -html=coverage.out -o coverage.html
go tool cover -func=coverage.out
```

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

See [LICENSE](LICENSE) file for details.
