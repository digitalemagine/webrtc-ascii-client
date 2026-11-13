// Package latest passes values between goroutines keeping only the newest one.
package latest

import (
	"context"
	"sync"
	"sync/atomic"
)

// Slot passes values from one goroutine to another, keeping only the newest:
// Put replaces a value that hasn't been taken yet. A consumer that falls
// behind therefore skips to the latest value instead of queueing.
type Slot[T any] struct {
	mu      sync.Mutex
	value   T
	full    bool
	ready   chan struct{} // has a token while full
	dropped atomic.Int64
}

// NewSlot returns an empty slot.
func NewSlot[T any]() *Slot[T] {
	return &Slot[T]{ready: make(chan struct{}, 1)}
}

// Put stores v, replacing (and counting as dropped) a value not yet taken.
func (s *Slot[T]) Put(v T) {
	s.mu.Lock()
	if s.full {
		s.dropped.Add(1)
	}
	s.value, s.full = v, true
	s.mu.Unlock()
	select {
	case s.ready <- struct{}{}:
	default: // a token is already waiting
	}
}

// Ready is signaled when a value may be available; call TryGet after it fires.
func (s *Slot[T]) Ready() <-chan struct{} { return s.ready }

// TryGet takes the value if there is one.
func (s *Slot[T]) TryGet() (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.value, s.full
	var zero T
	s.value, s.full = zero, false
	return v, ok
}

// Get waits for a value or for ctx to end.
func (s *Slot[T]) Get(ctx context.Context) (T, error) {
	for {
		if v, ok := s.TryGet(); ok {
			return v, nil
		}
		select {
		case <-s.ready:
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
}

// Dropped is how many values were replaced before being taken.
func (s *Slot[T]) Dropped() int64 { return s.dropped.Load() }
