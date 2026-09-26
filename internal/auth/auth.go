package auth

const (
	// AuthMethodIAM uses the default credential chain of the AWS SDK. The chain
	// covers IRSA (AWS_WEB_IDENTITY_TOKEN_FILE with AWS_ROLE_ARN), EKS Pod
	// Identity and ECS task roles (container credentials provider),
	// ~/.aws/config profiles with role_arn and EC2 IMDS.
	AuthMethodIAM = "iam"
	// AuthMethodKeys uses static credentials from S3_ACCESS_KEY and
	// S3_SECRET_KEY.
	AuthMethodKeys = "keys"
)

type AuthConfig struct {
	Method        string
	Region        string
	Endpoint      string
	AccessKey     string
	SecretKey     string
	SkipTLSVerify bool
	MaxIdleConns  int
}

// DetectAuthMethod determines the authentication method based on available parameters
func DetectAuthMethod(cfg AuthConfig) string {
	if cfg.Method != "" {
		return cfg.Method
	}

	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		return AuthMethodKeys
	}
	return AuthMethodIAM
}
