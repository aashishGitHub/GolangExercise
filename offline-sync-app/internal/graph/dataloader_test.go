package graph

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fetchRecorder struct {
	mu      sync.Mutex
	batches [][]int
}

func (f *fetchRecorder) fetch(_ context.Context, keys []int) (map[int]string, error) {
	f.mu.Lock()
	f.batches = append(f.batches, append([]int(nil), keys...))
	f.mu.Unlock()
	out := make(map[int]string, len(keys))
	for _, k := range keys {
		out[k] = "v"
	}
	return out, nil
}

func loadAll(t *testing.T, l Loader[int, string], keys []int) []string {
	t.Helper()
	out := make([]string, len(keys))
	var wg sync.WaitGroup
	for i, k := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := l.Load(context.Background(), k)
			if err != nil {
				t.Errorf("Load(%d): %v", k, err)
			}
			out[i] = v
		}()
	}
	wg.Wait()
	return out
}

func TestDataLoader_ConcurrentLoadsShareOneBatch(t *testing.T) {
	rec := &fetchRecorder{}
	l := NewDataLoader(rec.fetch, LoaderConfig{Wait: 5 * time.Second, MaxBatch: 5})

	got := loadAll(t, l, []int{1, 2, 3, 4, 5})

	if len(rec.batches) != 1 || len(rec.batches[0]) != 5 {
		t.Fatalf("batches = %v, want exactly one batch of 5 keys", rec.batches)
	}
	for i, v := range got {
		if v != "v" {
			t.Errorf("result[%d] = %q, want %q", i, v, "v")
		}
	}
}

func TestDataLoader_DuplicateKeysAreFetchedOnce(t *testing.T) {
	rec := &fetchRecorder{}
	l := NewDataLoader(rec.fetch, LoaderConfig{Wait: 5 * time.Second, MaxBatch: 2})

	loadAll(t, l, []int{7, 7, 7, 8})

	if len(rec.batches) != 1 || len(rec.batches[0]) != 2 {
		t.Fatalf("batches = %v, want one batch of the 2 distinct keys", rec.batches)
	}
}

func TestDataLoader_CacheServesLaterLoadsWithoutFetching(t *testing.T) {
	rec := &fetchRecorder{}
	l := NewDataLoader(rec.fetch, LoaderConfig{Wait: time.Millisecond, MaxBatch: 100})

	loadAll(t, l, []int{1})
	loadAll(t, l, []int{1})

	if len(rec.batches) != 1 {
		t.Fatalf("batches = %v, want a single fetch because the second Load is cached", rec.batches)
	}
}

func TestDataLoader_MaxBatchSplitsAndDispatchesWithoutWaiting(t *testing.T) {
	rec := &fetchRecorder{}
	l := NewDataLoader(rec.fetch, LoaderConfig{Wait: time.Hour, MaxBatch: 3})

	loadAll(t, l, []int{1, 2, 3, 4, 5, 6})

	if len(rec.batches) != 2 {
		t.Fatalf("batches = %v, want 2 batches of 3 sent by size, not by the hour-long timer", rec.batches)
	}
	for _, b := range rec.batches {
		if len(b) != 3 {
			t.Errorf("batch %v has %d keys, want 3", b, len(b))
		}
	}
}

func TestDataLoader_MissingKeyYieldsZeroValueAndNoError(t *testing.T) {
	l := NewDataLoader(func(context.Context, []int) (map[int]string, error) {
		return map[int]string{}, nil
	}, LoaderConfig{Wait: time.Millisecond, MaxBatch: 10})

	v, err := l.Load(context.Background(), 42)

	if err != nil || v != "" {
		t.Fatalf("Load = (%q, %v), want zero value and nil error", v, err)
	}
}

func TestDataLoader_FetchErrorReachesEveryWaiter(t *testing.T) {
	boom := errors.New("boom")
	l := NewDataLoader(func(context.Context, []int) (map[int]string, error) {
		return nil, boom
	}, LoaderConfig{Wait: 5 * time.Second, MaxBatch: 4})

	var failed atomic.Int32
	var wg sync.WaitGroup
	for k := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := l.Load(context.Background(), k); errors.Is(err, boom) {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()

	if failed.Load() != 4 {
		t.Fatalf("%d of 4 waiters saw the batch error, want all 4", failed.Load())
	}
}

func TestDataLoader_FetchPanicBecomesError(t *testing.T) {
	l := NewDataLoader(func(context.Context, []int) (map[int]string, error) {
		panic("kaboom")
	}, LoaderConfig{Wait: time.Millisecond, MaxBatch: 10})

	_, err := l.Load(context.Background(), 1)

	if err == nil {
		t.Fatal("Load returned nil error, want the recovered panic")
	}
}

func TestDataLoader_LoadHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	l := NewDataLoader(func(context.Context, []int) (map[int]string, error) {
		<-release
		return nil, nil
	}, LoaderConfig{Wait: time.Millisecond, MaxBatch: 10})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := l.Load(ctx, 1)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}
