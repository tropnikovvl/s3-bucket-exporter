package controllers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type StorageClassMetrics struct {
	Size         float64
	ObjectNumber float64
}

type Bucket struct {
	BucketName     string
	StorageClasses map[string]StorageClassMetrics
	ListDuration   time.Duration
}

type S3Summary struct {
	EndpointStatus    bool
	StorageClasses    map[string]StorageClassMetrics
	S3Buckets         []Bucket
	BucketCount       int
	TotalListDuration time.Duration
}

type S3Collector struct {
	metrics      S3Summary
	metricsMutex sync.RWMutex
	err          error
	s3Endpoint   string
	s3Region     string
}

type S3ClientInterface interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

var metricsDesc = map[string]*prometheus.Desc{
	"up":              prometheus.NewDesc("s3_endpoint_up", "Connection to S3 successful", []string{"s3Endpoint", "s3Region"}, nil),
	"total_size":      prometheus.NewDesc("s3_total_size", "S3 Total Bucket Size", []string{"s3Endpoint", "s3Region", "storageClass"}, nil),
	"total_objects":   prometheus.NewDesc("s3_total_object_number", "S3 Total Object Number", []string{"s3Endpoint", "s3Region", "storageClass"}, nil),
	"bucket_count":    prometheus.NewDesc("s3_bucket_count", "S3 Total Number of Buckets", []string{"s3Endpoint", "s3Region"}, nil),
	"total_duration":  prometheus.NewDesc("s3_list_total_duration_seconds", "Total time spent listing objects across all buckets", []string{"s3Endpoint", "s3Region"}, nil),
	"bucket_size":     prometheus.NewDesc("s3_bucket_size", "S3 Bucket Size", []string{"s3Endpoint", "s3Region", "bucketName", "storageClass"}, nil),
	"bucket_objects":  prometheus.NewDesc("s3_bucket_object_number", "S3 Bucket Object Number", []string{"s3Endpoint", "s3Region", "bucketName", "storageClass"}, nil),
	"bucket_duration": prometheus.NewDesc("s3_list_duration_seconds", "Time spent listing objects in bucket", []string{"s3Endpoint", "s3Region", "bucketName"}, nil),
}

// NewS3Collector creates a new S3Collector
func NewS3Collector(s3Endpoint, s3Region string) *S3Collector {
	return &S3Collector{
		s3Endpoint: s3Endpoint,
		s3Region:   s3Region,
	}
}

// Describe implements prometheus.Collector
func (c *S3Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range metricsDesc {
		ch <- desc
	}
}

// Collect implements prometheus.Collector
func (c *S3Collector) Collect(ch chan<- prometheus.Metric) {
	metrics, err := c.GetMetrics()

	status := 0
	if metrics.EndpointStatus {
		status = 1
	}

	ch <- prometheus.MustNewConstMetric(metricsDesc["up"], prometheus.GaugeValue, float64(status), c.s3Endpoint, c.s3Region)

	if err != nil {
		log.Errorf("Cached error: %v", err)
		return
	}

	log.Debugf("Cached S3 metrics %s: %+v", c.s3Endpoint, metrics)

	for class, s3Metrics := range metrics.StorageClasses {
		ch <- prometheus.MustNewConstMetric(metricsDesc["total_size"], prometheus.GaugeValue, s3Metrics.Size, c.s3Endpoint, c.s3Region, class)
		ch <- prometheus.MustNewConstMetric(metricsDesc["total_objects"], prometheus.GaugeValue, s3Metrics.ObjectNumber, c.s3Endpoint, c.s3Region, class)
	}
	ch <- prometheus.MustNewConstMetric(metricsDesc["bucket_count"], prometheus.GaugeValue, float64(metrics.BucketCount), c.s3Endpoint, c.s3Region)
	ch <- prometheus.MustNewConstMetric(metricsDesc["total_duration"], prometheus.GaugeValue, float64(metrics.TotalListDuration.Seconds()), c.s3Endpoint, c.s3Region)

	for _, bucket := range metrics.S3Buckets {
		for class, s3Metrics := range bucket.StorageClasses {
			ch <- prometheus.MustNewConstMetric(metricsDesc["bucket_size"], prometheus.GaugeValue, s3Metrics.Size, c.s3Endpoint, c.s3Region, bucket.BucketName, class)
			ch <- prometheus.MustNewConstMetric(metricsDesc["bucket_objects"], prometheus.GaugeValue, s3Metrics.ObjectNumber, c.s3Endpoint, c.s3Region, bucket.BucketName, class)
		}
		ch <- prometheus.MustNewConstMetric(metricsDesc["bucket_duration"], prometheus.GaugeValue, float64(bucket.ListDuration.Seconds()), c.s3Endpoint, c.s3Region, bucket.BucketName)
	}
}

// UpdateMetrics updates the cached metrics
func (c *S3Collector) UpdateMetrics(ctx context.Context, s3Client S3ClientInterface, s3Region, s3BucketNames string) {
	metrics, err := S3UsageInfo(ctx, s3Region, s3Client, s3BucketNames)

	c.metricsMutex.Lock()
	c.metrics = metrics
	c.err = err
	c.metricsMutex.Unlock()
}

// GetMetrics safely reads the cached metrics and error
func (c *S3Collector) GetMetrics() (S3Summary, error) {
	c.metricsMutex.RLock()
	defer c.metricsMutex.RUnlock()
	return c.metrics, c.err
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
