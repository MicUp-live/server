// Package catalog uploads immutable catalog assets.
package catalog

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	immutableCacheControl = "public, max-age=31536000, immutable"
	privateCacheControl   = "private, no-store"
)

// AssetStore persists a complete immutable catalog under a versioned prefix.
type AssetStore interface {
	Put(ctx context.Context, prefix string, assets []Asset) error
}

type s3PutAPI interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3AssetStore writes public catalog assets to one private S3 bucket.
type S3AssetStore struct {
	bucket string
	client s3PutAPI
}

// NewS3AssetStore creates an object store for versioned catalog assets.
func NewS3AssetStore(bucket string, client s3PutAPI) (*S3AssetStore, error) {
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("new S3 asset store: bucket is required")
	}
	if client == nil {
		return nil, fmt.Errorf("new S3 asset store: client is required")
	}
	return &S3AssetStore{bucket: bucket, client: client}, nil
}

// Put uploads all assets with immutable caching. A caller must publish the
// catalog version only after this method succeeds.
func (s *S3AssetStore) Put(ctx context.Context, prefix string, assets []Asset) error {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return fmt.Errorf("put catalog assets: prefix is required")
	}
	for _, asset := range assets {
		if err := validateAssetPath(asset.Path); err != nil {
			return err
		}
		cacheControl := immutableCacheControl
		if strings.HasPrefix(prefix, "private/") {
			cacheControl = privateCacheControl
		}
		input := &s3.PutObjectInput{
			Bucket:       aws.String(s.bucket),
			Key:          aws.String(prefix + "/" + asset.Path),
			Body:         bytes.NewReader(asset.Body),
			ContentType:  aws.String(asset.ContentType),
			CacheControl: aws.String(cacheControl),
		}
		if asset.ContentEncoding != "" {
			input.ContentEncoding = aws.String(asset.ContentEncoding)
		}
		if _, err := s.client.PutObject(ctx, input); err != nil {
			return fmt.Errorf("put catalog asset %s: %w", asset.Path, err)
		}
	}
	return nil
}

func validateAssetPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return fmt.Errorf("put catalog assets: invalid asset path %q", path)
	}
	return nil
}
