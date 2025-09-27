package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var (
	authAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "s3_auth_attempts_total",
		Help: "Total number of authentication attempts by method and status",
	}, []string{"method", "status", "s3Endpoint"})
)

func init() {
	prometheus.MustRegister(authAttempts)
}

type AWSAuth struct {
	cfg AuthConfig
}

// CachedAWSAuth provides cached authentication with refresh-based logic
type CachedAWSAuth struct {
	cfg           AuthConfig
	cachedConfig  *aws.Config
	expiresAt     time.Time
	mutex         sync.RWMutex
	refreshBuffer time.Duration // Buffer time before actual expiry to refresh proactively
}

// CachedAuthEntry holds a cached authentication config with expiry
type CachedAuthEntry struct {
	config    aws.Config
	expiresAt time.Time
}

func NewAWSAuth(cfg AuthConfig) *AWSAuth {
	return &AWSAuth{cfg: cfg}
}

// NewCachedAWSAuth creates a new cached AWS authentication manager
func NewCachedAWSAuth(cfg AuthConfig) *CachedAWSAuth {
	return &CachedAWSAuth{
		cfg:           cfg,
		refreshBuffer: 5 * time.Minute, // Refresh 5 minutes before expiry
	}
}

// GetConfig returns cached AWS config or refreshes if needed
func (c *CachedAWSAuth) GetConfig(ctx context.Context) (aws.Config, error) {
	c.mutex.RLock()
	// Check if we have a valid cached config
	if c.cachedConfig != nil && time.Now().Before(c.expiresAt.Add(-c.refreshBuffer)) {
		config := *c.cachedConfig
		c.mutex.RUnlock()
		log.Debug("Using cached AWS configuration")
		return config, nil
	}
	c.mutex.RUnlock()

	// Need to refresh - acquire write lock
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Double-check in case another goroutine refreshed while we waited for the lock
	if c.cachedConfig != nil && time.Now().Before(c.expiresAt.Add(-c.refreshBuffer)) {
		log.Debug("Using AWS configuration refreshed by another goroutine")
		return *c.cachedConfig, nil
	}

	log.Debug("Refreshing AWS authentication configuration")
	
	// Create temporary AWSAuth to get fresh config
	tempAuth := NewAWSAuth(c.cfg)
	newConfig, err := tempAuth.GetConfig(ctx)
	if err != nil {
		return aws.Config{}, err
	}

	// Cache the new config with expiry time
	c.cachedConfig = &newConfig
	c.expiresAt = c.calculateExpiry()
	
	log.Debugf("AWS configuration cached until %v", c.expiresAt)
	return newConfig, nil
}

// calculateExpiry determines when the cached config should expire
func (c *CachedAWSAuth) calculateExpiry() time.Time {
	// For different auth methods, set appropriate expiry times
	switch c.cfg.Method {
	case AuthMethodKeys:
		// Static credentials don't expire, but refresh periodically for safety
		return time.Now().Add(1 * time.Hour)
	case AuthMethodRole, AuthMethodWebID:
		// STS credentials typically expire in 1 hour, but we'll be conservative
		return time.Now().Add(45 * time.Minute)
	case AuthMethodIAM:
		// IAM role credentials from EC2 metadata, refresh more frequently
		return time.Now().Add(30 * time.Minute)
	default:
		// Default conservative expiry
		return time.Now().Add(30 * time.Minute)
	}
}

// InvalidateCache forces cache invalidation for testing or error recovery
func (c *CachedAWSAuth) InvalidateCache() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.cachedConfig = nil
	c.expiresAt = time.Time{}
	log.Debug("AWS authentication cache invalidated")
}

type ConfigLoader interface {
	Load(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error)
}

type defaultConfigLoader struct{}

func (d *defaultConfigLoader) Load(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
	return config.LoadDefaultConfig(ctx, optFns...)
}

var configLoader ConfigLoader = &defaultConfigLoader{}

func (a *AWSAuth) GetConfig(ctx context.Context) (aws.Config, error) {
	log.Debugf("Starting authentication with method: %s", a.cfg.Method)

	status := "success"
	defer func() {
		authAttempts.With(prometheus.Labels{
			"method":     a.cfg.Method,
			"status":     status,
			"s3Endpoint": a.cfg.Endpoint,
		}).Inc()
	}()

	if a.cfg.Region == "" {
		status = "error"
		err := errors.New("region is required")
		return aws.Config{}, err
	}

	options := []func(*config.LoadOptions) error{
		config.WithRegion(a.cfg.Region),
	}

	if a.cfg.Endpoint != "" {
		options = append(options, config.WithDefaultsMode(aws.DefaultsModeStandard))
		options = append(options, func(o *config.LoadOptions) error {
			o.BaseEndpoint = string(a.cfg.Endpoint)
			return nil
		})
	}

	if a.cfg.SkipTLSVerify {
		customTransport := http.DefaultTransport.(*http.Transport).Clone()
		customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		options = append(options, config.WithHTTPClient(&http.Client{
			Transport: customTransport,
		}))
		log.Debug("TLS verification is disabled")
	}

	switch a.cfg.Method {
	case AuthMethodKeys:
		options = append(options, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(a.cfg.AccessKey, a.cfg.SecretKey, ""),
		))

	case AuthMethodRole:
		baseConfig, err := configLoader.Load(ctx, options...)
		if err != nil {
			status = "error"
			return aws.Config{}, fmt.Errorf("failed to load base AWS config: %w", err)
		}

		options = append(options, config.WithCredentialsProvider(
			stscreds.NewAssumeRoleProvider(
				sts.NewFromConfig(baseConfig),
				a.cfg.RoleARN,
			),
		))

	case AuthMethodWebID:
		options = append(options, config.WithWebIdentityRoleCredentialOptions(
			func(o *stscreds.WebIdentityRoleOptions) {
				o.RoleARN = a.cfg.RoleARN
				o.TokenRetriever = stscreds.IdentityTokenFile(a.cfg.WebIdentity)
			},
		))

	case AuthMethodIAM:
		log.Debug("Using IAM role authentication")

	default:
		status = "error"
		return aws.Config{}, fmt.Errorf("unsupported authentication method: %s", a.cfg.Method)
	}

	return configLoader.Load(ctx, options...)
}
