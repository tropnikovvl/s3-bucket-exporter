package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/auth"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/config"
	"github.com/tropnikovvl/s3-bucket-exporter/internal/controllers"
)

func updateMetrics(ctx context.Context, collector *controllers.S3Collector, interval time.Duration, clientFactory func(aws.Config) controllers.S3ClientInterface) {
	authCfg := auth.AuthConfig{
		Region:        config.S3Region,
		Endpoint:      config.S3Endpoint,
		AccessKey:     config.S3AccessKey,
		SecretKey:     config.S3SecretKey,
		SkipTLSVerify: config.S3SkipTLSVerify,
	}

	authCfg.Method = auth.DetectAuthMethod(authCfg)
	cachedAuth := auth.NewCachedAWSAuth(authCfg)

	collectMetrics := func() {
		timeoutDuration := time.Duration(float64(interval) * 0.9)
		iterCtx, cancel := context.WithTimeout(ctx, timeoutDuration)
		defer cancel()

		awsCfg, err := cachedAuth.GetConfig(iterCtx)
		if err != nil {
			log.Errorf("Failed to configure authentication: %v", err)
			return
		}

		collector.UpdateMetrics(iterCtx, clientFactory(awsCfg), config.S3Region, config.S3BucketNames)
	}

	collectMetrics()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("Stopping metrics collection due to context cancellation")
			return
		case <-ticker.C:
			collectMetrics()
		}
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

	if err := config.ValidateConfig(); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	config.SetupLogger()

	interval, err := time.ParseDuration(config.ScrapeInterval)
	if err != nil {
		log.Fatalf("Invalid scrape interval: %s", config.ScrapeInterval)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	collector := controllers.NewS3Collector(config.S3Endpoint, config.S3Region)
	go updateMetrics(ctx, collector, interval, func(cfg aws.Config) controllers.S3ClientInterface {
		return s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.UsePathStyle = config.S3ForcePathStyle
		})
	})

	prometheus.MustRegister(collector)

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", healthHandler)

	srv := &http.Server{
		Addr:         config.ListenPort,
		ReadTimeout:  35 * time.Second,
		WriteTimeout: 35 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Infof("Starting server on %s", config.ListenPort)
		if config.S3BucketNames != "" {
			log.Infof("Monitoring buckets: %s in %s region", config.S3BucketNames, config.S3Region)
		} else {
			log.Infof("Monitoring all buckets in %s region", config.S3Region)
		}

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	sig := <-sigChan
	log.Infof("Received signal %v, initiating graceful shutdown...", sig)

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Errorf("Server shutdown error: %v", err)
	} else {
		log.Info("Server shutdown completed successfully")
	}
}
