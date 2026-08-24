package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

const (
	ConfigFileVersion       = 2
	legacyConfigFileVersion = 1
	MaximumConfigFileBytes  = 64 << 10
	maximumConfigPathBytes  = 4096
)

var ErrConfigFileExists = errors.New("configuration file already exists")

type persistedConfig struct {
	Version      int               `json:"version"`
	Source       persistedSource   `json:"source"`
	Frontend     persistedFrontend `json:"frontend"`
	WriteEnabled bool              `json:"writeEnabled"`
}

type persistedSource struct {
	ProgID string `json:"progId,omitempty"`
	CLSID  string `json:"clsid,omitempty"`
}

type persistedFrontend struct {
	Type       string `json:"type"`
	HTTPListen string `json:"httpListen,omitempty"`
	GRPCListen string `json:"grpcListen,omitempty"`
}

// GuidedSetupConfig returns the conservative v0 defaults with exactly one
// explicitly selected local OPC DA source and the HTTP frontend settings that
// the operator chose. It does not activate the source.
func GuidedSetupConfig(source opcda.SourceConfig, httpListen string, writeEnabled bool) (Config, error) {
	return GuidedSetupFrontendConfig(source, FrontendHTTP, httpListen, writeEnabled)
}

// GuidedSetupFrontendConfig returns conservative settings for one explicitly
// selected DA source and one explicitly selected access frontend.
func GuidedSetupFrontendConfig(source opcda.SourceConfig, frontend FrontendType, listen string, writeEnabled bool) (Config, error) {
	config := DefaultConfig()
	config.Source = source
	config.Frontend = frontend
	switch frontend {
	case FrontendHTTP:
		config.HTTPListenAddress = listen
	case FrontendGRPC:
		config.GRPCListenAddress = listen
	default:
		return Config{}, fmt.Errorf("guided setup frontend must be http or grpc")
	}
	config.WriteEnabled = writeEnabled
	if source.ProgID == "" && source.CLSID == "" {
		return Config{}, fmt.Errorf("guided setup requires one explicit OPC DA source")
	}
	if err := config.finalizeAndValidate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// LoadConfigFile reads a small, strict setup configuration. Environment
// variables are deliberately not merged: a service must run the exact file
// that the operator reviewed rather than inherit ambient process settings.
func LoadConfigFile(path string) (Config, error) {
	if err := ValidateConfigFilePath(path); err != nil {
		return Config{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration file: %w", err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, MaximumConfigFileBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read configuration file: %w", err)
	}
	if len(data) > MaximumConfigFileBytes {
		return Config{}, fmt.Errorf("configuration file exceeds %d bytes", MaximumConfigFileBytes)
	}
	if err := validateConfigJSONStructure(data); err != nil {
		return Config{}, fmt.Errorf("validate configuration JSON: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var stored persistedConfig
	if err := decoder.Decode(&stored); err != nil {
		return Config{}, fmt.Errorf("decode configuration file: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Config{}, err
	}
	if stored.Version != legacyConfigFileVersion && stored.Version != ConfigFileVersion {
		return Config{}, fmt.Errorf("unsupported configuration version %d", stored.Version)
	}
	frontend := FrontendType(stored.Frontend.Type)
	listen := ""
	switch frontend {
	case FrontendHTTP:
		if stored.Frontend.HTTPListen == "" || stored.Frontend.GRPCListen != "" {
			return Config{}, fmt.Errorf("HTTP frontend requires only httpListen")
		}
		listen = stored.Frontend.HTTPListen
	case FrontendGRPC:
		if stored.Version == legacyConfigFileVersion {
			return Config{}, fmt.Errorf("configuration version 1 supports only HTTP")
		}
		if stored.Frontend.GRPCListen == "" || stored.Frontend.HTTPListen != "" {
			return Config{}, fmt.Errorf("gRPC frontend requires only grpcListen")
		}
		listen = stored.Frontend.GRPCListen
	default:
		return Config{}, fmt.Errorf("unsupported frontend %q", stored.Frontend.Type)
	}
	return GuidedSetupFrontendConfig(
		opcda.SourceConfig{ProgID: stored.Source.ProgID, CLSID: stored.Source.CLSID},
		frontend,
		listen,
		stored.WriteEnabled,
	)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing configuration data: %w", err)
	}
	return fmt.Errorf("configuration file contains more than one JSON value")
}

func validateConfigJSONStructure(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanConfigJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("configuration must contain exactly one JSON value")
}

func scanConfigJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 4 {
		return fmt.Errorf("configuration nesting exceeds four levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	if delimiter == '{' {
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("configuration object key is not a string")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("configuration contains duplicate field %q", key)
			}
			keys[key] = struct{}{}
			if err := scanConfigJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	} else {
		for decoder.More() {
			if err := scanConfigJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	expected := json.Delim('}')
	if delimiter == '[' {
		expected = ']'
	}
	if closing != expected {
		return fmt.Errorf("unexpected JSON closing delimiter %q", closing)
	}
	return nil
}

// WriteConfigFileExclusive writes a validated setup configuration without
// replacing an existing path. Refusing implicit overwrite keeps repeated
// setup runs from silently changing the source served by an installed adapter.
func WriteConfigFileExclusive(path string, config Config) error {
	if err := ValidateConfigFilePath(path); err != nil {
		return err
	}
	if config.Source.ProgID == "" && config.Source.CLSID == "" {
		return fmt.Errorf("configuration requires one explicit OPC DA source")
	}
	if err := config.finalizeAndValidate(); err != nil {
		return err
	}
	stored := persistedConfig{
		Version: ConfigFileVersion,
		Source: persistedSource{
			ProgID: config.Source.ProgID,
			CLSID:  config.Source.CLSID,
		},
		Frontend: persistedFrontend{
			Type: string(config.Frontend),
		},
		WriteEnabled: config.WriteEnabled,
	}
	switch config.Frontend {
	case FrontendHTTP:
		stored.Frontend.HTTPListen = config.HTTPListenAddress
	case FrontendGRPC:
		stored.Frontend.GRPCListen = config.GRPCListenAddress
	default:
		return fmt.Errorf("unsupported frontend %q", config.Frontend)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrConfigFileExists
		}
		return fmt.Errorf("create configuration: %w", err)
	}
	createdInfo, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return fmt.Errorf("inspect new configuration: %w", statErr)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		pathInfo, err := os.Lstat(path)
		if err == nil && os.SameFile(createdInfo, pathInfo) {
			_ = os.Remove(path)
		}
	}()

	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("restrict configuration permissions: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(stored); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode configuration: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync configuration: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close configuration: %w", err)
	}
	committed = true
	return nil
}

// ValidateConfigFilePath bounds a setup path before detection or filesystem
// work. OS-specific path and access checks still occur when the file is used.
func ValidateConfigFilePath(path string) error {
	if path == "" {
		return fmt.Errorf("configuration path is required")
	}
	if len(path) > maximumConfigPathBytes {
		return fmt.Errorf("configuration path exceeds %d bytes", maximumConfigPathBytes)
	}
	return nil
}
