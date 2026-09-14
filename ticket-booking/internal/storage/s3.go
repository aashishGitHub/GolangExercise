// Package storage wraps S3 object writes/reads used across two buckets with
// different serving policies: layouts (public, immutable, CDN-fronted in
// prod) and tickets (private, presigned-URL-only — see docs/plan.md
// "Terraform modules" / storage). endpoint/accessKey/secretKey are optional
// — set them for MinIO in local dev; leave them empty in prod and the
// client uses real S3's default endpoint + the Lambda execution role's
// credentials via the SDK's normal default chain (mirrors
// offline-sync-app/internal/storage's own reasoning).
package storage

import (
	"bytes"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
	s3       *s3.Client
	endpoint string // local-dev only; empty in prod (see PublicURL)
}

func New(ctx context.Context, endpoint, region, accessKey, secretKey string) (*Client, error) {
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if accessKey != "" && secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true // MinIO requires path-style bucket addressing
		}
	})

	return &Client{s3: client, endpoint: endpoint}, nil
}

// PutObject uploads body under bucket/key with the given content type and
// Cache-Control header. cmd/event-publisher uses this for layout.json/
// seats.bin with a 1-year-immutable cache-control (docs/plan.md "Static
// layout format").
func (c *Client) PutObject(ctx context.Context, bucket, key string, body []byte, contentType, cacheControl string) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		Body:         bytes.NewReader(body),
		ContentType:  aws.String(contentType),
		CacheControl: aws.String(cacheControl),
	})
	if err != nil {
		return fmt.Errorf("put s3://%s/%s: %w", bucket, key, err)
	}
	return nil
}

// PublicURL builds a directly-fetchable URL for an object in a
// publicly-readable bucket (the layouts bucket, whose anonymous-download
// policy minio-init sets locally and Terraform's OAC-fronted CloudFront
// distribution serves in prod — see docs/plan.md "storage" module).
// Local-dev only: it returns a MinIO path-style URL. In prod this value is
// never used for the layouts bucket — the CloudFront domain from Terraform
// output takes over instead; this method exists only so the same handler
// code path works against both without an environment branch.
func (c *Client) PublicURL(bucket, key string) string {
	if c.endpoint == "" {
		// No local endpoint configured: fall back to the real S3 virtual-
		// hosted URL shape. Never reached once Terraform's CloudFront
		// domain is wired through config in Phase 11.
		return fmt.Sprintf("https://%s.s3.amazonaws.com/%s", bucket, key)
	}
	return fmt.Sprintf("%s/%s/%s", c.endpoint, bucket, key)
}
