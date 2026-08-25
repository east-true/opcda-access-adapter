package opcda

import (
	"context"
	"fmt"
	"time"
)

// Runtime is a deliberately DA-specific frontend boundary. It is not a
// generic industrial-source abstraction: every operation and result preserves
// OPC DA concepts.
type Runtime interface {
	Status(context.Context) RuntimeStatus
	Browse(context.Context, BrowseRequest) (BrowseResult, error)
	ReadBatch(context.Context, ReadRequest) ([]ReadResult, error)
	WriteBatch(context.Context, []WriteItem) ([]WriteResult, error)
	Subscribe(context.Context, SubscribeRequest) (Subscription, error)
	Unsubscribe(context.Context, SubscriptionID) error
	Shutdown(context.Context) error
}

type Config struct {
	Source           SourceConfig
	WriteEnabled     bool
	Limits           Limits
	ReconnectInitial time.Duration
	ReconnectMax     time.Duration
	COMCallWatchdog  time.Duration
}

const (
	defaultReconnectInitial        = time.Second
	defaultReconnectMax            = 30 * time.Second
	defaultCOMCallWatchdog         = 30 * time.Second
	maximumRuntimeDuration         = 24 * time.Hour
	maximumBatchBSTRUnits          = uint64(8 << 20)
	maximumBrowseBSTRUnits         = uint64(128 << 20)
	maximumBatchItemIDBytes        = uint64(64 << 20)
	maximumCacheItemIDBytes        = uint64(128 << 20)
	maximumSubscriptionBSTRUnits   = uint64(128 << 20)
	maximumSubscriptionItemIDBytes = uint64(64 << 20)
)

func (config Config) withDefaults() Config {
	if config.ReconnectInitial == 0 {
		config.ReconnectInitial = defaultReconnectInitial
	}
	if config.ReconnectMax == 0 {
		config.ReconnectMax = defaultReconnectMax
	}
	if config.COMCallWatchdog == 0 {
		config.COMCallWatchdog = defaultCOMCallWatchdog
	}
	return config
}

func (config Config) validate() error {
	if err := config.Limits.validate(); err != nil {
		return err
	}
	if config.ReconnectInitial <= 0 || config.ReconnectMax <= 0 || config.COMCallWatchdog <= 0 {
		return fmt.Errorf("reconnect and COM watchdog durations must be positive")
	}
	if config.ReconnectInitial > config.ReconnectMax {
		return fmt.Errorf("reconnect initial delay must not exceed maximum delay")
	}
	if config.ReconnectMax > maximumRuntimeDuration || config.COMCallWatchdog > maximumRuntimeDuration {
		return fmt.Errorf("reconnect maximum and COM watchdog must not exceed 24 hours")
	}
	return nil
}

// ValidateForConfiguration applies runtime defaults and validates startup
// bounds without allocating COM or queue resources.
func (config Config) ValidateForConfiguration() error {
	return config.withDefaults().validate()
}

type Limits struct {
	CommandQueue         int
	MaxReadItems         int
	MaxWriteItems        int
	MaxBrowseEntries     int
	MaxBrowseDepth       int
	MaxRegisteredItems   int
	MaxItemIDBytes       int
	MaxBSTRCodeUnits     int
	MaxSubscriptions     int
	MaxSubscriptionItems int
}

func DefaultLimits() Limits {
	return Limits{
		CommandQueue:         64,
		MaxReadItems:         100,
		MaxWriteItems:        100,
		MaxBrowseEntries:     1000,
		MaxBrowseDepth:       64,
		MaxRegisteredItems:   1024,
		MaxItemIDBytes:       1024,
		MaxBSTRCodeUnits:     65536,
		MaxSubscriptions:     16,
		MaxSubscriptionItems: 100,
	}
}

func (limits Limits) validate() error {
	if limits.CommandQueue <= 0 ||
		limits.MaxReadItems <= 0 ||
		limits.MaxWriteItems <= 0 ||
		limits.MaxBrowseEntries <= 0 ||
		limits.MaxBrowseDepth <= 0 ||
		limits.MaxRegisteredItems <= 0 ||
		limits.MaxItemIDBytes <= 0 ||
		limits.MaxBSTRCodeUnits <= 0 ||
		limits.MaxSubscriptions <= 0 ||
		limits.MaxSubscriptionItems <= 0 {
		return fmt.Errorf("all DA runtime limits must be positive")
	}
	if limits.CommandQueue > 4096 ||
		limits.MaxReadItems > 10000 ||
		limits.MaxWriteItems > 10000 ||
		limits.MaxBrowseEntries > 100000 ||
		limits.MaxBrowseDepth > 256 ||
		limits.MaxRegisteredItems > 1000000 ||
		limits.MaxItemIDBytes > 65536 ||
		limits.MaxBSTRCodeUnits > 1048576 ||
		limits.MaxSubscriptions > 256 ||
		limits.MaxSubscriptionItems > 10000 {
		return fmt.Errorf("one or more DA runtime limits exceed the v0 hard ceiling")
	}
	if uint64(limits.MaxReadItems)*uint64(limits.MaxBSTRCodeUnits) > maximumBatchBSTRUnits ||
		uint64(limits.MaxWriteItems)*uint64(limits.MaxBSTRCodeUnits) > maximumBatchBSTRUnits {
		return fmt.Errorf("configured batch BSTR budget exceeds the v0 hard ceiling")
	}
	// A Browse item can retain both the enumerated name and exact ItemID.
	if uint64(limits.MaxBrowseEntries)*uint64(limits.MaxBSTRCodeUnits)*2 > maximumBrowseBSTRUnits {
		return fmt.Errorf("configured Browse BSTR budget exceeds the v0 hard ceiling")
	}
	if uint64(limits.MaxReadItems)*uint64(limits.MaxItemIDBytes) > maximumBatchItemIDBytes ||
		uint64(limits.MaxWriteItems)*uint64(limits.MaxItemIDBytes) > maximumBatchItemIDBytes {
		return fmt.Errorf("configured batch ItemID budget exceeds the v0 hard ceiling")
	}
	if uint64(limits.MaxRegisteredItems)*uint64(limits.MaxItemIDBytes) > maximumCacheItemIDBytes {
		return fmt.Errorf("configured registration-cache ItemID budget exceeds the v0 hard ceiling")
	}
	// Each subscription retains at most one pending value per active item.
	if uint64(limits.MaxSubscriptions)*uint64(limits.MaxSubscriptionItems)*uint64(limits.MaxBSTRCodeUnits) > maximumSubscriptionBSTRUnits {
		return fmt.Errorf("configured subscription pending-value budget exceeds the v0 hard ceiling")
	}
	if uint64(limits.MaxSubscriptions)*uint64(limits.MaxSubscriptionItems)*uint64(limits.MaxItemIDBytes) > maximumSubscriptionItemIDBytes {
		return fmt.Errorf("configured subscription ItemID budget exceeds the v0 hard ceiling")
	}
	return nil
}

// ValidateForConfiguration lets the application reject unsafe environment
// settings before allocating bounded runtime structures.
func (limits Limits) ValidateForConfiguration() error {
	return limits.validate()
}
