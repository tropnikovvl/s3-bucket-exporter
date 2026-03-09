package auth

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
func DetectAuthMethod(cfg AuthConfig) string {
	if cfg.Method != "" {
		return cfg.Method
	}

	switch {
	case cfg.WebIdentity != "" && cfg.RoleARN != "":
		return AuthMethodWebID
	case cfg.RoleARN != "":
		return AuthMethodRole
	case cfg.AccessKey != "" && cfg.SecretKey != "":
		return AuthMethodKeys
	default:
		return AuthMethodIAM
	}
}
