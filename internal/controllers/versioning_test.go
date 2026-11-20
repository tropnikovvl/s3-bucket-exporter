package controllers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDetectListMode(t *testing.T) {
	tests := []struct {
		name   string
		status types.BucketVersioningStatus
		err    error
		want   listMode
	}{
		{name: "never enabled", status: "", want: modeObjectsV2},
		{name: "enabled", status: types.BucketVersioningStatusEnabled, want: modeVersions},
		{name: "suspended", status: types.BucketVersioningStatusSuspended, want: modeVersions},
		{name: "unknown status", status: "Weird", want: modeVersions},
		{name: "access denied", err: errors.New("api error AccessDenied: Access Denied"), want: modeVersions},
		{name: "not implemented", err: errors.New("api error NotImplemented"), want: modeVersions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(MockS3Client)
			var out *s3.GetBucketVersioningOutput
			if tt.err == nil {
				out = &s3.GetBucketVersioningOutput{Status: tt.status}
			}
			m.On("GetBucketVersioning", mock.Anything, &s3.GetBucketVersioningInput{Bucket: aws.String("b")}, mock.Anything).Return(out, tt.err)

			got, err := detectListMode(context.Background(), m, "b")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectListMode_CancelledContextReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := new(MockS3Client)
	m.On("GetBucketVersioning", mock.Anything, mock.Anything, mock.Anything).
		Return((*s3.GetBucketVersioningOutput)(nil), context.Canceled)

	_, err := detectListMode(ctx, m, "b")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestS3UsageInfo_UnversionedBucketUsesObjectsV2(t *testing.T) {
	client := &fakeS3{objs: unversionedData(), pageSize: 1, unversioned: true}
	summary, err := S3UsageInfo(context.Background(), "us-east-1", client, "bucket", 4)
	require.NoError(t, err)

	std := summary.StorageClasses["STANDARD"]
	assert.Equal(t, float64(4), std.CurrentObjectNumber)
	assert.Equal(t, float64(1+10+5+2), std.CurrentSize)
	assert.Equal(t, float64(0), summary.DeleteMarkers)
	assert.Equal(t, int32(0), atomic.LoadInt32(&client.calls), "ListObjectVersions must not be used")
	assert.Greater(t, atomic.LoadInt32(&client.v2Calls), int32(0))
	assert.True(t, summary.EndpointStatus)
}

func TestS3UsageInfo_VersioningErrorFallsBack(t *testing.T) {
	client := &fakeS3{objs: hierData(), pageSize: 1, versioningErr: errors.New("api error NotImplemented")}
	summary, err := S3UsageInfo(context.Background(), "us-east-1", client, "bucket", 4)
	require.NoError(t, err)

	std := summary.StorageClasses["STANDARD"]
	assert.Equal(t, float64(5), std.CurrentObjectNumber)
	assert.Equal(t, float64(1+10+10+10+5), std.CurrentSize)
	assert.Equal(t, float64(1), summary.DeleteMarkers)
	assert.Equal(t, 0, summary.FailedBucketCount)
	assert.Equal(t, int32(0), atomic.LoadInt32(&client.v2Calls))
}

func TestS3UsageInfo_VersioningEnabledBetweenScrapes(t *testing.T) {
	client := &fakeS3{objs: unversionedData(), pageSize: 2, unversioned: true}
	first, err := S3UsageInfo(context.Background(), "us-east-1", client, "bucket", 4)
	require.NoError(t, err)
	assert.Equal(t, float64(4), first.StorageClasses["STANDARD"].CurrentObjectNumber)
	assert.Equal(t, int32(0), atomic.LoadInt32(&client.calls))

	// Versioning gets enabled: "k" is overwritten (old version becomes
	// noncurrent) and "q" is deleted (delete marker).
	client.unversioned = false
	client.objs = []fakeObj{
		{key: "a", version: "null", size: 1},
		{key: "k", version: "v2", size: 20},
		{key: "k", version: "null", size: 10, noncurrent: true},
		{key: "m", version: "null", size: 5},
		{key: "q", version: "d1", isDelete: true},
		{key: "z", version: "null", size: 2},
	}
	second, err := S3UsageInfo(context.Background(), "us-east-1", client, "bucket", 4)
	require.NoError(t, err)

	std := second.StorageClasses["STANDARD"]
	assert.Equal(t, float64(4), std.CurrentObjectNumber)
	assert.Equal(t, float64(1), std.NoncurrentObjectNumber)
	assert.Equal(t, float64(10), std.NoncurrentSize)
	assert.Equal(t, float64(1), second.DeleteMarkers)
	assert.Greater(t, atomic.LoadInt32(&client.calls), int32(0), "second scrape must use ListObjectVersions")
}

func TestS3UsageInfo_MixedVersioningBuckets(t *testing.T) {
	forBucket := func(name string) func(any) bool {
		return func(in any) bool {
			switch v := in.(type) {
			case *s3.GetBucketVersioningInput:
				return aws.ToString(v.Bucket) == name
			case *s3.ListObjectsV2Input:
				return aws.ToString(v.Bucket) == name
			case *s3.ListObjectVersionsInput:
				return aws.ToString(v.Bucket) == name
			}
			return false
		}
	}
	m := new(MockS3Client)
	m.On("GetBucketVersioning", mock.Anything, mock.MatchedBy(forBucket("plain")), mock.Anything).
		Return(&s3.GetBucketVersioningOutput{}, nil)
	m.On("GetBucketVersioning", mock.Anything, mock.MatchedBy(forBucket("versioned")), mock.Anything).
		Return(&s3.GetBucketVersioningOutput{Status: types.BucketVersioningStatusEnabled}, nil)
	m.On("ListObjectsV2", mock.Anything, mock.MatchedBy(forBucket("plain")), mock.Anything).
		Return(&s3.ListObjectsV2Output{
			Contents: []types.Object{
				{Key: aws.String("p1"), Size: aws.Int64(100)},
				{Key: aws.String("p2"), Size: aws.Int64(200)},
			},
			IsTruncated: aws.Bool(false),
		}, nil)
	m.On("ListObjectVersions", mock.Anything, mock.MatchedBy(forBucket("versioned")), mock.Anything).
		Return(&s3.ListObjectVersionsOutput{
			Versions: []types.ObjectVersion{
				{Key: aws.String("v1"), Size: aws.Int64(100), IsLatest: aws.Bool(true)},
				{Key: aws.String("v1"), Size: aws.Int64(50), IsLatest: aws.Bool(false)},
			},
			DeleteMarkers: []types.DeleteMarkerEntry{{Key: aws.String("v2"), IsLatest: aws.Bool(true)}},
			IsTruncated:   aws.Bool(false),
		}, nil)

	summary, err := S3UsageInfo(context.Background(), "us-east-1", m, "plain,versioned", 4)
	require.NoError(t, err)

	std := summary.StorageClasses["STANDARD"]
	assert.Equal(t, float64(3), std.CurrentObjectNumber)
	assert.Equal(t, float64(400), std.CurrentSize)
	assert.Equal(t, float64(1), std.NoncurrentObjectNumber)
	assert.Equal(t, float64(50), std.NoncurrentSize)
	assert.Equal(t, float64(1), summary.DeleteMarkers)
	assert.Equal(t, 0, summary.FailedBucketCount)
}

func TestS3UsageInfo_EmptyUnversionedBucket(t *testing.T) {
	client := &fakeS3{pageSize: 10, unversioned: true}
	summary, err := S3UsageInfo(context.Background(), "us-east-1", client, "bucket", 4)
	require.NoError(t, err)

	require.Len(t, summary.S3Buckets, 1)
	assert.Empty(t, summary.StorageClasses)
	assert.Equal(t, float64(0), summary.DeleteMarkers)
	assert.True(t, summary.EndpointStatus)
	assert.Equal(t, 0, summary.FailedBucketCount)
}

// A deployment without s3:GetBucketVersioning must not log a warning for every
// bucket on every scrape: warn once per bucket, then stay quiet.
func TestDetectListMode_WarnsOncePerBucket(t *testing.T) {
	const bucket = "warn-once-bucket"
	// versioningWarned is process-wide: start clean and leave it clean so the
	// test also passes under -count=N.
	versioningWarned.Delete(bucket)
	t.Cleanup(func() { versioningWarned.Delete(bucket) })

	hook := logtest.NewGlobal()
	defer hook.Reset()

	m := new(MockS3Client)
	m.On("GetBucketVersioning", mock.Anything, mock.Anything, mock.Anything).
		Return((*s3.GetBucketVersioningOutput)(nil), errors.New("api error AccessDenied: Access Denied"))

	for i := 0; i < 3; i++ {
		mode, err := detectListMode(context.Background(), m, bucket)
		require.NoError(t, err)
		assert.Equal(t, modeVersions, mode)
	}

	warns := 0
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel {
			warns++
		}
	}
	assert.Equal(t, 1, warns)
}
