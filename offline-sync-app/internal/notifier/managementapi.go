package notifier

import (
	"context"
	"errors"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi"
	"github.com/aws/aws-sdk-go-v2/service/apigatewaymanagementapi/types"
)

// ManagementAPIPusher is the real-AWS Pusher: it posts to a WebSocket API
// Gateway connection via the Management API. endpoint is per-deployment
// (https://{api-id}.execute-api.{region}.amazonaws.com/{stage}) — there's no
// local equivalent, which is exactly why local dev uses wsstub.Hub instead
// (see that package's comment on why this can't be a separate local process).
type ManagementAPIPusher struct {
	client *apigatewaymanagementapi.Client
}

func NewManagementAPIPusher(ctx context.Context, endpoint, region string) (*ManagementAPIPusher, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := apigatewaymanagementapi.NewFromConfig(cfg, func(o *apigatewaymanagementapi.Options) {
		o.BaseEndpoint = &endpoint
	})
	return &ManagementAPIPusher{client: client}, nil
}

func (p *ManagementAPIPusher) Push(ctx context.Context, connectionID string, message []byte) error {
	_, err := p.client.PostToConnection(ctx, &apigatewaymanagementapi.PostToConnectionInput{
		ConnectionId: &connectionID,
		Data:         message,
	})
	// A GoneException means the client disconnected without a clean close —
	// same "not an error" treatment as wsstub.Hub.Push's missing-connection
	// case, since the stale ws_connections row will be cleaned up separately.
	if err != nil && isGone(err) {
		return nil
	}
	return err
}

func isGone(err error) bool {
	var gone *types.GoneException
	return errors.As(err, &gone)
}
