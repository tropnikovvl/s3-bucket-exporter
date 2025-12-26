package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// Mock config loader for testing
type mockConfigLoader struct {
	loadFunc func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error)
	callCount int
	mu sync.Mutex
}

func (m *mockConfigLoader) Load(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	return m.loadFunc(ctx, optFns...)
}

func (m *mockConfigLoader) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func TestNewCachedAWSAuth(t *testing.T) {
	cfg := AuthConfig{
		Region: "us-east-1",
		Method: AuthMethodIAM,
	}

	cachedAuth := NewCachedAWSAuth(cfg)

	assert.NotNil(t, cachedAuth)
	assert.Equal(t, cfg, cachedAuth.cfg)
	assert.Equal(t, 5*time.Minute, cachedAuth.refreshBuffer)
	assert.Nil(t, cachedAuth.cachedConfig)
	assert.True(t, cachedAuth.expiresAt.IsZero())
}

func TestCachedAWSAuth_CalculateExpiry(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectedDelta  time.Duration
	}{
		{
			name:          "Keys method - 1 hour",
			method:        AuthMethodKeys,
			expectedDelta: 1 * time.Hour,
		},
		{
			name:          "Role method - 45 minutes",
			method:        AuthMethodRole,
			expectedDelta: 45 * time.Minute,
		},
		{
			name:          "WebID method - 45 minutes",
			method:        AuthMethodWebID,
			expectedDelta: 45 * time.Minute,
		},
		{
			name:          "IAM method - 30 minutes",
			method:        AuthMethodIAM,
			expectedDelta: 30 * time.Minute,
		},
		{
			name:          "Unknown method - 30 minutes (default)",
			method:        "unknown",
			expectedDelta: 30 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cachedAuth := &CachedAWSAuth{
				cfg: AuthConfig{
					Method: tt.method,
				},
			}

			before := time.Now()
			expiry := cachedAuth.calculateExpiry()
			after := time.Now()

			// Verify expiry is approximately the expected duration from now
			expectedExpiry := before.Add(tt.expectedDelta)
			assert.True(t, expiry.After(expectedExpiry.Add(-time.Second)))
			assert.True(t, expiry.Before(after.Add(tt.expectedDelta).Add(time.Second)))
		})
	}
}

func TestCachedAWSAuth_GetConfig_FirstCall(t *testing.T) {
	// Setup mock
	mockLoader := &mockConfigLoader{
		loadFunc: func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
			return aws.Config{Region: "us-east-1"}, nil
		},
	}
	originalLoader := configLoader
	configLoader = mockLoader
	defer func() { configLoader = originalLoader }()

	cachedAuth := NewCachedAWSAuth(AuthConfig{
		Region: "us-east-1",
		Method: AuthMethodIAM,
	})

	cfg, err := cachedAuth.GetConfig(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "us-east-1", cfg.Region)
	assert.NotNil(t, cachedAuth.cachedConfig)
	assert.False(t, cachedAuth.expiresAt.IsZero())
	assert.Equal(t, 1, mockLoader.getCallCount())
}

func TestCachedAWSAuth_GetConfig_UsesCache(t *testing.T) {
	// Setup mock
	mockLoader := &mockConfigLoader{
		loadFunc: func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
			return aws.Config{Region: "us-east-1"}, nil
		},
	}
	originalLoader := configLoader
	configLoader = mockLoader
	defer func() { configLoader = originalLoader }()

	cachedAuth := NewCachedAWSAuth(AuthConfig{
		Region: "us-east-1",
		Method: AuthMethodKeys, // Keys method has 1 hour expiry
	})

	// First call - loads from AWS
	_, err := cachedAuth.GetConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, mockLoader.getCallCount())

	// Second call - should use cache
	_, err = cachedAuth.GetConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, mockLoader.getCallCount(), "Should use cached config, not call loader again")
}

func TestCachedAWSAuth_GetConfig_RefreshesWhenExpired(t *testing.T) {
	// Setup mock
	mockLoader := &mockConfigLoader{
		loadFunc: func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
			return aws.Config{Region: "us-east-1"}, nil
		},
	}
	originalLoader := configLoader
	configLoader = mockLoader
	defer func() { configLoader = originalLoader }()

	cachedAuth := NewCachedAWSAuth(AuthConfig{
		Region: "us-east-1",
		Method: AuthMethodIAM,
	})
	// Set very short refresh buffer to force refresh
	cachedAuth.refreshBuffer = 1 * time.Hour

	// First call
	_, err := cachedAuth.GetConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, mockLoader.getCallCount())

	// Manually expire the cache
	cachedAuth.expiresAt = time.Now().Add(-10 * time.Minute)

	// Second call - should refresh
	_, err = cachedAuth.GetConfig(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, mockLoader.getCallCount(), "Should refresh expired config")
}

func TestCachedAWSAuth_GetConfig_ConcurrentAccess(t *testing.T) {
	// Setup mock with delay to simulate slow AWS calls
	callCount := 0
	var mu sync.Mutex
	mockLoader := &mockConfigLoader{
		loadFunc: func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
			time.Sleep(50 * time.Millisecond) // Simulate slow call
			mu.Lock()
			callCount++
			mu.Unlock()
			return aws.Config{Region: "us-east-1"}, nil
		},
	}
	originalLoader := configLoader
	configLoader = mockLoader
	defer func() { configLoader = originalLoader }()

	cachedAuth := NewCachedAWSAuth(AuthConfig{
		Region: "us-east-1",
		Method: AuthMethodKeys,
	})

	// Make concurrent calls
	var wg sync.WaitGroup
	numGoroutines := 10
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cachedAuth.GetConfig(context.Background())
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check no errors occurred
	for err := range errors {
		t.Errorf("Unexpected error: %v", err)
	}

	// Verify that Load was called only once (or very few times due to race)
	// despite 10 concurrent requests
	mu.Lock()
	finalCallCount := callCount
	mu.Unlock()
	assert.LessOrEqual(t, finalCallCount, 2, "Should avoid calling Load multiple times concurrently")
}

func TestCachedAWSAuth_GetConfig_DoubleCheckLocking(t *testing.T) {
	// Setup mock with longer delay to ensure goroutines pile up
	mockLoader := &mockConfigLoader{
		loadFunc: func(ctx context.Context, optFns ...func(*config.LoadOptions) error) (aws.Config, error) {
			time.Sleep(50 * time.Millisecond)
			return aws.Config{Region: "us-east-1"}, nil
		},
	}
	originalLoader := configLoader
	configLoader = mockLoader
	defer func() { configLoader = originalLoader }()

	cachedAuth := NewCachedAWSAuth(AuthConfig{
		Region: "us-east-1",
		Method: AuthMethodKeys,
	})

	// Set refresh buffer shorter than cache duration (Keys = 1 hour)
	cachedAuth.refreshBuffer = 30 * time.Minute

	// First call to populate cache
	_, err := cachedAuth.GetConfig(context.Background())
	require.NoError(t, err)
	firstCallCount := mockLoader.getCallCount()

	// Manually set expiry to past
	cachedAuth.mutex.Lock()
	cachedAuth.expiresAt = time.Now().Add(-1 * time.Hour)
	cachedAuth.mutex.Unlock()

	// Make concurrent calls when cache is expired
	var wg sync.WaitGroup
	numGoroutines := 5

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = cachedAuth.GetConfig(context.Background())
		}()
	}

	wg.Wait()

	// With double-check locking, only one goroutine should refresh
	// Total calls should be initial + 1 refresh
	finalCallCount := mockLoader.getCallCount()
	assert.Equal(t, firstCallCount+1, finalCallCount, "Double-check locking should prevent multiple refreshes")
}
