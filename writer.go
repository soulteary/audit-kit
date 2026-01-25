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
	started     bool
	stopped     bool
	mu          sync.Mutex

	// Callbacks for monitoring
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
	}
}

// OnEnqueueFailed sets a callback for when enqueue fails (queue full)
func (w *Writer) OnEnqueueFailed(fn func(record *Record)) *Writer {
	w.onEnqueueFailed = fn
	return w
}

// OnWriteFailed sets a callback for when write fails
func (w *Writer) OnWriteFailed(fn func(record *Record, err error)) *Writer {
	w.onWriteFailed = fn
	return w
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

// Stop stops the writer workers gracefully
func (w *Writer) Stop() error {
	w.mu.Lock()
	if !w.started || w.stopped {
		w.mu.Unlock()
		return nil
	}
	w.stopped = true
	w.mu.Unlock()

	log.Println("[audit] Stopping audit log writer...")

	// Cancel context to signal workers to stop
	w.cancel()

	// Close queue to prevent new writes
	close(w.queue)

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
		log.Println("[audit] Timeout waiting for audit log writer workers to stop")
	}

	// Close storage
	if w.storage != nil {
		return w.storage.Close()
	}

	return nil
}

// Enqueue enqueues an audit record for asynchronous writing
// Returns false if queue is full (non-blocking)
func (w *Writer) Enqueue(record *Record) bool {
	select {
	case w.queue <- record:
		return true
	default:
		// Queue is full
		if w.onEnqueueFailed != nil {
			w.onEnqueueFailed(record)
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
		case <-w.ctx.Done():
			// Drain remaining items from queue before stopping
			for {
				select {
				case record, ok := <-w.queue:
					if !ok {
						return
					}
					w.writeRecord(id, record)
				default:
					return
				}
			}
		case record, ok := <-w.queue:
			if !ok {
				// Queue closed
				return
			}
			w.writeRecord(id, record)
		}
	}
}

// writeRecord writes a single record to storage
func (w *Writer) writeRecord(workerID int, record *Record) {
	if err := w.storage.Write(w.ctx, record); err != nil {
		if w.onWriteFailed != nil {
			w.onWriteFailed(record, err)
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
	w.mu.Lock()
	started := w.started
	stopped := w.stopped
	w.mu.Unlock()

	queueLen := 0
	if !stopped {
		queueLen = len(w.queue)
	}

	return Stats{
		QueueLength: queueLen,
		QueueCap:    cap(w.queue),
		Workers:     w.workers,
		Started:     started,
		Stopped:     stopped,
	}
}
