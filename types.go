// Package audit provides a unified audit logging toolkit for Go services.
// It supports multiple storage backends (file, database, Redis) and async writing.
package audit

import (
	"encoding/json"
	"time"
)

// EventType represents the type of audit event
type EventType string

// Common event types for OTP/Authentication services
const (
	// Challenge lifecycle events
	EventChallengeCreated  EventType = "challenge_created"
	EventChallengeVerified EventType = "challenge_verified"
	EventChallengeRevoked  EventType = "challenge_revoked"
	EventChallengeExpired  EventType = "challenge_expired"

	// Send events
	EventSendSuccess EventType = "send_success"
	EventSendFailed  EventType = "send_failed"

	// Verification events
	EventVerificationSuccess EventType = "verification_success"
	EventVerificationFailed  EventType = "verification_failed"

	// Authentication events
	EventLoginSuccess  EventType = "login_success"
	EventLoginFailed   EventType = "login_failed"
	EventLogout        EventType = "logout"
	EventSessionCreate EventType = "session_create"
	EventSessionExpire EventType = "session_expire"

	// Authorization events
	EventAccessGranted EventType = "access_granted"
	EventAccessDenied  EventType = "access_denied"

	// User management events
	EventUserCreated  EventType = "user_created"
	EventUserUpdated  EventType = "user_updated"
	EventUserDeleted  EventType = "user_deleted"
	EventUserLocked   EventType = "user_locked"
	EventUserUnlocked EventType = "user_unlocked"

	// Rate limit events
	EventRateLimited EventType = "rate_limited"

	// Generic events
	EventCustom EventType = "custom"
)

// Result represents the outcome of an audit event
type Result string

const (
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
	ResultPending Result = "pending"
)

// Record represents an audit log entry
type Record struct {
	// Event identification
	EventType EventType `json:"event_type"`
	EventID   string    `json:"event_id,omitempty"` // Unique event identifier

	// Subject identification
	UserID      string `json:"user_id,omitempty"`
	ChallengeID string `json:"challenge_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`

	// Event details
	Channel     string `json:"channel,omitempty"`     // sms, email, push, etc.
	Destination string `json:"destination,omitempty"` // May be masked
	Purpose     string `json:"purpose,omitempty"`     // login, reset, bind, etc.
	Resource    string `json:"resource,omitempty"`    // Accessed resource

	// Result
	Result Result `json:"result"`
	Reason string `json:"reason,omitempty"` // Failure reason

	// Provider info (for external services)
	Provider          string `json:"provider,omitempty"`
	ProviderMessageID string `json:"provider_message_id,omitempty"`

	// Request context
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`

	// Timing
	Timestamp  int64 `json:"timestamp"`             // Unix timestamp
	DurationMS int64 `json:"duration_ms,omitempty"` // Operation duration

	// Extensible metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// NewRecord creates a new audit record with required fields
func NewRecord(eventType EventType, result Result) *Record {
	return &Record{
		EventType: eventType,
		Result:    result,
		Timestamp: time.Now().Unix(),
	}
}

// WithUserID sets the user ID
func (r *Record) WithUserID(userID string) *Record {
	r.UserID = userID
	return r
}

// WithChallengeID sets the challenge ID
func (r *Record) WithChallengeID(challengeID string) *Record {
	r.ChallengeID = challengeID
	return r
}

// WithSessionID sets the session ID
func (r *Record) WithSessionID(sessionID string) *Record {
	r.SessionID = sessionID
	return r
}

// WithChannel sets the channel (sms, email, etc.)
func (r *Record) WithChannel(channel string) *Record {
	r.Channel = channel
	return r
}

// WithDestination sets the destination (phone, email, etc.)
func (r *Record) WithDestination(destination string) *Record {
	r.Destination = destination
	return r
}

// WithPurpose sets the purpose (login, reset, etc.)
func (r *Record) WithPurpose(purpose string) *Record {
	r.Purpose = purpose
	return r
}

// WithResource sets the accessed resource
func (r *Record) WithResource(resource string) *Record {
	r.Resource = resource
	return r
}

// WithReason sets the failure reason
func (r *Record) WithReason(reason string) *Record {
	r.Reason = reason
	return r
}

// WithProvider sets the provider info
func (r *Record) WithProvider(provider, messageID string) *Record {
	r.Provider = provider
	r.ProviderMessageID = messageID
	return r
}

// WithIP sets the client IP
func (r *Record) WithIP(ip string) *Record {
	r.IP = ip
	return r
}

// WithUserAgent sets the user agent
func (r *Record) WithUserAgent(ua string) *Record {
	r.UserAgent = ua
	return r
}

// WithRequestID sets the request ID
func (r *Record) WithRequestID(requestID string) *Record {
	r.RequestID = requestID
	return r
}

// WithTraceID sets the trace ID
func (r *Record) WithTraceID(traceID string) *Record {
	r.TraceID = traceID
	return r
}

// WithDuration sets the operation duration in milliseconds
func (r *Record) WithDuration(durationMS int64) *Record {
	r.DurationMS = durationMS
	return r
}

// WithMetadata sets custom metadata
func (r *Record) WithMetadata(key string, value interface{}) *Record {
	if r.Metadata == nil {
		r.Metadata = make(map[string]interface{})
	}
	r.Metadata[key] = value
	return r
}

// SetTimestamp sets the timestamp (useful for testing)
func (r *Record) SetTimestamp(ts int64) *Record {
	r.Timestamp = ts
	return r
}

// ToJSON serializes the record to JSON
func (r *Record) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

// RecordFromJSON deserializes a record from JSON
func RecordFromJSON(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
