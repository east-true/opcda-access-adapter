package opcda

import "time"

// DAItemID is the exact source identifier. No trimming, case conversion, or
// delimiter normalization is allowed anywhere in the adapter.
type DAItemID string

type DADataSource string

const (
	DADataSourceDevice DADataSource = "device"
	DADataSourceCache  DADataSource = "cache"
)

type DAAccessRights struct {
	Raw   uint32 `json:"raw"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
}

// DAValue represents a scalar value copied out of a COM VARIANT. Value holds
// a width-preserving Go scalar (for example int16, float32, uint64, string),
// never a normalized common model value.
type DAValue struct {
	ItemID           DAItemID
	VarType          DAVarType
	Value            any
	QualityRaw       uint16
	Timestamp        time.Time
	TimestampPresent bool
	HRESULT          HRESULT
	AccessRights     *DAAccessRights
}

type ReadRequest struct {
	Items  []DAItemID
	Source DADataSource
}

type ReadResult struct {
	ItemID         DAItemID
	Value          *DAValue
	VarType        *DAVarType
	CanonicalType  *DAVarType
	AccessRights   *DAAccessRights
	HRESULT        HRESULT
	HRESULTPresent bool
	ErrorCode      string
}

type BrowseFilter string

const (
	BrowseFilterAll    BrowseFilter = "all"
	BrowseFilterBranch BrowseFilter = "branch"
	BrowseFilterItem   BrowseFilter = "item"
)

type BrowseRequest struct {
	Path   []string
	Filter BrowseFilter
}

type BrowseEntryKind string

const (
	BrowseEntryBranch BrowseEntryKind = "branch"
	BrowseEntryItem   BrowseEntryKind = "item"
)

type BrowseEntry struct {
	Kind          BrowseEntryKind
	Name          string
	ItemID        *DAItemID
	CanonicalType *DAVarType
	AccessRights  *DAAccessRights
}

type BrowseResult struct {
	Path    []string
	Entries []BrowseEntry
}

type WriteItem struct {
	ItemID  DAItemID
	VarType DAVarType
	Value   any
}

type WriteResult struct {
	ItemID         DAItemID
	HRESULT        HRESULT
	HRESULTPresent bool
	ErrorCode      string
}
