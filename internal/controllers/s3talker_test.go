package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockS3Client implements S3ClientInterface
type MockS3Client struct {
	mock.Mock
}

func (m *MockS3Client) ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	args := m.Called(ctx, params, optFns)
	return args.Get(0).(*s3.ListBucketsOutput), args.Error(1)
}

func (m *MockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	args := m.Called(ctx, params, optFns)
	return args.Get(0).(*s3.ListObjectsV2Output), args.Error(1)
}

func TestS3UsageInfo_SingleBucket(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024), StorageClass: "STANDARD"},
			{Size: aws.Int64(2048), StorageClass: "STANDARD"},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(3072), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(2), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
}

func TestS3UsageInfo_FailedBucket(t *testing.T) {
	mockClient := new(MockS3Client)

	// bucket1 succeeds, bucket2 fails
	mockClient.On("ListObjectsV2", mock.Anything, &s3.ListObjectsV2Input{
		Bucket:            aws.String("bucket1"),
		ContinuationToken: (*string)(nil),
	}, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents:    []types.Object{{Size: aws.Int64(1024), StorageClass: "STANDARD"}},
		IsTruncated: aws.Bool(false),
	}, nil)

	mockClient.On("ListObjectsV2", mock.Anything, &s3.ListObjectsV2Input{
		Bucket:            aws.String("bucket2"),
		ContinuationToken: (*string)(nil),
	}, mock.Anything).Return((*s3.ListObjectsV2Output)(nil), assert.AnError)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1,bucket2")

	assert.NoError(t, err)
	assert.False(t, summary.EndpointStatus, "EndpointStatus should be false when any bucket fails")
	assert.Equal(t, 1, summary.FailedBucketCount, "FailedBucketCount should be 1")
	assert.Equal(t, 2, summary.BucketCount, "BucketCount should reflect total requested buckets")
	assert.Len(t, summary.S3Buckets, 1, "Only one bucket should be in results")
	assert.Equal(t, "bucket1", summary.S3Buckets[0].BucketName)
}

func TestS3UsageInfo_AllBucketsFail(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).
		Return((*s3.ListObjectsV2Output)(nil), assert.AnError)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1,bucket2,bucket3")

	assert.NoError(t, err)
	assert.False(t, summary.EndpointStatus)
	assert.Equal(t, 3, summary.FailedBucketCount)
	assert.Equal(t, 3, summary.BucketCount)
	assert.Empty(t, summary.S3Buckets)
}

func TestS3UsageInfo_MultipleBuckets(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
			{Size: aws.Int64(2048)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1,bucket2")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(6144), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(4), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 2)
}

func TestS3UsageInfo_TrailingSpaceInBucketNames(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1, bucket2 ")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Len(t, summary.S3Buckets, 2)
	bucketNames := []string{summary.S3Buckets[0].BucketName, summary.S3Buckets[1].BucketName}
	assert.ElementsMatch(t, []string{"bucket1", "bucket2"}, bucketNames)
}

func TestS3UsageInfo_EmptyBucketList(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListBucketsOutput{
		Buckets: []types.Bucket{
			{Name: aws.String("bucket1")},
			{Name: aws.String("bucket2")},
			{Name: aws.String("bucket3")},
		},
	}, nil)
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
			{Size: aws.Int64(2048)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(9216), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(6), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 3)
}

func TestCalculateBucketMetrics(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024), StorageClass: "STANDARD"},
			{Size: aws.Int64(2048), StorageClass: "STANDARD"},
			{Size: aws.Int64(4096), StorageClass: "GLACIER"},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	storageClasses, duration, err := calculateBucketMetrics(context.Background(), "bucket1", mockClient)

	assert.NoError(t, err)
	assert.Equal(t, float64(3072), storageClasses["STANDARD"].Size)
	assert.Equal(t, float64(2), storageClasses["STANDARD"].ObjectNumber)
	assert.Equal(t, float64(4096), storageClasses["GLACIER"].Size)
	assert.Equal(t, float64(1), storageClasses["GLACIER"].ObjectNumber)
	assert.Greater(t, duration, time.Duration(0))
}

func TestS3UsageInfo_WithIAMRole(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(100)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-east-1", mockClient, "bucket1")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(100), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(1), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
}

func TestS3UsageInfo_WithAccessKeys(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(100)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-east-1", mockClient, "bucket1")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(100), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(1), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
}

func TestContextCancellation(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).
		Return(&s3.ListBucketsOutput{Buckets: []types.Bucket{}}, context.Canceled).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	summary, err := S3UsageInfo(ctx, "us-west-2", mockClient, "")

	if err != nil {
		assert.Contains(t, err.Error(), "unable to connect")
	} else {
		assert.False(t, summary.EndpointStatus)
	}
}

func TestContextTimeout(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).
		Return(&s3.ListBucketsOutput{}, context.DeadlineExceeded)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(10 * time.Millisecond)

	summary, err := S3UsageInfo(ctx, "us-west-2", mockClient, "")

	assert.Error(t, err)
	assert.False(t, summary.EndpointStatus)
}

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const testContextKey contextKey = "test-key"

func TestContextPropagationThroughChain(t *testing.T) {
	mockClient := new(MockS3Client)

	var capturedCtx context.Context
	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			capturedCtx = args.Get(0).(context.Context)
		}).
		Return(&s3.ListObjectsV2Output{
			Contents:    []types.Object{},
			IsTruncated: aws.Bool(false),
		}, nil)

	ctx := context.WithValue(context.Background(), testContextKey, "test-value")

	summary, err := S3UsageInfo(ctx, "us-west-2", mockClient, "test-bucket")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.NotNil(t, capturedCtx)
	assert.Equal(t, "test-value", capturedCtx.Value(testContextKey))
}

func TestS3UsageInfo_WithAccessKeysCredentials(t *testing.T) {
	creds := aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("test-access-key", "test-secret-key", ""))
	retrieved, err := creds.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "test-access-key", retrieved.AccessKeyID)
	assert.Equal(t, "test-secret-key", retrieved.SecretAccessKey)
}
