package controllers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

type S3ClientInterface interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	ListObjectVersions(ctx context.Context, params *s3.ListObjectVersionsInput, optFns ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
}

// distinct removes duplicates and blank entries from a slice of strings
func distinct(input []string) []string {
	seen := make(map[string]struct{})
	var result []string

	for _, val := range input {
		val = strings.TrimSpace(val)
		if val != "" {
			if _, exists := seen[val]; !exists {
				seen[val] = struct{}{}
				result = append(result, val)
			}
		}
	}

	return result
}

// S3UsageInfo gets S3 usage information and returns S3Summary
func S3UsageInfo(ctx context.Context, s3Region string, s3Client S3ClientInterface, s3BucketNames string, maxConcurrency int) (S3Summary, error) {
	summary := S3Summary{
		StorageClasses: make(map[string]StorageClassMetrics),
	}

	var bucketNames []string
	start := time.Now()

	if s3BucketNames != "" {
		bucketNames = distinct(strings.Split(s3BucketNames, ","))
	} else {
		result, err := s3Client.ListBuckets(ctx, &s3.ListBucketsInput{BucketRegion: aws.String(s3Region)})
		if err != nil {
			log.Errorf("Failed to list buckets: %v", err)
			return summary, errors.New("unable to connect to S3 endpoint")
		}

		for _, b := range result.Buckets {
			bucketNames = append(bucketNames, aws.ToString(b.Name))
		}
	}

	log.Debugf("List of buckets in %s region: %v", s3Region, bucketNames)
	summary.S3Buckets = make([]Bucket, 0, len(bucketNames))

	type bucketResult struct {
		bucket Bucket
		err    error
	}
	results := make([]bucketResult, len(bucketNames))

	g := new(errgroup.Group)
	g.SetLimit(maxConcurrency)
	for i, bucketName := range bucketNames {
		g.Go(func() error {
			storageClasses, deleteMarkers, duration, err := calculateBucketMetrics(ctx, bucketName, s3Client)
			if err != nil {
				results[i] = bucketResult{err: err}
				return nil // collect failures; never cancel sibling buckets
			}
			results[i] = bucketResult{bucket: Bucket{
				BucketName:     bucketName,
				StorageClasses: storageClasses,
				DeleteMarkers:  deleteMarkers,
				ListDuration:   duration,
			}}
			log.Debugf("Finish bucket %s processing", bucketName)
			return nil
		})
	}
	_ = g.Wait() // errors are aggregated via results, not returned

	var errs []error
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		bucket := r.bucket
		summary.S3Buckets = append(summary.S3Buckets, bucket)
		summary.DeleteMarkers += bucket.DeleteMarkers
		for storageClass, metrics := range bucket.StorageClasses {
			summaryMetrics := summary.StorageClasses[storageClass]
			summaryMetrics.CurrentSize += metrics.CurrentSize
			summaryMetrics.CurrentObjectNumber += metrics.CurrentObjectNumber
			summaryMetrics.NoncurrentSize += metrics.NoncurrentSize
			summaryMetrics.NoncurrentObjectNumber += metrics.NoncurrentObjectNumber
			summary.StorageClasses[storageClass] = summaryMetrics
		}
		log.Debugf("Bucket size and objects count: %v", bucket)
	}

	summary.FailedBucketCount = len(errs)
	if len(errs) > 0 {
		log.Errorf("Encountered errors while processing %d of %d buckets", len(errs), len(bucketNames))
	}

	summary.EndpointStatus = len(errs) == 0 && len(summary.S3Buckets) > 0
	summary.BucketCount = len(bucketNames)
	summary.TotalListDuration = time.Since(start)
	return summary, nil
}

// calculateBucketMetrics computes the total size, object count, and delete markers for a bucket
func calculateBucketMetrics(ctx context.Context, bucketName string, s3Client S3ClientInterface) (map[string]StorageClassMetrics, float64, time.Duration, error) {
	storageClasses := make(map[string]StorageClassMetrics)
	var deleteMarkers float64
	var keyMarker *string
	var versionIDMarker *string

	start := time.Now()

	for {
		page, err := s3Client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket:          aws.String(bucketName),
			KeyMarker:       keyMarker,
			VersionIdMarker: versionIDMarker,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Warnf("Listing aborted for bucket %s: scrape deadline exceeded", bucketName)
			} else {
				log.Errorf("Failed to list object versions for bucket %s: %v", bucketName, err)
			}
			return nil, 0, 0, err
		}

		for _, ver := range page.Versions {
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

		deleteMarkers += float64(len(page.DeleteMarkers))

		if page.IsTruncated == nil || !*page.IsTruncated {
			break
		}
		keyMarker = page.NextKeyMarker
		versionIDMarker = page.NextVersionIdMarker
	}

	return storageClasses, deleteMarkers, time.Since(start), nil
}
