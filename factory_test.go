package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStorageFromType_File(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "audit.log")

	storage, err := NewStorageFromType(StorageTypeFile, &StorageOptions{
		FilePath: filePath,
	})
	require.NoError(t, err)
	require.NotNil(t, storage)
	defer func() { _ = storage.Close() }()

	_, ok := storage.(*FileStorage)
	assert.True(t, ok)
}

func TestNewStorageFromType_File_NoPath(t *testing.T) {
	_, err := NewStorageFromType(StorageTypeFile, &StorageOptions{})
	assert.Error(t, err)
}

func TestNewStorageFromType_Redis(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = client.Close() }()

	storage, err := NewStorageFromType(StorageTypeRedis, &StorageOptions{
		RedisClient: client,
		RedisPrefix: "test:audit:",
		RedisTTL:    24 * time.Hour,
	})
	require.NoError(t, err)
	require.NotNil(t, storage)

	_, ok := storage.(*RedisStorage)
	assert.True(t, ok)
}

func TestNewStorageFromType_Redis_NoClient(t *testing.T) {
	_, err := NewStorageFromType(StorageTypeRedis, &StorageOptions{})
	assert.Error(t, err)
}

func TestNewStorageFromType_Database_NoURL(t *testing.T) {
	_, err := NewStorageFromType(StorageTypeDatabase, &StorageOptions{})
	assert.Error(t, err)
}

func TestNewStorageFromType_None(t *testing.T) {
	storage, err := NewStorageFromType(StorageTypeNone, nil)
	assert.NoError(t, err)
	assert.Nil(t, storage)
}

func TestNewStorageFromType_Empty(t *testing.T) {
	storage, err := NewStorageFromType("", nil)
	assert.NoError(t, err)
	assert.Nil(t, storage)
}

func TestNewStorageFromType_Invalid(t *testing.T) {
	_, err := NewStorageFromType("invalid", nil)
	assert.Error(t, err)
}

func TestParseStorageType(t *testing.T) {
	tests := []struct {
		input    string
		expected StorageType
	}{
		{"file", StorageTypeFile},
		{"FILE", StorageTypeFile},
		{" File ", StorageTypeFile},
		{"database", StorageTypeDatabase},
		{"db", StorageTypeDatabase},
		{"redis", StorageTypeRedis},
		{"none", StorageTypeNone},
		{"", StorageTypeNone},
		{"unknown", StorageType("unknown")},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseStorageType(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMultiStorage(t *testing.T) {
	tempDir := t.TempDir()

	// Create two file storages
	storage1, err := NewFileStorage(filepath.Join(tempDir, "audit1.log"))
	require.NoError(t, err)

	storage2, err := NewFileStorage(filepath.Join(tempDir, "audit2.log"))
	require.NoError(t, err)

	multi := NewMultiStorage(storage1, storage2)
	defer func() { _ = multi.Close() }()

	// Write should go to both
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = multi.Write(context.Background(), record)
	require.NoError(t, err)

	// Check both files have the record
	data1, err := os.ReadFile(filepath.Join(tempDir, "audit1.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data1), "login_success")

	data2, err := os.ReadFile(filepath.Join(tempDir, "audit2.log"))
	require.NoError(t, err)
	assert.Contains(t, string(data2), "login_success")
}

func TestMultiStorage_Query(t *testing.T) {
	tempDir := t.TempDir()

	storage1, err := NewFileStorage(filepath.Join(tempDir, "audit1.log"))
	require.NoError(t, err)

	storage2, err := NewFileStorage(filepath.Join(tempDir, "audit2.log"))
	require.NoError(t, err)

	// Write to first storage only
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = storage1.Write(context.Background(), record)
	require.NoError(t, err)

	multi := NewMultiStorage(storage1, storage2)
	defer func() { _ = multi.Close() }()

	// Query should return from first storage
	results, err := multi.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestMultiStorage_Query_NoStorage(t *testing.T) {
	multi := NewMultiStorage()
	_, err := multi.Query(context.Background(), DefaultQueryFilter())
	assert.Error(t, err)
}

func TestMultiStorage_WithNil(t *testing.T) {
	tempDir := t.TempDir()

	storage, err := NewFileStorage(filepath.Join(tempDir, "audit.log"))
	require.NoError(t, err)

	multi := NewMultiStorage(nil, storage, nil)
	defer func() { _ = multi.Close() }()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = multi.Write(context.Background(), record)
	require.NoError(t, err)

	results, err := multi.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestMultiStorage_Storages(t *testing.T) {
	tempDir := t.TempDir()

	storage1, err := NewFileStorage(filepath.Join(tempDir, "audit1.log"))
	require.NoError(t, err)

	storage2, err := NewFileStorage(filepath.Join(tempDir, "audit2.log"))
	require.NoError(t, err)

	multi := NewMultiStorage(storage1, storage2)
	defer func() { _ = multi.Close() }()

	storages := multi.Storages()
	assert.Len(t, storages, 2)
}

func TestNoopStorage(t *testing.T) {
	storage := NewNoopStorage()

	// Write should succeed
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err := storage.Write(context.Background(), record)
	assert.NoError(t, err)

	// Query should return empty
	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	assert.NoError(t, err)
	assert.Len(t, results, 0)

	// Close should succeed
	err = storage.Close()
	assert.NoError(t, err)
}

func TestNewStorageFromType_DBAlias(t *testing.T) {
	// Test "db" alias for database
	_, err := NewStorageFromType(StorageTypeDB, &StorageOptions{})
	assert.Error(t, err) // No URL provided
	assert.Contains(t, err.Error(), "database URL is required")
}

func TestNewStorageFromType_NilOptions(t *testing.T) {
	storage, err := NewStorageFromType(StorageTypeNone, nil)
	assert.NoError(t, err)
	assert.Nil(t, storage)
}

func TestMultiStorage_WriteError(t *testing.T) {
	tempDir := t.TempDir()

	// Create a valid storage
	storage1, err := NewFileStorage(filepath.Join(tempDir, "audit1.log"))
	require.NoError(t, err)

	// Create multi-storage with nil (which will be skipped)
	multi := NewMultiStorage(nil, storage1)
	defer func() { _ = multi.Close() }()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = multi.Write(context.Background(), record)
	assert.NoError(t, err)
}

func TestMultiStorage_CloseError(t *testing.T) {
	tempDir := t.TempDir()

	storage1, err := NewFileStorage(filepath.Join(tempDir, "audit1.log"))
	require.NoError(t, err)

	storage2, err := NewFileStorage(filepath.Join(tempDir, "audit2.log"))
	require.NoError(t, err)

	multi := NewMultiStorage(storage1, storage2)

	// Close should close all storages
	err = multi.Close()
	assert.NoError(t, err)
}

// failingCloseStorage is a Storage that returns an error on Close.
type failingCloseStorage struct {
	Storage
	closeErr error
}

func (f *failingCloseStorage) Close() error {
	if f.closeErr != nil {
		return f.closeErr
	}
	return f.Storage.Close()
}

func TestMultiStorage_CloseReturnsFirstError(t *testing.T) {
	tempDir := t.TempDir()
	fileStorage, err := NewFileStorage(filepath.Join(tempDir, "audit.log"))
	require.NoError(t, err)

	closeErr := errors.New("close failed")
	failing := &failingCloseStorage{Storage: fileStorage, closeErr: closeErr}
	multi := NewMultiStorage(failing)

	err = multi.Close()
	assert.Error(t, err)
	assert.Equal(t, closeErr, err)
}

func TestNewStorageFromType_DatabaseWithTableName(t *testing.T) {
	// This will fail but tests the code path
	_, err := NewStorageFromType(StorageTypeDatabase, &StorageOptions{
		DatabaseURL: "postgres://invalid",
		TableName:   "custom_table",
	})
	assert.Error(t, err)
}

func TestNewStorageFromType_RedisWithConfig(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer func() { _ = client.Close() }()

	storage, err := NewStorageFromType(StorageTypeRedis, &StorageOptions{
		RedisClient: client,
		RedisPrefix: "custom:",
		RedisTTL:    1 * time.Hour,
	})
	require.NoError(t, err)
	require.NotNil(t, storage)

	rs, ok := storage.(*RedisStorage)
	assert.True(t, ok)
	assert.Equal(t, "custom:", rs.KeyPrefix())
	assert.Equal(t, 1*time.Hour, rs.TTL())
}

// errorStorage is a storage that always returns errors
type errorStorage struct{}

func (e *errorStorage) Write(ctx context.Context, record *Record) error {
	return fmt.Errorf("write error")
}

func (e *errorStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	return nil, fmt.Errorf("query error")
}

func (e *errorStorage) Close() error {
	return fmt.Errorf("close error")
}

func TestMultiStorage_WriteWithError(t *testing.T) {
	tempDir := t.TempDir()

	// Create a valid storage and an error storage
	validStorage, err := NewFileStorage(filepath.Join(tempDir, "audit.log"))
	require.NoError(t, err)

	errStorage := &errorStorage{}

	multi := NewMultiStorage(errStorage, validStorage)
	defer func() { _ = multi.Close() }()

	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err = multi.Write(context.Background(), record)
	// Should return the first error
	assert.Error(t, err)
}

func TestMultiStorage_CloseWithError(t *testing.T) {
	tempDir := t.TempDir()

	validStorage, err := NewFileStorage(filepath.Join(tempDir, "audit.log"))
	require.NoError(t, err)

	errStorage := &errorStorage{}

	multi := NewMultiStorage(errStorage, validStorage)

	err = multi.Close()
	// Should return the first error
	assert.Error(t, err)
}

func TestNewStorageFromType_DefaultTableName(t *testing.T) {
	// This will fail but tests the code path where TableName is empty
	_, err := NewStorageFromType(StorageTypeDatabase, &StorageOptions{
		DatabaseURL: "postgres://invalid",
		TableName:   "", // Empty, should use default
	})
	assert.Error(t, err) // Will fail because no server
}

func TestStorageTypes(t *testing.T) {
	// Test storage type constants
	assert.Equal(t, StorageType("file"), StorageTypeFile)
	assert.Equal(t, StorageType("database"), StorageTypeDatabase)
	assert.Equal(t, StorageType("db"), StorageTypeDB)
	assert.Equal(t, StorageType("redis"), StorageTypeRedis)
	assert.Equal(t, StorageType("none"), StorageTypeNone)
}
