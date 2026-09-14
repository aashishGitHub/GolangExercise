package waitingroom

import (
	"sort"
	"sync"
	"time"
)

// HoldMetrics is a fixed-capacity ring buffer of recent hold-attempt
// outcomes — the "hold CAS p99" and "hold error rate" inputs the AIMD
// controller reads. Windowed by CALL COUNT, not wall-clock time: simpler,
// and at any real admission rate the last N calls already span a bounded,
// recent time window. A production version watching a much larger AND
// bursty request rate would want a genuine time-decayed window instead.
type HoldMetrics struct {
	mu        sync.Mutex
	latencies []time.Duration
	failed    []bool
	idx       int
	filled    bool
	cap       int
}

func NewHoldMetrics(capacity int) *HoldMetrics {
	return &HoldMetrics{
		latencies: make([]time.Duration, capacity),
		failed:    make([]bool, capacity),
		cap:       capacity,
	}
}

func (m *HoldMetrics) Record(d time.Duration, failed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latencies[m.idx] = d
	m.failed[m.idx] = failed
	m.idx++
	if m.idx == m.cap {
		m.idx = 0
		m.filled = true
	}
}

func (m *HoldMetrics) snapshot() ([]time.Duration, []bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.idx
	if m.filled {
		n = m.cap
	}
	lat := append([]time.Duration(nil), m.latencies[:n]...)
	failed := append([]bool(nil), m.failed[:n]...)
	return lat, failed
}

func (m *HoldMetrics) P99() time.Duration {
	lat, _ := m.snapshot()
	if len(lat) == 0 {
		return 0
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	idx := int(float64(len(lat)) * 0.99)
	if idx >= len(lat) {
		idx = len(lat) - 1
	}
	return lat[idx]
}

func (m *HoldMetrics) ErrorRate() float64 {
	_, failed := m.snapshot()
	if len(failed) == 0 {
		return 0
	}
	n := 0
	for _, f := range failed {
		if f {
			n++
		}
	}
	return float64(n) / float64(len(failed))
}
