// cmd/server-lambda is the API Gateway HTTP API v2 twin of cmd/server —
// the IDENTICAL router from internal/wiring, wrapped by
// aws-lambda-go-api-proxy's httpadapter instead of net/http.ListenAndServe.
// modules/api's HTTP API v2 + JWT authorizer terminates auth before this
// function is ever invoked in prod, but internal/auth.Verifier.Middleware
// still runs here too — belt and suspenders costs one JWKS-cache-hit
// per request and means this binary is independently correct even if
// wired directly behind something else.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"ticketing/internal/config"
	"ticketing/internal/wiring"
)

var adapter *httpadapter.HandlerAdapterV2

func init() {
	cfg := config.Load()
	app, err := wiring.New(context.Background(), cfg)
	if err != nil {
		log.Fatalf("wiring: %v", err)
	}
	// Deliberately no defer Close(): a Lambda execution environment is
	// reused across warm invocations, and app.Pool is a connection POOL —
	// closing it after the first request would defeat the entire point of
	// pooling in a serverless context (docs/plan.md's own reasoning for
	// RDS Proxy existing at all).
	adapter = httpadapter.NewV2(app.Router)
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	return adapter.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(handler)
}
