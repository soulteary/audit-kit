package audit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRedisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	return client, mr
}

func TestNewRedisStorage(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	require.NotNil(t, storage)

	assert.Equal(t, "audit:", storage.KeyPrefix())
	assert.Equal(t, 7*24*time.Hour, storage.TTL())
}

func TestNewRedisStorageWithConfig(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	config := &RedisConfig{
		KeyPrefix: "myapp:audit:",
		TTL:       24 * time.Hour,
	}

	storage := NewRedisStorageWithConfig(client, config)
	require.NotNil(t, storage)

	assert.Equal(t, "myapp:audit:", storage.KeyPrefix())
	assert.Equal(t, 24*time.Hour, storage.TTL())
}

func TestRedisStorage_Write(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	setKey := storage.KeyPrefix() + "index"

	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123").
		WithIP("192.168.1.1")

	err := storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Verify key was created
	keys := mr.Keys()
	assert.Len(t, keys, 2) // record key + index
	assert.Greater(t, mr.TTL(setKey), time.Duration(0), "index TTL should be set")
	assert.LessOrEqual(t, mr.TTL(setKey), storage.TTL())
}

func TestRedisStorage_Write_SameSecondNoID(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	now := time.Now().Unix()

	// Two records in same second with no EventID/ChallengeID/UserID must get distinct keys
	r1 := NewRecord(EventLoginSuccess, ResultSuccess).SetTimestamp(now)
	r2 := NewRecord(EventLoginFailed, ResultFailure).SetTimestamp(now)

	err := storage.Write(context.Background(), r1)
	require.NoError(t, err)
	err = storage.Write(context.Background(), r2)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), DefaultQueryFilter().WithLimit(10))
	require.NoError(t, err)
	assert.Len(t, results, 2, "both records must be stored with unique keys")
}

func TestRedisStorage_Query(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write multiple records
	for i := 0; i < 5; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			WithUserID("user" + string(rune('0'+i))).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query all
	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 5)

	// Should be in descending order (newest first)
	for i := 0; i < len(results)-1; i++ {
		assert.GreaterOrEqual(t, results[i].Timestamp, results[i+1].Timestamp)
	}
}

func TestRedisStorage_Query_WithFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with different attributes
	records := []*Record{
		NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("user1").SetTimestamp(now),
		NewRecord(EventLoginFailed, ResultFailure).WithUserID("user2").SetTimestamp(now + 1),
		NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("user3").SetTimestamp(now + 2),
	}

	for _, r := range records {
		err := storage.Write(context.Background(), r)
		require.NoError(t, err)
	}

	// Filter by event type
	filter := DefaultQueryFilter().WithEventType("login_success")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Filter by user ID
	filter = DefaultQueryFilter().WithUserID("user1")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Filter by result
	filter = DefaultQueryFilter().WithResult("failure")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestRedisStorage_Query_TimeRange(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with different timestamps
	for i := 0; i < 5; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			SetTimestamp(now + int64(i*100))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Filter by time range
	filter := DefaultQueryFilter().WithTimeRange(now+100, now+300)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3) // timestamps: now+100, now+200, now+300
}

func TestRedisStorage_Query_Pagination(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write 10 records
	for i := 0; i < 10; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Get first page
	filter := DefaultQueryFilter().WithLimit(3).WithOffset(0)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Get second page
	filter = DefaultQueryFilter().WithLimit(3).WithOffset(3)
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestRedisStorage_Query_Empty(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestRedisStorage_Cleanup(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	// Write a record
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err := storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Cleanup should not remove anything yet
	removed, err := storage.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(0), removed)
}

// TestRedisStorage_Cleanup_RemovesStale verifies that Cleanup removes index entries whose keys expired.
func TestRedisStorage_Cleanup_RemovesStale(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	setKey := storage.KeyPrefix() + "index"

	// Add a stale member to the index (no corresponding record key)
	err := client.ZAdd(context.Background(), setKey, redis.Z{Score: 1, Member: storage.KeyPrefix() + "1:stale"}).Err()
	require.NoError(t, err)

	removed, err := storage.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed)
}

func TestRedisStorage_Write_SetFails(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	require.NoError(t, client.Close())

	storage := NewRedisStorage(client)
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err := storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to set key")
}

func TestRedisStorage_Write_ZAddFails(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	setKey := storage.KeyPrefix() + "index"
	// Make index key a string so ZAdd fails (wrong type)
	require.NoError(t, client.Set(context.Background(), setKey, "not-a-sorted-set", 0).Err())

	record := NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("u1")
	err := storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add to sorted set")
}

func TestRedisStorage_Query_ZRevRangeFails(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	require.NoError(t, client.Close())

	storage := NewRedisStorage(client)
	_, err := storage.Query(context.Background(), DefaultQueryFilter())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get keys")
}

func TestRedisStorage_Cleanup_ZRangeFails(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	require.NoError(t, client.Close())

	storage := NewRedisStorage(client)
	_, err := storage.Cleanup(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get keys")
}

// TestRedisStorage_Query_ExpiredKey verifies that when a key in the index has expired (redis.Nil),
// Query skips it and removes it from the index.
func TestRedisStorage_Query_ExpiredKey(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	record := NewRecord(EventLoginSuccess, ResultSuccess).WithUserID("u1")
	err := storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Delete the record key but leave the index entry (simulates TTL expiry)
	keys, err := client.Keys(context.Background(), storage.KeyPrefix()+"*").Result()
	require.NoError(t, err)
	for _, k := range keys {
		if k != storage.KeyPrefix()+"index" {
			_ = client.Del(context.Background(), k).Err()
			break
		}
	}

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

// TestRedisStorage_Query_PaginationOffsetBeyondResults verifies start >= len(records) returns empty.
func TestRedisStorage_Query_PaginationOffsetBeyondResults(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	err := storage.Write(context.Background(), NewRecord(EventLoginSuccess, ResultSuccess))
	require.NoError(t, err)

	filter := DefaultQueryFilter().WithLimit(10).WithOffset(100)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

// TestRedisStorage_Query_InvalidJSONInKey skips keys whose value is not valid record JSON.
func TestRedisStorage_Query_InvalidJSONInKey(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	setKey := storage.KeyPrefix() + "index"
	badKey := storage.KeyPrefix() + "1:bad"
	// Add index entry and set value to invalid JSON
	err := client.ZAdd(context.Background(), setKey, redis.Z{Score: 1, Member: badKey}).Err()
	require.NoError(t, err)
	err = client.Set(context.Background(), badKey, "not valid json", 0).Err()
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestRedisStorage_Close(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()

	storage := NewRedisStorage(client)

	err := storage.Close()
	assert.NoError(t, err)
}

func TestRedisStorage_Client(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	assert.Equal(t, client, storage.Client())
}

func TestDefaultRedisConfig(t *testing.T) {
	config := DefaultRedisConfig()
	assert.Equal(t, "audit:", config.KeyPrefix)
	assert.Equal(t, 7*24*time.Hour, config.TTL)
}

func TestNewRedisStorageWithConfig_Defaults(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	// Test with empty prefix and zero TTL
	config := &RedisConfig{
		KeyPrefix: "",
		TTL:       0,
	}

	storage := NewRedisStorageWithConfig(client, config)
	require.NotNil(t, storage)

	// Should use defaults
	assert.Equal(t, "audit:", storage.KeyPrefix())
	assert.Equal(t, 7*24*time.Hour, storage.TTL())
}

func TestRedisStorage_Write_DifferentKeyTypes(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	// Write with EventID
	record1 := NewRecord(EventLoginSuccess, ResultSuccess)
	record1.EventID = "evt_123"
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	// Write with ChallengeID only
	record2 := NewRecord(EventChallengeCreated, ResultSuccess)
	record2.ChallengeID = "ch_456"
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Write with UserID only
	record3 := NewRecord(EventLogout, ResultSuccess)
	record3.UserID = "user789"
	err = storage.Write(context.Background(), record3)
	require.NoError(t, err)

	// Write with nothing (just timestamp)
	record4 := NewRecord(EventCustom, ResultSuccess)
	err = storage.Write(context.Background(), record4)
	require.NoError(t, err)
}

func TestRedisStorage_Query_NoTimeRange(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write some records with unique identifiers
	for i := 0; i < 3; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			WithUserID("user" + string(rune('0'+i))).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query without time range (should use -inf to +inf)
	filter := DefaultQueryFilter()
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestRedisStorage_Query_OffsetBeyondResults(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	// Write 3 records
	for i := 0; i < 3; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess)
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query with offset beyond results
	filter := DefaultQueryFilter().WithOffset(100)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestRedisStorage_CloseNilClient(t *testing.T) {
	storage := &RedisStorage{client: nil}
	err := storage.Close()
	assert.NoError(t, err)
}

func TestRedisStorage_Query_WithSessionFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with session IDs
	record1 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithSessionID("sess_123").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithSessionID("sess_456").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by session ID
	filter := DefaultQueryFilter().WithSessionID("sess_123")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestRedisStorage_Query_WithChannelFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with channels
	record1 := NewRecord(EventSendSuccess, ResultSuccess).
		WithChannel("sms").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventSendSuccess, ResultSuccess).
		WithChannel("email").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by channel
	filter := DefaultQueryFilter().WithChannel("sms")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestRedisStorage_Query_WithIPFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with IPs
	record1 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithIP("192.168.1.1").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithIP("10.0.0.1").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by IP
	filter := DefaultQueryFilter().WithIP("192.168.1.1")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestRedisStorage_Cleanup_RemovesExpiredKeys(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)
	ctx := context.Background()

	// Write a record (creates index entry)
	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123")
	err := storage.Write(ctx, record)
	require.NoError(t, err)

	// Manually add a non-existent key to the index
	setKey := storage.keyPrefix + "index"
	client.ZAdd(ctx, setKey, redis.Z{Score: float64(time.Now().Unix()), Member: "fake_key_123"})

	// Cleanup should remove the fake key
	removed, err := storage.Cleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed)
}

func TestRedisStorage_Query_ChallengeIDFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with challenge IDs
	record1 := NewRecord(EventChallengeCreated, ResultSuccess).
		WithChallengeID("ch_123").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventChallengeCreated, ResultSuccess).
		WithChallengeID("ch_456").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by challenge ID
	filter := DefaultQueryFilter().WithChallengeID("ch_123")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestRedisStorage_Query_NilFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	// Write a record
	record := NewRecord(EventLoginSuccess, ResultSuccess)
	err := storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Query with nil filter
	results, err := storage.Query(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestRedisStorage_Query_EndBeyondRecords(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write 3 records
	for i := 0; i < 3; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query with large limit (should return all)
	filter := DefaultQueryFilter().WithLimit(100)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestRedisStorage_Query_WithResultFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with different results
	record1 := NewRecord(EventLoginSuccess, ResultSuccess).
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLoginFailed, ResultFailure).
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by result
	filter := DefaultQueryFilter().WithResult("success")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestRedisStorage_Query_StartEndTimeFilters(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records at different times
	for i := 0; i < 5; i++ {
		record := NewRecord(EventLoginSuccess, ResultSuccess).
			SetTimestamp(now + int64(i*100))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Filter with StartTime only
	filter := &QueryFilter{Limit: 100, StartTime: now + 200}
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Filter with EndTime only
	filter = &QueryFilter{Limit: 100, EndTime: now + 200}
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestRedisStorage_Query_EventTypeFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with different event types
	record1 := NewRecord(EventLoginSuccess, ResultSuccess).SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLogout, ResultSuccess).SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by event type
	filter := DefaultQueryFilter().WithEventType("login_success")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestRedisStorage_Query_UserFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := NewRedisStorage(client)

	now := time.Now().Unix()

	// Write records with different user IDs
	record1 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user1").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user2").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by user ID
	filter := DefaultQueryFilter().WithUserID("user1")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}
