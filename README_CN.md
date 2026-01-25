# audit-kit

[![Go Reference](https://pkg.go.dev/badge/github.com/soulteary/audit-kit.svg)](https://pkg.go.dev/github.com/soulteary/audit-kit)
[![Go Report Card](https://goreportcard.com/badge/github.com/soulteary/audit-kit)](https://goreportcard.com/report/github.com/soulteary/audit-kit)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![codecov](https://codecov.io/gh/soulteary/audit-kit/graph/badge.svg)](https://codecov.io/gh/soulteary/audit-kit)

[English](README.md)

统一的 Go 服务审计日志工具包。提供审计日志接口、多存储后端（文件、数据库、Redis）、异步写入工作池以及敏感数据脱敏功能。

## 特性

- **存储接口**：所有存储后端的统一接口
- **多后端支持**：文件（JSON Lines）、数据库（PostgreSQL/MySQL/SQLite）、Redis
- **异步写入**：用于非阻塞审计日志的工作池
- **多存储写入**：同时写入多个存储后端
- **数据脱敏**：自动脱敏敏感数据（邮箱、手机号、IP）
- **流式 API**：用于构建审计记录的构建器模式
- **查询支持**：过滤和分页审计记录
- **可扩展**：自定义事件类型和元数据支持

## 安装

```bash
go get github.com/soulteary/audit-kit
```

## 使用

### 基础审计日志

```go
import (
    audit "github.com/soulteary/audit-kit"
)

// 创建文件存储
storage, err := audit.NewFileStorage("/var/log/audit.log")
if err != nil {
    log.Fatal(err)
}

// 使用默认配置创建日志记录器
logger := audit.NewLogger(storage, nil)
defer logger.Stop()

// 记录事件
record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
    WithUserID("user123").
    WithIP("192.168.1.1").
    WithUserAgent("Mozilla/5.0")

logger.Log(context.Background(), record)
```

### 异步写入（生产环境推荐）

```go
// 创建带异步写入器的日志记录器
config := audit.DefaultConfig()
config.Writer = &audit.WriterConfig{
    QueueSize: 1000,
    Workers:   4,
}

logger := audit.NewLoggerWithWriter(storage, config)
defer logger.Stop()

// 日志记录现在是非阻塞的
logger.Log(ctx, record)
```

### 数据库存储

```go
// PostgreSQL
storage, err := audit.NewDatabaseStorage("postgres://user:pass@localhost/db")

// MySQL
storage, err := audit.NewDatabaseStorage("mysql://user:pass@tcp(localhost:3306)/db")

// SQLite（用于测试）
db, _ := sql.Open("sqlite", ":memory:")
storage, err := audit.NewDatabaseStorageFromDB(db, "sqlite", nil)
```

### Redis 存储

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

### 多存储写入（写入多个后端）

```go
fileStorage, _ := audit.NewFileStorage("/var/log/audit.log")
redisStorage := audit.NewRedisStorage(redisClient)

multiStorage := audit.NewMultiStorage(fileStorage, redisStorage)
logger := audit.NewLogger(multiStorage, nil)
```

### 查询审计记录

```go
// 构建过滤器
filter := audit.DefaultQueryFilter().
    WithEventType("login_success").
    WithUserID("user123").
    WithTimeRange(startTime, endTime).
    WithLimit(50)

// 查询记录
records, err := logger.Query(ctx, filter)
```

### 便捷日志方法

```go
// 记录 challenge 事件（OTP/验证）
logger.LogChallenge(ctx, audit.EventChallengeCreated, "ch_123", "user123", audit.ResultSuccess,
    audit.WithRecordChannel("email"),
    audit.WithRecordDestination("test@example.com"),
)

// 记录认证事件
logger.LogAuth(ctx, audit.EventLoginSuccess, "user123", audit.ResultSuccess,
    audit.WithRecordIP("192.168.1.1"),
    audit.WithRecordUserAgent("Mozilla/5.0"),
)

// 记录访问控制事件
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
// 日志记录时自动脱敏
config := audit.DefaultConfig()
config.MaskDestination = true // 默认启用

logger := audit.NewLogger(storage, config)

// 手动脱敏
masked := audit.MaskEmail("user@example.com")    // u***@example.com
masked = audit.MaskPhone("13800138000")          // 138****8000
masked = audit.MaskIP("192.168.1.100")           // 192.***.100
```

### 日志回调（用于标准日志）

```go
logger.SetLogCallback(func(record *audit.Record) {
    log.Printf("[AUDIT] %s user=%s result=%s",
        record.EventType, record.UserID, record.Result)
})
```

## 事件类型

### 内置事件类型

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

## 配置

```go
config := &audit.Config{
    Enabled:         true,                    // 启用/禁用日志
    MaskDestination: true,                    // 在日志中脱敏手机号/邮箱
    TTL:             7 * 24 * time.Hour,      // Redis 存储的 TTL
    Writer: &audit.WriterConfig{
        QueueSize:   1000,                    // 异步队列大小
        Workers:     2,                       // 工作线程数
        StopTimeout: 10 * time.Second,        // 优雅关闭超时
    },
}
```

## 项目结构

```
audit-kit/
├── types.go           # 记录类型和事件定义
├── storage.go         # 存储接口和查询过滤器
├── logger.go          # 支持异步的日志记录器
├── writer.go          # 带工作池的异步写入器
├── file.go            # 文件存储（JSON Lines）
├── database.go        # 数据库存储（PostgreSQL/MySQL/SQLite）
├── redis.go           # Redis 存储
├── factory.go         # 存储工厂和多存储
├── mask.go            # 数据脱敏工具
└── *_test.go          # 完整测试
```

## 集成示例

### Herald（OTP 服务）

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

    // 记录 OTP challenge 创建
    logger.LogChallenge(ctx, audit.EventChallengeCreated, challengeID, userID, audit.ResultSuccess,
        audit.WithRecordChannel("sms"),
        audit.WithRecordDestination(phone),
        audit.WithRecordIP(clientIP),
        audit.WithRecordProvider("aliyun", messageID),
    )

    // 记录验证结果
    logger.LogChallenge(ctx, audit.EventChallengeVerified, challengeID, userID, audit.ResultSuccess)
}
```

### Stargate（认证网关）

```go
package main

import (
    audit "github.com/soulteary/audit-kit"
)

func main() {
    storage, _ := audit.NewDatabaseStorage("postgres://...")
    logger := audit.NewLoggerWithWriter(storage, nil)
    defer logger.Stop()

    // 记录成功登录
    logger.LogAuth(ctx, audit.EventLoginSuccess, userID, audit.ResultSuccess,
        audit.WithRecordIP(clientIP),
        audit.WithRecordUserAgent(userAgent),
        audit.WithRecordMetadata("auth_method", "otp"),
    )

    // 记录访问控制
    logger.LogAccess(ctx, audit.EventAccessGranted, userID, requestPath, audit.ResultSuccess)
}
```

### Warden（用户服务）

```go
package main

import (
    audit "github.com/soulteary/audit-kit"
)

func main() {
    storage := audit.NewRedisStorage(redisClient)
    logger := audit.NewLogger(storage, nil)
    defer logger.Stop()

    // 记录用户查询
    logger.Log(ctx, audit.NewRecord(audit.EventCustom, audit.ResultSuccess).
        WithUserID(userID).
        WithMetadata("action", "user_lookup").
        WithMetadata("query_type", "email"),
    )
}
```

## 要求

- Go 1.25 或更高版本
- 可选：github.com/redis/go-redis/v9（用于 Redis 存储）
- 可选：github.com/go-sql-driver/mysql 或 github.com/lib/pq（用于数据库存储）

## 测试覆盖率

运行测试：

```bash
go test ./... -v

# 带覆盖率
go test ./... -coverprofile=coverage.out -covermode=atomic
go tool cover -html=coverage.out -o coverage.html
go tool cover -func=coverage.out
```

## 贡献

1. Fork 本仓库
2. 创建功能分支 (`git checkout -b feature/amazing-feature`)
3. 提交更改 (`git commit -m 'Add some amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 提交 Pull Request

## 许可证

详见 [LICENSE](LICENSE) 文件。
