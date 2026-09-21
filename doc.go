// Package audit provides a unified audit logging toolkit for Go services:
// a common [Storage] interface, built-in file and SQL database backends, an
// asynchronous [Writer], destination masking and a [Logger] that ties them
// together.
//
// # Layout
//
// The root package links no database driver and no Redis client. A service
// that writes its audit log to a file links neither, and one that writes to
// Postgres links only the driver it registers itself:
//
//   - The SQL backend ([NewDatabaseStorage], [NewDatabaseStorageFromDB]) is
//     built on database/sql alone. The program registers its own driver, the
//     usual way, with a blank import -- github.com/lib/pq,
//     github.com/go-sql-driver/mysql, modernc.org/sqlite or any other driver
//     speaking one of those three dialects.
//   - The Redis backend lives in
//     github.com/soulteary/audit-kit/v2/redisstore, and with it go-redis.
//     Only importing that package links it in.
//
// # Getting started
//
//	storage, err := audit.NewFileStorage("/var/log/app/audit.log")
//	if err != nil {
//		return err
//	}
//	logger := audit.NewLoggerWithWriter(storage, audit.DefaultConfig())
//	defer logger.Stop()
//
//	logger.LogAuth(ctx, audit.EventLoginSuccess, userID, audit.ResultSuccess,
//		audit.WithRecordIP(clientIP))
//
// [NewLoggerWithWriter] enqueues each record and persists it on a background
// worker, so a slow or unavailable backend never blocks the request path. It
// is the right default for a service; [NewLogger] writes synchronously, which
// is simpler for a CLI or a test. Either way, [Logger.Stop] must run before
// the process exits or queued records are lost. A full queue drops records
// rather than blocking, which is what [Config.OnEnqueueFailed] is there to
// make visible.
//
// # Masking
//
// [DefaultConfig] masks the destination of every record it writes, so
// 13812345678 reaches storage as 138****5678 and user@example.com as
// u***@example.com.
// The record the caller passes to [Logger.Log] is never modified. Masking
// applies to [Record.Destination] only: anything sensitive put in
// [Record.Metadata], [Record.Reason] or [Record.Resource] is stored verbatim.
//
// # Writing another backend
//
// A backend is a [Storage]: Write, Query and Close. Query takes a
// [QueryFilter], and [QueryFilter.Matches] is exported so that a backend which
// cannot push the whole filter down to its query language finishes the job in
// memory exactly as the built-in ones do. The redisstore subpackage is a
// worked example, including how it takes a narrow client interface rather than
// a concrete client type.
package audit
