package graph

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Loader[K comparable, V any] interface {
	Load(ctx context.Context, key K) (V, error)
}

type BatchFunc[K comparable, V any] func(ctx context.Context, keys []K) (map[K]V, error)

type LoaderConfig struct {
	Wait     time.Duration
	MaxBatch int
}

// DataLoader must be created per request: its cache is never invalidated and is not scoped to a user.
type DataLoader[K comparable, V any] struct {
	fetch BatchFunc[K, V]
	cfg   LoaderConfig

	mu      sync.Mutex
	batches map[K]*batch[K, V]
	pending *batch[K, V]
}

type batch[K comparable, V any] struct {
	keys    []K
	sent    bool
	done    chan struct{}
	results map[K]V
	err     error
}

func NewDataLoader[K comparable, V any](fetch BatchFunc[K, V], cfg LoaderConfig) *DataLoader[K, V] {
	return &DataLoader[K, V]{fetch: fetch, cfg: cfg, batches: map[K]*batch[K, V]{}}
}

func (l *DataLoader[K, V]) Load(ctx context.Context, key K) (V, error) {
	l.mu.Lock()
	b, cached := l.batches[key]
	if !cached {
		b = l.enqueue(ctx, key)
	}
	l.mu.Unlock()

	select {
	case <-b.done:
		return b.results[key], b.err
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
}

func (l *DataLoader[K, V]) enqueue(ctx context.Context, key K) *batch[K, V] {
	b := l.pending
	if b == nil {
		b = &batch[K, V]{done: make(chan struct{})}
		l.pending = b
		time.AfterFunc(l.cfg.Wait, func() { l.flush(ctx, b) })
	}
	b.keys = append(b.keys, key)
	l.batches[key] = b

	if len(b.keys) >= l.cfg.MaxBatch {
		b.sent = true
		l.pending = nil
		go l.run(ctx, b)
	}
	return b
}

func (l *DataLoader[K, V]) flush(ctx context.Context, b *batch[K, V]) {
	l.mu.Lock()
	if b.sent {
		l.mu.Unlock()
		return
	}
	b.sent = true
	l.pending = nil
	l.mu.Unlock()

	l.run(ctx, b)
}

// run executes on a timer goroutine that gqlgen's panic recovery does not cover, so a panic must not escape.
func (l *DataLoader[K, V]) run(ctx context.Context, b *batch[K, V]) {
	defer close(b.done)
	defer func() {
		if r := recover(); r != nil {
			b.err = fmt.Errorf("batch fetch panicked: %v", r)
		}
	}()
	b.results, b.err = l.fetch(ctx, b.keys)
}
