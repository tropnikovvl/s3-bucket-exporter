package controllers

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestS3Collector(t *testing.T) {
	s3Endpoint := "http://localhost"
	s3Region := "us-east-1"

	collector := NewS3Collector(s3Endpoint, s3Region)
	collector.metricsMutex.Lock()
	collector.metrics = S3Summary{
		EndpointStatus:    true,
		FailedBucketCount: 0,
		StorageClasses: map[string]StorageClassMetrics{
			"STANDARD": {Size: 1024.0, ObjectNumber: 1.0},
		},
		BucketCount:       1,
		TotalListDuration: 2 * time.Second,
		S3Buckets: []Bucket{
			{
				BucketName:     "test-bucket",
				StorageClasses: map[string]StorageClassMetrics{"STANDARD": {Size: 1024.0, ObjectNumber: 1.0}},
				ListDuration:   1 * time.Second,
			},
		},
	}
	collector.metricsMutex.Unlock()

	ch := make(chan prometheus.Metric)
	done := make(chan bool)

	var collectedMetrics []prometheus.Metric

	go func() {
		expectedExact := []struct {
			name   string
			labels map[string]string
			value  float64
		}{
			{"s3_endpoint_up", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}, 1.0},
			{"s3_total_size", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "storageClass": "STANDARD"}, 1024.0},
			{"s3_total_object_number", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region, "storageClass": "STANDARD"}, 1.0},
			{"s3_bucket_count", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}, 1.0},
			{"s3_failed_bucket_count", map[string]string{"s3Endpoint": s3Endpoint, "s3Region": s3Region}, 0.0},
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

		var matchedExactCount, matchedDurationCount int

		for metric := range ch {
			collectedMetrics = append(collectedMetrics, metric)

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
		assert.Equal(t, len(expectedExact)+len(expectedDuration), len(collectedMetrics), "Mismatch in number of metrics")
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

func TestGetMetrics(t *testing.T) {
	collector := NewS3Collector("http://localhost", "us-east-1")

	m, err := collector.GetMetrics()
	assert.NoError(t, err)
	assert.False(t, m.EndpointStatus)
	assert.Equal(t, 0, m.BucketCount)

	testMetrics := S3Summary{
		EndpointStatus:    true,
		FailedBucketCount: 0,
		StorageClasses: map[string]StorageClassMetrics{
			"STANDARD": {Size: 2048.0, ObjectNumber: 5.0},
		},
		BucketCount:       2,
		TotalListDuration: 3 * time.Second,
		S3Buckets: []Bucket{
			{BucketName: "test-bucket-1", StorageClasses: map[string]StorageClassMetrics{"STANDARD": {Size: 1024.0, ObjectNumber: 3.0}}, ListDuration: 1 * time.Second},
			{BucketName: "test-bucket-2", StorageClasses: map[string]StorageClassMetrics{"STANDARD": {Size: 1024.0, ObjectNumber: 2.0}}, ListDuration: 2 * time.Second},
		},
	}

	collector.metricsMutex.Lock()
	collector.metrics = testMetrics
	collector.err = nil
	collector.metricsMutex.Unlock()

	m, err = collector.GetMetrics()
	assert.NoError(t, err)
	assert.True(t, m.EndpointStatus)
	assert.Equal(t, 2, m.BucketCount)
	assert.Equal(t, 2048.0, m.StorageClasses["STANDARD"].Size)
	assert.Equal(t, 5.0, m.StorageClasses["STANDARD"].ObjectNumber)
	assert.Len(t, m.S3Buckets, 2)
	assert.Equal(t, "test-bucket-1", m.S3Buckets[0].BucketName)
	assert.Equal(t, "test-bucket-2", m.S3Buckets[1].BucketName)

	testError := errors.New("test error")
	collector.metricsMutex.Lock()
	collector.err = testError
	collector.metricsMutex.Unlock()

	m, err = collector.GetMetrics()
	assert.Error(t, err)
	assert.Equal(t, testError, err)
}

func TestGetMetricsConcurrentAccess(t *testing.T) {
	collector := NewS3Collector("http://localhost", "us-east-1")

	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = collector.GetMetrics()
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()
			collector.metricsMutex.Lock()
			collector.metrics = S3Summary{EndpointStatus: true, BucketCount: iteration}
			collector.metricsMutex.Unlock()
		}(i)
	}

	wg.Wait()

	m, err := collector.GetMetrics()
	assert.NoError(t, err)
	assert.True(t, m.EndpointStatus)
}
