// cmd/ws-connect-lambda handles the real API Gateway WebSocket API's
// $connect/$disconnect routes — the prod counterpart to cmd/server's
// wsHandler (which only works locally because it runs in the same process
// as the connections it accepts; see internal/wsstub's package comment).
// ws_connections lives in Postgres in both prod and local dev — this app's
// actual connection-churn volume (a handful of field workers per disaster
// location) never approaches what would justify DynamoDB's added
// complexity, so the plan doc's original DynamoDB assumption is deliberately
// not followed here.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/auth"
	"fieldsync/internal/config"
	"fieldsync/internal/db"
)

var (
	verifier *auth.Verifier
	q        db.Querier
)

func init() {
	cfg := config.Load()
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	q = db.New(pool)
	verifier = auth.NewVerifier(cfg.CognitoIssuer, cfg.CognitoAudience)
}

func handler(ctx context.Context, req events.APIGatewayWebsocketProxyRequest) (events.APIGatewayProxyResponse, error) {
	connectionID := req.RequestContext.ConnectionID

	switch req.RequestContext.RouteKey {
	case "$connect":
		return handleConnect(ctx, connectionID, req.QueryStringParameters)
	case "$disconnect":
		if err := q.DeleteWSConnection(ctx, connectionID); err != nil {
			log.Printf("ws-connect: delete connection %s: %v", connectionID, err)
		}
		return events.APIGatewayProxyResponse{StatusCode: 200}, nil
	default:
		return events.APIGatewayProxyResponse{StatusCode: 400, Body: "unknown route"}, nil
	}
}

func handleConnect(ctx context.Context, connectionID string, query map[string]string) (events.APIGatewayProxyResponse, error) {
	locationID, err := uuid.Parse(query["locationId"])
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: 400, Body: "invalid locationId"}, nil
	}

	claims, err := verifier.VerifyToken(ctx, query["token"])
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: 401, Body: "unauthorized"}, nil
	}

	if err := q.InsertWSConnection(ctx, db.InsertWSConnectionParams{
		ConnectionID: connectionID,
		UserSub:      claims.Sub,
		LocationID:   locationID,
	}); err != nil {
		log.Printf("ws-connect: insert connection %s: %v", connectionID, err)
		return events.APIGatewayProxyResponse{StatusCode: 500, Body: "failed to register connection"}, nil
	}

	return events.APIGatewayProxyResponse{StatusCode: 200}, nil
}

func main() {
	lambda.Start(handler)
}
