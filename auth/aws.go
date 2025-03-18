package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"

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

func NewAWSAuth(cfg AuthConfig) *AWSAuth {
	return &AWSAuth{cfg: cfg}
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
