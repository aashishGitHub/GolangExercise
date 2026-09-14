package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
)

// runRaceTest is the DynamoDB-path proof of docs/plan.md's thesis — the
// EXACT same shape as Phase 3's TestAcquireHold_ExactlyOneWinnerUnderRace:
// 200 goroutines released from one barrier, racing the SAME single seat.
// Repeated 20x, because a race test that passes once proves nothing.
func runRaceTest(ctx context.Context, client *dynamodb.Client, _ int) {
	const eventID = "race-test-event"
	const seatID = 1
	const rounds = 20
	const racers = 200

	for round := 0; round < rounds; round++ {
		// Reset the seat to FREE before each round.
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item: map[string]types.AttributeValue{
				"event_id": &types.AttributeValueMemberS{Value: eventID},
				"seat_id":  &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", seatID)},
				"status":   &types.AttributeValueMemberS{Value: statusFree},
			},
		})
		if err != nil {
			log.Fatalf("round %d: reset seat: %v", round, err)
		}

		var wins, conflicts int64
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func() {
				defer wg.Done()
				<-start
				holdID := uuid.New().String()
				err := AcquireHoldSingle(ctx, client, eventID, seatID, holdID, "racer", 5*time.Minute)
				if err == nil {
					atomic.AddInt64(&wins, 1)
				} else if err == ErrConflict {
					atomic.AddInt64(&conflicts, 1)
				} else {
					log.Printf("round %d: unexpected error: %v", round, err)
				}
			}()
		}
		close(start)
		wg.Wait()

		status := "PASS"
		if wins != 1 || conflicts != racers-1 {
			status = "FAIL"
		}
		log.Printf("race round %d/%d: wins=%d conflicts=%d [%s]", round+1, rounds, wins, conflicts, status)
		if status == "FAIL" {
			log.Fatalf("race test FAILED at round %d — this should be structurally impossible", round+1)
		}
	}
	log.Printf("race test: %d/%d rounds passed — exactly one winner every time, DynamoDB's native conditional write", rounds, rounds)
}
