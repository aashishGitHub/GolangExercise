// Package config loads process configuration from the environment. It is
// deliberately dependency-free (no viper, no struct tags, no error return) —
// mirrors offline-sync-app/internal/config exactly.
//
// Convention: local-only settings default to EMPTY, not to a localhost
// address, so a Lambda in prod can never silently point at a dev emulator
// because someone forgot to set an env var.
package config

import (
	"os"
	"strconv"
)

type Config struct {
	Addr        string // ADDR — local HTTP listen address
	DatabaseURL string // DATABASE_URL
	Env         string // APP_ENV — "local" | "prod"

	CognitoIssuer   string // COGNITO_ISSUER, else assembled from the two below
	CognitoEndpoint string // COGNITO_ENDPOINT — local only, empty in prod
	CognitoPoolID   string // COGNITO_USER_POOL_ID
	CognitoAudience string // COGNITO_CLIENT_ID

	RedisAddr string // REDIS_ADDR — empty in prod means "use the SDK default resolution"

	S3Endpoint      string // S3_ENDPOINT — empty in prod: real S3 default endpoint
	S3AccessKey     string // S3_ACCESS_KEY — empty in prod: Lambda execution role
	S3SecretKey     string // S3_SECRET_KEY
	S3LayoutsBucket string // S3_LAYOUTS_BUCKET
	S3TicketsBucket string // S3_TICKETS_BUCKET
	AWSRegion       string // AWS_REGION

	EventBusEndpoint string // EVENT_BUS_ENDPOINT — empty in prod: real EventBridge
	EventBusName     string // EVENT_BUS_NAME

	WSManagementAPIEndpoint string // WS_MANAGEMENT_API_ENDPOINT — empty locally, in-process hub used instead

	HoldTTLSeconds    int    // HOLD_TTL_SECONDS
	MaxSeatsPerHold   int    // MAX_SEATS_PER_HOLD
	WaitingRoomSecret string // WAITING_ROOM_SECRET — HMAC key for admission tokens
	QRSigningSecret   string // QR_SIGNING_SECRET — env key locally, KMS in prod (docs/plan.md Signer)

	// Fake payment provider pathology injection (internal/payment) — all
	// default to 0 so ordinary dev stays clean; the load harness (Phase 10)
	// and reconciler verification turn these on.
	PaymentFailRate      float64 // PAYMENT_FAIL_RATE
	PaymentTimeoutRate   float64 // PAYMENT_TIMEOUT_RATE
	PaymentAmbiguousRate float64 // PAYMENT_AMBIGUOUS_RATE
}

func Load() Config {
	return Config{
		Addr:        getenv("ADDR", ":8080"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://ticketing:ticketing@localhost:5432/ticketing?sslmode=disable"),
		Env:         getenv("APP_ENV", "local"),

		CognitoIssuer:   getenv("COGNITO_ISSUER", ""),
		CognitoEndpoint: getenv("COGNITO_ENDPOINT", "http://localhost:9229"),
		CognitoPoolID:   getenv("COGNITO_USER_POOL_ID", ""),
		CognitoAudience: getenv("COGNITO_CLIENT_ID", ""),

		RedisAddr: getenv("REDIS_ADDR", "localhost:6379"),

		S3Endpoint:      getenv("S3_ENDPOINT", ""),
		S3AccessKey:     getenv("S3_ACCESS_KEY", ""),
		S3SecretKey:     getenv("S3_SECRET_KEY", ""),
		S3LayoutsBucket: getenv("S3_LAYOUTS_BUCKET", "ticketing-layouts"),
		S3TicketsBucket: getenv("S3_TICKETS_BUCKET", "ticketing-tickets"),
		AWSRegion:       getenv("AWS_REGION", "us-east-1"),

		EventBusEndpoint: getenv("EVENT_BUS_ENDPOINT", ""),
		EventBusName:     getenv("EVENT_BUS_NAME", "ticketing-events"),

		WSManagementAPIEndpoint: getenv("WS_MANAGEMENT_API_ENDPOINT", ""),

		HoldTTLSeconds:    atoiOr(getenv("HOLD_TTL_SECONDS", "600"), 600),
		MaxSeatsPerHold:   atoiOr(getenv("MAX_SEATS_PER_HOLD", "8"), 8),
		WaitingRoomSecret: getenv("WAITING_ROOM_SECRET", "local-dev-only-insecure-secret"),
		QRSigningSecret:   getenv("QR_SIGNING_SECRET", "local-dev-only-insecure-qr-secret"),

		PaymentFailRate:      atofOr(getenv("PAYMENT_FAIL_RATE", "0"), 0),
		PaymentTimeoutRate:   atofOr(getenv("PAYMENT_TIMEOUT_RATE", "0"), 0),
		PaymentAmbiguousRate: atofOr(getenv("PAYMENT_AMBIGUOUS_RATE", "0"), 0),
	}
}

// CognitoIssuerURL returns the configured issuer, or assembles the
// cognito-local shape from endpoint+pool id when unset (local dev only —
// Terraform sets COGNITO_ISSUER directly from the real user pool in prod).
func (c Config) CognitoIssuerURL() string {
	if c.CognitoIssuer != "" {
		return c.CognitoIssuer
	}
	return c.CognitoEndpoint + "/" + c.CognitoPoolID
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func atofOr(s string, fallback float64) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	return f
}
