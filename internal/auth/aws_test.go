package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAWSConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      AuthConfig
		shouldError bool
		errorMsg    string
	}{
		{
			name: "Empty region",
			config: AuthConfig{
				Method: AuthMethodIAM,
				Region: "",
			},
			shouldError: true,
			errorMsg:    "region is required",
		},
		{
			name: "Invalid auth method",
			config: AuthConfig{
				Region: "us-east-1",
				Method: "invalid",
			},
			shouldError: true,
			errorMsg:    "unsupported authentication method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GetAWSConfig(context.Background(), tt.config)
			if tt.shouldError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
