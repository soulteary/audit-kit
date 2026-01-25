package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskDestination(t *testing.T) {
	tests := []struct {
		name     string
		dest     string
		channel  string
		expected string
	}{
		{
			name:     "email masking",
			dest:     "user@example.com",
			channel:  "email",
			expected: "u***@example.com",
		},
		{
			name:     "phone masking",
			dest:     "13800138000",
			channel:  "sms",
			expected: "138****8000",
		},
		{
			name:     "phone with plus",
			dest:     "+8613800138000",
			channel:  "phone",
			expected: "+86*******8000",
		},
		{
			name:     "unknown channel",
			dest:     "test@example.com",
			channel:  "unknown",
			expected: "****",
		},
		{
			name:     "empty destination",
			dest:     "",
			channel:  "email",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskDestination(tt.dest, tt.channel)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		email    string
		expected string
	}{
		{"user@example.com", "u***@example.com"},
		{"a@example.com", "a***@example.com"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			result := MaskEmail(tt.email)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMaskPhone(t *testing.T) {
	tests := []struct {
		phone    string
		expected string
	}{
		{"13800138000", "138****8000"},
		{"+8613800138000", "+86*******8000"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.phone, func(t *testing.T) {
			result := MaskPhone(tt.phone)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMaskIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected string
	}{
		{
			name:     "IPv4",
			ip:       "192.168.1.100",
			expected: "192.***.100",
		},
		{
			name:     "IPv4 localhost",
			ip:       "127.0.0.1",
			expected: "127.***.1",
		},
		{
			name:     "IPv4 short",
			ip:       "1.2.3.4",
			expected: "1.***.4",
		},
		{
			name:     "IPv6 long",
			ip:       "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expected: "2001****7334",
		},
		{
			name:     "empty",
			ip:       "",
			expected: "",
		},
		{
			name:     "short string",
			ip:       "abc",
			expected: "****",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskIP(tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMaskString(t *testing.T) {
	tests := []struct {
		name      string
		s         string
		keepChars int
		expected  string
	}{
		{
			name:      "normal string",
			s:         "HelloWorld",
			keepChars: 2,
			expected:  "He****ld",
		},
		{
			name:      "short string",
			s:         "Hi",
			keepChars: 2,
			expected:  "****",
		},
		{
			name:      "empty string",
			s:         "",
			keepChars: 2,
			expected:  "",
		},
		{
			name:      "exact length",
			s:         "Test",
			keepChars: 2,
			expected:  "****",
		},
		{
			name:      "unicode",
			s:         "你好世界测试",
			keepChars: 2,
			expected:  "你好****测试",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskString(tt.s, tt.keepChars)
			assert.Equal(t, tt.expected, result)
		})
	}
}
