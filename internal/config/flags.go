package config

import (
	"flag"
	"os"
	"strconv"
)

var (
	ListenPort       string
	LogLevel         string
	LogFormat        string
	ScrapeInterval   string
	S3Endpoint       string
	S3BucketNames    string
	S3AccessKey      string
	S3SecretKey      string
	S3Region         string
	S3ForcePathStyle bool
	S3SkipTLSVerify  bool
)

func InitFlags() {
	flag.StringVar(&ListenPort, "listen_port", envString("LISTEN_PORT", ":9655"), "Port to listen on")
	flag.StringVar(&LogLevel, "log_level", envString("LOG_LEVEL", "info"), "Log level (debug, info, warn, error, fatal, panic)")
	flag.StringVar(&LogFormat, "log_format", envString("LOG_FORMAT", "text"), "Log format (text, json)")
	flag.StringVar(&ScrapeInterval, "scrape_interval", envString("SCRAPE_INTERVAL", "5m"), "Scrape interval duration")
	flag.StringVar(&S3Endpoint, "s3_endpoint", envString("S3_ENDPOINT", ""), "S3 endpoint URL")
	flag.StringVar(&S3BucketNames, "s3_bucket_names", envString("S3_BUCKET_NAMES", ""), "Comma-separated list of S3 bucket names to monitor")
	flag.StringVar(&S3AccessKey, "s3_access_key", envString("S3_ACCESS_KEY", ""), "S3 access key")
	flag.StringVar(&S3SecretKey, "s3_secret_key", envString("S3_SECRET_KEY", ""), "S3 secret key")
	flag.StringVar(&S3Region, "s3_region", envString("S3_REGION", "us-east-1"), "S3 region")
	flag.BoolVar(&S3ForcePathStyle, "s3_force_path_style", envBool("S3_FORCE_PATH_STYLE", false), "Use path-style S3 URLs")
	flag.BoolVar(&S3SkipTLSVerify, "s3_skip_tls_verify", envBool("S3_SKIP_TLS_VERIFY", false), "Skip TLS verification for S3 connections")
}

func envString(key string, def string) string {
	if x := os.Getenv(key); x != "" {
		return x
	}
	return def
}

func envBool(key string, def bool) bool {
	x, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return x
}
