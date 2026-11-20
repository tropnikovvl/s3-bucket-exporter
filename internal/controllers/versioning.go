package controllers

import (
	"context"
	"errors"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
)

// versioningWarned holds the buckets whose detection failure was already logged
// at Warn level; later failures log at Debug so a missing permission does not
// produce a warning per bucket on every scrape.
var versioningWarned sync.Map

// detectListMode picks the listing API for a bucket from its versioning
// status. A bucket that never had versioning enabled (empty status) holds no
// noncurrent versions or delete markers, so it is listed with the cheaper
// ListObjectsV2. Enabled, Suspended (may still hold old versions) and unknown
// statuses keep ListObjectVersions.
//
// Detection failures (AccessDenied, NotImplemented on some S3-compatible
// backends, ...) fall back to ListObjectVersions. Only a cancelled or expired
// context is returned as an error.
func detectListMode(ctx context.Context, client S3ClientInterface, bucket string) (listMode, error) {
	out, err := client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucket)})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return modeVersions, err
		}
		if _, seen := versioningWarned.LoadOrStore(bucket, struct{}{}); seen {
			log.Debugf("Cannot detect versioning for bucket %s, falling back to ListObjectVersions: %v", bucket, err)
		} else {
			log.Warnf("Cannot detect versioning for bucket %s, falling back to ListObjectVersions: %v", bucket, err)
		}
		return modeVersions, nil
	}
	if out.Status == "" {
		return modeObjectsV2, nil
	}
	return modeVersions, nil
}
