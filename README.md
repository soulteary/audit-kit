# audit-kit

[![Go Reference](https://pkg.go.dev/badge/github.com/soulteary/audit-kit.svg)](https://pkg.go.dev/github.com/soulteary/audit-kit)
[![Go Report Card](.github/goreportcard.svg)](.github/goreportcard-report.md)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![codecov](https://codecov.io/gh/soulteary/audit-kit/graph/badge.svg)](https://codecov.io/gh/soulteary/audit-kit)

[中文文档](README_CN.md)

A unified audit logging toolkit for Go services. It provides a single storage
interface over several backends (file, database, Redis), an async writer with a
worker pool and a bounded queue, a fluent record builder, and masking for
sensitive fields.

## Features

- **Storage interface**: one interface (`Write`/`Query`/`Close`) for every backend
- **Multiple backends**: file (JSON Lines), database (PostgreSQL/MySQL/SQLite), Redis, no-op
- **Async writing**: worker pool with a bounded queue, so logging never blocks a request
- **Durable shutdown**: `Stop()` drains the queue and writes it before the context is cancelled
- **Multi-storage**: fan one record out to several backends at once
- **Data masking**: email, phone, IP and free-form string masking
- **Fluent API**: builder pattern for records, functional options for the convenience helpers
- **Query support**: filter and paginate stored records
- **Extensible**: custom event types and arbitrary metadata

## Requirements

- **Go 1.27+** (`go.mod` declares `go 1.27.0`)
- Optional: `github.com/redis/go-redis/v9` for Redis storage
- Optional: `github.com/go-sql-driver/mysql`, `github.com/lib/pq` or
  `modernc.org/sqlite` for database storage

## Installation

```bash
go get github.com/soulteary/audit-kit
```

## Quick Start

```go
package main

import (
    "context"
    "log"

    audit "github.com/soulteary/audit-kit"
)

func main() {
    // filePath must come from trusted config, not from user input.
    storage, err := audit.NewFileStorage("/var/log/audit.log")
    if err != nil {
        log.Fatal(err)
    }

    logger := audit.NewLogger(storage, nil)
    defer logger.Stop()

    record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
        WithUserID("user123").
        WithIP("192.168.1.1").
        WithUserAgent("Mozilla/5.0")

    logger.Log(context.Background(), record)
}
```

## Usage

### Async writing (recommended in production)

`NewLoggerWithWriter` puts a bounded queue and a worker pool in front of the
storage, so `Log` returns without waiting for the backend.

```go
config := audit.DefaultConfig()
config.Writer = &audit.WriterConfig{
    QueueSize:   1000,
    Workers:     4,
    StopTimeout: 10 * time.Second,
}

logger := audit.NewLoggerWithWriter(storage, config)
defer logger.Stop() // drains the queue, then closes storage

logger.Log(ctx, record) // non-blocking
```

`Log` is best-effort by design: when the queue is full the record is dropped
rather than blocking the caller. Set a callback so a drop is never silent:

```go
config := audit.DefaultConfig()
config.Writer = audit.DefaultWriterConfig()
config.OnEnqueueFailed = func(r *audit.Record) {
    metrics.AuditDropped.Inc()      // or write to a fallback sink
}
config.OnWriteFailed = func(r *audit.Record, err error) {
    log.Printf("audit write failed: %v", err)
}
```

Both callbacks are caller code invoked outside the writer's lifecycle lock, so
they may safely block, or even call `Stop()`, without deadlocking shutdown.
They may also be set after the workers have started.

### Shutdown and queue statistics

```go
logger := audit.NewLoggerWithWriter(storage, nil)

// ... later, on SIGTERM:
if err := logger.Stop(); err != nil {
    log.Printf("audit shutdown: %v", err)
}

// Inspect the queue at any time.
stats := logger.GetStats() // *audit.Stats, nil when the logger is synchronous
if stats != nil {
    log.Printf("queued=%d/%d workers=%d started=%t stopped=%t",
        stats.QueueLength, stats.QueueCap, stats.Workers, stats.Started, stats.Stopped)
}
```

`Stop()` marks the writer stopped, waits for in-flight `Enqueue` calls, drains
everything still queued **with a live context**, and only then cancels the
context and closes the storage. If the drain exceeds `StopTimeout` the writer
logs how many records were left unwritten. Calling `Log` after `Stop()` is safe
and simply drops the record.

### Database storage

```go
// PostgreSQL
storage, err := audit.NewDatabaseStorage("postgres://user:pass@localhost/db")

// MySQL
storage, err := audit.NewDatabaseStorage("mysql://user:pass@tcp(localhost:3306)/db")

// An existing *sql.DB (handy for tests)
db, _ := sql.Open("sqlite", ":memory:")
storage, err := audit.NewDatabaseStorageFromDB(db, "sqlite", nil)

// Custom table name — ASCII letters, digits and underscore only, max 64 chars
storage, err := audit.NewDatabaseStorageWithConfig(dsn, &audit.DatabaseConfig{
    TableName: "audit_records",
})
```

### Redis storage

```go
import "github.com/redis/go-redis/v9"

client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

storage := audit.NewRedisStorageWithConfig(client, &audit.RedisConfig{
    KeyPrefix: "myapp:audit:",
    TTL:       7 * 24 * time.Hour,
})

// The index key itself has no TTL; prune expired references periodically.
removed, err := storage.Cleanup(ctx)
```

### Multi-storage

```go
fileStorage, _ := audit.NewFileStorage("/var/log/audit.log")
redisStorage := audit.NewRedisStorage(redisClient)

multi := audit.NewMultiStorage(fileStorage, redisStorage)
logger := audit.NewLogger(multi, nil)
```

### Building storage from configuration

```go
storage, err := audit.NewStorageFromType(
    audit.ParseStorageType(os.Getenv("AUDIT_STORAGE")), // "file" | "database" | "redis" | "none"
    &audit.StorageOptions{
        FilePath:    "/var/log/audit.log",
        DatabaseURL: os.Getenv("DATABASE_URL"),
        RedisClient: redisClient,
        RedisPrefix: "myapp:audit:",
        RedisTTL:    7 * 24 * time.Hour,
        TableName:   "audit_records",
    },
)
```

`audit.NewNoopStorage()` discards everything, which is useful in tests and when
auditing is switched off.

### Querying records

```go
filter := audit.DefaultQueryFilter().
    WithEventType("login_success").
    WithUserID("user123").
    WithTimeRange(startUnix, endUnix).
    WithLimit(50).
    WithOffset(0)

records, err := logger.Query(ctx, filter)
```

### Convenience logging helpers

```go
// OTP / verification challenges
logger.LogChallenge(ctx, audit.EventChallengeCreated, "ch_123", "user123", audit.ResultSuccess,
    audit.WithRecordChannel("email"),
    audit.WithRecordDestination("test@example.com"),
)

// Authentication
logger.LogAuth(ctx, audit.EventLoginSuccess, "user123", audit.ResultSuccess,
    audit.WithRecordIP("192.168.1.1"),
    audit.WithRecordUserAgent("Mozilla/5.0"),
)

// Access control
logger.LogAccess(ctx, audit.EventAccessGranted, "user123", "/api/users", audit.ResultSuccess)
```

### Custom event types

```go
const (
    EventPasswordChange audit.EventType = "password_change"
    EventAPIKeyCreated  audit.EventType = "api_key_created"
)

record := audit.NewRecord(EventPasswordChange, audit.ResultSuccess).
    WithUserID("user123").
    WithMetadata("changed_by", "admin")
```

### Data masking

```go
// Applied automatically to Destination when Config.MaskDestination is true.
config := audit.DefaultConfig()
config.MaskDestination = true // default

// Or call the helpers directly.
audit.MaskEmail("user@example.com") // u***@example.com
audit.MaskPhone("13800138000")      // 138****8000
audit.MaskIP("192.168.1.100")       // 192.***.100
audit.MaskString("secret-token", 2) // se********en
audit.MaskDestination(dest, "sms")  // picks the right masker for the channel
```

`MaskIP` keeps the first and last IPv4 octet. That is **pseudonymisation, not
anonymisation** — it narrows an address to one of at most 65536, and often far
fewer. Use `MaskString` when you need a stronger reduction. IPv4-mapped IPv6
addresses (`::ffff:192.168.1.1`) are normalised to their IPv4 form first, so
they mask to `192.***.1` rather than leaking three octets.

### Serialising records

```go
data, err := record.ToJSON()
back, err := audit.RecordFromJSON(data)
clone := record.Copy() // deep copy, including Metadata
```

A record whose JSON exceeds `audit.MaxRecordJSONSize` (1 MiB) is rejected, which
keeps one oversized metadata blob from filling the log. Note that after a JSON
round-trip numeric metadata values come back as `float64`.

### Log callback

```go
logger.SetLogCallback(func(record *audit.Record) {
    log.Printf("[AUDIT] %s user=%s result=%s",
        record.EventType, record.UserID, record.Result)
})
```

## Configuration

```go
config := &audit.Config{
    Enabled:         true,               // false disables logging entirely
    MaskDestination: true,               // mask phone/email in Destination
    TTL:             7 * 24 * time.Hour, // Redis storage TTL
    Writer: &audit.WriterConfig{
        QueueSize:   1000,               // bounded async queue
        Workers:     2,                  // worker goroutines
        StopTimeout: 10 * time.Second,   // bound on the shutdown drain
    },
    OnEnqueueFailed: func(r *audit.Record) { /* queue full */ },
    OnWriteFailed:   func(r *audit.Record, err error) { /* backend rejected it */ },
}
```

| Option | Default | Notes |
|--------|---------|-------|
| `Enabled` | `true` | `false` makes `Log` a no-op |
| `MaskDestination` | `true` | masks `Record.Destination` by channel |
| `TTL` | `168h` (7 days) | Redis only |
| `Writer.QueueSize` | `1000` | a non-positive value falls back to the default |
| `Writer.Workers` | `2` | a non-positive value falls back to the default |
| `Writer.StopTimeout` | `10s` | a non-positive value falls back to the default |
| `OnEnqueueFailed` | `nil` | without it, a dropped record is only logged |
| `OnWriteFailed` | `nil` | without it, a failed write is only logged |

## API Reference

### Records

| Function | Description |
|----------|-------------|
| `NewRecord(eventType, result)` | Start a record |
| `(*Record).With…` | Fluent setters (`WithUserID`, `WithIP`, `WithMetadata`, …) |
| `(*Record).Copy()` | Deep copy |
| `(*Record).ToJSON()` / `RecordFromJSON(data)` | Serialise / parse |

### Loggers and writers

| Function | Description |
|----------|-------------|
| `NewLogger(storage, config)` | Synchronous logger |
| `NewLoggerWithWriter(storage, config)` | Logger backed by the async writer |
| `(*Logger).Log/LogAuth/LogAccess/LogChallenge` | Write a record |
| `(*Logger).Query(ctx, filter)` | Query stored records |
| `(*Logger).GetStats()` | Queue statistics, `nil` when synchronous |
| `(*Logger).Stop()` | Drain, then close storage |
| `NewWriter(storage, config)` | Use the writer on its own |
| `(*Writer).Start/Enqueue/Stop/GetStats` | Writer lifecycle |

### Storage

| Function | Description |
|----------|-------------|
| `NewFileStorage(path)` | JSON Lines file; `Rotate()` rotates it |
| `NewDatabaseStorage(url)` | PostgreSQL / MySQL from a DSN |
| `NewDatabaseStorageFromDB(db, dbType, cfg)` | Wrap an existing `*sql.DB` |
| `NewRedisStorage(client)` | Redis; `Cleanup(ctx)` prunes the index |
| `NewMultiStorage(storages…)` | Fan-out to several backends |
| `NewNoopStorage()` | Discard everything |
| `NewStorageFromType(type, opts)` | Build from configuration |

### Masking

| Function | Description |
|----------|-------------|
| `MaskEmail(email)` | `u***@example.com` |
| `MaskPhone(phone)` | `138****8000` |
| `MaskIP(ip)` | `192.***.100` (pseudonymisation) |
| `MaskString(s, keepChars)` | Keep first/last `keepChars`; negative is treated as `0` |
| `MaskDestination(dest, channel)` | Channel-aware masking |

## Event Types

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

Results are `audit.ResultSuccess`, `audit.ResultFailure` and
`audit.ResultPending`.

## Upgrade Notes (v1.10.0)

Dependency refresh only. No API was removed and no call needs rewriting.

- The SQLite driver is `modernc.org/sqlite` v1.59.0 (was v1.58.0). Its own
  dependencies did not move, so `go.sum` changes by two lines and nothing else
  in the graph shifts.
- Nothing in the library links it. `modernc.org/sqlite` is a `database/sql`
  driver that only this module's tests import, and the README lists it as one of
  three optional drivers you pick between — so the new version reaches your
  build only if you import it yourself.

## Upgrade Notes (v1.9.0)

Dependency refresh only. No API was removed and no call needs rewriting.

- Test Redis is `miniredis` v2.39.0 (was v2.36.1).
- The SQLite driver is `modernc.org/sqlite` v1.58.0 (was v1.44.3).

## Upgrade Notes (v1.8.0)

This release fixes two lifecycle defects in the async writer. No API was
removed, and no call needs rewriting — but the observable behaviour changes.

- **Queued records now survive shutdown.** `Stop()` used to cancel the writer's
  context *before* draining the queue, so every record still queued was written
  with a dead context and rejected by the backend: a 100-record queue persisted
  nothing. The context is now cancelled only after the workers finish, so
  `StopTimeout` bounds a drain that actually writes. If you previously sized
  `StopTimeout` around a drain that never succeeded, give it enough room for the
  real thing.
- **`Log`/`Enqueue` after `Stop()` no longer panics.** The queue used to be
  closed while a send could still be in flight, which panicked with "send on
  closed channel" under the right interleaving. The queue is never closed now;
  a post-`Stop` call returns without sending.
- **A slow or re-entrant queue-full callback no longer hangs shutdown.**
  `OnEnqueueFailed` is invoked outside the lifecycle lock, so a callback that
  blocks — or calls `Stop()` itself — no longer deadlocks or starves
  `StopTimeout`.
- **`OnEnqueueFailed` / `OnWriteFailed` can be set while workers run.** Both
  fields were previously written without synchronisation, which raced with the
  workers reading them.
- **`GetStats().QueueLength` reports the real depth after `Stop()`.** It used to
  be forced to `0` once stopped, which hid records lost to a timeout.
- **`MaskIP` handles IPv4-mapped IPv6.** `::ffff:192.168.1.1` masked to
  `::ffff:192.***.1` before, exposing more than intended; it now yields
  `192.***.1`. If you match on masked output, re-check those assertions.
- **`MaskString` with a negative `keepChars` returns a fully masked string**
  instead of panicking with a slice-bounds error.
- **Table names are validated as ASCII `[a-zA-Z0-9_]`.** `validateTableName`
  documented that set but used `unicode.IsLetter`/`IsNumber`, so Cyrillic
  letters and full-width digits were accepted and produced identifiers several
  engines need quoted. A non-ASCII `DatabaseConfig.TableName` is now rejected at
  construction.

## Security and Operational Notes

- **File storage**: pass only trusted paths to `NewFileStorage`. A
  user-controlled path allows traversal, and a symlink can redirect the log.
- **Database errors**: do not log `err.Error()` verbatim from database storage —
  drivers may include the DSN, and therefore the password. Log a fixed message
  or check the error type.
- **Dropped records**: a full queue drops records by design. Set
  `OnEnqueueFailed` and size `QueueSize` for your peak, or use the synchronous
  logger where no record may ever be lost.
- **Redis**: set `EventID` or `ChallengeID` so keys stay unique, and run
  `Cleanup()` periodically — the index key itself carries no TTL.
- **Metadata**: after a JSON round-trip, numbers are `float64`. Type-assert
  accordingly.

## Project Structure

```
audit-kit/
├── types.go     # Record, event/result constants, record options
├── storage.go   # Storage interface and QueryFilter
├── logger.go    # Logger, Config, DefaultConfig
├── writer.go    # Async writer, worker pool, lifecycle
├── file.go      # File storage (JSON Lines)
├── database.go  # Database storage (PostgreSQL/MySQL/SQLite)
├── redis.go     # Redis storage
├── factory.go   # Storage factory and multi-storage
└── mask.go      # Masking helpers
```

## Testing

```bash
go test ./...

# With coverage
go test ./... -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out
go tool cover -html=coverage.out -o coverage.html
```

A few tests simulate I/O failures with `chmod`, which uid 0 ignores; those skip
when the suite runs as root.

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.
