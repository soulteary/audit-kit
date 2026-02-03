package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileStorage implements Storage interface for file-based audit logging
// Uses JSON Lines format (one JSON object per line)
type FileStorage struct {
	filePath string
	file     *os.File
	writer   *bufio.Writer
	mu       sync.Mutex
}

// NewFileStorage creates a new file storage instance.
// filePath must come from trusted configuration only; do not pass user-controlled
// paths (path traversal or symlinks could write audit logs to unintended locations).
func NewFileStorage(filePath string) (*FileStorage, error) {
	// Create directory if it doesn't exist
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Open file in append mode
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return &FileStorage{
		filePath: filePath,
		file:     file,
		writer:   bufio.NewWriter(file),
	}, nil
}

// Write writes an audit record to the file (JSON Lines format)
func (s *FileStorage) Write(ctx context.Context, record *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Marshal record to JSON
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal audit record: %w", err)
	}

	// Write JSON line
	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	if _, err := s.writer.WriteString("\n"); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	// Flush to ensure data is written
	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush buffer: %w", err)
	}

	return nil
}

// Query reads audit records from the file matching the filter
// Note: File storage query is simple and may be slow for large files
// For production use, consider using database storage
func (s *FileStorage) Query(ctx context.Context, filter *QueryFilter) ([]*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if filter == nil {
		filter = DefaultQueryFilter()
	}
	filter.Normalize()

	// Flush current writer
	if err := s.writer.Flush(); err != nil {
		// Log warning but continue
		_ = err
	}

	// Open file for reading
	file, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Record{}, nil
		}
		return nil, fmt.Errorf("failed to open file for reading: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Read all records. Use a larger buffer so lines up to MaxRecordJSONSize
	// are read in full (default 64KB would truncate and drop records).
	var allRecords []*Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64), MaxRecordJSONSize)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if len(line) > MaxRecordJSONSize {
			// Skip oversized lines (e.g. malformed or DoS)
			continue
		}

		var record Record
		if err := json.Unmarshal(line, &record); err != nil {
			// Skip malformed records
			continue
		}

		allRecords = append(allRecords, &record)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Filter and paginate (newest first)
	var results []*Record
	offset := 0
	for i := len(allRecords) - 1; i >= 0; i-- {
		record := allRecords[i]

		if !matchesFilter(record, filter) {
			continue
		}

		// Apply offset
		if offset < filter.Offset {
			offset++
			continue
		}

		results = append(results, record)
		if len(results) >= filter.Limit {
			break
		}
	}

	return results, nil
}

// Close closes the file and releases resources
func (s *FileStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writer != nil {
		_ = s.writer.Flush()
	}

	if s.file != nil {
		return s.file.Close()
	}

	return nil
}

// Rotate rotates the audit log file
// Creates a new file with timestamp suffix and reopens the main file
func (s *FileStorage) Rotate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Flush current writer
	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush writer: %w", err)
	}

	// Close current file
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	// Rename current file with timestamp
	timestamp := time.Now().Format("20060102-150405")
	rotatedPath := fmt.Sprintf("%s.%s", s.filePath, timestamp)
	if err := os.Rename(s.filePath, rotatedPath); err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}

	// Open new file
	file, err := os.OpenFile(s.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open new file: %w", err)
	}

	s.file = file
	s.writer = bufio.NewWriter(file)

	return nil
}

// FilePath returns the file path
func (s *FileStorage) FilePath() string {
	return s.filePath
}

// matchesFilter checks if a record matches the filter criteria
func matchesFilter(record *Record, filter *QueryFilter) bool {
	if filter.EventType != "" && string(record.EventType) != filter.EventType {
		return false
	}
	if filter.UserID != "" && record.UserID != filter.UserID {
		return false
	}
	if filter.ChallengeID != "" && record.ChallengeID != filter.ChallengeID {
		return false
	}
	if filter.SessionID != "" && record.SessionID != filter.SessionID {
		return false
	}
	if filter.Channel != "" && record.Channel != filter.Channel {
		return false
	}
	if filter.Result != "" && string(record.Result) != filter.Result {
		return false
	}
	if filter.IP != "" && record.IP != filter.IP {
		return false
	}
	if filter.StartTime > 0 && record.Timestamp < filter.StartTime {
		return false
	}
	if filter.EndTime > 0 && record.Timestamp > filter.EndTime {
		return false
	}
	return true
}
