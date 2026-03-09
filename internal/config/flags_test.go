package config

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		cfg           Config
		expectError   bool
		errorContains string
	}{
		{
			name: "valid configuration",
			cfg: Config{
				ScrapeInterval: "5m",
				S3Endpoint:     "https://s3.amazonaws.com",
				S3Region:       "us-east-1",
				ListenPort:     ":9655",
				LogLevel:       "info",
				LogFormat:      "text",
			},
			expectError: false,
		},
		{
			name: "valid configuration with empty endpoint",
			cfg: Config{
				ScrapeInterval: "1h",
				S3Endpoint:     "",
				S3Region:       "eu-west-1",
				ListenPort:     ":8080",
				LogLevel:       "debug",
				LogFormat:      "json",
			},
			expectError: false,
		},
		{
			name: "invalid scrape interval",
			cfg: Config{
				ScrapeInterval: "invalid",
				S3Endpoint:     "https://s3.amazonaws.com",
				S3Region:       "us-east-1",
				ListenPort:     ":9655",
				LogLevel:       "info",
				LogFormat:      "text",
			},
			expectError:   true,
			errorContains: "invalid scrape interval",
		},
		{
			name: "invalid endpoint - no scheme",
			cfg: Config{
				ScrapeInterval: "5m",
				S3Endpoint:     "s3.amazonaws.com",
				S3Region:       "us-east-1",
				ListenPort:     ":9655",
				LogLevel:       "info",
				LogFormat:      "text",
			},
			expectError:   true,
			errorContains: "must include a scheme",
		},
		{
			name: "invalid endpoint - no host",
			cfg: Config{
				ScrapeInterval: "5m",
				S3Endpoint:     "http://",
				S3Region:       "us-east-1",
				ListenPort:     ":9655",
				LogLevel:       "info",
				LogFormat:      "text",
			},
			expectError:   true,
			errorContains: "must include a host",
		},
		{
			name: "empty region",
			cfg: Config{
				ScrapeInterval: "5m",
				S3Endpoint:     "https://s3.amazonaws.com",
				S3Region:       "",
				ListenPort:     ":9655",
				LogLevel:       "info",
				LogFormat:      "text",
			},
			expectError:   true,
			errorContains: "region cannot be empty",
		},
		{
			name: "invalid listen port format",
			cfg: Config{
				ScrapeInterval: "5m",
				S3Endpoint:     "https://s3.amazonaws.com",
				S3Region:       "us-east-1",
				ListenPort:     "9655",
				LogLevel:       "info",
				LogFormat:      "text",
			},
			expectError:   true,
			errorContains: "must start with ':'",
		},
		{
			name: "invalid log level",
			cfg: Config{
				ScrapeInterval: "5m",
				S3Endpoint:     "https://s3.amazonaws.com",
				S3Region:       "us-east-1",
				ListenPort:     ":9655",
				LogLevel:       "invalid",
				LogFormat:      "text",
			},
			expectError:   true,
			errorContains: "invalid log level",
		},
		{
			name: "invalid log format",
			cfg: Config{
				ScrapeInterval: "5m",
				S3Endpoint:     "https://s3.amazonaws.com",
				S3Region:       "us-east-1",
				ListenPort:     ":9655",
				LogLevel:       "info",
				LogFormat:      "xml",
			},
			expectError:   true,
			errorContains: "invalid log format",
		},
		{
			name: "multiple validation errors",
			cfg: Config{
				ScrapeInterval: "invalid",
				S3Endpoint:     "invalid-url",
				S3Region:       "",
				ListenPort:     "9655",
				LogLevel:       "bad",
				LogFormat:      "bad",
			},
			expectError:   true,
			errorContains: "configuration validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()

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

func TestInitFlags(t *testing.T) {
	oldCommandLine := flag.CommandLine
	defer func() { flag.CommandLine = oldCommandLine }()

	tests := []struct {
		name     string
		envVars  map[string]string
		expected Config
	}{
		{
			name: "default values when no env vars set",
			envVars: map[string]string{
				"LISTEN_PORT":         "",
				"LOG_LEVEL":           "",
				"LOG_FORMAT":          "",
				"SCRAPE_INTERVAL":     "",
				"S3_ENDPOINT":         "",
				"S3_BUCKET_NAMES":     "",
				"S3_ACCESS_KEY":       "",
				"S3_SECRET_KEY":       "",
				"S3_REGION":           "",
				"S3_FORCE_PATH_STYLE": "",
				"S3_SKIP_TLS_VERIFY":  "",
			},
			expected: Config{
				ListenPort:       ":9655",
				LogLevel:         "info",
				LogFormat:        "text",
				ScrapeInterval:   "5m",
				S3Endpoint:       "",
				S3BucketNames:    "",
				S3AccessKey:      "",
				S3SecretKey:      "",
				S3Region:         "us-east-1",
				S3ForcePathStyle: false,
				S3SkipTLSVerify:  false,
			},
		},
		{
			name: "custom values from env vars",
			envVars: map[string]string{
				"LISTEN_PORT":         ":8080",
				"LOG_LEVEL":           "debug",
				"LOG_FORMAT":          "json",
				"SCRAPE_INTERVAL":     "10m",
				"S3_ENDPOINT":         "https://s3.custom.com",
				"S3_BUCKET_NAMES":     "bucket1,bucket2",
				"S3_ACCESS_KEY":       "test-key",
				"S3_SECRET_KEY":       "test-secret",
				"S3_REGION":           "eu-west-1",
				"S3_FORCE_PATH_STYLE": "true",
				"S3_SKIP_TLS_VERIFY":  "true",
			},
			expected: Config{
				ListenPort:       ":8080",
				LogLevel:         "debug",
				LogFormat:        "json",
				ScrapeInterval:   "10m",
				S3Endpoint:       "https://s3.custom.com",
				S3BucketNames:    "bucket1,bucket2",
				S3AccessKey:      "test-key",
				S3SecretKey:      "test-secret",
				S3Region:         "eu-west-1",
				S3ForcePathStyle: true,
				S3SkipTLSVerify:  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

			for key, value := range tt.envVars {
				if value == "" {
					_ = os.Unsetenv(key)
				} else {
					_ = os.Setenv(key, value)
				}
			}
			defer func() {
				for key := range tt.envVars {
					_ = os.Unsetenv(key)
				}
			}()

			cfg := InitFlags()

			err := flag.CommandLine.Parse([]string{})
			require.NoError(t, err)

			assert.Equal(t, tt.expected.ListenPort, cfg.ListenPort)
			assert.Equal(t, tt.expected.LogLevel, cfg.LogLevel)
			assert.Equal(t, tt.expected.LogFormat, cfg.LogFormat)
			assert.Equal(t, tt.expected.ScrapeInterval, cfg.ScrapeInterval)
			assert.Equal(t, tt.expected.S3Endpoint, cfg.S3Endpoint)
			assert.Equal(t, tt.expected.S3BucketNames, cfg.S3BucketNames)
			assert.Equal(t, tt.expected.S3AccessKey, cfg.S3AccessKey)
			assert.Equal(t, tt.expected.S3SecretKey, cfg.S3SecretKey)
			assert.Equal(t, tt.expected.S3Region, cfg.S3Region)
			assert.Equal(t, tt.expected.S3ForcePathStyle, cfg.S3ForcePathStyle)
			assert.Equal(t, tt.expected.S3SkipTLSVerify, cfg.S3SkipTLSVerify)
		})
	}
}

func TestSetupLogger(t *testing.T) {
	tests := []struct {
		name           string
		logLevel       string
		logFormat      string
		validateOutput func(t *testing.T, output string)
	}{
		{
			name:      "text format with info level",
			logLevel:  "info",
			logFormat: "text",
			validateOutput: func(t *testing.T, output string) {
				assert.Contains(t, output, "level=info")
				assert.Contains(t, output, "test message")
			},
		},
		{
			name:      "json format with debug level",
			logLevel:  "debug",
			logFormat: "json",
			validateOutput: func(t *testing.T, output string) {
				assert.Contains(t, output, `"level":"debug"`)
				assert.Contains(t, output, `"msg":"test message"`)
			},
		},
		{
			name:      "text format with warn level",
			logLevel:  "warn",
			logFormat: "text",
			validateOutput: func(t *testing.T, output string) {
				assert.Contains(t, output, "level=warning")
				assert.Contains(t, output, "test message")
			},
		},
		{
			name:      "text format with error level",
			logLevel:  "error",
			logFormat: "text",
			validateOutput: func(t *testing.T, output string) {
				assert.Contains(t, output, "level=error")
				assert.Contains(t, output, "test message")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{LogLevel: tt.logLevel, LogFormat: tt.logFormat}
			cfg.SetupLogger()

			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stdout)

			switch tt.logLevel {
			case "debug":
				log.Debug("test message")
			case "info":
				log.Info("test message")
			case "warn":
				log.Warn("test message")
			case "error":
				log.Error("test message")
			}

			if tt.validateOutput != nil {
				tt.validateOutput(t, buf.String())
			}
		})
	}
}

func TestSetupLogger_InvalidLevel(t *testing.T) {
	invalidLevels := []string{"invalid", "bad", "notexist"}

	for _, level := range invalidLevels {
		t.Run("invalid_level_"+level, func(t *testing.T) {
			_, err := log.ParseLevel(level)
			assert.Error(t, err, "ParseLevel should return error for invalid level: %s", level)
		})
	}
}

func TestSetupLogger_TextFormatterOutput(t *testing.T) {
	cfg := &Config{LogLevel: "info", LogFormat: "text"}
	cfg.SetupLogger()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stdout)

	log.WithFields(log.Fields{
		"key1": "value1",
		"key2": "value2",
	}).Info("test message with fields")

	output := buf.String()
	assert.Contains(t, output, "level=info")
	assert.Contains(t, output, "test message with fields")
	assert.Contains(t, output, "key1=value1")
	assert.Contains(t, output, "key2=value2")
}

func TestSetupLogger_JSONFormatterOutput(t *testing.T) {
	cfg := &Config{LogLevel: "info", LogFormat: "json"}
	cfg.SetupLogger()

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stdout)

	log.WithFields(log.Fields{
		"key1": "value1",
		"key2": 123,
	}).Info("test message with fields")

	output := buf.String()
	assert.True(t, strings.HasPrefix(output, "{"), "JSON output should start with {")
	assert.Contains(t, output, `"level":"info"`)
	assert.Contains(t, output, `"msg":"test message with fields"`)
	assert.Contains(t, output, `"key1":"value1"`)
	assert.Contains(t, output, `"key2":123`)
}
