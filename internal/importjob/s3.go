package importjob

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ManifestWriter stores private import manifests in object storage.
type ManifestWriter struct {
	Bucket    string
	Client    *s3.Client
	Presigner *s3.PresignClient
}

func (w *ManifestWriter) Presign(ctx context.Context, key string, size int64) (string, error) {
	if w == nil || w.Presigner == nil || w.Bucket == "" || key == "" || size <= 0 {
		return "", fmt.Errorf("manifest presign is not configured")
	}
	result, err := w.Presigner.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(w.Bucket), Key: aws.String(key), ContentLength: &size, ContentType: aws.String("application/json")}, func(opts *s3.PresignOptions) { opts.Expires = 15 * time.Minute })
	if err != nil {
		return "", err
	}
	return result.URL, nil
}

func (w *ManifestWriter) Put(ctx context.Context, key string, body []byte) error {
	if w == nil || w.Client == nil || w.Bucket == "" {
		return fmt.Errorf("manifest storage is not configured")
	}
	_, err := w.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(w.Bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentType: aws.String("application/json")})
	return err
}

func (w *ManifestWriter) DeleteKey(ctx context.Context, key string) error {
	if w == nil || w.Client == nil || w.Bucket == "" || key == "" {
		return nil
	}
	_, err := w.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(w.Bucket),
		Key:    aws.String(key),
	})
	return err
}

func (w *ManifestWriter) DeletePrefix(ctx context.Context, prefix string) error {
	if w == nil || w.Client == nil || w.Bucket == "" || prefix == "" {
		return nil
	}
	paginator := s3.NewListObjectsV2Paginator(w.Client, &s3.ListObjectsV2Input{
		Bucket: aws.String(w.Bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, obj := range page.Contents {
			if obj.Key != nil {
				if _, err := w.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(w.Bucket),
					Key:    obj.Key,
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
