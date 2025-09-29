package main

import (
	"context"
	"flag"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/auth"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/config"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/controllers"
)

func updateMetrics(collector *controllers.S3Collector, interval time.Duration) {
	authCfg := auth.AuthConfig{
		Region:        config.S3Region,
		Endpoint:      config.S3Endpoint,
		AccessKey:     config.S3AccessKey,
		SecretKey:     config.S3SecretKey,
		SkipTLSVerify: config.S3SkipTLSVerify,
	}

	auth.DetectAuthMethod(&authCfg)
	cachedAuth := auth.NewCachedAWSAuth(authCfg)

	for {
		// Use cached authentication - will only refresh when needed
		awsCfg, err := cachedAuth.GetConfig(context.Background())
		if err != nil {
			log.Errorf("Failed to configure authentication: %v", err)
			time.Sleep(interval)
			continue
		}

		s3Conn := controllers.S3Conn{
			Endpoint:       config.S3Endpoint,
			Region:         config.S3Region,
			ForcePathStyle: config.S3ForcePathStyle,
			AWSConfig:      &awsCfg,
		}

		collector.UpdateMetrics(s3Conn, config.S3BucketNames)
		log.Debugf("Waiting for %v before updating metrics", interval)
		time.Sleep(interval)
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
		log.Errorf("Error writing health response: %v", err)
	}
}

func main() {
	config.InitFlags()
	flag.Parse()

	config.SetupLogger()

	interval, err := time.ParseDuration(config.ScrapeInterval)
	if err != nil {
		log.Fatalf("Invalid scrape interval: %s", config.ScrapeInterval)
	}

	collector := controllers.NewS3Collector(config.S3Endpoint, config.S3Region)
	go updateMetrics(collector, interval)

	prometheus.MustRegister(collector)

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", healthHandler)

	srv := &http.Server{
		Addr:         config.ListenPort,
		ReadTimeout:  35 * time.Second,
		WriteTimeout: 35 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Infof("Starting server on %s", config.ListenPort)
	if config.S3BucketNames != "" {
		log.Infof("Monitoring buckets: %s in %s region", config.S3BucketNames, config.S3Region)
	} else {
		log.Infof("Monitoring all buckets in %s region", config.S3Region)
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}
