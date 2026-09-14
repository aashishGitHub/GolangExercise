package outbox

import (
	"context"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
)

// NewEventBridgeClient mirrors internal/storage.New's endpoint-override
// pattern: empty endpoint means real AWS EventBridge (region + the caller's
// IAM identity via the SDK's default credential chain); a non-empty one
// points at moto-server for local dev.
func NewEventBridgeClient(ctx context.Context, endpoint, region string) (*eventbridge.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}

	return eventbridge.NewFromConfig(cfg, func(o *eventbridge.Options) {
		if endpoint != "" {
			o.BaseEndpoint = &endpoint
		}
	}), nil
}
