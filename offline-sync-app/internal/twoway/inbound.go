package twoway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"fieldsync/internal/db"
)

var errInvalidWebhook = errors.New("webhook body has no issue id or version")

// ProcessWebhook decodes one stored delivery and applies it. Its signature is
// webhook.Processor's, so a method value plugs straight into webhook.Worker.
func (a *Applier) ProcessWebhook(ctx context.Context, q db.Querier, body []byte) (string, error) {
	var env struct {
		Issue ExternalIssue `json:"issue"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("decode webhook body: %w", err)
	}
	if env.Issue.ID == "" || env.Issue.Version <= 0 {
		return "", errInvalidWebhook
	}

	outcome, err := a.Apply(ctx, q, SourceWebhook, env.Issue)
	return string(outcome), err
}
