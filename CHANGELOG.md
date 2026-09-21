# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Because Go encodes the major version in the import path, every major release
also changes the module path. The current one is
`github.com/soulteary/audit-kit/v2`.

## [Unreleased]

Dependency refresh only. No API was removed and no call needs rewriting.

- secure-kit is `github.com/soulteary/secure-kit/v2` v2.1.0 (was v2.0.0). No
  library code changed between the two: v2.1.0 rewrote secure-kit's own tests
  against the standard library, which takes `testify` and `go.yaml.in/yaml/v3`
  out of *its* `go.mod`. `go mod tidy` in an importing module walks the tests
  of the packages it imports, so a test dependency there is not private to it
  — but this module requires testify directly for its own tests and keeps it
  in the graph either way. `go.sum` changes by two lines and nothing else
  shifts: a program importing only this package sees the same 26 modules, 28
  `go.sum` lines, one `// indirect` requirement and 137 linked packages as
  [2.1.0].

## [2.1.0] — 2026-09-21

### Changed

- **secure-kit v1.6.0 → v2.0.0**, which is the follow-up the 2.0.0 notes below
  named and could not make on their own:

  > `mask.go` uses secure-kit's `MaskEmail`/`MaskPhone`, and those live in the
  > same package as its argon2 and bcrypt helpers, so every consumer links
  > password hashing it cannot call. A `mask` subpackage in secure-kit would
  > take the root package to stdlib-only without duplicating the masking rules.

  secure-kit v2.0.0 did the equivalent from the other side: it moved argon2 and
  bcrypt into a `passwd` subpackage, leaving the masking helpers in a root
  package that no longer needs `golang.org/x/crypto`. `mask.go` changes its
  import path and nothing else — the functions kept their names and their
  behaviour.

  Measured for a program importing only this package, `-trimpath`, go1.27.0
  linux/amd64:

  | | v2.0.0 | v2.1.0 |
  |---|---|---|
  | modules in `go list -m all` | 32 | 26 |
  | `go.sum` lines | 30 | 28 |
  | `// indirect` requirements in the consumer's `go.mod` | 3 | 1 |
  | linked packages | 142 | 137 |
  | binary size | 3,341,838 bytes | 3,277,091 bytes (−1.9%) |

  `golang.org/x/crypto` and `golang.org/x/sys` leave the consumer's `go.mod`
  entirely; `secure-kit/v2` is the one requirement left.

  The 2.0.0 note also measured the alternative — inlining the two functions —
  at 129 linked packages. Keeping the dependency costs 8 more than that, all
  of them standard library packages secure-kit's root reaches for its SHA,
  HMAC, random and comparison helpers. That is the price of one source of
  truth for the masking rules rather than a second copy here, and it is worth
  paying: a masking rule that drifts between two implementations is a privacy
  bug nobody sees until it is in a log.

  No API changes. Nothing in this package's surface names a secure-kit type,
  so a consumer notices only the smaller module graph.

## [2.0.0] — 2026-09-21

Every breaking change this kit had queued up, in one release. That is
deliberate: each one alone would force every user to rewrite an import path,
and spreading them over three majors is three times the work for the same
result.

### Changed — BREAKING

- **The root package no longer imports a database driver.** `database.go`
  blank-imported `github.com/go-sql-driver/mysql` and `github.com/lib/pq`, so
  every program that imported this package linked both drivers — including one
  that only ever wrote a JSON Lines file. Registering a driver is the
  program's job, as it is for every other `database/sql` user:

  ```go
  import (
      _ "github.com/lib/pq"            // or go-sql-driver/mysql, modernc.org/sqlite
      audit "github.com/soulteary/audit-kit/v2"
  )
  ```

  `NewDatabaseStorage` and `NewDatabaseStorageWithConfig` keep their
  signatures. When the driver the URL scheme names is not registered they now
  fail before dialling anything, with a message naming the import to add and
  listing the drivers the program did register.
  `NewDatabaseStorageFromDB` is unaffected: its caller already supplies the
  `*sql.DB`, and therefore the driver.

- **The Redis backend moved to the `redisstore` subpackage.** The root package
  no longer imports go-redis, so a binary that never talks to Redis no longer
  links it.

  | Removed from the root package | Replacement |
  |---|---|
  | `audit.RedisStorage` | `redisstore.Storage` |
  | `audit.NewRedisStorage` | `redisstore.New` |
  | `audit.NewRedisStorageWithConfig` | `redisstore.NewWithConfig` |
  | `audit.RedisConfig` | `redisstore.Config` |
  | `audit.DefaultRedisConfig` | `redisstore.DefaultConfig` |
  | `StorageOptions.RedisClient/RedisPrefix/RedisTTL` | `StorageOptions.RedisStorage`, built with `redisstore` |

  Keeping these as deprecated shims was not an option: a shim has to import
  go-redis, which relinks it and gives back the entire benefit.

  A subpackage is enough; go-redis does not need its own module. Module graph
  pruning keeps a requirement that no imported package needs out of the
  consumer's `go.mod` and `go.sum` entirely.

- **`redisstore.Storage.Close` no longer closes the Redis client.** v1's
  `RedisStorage.Close()` closed the client it was handed — and
  `MultiStorage.Close` and `Logger.Stop` both call it, so shutting the audit
  logger down took the rest of the program's Redis with it. A store now closes
  only what it owns. Set `redisstore.Config.CloseClient` to restore v1's
  behaviour, or close the client where it was created:

  ```go
  client := redis.NewClient(opts)
  defer client.Close()
  ```

- **`StorageOptions.RedisClient`, `RedisPrefix` and `RedisTTL` are gone.**
  Build the store first and pass it in, which keeps the configuration-driven
  factory working without the root package knowing what a Redis client is:

  ```go
  opts := &audit.StorageOptions{FilePath: path, DatabaseURL: dsn}
  if useRedis {
      opts.RedisStorage = redisstore.NewWithConfig(client, &redisstore.Config{
          KeyPrefix: "myapp:audit:", TTL: 7 * 24 * time.Hour,
      })
  }
  storage, err := audit.NewStorageFromType(audit.ParseStorageType(kind), opts)
  ```

- **`Config.TTL` is gone.** It documented itself as "TTL for Redis/cache
  storage", was set to 7 days by `DefaultConfig`, and was read by nothing:
  the Redis TTL always came from `RedisConfig.TTL`, now
  `redisstore.Config.TTL`. A field that silently ignores what you set it to is
  worse than no field, and with the Redis backend out of the root package
  there is nothing left for it to have meant. Set `redisstore.Config.TTL`.

- **The module path is therefore now `github.com/soulteary/audit-kit/v2`**, by
  the import compatibility rule. Every user must update the import path,
  including a file-only service unaffected by everything above.

### Changed

- The integration tests moved to the `integrationtest` package, behind the
  same `integration` build tag, and run with
  `go test -tags=integration ./integrationtest/...`. A driver imported by a
  package's *test* still reaches every consumer's `go.sum`, so leaving them in
  the root package would have kept lib/pq there after all the work to remove
  it. Out here they register their drivers exactly as a real program does, and
  `TestIntegration_MySQL` gets the driver it has been missing since the root
  package stopped providing one.

### Fixed

- **`postgres://` URLs were rejected outright.** `NewDatabaseStorage` compared
  a 10-byte slice of the URL against the 11-byte literal `"postgres://"`, so
  the comparison could never be true and every PostgreSQL URL failed with
  `unsupported database URL format`. The `lib/pq` blank import the root
  package paid for was therefore unreachable through this constructor: the
  only PostgreSQL path that ever worked was `NewDatabaseStorageFromDB`, where
  the caller registers the driver anyway. The scheme check now uses
  `strings.HasPrefix`, and the test that covered this asserted only that *an*
  error came back, so it passed throughout.

### Added

- The `redisstore` subpackage. `redisstore.Client` is the part of a go-redis
  client the store uses, so `*redis.Client`, `*redis.ClusterClient`,
  `*redis.Ring` and `redis.UniversalClient` all work where the root package
  took only `*redis.Client` — a cluster or Sentinel deployment no longer needs
  a backend of its own, and an instrumented wrapper satisfies it too.

  Taking an interface reopens a hole the old `*redis.Client` field closed by
  construction, so the store closes it explicitly: a plain `c == nil` misses a
  typed nil, such as an unassigned `*redis.Client` field, and calling a method
  on one panics. `redisstore` checks through `reflect` and returns
  `ErrNilClient` instead, because an audit backend that takes the process down
  is worse than the record it failed to write.
- `redisstore.DefaultKeyPrefix` and `redisstore.DefaultTTL`, naming the
  `"audit:"` and 7-day defaults v1 repeated as literals.
- `DatabaseConfig.DriverName` and `StorageOptions.DriverName`, overriding the
  driver name derived from the URL scheme. Now that the program registers the
  driver, it may not be registered under the dialect's name: `pgx` for
  jackc/pgx's `stdlib` driver, `sqlite3` for mattn/go-sqlite3, or any name an
  instrumented wrapper chooses.
- `sqlite://` and `postgresql://` URL schemes. The SQLite dialect was already
  supported through `NewDatabaseStorageFromDB`; it now has a URL form too.
- `QueryFilter.Matches`, the filter comparison `FileStorage` and `redisstore`
  both apply in memory. It is exported so that a `Storage` implemented outside
  this package filters identically instead of reimplementing the rules.
- `CHANGELOG.md`, a package doc in `doc.go`, and runnable examples
  (`Example`, `ExampleNewDatabaseStorageFromDB`, `ExampleQueryFilter_Matches`,
  `ExampleMaskDestination`, `ExampleMaskIP`, and `Example` /
  `ExampleNew_cluster` in `redisstore`) that `go test` verifies, so they
  cannot drift from the API.
- A `Root Package Dependency Gate` job in CI and in the release workflow. The
  root package's non-stdlib imports are checked against a deny list, because
  the whole of this release is one blank import away from being undone.
- `.github/workflows/release.yml`. It runs CI's gate against the tagged commit
  plus the two checks that only matter at tag time: that the module path
  carries the tag's major version, and that both READMEs' `go get` line names
  that same path. A `/v2` path that ships with a `v2.0.0` tag on a commit
  declaring `/v1` is unfetchable, and that is caught at tag time or not at all.

### Measured

For a program that imports only the root package — a file-backed audit log,
the case the split is for — measured against v1.10.0 with the same source and
`go build -trimpath`:

| | v1.10.0 | v2.0.0 |
|---|---|---|
| Binary size | 6,080,647 B | 4,703,739 B (−22.6%) |
| Linked packages | 225 | 142 |
| Non-stdlib packages linked | 46 | 9 |
| `// indirect` requirements in the consumer's `go.mod` | 9 | 3 |
| Modules in the consumer's `go.sum` | 27 | 15 |

Twelve modules leave that `go.sum`: go-redis and miniredis plus the five they
drag along (cespare/xxhash, klauspost/cpuid, yuin/gopher-lua, zeebo/xxh3,
go.uber.org/atomic), the two bsm testing modules, and both SQL drivers —
go-sql-driver/mysql with filippo.io/edwards25519, and lib/pq.

A program that *does* use Redis pays nothing for the move: it imports
`redisstore` and go-redis directly, and still drops the SQL drivers
(10,968,757 → 10,746,586 bytes, 225 → 209 packages, 27 → 24 modules in
`go.sum`).

The three remaining indirect requirements are `github.com/soulteary/secure-kit`
and, through it, `golang.org/x/crypto` and `golang.org/x/sys`. `mask.go` uses
secure-kit's `MaskEmail`/`MaskPhone`, and those live in the same package as its
argon2 and bcrypt helpers, so every consumer links password hashing it cannot
call. A `mask` subpackage in secure-kit would take the root package to
stdlib-only without duplicating the masking rules. Measured by inlining the
two functions: 142 → 129 linked packages, 15 → 13 modules in `go.sum`, no
`// indirect` requirement left at all, and 4,703,739 → 4,573,184 bytes. It
needs no further change here.

## [1.10.0]

Dependency refresh only. No API was removed and no call needs rewriting.

- The SQLite driver is `modernc.org/sqlite` v1.59.0 (was v1.58.0). Its own
  dependencies did not move, so `go.sum` changes by two lines and nothing else
  in the graph shifts.
- Nothing in the library links it. `modernc.org/sqlite` is a `database/sql`
  driver that only this module's tests import.

## [1.9.0]

Dependency refresh only. No API was removed and no call needs rewriting.

- Test Redis is `miniredis` v2.39.0 (was v2.36.1).
- The SQLite driver is `modernc.org/sqlite` v1.58.0 (was v1.44.3).

## [1.8.0]

Two lifecycle defects in the async writer. No API was removed, and no call
needs rewriting — but the observable behaviour changes.

- **Queued records now survive shutdown.** `Stop()` used to cancel the writer's
  context *before* draining the queue, so every record still queued was written
  with a dead context and rejected by the backend: a 100-record queue persisted
  nothing.
- **`Log`/`Enqueue` after `Stop()` no longer panics.** The queue used to be
  closed while a send could still be in flight, which panicked with "send on
  closed channel" under the right interleaving.
- **A slow or re-entrant queue-full callback no longer hangs shutdown.**
  `OnEnqueueFailed` is invoked outside the lifecycle lock.
- **`OnEnqueueFailed` / `OnWriteFailed` can be set while workers run.** Both
  fields were previously written without synchronisation.
- **`GetStats().QueueLength` reports the real depth after `Stop()`.**
- **`MaskIP` handles IPv4-mapped IPv6.** `::ffff:192.168.1.1` masked to
  `::ffff:192.***.1` before; it now yields `192.***.1`.
- **`MaskString` with a negative `keepChars` returns a fully masked string**
  instead of panicking with a slice-bounds error.
- **Table names are validated as ASCII `[a-zA-Z0-9_]`.**
