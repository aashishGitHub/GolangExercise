// cmd/dynamo-spike evaluates docs/script.md's DynamoDB-arbiter model
// against amazon/dynamodb-local — docs/plan.md Phase 12, explicitly
// non-blocking and explicitly not dual-maintained. Nothing here wires
// into cmd/server; this package exists to produce real, measured numbers
// for the comparison writeup at docs/dynamodb-comparison.md, not to become
// a second production path.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

const tableWaitTimeout = 30 * time.Second

func newClient(ctx context.Context, endpoint string) (*dynamodb.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
	)
	if err != nil {
		return nil, err
	}
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
	}), nil
}

func main() {
	endpoint := flag.String("endpoint", "http://localhost:8000", "dynamodb-local endpoint")
	mode := flag.String("mode", "race", "race | loadtest")
	seatCount := flag.Int("seats", 30000, "seats to seed for the event")
	workers := flag.Int("workers", 200, "concurrent goroutines")
	duration := flag.Duration("duration", 15*time.Second, "loadtest duration")
	contention := flag.String("contention", "uniform", "uniform | hot (loadtest mode only)")
	multiSeat := flag.Int("multi-seat", 1, "seats per hold — 1 uses UpdateItem, >1 uses TransactWriteItems")
	flag.Parse()

	ctx := context.Background()
	client, err := newClient(ctx, *endpoint)
	if err != nil {
		log.Fatalf("dynamo client: %v", err)
	}

	if err := ensureTable(ctx, client); err != nil {
		log.Fatalf("ensure table: %v", err)
	}

	switch *mode {
	case "race":
		runRaceTest(ctx, client, *seatCount)
	case "loadtest":
		runLoadTest(ctx, client, loadTestConfig{
			seatCount:  *seatCount,
			workers:    *workers,
			duration:   *duration,
			contention: *contention,
			multiSeat:  *multiSeat,
		})
	default:
		fmt.Println("unknown -mode, want race or loadtest")
	}
}
