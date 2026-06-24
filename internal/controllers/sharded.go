package controllers

import (
	"context"
	"errors"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	bucketDelimiter  = "/"
	discoverMaxDepth = 8
)

// limitedClient bounds concurrent ListObjectVersions calls to a shared budget,
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

// listRange lists the half-open range (lo, hi] of a bucket to completion. lo ==
// "" starts at the first key; hi == "" lists to the end. It carries both
// KeyMarker and VersionIdMarker across pages so versions of a key that straddle
// a page boundary are not lost, and stops once a returned key exceeds hi.
func listRange(ctx context.Context, client S3ClientInterface, bucket, lo, hi string) (map[string]StorageClassMetrics, float64, error) {
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
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Warnf("Listing aborted for bucket %s: scrape deadline exceeded", bucket)
			} else {
				log.Errorf("Failed to list object versions for bucket %s: %v", bucket, err)
			}
			return nil, 0, err
		}

		crossed := false
		for _, ver := range page.Versions {
			if hi != "" && aws.ToString(ver.Key) > hi {
				crossed = true
				break
			}
			storageClass := string(ver.StorageClass)
			if storageClass == "" {
				storageClass = "STANDARD"
			}
			size := float64(aws.ToInt64(ver.Size))
			metrics := storageClasses[storageClass]
			if aws.ToBool(ver.IsLatest) {
				metrics.CurrentSize += size
				metrics.CurrentObjectNumber++
			} else {
				metrics.NoncurrentSize += size
				metrics.NoncurrentObjectNumber++
			}
			storageClasses[storageClass] = metrics
		}
		for _, dm := range page.DeleteMarkers {
			if hi != "" && aws.ToString(dm.Key) > hi {
				crossed = true
				break
			}
			deleteMarkers++
		}

		if crossed || page.IsTruncated == nil || !*page.IsTruncated {
			break
		}
		keyMarker = page.NextKeyMarker
		versionMarker = page.NextVersionIdMarker
	}

	return storageClasses, deleteMarkers, nil
}

// discoverBoundaries walks the delimiter "directory" tree level by level,
// listing each level's prefixes in parallel (concurrency bounded by the limited
// client), collecting child prefixes as candidate split points until it has
// `target` of them or runs out (bounded by maxDepth). It counts no objects.
func discoverBoundaries(ctx context.Context, client S3ClientInterface, bucket string, target, maxDepth int) ([]string, error) {
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
				page, err := client.ListObjectVersions(gctx, &s3.ListObjectVersionsInput{
					Bucket:    aws.String(bucket),
					Prefix:    aws.String(prefix),
					Delimiter: aws.String(bucketDelimiter),
				})
				if err != nil {
					return err
				}
				var ch []string
				for _, cp := range page.CommonPrefixes {
					ch = append(ch, aws.ToString(cp.Prefix))
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
