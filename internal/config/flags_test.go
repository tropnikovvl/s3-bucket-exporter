package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvString(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		defValue string
		envValue string
		expValue string
	}{
		{
			name:     "returns default when env not set",
			key:      "TEST_KEY_1",
			defValue: "default",
			envValue: "",
			expValue: "default",
		},
		{
			name:     "returns env value when set",
			key:      "TEST_KEY_2",
			defValue: "default",
			envValue: "from_env",
			expValue: "from_env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				err := os.Setenv(tt.key, tt.envValue)
				if err != nil {
					t.Fatalf("failed to set environment variable: %v", err)
				}
				defer func() {
					if err := os.Unsetenv(tt.key); err != nil {
						t.Errorf("failed to unset environment variable: %v", err)
					}
				}()
			}
			got := envString(tt.key, tt.defValue)
			assert.Equal(t, tt.expValue, got)
		})
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		defValue bool
		envValue string
		expValue bool
	}{
		{
			name:     "returns default when env not set",
			key:      "TEST_BOOL_1",
			defValue: false,
			envValue: "",
			expValue: false,
		},
		{
			name:     "returns true when env is 'true'",
			key:      "TEST_BOOL_2",
			defValue: false,
			envValue: "true",
			expValue: true,
		},
		{
			name:     "returns default for invalid value",
			key:      "TEST_BOOL_3",
			defValue: true,
			envValue: "invalid",
			expValue: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				err := os.Setenv(tt.key, tt.envValue)
				if err != nil {
					t.Fatalf("failed to set environment variable: %v", err)
				}
				defer func() {
					if err := os.Unsetenv(tt.key); err != nil {
						t.Errorf("failed to unset environment variable: %v", err)
					}
				}()
			}
			got := envBool(tt.key, tt.defValue)
			assert.Equal(t, tt.expValue, got)
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name          string
		setupConfig   func()
		expectError   bool
		errorContains string
	}{
		{
			name: "valid configuration",
			setupConfig: func() {
				ScrapeInterval = "5m"
				S3Endpoint = "https://s3.amazonaws.com"
				S3Region = "us-east-1"
				ListenPort = ":9655"
				LogLevel = "info"
				LogFormat = "text"
			},
			expectError: false,
		},
		{
			name: "valid configuration with empty endpoint",
			setupConfig: func() {
				ScrapeInterval = "1h"
				S3Endpoint = ""
				S3Region = "eu-west-1"
				ListenPort = ":8080"
				LogLevel = "debug"
				LogFormat = "json"
			},
			expectError: false,
		},
		{
			name: "invalid scrape interval",
			setupConfig: func() {
				ScrapeInterval = "invalid"
				S3Endpoint = "https://s3.amazonaws.com"
				S3Region = "us-east-1"
				ListenPort = ":9655"
				LogLevel = "info"
				LogFormat = "text"
			},
			expectError:   true,
			errorContains: "invalid scrape interval",
		},
		{
			name: "invalid endpoint - no scheme",
			setupConfig: func() {
				ScrapeInterval = "5m"
				S3Endpoint = "s3.amazonaws.com"
				S3Region = "us-east-1"
				ListenPort = ":9655"
				LogLevel = "info"
				LogFormat = "text"
			},
			expectError:   true,
			errorContains: "must include a scheme",
		},
		{
			name: "invalid endpoint - no host",
			setupConfig: func() {
				ScrapeInterval = "5m"
				S3Endpoint = "http://"
				S3Region = "us-east-1"
				ListenPort = ":9655"
				LogLevel = "info"
				LogFormat = "text"
			},
			expectError:   true,
			errorContains: "must include a host",
		},
		{
			name: "empty region",
			setupConfig: func() {
				ScrapeInterval = "5m"
				S3Endpoint = "https://s3.amazonaws.com"
				S3Region = ""
				ListenPort = ":9655"
				LogLevel = "info"
				LogFormat = "text"
			},
			expectError:   true,
			errorContains: "region cannot be empty",
		},
		{
			name: "invalid listen port format",
			setupConfig: func() {
				ScrapeInterval = "5m"
				S3Endpoint = "https://s3.amazonaws.com"
				S3Region = "us-east-1"
				ListenPort = "9655"
				LogLevel = "info"
				LogFormat = "text"
			},
			expectError:   true,
			errorContains: "must start with ':'",
		},
		{
			name: "invalid log level",
			setupConfig: func() {
				ScrapeInterval = "5m"
				S3Endpoint = "https://s3.amazonaws.com"
				S3Region = "us-east-1"
				ListenPort = ":9655"
				LogLevel = "invalid"
				LogFormat = "text"
			},
			expectError:   true,
			errorContains: "invalid log level",
		},
		{
			name: "invalid log format",
			setupConfig: func() {
				ScrapeInterval = "5m"
				S3Endpoint = "https://s3.amazonaws.com"
				S3Region = "us-east-1"
				ListenPort = ":9655"
				LogLevel = "info"
				LogFormat = "xml"
			},
			expectError:   true,
			errorContains: "invalid log format",
		},
		{
			name: "multiple validation errors",
			setupConfig: func() {
				ScrapeInterval = "invalid"
				S3Endpoint = "invalid-url"
				S3Region = ""
				ListenPort = "9655"
				LogLevel = "bad"
				LogFormat = "bad"
			},
			expectError:   true,
			errorContains: "configuration validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupConfig()
			err := ValidateConfig()

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
