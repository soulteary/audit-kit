package audit

import (
	"context"
	"log"
	"sync"
	"time"
)

// WriterConfig holds configuration for the async writer
type WriterConfig struct {
	QueueSize   int           // Size of the async queue (default: 1000)
	Workers     int           // Number of worker goroutines (default: 2)
	StopTimeout time.Duration // Timeout for graceful shutdown (default: 10s)
}

// DefaultWriterConfig returns default writer configuration
func DefaultWriterConfig() *WriterConfig {
	return &WriterConfig{
		QueueSize:   1000,
		Workers:     2,
		StopTimeout: 10 * time.Second,
	}
}

// Writer handles asynchronous writing of audit records to persistent storage
type Writer struct {
	storage     Storage
	queue       chan *Record
	workers     int
	stopTimeout time.Duration
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	shutdown    chan struct{}
	started     bool
	stopped     bool

	// mu guards started/stopped and, as a read lock, the whole of Enqueue.
	// Stop takes it for writing, which is what guarantees no send is in flight
	// when shutdown begins.
	mu sync.RWMutex

	// Callbacks for monitoring. Guarded by cbMu because they may be set while
	// workers are already running.
	cbMu            sync.RWMutex
	onEnqueueFailed func(record *Record)
	onWriteFailed   func(record *Record, err error)
}

// NewWriter creates a new asynchronous audit writer
func NewWriter(storage Storage, config *WriterConfig) *Writer {
	if config == nil {
		config = DefaultWriterConfig()
	}

	if config.QueueSize <= 0 {
		config.QueueSize = 1000
	}
	if config.Workers <= 0 {
		config.Workers = 2
	}
	if config.StopTimeout <= 0 {
		config.StopTimeout = 10 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Writer{
		storage:     storage,
		queue:       make(chan *Record, config.QueueSize),
		workers:     config.Workers,
		stopTimeout: config.StopTimeout,
		ctx:         ctx,
		cancel:      cancel,
		shutdown:    make(chan struct{}),
	}
}

// OnEnqueueFailed sets a callback for when enqueue fails (queue full)
func (w *Writer) OnEnqueueFailed(fn func(record *Record)) *Writer {
	w.cbMu.Lock()
	w.onEnqueueFailed = fn
	w.cbMu.Unlock()
	return w
}

// OnWriteFailed sets a callback for when write fails
func (w *Writer) OnWriteFailed(fn func(record *Record, err error)) *Writer {
	w.cbMu.Lock()
	w.onWriteFailed = fn
	w.cbMu.Unlock()
	return w
}

func (w *Writer) enqueueFailedCallback() func(*Record) {
	w.cbMu.RLock()
	defer w.cbMu.RUnlock()
	return w.onEnqueueFailed
}

func (w *Writer) writeFailedCallback() func(*Record, error) {
	w.cbMu.RLock()
	defer w.cbMu.RUnlock()
	return w.onWriteFailed
}

// Start starts the writer workers
func (w *Writer) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.started {
		return
	}

	for i := 0; i < w.workers; i++ {
		w.wg.Add(1)
		go w.worker(i)
	}
	w.started = true
	log.Printf("[audit] Started %d audit log writer workers", w.workers)
}

// Stop stops the writer workers gracefully.
//
// Ordering matters here, and used to be wrong in two ways.
//
// The context was cancelled *before* the queue was drained, so every record
// still queued was written with a dead context and the storage backend
// rejected it immediately -- the carefully written drain loop lost everything
// it was draining.
//
// The queue was also closed while an Enqueue could still be mid-send, because
// Enqueue checked the stopped flag under the lock and then sent after
// releasing it. That interleaving panics with "send on closed channel",
// despite Enqueue documenting itself as safe to call after Stop.
//
// Now: mark stopped and wait for in-flight Enqueue calls to finish (the write
// lock does that), signal the drain, let workers finish with a live context,
// and only then cancel and close storage. The queue is never closed.
func (w *Writer) Stop() error {
	w.mu.Lock()
	if !w.started || w.stopped {
		w.mu.Unlock()
		return nil
	}
	// Setting this under the write lock means every Enqueue either completed
	// its send already or will observe stopped and not send at all.
	w.stopped = true
	w.mu.Unlock()

	log.Println("[audit] Stopping audit log writer...")

	// Tell workers to drain what is queued and exit. The queue is deliberately
	// not closed: nothing can send to it any more.
	close(w.shutdown)

	// Wait for all workers to finish processing remaining items
	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	// Wait with timeout
	select {
	case <-done:
		log.Println("[audit] All audit log writer workers stopped")
	case <-time.After(w.stopTimeout):
		log.Printf("[audit] Timeout waiting for audit log writer workers to stop; %d record(s) left unwritten", len(w.queue))
	}

	// Only now is the context no longer needed by anyone.
	w.cancel()

	// Close storage
	if w.storage != nil {
		return w.storage.Close()
	}

	return nil
}

// Enqueue enqueues an audit record for asynchronous writing.
// Returns false if record is nil, the writer is stopped, or the queue is full (non-blocking).
// Safe to call after Stop(); will return false instead of panicking.
func (w *Writer) Enqueue(record *Record) bool {
	if record == nil {
		return false
	}

	// Held for the whole send. Stop takes the same lock for writing, so it
	// cannot begin shutdown while a send is in flight.
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.stopped {
		return false
	}

	select {
	case w.queue <- record:
		return true
	default:
		if cb := w.enqueueFailedCallback(); cb != nil {
			cb(record)
		} else {
			log.Printf("[audit] Audit log queue is full, dropping record: event_type=%s, user_id=%s",
				record.EventType, record.UserID)
		}
		return false
	}
}

// worker is the worker goroutine that processes audit records from the queue
func (w *Writer) worker(id int) {
	defer w.wg.Done()

	for {
		select {
		case <-w.shutdown:
			// Drain what is left. Nothing can enqueue any more, so an empty
			// queue means we are done.
			for {
				select {
				case record := <-w.queue:
					w.writeRecord(id, record)
				default:
					return
				}
			}
		case record := <-w.queue:
			w.writeRecord(id, record)
		}
	}
}

// writeRecord writes a single record to storage
func (w *Writer) writeRecord(workerID int, record *Record) {
	if err := w.storage.Write(w.ctx, record); err != nil {
		if cb := w.writeFailedCallback(); cb != nil {
			cb(record, err)
		} else {
			log.Printf("[audit] Worker %d failed to write record: %v", workerID, err)
		}
	}
}

// Stats returns writer statistics
type Stats struct {
	QueueLength int
	QueueCap    int
	Workers     int
	Started     bool
	Stopped     bool
}

// GetStats returns current writer statistics
func (w *Writer) GetStats() Stats {
	w.mu.RLock()
	started := w.started
	stopped := w.stopped
	w.mu.RUnlock()

	queueLen := len(w.queue)

	return Stats{
		QueueLength: queueLen,
		QueueCap:    cap(w.queue),
		Workers:     w.workers,
		Started:     started,
		Stopped:     stopped,
	}
}
