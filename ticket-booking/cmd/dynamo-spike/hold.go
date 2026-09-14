// docs/script.md's model, implemented for real: a hold is a conditional
// UpdateItem, never a lock server. Single-seat holds get DynamoDB's native
// per-item atomicity for free; multi-seat holds pay for TransactWriteItems
// (2x WCU, per docs/script.md) to get atomicity ACROSS seats, which a
// per-seat loop cannot give you — the exact tradeoff docs/plan.md's
// comparison conclusion is supposed to state with real numbers, not just
// assert.
package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	statusFree   = "FREE"
	statusHeld   = "HELD"
	statusBooked = "BOOKED"
)

var ErrConflict = errors.New("dynamo-spike: conditional check failed (seat taken)")

// AcquireHoldSingle: docs/script.md verbatim — "set status to HELD, held_by
// to this user, hold_expires_at to now plus five minutes — only if status
// is FREE, or status is HELD and hold_expires_at is less than now."
// Expiry is READ-TIME (the condition expression), never TTL — TTL is
// janitor-only, exactly like docs/plan.md decision #2's Postgres
// equivalent (hold_expires_at < now() in the WHERE clause, not a
// scheduler).
func AcquireHoldSingle(ctx context.Context, client *dynamodb.Client, eventID string, seatID int, holdID, heldBy string, ttl time.Duration) error {
	now := time.Now()
	expiresAt := now.Add(ttl)

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"event_id": &types.AttributeValueMemberS{Value: eventID},
			"seat_id":  &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", seatID)},
		},
		UpdateExpression: aws.String("SET #status = :held, held_by = :heldBy, hold_id = :holdId, hold_expires_at = :expiresAt"),
		ConditionExpression: aws.String(
			"attribute_not_exists(#status) OR #status = :free OR (#status = :heldCheck AND hold_expires_at < :now)"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":held":      &types.AttributeValueMemberS{Value: statusHeld},
			":heldBy":    &types.AttributeValueMemberS{Value: heldBy},
			":holdId":    &types.AttributeValueMemberS{Value: holdID},
			":expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", expiresAt.UnixMilli())},
			":free":      &types.AttributeValueMemberS{Value: statusFree},
			":heldCheck": &types.AttributeValueMemberS{Value: statusHeld},
			":now":       &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.UnixMilli())},
		},
	})
	if err != nil {
		var condFailed *types.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return ErrConflict
		}
		return fmt.Errorf("acquire hold seat %d: %w", seatID, err)
	}
	return nil
}

// AcquireHoldMulti: TransactWriteItems, all-or-nothing across seats — the
// atomicity a per-seat loop cannot give. docs/script.md: "the tradeoff is
// that transactions cost double the write units and fail hard under
// contention" — this function does NOT retry on ConditionalCheckFailed
// (that's a real conflict, not a transient error); it DOES retry with
// jittered exponential backoff on throttling, matching the doc's stated
// design.
func AcquireHoldMulti(ctx context.Context, client *dynamodb.Client, eventID string, seatIDs []int, holdID, heldBy string, ttl time.Duration) error {
	now := time.Now()
	expiresAt := now.Add(ttl)

	items := make([]types.TransactWriteItem, len(seatIDs))
	for i, seatID := range seatIDs {
		items[i] = types.TransactWriteItem{
			Update: &types.Update{
				TableName: aws.String(tableName),
				Key: map[string]types.AttributeValue{
					"event_id": &types.AttributeValueMemberS{Value: eventID},
					"seat_id":  &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", seatID)},
				},
				UpdateExpression: aws.String("SET #status = :held, held_by = :heldBy, hold_id = :holdId, hold_expires_at = :expiresAt"),
				ConditionExpression: aws.String(
					"attribute_not_exists(#status) OR #status = :free OR (#status = :heldCheck AND hold_expires_at < :now)"),
				ExpressionAttributeNames: map[string]string{"#status": "status"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":held":      &types.AttributeValueMemberS{Value: statusHeld},
					":heldBy":    &types.AttributeValueMemberS{Value: heldBy},
					":holdId":    &types.AttributeValueMemberS{Value: holdID},
					":expiresAt": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", expiresAt.UnixMilli())},
					":free":      &types.AttributeValueMemberS{Value: statusFree},
					":heldCheck": &types.AttributeValueMemberS{Value: statusHeld},
					":now":       &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", now.UnixMilli())},
				},
			},
		}
	}

	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
		if err == nil {
			return nil
		}

		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			if anyConditionFailed(cancelled.CancellationReasons) {
				return ErrConflict // a real conflict — not retried, matching the doc's "fails hard under contention"
			}
			// Every reason was "None" (no-op) or a throughput-shaped
			// cancellation — jittered exponential backoff, per doc.
			backoff := time.Duration(1<<attempt) * 10 * time.Millisecond
			jitter := time.Duration(rand.Int63n(int64(backoff)))
			select {
			case <-time.After(backoff + jitter):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		return fmt.Errorf("transact write items: %w", err)
	}
	return fmt.Errorf("transact write items: exhausted %d attempts", maxAttempts)
}

func anyConditionFailed(reasons []types.CancellationReason) bool {
	for _, r := range reasons {
		if r.Code != nil && *r.Code == "ConditionalCheckFailed" {
			return true
		}
	}
	return false
}

// ConfirmSeats flips HELD -> BOOKED, conditioned on hold_id still matching
// — the same self-idempotency Postgres's ConfirmSeats CAS has (a zombie
// confirm after reclaim+re-hold has a stale hold_id and fails the
// condition, docs/plan.md's fence-token property achieved here for free
// by DynamoDB's own conditional write, no separate fence column needed).
func ConfirmSeats(ctx context.Context, client *dynamodb.Client, eventID string, seatIDs []int, holdID string) error {
	items := make([]types.TransactWriteItem, len(seatIDs))
	for i, seatID := range seatIDs {
		items[i] = types.TransactWriteItem{
			Update: &types.Update{
				TableName: aws.String(tableName),
				Key: map[string]types.AttributeValue{
					"event_id": &types.AttributeValueMemberS{Value: eventID},
					"seat_id":  &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", seatID)},
				},
				UpdateExpression:    aws.String("SET #status = :booked REMOVE hold_id, hold_expires_at"),
				ConditionExpression: aws.String("#status = :held AND hold_id = :holdId"),
				ExpressionAttributeNames: map[string]string{"#status": "status"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":booked": &types.AttributeValueMemberS{Value: statusBooked},
					":held":   &types.AttributeValueMemberS{Value: statusHeld},
					":holdId": &types.AttributeValueMemberS{Value: holdID},
				},
			},
		}
	}
	_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if err != nil {
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) && anyConditionFailed(cancelled.CancellationReasons) {
			return ErrConflict
		}
		return fmt.Errorf("confirm seats: %w", err)
	}
	return nil
}
