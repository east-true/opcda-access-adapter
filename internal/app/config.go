package app

import (
	"fmt"
	"net"
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
	MaxHTTPConnections    int
	MaxConcurrentRequests int
	MaxHTTPHeaderBytes    int
	MaxJSONDepth          int
	HTTPReadHeaderTimeout time.Duration
	HTTPReadTimeout       time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration
	RequestDeadline       time.Duration
	Runtime               opcda.Config
}

const (
	maximumInflightHTTPBodyBytes = uint64(256 << 20)
	maximumInflightHeaderBytes   = uint64(64 << 20)
)

func DefaultConfig() Config {
	limits := opcda.DefaultLimits()
	return Config{
		HTTPListenAddress:     "127.0.0.1:8080",
		MaxHTTPBodyBytes:      1 << 20,
		MaxHTTPConnections:    64,
		MaxConcurrentRequests: 32,
		MaxHTTPHeaderBytes:    32 << 10,
		MaxJSONDepth:          64,
		HTTPReadHeaderTimeout: 5 * time.Second,
		HTTPReadTimeout:       15 * time.Second,
		HTTPWriteTimeout:      15 * time.Second,
		HTTPIdleTimeout:       30 * time.Second,
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
	if config.MaxHTTPConnections, err = intEnv("OPCDA_MAX_HTTP_CONNECTIONS", config.MaxHTTPConnections); err != nil {
		return Config{}, err
	}
	if config.MaxConcurrentRequests, err = intEnv("OPCDA_MAX_CONCURRENT_REQUESTS", config.MaxConcurrentRequests); err != nil {
		return Config{}, err
	}
	if config.MaxHTTPHeaderBytes, err = intEnv("OPCDA_MAX_HTTP_HEADER_BYTES", config.MaxHTTPHeaderBytes); err != nil {
		return Config{}, err
	}
	if config.MaxJSONDepth, err = intEnv("OPCDA_MAX_JSON_DEPTH", config.MaxJSONDepth); err != nil {
		return Config{}, err
	}
	if config.HTTPReadHeaderTimeout, err = durationEnv("OPCDA_HTTP_READ_HEADER_TIMEOUT", config.HTTPReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if config.HTTPReadTimeout, err = durationEnv("OPCDA_HTTP_READ_TIMEOUT", config.HTTPReadTimeout); err != nil {
		return Config{}, err
	}
	if config.HTTPWriteTimeout, err = durationEnv("OPCDA_HTTP_WRITE_TIMEOUT", config.HTTPWriteTimeout); err != nil {
		return Config{}, err
	}
	if config.HTTPIdleTimeout, err = durationEnv("OPCDA_HTTP_IDLE_TIMEOUT", config.HTTPIdleTimeout); err != nil {
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

	if err := config.finalizeAndValidate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) finalizeAndValidate() error {
	if config.Source.ProgID != "" && config.Source.CLSID != "" {
		return fmt.Errorf("set exactly one of OPCDA_SOURCE_PROG_ID and OPCDA_SOURCE_CLSID")
	}
	if config.MaxHTTPBodyBytes <= 0 || config.MaxHTTPConnections <= 0 || config.MaxConcurrentRequests <= 0 ||
		config.MaxHTTPHeaderBytes <= 0 || config.MaxJSONDepth <= 0 || config.HTTPReadHeaderTimeout <= 0 || config.HTTPReadTimeout <= 0 ||
		config.HTTPWriteTimeout <= 0 || config.HTTPIdleTimeout <= 0 || config.RequestDeadline <= 0 {
		return fmt.Errorf("HTTP bounds and timeouts must be positive")
	}
	if config.MaxHTTPBodyBytes > 64<<20 || config.MaxHTTPConnections > 2048 || config.MaxConcurrentRequests > 1024 ||
		config.MaxHTTPHeaderBytes > 1<<20 || config.MaxJSONDepth > 256 || config.HTTPReadHeaderTimeout > 24*time.Hour ||
		config.HTTPReadTimeout > 24*time.Hour || config.HTTPWriteTimeout > 24*time.Hour ||
		config.HTTPIdleTimeout > 24*time.Hour || config.RequestDeadline > 24*time.Hour {
		return fmt.Errorf("HTTP bound or timeout exceeds the v0 hard ceiling")
	}
	if uint64(config.MaxHTTPBodyBytes)*uint64(config.MaxConcurrentRequests) > maximumInflightHTTPBodyBytes {
		return fmt.Errorf("configured concurrent HTTP body budget exceeds the v0 hard ceiling")
	}
	if uint64(config.MaxHTTPHeaderBytes)*uint64(config.MaxHTTPConnections) > maximumInflightHeaderBytes {
		return fmt.Errorf("configured concurrent HTTP header budget exceeds the v0 hard ceiling")
	}
	_, port, err := net.SplitHostPort(config.HTTPListenAddress)
	if err != nil {
		return fmt.Errorf("OPCDA_HTTP_LISTEN must be a host:port address: %w", err)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("OPCDA_HTTP_LISTEN port must be numeric and between 0 and 65535: %w", err)
	}
	if config.Runtime.ReconnectInitial <= 0 || config.Runtime.ReconnectMax <= 0 || config.Runtime.COMCallWatchdog <= 0 {
		return fmt.Errorf("reconnect and COM watchdog durations must be positive")
	}
	if config.Runtime.ReconnectInitial > config.Runtime.ReconnectMax {
		return fmt.Errorf("OPCDA_RECONNECT_INITIAL must not exceed OPCDA_RECONNECT_MAX")
	}
	if err := config.Runtime.Limits.ValidateForConfiguration(); err != nil {
		return err
	}

	config.Runtime.Source = config.Source
	config.Runtime.WriteEnabled = config.WriteEnabled
	if err := config.Runtime.ValidateForConfiguration(); err != nil {
		return err
	}
	return nil
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
