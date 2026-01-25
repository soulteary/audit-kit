package audit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRecord(t *testing.T) {
	record := NewRecord(EventChallengeCreated, ResultSuccess)

	assert.Equal(t, EventChallengeCreated, record.EventType)
	assert.Equal(t, ResultSuccess, record.Result)
	assert.Greater(t, record.Timestamp, int64(0))
}

func TestRecord_Fluent(t *testing.T) {
	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123").
		WithChallengeID("ch_abc").
		WithSessionID("sess_xyz").
		WithChannel("email").
		WithDestination("test@example.com").
		WithPurpose("login").
		WithResource("/api/users").
		WithReason("").
		WithProvider("sendgrid", "msg_123").
		WithIP("192.168.1.1").
		WithUserAgent("Mozilla/5.0").
		WithRequestID("req_123").
		WithTraceID("trace_abc").
		WithDuration(150).
		WithMetadata("key1", "value1").
		WithMetadata("key2", 123)

	assert.Equal(t, "user123", record.UserID)
	assert.Equal(t, "ch_abc", record.ChallengeID)
	assert.Equal(t, "sess_xyz", record.SessionID)
	assert.Equal(t, "email", record.Channel)
	assert.Equal(t, "test@example.com", record.Destination)
	assert.Equal(t, "login", record.Purpose)
	assert.Equal(t, "/api/users", record.Resource)
	assert.Equal(t, "", record.Reason)
	assert.Equal(t, "sendgrid", record.Provider)
	assert.Equal(t, "msg_123", record.ProviderMessageID)
	assert.Equal(t, "192.168.1.1", record.IP)
	assert.Equal(t, "Mozilla/5.0", record.UserAgent)
	assert.Equal(t, "req_123", record.RequestID)
	assert.Equal(t, "trace_abc", record.TraceID)
	assert.Equal(t, int64(150), record.DurationMS)
	assert.Equal(t, "value1", record.Metadata["key1"])
	assert.Equal(t, 123, record.Metadata["key2"])
}

func TestRecord_SetTimestamp(t *testing.T) {
	ts := time.Now().Unix()
	record := NewRecord(EventLoginSuccess, ResultSuccess).SetTimestamp(ts)

	assert.Equal(t, ts, record.Timestamp)
}

func TestRecord_ToJSON(t *testing.T) {
	record := NewRecord(EventLoginSuccess, ResultSuccess).
		WithUserID("user123").
		WithIP("192.168.1.1")

	data, err := record.ToJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), "login_success")
	assert.Contains(t, string(data), "user123")
	assert.Contains(t, string(data), "192.168.1.1")
}

func TestRecordFromJSON(t *testing.T) {
	original := NewRecord(EventChallengeCreated, ResultSuccess).
		WithUserID("user123").
		WithChannel("sms").
		WithDestination("+1234567890").
		WithMetadata("key", "value")

	data, err := json.Marshal(original)
	require.NoError(t, err)

	record, err := RecordFromJSON(data)
	require.NoError(t, err)

	assert.Equal(t, original.EventType, record.EventType)
	assert.Equal(t, original.Result, record.Result)
	assert.Equal(t, original.UserID, record.UserID)
	assert.Equal(t, original.Channel, record.Channel)
	assert.Equal(t, original.Destination, record.Destination)
	assert.Equal(t, original.Timestamp, record.Timestamp)
	assert.Equal(t, "value", record.Metadata["key"])
}

func TestRecordFromJSON_Invalid(t *testing.T) {
	_, err := RecordFromJSON([]byte("invalid json"))
	assert.Error(t, err)
}

func TestEventTypes(t *testing.T) {
	eventTypes := []EventType{
		EventChallengeCreated,
		EventChallengeVerified,
		EventChallengeRevoked,
		EventChallengeExpired,
		EventSendSuccess,
		EventSendFailed,
		EventVerificationSuccess,
		EventVerificationFailed,
		EventLoginSuccess,
		EventLoginFailed,
		EventLogout,
		EventSessionCreate,
		EventSessionExpire,
		EventAccessGranted,
		EventAccessDenied,
		EventUserCreated,
		EventUserUpdated,
		EventUserDeleted,
		EventUserLocked,
		EventUserUnlocked,
		EventRateLimited,
		EventCustom,
	}

	for _, et := range eventTypes {
		assert.NotEmpty(t, string(et), "Event type should not be empty")
	}
}

func TestResults(t *testing.T) {
	results := []Result{
		ResultSuccess,
		ResultFailure,
		ResultPending,
	}

	for _, r := range results {
		assert.NotEmpty(t, string(r), "Result should not be empty")
	}
}
