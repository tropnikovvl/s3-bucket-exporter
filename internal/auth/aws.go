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
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

type AWSAuth struct {
	cfg AuthConfig
	// httpClient exists once and every credential refresh shares it. A new
	// transport for each refresh discards the warm keep-alive pool. The next
	// scrape then makes up to MaxIdleConns TLS handshakes again.
	httpClient *http.Client
	loader     func(context.Context, ...func(*config.LoadOptions) error) (aws.Config, error)
}

// CachedAWSAuth provides cached authentication with refresh-based logic
type CachedAWSAuth struct {
	AWSAuth
	cachedConfig  *aws.Config
	expiresAt     time.Time
	mutex         sync.RWMutex
	refreshBuffer time.Duration // Buffer time before actual expiry to refresh proactively
}

func NewAWSAuth(cfg AuthConfig) *AWSAuth {
	return &AWSAuth{
		cfg:        cfg,
		httpClient: buildHTTPClient(cfg),
		loader:     config.LoadDefaultConfig,
	}
}

// buildHTTPClient returns a client with a pool as large as the LIST
// concurrency budget. It returns nil when the SDK defaults are sufficient:
// without a custom client, the SDK uses its own client with the timeouts of
// the defaults mode. A nil result means "do not pass config.WithHTTPClient".
func buildHTTPClient(cfg AuthConfig) *http.Client {
	if !cfg.SkipTLSVerify && cfg.MaxIdleConns <= 0 {
		return nil
	}

	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		baseTransport = &http.Transport{}
	}
	customTransport := baseTransport.Clone()

	if cfg.MaxIdleConns > 0 {
		// Size the connection pool to the LIST concurrency budget so parallel
		// listing reuses keep-alive connections instead of churning TLS handshakes.
		customTransport.MaxIdleConns = cfg.MaxIdleConns
		customTransport.MaxIdleConnsPerHost = cfg.MaxIdleConns
	}
	if cfg.SkipTLSVerify {
		// #nosec G402 -- user opt-in via S3_SKIP_TLS_VERIFY
		customTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		log.Debug("TLS verification is disabled")
	}

	return &http.Client{Transport: customTransport}
}

// NewCachedAWSAuth creates a new cached AWS authentication manager
func NewCachedAWSAuth(cfg AuthConfig) *CachedAWSAuth {
	return &CachedAWSAuth{
		AWSAuth:       *NewAWSAuth(cfg),
		refreshBuffer: 5 * time.Minute, // Refresh 5 minutes before expiry
	}
}

func (c *CachedAWSAuth) isCacheValid() bool {
	return c.cachedConfig != nil && (c.expiresAt.IsZero() || time.Now().Before(c.expiresAt.Add(-c.refreshBuffer)))
}

// GetConfig returns cached AWS config or refreshes if needed
func (c *CachedAWSAuth) GetConfig(ctx context.Context) (aws.Config, error) {
	c.mutex.RLock()
	if c.isCacheValid() {
		cfg := *c.cachedConfig
		c.mutex.RUnlock()
		log.Debug("Using cached AWS configuration")
		return cfg, nil
	}
	c.mutex.RUnlock()

	// Need to refresh - acquire write lock
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Double-check in case another goroutine refreshed while we waited for the lock
	if c.isCacheValid() {
		log.Debug("Using AWS configuration refreshed by another goroutine")
		return *c.cachedConfig, nil
	}

	log.Debug("Refreshing AWS authentication configuration")

	newConfig, err := c.AWSAuth.GetConfig(ctx)
	if err != nil {
		return aws.Config{}, err
	}

	// Retrieve the credentials now, so that the cache expiry matches their real
	// lifetime. Fill the cache only after a successful retrieval. A transient
	// failure otherwise poisons the cache. A non-nil config with a zero expiry
	// reads as "valid forever", and the loader then never retries.
	creds, err := newConfig.Credentials.Retrieve(ctx)
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to retrieve credentials: %w", err)
	}

	c.cachedConfig = &newConfig
	c.expiresAt = c.calculateExpiry(creds)

	log.Debugf("AWS configuration cached until %v", c.expiresAt)
	return newConfig, nil
}

// calculateExpiry returns the cached config's expiry derived from the
// credentials themselves. Non-expiring credentials (static keys) yield the
// zero time, meaning "never expires".
func (c *CachedAWSAuth) calculateExpiry(creds aws.Credentials) time.Time {
	if !creds.CanExpire {
		return time.Time{}
	}
	return creds.Expires
}

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
			o.BaseEndpoint = a.cfg.Endpoint
			return nil
		})
	}

	if a.httpClient != nil {
		options = append(options, config.WithHTTPClient(a.httpClient))
	}

	switch a.cfg.Method {
	case AuthMethodKeys:
		options = append(options, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(a.cfg.AccessKey, a.cfg.SecretKey, ""),
		))

	case AuthMethodIAM:
		log.Debug("Using the default AWS credential chain")

	default:
		status = "error"
		return aws.Config{}, fmt.Errorf("unsupported authentication method: %s", a.cfg.Method)
	}

	return a.loader(ctx, options...)
}
