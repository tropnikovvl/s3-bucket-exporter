package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectAuthMethod(t *testing.T) {
	tests := []struct {
		name           string
		config         AuthConfig
		expectedMethod string
	}{
		{
			name: "explicit method wins",
			config: AuthConfig{
				Method:    AuthMethodIAM,
				AccessKey: "test-key",
				SecretKey: "test-secret",
			},
			expectedMethod: AuthMethodIAM,
		},
		{
			name: "detect keys when both are set",
			config: AuthConfig{
				AccessKey: "test-key",
				SecretKey: "test-secret",
			},
			expectedMethod: AuthMethodKeys,
		},
		{
			name: "access key alone falls back to the default chain",
			config: AuthConfig{
				AccessKey: "test-key",
			},
			expectedMethod: AuthMethodIAM,
		},
		{
			name: "secret key alone falls back to the default chain",
			config: AuthConfig{
				SecretKey: "test-secret",
			},
			expectedMethod: AuthMethodIAM,
		},
		{
			name:           "default to the credential chain",
			config:         AuthConfig{},
			expectedMethod: AuthMethodIAM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := DetectAuthMethod(tt.config)
			assert.Equal(t, tt.expectedMethod, method)
		})
	}
}
