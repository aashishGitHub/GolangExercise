// Package waitingroom is docs/plan.md's virtual waiting room: a Redis
// per-event ZSET ordering arrivals, a cursor advanced by an AIMD
// controller reading real backpressure signals, and HMAC admission tokens
// bound to the Cognito sub so admission can't be traded between users.
//
// A joiner is "admitted" once their ZRANK in the arrival ZSET is less than
// the current cursor — the cursor is the only mutable state the AIMD
// controller touches, so admission logic here never has to reason about
// rate directly, only about where the cursor currently sits.
package waitingroom

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// DefaultRate is used if the AIMD controller hasn't ticked yet for an
	// event (e.g. right after a deploy) — conservative rather than
	// admitting nobody (rate=0 would mean etaSeconds is undefined) or
	// everybody (defeats the point of a queue that exists at all).
	DefaultRate = 1.0
)

type Queue struct {
	rdb    *redis.Client
	secret []byte
}

func New(rdb *redis.Client, secret string) *Queue {
	return &Queue{rdb: rdb, secret: []byte(secret)}
}

func queueKey(eventID int64) string  { return fmt.Sprintf("{event:%d}:q", eventID) }
func cursorKey(eventID int64) string { return fmt.Sprintf("{event:%d}:cursor", eventID) }
func rateKey(eventID int64) string   { return fmt.Sprintf("{event:%d}:rate", eventID) }

type JoinResult struct {
	Admitted   bool
	Token      string // set iff Admitted
	Position   int64  // 0-indexed distance behind the cursor, set iff !Admitted
	EtaSeconds int
}

// Join adds sub to the arrival ZSET (ZADD NX: a refresh/retry keeps the
// ORIGINAL arrival timestamp, never pushes someone back in line) and
// reports whether they're already past the cursor.
func (q *Queue) Join(ctx context.Context, eventID int64, sub string) (JoinResult, error) {
	now := float64(time.Now().UnixMicro())
	if err := q.rdb.ZAddNX(ctx, queueKey(eventID), redis.Z{Score: now, Member: sub}).Err(); err != nil {
		return JoinResult{}, fmt.Errorf("join queue: %w", err)
	}

	rank, err := q.rdb.ZRank(ctx, queueKey(eventID), sub).Result()
	if err != nil {
		return JoinResult{}, fmt.Errorf("rank: %w", err)
	}
	cursor, err := q.cursorValue(ctx, eventID)
	if err != nil {
		return JoinResult{}, err
	}

	if rank < cursor {
		token, err := q.issueToken(sub, eventID, rank)
		if err != nil {
			return JoinResult{}, fmt.Errorf("issue token: %w", err)
		}
		return JoinResult{Admitted: true, Token: token}, nil
	}

	rate, err := q.currentRate(ctx, eventID)
	if err != nil {
		return JoinResult{}, err
	}
	position := rank - cursor
	eta := int(float64(position) / rate)
	return JoinResult{Admitted: false, Position: position, EtaSeconds: eta}, nil
}

func (q *Queue) cursorValue(ctx context.Context, eventID int64) (int64, error) {
	v, err := q.rdb.Get(ctx, cursorKey(eventID)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read cursor: %w", err)
	}
	return v, nil
}

func (q *Queue) currentRate(ctx context.Context, eventID int64) (float64, error) {
	v, err := q.rdb.Get(ctx, rateKey(eventID)).Float64()
	if err == redis.Nil {
		return DefaultRate, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read rate: %w", err)
	}
	return v, nil
}
