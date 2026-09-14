package waitingroom

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"ticketing/internal/db"
)

// AIMD thresholds and rate bounds — docs/plan.md "Waiting room": three
// inputs, any one red triggers multiplicative decrease; all green triggers
// additive increase.
const (
	LatencyRedThreshold   = 500 * time.Millisecond
	PoolRedThreshold      = 0.80
	ErrorRateRedThreshold = 0.01

	MinRate      = 1.0   // admissions/sec — never fully stalls
	MaxRate      = 1000.0
	AdditiveStep = 5.0 // admissions/sec added per green tick
	TickInterval = time.Second
)

type Controller struct {
	q       db.Querier
	rdb     *redis.Client
	pool    *pgxpool.Pool
	metrics *HoldMetrics
}

func NewController(q db.Querier, rdb *redis.Client, pool *pgxpool.Pool, metrics *HoldMetrics) *Controller {
	return &Controller{q: q, rdb: rdb, pool: pool, metrics: metrics}
}

// TickResult is returned for tests/tooling that want to observe a tick's
// outcome directly instead of re-reading it back out of waiting_room_audit.
type TickResult struct {
	Rate                            float64
	Cursor                          int64
	P99Ms                           float64
	PoolUtilization                 float64
	ErrorRate                       float64
	RedLatency, RedPool, RedErrors  bool
}

// Tick runs one AIMD step for one event: read the three signals, decide
// red/green, adjust {event:E}:rate, advance {event:E}:cursor by rate *
// tick length, and append one waiting_room_audit row — every tick, not
// just the interesting ones, so "the loop closing" can be plotted from a
// complete real time series.
func (c *Controller) Tick(ctx context.Context, eventID int64) (TickResult, error) {
	p99 := c.metrics.P99()
	errRate := c.metrics.ErrorRate()

	stat := c.pool.Stat()
	utilization := 0.0
	if stat.MaxConns() > 0 {
		utilization = float64(stat.AcquiredConns()) / float64(stat.MaxConns())
	}

	redLatency := p99 > LatencyRedThreshold
	redPool := utilization > PoolRedThreshold
	redErrors := errRate > ErrorRateRedThreshold
	anyRed := redLatency || redPool || redErrors

	rate, err := c.currentRate(ctx, eventID)
	if err != nil {
		return TickResult{}, err
	}
	if anyRed {
		rate = math.Max(rate*0.5, MinRate)
	} else {
		rate = math.Min(rate+AdditiveStep, MaxRate)
	}
	if err := c.rdb.Set(ctx, rateKey(eventID), rate, 0).Err(); err != nil {
		return TickResult{}, fmt.Errorf("set rate: %w", err)
	}

	// Cursor advances by an integer count of admissions this tick — ZRANK
	// comparisons need an integer, so fractional rate (e.g. MinRate itself
	// below 1 admission/tick) rounds down to at least 0; MinRate=1.0 keeps
	// this from ever fully stalling a green-turning queue.
	cursorIncr := int64(rate * TickInterval.Seconds())
	newCursor, err := c.rdb.IncrBy(ctx, cursorKey(eventID), cursorIncr).Result()
	if err != nil {
		return TickResult{}, fmt.Errorf("incr cursor: %w", err)
	}

	result := TickResult{
		Rate: rate, Cursor: newCursor, P99Ms: float64(p99.Microseconds()) / 1000.0,
		PoolUtilization: utilization, ErrorRate: errRate,
		RedLatency: redLatency, RedPool: redPool, RedErrors: redErrors,
	}
	if err := c.q.InsertWaitingRoomAudit(ctx, db.InsertWaitingRoomAuditParams{
		EventID: eventID, Rate: result.Rate, CursorValue: result.Cursor,
		HoldP99Ms: result.P99Ms, PoolUtilization: result.PoolUtilization, HoldErrorRate: result.ErrorRate,
		RedLatency: result.RedLatency, RedPool: result.RedPool, RedErrors: result.RedErrors,
	}); err != nil {
		return TickResult{}, fmt.Errorf("audit tick: %w", err)
	}
	return result, nil
}

func (c *Controller) currentRate(ctx context.Context, eventID int64) (float64, error) {
	v, err := c.rdb.Get(ctx, rateKey(eventID)).Float64()
	if err == redis.Nil {
		return DefaultRate, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read rate: %w", err)
	}
	return v, nil
}
