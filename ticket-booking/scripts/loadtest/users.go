package main

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

// provisionUsers creates (idempotently) n real cognito-local users and
// returns their real ID tokens. A real virtual-arrival count in the
// thousands would make per-arrival user creation impractical against
// cognito-local's admin API latency — n is a REUSED pool, round-robined
// across journeys. This measures the seat-contention and saga machinery
// under real concurrent load; it does not claim n independent identities
// per second, which is a distinct (and separately provable) claim this
// harness doesn't make.
func provisionUsers(ctx context.Context, endpoint, poolID, clientID string, n int) ([]string, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := cognitoidentityprovider.NewFromConfig(cfg, func(o *cognitoidentityprovider.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	tokens := make([]string, 0, n)
	for i := 0; i < n; i++ {
		username := fmt.Sprintf("loadtest-user-%d@example.com", i)

		_, err := client.AdminCreateUser(ctx, &cognitoidentityprovider.AdminCreateUserInput{
			UserPoolId: aws.String(poolID), Username: aws.String(username),
			UserAttributes: []types.AttributeType{{Name: aws.String("email"), Value: aws.String(username)}},
			MessageAction:  types.MessageActionTypeSuppress,
		})
		if err != nil {
			log.Printf("loadtest: user %s: create skipped (likely exists): %v", username, err)
		}
		if _, err := client.AdminSetUserPassword(ctx, &cognitoidentityprovider.AdminSetUserPasswordInput{
			UserPoolId: aws.String(poolID), Username: aws.String(username),
			Password: aws.String("Passw0rd!"), Permanent: true,
		}); err != nil {
			return nil, fmt.Errorf("set password for %s: %w", username, err)
		}

		out, err := client.AdminInitiateAuth(ctx, &cognitoidentityprovider.AdminInitiateAuthInput{
			UserPoolId: aws.String(poolID), ClientId: aws.String(clientID),
			AuthFlow: types.AuthFlowTypeAdminUserPasswordAuth,
			AuthParameters: map[string]string{
				"USERNAME": username, "PASSWORD": "Passw0rd!",
			},
		})
		if err != nil {
			return nil, fmt.Errorf("auth %s: %w", username, err)
		}
		tokens = append(tokens, *out.AuthenticationResult.IdToken)
	}
	return tokens, nil
}
