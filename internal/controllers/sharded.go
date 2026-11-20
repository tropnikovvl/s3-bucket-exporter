package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	bucketDelimiter  = "/"
	discoverMaxDepth = 8
)

// listMode selects the S3 API used to list a bucket.
type listMode int

const (
	// modeVersions lists every version and delete marker (ListObjectVersions).
	modeVersions listMode = iota
	// modeObjectsV2 lists current objects only (ListObjectsV2); used for
	// buckets that have never had versioning enabled.
	modeObjectsV2
)

func (m listMode) String() string {
	if m == modeObjectsV2 {
		return "ListObjectsV2"
	}
	return "ListObjectVersions"
}

// limitedClient bounds concurrent S3 listing calls to a shared budget,
// implementing the single global concurrency budget (Model A).
type limitedClient struct {
	S3ClientInterface
	sem *semaphore.Weighted
}

func newLimitedClient(c S3ClientInterface, maxConcurrency int) S3ClientInterface {
	return &limitedClient{S3ClientInterface: c, sem: semaphore.NewWeighted(int64(maxConcurrency))}
}

func (l *limitedClient) ListObjectVersions(ctx context.Context, in *s3.ListObjectVersionsInput, optFns ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	if err := l.sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer l.sem.Release(1)
	return l.S3ClientInterface.ListObjectVersions(ctx, in, optFns...)
}

func (l *limitedClient) ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if err := l.sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer l.sem.Release(1)
	return l.S3ClientInterface.ListObjectsV2(ctx, in, optFns...)
}

func (l *limitedClient) GetBucketVersioning(ctx context.Context, in *s3.GetBucketVersioningInput, optFns ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	if err := l.sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer l.sem.Release(1)
	return l.S3ClientInterface.GetBucketVersioning(ctx, in, optFns...)
}

// listRange lists the half-open range (lo, hi] of a bucket to completion with
// the API selected by mode. lo == "" starts at the first key; hi == "" lists to
// the end. It returns per-storage-class metrics and the delete-marker count.
func listRange(ctx context.Context, client S3ClientInterface, bucket, lo, hi string, mode listMode) (map[string]StorageClassMetrics, float64, error) {
	if mode == modeObjectsV2 {
		sc, err := listRangeObjectsV2(ctx, client, bucket, lo, hi)
		return sc, 0, err
	}
	return listRangeVersions(ctx, client, bucket, lo, hi)
}

// rangeCheck places key relative to the half-open range (lo, hi]: stop when
// the key is past hi, skip when it is at or below lo. The lower bound is
// enforced here because AWS treats KeyMarker/StartAfter as exclusive, but some
// S3-compatible backends return the marker key itself, which would
// double-count a boundary key across adjacent ranges.
func rangeCheck(key, lo, hi string) (skip, stop bool) {
	if hi != "" && key > hi {
		return false, true
	}
	return lo != "" && key <= lo, false
}

// addObject counts one object (or object version) of size bytes under its
// storage class; an empty class is reported as STANDARD.
func addObject(storageClasses map[string]StorageClassMetrics, storageClass string, size int64, current bool) {
	if storageClass == "" {
		storageClass = "STANDARD"
	}
	m := storageClasses[storageClass]
	if current {
		m.CurrentSize += float64(size)
		m.CurrentObjectNumber++
	} else {
		m.NoncurrentSize += float64(size)
		m.NoncurrentObjectNumber++
	}
	storageClasses[storageClass] = m
}

// logListError logs a failed listing call: a cancelled or expired scrape is a
// warning, anything else an error.
func logListError(bucket, what string, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		log.Warnf("Listing aborted for bucket %s: scrape deadline exceeded", bucket)
		return
	}
	log.Errorf("Failed to list %s for bucket %s: %v", what, bucket, err)
}

// listRangeVersions lists (lo, hi] via ListObjectVersions. It carries both
// KeyMarker and VersionIdMarker across pages so versions of a key that straddle
// a page boundary are not lost, and stops once a returned key exceeds hi.
func listRangeVersions(ctx context.Context, client S3ClientInterface, bucket, lo, hi string) (map[string]StorageClassMetrics, float64, error) {
	storageClasses := make(map[string]StorageClassMetrics)
	var deleteMarkers float64

	var keyMarker, versionMarker *string
	if lo != "" {
		keyMarker = aws.String(lo)
	}

	for {
		page, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:          aws.String(bucket),
			KeyMarker:       keyMarker,
			VersionIdMarker: versionMarker,
		})
		if err != nil {
			logListError(bucket, "object versions", err)
			return nil, 0, err
		}

		crossed := false
		for _, ver := range page.Versions {
			skip, stop := rangeCheck(aws.ToString(ver.Key), lo, hi)
			if stop {
				crossed = true
				break
			}
			if skip {
				continue
			}
			addObject(storageClasses, string(ver.StorageClass), aws.ToInt64(ver.Size), aws.ToBool(ver.IsLatest))
		}
		for _, dm := range page.DeleteMarkers {
			skip, stop := rangeCheck(aws.ToString(dm.Key), lo, hi)
			if stop {
				crossed = true
				break
			}
			if skip {
				continue
			}
			deleteMarkers++
		}

		if crossed || !aws.ToBool(page.IsTruncated) {
			break
		}
		keyMarker = page.NextKeyMarker
		versionMarker = page.NextVersionIdMarker
	}

	return storageClasses, deleteMarkers, nil
}

// listRangeObjectsV2 lists (lo, hi] via ListObjectsV2. Every object is
// current; there are no delete markers. StartAfter carries the lower bound on
// the first request, ContinuationToken on the rest.
func listRangeObjectsV2(ctx context.Context, client S3ClientInterface, bucket, lo, hi string) (map[string]StorageClassMetrics, error) {
	storageClasses := make(map[string]StorageClassMetrics)

	var startAfter, token *string
	if lo != "" {
		startAfter = aws.String(lo)
	}
	// floor is the exclusive lower bound actually enforced: lo, or the last key
	// already counted after a tokenless resume (StartAfter may be inclusive).
	floor, resumed := lo, false

	for {
		page, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			StartAfter:        startAfter,
			ContinuationToken: token,
		})
		if err != nil {
			logListError(bucket, "objects", err)
			return nil, err
		}

		crossed := false
		lastKey := ""
		for _, obj := range page.Contents {
			lastKey = aws.ToString(obj.Key)
			skip, stop := rangeCheck(lastKey, floor, hi)
			if stop {
				crossed = true
				break
			}
			if skip {
				continue
			}
			addObject(storageClasses, string(obj.StorageClass), aws.ToInt64(obj.Size), true)
		}

		if resumed && lastKey <= floor {
			return nil, fmt.Errorf("bucket %s: ListObjectsV2 made no progress after resuming at %q", bucket, floor)
		}
		resumed = false
		if crossed || !aws.ToBool(page.IsTruncated) {
			break
		}
		if aws.ToString(page.NextContinuationToken) == "" {
			// Some backends omit the token on truncated pages: resume after the
			// last key seen, or fail rather than report a partial count.
			if lastKey == "" {
				return nil, fmt.Errorf("bucket %s: truncated ListObjectsV2 page with no continuation token and no keys", bucket)
			}
			log.Debugf("Bucket %s: truncated ListObjectsV2 page without a continuation token, resuming after %q", bucket, lastKey)
			startAfter, token = aws.String(lastKey), nil
			floor, resumed = lastKey, true
			continue
		}
		startAfter = nil
		token = page.NextContinuationToken
	}

	return storageClasses, nil
}

// listChildPrefixes returns the CommonPrefixes directly under prefix from one
// unpaginated delimiter listing made with the API selected by mode.
func listChildPrefixes(ctx context.Context, client S3ClientInterface, bucket, prefix string, mode listMode) ([]string, error) {
	var cps []types.CommonPrefix
	if mode == modeObjectsV2 {
		page, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:    aws.String(bucket),
			Prefix:    aws.String(prefix),
			Delimiter: aws.String(bucketDelimiter),
		})
		if err != nil {
			return nil, err
		}
		cps = page.CommonPrefixes
	} else {
		page, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:    aws.String(bucket),
			Prefix:    aws.String(prefix),
			Delimiter: aws.String(bucketDelimiter),
		})
		if err != nil {
			return nil, err
		}
		cps = page.CommonPrefixes
	}
	var out []string
	for _, cp := range cps {
		out = append(out, aws.ToString(cp.Prefix))
	}
	return out, nil
}

// discoverBoundaries walks the delimiter "directory" tree level by level,
// listing each level's prefixes in parallel (concurrency bounded by the limited
// client), collecting child prefixes as candidate split points until it has
// `target` of them or runs out (bounded by maxDepth). It counts no objects.
func discoverBoundaries(ctx context.Context, client S3ClientInterface, bucket string, target, maxDepth int, mode listMode) ([]string, error) {
	level := []string{""}
	seen := map[string]bool{}
	var boundaries []string

	for depth := 0; depth < maxDepth && len(level) > 0 && len(boundaries) < target; depth++ {
		childrenOf := make([][]string, len(level))
		g, gctx := errgroup.WithContext(ctx)
		for i, prefix := range level {
			g.Go(func() error {
				// One (unpaginated) page per prefix: cheap, and only the first ~1000
				// child prefixes are sampled. This affects shard balance for very wide
				// levels, never coverage — ranges always tile the keyspace completely.
				ch, err := listChildPrefixes(gctx, client, bucket, prefix, mode)
				if err != nil {
					return err
				}
				childrenOf[i] = ch
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return nil, err
		}

		var next []string
		for _, ch := range childrenOf {
			for _, c := range ch {
				if seen[c] {
					continue
				}
				seen[c] = true
				boundaries = append(boundaries, c)
				next = append(next, c)
			}
		}
		level = next
	}

	sort.Strings(boundaries)
	return boundaries, nil
}

// coalesceBoundaries reduces sorted candidates to at most shards-1 evenly-spaced
// split points. With fewer candidates than shards, returns them all.
func coalesceBoundaries(sorted []string, shards int) []string {
	if shards < 2 || len(sorted) == 0 {
		return nil
	}
	if len(sorted) < shards {
		return sorted
	}
	out := make([]string, 0, shards-1)
	for i := 1; i < shards; i++ {
		out = append(out, sorted[len(sorted)*i/shards])
	}
	return out
}
