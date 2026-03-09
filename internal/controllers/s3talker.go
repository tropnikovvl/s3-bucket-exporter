package controllers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	log "github.com/sirupsen/logrus"
)

type S3ClientInterface interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// distinct removes duplicates and blank entries from a slice of strings
func distinct(input []string) []string {
	seen := make(map[string]struct{})
	result := []string{}

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
func S3UsageInfo(ctx context.Context, s3Region string, s3Client S3ClientInterface, s3BucketNames string) (S3Summary, error) {
	summary := S3Summary{
		StorageClasses: make(map[string]StorageClassMetrics),
		S3Buckets:      make([]Bucket, 0),
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

	var (
		wg           sync.WaitGroup
		summaryMutex sync.Mutex
		errs         []error
		errMutex     sync.Mutex
	)

	processBucketResult := func(bucket Bucket) {
		summaryMutex.Lock()
		defer summaryMutex.Unlock()

		summary.S3Buckets = append(summary.S3Buckets, bucket)
		for storageClass, metrics := range bucket.StorageClasses {
			summaryMetrics := summary.StorageClasses[storageClass]
			summaryMetrics.Size += metrics.Size
			summaryMetrics.ObjectNumber += metrics.ObjectNumber
			summary.StorageClasses[storageClass] = summaryMetrics
		}
		log.Debugf("Bucket size and objects count: %v", bucket)
	}

	for _, bucketName := range bucketNames {
		bucketName := strings.TrimSpace(bucketName)
		if bucketName == "" {
			continue
		}

		wg.Add(1)
		go func(bucketName string) {
			defer wg.Done()

			storageClasses, duration, err := calculateBucketMetrics(ctx, bucketName, s3Client)
			if err != nil {
				errMutex.Lock()
				errs = append(errs, err)
				errMutex.Unlock()
				return
			}

			processBucketResult(Bucket{
				BucketName:     bucketName,
				StorageClasses: storageClasses,
				ListDuration:   duration,
			})
			log.Debugf("Finish bucket %s processing", bucketName)
		}(bucketName)
	}

	wg.Wait()

	if len(errs) > 0 {
		log.Errorf("Encountered errors while processing buckets: %v", errs)
	}

	if len(summary.S3Buckets) > 0 {
		summary.EndpointStatus = true
	}

	summary.BucketCount = len(bucketNames)
	summary.TotalListDuration = time.Since(start)
	return summary, nil
}

// calculateBucketMetrics computes the total size and object count for a bucket
func calculateBucketMetrics(ctx context.Context, bucketName string, s3Client S3ClientInterface) (map[string]StorageClassMetrics, time.Duration, error) {
	var continuationToken *string
	storageClasses := make(map[string]StorageClassMetrics)

	start := time.Now()

	for {
		page, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucketName),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			log.Errorf("Failed to list objects for bucket %s: %v", bucketName, err)
			return nil, 0, err
		}

		for _, obj := range page.Contents {
			storageClass := string(obj.StorageClass)
			if storageClass == "" {
				storageClass = "STANDARD"
			}

			metrics := storageClasses[storageClass]
			metrics.Size += float64(*obj.Size)
			metrics.ObjectNumber++
			storageClasses[storageClass] = metrics
		}

		if page.IsTruncated != nil && !*page.IsTruncated {
			break
		}
		continuationToken = page.NextContinuationToken
	}

	return storageClasses, time.Since(start), nil
}
