package main

import (
	"testing"

	"github.com/tropnikovvl/s3-bucket-exporter/internal/auth"
)

// The benchmark must size its HTTP pool like the exporter does
// (cmd/s3-bucket-exporter/main.go: MaxIdleConns = S3MaxConcurrency), otherwise
// it measures the SDK's default 10-conn-per-host pool instead of the exporter.
func TestBenchAuthConfigMatchesExporterPool(t *testing.T) {
	tests := []struct {
		name                 string
		accessKey, secretKey string
		concurrency          int
		wantMethod           string
	}{
		{"static keys", "a", "b", 25, auth.AuthMethodKeys},
		{"default chain, concurrency 1", "", "", 1, auth.AuthMethodIAM},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := benchAuthConfig("eu-west-1", "http://localhost:4566", tt.accessKey, tt.secretKey, tt.concurrency)
			if got.MaxIdleConns != tt.concurrency {
				t.Errorf("MaxIdleConns = %d, want %d", got.MaxIdleConns, tt.concurrency)
			}
			if got.Method != tt.wantMethod {
				t.Errorf("Method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.Region != "eu-west-1" || got.Endpoint != "http://localhost:4566" {
				t.Errorf("Region/Endpoint not passed through: %+v", got)
			}
			if got.AccessKey != tt.accessKey || got.SecretKey != tt.secretKey {
				t.Errorf("keys not passed through: %+v", got)
			}
		})
	}
}
