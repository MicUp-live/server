// Package catalog tests S3 catalog asset upload requests.
package catalog

import (
	"context"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3AssetStoreWritesImmutableAssets(t *testing.T) {
	client := &fakeS3Client{}
	store, err := NewS3AssetStore("catalog-bucket", client)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Put(context.Background(), "catalogs/version-1", []Asset{{Path: "search.json.gz", Body: []byte("data"), ContentType: "application/json", ContentEncoding: "gzip"}})
	if err != nil {
		t.Fatalf("put assets: %v", err)
	}
	if len(client.inputs) != 1 || *client.inputs[0].Key != "catalogs/version-1/search.json.gz" || *client.inputs[0].CacheControl != immutableCacheControl || *client.inputs[0].ContentEncoding != "gzip" {
		t.Fatalf("put input = %+v", client.inputs)
	}
}

func TestS3AssetStoreRejectsUnsafePaths(t *testing.T) {
	store, _ := NewS3AssetStore("catalog-bucket", &fakeS3Client{})
	if err := store.Put(context.Background(), "catalogs/version-1", []Asset{{Path: "../secret", ContentType: "application/json"}}); err == nil {
		t.Fatal("unsafe path succeeded")
	}
}

type fakeS3Client struct {
	inputs []*s3.PutObjectInput
	err    error
}

func (c *fakeS3Client) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if c.err != nil {
		return nil, c.err
	}
	_, _ = io.ReadAll(input.Body)
	c.inputs = append(c.inputs, input)
	return &s3.PutObjectOutput{}, nil
}
