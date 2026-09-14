// Package holdlock is the Redis contention-filter primitive from
// docs/plan.md "Acquire a hold" step 2 — SET NX PX plus a Lua
// compare-and-delete release. It is a throughput mechanism ONLY: losing a
// key here rejects a contender in ~0.2ms with zero DB work, but Postgres'
// event_seats CAS is the sole arbiter (docs/plan.md decision #1). Every
// exported method distinguishes "lost the key" (ok=false, err=nil) from "Redis
// itself is unreachable" (err=ErrUnavailable) so callers can implement the
// fail-open degrade: skip Redis entirely and go straight to the DB CAS
// rather than treating a non-arbiter's outage as a write-path outage
// (fixed gap #7).
package holdlock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrUnavailable means Redis could not be reached at all — distinct from a
// normal lost-the-key case. Callers should treat every seat as
// provisionally uncontended and fall through to the Postgres CAS.
var ErrUnavailable = errors.New("holdlock: redis unavailable")

// releaseScript is the Lua compare-and-delete: only the current holder of a
// key may delete it, so a release can never clobber a DIFFERENT holder's
// key that has since taken over the same seat after this one expired.
const releaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
else
  return 0
end`

type Lock struct {
	rdb *redis.Client
}

func New(addr string) *Lock {
	return &Lock{rdb: redis.NewClient(&redis.Options{Addr: addr})}
}

// Close releases the underlying connection pool — mainly for tests.
func (l *Lock) Close() error { return l.rdb.Close() }

func key(eventID, seatID int64) string {
	// Hash-tagged so every key for one event lands in the same slot if
	// ElastiCache cluster mode is ever enabled (docs/plan.md fidelity gap).
	return fmt.Sprintf("{event:%d}:seat:%d:hold", eventID, seatID)
}

// Acquire attempts SET NX PX for one seat. ok=true means this holdID now
// owns the key; ok=false (err=nil) means someone else already holds it —
// the caller should NOT reach the DB for this seat's Redis fast-path
// rejection. err=ErrUnavailable means Redis is unreachable — the caller
// must treat this seat as provisionally free and let the DB CAS decide.
func (l *Lock) Acquire(ctx context.Context, eventID, seatID int64, holdID string, ttl time.Duration) (ok bool, err error) {
	set, err := l.rdb.SetNX(ctx, key(eventID, seatID), holdID, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return set, nil
}

// Release runs the compare-and-delete. Best-effort: a Redis error here is
// never fatal to the caller's overall operation (docs/plan.md "Release" —
// "Redis errors here are logged, not fatal"), so it returns the error for
// the caller to log rather than forcing a particular handling policy.
func (l *Lock) Release(ctx context.Context, eventID, seatID int64, holdID string) error {
	_, err := l.rdb.Eval(ctx, releaseScript, []string{key(eventID, seatID)}, holdID).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return nil
}
