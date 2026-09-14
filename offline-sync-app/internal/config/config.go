// Package config centralizes environment-driven settings so every entrypoint
// (cmd/server today, cmd/lambda later) reads configuration the same way.
package config

import "os"

type Config struct {
	Addr        string
	DatabaseURL string
	Env         string

	CognitoIssuer   string
	CognitoAudience string

	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	AWSRegion   string

	EventBusEndpoint string
	EventBusName     string

	// Only meaningful for cmd/notifier-lambda in prod — the WebSocket API's
	// per-deployment Management API endpoint. No local equivalent (see
	// internal/wsstub's package comment); cmd/server's in-process notifier
	// loop never reads this.
	WSManagementAPIEndpoint string
}

func Load() Config {
	return Config{
		Addr:        getenv("ADDR", ":8080"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://fieldsync:fieldsync@localhost:5432/fieldsync?sslmode=disable"),
		Env:         getenv("APP_ENV", "local"),

		// COGNITO_ISSUER is the single source of truth (Terraform sets it
		// directly from the real user pool's endpoint attribute, which already
		// includes the pool id). Local dev has no such single value handy, so
		// it falls back to assembling one from COGNITO_ENDPOINT +
		// COGNITO_USER_POOL_ID, which scripts/seed-cognito-local.sh writes —
		// matching the "iss" claim cognito-local stamps on every token.
		CognitoIssuer:   getenv("COGNITO_ISSUER", getenv("COGNITO_ENDPOINT", "http://localhost:9229")+"/"+getenv("COGNITO_USER_POOL_ID", "")),
		CognitoAudience: getenv("COGNITO_CLIENT_ID", ""),

		// No local-dev fallback here (unlike the fields above): in prod these
		// stay empty so internal/storage.New uses real S3's default endpoint +
		// the Lambda execution role's own credentials via the SDK's default
		// chain, instead of accidentally pointing at a localhost MinIO that
		// doesn't exist. Local dev supplies them via .env.local/.env.example.
		S3Endpoint:  getenv("S3_ENDPOINT", ""),
		S3Bucket:    getenv("S3_PHOTOS_BUCKET", "fieldsync-photos"),
		S3AccessKey: getenv("S3_ACCESS_KEY", ""),
		S3SecretKey: getenv("S3_SECRET_KEY", ""),
		AWSRegion:   getenv("AWS_REGION", "us-east-1"),

		// EventBusEndpoint empty (as in S3 above) means real AWS EventBridge in
		// prod; local dev points it at moto-server, the free EventBridge
		// supplement (see docker-compose.yml — LocalStack's free tier never
		// covered EventBridge even before its Phase 3 licensing surprise).
		EventBusEndpoint: getenv("EVENT_BUS_ENDPOINT", ""),
		EventBusName:     getenv("EVENT_BUS_NAME", "fieldsync-events"),

		WSManagementAPIEndpoint: getenv("WS_MANAGEMENT_API_ENDPOINT", ""),
	}
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
