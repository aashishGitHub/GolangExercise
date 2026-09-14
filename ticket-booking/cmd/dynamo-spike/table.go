package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const tableName = "ticketing-spike-event-seats"

// ensureTable creates the table idempotently — partition key event_id (S),
// sort key seat_id (N). docs/script.md: "Partition key is event_id, sort
// key is seat_id. That gives me per-seat item-level isolation and it
// scales by event."
func ensureTable(ctx context.Context, client *dynamodb.Client) error {
	_, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(tableName)})
	if err == nil {
		return nil // already exists
	}
	var notFound *types.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return fmt.Errorf("describe table: %w", err)
	}

	_, err = client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(tableName),
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("event_id"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("seat_id"), AttributeType: types.ScalarAttributeTypeN},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("event_id"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("seat_id"), KeyType: types.KeyTypeRange},
		},
		BillingMode: types.BillingModePayPerRequest, // no capacity planning needed for a spike
	})
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	waiter := dynamodb.NewTableExistsWaiter(client)
	return waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(tableName)}, tableWaitTimeout)
}

// seedSeats writes n FREE items for eventID — DynamoDB has no schema to
// pre-populate; an item exists once written, so "seeding" here is just
// making sure a fresh spike run starts from a known FREE state, overwriting
// any leftover HELD/BOOKED status from a prior run.
func seedSeats(ctx context.Context, client *dynamodb.Client, eventID string, n int) error {
	for i := 0; i < n; i++ {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item: map[string]types.AttributeValue{
				"event_id": &types.AttributeValueMemberS{Value: eventID},
				"seat_id":  &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", i)},
				"status":   &types.AttributeValueMemberS{Value: statusFree},
			},
		})
		if err != nil {
			return fmt.Errorf("seed seat %d: %w", i, err)
		}
	}
	return nil
}
