package controllers

import (
	"context"
	"sync"
	"time"

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
	ch <- prometheus.MustNewConstMetric(metricsDesc["total_duration"], prometheus.GaugeValue, metrics.TotalListDuration.Seconds(), c.s3Endpoint, c.s3Region)

	for _, bucket := range metrics.S3Buckets {
		for class, s3Metrics := range bucket.StorageClasses {
			ch <- prometheus.MustNewConstMetric(metricsDesc["bucket_size"], prometheus.GaugeValue, s3Metrics.Size, c.s3Endpoint, c.s3Region, bucket.BucketName, class)
			ch <- prometheus.MustNewConstMetric(metricsDesc["bucket_objects"], prometheus.GaugeValue, s3Metrics.ObjectNumber, c.s3Endpoint, c.s3Region, bucket.BucketName, class)
		}
		ch <- prometheus.MustNewConstMetric(metricsDesc["bucket_duration"], prometheus.GaugeValue, bucket.ListDuration.Seconds(), c.s3Endpoint, c.s3Region, bucket.BucketName)
	}
}

// UpdateMetrics updates the cached metrics
func (c *S3Collector) UpdateMetrics(ctx context.Context, client S3ClientInterface, s3Region, s3BucketNames string) {
	metrics, err := S3UsageInfo(ctx, s3Region, client, s3BucketNames)

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
