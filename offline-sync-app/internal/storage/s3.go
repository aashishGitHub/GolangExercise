// Package storage wraps presigned S3 uploads: the client PUTs photo bytes
// directly to S3, never through the Lambda-lith, dodging Lambda payload
// limits (per the plan doc's locked decision). endpoint/accessKey/secretKey
// are optional — set them for MinIO in local dev; leave them empty in prod
// and the client uses real S3's default endpoint + the Lambda execution
// role's credentials via the SDK's normal default chain.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Storage struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

func New(ctx context.Context, endpoint, region, bucket, accessKey, secretKey string) (*Storage, error) {
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if accessKey != "" && secretKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true // MinIO/LocalStack require path-style bucket addressing
		}
	})

	return &Storage{client: client, presign: s3.NewPresignClient(client), bucket: bucket}, nil
}

// PresignPut returns a URL the client can PUT the photo bytes to directly.
func (s *Storage) PresignPut(ctx context.Context, key, contentType string, expires time.Duration) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// Exists confirms the object actually landed in S3 before the confirm route
// commits the photo row — defends against confirming an upload that never
// completed (dropped connection, client crash mid-PUT).
func (s *Storage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	return false, err
}

// EnsureBucket creates the bucket if it doesn't exist — real AWS provisions
// this via Terraform; local dev doesn't run Terraform apply, so the server
// creates it itself against LocalStack on startup.
func (s *Storage) EnsureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err == nil {
		return nil
	}
	_, err = s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	return err
}
