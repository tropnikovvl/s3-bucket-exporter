package controllers

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
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

func (m *MockS3Client) ListObjectVersions(ctx context.Context, params *s3.ListObjectVersionsInput, optFns ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	args := m.Called(ctx, params, optFns)
	return args.Get(0).(*s3.ListObjectVersionsOutput), args.Error(1)
}

func TestS3UsageInfo_SingleBucket(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(1024), StorageClass: "STANDARD", IsLatest: aws.Bool(true)},
			{Size: aws.Int64(2048), StorageClass: "STANDARD", IsLatest: aws.Bool(true)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1", 25)

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(3072), summary.StorageClasses["STANDARD"].CurrentSize)
	assert.Equal(t, float64(2), summary.StorageClasses["STANDARD"].CurrentObjectNumber)
	assert.Equal(t, float64(0), summary.StorageClasses["STANDARD"].NoncurrentSize)
	assert.Equal(t, float64(0), summary.StorageClasses["STANDARD"].NoncurrentObjectNumber)
	assert.Equal(t, float64(0), summary.DeleteMarkers)
	assert.Len(t, summary.S3Buckets, 1)
}

func TestS3UsageInfo_VersionedBucket(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(1024), StorageClass: "STANDARD", IsLatest: aws.Bool(true)},
			{Size: aws.Int64(512), StorageClass: "STANDARD", IsLatest: aws.Bool(false)},
			{Size: aws.Int64(256), StorageClass: "STANDARD", IsLatest: aws.Bool(false)},
		},
		DeleteMarkers: []types.DeleteMarkerEntry{
			{IsLatest: aws.Bool(true), Key: aws.String("deleted-file.txt")},
			{IsLatest: aws.Bool(false), Key: aws.String("old-deleted.txt")},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1", 25)

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(1024), summary.StorageClasses["STANDARD"].CurrentSize)
	assert.Equal(t, float64(1), summary.StorageClasses["STANDARD"].CurrentObjectNumber)
	assert.Equal(t, float64(768), summary.StorageClasses["STANDARD"].NoncurrentSize)
	assert.Equal(t, float64(2), summary.StorageClasses["STANDARD"].NoncurrentObjectNumber)
	assert.Equal(t, float64(2), summary.DeleteMarkers)
	assert.Equal(t, float64(2), summary.S3Buckets[0].DeleteMarkers)
}

func TestS3UsageInfo_FailedBucket(t *testing.T) {
	mockClient := new(MockS3Client)

	// Discovery probes (Delimiter set) return no CommonPrefixes → each bucket
	// lists as a single range; the per-bucket matchers below drive success/error.
	mockClient.On("ListObjectVersions", mock.Anything,
		mock.MatchedBy(func(in *s3.ListObjectVersionsInput) bool { return in.Delimiter != nil }),
		mock.Anything).Return(&s3.ListObjectVersionsOutput{IsTruncated: aws.Bool(false)}, nil)

	mockClient.On("ListObjectVersions", mock.Anything, &s3.ListObjectVersionsInput{
		Bucket:          aws.String("bucket1"),
		KeyMarker:       (*string)(nil),
		VersionIdMarker: (*string)(nil),
	}, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(1024), StorageClass: "STANDARD", IsLatest: aws.Bool(true)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	mockClient.On("ListObjectVersions", mock.Anything, &s3.ListObjectVersionsInput{
		Bucket:          aws.String("bucket2"),
		KeyMarker:       (*string)(nil),
		VersionIdMarker: (*string)(nil),
	}, mock.Anything).Return((*s3.ListObjectVersionsOutput)(nil), assert.AnError)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1,bucket2", 25)

	assert.NoError(t, err)
	assert.False(t, summary.EndpointStatus, "EndpointStatus should be false when any bucket fails")
	assert.Equal(t, 1, summary.FailedBucketCount, "FailedBucketCount should be 1")
	assert.Equal(t, 2, summary.BucketCount, "BucketCount should reflect total requested buckets")
	assert.Len(t, summary.S3Buckets, 1, "Only one bucket should be in results")
	assert.Equal(t, "bucket1", summary.S3Buckets[0].BucketName)
}

func TestS3UsageInfo_AllBucketsFail(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).
		Return((*s3.ListObjectVersionsOutput)(nil), assert.AnError)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1,bucket2,bucket3", 25)

	assert.NoError(t, err)
	assert.False(t, summary.EndpointStatus)
	assert.Equal(t, 3, summary.FailedBucketCount)
	assert.Equal(t, 3, summary.BucketCount)
	assert.Empty(t, summary.S3Buckets)
}

func TestS3UsageInfo_MultipleBuckets(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(1024), IsLatest: aws.Bool(true)},
			{Size: aws.Int64(2048), IsLatest: aws.Bool(true)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1,bucket2", 25)

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(6144), summary.StorageClasses["STANDARD"].CurrentSize)
	assert.Equal(t, float64(4), summary.StorageClasses["STANDARD"].CurrentObjectNumber)
	assert.Len(t, summary.S3Buckets, 2)
}

func TestS3UsageInfo_TrailingSpaceInBucketNames(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(1024), IsLatest: aws.Bool(true)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "bucket1, bucket2 ", 25)

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
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(1024), IsLatest: aws.Bool(true)},
			{Size: aws.Int64(2048), IsLatest: aws.Bool(true)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, "", 25)

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(9216), summary.StorageClasses["STANDARD"].CurrentSize)
	assert.Equal(t, float64(6), summary.StorageClasses["STANDARD"].CurrentObjectNumber)
	assert.Len(t, summary.S3Buckets, 3)
}

func TestS3UsageInfo_ConcurrencyIsBounded(t *testing.T) {
	var inFlight, maxObserved int32
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxObserved)
				if cur <= old || atomic.CompareAndSwapInt32(&maxObserved, old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
		}).
		Return(&s3.ListObjectVersionsOutput{
			Versions:    []types.ObjectVersion{{Size: aws.Int64(1), IsLatest: aws.Bool(true)}},
			IsTruncated: aws.Bool(false),
		}, nil)

	names := make([]string, 50)
	for i := range names {
		names[i] = fmt.Sprintf("bucket%d", i)
	}

	const limit = 5
	summary, err := S3UsageInfo(context.Background(), "us-west-2", mockClient, strings.Join(names, ","), limit)

	require.NoError(t, err)
	assert.Len(t, summary.S3Buckets, 50)
	assert.LessOrEqual(t, int(atomic.LoadInt32(&maxObserved)), limit,
		"in-flight ListObjectVersions calls must not exceed the configured limit")
}

func TestCalculateBucketMetrics(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(1024), StorageClass: "STANDARD", IsLatest: aws.Bool(true)},
			{Size: aws.Int64(2048), StorageClass: "STANDARD", IsLatest: aws.Bool(false)},
			{Size: aws.Int64(4096), StorageClass: "GLACIER", IsLatest: aws.Bool(true)},
		},
		DeleteMarkers: []types.DeleteMarkerEntry{
			{IsLatest: aws.Bool(true), Key: aws.String("deleted.txt")},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	storageClasses, deleteMarkers, duration, err := calculateBucketMetrics(context.Background(), "bucket1", mockClient, 4)

	assert.NoError(t, err)
	assert.Equal(t, float64(1024), storageClasses["STANDARD"].CurrentSize)
	assert.Equal(t, float64(1), storageClasses["STANDARD"].CurrentObjectNumber)
	assert.Equal(t, float64(2048), storageClasses["STANDARD"].NoncurrentSize)
	assert.Equal(t, float64(1), storageClasses["STANDARD"].NoncurrentObjectNumber)
	assert.Equal(t, float64(4096), storageClasses["GLACIER"].CurrentSize)
	assert.Equal(t, float64(1), storageClasses["GLACIER"].CurrentObjectNumber)
	assert.Equal(t, float64(0), storageClasses["GLACIER"].NoncurrentSize)
	assert.Equal(t, float64(1), deleteMarkers)
	assert.Greater(t, duration, time.Duration(0))
}

func TestCalculateBucketMetrics_Pagination(t *testing.T) {
	mockClient := new(MockS3Client)

	nextKey := "key2"
	nextVersionID := "v2"

	// Discovery probes with Prefix+Delimiter; return no CommonPrefixes so the
	// bucket lists as a single range and the pagination matchers below apply.
	mockClient.On("ListObjectVersions", mock.Anything, &s3.ListObjectVersionsInput{
		Bucket:    aws.String("bucket1"),
		Prefix:    aws.String(""),
		Delimiter: aws.String("/"),
	}, mock.Anything).Return(&s3.ListObjectVersionsOutput{IsTruncated: aws.Bool(false)}, nil)

	mockClient.On("ListObjectVersions", mock.Anything, &s3.ListObjectVersionsInput{
		Bucket:          aws.String("bucket1"),
		KeyMarker:       (*string)(nil),
		VersionIdMarker: (*string)(nil),
	}, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(1024), StorageClass: "STANDARD", IsLatest: aws.Bool(true)},
		},
		IsTruncated:         aws.Bool(true),
		NextKeyMarker:       &nextKey,
		NextVersionIdMarker: &nextVersionID,
	}, nil)

	mockClient.On("ListObjectVersions", mock.Anything, &s3.ListObjectVersionsInput{
		Bucket:          aws.String("bucket1"),
		KeyMarker:       &nextKey,
		VersionIdMarker: &nextVersionID,
	}, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(2048), StorageClass: "STANDARD", IsLatest: aws.Bool(true)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	storageClasses, deleteMarkers, _, err := calculateBucketMetrics(context.Background(), "bucket1", mockClient, 4)

	assert.NoError(t, err)
	assert.Equal(t, float64(3072), storageClasses["STANDARD"].CurrentSize)
	assert.Equal(t, float64(2), storageClasses["STANDARD"].CurrentObjectNumber)
	assert.Equal(t, float64(0), deleteMarkers)
}

func TestS3UsageInfo_WithIAMRole(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(100), IsLatest: aws.Bool(true)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-east-1", mockClient, "bucket1", 25)

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(100), summary.StorageClasses["STANDARD"].CurrentSize)
	assert.Equal(t, float64(1), summary.StorageClasses["STANDARD"].CurrentObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
}

func TestS3UsageInfo_WithAccessKeys(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectVersionsOutput{
		Versions: []types.ObjectVersion{
			{Size: aws.Int64(100), IsLatest: aws.Bool(true)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-east-1", mockClient, "bucket1", 25)

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(100), summary.StorageClasses["STANDARD"].CurrentSize)
	assert.Equal(t, float64(1), summary.StorageClasses["STANDARD"].CurrentObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
}

func TestContextCancellation(t *testing.T) {
	mockClient := new(MockS3Client)
	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).
		Return(&s3.ListBucketsOutput{Buckets: []types.Bucket{}}, context.Canceled).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	summary, err := S3UsageInfo(ctx, "us-west-2", mockClient, "", 25)

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

	summary, err := S3UsageInfo(ctx, "us-west-2", mockClient, "", 25)

	assert.Error(t, err)
	assert.False(t, summary.EndpointStatus)
}

// contextKey is a custom type for context keys to avoid collisions
type contextKey string

const testContextKey contextKey = "test-key"

func TestContextPropagationThroughChain(t *testing.T) {
	mockClient := new(MockS3Client)

	var capturedCtx context.Context
	mockClient.On("ListObjectVersions", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			capturedCtx = args.Get(0).(context.Context)
		}).
		Return(&s3.ListObjectVersionsOutput{
			Versions:    []types.ObjectVersion{},
			IsTruncated: aws.Bool(false),
		}, nil)

	ctx := context.WithValue(context.Background(), testContextKey, "test-value")

	summary, err := S3UsageInfo(ctx, "us-west-2", mockClient, "test-bucket", 25)

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
