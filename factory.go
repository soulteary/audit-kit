package audit

import (
	"context"
	"fmt"
	"strings"
)

// StorageType represents the type of storage backend
type StorageType string

const (
	StorageTypeFile     StorageType = "file"
	StorageTypeDatabase StorageType = "database"
	StorageTypeDB       StorageType = "db" // Alias for database
	StorageTypeRedis    StorageType = "redis"
	StorageTypeNone     StorageType = "none"
)

// StorageOptions holds options for creating storage
type StorageOptions struct {
	// File storage options
	FilePath string

	// Database storage options
	DatabaseURL string
	TableName   string
	// DriverName overrides the database/sql driver name derived from
	// DatabaseURL's scheme; see [DatabaseConfig.DriverName].
	DriverName string

	// RedisStorage is a Redis-backed storage for [StorageTypeRedis]. Build it
	// with the redisstore subpackage and assign it here:
	//
	//	opts.RedisStorage = redisstore.New(client)
	//
	// The root package keeps no go-redis types of its own, so that a program
	// which never selects Redis does not link go-redis. The field is a
	// [Storage], so any backend can stand in for Redis in a test.
	RedisStorage Storage
}

// NewStorageFromType creates a storage instance based on type
func NewStorageFromType(storageType StorageType, opts *StorageOptions) (Storage, error) {
	if opts == nil {
		opts = &StorageOptions{}
	}

	switch storageType {
	case StorageTypeFile:
		if opts.FilePath == "" {
			return nil, fmt.Errorf("file path is required for file storage")
		}
		return NewFileStorage(opts.FilePath)

	case StorageTypeDatabase, StorageTypeDB:
		if opts.DatabaseURL == "" {
			return nil, fmt.Errorf("database URL is required for database storage")
		}
		config := &DatabaseConfig{
			TableName:  opts.TableName,
			DriverName: opts.DriverName,
		}
		if config.TableName == "" {
			config.TableName = "audit_logs"
		}
		return NewDatabaseStorageWithConfig(opts.DatabaseURL, config)

	case StorageTypeRedis:
		if opts.RedisStorage == nil {
			return nil, fmt.Errorf("redis storage is required for redis storage: build it with the redisstore subpackage (redisstore.New(client)) and set StorageOptions.RedisStorage")
		}
		return opts.RedisStorage, nil

	case StorageTypeNone, "":
		return nil, nil

	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}

// ParseStorageType parses a storage type string
func ParseStorageType(s string) StorageType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "file":
		return StorageTypeFile
	case "database", "db":
		return StorageTypeDatabase
	case "redis":
		return StorageTypeRedis
	case "none", "":
		return StorageTypeNone
	default:
		return StorageType(s)
	}
}

// MultiStorage combines multiple storage backends
type MultiStorage struct {
	storages []Storage
}

// NewMultiStorage creates a storage that writes to multiple backends
func NewMultiStorage(storages ...Storage) *MultiStorage {
	return &MultiStorage{
		storages: storages,
	}
}

// Write writes to all storage backends
func (m *MultiStorage) Write(ctx context.Context, record *Record) error {
	var firstErr error
	for _, s := range m.storages {
		if s == nil {
			continue
		}
		if err := s.Write(ctx, record); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Query queries from the first storage backend
func (m *MultiStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	for _, s := range m.storages {
		if s == nil {
			continue
		}
		return s.Query(ctx, filter)
	}
	return nil, fmt.Errorf("no storage configured")
}

// Close closes all storage backends
func (m *MultiStorage) Close() error {
	var firstErr error
	for _, s := range m.storages {
		if s == nil {
			continue
		}
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Storages returns all storage backends
func (m *MultiStorage) Storages() []Storage {
	return m.storages
}

// NoopStorage is a no-op storage that discards all records
type NoopStorage struct{}

// NewNoopStorage creates a new no-op storage
func NewNoopStorage() *NoopStorage {
	return &NoopStorage{}
}

// Write does nothing
func (s *NoopStorage) Write(ctx context.Context, record *Record) error {
	return nil
}

// Query returns empty results
func (s *NoopStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	return []*Record{}, nil
}

// Close does nothing
func (s *NoopStorage) Close() error {
	return nil
}
