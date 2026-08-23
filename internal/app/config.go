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
		Runtime: opcda.Config{
			Limits:           limits,
			ReconnectInitial: time.Second,
			ReconnectMax:     30 * time.Second,
			COMCallWatchdog:  30 * time.Second,
		},
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
	if config.Runtime.ReconnectInitial, err = durationEnv("OPCDA_RECONNECT_INITIAL", config.Runtime.ReconnectInitial); err != nil {
		return Config{}, err
	}
	if config.Runtime.ReconnectMax, err = durationEnv("OPCDA_RECONNECT_MAX", config.Runtime.ReconnectMax); err != nil {
		return Config{}, err
	}
	if config.Runtime.COMCallWatchdog, err = durationEnv("OPCDA_COM_CALL_WATCHDOG", config.Runtime.COMCallWatchdog); err != nil {
		return Config{}, err
	}
	limitSettings := []struct {
		key    string
		target *int
	}{
		{key: "OPCDA_COMMAND_QUEUE", target: &config.Runtime.Limits.CommandQueue},
		{key: "OPCDA_MAX_READ_ITEMS", target: &config.Runtime.Limits.MaxReadItems},
		{key: "OPCDA_MAX_WRITE_ITEMS", target: &config.Runtime.Limits.MaxWriteItems},
		{key: "OPCDA_MAX_BROWSE_ENTRIES", target: &config.Runtime.Limits.MaxBrowseEntries},
		{key: "OPCDA_MAX_BROWSE_DEPTH", target: &config.Runtime.Limits.MaxBrowseDepth},
		{key: "OPCDA_MAX_REGISTERED_ITEMS", target: &config.Runtime.Limits.MaxRegisteredItems},
		{key: "OPCDA_MAX_ITEM_ID_BYTES", target: &config.Runtime.Limits.MaxItemIDBytes},
		{key: "OPCDA_MAX_BSTR_CODE_UNITS", target: &config.Runtime.Limits.MaxBSTRCodeUnits},
	}
	for _, setting := range limitSettings {
		if *setting.target, err = intEnv(setting.key, *setting.target); err != nil {
			return Config{}, err
		}
	}

	if config.Source.ProgID != "" && config.Source.CLSID != "" {
		return Config{}, fmt.Errorf("set exactly one of OPCDA_SOURCE_PROG_ID and OPCDA_SOURCE_CLSID")
	}
	if config.MaxHTTPBodyBytes <= 0 || config.MaxConcurrentRequests <= 0 || config.RequestDeadline <= 0 {
		return Config{}, fmt.Errorf("HTTP body, concurrency, and deadline limits must be positive")
	}
	if config.MaxHTTPBodyBytes > 64<<20 || config.MaxConcurrentRequests > 1024 || config.RequestDeadline > 24*time.Hour {
		return Config{}, fmt.Errorf("HTTP body, concurrency, or deadline exceeds the v0 hard ceiling")
	}
	if config.Runtime.ReconnectInitial <= 0 || config.Runtime.ReconnectMax <= 0 || config.Runtime.COMCallWatchdog <= 0 {
		return Config{}, fmt.Errorf("reconnect and COM watchdog durations must be positive")
	}
	if config.Runtime.ReconnectInitial > config.Runtime.ReconnectMax {
		return Config{}, fmt.Errorf("OPCDA_RECONNECT_INITIAL must not exceed OPCDA_RECONNECT_MAX")
	}
	for _, setting := range limitSettings {
		if *setting.target <= 0 {
			return Config{}, fmt.Errorf("%s must be positive", setting.key)
		}
	}
	if err := config.Runtime.Limits.ValidateForConfiguration(); err != nil {
		return Config{}, err
	}

	config.Runtime.Source = config.Source
	config.Runtime.WriteEnabled = config.WriteEnabled
	if err := config.Runtime.ValidateForConfiguration(); err != nil {
		return Config{}, err
	}
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
