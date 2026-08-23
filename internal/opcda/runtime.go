package opcda

import (
	"context"
	"fmt"
)

// Runtime is a deliberately DA-specific frontend boundary. It is not a
// generic industrial-source abstraction: every operation and result preserves
// OPC DA concepts.
type Runtime interface {
	Status(context.Context) RuntimeStatus
	Browse(context.Context, BrowseRequest) (BrowseResult, error)
	ReadBatch(context.Context, ReadRequest) ([]ReadResult, error)
	WriteBatch(context.Context, []WriteItem) ([]WriteResult, error)
	Shutdown(context.Context) error
}

type Config struct {
	Source       SourceConfig
	WriteEnabled bool
	Limits       Limits
}

type Limits struct {
	CommandQueue       int
	MaxReadItems       int
	MaxWriteItems      int
	MaxBrowseEntries   int
	MaxRegisteredItems int
	MaxItemIDBytes     int
	MaxBSTRCodeUnits   int
}

func DefaultLimits() Limits {
	return Limits{
		CommandQueue:       64,
		MaxReadItems:       100,
		MaxWriteItems:      100,
		MaxBrowseEntries:   1000,
		MaxRegisteredItems: 1024,
		MaxItemIDBytes:     1024,
		MaxBSTRCodeUnits:   65536,
	}
}

func (limits Limits) validate() error {
	if limits.CommandQueue <= 0 ||
		limits.MaxReadItems <= 0 ||
		limits.MaxWriteItems <= 0 ||
		limits.MaxBrowseEntries <= 0 ||
		limits.MaxRegisteredItems <= 0 ||
		limits.MaxItemIDBytes <= 0 ||
		limits.MaxBSTRCodeUnits <= 0 {
		return fmt.Errorf("all DA runtime limits must be positive")
	}
	return nil
}
