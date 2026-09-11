package audit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingStorage counts writes and remembers whether the context it was
// handed was already cancelled.
type recordingStorage struct {
	mu            sync.Mutex
	written       int
	ctxCancelled  int
	closed        bool
	writeDelay    time.Duration
	writtenEvents []string
}

func (s *recordingStorage) Write(ctx context.Context, r *Record) error {
	if s.writeDelay > 0 {
		time.Sleep(s.writeDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		s.ctxCancelled++
		return err
	}
	s.written++
	s.writtenEvents = append(s.writtenEvents, string(r.EventType))
	return nil
}

func (s *recordingStorage) Query(context.Context, *QueryFilter) ([]*Record, error) {
	return nil, nil
}

func (s *recordingStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *recordingStorage) counts() (written, cancelled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written, s.ctxCancelled
}

// TestStopDrainsWithLiveContext is the regression test for Stop cancelling the
// context before draining: every queued record was then written with a dead
// context and rejected by the backend, so the drain lost everything.
func TestStopDrainsWithLiveContext(t *testing.T) {
	storage := &recordingStorage{writeDelay: time.Millisecond}
	w := NewWriter(storage, &WriterConfig{QueueSize: 256, Workers: 1, StopTimeout: 10 * time.Second})

	const n = 100
	for i := 0; i < n; i++ {
		if !w.Enqueue(NewRecord(EventLoginSuccess, ResultSuccess)) {
			t.Fatalf("Enqueue %d failed before Start", i)
		}
	}
	w.Start()

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	written, cancelled := storage.counts()
	if cancelled != 0 {
		t.Errorf("%d record(s) were written with an already-cancelled context", cancelled)
	}
	if written != n {
		t.Errorf("wrote %d of %d queued records; the drain lost the rest", written, n)
	}
	if !storage.closed {
		t.Error("storage was not closed")
	}
}

// TestEnqueueDuringStopDoesNotPanic is the regression test for the
// send-on-closed-channel race. Enqueue documented itself as safe to call after
// Stop, but checked the stopped flag and then sent after releasing the lock.
func TestEnqueueDuringStopDoesNotPanic(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		storage := &recordingStorage{}
		w := NewWriter(storage, &WriterConfig{QueueSize: 1024, Workers: 2, StopTimeout: 5 * time.Second})
		w.Start()

		var wg sync.WaitGroup
		var accepted int32

		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					if w.Enqueue(NewRecord(EventLoginSuccess, ResultSuccess)) {
						atomic.AddInt32(&accepted, 1)
					}
				}
			}()
		}

		// Stop concurrently with the enqueuing goroutines.
		go func() { _ = w.Stop() }()
		wg.Wait()
	}
}

// TestEnqueueAfterStopReturnsFalse pins the documented contract.
func TestEnqueueAfterStopReturnsFalse(t *testing.T) {
	storage := &recordingStorage{}
	w := NewWriter(storage, &WriterConfig{QueueSize: 8, Workers: 1, StopTimeout: time.Second})
	w.Start()
	if err := w.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if w.Enqueue(NewRecord(EventLoginSuccess, ResultSuccess)) {
		t.Error("Enqueue() after Stop() returned true")
	}
	// Stop must be idempotent.
	if err := w.Stop(); err != nil {
		t.Errorf("second Stop() error = %v", err)
	}
}

// TestCallbacksAreRaceFree: the callbacks may be set while workers run.
func TestCallbacksAreRaceFree(t *testing.T) {
	storage := &recordingStorage{}
	w := NewWriter(storage, &WriterConfig{QueueSize: 4, Workers: 2, StopTimeout: time.Second})
	w.Start()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			w.OnWriteFailed(func(*Record, error) {})
			w.OnEnqueueFailed(func(*Record) {})
		}
	}()
	for i := 0; i < 200; i++ {
		w.Enqueue(NewRecord(EventLoginSuccess, ResultSuccess))
	}
	wg.Wait()

	if err := w.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
