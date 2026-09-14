# audit-kit

[![Go Reference](https://pkg.go.dev/badge/github.com/soulteary/audit-kit.svg)](https://pkg.go.dev/github.com/soulteary/audit-kit)
[![Go Report Card](.github/goreportcard.svg)](.github/goreportcard-report.md)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![codecov](https://codecov.io/gh/soulteary/audit-kit/graph/badge.svg)](https://codecov.io/gh/soulteary/audit-kit)

[English](README.md)

Go 服务的统一审计日志工具包。提供统一的存储接口（文件、数据库、Redis）、带
worker 池和有界队列的异步写入、链式记录构造器，以及敏感字段脱敏。

## 特性

- **存储接口**：所有后端共用一套 `Write`/`Query`/`Close` 接口
- **多种后端**：文件（JSON Lines）、数据库（PostgreSQL/MySQL/SQLite）、Redis、空实现
- **异步写入**：worker 池 + 有界队列，写日志不阻塞请求
- **可靠关闭**：`Stop()` 先把队列排空写完，之后才取消 context
- **多存储写入**：一条记录同时写入多个后端
- **数据脱敏**：邮箱、手机号、IP 以及通用字符串脱敏
- **链式 API**：记录用构造器，便捷方法用函数式选项
- **查询支持**：过滤与分页
- **可扩展**：自定义事件类型与任意元数据

## 要求

- **Go 1.27+**（`go.mod` 声明 `go 1.27.0`）
- 可选：`github.com/redis/go-redis/v9`（Redis 存储）
- 可选：`github.com/go-sql-driver/mysql`、`github.com/lib/pq` 或
  `modernc.org/sqlite`（数据库存储）

## 安装

```bash
go get github.com/soulteary/audit-kit
```

## 快速开始

```go
package main

import (
    "context"
    "log"

    audit "github.com/soulteary/audit-kit"
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

```go
// PostgreSQL
storage, err := audit.NewDatabaseStorage("postgres://user:pass@localhost/db")

// MySQL
storage, err := audit.NewDatabaseStorage("mysql://user:pass@tcp(localhost:3306)/db")

// 复用已有的 *sql.DB（测试很方便）
db, _ := sql.Open("sqlite", ":memory:")
storage, err := audit.NewDatabaseStorageFromDB(db, "sqlite", nil)

// 自定义表名 —— 只允许 ASCII 字母、数字和下划线，最长 64 字符
storage, err := audit.NewDatabaseStorageWithConfig(dsn, &audit.DatabaseConfig{
    TableName: "audit_records",
})
```

### Redis 存储

```go
import "github.com/redis/go-redis/v9"

client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

storage := audit.NewRedisStorageWithConfig(client, &audit.RedisConfig{
    KeyPrefix: "myapp:audit:",
    TTL:       7 * 24 * time.Hour,
})

// 索引键本身没有 TTL，需要定期清理过期引用。
removed, err := storage.Cleanup(ctx)
```

### 多存储写入

```go
fileStorage, _ := audit.NewFileStorage("/var/log/audit.log")
redisStorage := audit.NewRedisStorage(redisClient)

multi := audit.NewMultiStorage(fileStorage, redisStorage)
logger := audit.NewLogger(multi, nil)
```

### 按配置构造存储

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
    TTL:             7 * 24 * time.Hour, // Redis 存储的 TTL
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
| `TTL` | `168h`（7 天） | 仅 Redis 有效 |
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
| `NewDatabaseStorage(url)` | 由 DSN 连接 PostgreSQL / MySQL |
| `NewDatabaseStorageFromDB(db, dbType, cfg)` | 包装已有 `*sql.DB` |
| `NewRedisStorage(client)` | Redis，`Cleanup(ctx)` 清理索引 |
| `NewMultiStorage(storages…)` | 扇出到多个后端 |
| `NewNoopStorage()` | 全部丢弃 |
| `NewStorageFromType(type, opts)` | 按配置构造 |

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
- **Redis**：给记录设置 `EventID` 或 `ChallengeID` 以保证键唯一，并定期执行
  `Cleanup()`——索引键本身没有 TTL。
- **元数据**：JSON 往返后数字是 `float64`，类型断言时注意。

## 项目结构

```
audit-kit/
├── types.go     # Record、事件/结果常量、记录选项
├── storage.go   # Storage 接口与 QueryFilter
├── logger.go    # Logger、Config、DefaultConfig
├── writer.go    # 异步写入器、worker 池、生命周期
├── file.go      # 文件存储（JSON Lines）
├── database.go  # 数据库存储（PostgreSQL/MySQL/SQLite）
├── redis.go     # Redis 存储
├── factory.go   # 存储工厂与多存储
└── mask.go      # 脱敏工具
```

## 测试

```bash
go test ./...

# 带覆盖率
go test ./... -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out
go tool cover -html=coverage.out -o coverage.html
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
