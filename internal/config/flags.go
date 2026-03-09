package config

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
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
}

func InitFlags() *Config {
	cfg := &Config{}
	flag.StringVar(&cfg.ListenPort, "listen_port", envString("LISTEN_PORT", ":9655"), "Port to listen on")
	flag.StringVar(&cfg.LogLevel, "log_level", envString("LOG_LEVEL", "info"), "Log level (debug, info, warn, error, fatal, panic)")
	flag.StringVar(&cfg.LogFormat, "log_format", envString("LOG_FORMAT", "text"), "Log format (text, json)")
	flag.StringVar(&cfg.ScrapeInterval, "scrape_interval", envString("SCRAPE_INTERVAL", "5m"), "Scrape interval duration")
	flag.StringVar(&cfg.S3Endpoint, "s3_endpoint", envString("S3_ENDPOINT", ""), "S3 endpoint URL")
	flag.StringVar(&cfg.S3BucketNames, "s3_bucket_names", envString("S3_BUCKET_NAMES", ""), "Comma-separated list of S3 bucket names to monitor")
	flag.StringVar(&cfg.S3AccessKey, "s3_access_key", envString("S3_ACCESS_KEY", ""), "S3 access key")
	flag.StringVar(&cfg.S3SecretKey, "s3_secret_key", envString("S3_SECRET_KEY", ""), "S3 secret key")
	flag.StringVar(&cfg.S3Region, "s3_region", envString("S3_REGION", "us-east-1"), "S3 region")
	flag.BoolVar(&cfg.S3ForcePathStyle, "s3_force_path_style", envBool("S3_FORCE_PATH_STYLE", false), "Use path-style S3 URLs")
	flag.BoolVar(&cfg.S3SkipTLSVerify, "s3_skip_tls_verify", envBool("S3_SKIP_TLS_VERIFY", false), "Skip TLS verification for S3 connections")
	return cfg
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

func (c *Config) Validate() error {
	var errs []string

	if _, err := time.ParseDuration(c.ScrapeInterval); err != nil {
		errs = append(errs, fmt.Sprintf("invalid scrape interval '%s': %v", c.ScrapeInterval, err))
	}

	if c.S3Endpoint != "" {
		parsedURL, err := url.Parse(c.S3Endpoint)
		if err != nil {
			errs = append(errs, fmt.Sprintf("invalid S3 endpoint URL '%s': %v", c.S3Endpoint, err))
		} else if parsedURL.Scheme == "" {
			errs = append(errs, fmt.Sprintf("S3 endpoint URL '%s' must include a scheme (http:// or https://)", c.S3Endpoint))
		} else if parsedURL.Host == "" {
			errs = append(errs, fmt.Sprintf("S3 endpoint URL '%s' must include a host", c.S3Endpoint))
		}
	}

	if strings.TrimSpace(c.S3Region) == "" {
		errs = append(errs, "S3 region cannot be empty")
	}

	if !strings.HasPrefix(c.ListenPort, ":") {
		errs = append(errs, fmt.Sprintf("listen port '%s' must start with ':' (e.g., ':9655')", c.ListenPort))
	}

	validLogLevels := map[string]bool{
		"debug": true, "info": true, "warn": true,
		"error": true, "fatal": true, "panic": true,
	}
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		errs = append(errs, fmt.Sprintf("invalid log level '%s': must be one of debug, info, warn, error, fatal, panic", c.LogLevel))
	}

	if c.LogFormat != "text" && c.LogFormat != "json" {
		errs = append(errs, fmt.Sprintf("invalid log format '%s': must be 'text' or 'json'", c.LogFormat))
	}

	if len(errs) > 0 {
		return errors.New("configuration validation failed:\n  - " + strings.Join(errs, "\n  - "))
	}

	return nil
}
