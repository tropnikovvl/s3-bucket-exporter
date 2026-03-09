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
			name: "Detect WebIdentity",
			config: AuthConfig{
				WebIdentity: "/path/to/token",
				RoleARN:     "arn:aws:iam::123456789012:role/test-role",
			},
			expectedMethod: AuthMethodWebID,
		},
		{
			name: "Detect Role",
			config: AuthConfig{
				RoleARN: "arn:aws:iam::123456789012:role/test-role",
			},
			expectedMethod: AuthMethodRole,
		},
		{
			name: "Detect Keys",
			config: AuthConfig{
				AccessKey: "test-key",
				SecretKey: "test-secret",
			},
			expectedMethod: AuthMethodKeys,
		},
		{
			name:           "Default to IAM",
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
