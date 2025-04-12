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
				defer os.Unsetenv(tt.key)
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
				defer os.Unsetenv(tt.key)
			}
			got := envBool(tt.key, tt.defValue)
			assert.Equal(t, tt.expValue, got)
		})
	}
}
