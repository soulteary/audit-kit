package audit

import (
	"context"
)

// Storage defines the interface for audit log storage backends
type Storage interface {
	// Write writes an audit record to the storage backend
	Write(ctx context.Context, record *Record) error

	// Query queries audit records based on filter criteria
	// Returns records matching the filter, ordered by timestamp (newest first)
	Query(ctx context.Context, filter *QueryFilter) ([]*Record, error)

	// Close closes the storage connection and releases resources
	Close() error
}

// QueryFilter defines filter criteria for querying audit records
type QueryFilter struct {
	// Filter by event type
	EventType string `json:"event_type,omitempty"`

	// Filter by subject
	UserID      string `json:"user_id,omitempty"`
	ChallengeID string `json:"challenge_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`

	// Filter by channel and result
	Channel string `json:"channel,omitempty"`
	Result  string `json:"result,omitempty"`

	// Time range filters (Unix timestamps)
	StartTime int64 `json:"start_time,omitempty"`
	EndTime   int64 `json:"end_time,omitempty"`

	// Filter by IP
	IP string `json:"ip,omitempty"`

	// Pagination
	Limit  int `json:"limit,omitempty"`  // Maximum number of records (default: 100)
	Offset int `json:"offset,omitempty"` // Offset for pagination (default: 0)
}

// DefaultQueryFilter returns a default query filter with sensible defaults
func DefaultQueryFilter() *QueryFilter {
	return &QueryFilter{
		Limit: 100,
	}
}

// WithEventType sets the event type filter
func (f *QueryFilter) WithEventType(eventType string) *QueryFilter {
	f.EventType = eventType
	return f
}

// WithUserID sets the user ID filter
func (f *QueryFilter) WithUserID(userID string) *QueryFilter {
	f.UserID = userID
	return f
}

// WithChallengeID sets the challenge ID filter
func (f *QueryFilter) WithChallengeID(challengeID string) *QueryFilter {
	f.ChallengeID = challengeID
	return f
}

// WithSessionID sets the session ID filter
func (f *QueryFilter) WithSessionID(sessionID string) *QueryFilter {
	f.SessionID = sessionID
	return f
}

// WithChannel sets the channel filter
func (f *QueryFilter) WithChannel(channel string) *QueryFilter {
	f.Channel = channel
	return f
}

// WithResult sets the result filter
func (f *QueryFilter) WithResult(result string) *QueryFilter {
	f.Result = result
	return f
}

// WithTimeRange sets the time range filter
func (f *QueryFilter) WithTimeRange(startTime, endTime int64) *QueryFilter {
	f.StartTime = startTime
	f.EndTime = endTime
	return f
}

// WithIP sets the IP filter
func (f *QueryFilter) WithIP(ip string) *QueryFilter {
	f.IP = ip
	return f
}

// WithLimit sets the limit
func (f *QueryFilter) WithLimit(limit int) *QueryFilter {
	f.Limit = limit
	return f
}

// WithOffset sets the offset
func (f *QueryFilter) WithOffset(offset int) *QueryFilter {
	f.Offset = offset
	return f
}

// Normalize ensures filter has valid values
func (f *QueryFilter) Normalize() {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
}
