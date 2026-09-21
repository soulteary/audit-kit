package redisstore

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	audit "github.com/soulteary/audit-kit/v2"
)

func newTestRedisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	return client, mr
}

func TestNew(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	require.NotNil(t, storage)

	assert.Equal(t, "audit:", storage.KeyPrefix())
	assert.Equal(t, 7*24*time.Hour, storage.TTL())
}

func TestNewWithConfig(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	config := &Config{
		KeyPrefix: "myapp:audit:",
		TTL:       24 * time.Hour,
	}

	storage := NewWithConfig(client, config)
	require.NotNil(t, storage)

	assert.Equal(t, "myapp:audit:", storage.KeyPrefix())
	assert.Equal(t, 24*time.Hour, storage.TTL())
}

func TestStorage_Write(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithUserID("user123").
		WithIP("192.168.1.1")

	err := storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Verify key was created
	keys := mr.Keys()
	assert.Len(t, keys, 2) // record key + index
}

func TestStorage_Write_SameSecondNoID(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	now := time.Now().Unix()

	// Two records in same second with no EventID/ChallengeID/UserID must get distinct keys
	r1 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).SetTimestamp(now)
	r2 := audit.NewRecord(audit.EventLoginFailed, audit.ResultFailure).SetTimestamp(now)

	err := storage.Write(context.Background(), r1)
	require.NoError(t, err)
	err = storage.Write(context.Background(), r2)
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), audit.DefaultQueryFilter().WithLimit(10))
	require.NoError(t, err)
	assert.Len(t, results, 2, "both records must be stored with unique keys")
}

func TestStorage_Query(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write multiple records
	for i := 0; i < 5; i++ {
		record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
			WithUserID("user" + string(rune('0'+i))).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query all
	results, err := storage.Query(context.Background(), audit.DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 5)

	// Should be in descending order (newest first)
	for i := 0; i < len(results)-1; i++ {
		assert.GreaterOrEqual(t, results[i].Timestamp, results[i+1].Timestamp)
	}
}

func TestStorage_Query_WithFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with different attributes
	records := []*audit.Record{
		audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).WithUserID("user1").SetTimestamp(now),
		audit.NewRecord(audit.EventLoginFailed, audit.ResultFailure).WithUserID("user2").SetTimestamp(now + 1),
		audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).WithUserID("user3").SetTimestamp(now + 2),
	}

	for _, r := range records {
		err := storage.Write(context.Background(), r)
		require.NoError(t, err)
	}

	// Filter by event type
	filter := audit.DefaultQueryFilter().WithEventType("login_success")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Filter by user ID
	filter = audit.DefaultQueryFilter().WithUserID("user1")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)

	// Filter by result
	filter = audit.DefaultQueryFilter().WithResult("failure")
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestStorage_Query_TimeRange(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with different timestamps
	for i := 0; i < 5; i++ {
		record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
			SetTimestamp(now + int64(i*100))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Filter by time range
	filter := audit.DefaultQueryFilter().WithTimeRange(now+100, now+300)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3) // timestamps: now+100, now+200, now+300
}

func TestStorage_Query_Pagination(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write 10 records
	for i := 0; i < 10; i++ {
		record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Get first page
	filter := audit.DefaultQueryFilter().WithLimit(3).WithOffset(0)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Get second page
	filter = audit.DefaultQueryFilter().WithLimit(3).WithOffset(3)
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestStorage_Query_Empty(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	results, err := storage.Query(context.Background(), audit.DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestStorage_Cleanup(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	// Write a record
	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess)
	err := storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Cleanup should not remove anything yet
	removed, err := storage.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(0), removed)
}

// TestRedisStorage_Cleanup_RemovesStale verifies that Cleanup removes index entries whose keys expired.
func TestStorage_Cleanup_RemovesStale(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	setKey := storage.KeyPrefix() + "index"

	// Add a stale member to the index (no corresponding record key)
	err := client.ZAdd(context.Background(), setKey, redis.Z{Score: 1, Member: storage.KeyPrefix() + "1:stale"}).Err()
	require.NoError(t, err)

	removed, err := storage.Cleanup(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed)
}

func TestStorage_Write_SetFails(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	require.NoError(t, client.Close())

	storage := New(client)
	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess)
	err := storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to set key")
}

func TestStorage_Write_ZAddFails(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	setKey := storage.KeyPrefix() + "index"
	// Make index key a string so ZAdd fails (wrong type)
	require.NoError(t, client.Set(context.Background(), setKey, "not-a-sorted-set", 0).Err())

	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).WithUserID("u1")
	err := storage.Write(context.Background(), record)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add to sorted set")
}

func TestStorage_Query_ZRevRangeFails(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	require.NoError(t, client.Close())

	storage := New(client)
	_, err := storage.Query(context.Background(), audit.DefaultQueryFilter())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get keys")
}

func TestStorage_Cleanup_ZRangeFails(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	require.NoError(t, client.Close())

	storage := New(client)
	_, err := storage.Cleanup(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get keys")
}

// TestRedisStorage_Query_ExpiredKey verifies that when a key in the index has expired (redis.Nil),
// Query skips it and removes it from the index.
func TestStorage_Query_ExpiredKey(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).WithUserID("u1")
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

	results, err := storage.Query(context.Background(), audit.DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

// TestRedisStorage_Query_PaginationOffsetBeyondResults verifies start >= len(records) returns empty.
func TestStorage_Query_PaginationOffsetBeyondResults(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	err := storage.Write(context.Background(), audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess))
	require.NoError(t, err)

	filter := audit.DefaultQueryFilter().WithLimit(10).WithOffset(100)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

// TestRedisStorage_Query_InvalidJSONInKey skips keys whose value is not valid record JSON.
func TestStorage_Query_InvalidJSONInKey(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	setKey := storage.KeyPrefix() + "index"
	badKey := storage.KeyPrefix() + "1:bad"
	// Add index entry and set value to invalid JSON
	err := client.ZAdd(context.Background(), setKey, redis.Z{Score: 1, Member: badKey}).Err()
	require.NoError(t, err)
	err = client.Set(context.Background(), badKey, "not valid json", 0).Err()
	require.NoError(t, err)

	results, err := storage.Query(context.Background(), audit.DefaultQueryFilter())
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

// Close leaves a shared client alone by default: the program that handed the
// client over keeps using it after the audit logger has shut down.
func TestStorage_Close_LeavesSharedClientOpen(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	require.NoError(t, storage.Close())

	// The client still works, which is the whole point of the default.
	require.NoError(t, client.Ping(context.Background()).Err())
}

// CloseClient opts back into v1's behaviour, for a client the store owns.
func TestStorage_Close_ClosesOwnedClient(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()

	storage := NewWithConfig(client, &Config{CloseClient: true})

	require.NoError(t, storage.Close())

	err := client.Ping(context.Background()).Err()
	assert.ErrorIs(t, err, redis.ErrClosed)
}

func TestStorage_Client(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	assert.Equal(t, client, storage.Client())
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	assert.Equal(t, "audit:", config.KeyPrefix)
	assert.Equal(t, 7*24*time.Hour, config.TTL)
}

func TestNewWithConfig_Defaults(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	// Test with empty prefix and zero TTL
	config := &Config{
		KeyPrefix: "",
		TTL:       0,
	}

	storage := NewWithConfig(client, config)
	require.NotNil(t, storage)

	// Should use defaults
	assert.Equal(t, "audit:", storage.KeyPrefix())
	assert.Equal(t, 7*24*time.Hour, storage.TTL())
}

func TestStorage_Write_DifferentKeyTypes(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	// Write with EventID
	record1 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess)
	record1.EventID = "evt_123"
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	// Write with ChallengeID only
	record2 := audit.NewRecord(audit.EventChallengeCreated, audit.ResultSuccess)
	record2.ChallengeID = "ch_456"
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Write with UserID only
	record3 := audit.NewRecord(audit.EventLogout, audit.ResultSuccess)
	record3.UserID = "user789"
	err = storage.Write(context.Background(), record3)
	require.NoError(t, err)

	// Write with nothing (just timestamp)
	record4 := audit.NewRecord(audit.EventCustom, audit.ResultSuccess)
	err = storage.Write(context.Background(), record4)
	require.NoError(t, err)
}

func TestStorage_Query_NoTimeRange(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write some records with unique identifiers
	for i := 0; i < 3; i++ {
		record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
			WithUserID("user" + string(rune('0'+i))).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query without time range (should use -inf to +inf)
	filter := audit.DefaultQueryFilter()
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestStorage_Query_OffsetBeyondResults(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	// Write 3 records
	for i := 0; i < 3; i++ {
		record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess)
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query with offset beyond results
	filter := audit.DefaultQueryFilter().WithOffset(100)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestStorage_CloseNilClient(t *testing.T) {
	storage := &Storage{client: nil}
	err := storage.Close()
	assert.NoError(t, err)
}

func TestStorage_Query_WithSessionFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with session IDs
	record1 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithSessionID("sess_123").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithSessionID("sess_456").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by session ID
	filter := audit.DefaultQueryFilter().WithSessionID("sess_123")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestStorage_Query_WithChannelFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with channels
	record1 := audit.NewRecord(audit.EventSendSuccess, audit.ResultSuccess).
		WithChannel("sms").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := audit.NewRecord(audit.EventSendSuccess, audit.ResultSuccess).
		WithChannel("email").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by channel
	filter := audit.DefaultQueryFilter().WithChannel("sms")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestStorage_Query_WithIPFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with IPs
	record1 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithIP("192.168.1.1").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithIP("10.0.0.1").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by IP
	filter := audit.DefaultQueryFilter().WithIP("192.168.1.1")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestStorage_Cleanup_RemovesExpiredKeys(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)
	ctx := context.Background()

	// Write a record (creates index entry)
	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
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

func TestStorage_Query_ChallengeIDFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with challenge IDs
	record1 := audit.NewRecord(audit.EventChallengeCreated, audit.ResultSuccess).
		WithChallengeID("ch_123").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := audit.NewRecord(audit.EventChallengeCreated, audit.ResultSuccess).
		WithChallengeID("ch_456").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by challenge ID
	filter := audit.DefaultQueryFilter().WithChallengeID("ch_123")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestStorage_Query_NilFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	// Write a record
	record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess)
	err := storage.Write(context.Background(), record)
	require.NoError(t, err)

	// Query with nil filter
	results, err := storage.Query(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestStorage_Query_EndBeyondRecords(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write 3 records
	for i := 0; i < 3; i++ {
		record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
			SetTimestamp(now + int64(i))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Query with large limit (should return all)
	filter := audit.DefaultQueryFilter().WithLimit(100)
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestStorage_Query_WithResultFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with different results
	record1 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := audit.NewRecord(audit.EventLoginFailed, audit.ResultFailure).
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by result
	filter := audit.DefaultQueryFilter().WithResult("success")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestStorage_Query_StartEndTimeFilters(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records at different times
	for i := 0; i < 5; i++ {
		record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
			SetTimestamp(now + int64(i*100))
		err := storage.Write(context.Background(), record)
		require.NoError(t, err)
	}

	// Filter with StartTime only
	filter := &audit.QueryFilter{Limit: 100, StartTime: now + 200}
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Filter with EndTime only
	filter = &audit.QueryFilter{Limit: 100, EndTime: now + 200}
	results, err = storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestStorage_Query_EventTypeFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with different event types
	record1 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := audit.NewRecord(audit.EventLogout, audit.ResultSuccess).SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by event type
	filter := audit.DefaultQueryFilter().WithEventType("login_success")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestStorage_Query_UserFilter(t *testing.T) {
	client, mr := newTestRedisClient(t)
	defer mr.Close()
	defer func() { _ = client.Close() }()

	storage := New(client)

	now := time.Now().Unix()

	// Write records with different user IDs
	record1 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithUserID("user1").
		SetTimestamp(now)
	err := storage.Write(context.Background(), record1)
	require.NoError(t, err)

	record2 := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).
		WithUserID("user2").
		SetTimestamp(now + 1)
	err = storage.Write(context.Background(), record2)
	require.NoError(t, err)

	// Filter by user ID
	filter := audit.DefaultQueryFilter().WithUserID("user1")
	results, err := storage.Query(context.Background(), filter)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

// The store accepts every go-redis client shape, not just a standalone one: a
// cluster or Sentinel deployment is where an audit trail matters most.
// Compile-time only; nothing here dials.
var (
	_ Client = (*redis.Client)(nil)
	_ Client = (*redis.ClusterClient)(nil)
	_ Client = (*redis.Ring)(nil)
	_ Client = (redis.UniversalClient)(nil)
)

// A Storage is usable wherever the root package wants an audit.Storage.
var _ audit.Storage = (*Storage)(nil)

// A nil client is reported, not panicked on. The typed-nil case is the one
// that actually reaches here -- an unassigned *redis.Client field, or a
// constructor that returned early -- and a plain c == nil does not catch it.
func TestStorage_NilClient(t *testing.T) {
	var typedNil *redis.Client

	for _, tt := range []struct {
		name    string
		storage *Storage
	}{
		{"untyped nil", New(nil)},
		{"typed nil", New(typedNil)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			record := audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess)

			assert.ErrorIs(t, tt.storage.Write(ctx, record), ErrNilClient)

			_, err := tt.storage.Query(ctx, audit.DefaultQueryFilter())
			assert.ErrorIs(t, err, ErrNilClient)

			_, err = tt.storage.Cleanup(ctx)
			assert.ErrorIs(t, err, ErrNilClient)

			// Close is the exception: nothing to close is not an error.
			assert.NoError(t, tt.storage.Close())
		})
	}
}

// A store built against the Client interface works through any client shape.
// miniredis is a single server, so this exercises the interface itself rather
// than cluster routing: what matters is that the code path never needs a
// concrete *redis.Client.
func TestStorage_ThroughUniversalClient(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	var client redis.UniversalClient = redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: []string{mr.Addr()},
	})
	defer func() { _ = client.Close() }()

	storage := New(client)
	ctx := context.Background()

	require.NoError(t, storage.Write(ctx, audit.NewRecord(audit.EventLoginSuccess, audit.ResultSuccess).WithUserID("u1")))

	results, err := storage.Query(ctx, audit.DefaultQueryFilter())
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "u1", results[0].UserID)
}
