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

func updateMetrics(ctx context.Context, collector *controllers.S3Collector, cfg *config.Config, interval time.Duration, clientFactory func(aws.Config) controllers.S3ClientInterface) {
	authCfg := auth.AuthConfig{
		Region:        cfg.S3Region,
		Endpoint:      cfg.S3Endpoint,
		AccessKey:     cfg.S3AccessKey,
		SecretKey:     cfg.S3SecretKey,
		SkipTLSVerify: cfg.S3SkipTLSVerify,
	}

	authCfg.Method = auth.DetectAuthMethod(authCfg)
	cachedAuth := auth.NewCachedAWSAuth(authCfg)

	collectMetrics := func() {
		iterCtx, cancel := context.WithTimeout(ctx, interval*9/10)
		defer cancel()

		awsCfg, err := cachedAuth.GetConfig(iterCtx)
		if err != nil {
			log.Errorf("Failed to configure authentication: %v", err)
			return
		}

		collector.UpdateMetrics(iterCtx, clientFactory(awsCfg), cfg.S3BucketNames)
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
	if _, err := w.Write([]byte("OK")); err != nil {
		log.Errorf("Error writing health response: %v", err)
	}
}

func main() {
	cfg := config.InitFlags()
	flag.Parse()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	cfg.SetupLogger()

	interval, err := time.ParseDuration(cfg.ScrapeInterval)
	if err != nil {
		log.Fatalf("Invalid scrape interval: %s", cfg.ScrapeInterval)
	}

	ctx, cancel := context.WithCancel(context.Background())

	collector := controllers.NewS3Collector(cfg.S3Endpoint, cfg.S3Region)
	go updateMetrics(ctx, collector, cfg, interval, func(awsCfg aws.Config) controllers.S3ClientInterface {
		return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.UsePathStyle = cfg.S3ForcePathStyle
		})
	})

	prometheus.MustRegister(collector)

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", healthHandler)

	srv := &http.Server{
		Addr:         cfg.ListenPort,
		ReadTimeout:  35 * time.Second,
		WriteTimeout: 35 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	log.Infof("Starting server on %s", cfg.ListenPort)
	if cfg.S3BucketNames != "" {
		log.Infof("Monitoring buckets: %s in %s region", cfg.S3BucketNames, cfg.S3Region)
	} else {
		log.Infof("Monitoring all buckets in %s region", cfg.S3Region)
	}

	go func() {
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
