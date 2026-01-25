package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultQueryFilter(t *testing.T) {
	filter := DefaultQueryFilter()

	assert.Equal(t, 100, filter.Limit)
	assert.Equal(t, 0, filter.Offset)
}

func TestQueryFilter_Fluent(t *testing.T) {
	filter := DefaultQueryFilter().
		WithEventType("login_success").
		WithUserID("user123").
		WithChallengeID("ch_abc").
		WithSessionID("sess_xyz").
		WithChannel("email").
		WithResult("success").
		WithTimeRange(1000, 2000).
		WithIP("192.168.1.1").
		WithLimit(50).
		WithOffset(10)

	assert.Equal(t, "login_success", filter.EventType)
	assert.Equal(t, "user123", filter.UserID)
	assert.Equal(t, "ch_abc", filter.ChallengeID)
	assert.Equal(t, "sess_xyz", filter.SessionID)
	assert.Equal(t, "email", filter.Channel)
	assert.Equal(t, "success", filter.Result)
	assert.Equal(t, int64(1000), filter.StartTime)
	assert.Equal(t, int64(2000), filter.EndTime)
	assert.Equal(t, "192.168.1.1", filter.IP)
	assert.Equal(t, 50, filter.Limit)
	assert.Equal(t, 10, filter.Offset)
}

func TestQueryFilter_Normalize(t *testing.T) {
	tests := []struct {
		name           string
		limit          int
		offset         int
		expectedLimit  int
		expectedOffset int
	}{
		{
			name:           "zero limit",
			limit:          0,
			offset:         0,
			expectedLimit:  100,
			expectedOffset: 0,
		},
		{
			name:           "negative limit",
			limit:          -1,
			offset:         0,
			expectedLimit:  100,
			expectedOffset: 0,
		},
		{
			name:           "limit over max",
			limit:          2000,
			offset:         0,
			expectedLimit:  1000,
			expectedOffset: 0,
		},
		{
			name:           "negative offset",
			limit:          50,
			offset:         -5,
			expectedLimit:  50,
			expectedOffset: 0,
		},
		{
			name:           "valid values",
			limit:          50,
			offset:         10,
			expectedLimit:  50,
			expectedOffset: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &QueryFilter{
				Limit:  tt.limit,
				Offset: tt.offset,
			}
			filter.Normalize()

			assert.Equal(t, tt.expectedLimit, filter.Limit)
			assert.Equal(t, tt.expectedOffset, filter.Offset)
		})
	}
}
