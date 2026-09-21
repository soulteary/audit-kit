# audit-kit

[![Go Reference](https://pkg.go.dev/badge/github.com/soulteary/audit-kit/v2.svg)](https://pkg.go.dev/github.com/soulteary/audit-kit/v2)
[![Go Report Card](.github/goreportcard.svg)](.github/goreportcard-report.md)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![codecov](https://codecov.io/gh/soulteary/audit-kit/graph/badge.svg)](https://codecov.io/gh/soulteary/audit-kit)

[English](README.md)

Go 服务的统一审计日志工具包。提供统一的存储接口（文件、数据库、Redis）、带
worker 池和有界队列的异步写入、链式记录构造器，以及敏感字段脱敏。

根包不链接任何数据库驱动，也不链接 Redis 客户端：SQL 后端只依赖
`database/sql`，Redis 后端放在 `redisstore` 子包里。只把审计日志写到文件的
服务，两者都不会链接进来。

## 特性

- **存储接口**：所有后端共用一套 `Write`/`Query`/`Close` 接口
- **多种后端**：文件（JSON Lines）、数据库（PostgreSQL/MySQL/SQLite）、Redis、空实现
- **用多少付多少**：驱动由使用者自己注册，Redis 独立成子包
- **异步写入**：worker 池 + 有界队列，写日志不阻塞请求
- **可靠关闭**：`Stop()` 先把队列排空写完，之后才取消 context
- **多存储写入**：一条记录同时写入多个后端
- **数据脱敏**：邮箱、手机号、IP 以及通用字符串脱敏
- **链式 API**：记录用构造器，便捷方法用函数式选项
- **查询支持**：过滤与分页
- **可扩展**：自定义事件类型与任意元数据

## 要求

- **Go 1.27+**（`go.mod` 声明 `go 1.27.0`）
- 数据库存储：由**你的程序自己注册** `database/sql` 驱动 ——
  `github.com/lib/pq`、`github.com/go-sql-driver/mysql`、`modernc.org/sqlite`，
  或任何实现这三种方言之一的驱动
- Redis 存储：`github.com/redis/go-redis/v9`，通过
  `github.com/soulteary/audit-kit/v2/redisstore` 子包引入

两者都不是根包的依赖。不额外 import，就不会额外链接。

## 安装

```bash
go get github.com/soulteary/audit-kit/v2
```

从 v1 升级？先看[升级说明（v2.0.0）](#升级说明v200)：import 路径变了，Redis
与数据库的入口也变了。

## 快速开始

```go
package main

import (
    "context"
    "log"

    audit "github.com/soulteary/audit-kit/v2"
)

func main() {
    // filePath 必须来自可信配置，不能是用户输入。
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

## 使用

### 异步写入（生产环境推荐）

`NewLoggerWithWriter` 在存储前面加了一层有界队列和 worker 池，`Log` 不会等待后端。

```go
config := audit.DefaultConfig()
config.Writer = &audit.WriterConfig{
    QueueSize:   1000,
    Workers:     4,
    StopTimeout: 10 * time.Second,
}

logger := audit.NewLoggerWithWriter(storage, config)
defer logger.Stop() // 先排空队列，再关闭存储

logger.Log(ctx, record) // 非阻塞
```

`Log` 的语义是尽力而为：队列满时丢弃记录而不是阻塞调用方。配置回调，别让丢弃悄无声息：

```go
config := audit.DefaultConfig()
config.Writer = audit.DefaultWriterConfig()
config.OnEnqueueFailed = func(r *audit.Record) {
    metrics.AuditDropped.Inc()      // 或者写入兜底存储
}
config.OnWriteFailed = func(r *audit.Record, err error) {
    log.Printf("审计写入失败: %v", err)
}
```

两个回调都是调用方代码，在写入器的生命周期锁之外执行，因此回调里阻塞、甚至调用
`Stop()` 都不会造成死锁。它们也可以在 worker 已经启动之后再设置。

### 关闭与队列统计

```go
logger := audit.NewLoggerWithWriter(storage, nil)

// 收到 SIGTERM 时：
if err := logger.Stop(); err != nil {
    log.Printf("审计关闭: %v", err)
}

// 任意时刻查看队列状态。
stats := logger.GetStats() // *audit.Stats，同步 logger 返回 nil
if stats != nil {
    log.Printf("queued=%d/%d workers=%d started=%t stopped=%t",
        stats.QueueLength, stats.QueueCap, stats.Workers, stats.Started, stats.Stopped)
}
```

`Stop()` 会先标记停止、等待正在进行的 `Enqueue` 结束，然后**带着有效的 context**
把队列里剩下的记录写完，最后才取消 context 并关闭存储。排空超过 `StopTimeout`
时，日志会报告还剩多少条未写入。`Stop()` 之后再调用 `Log` 是安全的，记录直接丢弃。

### 数据库存储

本包不注册任何驱动，按 `database/sql` 的惯例由你的程序注册：

```go
import (
    _ "github.com/lib/pq"            // 或 go-sql-driver/mysql、modernc.org/sqlite

    audit "github.com/soulteary/audit-kit/v2"
)

// PostgreSQL
storage, err := audit.NewDatabaseStorage("postgres://user:pass@localhost/db")

// MySQL
storage, err := audit.NewDatabaseStorage("mysql://user:pass@tcp(localhost:3306)/db")

// SQLite
storage, err := audit.NewDatabaseStorage("sqlite:///var/lib/app/audit.db")

// 复用已有的 *sql.DB —— 服务里已有连接池时优先用这个
db, _ := sql.Open("sqlite", ":memory:")
storage, err := audit.NewDatabaseStorageFromDB(db, "sqlite", nil)

// 自定义表名 —— 只允许 ASCII 字母、数字和下划线，最长 64 字符
storage, err := audit.NewDatabaseStorageWithConfig(dsn, &audit.DatabaseConfig{
    TableName: "audit_records",
})

// 驱动名与方言不同名的情况：pgx、sqlite3、带埋点的包装驱动。
// 方言仍然由 URL scheme 决定。
storage, err := audit.NewDatabaseStorageWithConfig("postgres://…", &audit.DatabaseConfig{
    DriverName: "pgx",
})
```

驱动没注册时，构造函数在拨号之前就会失败，并在错误里写明该补哪个 import：

```
database/sql driver "postgres" is not registered: this package imports no
driver, so the program must do it, for example with a blank import of the
driver package (import _ "github.com/lib/pq"); registered drivers: []
```

### Redis 存储

Redis 在 `redisstore` 子包里，只有 import 它，go-redis 才会进你的二进制：

```go
import (
    "github.com/redis/go-redis/v9"

    "github.com/soulteary/audit-kit/v2/redisstore"
)

client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
defer client.Close() // 传进来的客户端，存储不会替你关

storage := redisstore.NewWithConfig(client, &redisstore.Config{
    KeyPrefix: "myapp:audit:",
    TTL:       7 * 24 * time.Hour,
})

// 索引键本身没有 TTL，需要定期清理过期引用。
removed, err := storage.Cleanup(ctx)
```

`redisstore.New` 接收的是 `redisstore.Client` —— 只包含存储真正用到的几个命令，
因此 `*redis.Client`、`*redis.ClusterClient`、`*redis.Ring`、
`redis.UniversalClient` 以及任何带埋点的包装客户端都能直接传入。

### 多存储写入

```go
fileStorage, _ := audit.NewFileStorage("/var/log/audit.log")
redisStorage := redisstore.New(redisClient)

multi := audit.NewMultiStorage(fileStorage, redisStorage)
logger := audit.NewLogger(multi, nil)
```

### 按配置构造存储

```go
opts := &audit.StorageOptions{
    FilePath:    "/var/log/audit.log",
    DatabaseURL: os.Getenv("DATABASE_URL"),
    TableName:   "audit_records",
}

// Redis 由调用方自己构造，根包因此不必认识 go-redis。
// 不写这一段也可以，选到 "redis" 时错误信息会说明缺了什么。
opts.RedisStorage = redisstore.NewWithConfig(redisClient, &redisstore.Config{
    KeyPrefix: "myapp:audit:",
    TTL:       7 * 24 * time.Hour,
})

storage, err := audit.NewStorageFromType(
    audit.ParseStorageType(os.Getenv("AUDIT_STORAGE")), // "file" | "database" | "redis" | "none"
    opts,
)
```

`audit.NewNoopStorage()` 丢弃一切，适合测试和关闭审计的场景。

### 查询记录

```go
filter := audit.DefaultQueryFilter().
    WithEventType("login_success").
    WithUserID("user123").
    WithTimeRange(startUnix, endUnix).
    WithLimit(50).
    WithOffset(0)

records, err := logger.Query(ctx, filter)
```

### 便捷日志方法

```go
// OTP / 验证码
logger.LogChallenge(ctx, audit.EventChallengeCreated, "ch_123", "user123", audit.ResultSuccess,
    audit.WithRecordChannel("email"),
    audit.WithRecordDestination("test@example.com"),
)

// 认证
logger.LogAuth(ctx, audit.EventLoginSuccess, "user123", audit.ResultSuccess,
    audit.WithRecordIP("192.168.1.1"),
    audit.WithRecordUserAgent("Mozilla/5.0"),
)

// 访问控制
logger.LogAccess(ctx, audit.EventAccessGranted, "user123", "/api/users", audit.ResultSuccess)
```

### 自定义事件类型

```go
const (
    EventPasswordChange audit.EventType = "password_change"
    EventAPIKeyCreated  audit.EventType = "api_key_created"
)

record := audit.NewRecord(EventPasswordChange, audit.ResultSuccess).
    WithUserID("user123").
    WithMetadata("changed_by", "admin")
```

### 数据脱敏

```go
// Config.MaskDestination 为 true 时自动作用于 Destination。
config := audit.DefaultConfig()
config.MaskDestination = true // 默认值

// 也可以直接调用。
audit.MaskEmail("user@example.com") // u***@example.com
audit.MaskPhone("13800138000")      // 138****8000
audit.MaskIP("192.168.1.100")       // 192.***.100
audit.MaskString("secret-token", 2) // se********en
audit.MaskDestination(dest, "sms")  // 按渠道选择脱敏方式
```

`MaskIP` 保留 IPv4 的首尾两段。这是**假名化，不是匿名化**——地址被缩小到最多
65536 种可能，实际往往远少于此。需要更强的削减请用 `MaskString`。IPv4 映射的
IPv6 地址（`::ffff:192.168.1.1`）会先归一为 IPv4 形式，因此脱敏结果是
`192.***.1`，不会多泄露三段。

### 记录序列化

```go
data, err := record.ToJSON()
back, err := audit.RecordFromJSON(data)
clone := record.Copy() // 深拷贝，包含 Metadata
```

JSON 超过 `audit.MaxRecordJSONSize`（1 MiB）的记录会被拒绝，避免一条超大元数据把
日志撑满。注意：JSON 往返之后，数字类型的元数据会变成 `float64`。

### 日志回调

```go
logger.SetLogCallback(func(record *audit.Record) {
    log.Printf("[AUDIT] %s user=%s result=%s",
        record.EventType, record.UserID, record.Result)
})
```

## 配置

```go
config := &audit.Config{
    Enabled:         true,               // false 则完全关闭记录
    MaskDestination: true,               // 对 Destination 做手机号/邮箱脱敏
    Writer: &audit.WriterConfig{
        QueueSize:   1000,               // 有界异步队列
        Workers:     2,                  // worker 协程数
        StopTimeout: 10 * time.Second,   // 关闭时排空的时限
    },
    OnEnqueueFailed: func(r *audit.Record) { /* 队列满 */ },
    OnWriteFailed:   func(r *audit.Record, err error) { /* 后端写入失败 */ },
}
```

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `Enabled` | `true` | `false` 时 `Log` 为空操作 |
| `MaskDestination` | `true` | 按渠道脱敏 `Record.Destination` |
| `Writer.QueueSize` | `1000` | 非正值回退为默认值 |
| `Writer.Workers` | `2` | 非正值回退为默认值 |
| `Writer.StopTimeout` | `10s` | 非正值回退为默认值 |
| `OnEnqueueFailed` | `nil` | 不设置时，丢弃只打日志 |
| `OnWriteFailed` | `nil` | 不设置时，写失败只打日志 |

## API 参考

### 记录

| 函数 | 说明 |
|------|------|
| `NewRecord(eventType, result)` | 创建记录 |
| `(*Record).With…` | 链式设置（`WithUserID`、`WithIP`、`WithMetadata` 等） |
| `(*Record).Copy()` | 深拷贝 |
| `(*Record).ToJSON()` / `RecordFromJSON(data)` | 序列化 / 反序列化 |

### Logger 与 Writer

| 函数 | 说明 |
|------|------|
| `NewLogger(storage, config)` | 同步 logger |
| `NewLoggerWithWriter(storage, config)` | 基于异步写入器的 logger |
| `(*Logger).Log/LogAuth/LogAccess/LogChallenge` | 写入记录 |
| `(*Logger).Query(ctx, filter)` | 查询记录 |
| `(*Logger).GetStats()` | 队列统计，同步模式返回 `nil` |
| `(*Logger).Stop()` | 排空后关闭存储 |
| `NewWriter(storage, config)` | 单独使用写入器 |
| `(*Writer).Start/Enqueue/Stop/GetStats` | 写入器生命周期 |

### 存储

| 函数 | 说明 |
|------|------|
| `NewFileStorage(path)` | JSON Lines 文件，`Rotate()` 可轮转 |
| `NewDatabaseStorage(url)` | 由 URL 连接 PostgreSQL / MySQL / SQLite，驱动由你注册 |
| `NewDatabaseStorageFromDB(db, dbType, cfg)` | 包装已有 `*sql.DB` |
| `redisstore.New(client)` | Redis，`Cleanup(ctx)` 清理索引 |
| `NewMultiStorage(storages…)` | 扇出到多个后端 |
| `NewNoopStorage()` | 全部丢弃 |
| `NewStorageFromType(type, opts)` | 按配置构造 |
| `(*QueryFilter).Matches(record)` | 内存过滤规则，自定义后端可直接复用 |

### 脱敏

| 函数 | 说明 |
|------|------|
| `MaskEmail(email)` | `u***@example.com` |
| `MaskPhone(phone)` | `138****8000` |
| `MaskIP(ip)` | `192.***.100`（假名化） |
| `MaskString(s, keepChars)` | 保留首尾 `keepChars` 个字符；负数按 `0` 处理 |
| `MaskDestination(dest, channel)` | 按渠道脱敏 |

## 事件类型

| 类别 | 事件类型 | 描述 |
|------|----------|------|
| Challenge | `challenge_created` | OTP 验证创建 |
| Challenge | `challenge_verified` | OTP 验证成功 |
| Challenge | `challenge_revoked` | Challenge 手动撤销 |
| Challenge | `challenge_expired` | Challenge 过期 |
| 发送 | `send_success` | 消息发送成功 |
| 发送 | `send_failed` | 消息发送失败 |
| 验证 | `verification_success` | 验证成功 |
| 验证 | `verification_failed` | 验证失败 |
| 认证 | `login_success` | 登录成功 |
| 认证 | `login_failed` | 登录失败 |
| 认证 | `logout` | 用户登出 |
| 会话 | `session_create` | 会话创建 |
| 会话 | `session_expire` | 会话过期 |
| 授权 | `access_granted` | 访问允许 |
| 授权 | `access_denied` | 访问拒绝 |
| 用户 | `user_created` | 用户创建 |
| 用户 | `user_updated` | 用户更新 |
| 用户 | `user_deleted` | 用户删除 |
| 用户 | `user_locked` | 用户账户锁定 |
| 用户 | `user_unlocked` | 用户账户解锁 |
| 限流 | `rate_limited` | 触发限流 |
| 自定义 | `custom` | 自定义事件 |

结果取值为 `audit.ResultSuccess`、`audit.ResultFailure` 和 `audit.ResultPending`。

## 升级说明（v2.0.0）

所有破坏性改动合并在一个大版本里发布，import 路径只需要改一次，而不是每发一版
改一次。完整说明与实测数字见 [CHANGELOG.md](CHANGELOG.md)。

**1. import 路径变为 `github.com/soulteary/audit-kit/v2`。**

```bash
go get github.com/soulteary/audit-kit/v2
go mod tidy
```

```go
audit "github.com/soulteary/audit-kit/v2"
```

**2. 数据库驱动改由你自己注册。** 此前根包匿名 import 了
`go-sql-driver/mysql` 和 `lib/pq`，于是只把审计日志写到文件的服务也会把两个
驱动链接进来。现在按需补上匿名 import：

```go
import (
    _ "github.com/lib/pq"            // 或 go-sql-driver/mysql、modernc.org/sqlite

    audit "github.com/soulteary/audit-kit/v2"
)
```

`NewDatabaseStorage`、`NewDatabaseStorageWithConfig` 和
`NewDatabaseStorageFromDB` 的签名都没变。驱动没注册时，URL 构造函数会在拨号
之前失败，并写明该补哪个 import。

**3. Redis 迁移到 `redisstore` 子包。**

| v1 | v2 |
|----|----|
| `audit.NewRedisStorage(client)` | `redisstore.New(client)` |
| `audit.NewRedisStorageWithConfig(client, cfg)` | `redisstore.NewWithConfig(client, cfg)` |
| `audit.RedisStorage` | `redisstore.Storage` |
| `audit.RedisConfig` / `audit.DefaultRedisConfig()` | `redisstore.Config` / `redisstore.DefaultConfig()` |
| `StorageOptions.RedisClient` / `RedisPrefix` / `RedisTTL` | `StorageOptions.RedisStorage`，用 `redisstore` 构造 |

没有保留兼容 shim：shim 必须 import go-redis，那样就把收益全部还回去了。

**另外注意：** `redisstore.Storage.Close()` 不再关闭传进来的 Redis 客户端 ——
`MultiStorage.Close` 和 `Logger.Stop` 都会调它，v1 因此会把整个程序的 Redis
连同审计日志一起关掉。请在创建客户端的地方关闭它，或设置
`redisstore.Config.CloseClient` 恢复 v1 行为。

**另外删掉了 `Config.TTL`。** 它自称是 Redis 的 TTL，但从来没有被任何代码读取，
真正生效的一直是 Redis 那边的配置 —— 现在是 `redisstore.Config.TTL`。

**顺带修掉的 bug：** `NewDatabaseStorage` 此前会直接拒绝所有 `postgres://`
URL —— 用 10 字节的切片去比较 11 字节的字面量，永远不可能相等。此前唯一能用的
PostgreSQL 路径是 `NewDatabaseStorageFromDB`。

**新增：** `DatabaseConfig.DriverName`（用于 `pgx`、`sqlite3`、带埋点的包装
驱动）、`sqlite://` 与 `postgresql://` scheme、`QueryFilter.Matches`、
`redisstore.DefaultKeyPrefix` / `DefaultTTL`，以及可接收 cluster、ring、
universal 客户端的 `redisstore.Client` 接口。

**收益**（只 import 根包的程序，对比 v1.10.0）：

| | v1.10.0 | v2.0.0 |
|---|---|---|
| 二进制体积 | 6,080,647 B | 4,703,739 B（−22.6%） |
| 链接的包数 | 225 | 142 |
| 其中非标准库包 | 46 | 9 |
| 使用者 `go.sum` 中的模块数 | 27 | 15 |

## 升级说明（v1.10.0）

仅升级依赖。没有删除任何 API，调用方无需改代码。

- SQLite 驱动为 `modernc.org/sqlite` v1.59.0（此前 v1.58.0）。它自己的依赖没有变动，
  所以 `go.sum` 只改了两行，依赖图的其他部分不受影响。
- 库代码本身不链接它。`modernc.org/sqlite` 是一个 `database/sql` 驱动，只有本模块的
  测试会 import 它；README 把它列为三个可选驱动之一 —— 所以这个新版本只有在你自己
  import 它的时候才会进入你的构建。

## 升级说明（v1.9.0）

仅升级依赖。没有删除任何 API，调用方无需改代码。

- 测试用 Redis 为 `miniredis` v2.39.0（此前 v2.36.1）。
- SQLite 驱动为 `modernc.org/sqlite` v1.58.0（此前 v1.44.3）。

## 升级说明（v1.8.0）

本次修复了异步写入器的两个生命周期缺陷。没有删除任何 API，调用方无需改写代码，
但可观察的行为有变化。

- **关闭时队列里的记录不再丢失。** 原来 `Stop()` 在排空队列**之前**就取消了写入器
  的 context，于是队列里剩下的每条记录都带着已失效的 context 写入、被后端直接拒绝：
  100 条队列一条都没落盘。现在 context 只在 worker 结束后才取消，`StopTimeout`
  约束的是一次真正会写成功的排空。如果你之前按"反正写不成"来设 `StopTimeout`，
  现在要留出足够时间。
- **`Stop()` 之后调用 `Log`/`Enqueue` 不再 panic。** 原来队列会在可能仍有发送在飞行
  时被关闭，特定交错下会 panic "send on closed channel"。现在队列根本不关闭，
  `Stop()` 之后的调用直接返回、不发送。
- **队列满回调变慢或重入不再挂住关闭流程。** `OnEnqueueFailed` 在生命周期锁之外
  调用，因此回调内阻塞——甚至自己调用 `Stop()`——都不会死锁或耗尽 `StopTimeout`。
- **`OnEnqueueFailed` / `OnWriteFailed` 可在 worker 运行中设置。** 这两个字段此前
  无同步写入，与正在读取它们的 worker 竞争。
- **`GetStats().QueueLength` 在 `Stop()` 后报告真实深度。** 原来一旦停止就强制为
  `0`，把超时丢掉的记录数藏了起来。
- **`MaskIP` 正确处理 IPv4 映射的 IPv6。** `::ffff:192.168.1.1` 此前脱敏为
  `::ffff:192.***.1`，暴露超出预期；现在结果是 `192.***.1`。如果你的断言依赖脱敏
  输出，请复查。
- **`MaskString` 传入负数 `keepChars` 返回全脱敏字符串**，不再因切片越界 panic。
- **表名按 ASCII `[a-zA-Z0-9_]` 校验。** `validateTableName` 文档写的是这个集合，
  实际用的是 `unicode.IsLetter`/`IsNumber`，因此西里尔字母、全角数字都能通过，生成
  的标识符在若干引擎里需要加引号。现在非 ASCII 的 `DatabaseConfig.TableName` 会在
  构造阶段被拒绝。

## 安全与运维说明

- **文件存储**：只向 `NewFileStorage` 传可信路径。用户可控路径可造成路径穿越，
  符号链接可把日志重定向到别处。
- **数据库错误**：不要原样打印数据库存储返回的 `err.Error()`——驱动可能带上 DSN，
  也就带上了密码。打固定文案或判断错误类型。
- **被丢弃的记录**：队列满时按设计丢弃。请设置 `OnEnqueueFailed` 并按峰值调整
  `QueueSize`；一条都不能丢的场景请用同步 logger。
- **Redis**：关闭存储不再关闭客户端，请在创建它的地方关闭，或设置
  `redisstore.Config.CloseClient`。另外给记录设置 `EventID` 或 `ChallengeID`
  以保证键唯一，并定期执行
  `Cleanup()`——索引键本身没有 TTL。
- **元数据**：JSON 往返后数字是 `float64`，类型断言时注意。

## 项目结构

```
audit-kit/
├── doc.go       # 包文档与分层说明
├── types.go     # Record、事件/结果常量、记录选项
├── storage.go   # Storage 接口、QueryFilter 与 Matches 规则
├── logger.go    # Logger、Config、DefaultConfig
├── writer.go    # 异步写入器、worker 池、生命周期
├── file.go      # 文件存储（JSON Lines）
├── database.go  # 数据库存储（PostgreSQL/MySQL/SQLite），不 import 驱动
├── factory.go   # 存储工厂与多存储
├── mask.go      # 脱敏工具
├── redisstore/  # Redis 存储 —— 唯一 import go-redis 的包
└── integrationtest/ # 连真实 PostgreSQL / MySQL 的测试，带构建标签
```

## 测试

```bash
go test ./...

# 带覆盖率
go test ./... -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out
go tool cover -html=coverage.out -o coverage.html

# 连真实数据库（没设置 URL 时自动跳过）
TEST_POSTGRES_URL=postgres://… go test -tags=integration ./integrationtest/...
TEST_MYSQL_URL=user:pass@tcp(localhost:3306)/db go test -tags=integration ./integrationtest/...
```

少数测试用 `chmod` 模拟 I/O 失败，而 uid 0 会忽略权限位，因此以 root 运行时这些测试
会跳过。

## 贡献

1. Fork 本仓库
2. 创建特性分支（`git checkout -b feature/amazing-feature`）
3. 提交改动（`git commit -m 'Add some amazing feature'`）
4. 推送分支（`git push origin feature/amazing-feature`）
5. 发起 Pull Request

## 许可证

Apache License 2.0 —— 详见 [LICENSE](LICENSE)。
