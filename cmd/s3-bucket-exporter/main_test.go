package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/config"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/controllers"
)

// Mock implementation for S3 client interface
type mockS3Client struct {
	controllers.S3ClientInterface
	listBucketsFunc   func(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	listObjectsV2Func func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

func (m *mockS3Client) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return m.listBucketsFunc(ctx, params, optFns...)
}

func (m *mockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return m.listObjectsV2Func(ctx, params, optFns...)
}

func TestHealthHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/health", nil)
	assert.NoError(t, err)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(healthHandler)
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "OK", rr.Body.String())
}

func TestUpdateMetrics(t *testing.T) {
	mockClient := &mockS3Client{
		listBucketsFunc: func(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
			return &s3.ListBucketsOutput{
				Buckets: []types.Bucket{
					{Name: aws.String("test-bucket")},
				},
			}, nil
		},
		listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents: []types.Object{
					{
						Key:          aws.String("test-object"),
						Size:         aws.Int64(1024),
						StorageClass: types.ObjectStorageClass("STANDARD"),
					},
				},
				IsTruncated: aws.Bool(false),
			}, nil
		},
	}

	config.S3Endpoint = "http://localhost"
	config.S3AccessKey = "test"
	config.S3SecretKey = "test"
	config.S3Region = "us-east-1"
	config.S3BucketNames = "test-bucket"

	controllers.SetS3Client(mockClient)
	defer controllers.ResetS3Client()

	collector := controllers.NewS3Collector(config.S3Endpoint, config.S3Region)
	interval := 100 * time.Millisecond

	go updateMetrics(collector, interval)

	time.Sleep(interval * 2)

	assert.NoError(t, collector.Err, "Expected no error with mock client")
	assert.Equal(t, true, collector.Metrics.EndpointStatus, "EndpointStatus should be true")
	metrics := collector.Metrics.StorageClasses["STANDARD"]
	assert.Equal(t, 1024.0, metrics.Size, "Total size should match")
	assert.Equal(t, 1.0, metrics.ObjectNumber, "Total object number should match")
	require.Len(t, collector.Metrics.S3Buckets, 1, "Should have exactly one bucket")

	bucket := collector.Metrics.S3Buckets[0]
	assert.Equal(t, "test-bucket", bucket.BucketName, "BucketName should match")
	bucketMetrics := bucket.StorageClasses["STANDARD"]
	assert.Equal(t, 1024.0, bucketMetrics.Size, "Bucket size should match")
	assert.Equal(t, 1.0, bucketMetrics.ObjectNumber, "Bucket object number should match")
	assert.Greater(t, collector.Metrics.TotalListDuration, time.Duration(0), "TotalListDuration should be positive")
	assert.Greater(t, bucket.ListDuration, time.Duration(0), "Bucket ListDuration should be positive")
}
