package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStorage implements Storage interface for Redis-based audit logging
// Suitable for short-term storage and quick access
type RedisStorage struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
}

// RedisConfig holds configuration for Redis storage
type RedisConfig struct {
	KeyPrefix string        // Key prefix (default: "audit:")
	TTL       time.Duration // Time-to-live for records (default: 7 days)
}

// DefaultRedisConfig returns default Redis configuration
func DefaultRedisConfig() *RedisConfig {
	return &RedisConfig{
		KeyPrefix: "audit:",
		TTL:       7 * 24 * time.Hour, // 7 days
	}
}

// NewRedisStorage creates a new Redis storage instance
func NewRedisStorage(client *redis.Client) *RedisStorage {
	return NewRedisStorageWithConfig(client, nil)
}

// NewRedisStorageWithConfig creates a new Redis storage instance with config
func NewRedisStorageWithConfig(client *redis.Client, config *RedisConfig) *RedisStorage {
	if config == nil {
		config = DefaultRedisConfig()
	}

	if config.KeyPrefix == "" {
		config.KeyPrefix = "audit:"
	}
	if config.TTL <= 0 {
		config.TTL = 7 * 24 * time.Hour
	}

	return &RedisStorage{
		client:    client,
		keyPrefix: config.KeyPrefix,
		ttl:       config.TTL,
	}
}

// Write writes an audit record to Redis
func (s *RedisStorage) Write(ctx context.Context, record *Record) error {
	// Generate key: prefix:{timestamp}:{id}
	var key string
	if record.EventID != "" {
		key = fmt.Sprintf("%s%d:%s", s.keyPrefix, record.Timestamp, record.EventID)
	} else if record.ChallengeID != "" {
		key = fmt.Sprintf("%s%d:%s", s.keyPrefix, record.Timestamp, record.ChallengeID)
	} else if record.UserID != "" {
		key = fmt.Sprintf("%s%d:%s", s.keyPrefix, record.Timestamp, record.UserID)
	} else {
		key = fmt.Sprintf("%s%d", s.keyPrefix, record.Timestamp)
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
	setKey := s.keyPrefix + "index"
	member := redis.Z{
		Score:  float64(record.Timestamp),
		Member: key,
	}
	if err := s.client.ZAdd(ctx, setKey, member).Err(); err != nil {
		return fmt.Errorf("failed to add to sorted set: %w", err)
	}

	return nil
}

// Query queries audit records from Redis
func (s *RedisStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	if filter == nil {
		filter = DefaultQueryFilter()
	}
	filter.Normalize()

	// Get keys from sorted set
	setKey := s.keyPrefix + "index"

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

	// Get keys in descending order (newest first)
	keys, err := s.client.ZRevRangeByScore(ctx, setKey, &redis.ZRangeBy{
		Min:    min,
		Max:    max,
		Offset: 0,
		Count:  int64(filter.Limit + filter.Offset + 100), // Get extra for filtering
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get keys: %w", err)
	}

	if len(keys) == 0 {
		return []*Record{}, nil
	}

	// Get records
	var records []*Record
	for _, key := range keys {
		data, err := s.client.Get(ctx, key).Bytes()
		if err != nil {
			if err == redis.Nil {
				// Key expired, remove from index
				_ = s.client.ZRem(ctx, setKey, key)
				continue
			}
			continue
		}

		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			continue
		}

		// Apply filters
		if !matchesFilter(&record, filter) {
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
		return []*Record{}, nil
	}

	end := start + filter.Limit
	if end > len(records) {
		end = len(records)
	}

	return records[start:end], nil
}

// Close closes the Redis connection
func (s *RedisStorage) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// Client returns the underlying Redis client
func (s *RedisStorage) Client() *redis.Client {
	return s.client
}

// KeyPrefix returns the key prefix
func (s *RedisStorage) KeyPrefix() string {
	return s.keyPrefix
}

// TTL returns the TTL
func (s *RedisStorage) TTL() time.Duration {
	return s.ttl
}

// Cleanup removes expired keys from the index
func (s *RedisStorage) Cleanup(ctx context.Context) (int64, error) {
	setKey := s.keyPrefix + "index"

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
