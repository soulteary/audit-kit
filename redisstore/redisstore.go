// Package redisstore stores audit records in Redis for audit-kit.
//
// It lives in its own package so that importing the root package does not drag
// go-redis -- and with it cespare/xxhash and go.uber.org/atomic -- into
// binaries that never talk to Redis. A service that writes its audit log to a
// file or to Postgres pays nothing for Redis support existing; only importing
// this package links it in.
//
// The store is a translation layer and nothing more: what a record contains,
// how a query filter is interpreted and when a record is masked all live in the
// root package.
//
//	store := redisstore.New(redisClient)
//	logger := audit.NewLoggerWithWriter(store, audit.DefaultConfig())
//
// Records are short-lived by design. Each one is a key with a TTL, indexed in a
// sorted set by timestamp, so Redis is the right backend for "what happened in
// the last few days" and the wrong one for retention that has to outlive a
// [DefaultTTL] window. Pair it with a file or database store through
// [github.com/soulteary/audit-kit/v2.NewMultiStorage] when both are needed.
package redisstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"

	audit "github.com/soulteary/audit-kit/v2"
)

const (
	// DefaultKeyPrefix is the prefix every key and the index share when
	// [Config.KeyPrefix] is not set.
	DefaultKeyPrefix = "audit:"

	// DefaultTTL is how long a record survives when [Config.TTL] is not set.
	DefaultTTL = 7 * 24 * time.Hour
)

// ErrNilClient is returned by every operation when the store was built with no
// client. It is an error rather than a panic because an audit backend that
// takes the process down is worse than the lost record it was meant to report.
var ErrNilClient = errors.New("redisstore: redis client is nil")

// Client is the part of a go-redis client this store uses. *redis.Client,
// *redis.ClusterClient, *redis.Ring and redis.UniversalClient all satisfy it,
// so a store written against it works the same on a standalone server, a
// cluster and a Sentinel failover setup.
//
// Every method here is part of redis.Cmdable, which means an instrumented or
// wrapped client satisfies it too.
type Client interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	ZRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	ZRangeArgs(ctx context.Context, z redis.ZRangeArgs) *redis.StringSliceCmd
	ZRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
}

// Storage implements audit.Storage on top of Redis. It is suitable for
// short-term storage and quick access.
type Storage struct {
	client      Client
	keyPrefix   string
	ttl         time.Duration
	closeClient bool
}

// Config holds configuration for Redis storage.
type Config struct {
	KeyPrefix string        // Key prefix (default: DefaultKeyPrefix)
	TTL       time.Duration // Time-to-live for records (default: DefaultTTL)

	// CloseClient makes [Storage.Close] close the underlying client.
	//
	// It is off by default: a Redis client is normally shared with the rest of
	// the program, and closing it from an audit store -- which
	// audit.MultiStorage and audit.Logger.Stop both do on shutdown -- takes
	// the rest of the program's Redis with it. Set it when the store owns the
	// client. This reverses v1's behaviour, where RedisStorage.Close() always
	// closed the client.
	CloseClient bool
}

// DefaultConfig returns default Redis configuration.
func DefaultConfig() *Config {
	return &Config{
		KeyPrefix: DefaultKeyPrefix,
		TTL:       DefaultTTL,
	}
}

// New creates a new Redis storage instance with the default configuration.
func New(client Client) *Storage {
	return NewWithConfig(client, nil)
}

// NewWithConfig creates a new Redis storage instance with config. A nil config
// means [DefaultConfig]; a zero KeyPrefix or a non-positive TTL falls back to
// the default for that field alone.
func NewWithConfig(client Client, config *Config) *Storage {
	if config == nil {
		config = DefaultConfig()
	}

	keyPrefix := config.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = DefaultKeyPrefix
	}
	ttl := config.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}

	return &Storage{
		client:      client,
		keyPrefix:   keyPrefix,
		ttl:         ttl,
		closeClient: config.CloseClient,
	}
}

// Write writes an audit record to Redis.
func (s *Storage) Write(ctx context.Context, record *audit.Record) error {
	if isNil(s.client) {
		return ErrNilClient
	}

	// Generate key: prefix:{timestamp}:{id} so same-second records do not overwrite
	var key string
	if record.EventID != "" {
		key = fmt.Sprintf("%s%d:%s", s.keyPrefix, record.Timestamp, record.EventID)
	} else if record.ChallengeID != "" {
		key = fmt.Sprintf("%s%d:%s", s.keyPrefix, record.Timestamp, record.ChallengeID)
	} else if record.UserID != "" {
		key = fmt.Sprintf("%s%d:%s", s.keyPrefix, record.Timestamp, record.UserID)
	} else {
		// No IDs: use unique suffix to avoid overwriting records in the same second
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("failed to generate key: %w", err)
		}
		key = fmt.Sprintf("%s%d:%s", s.keyPrefix, record.Timestamp, hex.EncodeToString(b))
	}

	// Marshal record to JSON
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal record: %w", err)
	}

	// Store with TTL
	if err := s.client.Set(ctx, key, data, s.ttl).Err(); err != nil {
		return fmt.Errorf("failed to set key: %w", err)
	}

	// Also add to sorted set for efficient querying
	setKey := s.indexKey()
	member := redis.Z{
		Score:  float64(record.Timestamp),
		Member: key,
	}
	if err := s.client.ZAdd(ctx, setKey, member).Err(); err != nil {
		return fmt.Errorf("failed to add to sorted set: %w", err)
	}

	return nil
}

// Query queries audit records from Redis.
func (s *Storage) Query(ctx context.Context, filter *audit.QueryFilter) ([]*audit.Record, error) {
	if isNil(s.client) {
		return nil, ErrNilClient
	}

	if filter == nil {
		filter = audit.DefaultQueryFilter()
	}
	filter.Normalize()

	// Get keys from sorted set
	setKey := s.indexKey()

	var min, max string
	if filter.StartTime > 0 {
		min = fmt.Sprintf("%d", filter.StartTime)
	} else {
		min = "-inf"
	}
	if filter.EndTime > 0 {
		max = fmt.Sprintf("%d", filter.EndTime)
	} else {
		max = "+inf"
	}

	// Get keys in descending order (newest first); ZRangeArgs replaces deprecated ZRevRangeByScore (Redis 6.2+).
	// With Rev+ByScore the range bounds are reversed: Start is the high score, Stop is the low score.
	keys, err := s.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     setKey,
		Start:   max,
		Stop:    min,
		ByScore: true,
		Rev:     true,
		Offset:  0,
		Count:   int64(filter.Limit + filter.Offset + 100), // Get extra for filtering
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get keys: %w", err)
	}

	if len(keys) == 0 {
		return []*audit.Record{}, nil
	}

	// Get records
	var records []*audit.Record
	for _, key := range keys {
		data, err := s.client.Get(ctx, key).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				// Key expired, remove from index
				_ = s.client.ZRem(ctx, setKey, key)
				continue
			}
			continue
		}

		var record audit.Record
		if err := json.Unmarshal(data, &record); err != nil {
			continue
		}

		// Apply filters
		if !filter.Matches(&record) {
			continue
		}

		records = append(records, &record)
	}

	// Sort by timestamp descending
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp > records[j].Timestamp
	})

	// Apply pagination
	start := filter.Offset
	if start >= len(records) {
		return []*audit.Record{}, nil
	}

	end := start + filter.Limit
	if end > len(records) {
		end = len(records)
	}

	return records[start:end], nil
}

// Close releases the store. It closes the underlying client only when the
// store was built with [Config.CloseClient]; see that field for why a shared
// client is left alone by default.
func (s *Storage) Close() error {
	if !s.closeClient || isNil(s.client) {
		return nil
	}
	if closer, ok := s.client.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Client returns the underlying Redis client.
func (s *Storage) Client() Client {
	return s.client
}

// KeyPrefix returns the key prefix.
func (s *Storage) KeyPrefix() string {
	return s.keyPrefix
}

// TTL returns the TTL.
func (s *Storage) TTL() time.Duration {
	return s.ttl
}

// Cleanup removes from the index the entries whose record has already expired,
// and reports how many it removed. Queries skip such entries and drop them one
// by one, so this is an optimisation for an index that has grown large, not a
// correctness requirement.
func (s *Storage) Cleanup(ctx context.Context) (int64, error) {
	if isNil(s.client) {
		return 0, ErrNilClient
	}

	setKey := s.indexKey()

	// Get all keys from index
	keys, err := s.client.ZRange(ctx, setKey, 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get keys: %w", err)
	}

	var removed int64
	for _, key := range keys {
		exists, err := s.client.Exists(ctx, key).Result()
		if err != nil {
			continue
		}
		if exists == 0 {
			if err := s.client.ZRem(ctx, setKey, key).Err(); err == nil {
				removed++
			}
		}
	}

	return removed, nil
}

// indexKey is the sorted set holding every record key by timestamp.
func (s *Storage) indexKey() string {
	return s.keyPrefix + "index"
}

// isNil reports whether there is no client to talk to. Client is an interface,
// so a plain c == nil misses the case that actually reaches here -- a nil
// *redis.Client stored in it, which a caller gets from an unassigned field or
// a constructor that returned early. Calling a method on that panics.
func isNil(c Client) bool {
	if c == nil {
		return true
	}
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
