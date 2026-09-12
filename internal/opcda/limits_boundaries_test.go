package opcda

import "testing"

// Limits.validate is what stands between a misconfiguration and a runtime that
// will accept work it cannot bound. ADR-0001 records every ceiling in it, and
// the mutation sweep found almost none of them pinned: loosening a ceiling
// comparison by one, or relaxing a positivity check to allow zero, survived
// across the whole validator. What existed tested the far side of each limit --
// MaxReadItems = 129 is refused -- and never the limit itself, so nothing said
// that 128 is still allowed.
//
// The `||` chains have a second failure the same sweep found. Each block is one
// long disjunction, so a test that sets several fields wrong at once proves
// nothing about any of them: turning an `||` into `&&` still fails such a test
// for the wrong reason. Every case here moves exactly one field.

// limitField names a bounded field, the ceiling ADR-0001 gives it, and how to
// set it. Reading them from one table is what makes "every field" checkable
// rather than "the fields somebody listed".
type limitField struct {
	name    string
	ceiling int
	set     func(*Limits, int)
}

var limitFields = []limitField{
	{"CommandQueue", 4096, func(l *Limits, v int) { l.CommandQueue = v }},
	{"MaxReadItems", 10000, func(l *Limits, v int) { l.MaxReadItems = v }},
	{"MaxWriteItems", 10000, func(l *Limits, v int) { l.MaxWriteItems = v }},
	{"MaxBrowseEntries", 100000, func(l *Limits, v int) { l.MaxBrowseEntries = v }},
	{"MaxBrowseDepth", 256, func(l *Limits, v int) { l.MaxBrowseDepth = v }},
	{"MaxRegisteredItems", 1000000, func(l *Limits, v int) { l.MaxRegisteredItems = v }},
	{"MaxItemIDBytes", 65536, func(l *Limits, v int) { l.MaxItemIDBytes = v }},
	{"MaxBSTRCodeUnits", 1048576, func(l *Limits, v int) { l.MaxBSTRCodeUnits = v }},
	{"MaxSubscriptions", 256, func(l *Limits, v int) { l.MaxSubscriptions = v }},
	{"MaxSubscriptionItems", 10000, func(l *Limits, v int) { l.MaxSubscriptionItems = v }},
	{"MaxItemProperties", 1024, func(l *Limits, v int) { l.MaxItemProperties = v }},
}

// minimalLimits is every field at one: valid, and small enough that no
// aggregate budget is anywhere near its ceiling. A field raised to its own
// ceiling from here is testing that ceiling and nothing else.
func minimalLimits() Limits {
	limits := Limits{}
	for _, field := range limitFields {
		field.set(&limits, 1)
	}
	return limits
}

func TestEveryLimitMustBePositive(t *testing.T) {
	for _, field := range limitFields {
		t.Run(field.name, func(t *testing.T) {
			limits := DefaultLimits()
			field.set(&limits, 0)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Errorf("%s = 0 was accepted; every runtime limit must be positive", field.name)
			}
			// Negative is the same rule and is what a misparsed setting looks
			// like, so it is checked rather than assumed to follow.
			field.set(&limits, -1)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Errorf("%s = -1 was accepted", field.name)
			}
		})
	}
}

func TestEveryLimitAcceptsItsCeilingAndRefusesOneMore(t *testing.T) {
	if err := minimalLimits().ValidateForConfiguration(); err != nil {
		t.Fatalf("the minimal limits are not valid, so every case below is "+
			"testing the wrong thing: %v", err)
	}
	for _, field := range limitFields {
		t.Run(field.name, func(t *testing.T) {
			limits := minimalLimits()
			field.set(&limits, field.ceiling)
			if err := limits.ValidateForConfiguration(); err != nil {
				t.Errorf("%s = %d is the documented ceiling and was refused: %v",
					field.name, field.ceiling, err)
			}
			field.set(&limits, field.ceiling+1)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Errorf("%s = %d is one over the ceiling and was accepted",
					field.name, field.ceiling+1)
			}
		})
	}
}

// The aggregate budgets are products, so their boundary is a pair of factors
// rather than a single number. Each case sits exactly on the ceiling and then
// one unit over it, with every other field at one so that no other budget can
// be what refuses the configuration.
func TestEveryAggregateBudgetAcceptsItsCeilingAndRefusesOneMore(t *testing.T) {
	for _, testCase := range []struct {
		name string
		at   func(*Limits)
		over func(*Limits)
	}{
		{
			// MaxReadItems * MaxBSTRCodeUnits == 8 MiB of UTF-16 units.
			name: "batch BSTR budget, read side",
			at:   func(l *Limits) { l.MaxBSTRCodeUnits = 1024; l.MaxReadItems = 8192 },
			over: func(l *Limits) { l.MaxBSTRCodeUnits = 1024; l.MaxReadItems = 8193 },
		},
		{
			name: "batch BSTR budget, write side",
			at:   func(l *Limits) { l.MaxBSTRCodeUnits = 1024; l.MaxWriteItems = 8192 },
			over: func(l *Limits) { l.MaxBSTRCodeUnits = 1024; l.MaxWriteItems = 8193 },
		},
		{
			// MaxBrowseEntries * MaxBSTRCodeUnits * 2 == 128 MiB: a Browse
			// entry can retain both the enumerated name and the exact ItemID.
			name: "Browse BSTR budget",
			at:   func(l *Limits) { l.MaxBSTRCodeUnits = 1024; l.MaxBrowseEntries = 65536 },
			over: func(l *Limits) { l.MaxBSTRCodeUnits = 1024; l.MaxBrowseEntries = 65537 },
		},
		{
			// MaxReadItems * MaxItemIDBytes == 64 MiB.
			name: "batch ItemID budget, read side",
			at:   func(l *Limits) { l.MaxItemIDBytes = 8192; l.MaxReadItems = 8192 },
			over: func(l *Limits) { l.MaxItemIDBytes = 8192; l.MaxReadItems = 8193 },
		},
		{
			name: "batch ItemID budget, write side",
			at:   func(l *Limits) { l.MaxItemIDBytes = 8192; l.MaxWriteItems = 8192 },
			over: func(l *Limits) { l.MaxItemIDBytes = 8192; l.MaxWriteItems = 8193 },
		},
		{
			// MaxRegisteredItems * MaxItemIDBytes == 128 MiB.
			name: "registration cache ItemID budget",
			at:   func(l *Limits) { l.MaxItemIDBytes = 8192; l.MaxRegisteredItems = 16384 },
			over: func(l *Limits) { l.MaxItemIDBytes = 8192; l.MaxRegisteredItems = 16385 },
		},
		{
			// MaxSubscriptions * MaxSubscriptionItems * MaxBSTRCodeUnits == 128 MiB.
			name: "subscription pending-value budget",
			at:   func(l *Limits) { l.MaxBSTRCodeUnits = 1024; l.MaxSubscriptions = 128; l.MaxSubscriptionItems = 1024 },
			over: func(l *Limits) { l.MaxBSTRCodeUnits = 1024; l.MaxSubscriptions = 128; l.MaxSubscriptionItems = 1025 },
		},
		{
			// MaxSubscriptions * MaxSubscriptionItems * MaxItemIDBytes == 64 MiB.
			name: "subscription ItemID budget",
			at:   func(l *Limits) { l.MaxItemIDBytes = 1024; l.MaxSubscriptions = 64; l.MaxSubscriptionItems = 1024 },
			over: func(l *Limits) { l.MaxItemIDBytes = 1024; l.MaxSubscriptions = 64; l.MaxSubscriptionItems = 1025 },
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			limits := minimalLimits()
			testCase.at(&limits)
			if err := limits.ValidateForConfiguration(); err != nil {
				t.Errorf("a configuration exactly on the budget was refused: %v", err)
			}
			limits = minimalLimits()
			testCase.over(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Error("a configuration one unit over the budget was accepted")
			}
		})
	}
}

// The defaults have to sit inside every rule above. They are what ships, and
// ADR-0001 records them as the v0 bounds.
func TestDefaultLimitsSatisfyTheirOwnValidator(t *testing.T) {
	if err := DefaultLimits().ValidateForConfiguration(); err != nil {
		t.Fatalf("the shipped defaults do not validate: %v", err)
	}
}
