package app

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

type Config struct {
	Source                opcda.SourceConfig
	HTTPListenAddress     string
	WriteEnabled          bool
	MaxHTTPBodyBytes      int64
	MaxConcurrentRequests int
	RequestDeadline       time.Duration
	Runtime               opcda.Config
}

func DefaultConfig() Config {
	limits := opcda.DefaultLimits()
	return Config{
		HTTPListenAddress:     "127.0.0.1:8080",
		MaxHTTPBodyBytes:      1 << 20,
		MaxConcurrentRequests: 32,
		RequestDeadline:       10 * time.Second,
		Runtime:               opcda.Config{Limits: limits},
	}
}

// LoadConfig reads only bounded, explicit v0 settings. It intentionally has no
// configuration surface for sources other than the single local OPC DA server.
func LoadConfig() (Config, error) {
	config := DefaultConfig()
	config.Source.ProgID = os.Getenv("OPCDA_SOURCE_PROG_ID")
	config.Source.CLSID = os.Getenv("OPCDA_SOURCE_CLSID")
	config.HTTPListenAddress = valueOrDefault("OPCDA_HTTP_LISTEN", config.HTTPListenAddress)

	var err error
	if config.WriteEnabled, err = boolEnv("OPCDA_WRITE_ENABLED", false); err != nil {
		return Config{}, err
	}
	if config.MaxHTTPBodyBytes, err = int64Env("OPCDA_MAX_HTTP_BODY_BYTES", config.MaxHTTPBodyBytes); err != nil {
		return Config{}, err
	}
	if config.MaxConcurrentRequests, err = intEnv("OPCDA_MAX_CONCURRENT_REQUESTS", config.MaxConcurrentRequests); err != nil {
		return Config{}, err
	}
	if config.RequestDeadline, err = durationEnv("OPCDA_REQUEST_DEADLINE", config.RequestDeadline); err != nil {
		return Config{}, err
	}

	if config.Source.ProgID != "" && config.Source.CLSID != "" {
		return Config{}, fmt.Errorf("set exactly one of OPCDA_SOURCE_PROG_ID and OPCDA_SOURCE_CLSID")
	}
	if config.MaxHTTPBodyBytes <= 0 || config.MaxConcurrentRequests <= 0 || config.RequestDeadline <= 0 {
		return Config{}, fmt.Errorf("HTTP body, concurrency, and deadline limits must be positive")
	}

	config.Runtime.Source = config.Source
	config.Runtime.WriteEnabled = config.WriteEnabled
	return config, nil
}

func valueOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func boolEnv(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func intEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func int64Env(key string, fallback int64) (int64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return parsed, nil
}
