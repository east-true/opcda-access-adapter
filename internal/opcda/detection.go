package opcda

import (
	"context"
	"fmt"
	"sort"
)

// LocalDetectionLimits bound registration enumeration independently from the
// DA runtime. Detection reads registration metadata only and never activates a
// detected vendor server.
type LocalDetectionLimits struct {
	MaxServers         int
	MaxProgIDCodeUnits int
}

const (
	OPCDAServer20CategoryName = "OPC_DA_20"
	OPCDAServer20CategoryID   = "{63D5F432-CFE4-11D1-B2C8-0060083BA1FB}"

	defaultMaxDetectedServers = 256
	defaultMaxProgIDCodeUnits = 1024
	maximumDetectedServers    = 4096
	maximumProgIDCodeUnits    = 65536
)

func DefaultLocalDetectionLimits() LocalDetectionLimits {
	return LocalDetectionLimits{
		MaxServers:         defaultMaxDetectedServers,
		MaxProgIDCodeUnits: defaultMaxProgIDCodeUnits,
	}
}

func (limits LocalDetectionLimits) withDefaults() LocalDetectionLimits {
	if limits.MaxServers == 0 {
		limits.MaxServers = defaultMaxDetectedServers
	}
	if limits.MaxProgIDCodeUnits == 0 {
		limits.MaxProgIDCodeUnits = defaultMaxProgIDCodeUnits
	}
	return limits
}

// Validate rejects unbounded or nonsensical detector settings before COM work
// starts.
func (limits LocalDetectionLimits) Validate() error {
	limits = limits.withDefaults()
	if limits.MaxServers <= 0 || limits.MaxProgIDCodeUnits <= 0 {
		return fmt.Errorf("local detection limits must be positive")
	}
	if limits.MaxServers > maximumDetectedServers || limits.MaxProgIDCodeUnits > maximumProgIDCodeUnits {
		return fmt.Errorf("local detection limit exceeds the hard ceiling")
	}
	return nil
}

// DetectedLocalServer is one COM class registered in the OPC DA 2.0 component
// category. Registration is not proof that activation or DA operations will
// succeed. ProgID is omitted when the registration has no resolvable ProgID;
// CLSID remains the exact usable source identifier.
type DetectedLocalServer struct {
	CLSID  string
	ProgID string
}

// DetectLocalServers enumerates only local OPC DA 2.0 COM registrations. It
// does not accept a machine name, activate a detected server, select a source,
// or modify runtime configuration.
func DetectLocalServers(ctx context.Context, limits LocalDetectionLimits) ([]DetectedLocalServer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limits = limits.withDefaults()
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	servers, err := detectLocalServers(ctx, limits)
	if err != nil {
		return nil, err
	}
	if len(servers) > limits.MaxServers {
		return nil, NewAdapterError(CodeDetectionResultLimitExceeded, "local OPC DA detection result limit exceeded")
	}
	sortDetectedLocalServers(servers)
	return servers, nil
}

func sortDetectedLocalServers(servers []DetectedLocalServer) {
	sort.Slice(servers, func(left, right int) bool {
		if servers[left].ProgID != servers[right].ProgID {
			return servers[left].ProgID < servers[right].ProgID
		}
		return servers[left].CLSID < servers[right].CLSID
	})
}
