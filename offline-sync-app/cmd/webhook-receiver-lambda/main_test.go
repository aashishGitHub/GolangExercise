package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"fieldsync/internal/twoway"
)

const testSecret = "whsec-test"

type memInbox map[string][]byte

func (m memInbox) Insert(_ context.Context, system, deliveryID string, body []byte) (bool, error) {
	key := system + "/" + deliveryID
	if _, dup := m[key]; dup {
		return false, nil
	}
	m[key] = body
	return true, nil
}

func signedEvent(secret, path, deliveryID, body string) events.APIGatewayV2HTTPRequest {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + deliveryID + "." + body))
	return events.APIGatewayV2HTTPRequest{
		RawPath: path,
		Headers: map[string]string{
			"content-type":        "application/json",
			"x-webhook-delivery":  deliveryID,
			"x-webhook-timestamp": ts,
			"x-webhook-signature": "sha256=" + hex.EncodeToString(mac.Sum(nil)),
		},
		Body: body,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: http.MethodPost, Path: path},
		},
	}
}

func base64Body(e events.APIGatewayV2HTTPRequest) events.APIGatewayV2HTTPRequest {
	e.Body = base64.StdEncoding.EncodeToString([]byte(e.Body))
	e.IsBase64Encoded = true
	return e
}

func TestAdapter_ProxyEvent(t *testing.T) {
	const body = `{"event": "issue.updated",  "issue":{"id":"MJ-1"}}`
	tests := []struct {
		name       string
		event      events.APIGatewayV2HTTPRequest
		wantStatus int
		wantStored int
	}{
		{"valid signature is 202 and stored", signedEvent(testSecret, "/webhooks/mockjira", "dlv-1", body), http.StatusAccepted, 1},
		{"base64-encoded body still verifies against the raw bytes", base64Body(signedEvent(testSecret, "/webhooks/mockjira", "dlv-1", body)), http.StatusAccepted, 1},
		{"wrong secret is 401 and nothing stored", signedEvent("not-the-secret", "/webhooks/mockjira", "dlv-1", body), http.StatusUnauthorized, 0},
		{"another system's path is 404", signedEvent(testSecret, "/webhooks/other", "dlv-1", body), http.StatusNotFound, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inbox := memInbox{}
			cfg := twoway.Config{System: "mockjira", WebhookSecret: testSecret, WebhookTolerance: 5 * time.Minute, MaxBodyBytes: 1 << 20}

			resp, err := newAdapter(cfg, inbox).ProxyWithContext(context.Background(), tt.event)

			if err != nil || resp.StatusCode != tt.wantStatus || len(inbox) != tt.wantStored {
				t.Errorf("got %d %s, err %v, %d stored; want %d and %d stored", resp.StatusCode, resp.Body, err, len(inbox), tt.wantStatus, tt.wantStored)
			}
			if tt.wantStored == 1 && string(inbox["mockjira/dlv-1"]) != body {
				t.Errorf("stored body = %q, want the raw bytes %q", inbox["mockjira/dlv-1"], body)
			}
		})
	}
}
