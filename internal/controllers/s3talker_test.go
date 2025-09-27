package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
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
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		Region:    "us-west-2",
		Endpoint:  "test-endpoint",
		AWSConfig: &aws.Config{},
	}

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024), StorageClass: "STANDARD"},
			{Size: aws.Int64(2048), StorageClass: "STANDARD"},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "bucket1")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(3072), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(2), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
}

func TestS3UsageInfo_MultipleBuckets(t *testing.T) {
	mockClient := new(MockS3Client)
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		Region:    "us-west-2",
		Endpoint:  "test-endpoint",
		AWSConfig: &aws.Config{},
	}

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
			{Size: aws.Int64(2048)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "bucket1,bucket2")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(6144), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(4), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 2)
}

func TestS3UsageInfo_EmptyBucketList(t *testing.T) {
	mockClient := new(MockS3Client)
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		Region:    "us-west-2",
		Endpoint:  "test-endpoint",
		AWSConfig: &aws.Config{},
	}

	mockBucket1 := types.Bucket{Name: aws.String("bucket1")}
	mockBucket2 := types.Bucket{Name: aws.String("bucket2")}
	mockBucket3 := types.Bucket{Name: aws.String("bucket3")}

	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListBucketsOutput{
		Buckets: []types.Bucket{mockBucket1, mockBucket2, mockBucket3},
	}, nil)

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(1024)},
			{Size: aws.Int64(2048)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "")

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

	storageClasses, duration, err := calculateBucketMetrics("bucket1", mockClient)

	assert.NoError(t, err)
	assert.Equal(t, float64(3072), storageClasses["STANDARD"].Size)
	assert.Equal(t, float64(2), storageClasses["STANDARD"].ObjectNumber)
	assert.Equal(t, float64(4096), storageClasses["GLACIER"].Size)
	assert.Equal(t, float64(1), storageClasses["GLACIER"].ObjectNumber)
	assert.Greater(t, duration, time.Duration(0))
}

func TestS3UsageInfo_WithIAMRole(t *testing.T) {
	mockClient := new(MockS3Client)
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		Region:   "us-east-1",
		Endpoint: "s3.amazonaws.com",
		AWSConfig: &aws.Config{
			Region:      "us-east-1",
			Credentials: nil, // IAM role credentials should be automatically resolved
		},
	}

	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListBucketsOutput{
		Buckets: []types.Bucket{
			{Name: aws.String("bucket1")},
		},
	}, nil)

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(100)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "bucket1")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(100), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(1), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
	assert.Equal(t, "us-east-1", s3Conn.AWSConfig.Region)
	assert.Nil(t, s3Conn.AWSConfig.Credentials)
}

func TestS3UsageInfo_WithAccessKeys(t *testing.T) {
	mockClient := new(MockS3Client)
	SetS3Client(mockClient)
	defer ResetS3Client()

	s3Conn := S3Conn{
		Region:   "us-east-1",
		Endpoint: "s3.amazonaws.com",
		AWSConfig: &aws.Config{
			Region: "us-east-1",
			Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
				"test-access-key",
				"test-secret-key",
				"",
			)),
		},
	}

	mockClient.On("ListBuckets", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListBucketsOutput{
		Buckets: []types.Bucket{
			{Name: aws.String("bucket1")},
		},
	}, nil)

	mockClient.On("ListObjectsV2", mock.Anything, mock.Anything, mock.Anything).Return(&s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Size: aws.Int64(100)},
		},
		IsTruncated: aws.Bool(false),
	}, nil)

	summary, err := S3UsageInfo(s3Conn, "bucket1")

	assert.NoError(t, err)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, float64(100), summary.StorageClasses["STANDARD"].Size)
	assert.Equal(t, float64(1), summary.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, summary.S3Buckets, 1)
	assert.Equal(t, "us-east-1", s3Conn.AWSConfig.Region)

	creds, err := s3Conn.AWSConfig.Credentials.Retrieve(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "test-access-key", creds.AccessKeyID)
	assert.Equal(t, "test-secret-key", creds.SecretAccessKey)
}

func TestS3Collector(t *testing.T) {
	s3Endpoint := "http://localhost"
	s3Region := "us-east-1"

	collector := NewS3Collector(s3Endpoint, s3Region)
	collector.metricsMutex.Lock()
	collector.Metrics = S3Summary{
		EndpointStatus: true,
		StorageClasses: map[string]StorageClassMetrics{
			"STANDARD": {
				Size:         1024.0,
				ObjectNumber: 1.0,
			},
		},
		TotalListDuration: 2 * time.Second,
		S3Buckets: []Bucket{
			{
				BucketName: "test-bucket",
				StorageClasses: map[string]StorageClassMetrics{
					"STANDARD": {
						Size:         1024.0,
						ObjectNumber: 1.0,
					},
				},
				ListDuration: 1 * time.Second,
			},
		},
	}
	collector.metricsMutex.Unlock()

	ch := make(chan prometheus.Metric)
	done := make(chan bool)

	var metrics []prometheus.Metric

	go func() {
		expectedExact := []struct {
			name   string
			labels map[string]string
			value  float64
		}{
			{"s3_endpoint_up", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}, 1.0},
			{"s3_total_size", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "storageClass": "STANDARD"}, 1024.0},
			{"s3_total_object_number", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "storageClass": "STANDARD"}, 1.0},
			{"s3_bucket_size", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "bucketName": "test-bucket", "storageClass": "STANDARD"}, 1024.0},
			{"s3_bucket_object_number", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "bucketName": "test-bucket", "storageClass": "STANDARD"}, 1.0},
		}

		expectedDuration := []struct {
			name   string
			labels map[string]string
		}{
			{"s3_list_total_duration_seconds", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}},
			{"s3_list_duration_seconds", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "bucketName": "test-bucket"}},
		}

		var matchedExactCount int
		var matchedDurationCount int

		for metric := range ch {
			metrics = append(metrics, metric)

			dtoMetric := &io_prometheus_client.Metric{}
			err := metric.Write(dtoMetric)
			require.NoError(t, err)

			for _, exp := range expectedExact {
				if matchMetricExact(exp, metric, dtoMetric) {
					matchedExactCount++
					break
				}
			}

			for _, exp := range expectedDuration {
				if matchMetricDuration(exp, metric, dtoMetric) {
					matchedDurationCount++
					break
				}
			}
		}

		assert.Equal(t, len(expectedExact), matchedExactCount, "Not all expected exact metrics were found")
		assert.Equal(t, len(expectedDuration), matchedDurationCount, "Not all expected duration metrics were found")
		assert.Equal(t, len(expectedExact)+len(expectedDuration), len(metrics), "Mismatch in number of metrics")
		done <- true
	}()

	collector.Collect(ch)
	close(ch)
	<-done
}

func matchMetricExact(exp struct {
	name   string
	labels map[string]string
	value  float64
}, metric prometheus.Metric, dtoMetric *io_prometheus_client.Metric) bool {
	if !strings.Contains(metric.Desc().String(), exp.name) {
		return false
	}

	for _, label := range dtoMetric.GetLabel() {
		if val, ok := exp.labels[label.GetName()]; !ok || val != label.GetValue() {
			return false
		}
	}

	if dtoMetric.GetGauge() != nil {
		return dtoMetric.GetGauge().GetValue() == exp.value
	}
	return false
}

func matchMetricDuration(exp struct {
	name   string
	labels map[string]string
}, metric prometheus.Metric, dtoMetric *io_prometheus_client.Metric) bool {
	if !strings.Contains(metric.Desc().String(), exp.name) {
		return false
	}

	for _, label := range dtoMetric.GetLabel() {
		if val, ok := exp.labels[label.GetName()]; !ok || val != label.GetValue() {
			return false
		}
	}

	return *dtoMetric.Gauge.Value > 0
}
