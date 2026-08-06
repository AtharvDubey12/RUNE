package queue

import (
	"errors"
	"RUNE/internal/models"
)

// Job represents the payload moving through the async pipeline.
type Job struct {
	Token   string
	Request models.Judge0Request
}

// EngineQueue provides an abstract, thread-safe in-memory queue.
// The underlying channel is unexported to prevent direct access or mutations.
type EngineQueue struct {
	jobs chan Job
}

var (
	ErrQueueFull   = errors.New("queue capacity reached")
	ErrQueueClosed = errors.New("queue has been closed")
)

// NewEngineQueue initializes a new queue with the specified capacity.
// For the RUNE architecture, 10000 is the target capacity.
func NewEngineQueue(capacity int) *EngineQueue {
	return &EngineQueue{
		jobs: make(chan Job, capacity),
	}
}

// Enqueue attempts to push a job into the queue.
// It uses a non-blocking select statement to enable load-shedding if the queue is full.
func (q *EngineQueue) Enqueue(job Job) error {
	// Prevent panics if someone tries to enqueue after graceful shutdown begins
	if q.jobs == nil {
		return ErrQueueClosed
	}

	select {
	case q.jobs <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// Dequeue blocks until a job is available in the queue.
// It returns the Job and a boolean indicating if the queue is still open.
// A false boolean indicates the queue was closed and drained (useful for graceful shutdown).
func (q *EngineQueue) Dequeue() (Job, bool) {
	job, ok := <-q.jobs
	return job, ok
}

// Length returns the current number of pending jobs.
func (q *EngineQueue) Length() int {
	return len(q.jobs)
}

// Capacity returns the maximum number of jobs the queue can hold.
func (q *EngineQueue) Capacity() int {
	return cap(q.jobs)
}

// Close seals the queue, preventing new jobs from being enqueued.
// Existing jobs can still be dequeued until the channel is empty.
func (q *EngineQueue) Close() {
	if q.jobs != nil {
		close(q.jobs)
	}
}