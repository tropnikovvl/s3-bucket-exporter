package auth

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const (
	AuthMethodIAM   = "iam"
	AuthMethodKeys  = "keys"
	AuthMethodRole  = "role"
	AuthMethodWebID = "webid"
)

type AuthConfig struct {
	Method        string
	Region        string
	Endpoint      string
	AccessKey     string
	SecretKey     string
	RoleARN       string
	WebIdentity   string
	SkipTLSVerify bool
}

// DetectAuthMethod determines the authentication method based on available parameters
func DetectAuthMethod(cfg *AuthConfig) {
	if cfg.Method != "" {
		return
	}

	switch {
	case cfg.WebIdentity != "" && cfg.RoleARN != "":
		cfg.Method = AuthMethodWebID
	case cfg.RoleARN != "":
		cfg.Method = AuthMethodRole
	case cfg.AccessKey != "" && cfg.SecretKey != "":
		cfg.Method = AuthMethodKeys
	default:
		cfg.Method = AuthMethodIAM
	}
}

func GetAWSConfig(ctx context.Context, cfg AuthConfig) (aws.Config, error) {
	auth := NewAWSAuth(cfg)
	return auth.GetConfig(ctx)
}

// GetCachedAWSConfig returns a cached AWS config, refreshing only when needed
func GetCachedAWSConfig(ctx context.Context, cfg AuthConfig) (aws.Config, error) {
	cachedAuth := NewCachedAWSAuth(cfg)
	return cachedAuth.GetConfig(ctx)
}
