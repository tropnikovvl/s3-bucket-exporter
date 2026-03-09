package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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

type mockS3Client struct {
	listBucketsFunc   func(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	listObjectsV2Func func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

func (m *mockS3Client) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return m.listBucketsFunc(ctx, params, optFns...)
}

func (m *mockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return m.listObjectsV2Func(ctx, params, optFns...)
}

func mockFactory(client controllers.S3ClientInterface) func(aws.Config) controllers.S3ClientInterface {
	return func(aws.Config) controllers.S3ClientInterface { return client }
}

func testConfig() *config.Config {
	return &config.Config{
		S3Endpoint:    "http://localhost",
		S3AccessKey:   "test",
		S3SecretKey:   "test",
		S3Region:      "us-east-1",
		S3BucketNames: "",
	}
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

	cfg := testConfig()
	cfg.S3BucketNames = "test-bucket"

	collector := controllers.NewS3Collector(cfg.S3Endpoint, cfg.S3Region)
	interval := 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go updateMetrics(ctx, collector, cfg, interval, mockFactory(mockClient))

	time.Sleep(interval * 2)

	m, err := collector.GetMetrics()

	assert.NoError(t, err, "Expected no error with mock client")
	assert.True(t, m.EndpointStatus, "EndpointStatus should be true")
	storageMetrics := m.StorageClasses["STANDARD"]
	assert.Equal(t, 1024.0, storageMetrics.Size, "Total size should match")
	assert.Equal(t, 1.0, storageMetrics.ObjectNumber, "Total object number should match")
	require.Len(t, m.S3Buckets, 1, "Should have exactly one bucket")

	bucket := m.S3Buckets[0]
	assert.Equal(t, "test-bucket", bucket.BucketName, "BucketName should match")
	bucketMetrics := bucket.StorageClasses["STANDARD"]
	assert.Equal(t, 1024.0, bucketMetrics.Size, "Bucket size should match")
	assert.Equal(t, 1.0, bucketMetrics.ObjectNumber, "Bucket object number should match")
	assert.Greater(t, m.TotalListDuration, time.Duration(0), "TotalListDuration should be positive")
	assert.Greater(t, bucket.ListDuration, time.Duration(0), "Bucket ListDuration should be positive")
}

func TestUpdateMetricsContextCancellation(t *testing.T) {
	var callCount int
	var mu sync.Mutex
	mockClient := &mockS3Client{
		listBucketsFunc: func(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			return &s3.ListBucketsOutput{
				Buckets: []types.Bucket{
					{Name: aws.String("test-bucket")},
				},
			}, nil
		},
		listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents:    []types.Object{},
				IsTruncated: aws.Bool(false),
			}, nil
		},
	}

	cfg := testConfig()
	cfg.S3BucketNames = "test-bucket"

	collector := controllers.NewS3Collector(cfg.S3Endpoint, cfg.S3Region)
	interval := 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	go updateMetrics(ctx, collector, cfg, interval, mockFactory(mockClient))

	time.Sleep(interval / 2)

	mu.Lock()
	initialCallCount := callCount
	mu.Unlock()

	cancel()

	time.Sleep(interval * 2)

	mu.Lock()
	finalCallCount := callCount
	mu.Unlock()

	assert.LessOrEqual(t, finalCallCount-initialCallCount, 1,
		"Metrics collection should stop after context cancellation")
}

func TestUpdateMetricsImmediateCollection(t *testing.T) {
	var collectionTimes []time.Time
	var mu sync.Mutex
	mockClient := &mockS3Client{
		listBucketsFunc: func(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
			mu.Lock()
			collectionTimes = append(collectionTimes, time.Now())
			mu.Unlock()
			return &s3.ListBucketsOutput{
				Buckets: []types.Bucket{
					{Name: aws.String("test-bucket")},
				},
			}, nil
		},
		listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents:    []types.Object{},
				IsTruncated: aws.Bool(false),
			}, nil
		},
	}

	cfg := testConfig()

	collector := controllers.NewS3Collector(cfg.S3Endpoint, cfg.S3Region)
	interval := 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startTime := time.Now()
	go updateMetrics(ctx, collector, cfg, interval, mockFactory(mockClient))

	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	collectionCount := len(collectionTimes)
	var firstCollectionTime time.Time
	if collectionCount > 0 {
		firstCollectionTime = collectionTimes[0]
	}
	mu.Unlock()

	require.Greater(t, collectionCount, 0, "Should have at least one collection")

	firstCollectionDelay := firstCollectionTime.Sub(startTime)
	assert.Less(t, firstCollectionDelay, interval,
		"First metrics collection should happen immediately, not after interval")
}

func TestUpdateMetricsContextTimeout(t *testing.T) {
	blockingClient := &mockS3Client{
		listBucketsFunc: func(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(200 * time.Millisecond):
				return &s3.ListBucketsOutput{
					Buckets: []types.Bucket{
						{Name: aws.String("test-bucket")},
					},
				}, nil
			}
		},
		listObjectsV2Func: func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{
				Contents:    []types.Object{},
				IsTruncated: aws.Bool(false),
			}, nil
		},
	}

	cfg := testConfig()

	collector := controllers.NewS3Collector(cfg.S3Endpoint, cfg.S3Region)
	interval := 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go updateMetrics(ctx, collector, cfg, interval, mockFactory(blockingClient))

	time.Sleep(150 * time.Millisecond)

	m, err := collector.GetMetrics()

	if err == nil {
		assert.False(t, m.EndpointStatus, "Endpoint should be marked as down due to timeout")
	}
}
