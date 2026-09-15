package app

import (
	"strings"
	"testing"
	"time"
)

// finalizeAndValidate is where a deployment's configuration is accepted or
// refused, and the sweep found almost all of it unpinned: 61 of 130 mutations
// survived, the worst of any package here. Every bound in it is written as one
// arm of a long disjunction, so loosening any single arm by one leaves the
// chain still refusing everything the existing cases tried. What that means is
// that nothing said a configuration of exactly the documented shape is
// accepted -- only that something clearly outside it is refused.
//
// The two halves matter for different reasons. A ceiling that silently drifted
// outward would let an operator configure a budget the adapter cannot honour;
// a floor that drifted inward would accept a zero or negative bound, and a zero
// body limit or a zero timeout is a frontend that refuses every request while
// reporting itself configured.
//
// Each field is exercised at exactly its floor, exactly its ceiling, and one
// step outside each. Where a field appears in an aggregate budget or in a
// cross-field rule, the entry lowers the other factors first, so the case is
// about this field's own bound rather than about a product tripping first.

type configBound struct {
	name string
	set  func(*Config, int64)
	// isolate lowers whatever else would refuse the value before this field's
	// own bound is reached.
	isolate func(*Config)
	floor   int64
	ceiling int64
	// signed says whether a negative value is expressible in the field's type.
	signed bool
}

func durationBound(set func(*Config, time.Duration)) func(*Config, int64) {
	return func(config *Config, value int64) { set(config, time.Duration(value)) }
}

const oneDay = int64(24 * time.Hour)

func configBounds() []configBound {
	return []configBound{
		{
			name:    "OPCDA_MAX_BODY_BYTES",
			set:     func(c *Config, v int64) { c.MaxHTTPBodyBytes = v },
			isolate: func(c *Config) { c.MaxConcurrentRequests = 1 },
			floor:   1, ceiling: 64 << 20, signed: true,
		},
		{
			name:    "OPCDA_MAX_CONNECTIONS",
			set:     func(c *Config, v int64) { c.MaxHTTPConnections = int(v) },
			isolate: func(c *Config) { c.MaxHTTPHeaderBytes = 1 },
			floor:   1, ceiling: 2048, signed: true,
		},
		{
			name:    "OPCDA_MAX_CONCURRENT_REQUESTS",
			set:     func(c *Config, v int64) { c.MaxConcurrentRequests = int(v) },
			isolate: func(c *Config) { c.MaxHTTPBodyBytes = 1 },
			floor:   1, ceiling: 1024, signed: true,
		},
		{
			name:    "OPCDA_MAX_HEADER_BYTES",
			set:     func(c *Config, v int64) { c.MaxHTTPHeaderBytes = int(v) },
			isolate: func(c *Config) { c.MaxHTTPConnections = 1 },
			floor:   1, ceiling: 1 << 20, signed: true,
		},
		{
			name:  "OPCDA_MAX_JSON_DEPTH",
			set:   func(c *Config, v int64) { c.MaxJSONDepth = int(v) },
			floor: 1, ceiling: 256, signed: true,
		},
		{
			name:  "OPCDA_HTTP_READ_HEADER_TIMEOUT",
			set:   durationBound(func(c *Config, v time.Duration) { c.HTTPReadHeaderTimeout = v }),
			floor: 1, ceiling: oneDay, signed: true,
		},
		{
			name:  "OPCDA_HTTP_READ_TIMEOUT",
			set:   durationBound(func(c *Config, v time.Duration) { c.HTTPReadTimeout = v }),
			floor: 1, ceiling: oneDay, signed: true,
		},
		{
			name:  "OPCDA_HTTP_WRITE_TIMEOUT",
			set:   durationBound(func(c *Config, v time.Duration) { c.HTTPWriteTimeout = v }),
			floor: 1, ceiling: oneDay, signed: true,
		},
		{
			name:  "OPCDA_HTTP_IDLE_TIMEOUT",
			set:   durationBound(func(c *Config, v time.Duration) { c.HTTPIdleTimeout = v }),
			floor: 1, ceiling: oneDay, signed: true,
		},
		{
			name:  "OPCDA_REQUEST_DEADLINE",
			set:   durationBound(func(c *Config, v time.Duration) { c.RequestDeadline = v }),
			floor: 1, ceiling: oneDay, signed: true,
		},
		{
			name:    "OPCDA_GRPC_MAX_RECEIVE_BYTES",
			set:     func(c *Config, v int64) { c.MaxGRPCReceiveBytes = int(v) },
			isolate: func(c *Config) { c.MaxGRPCConnections, c.MaxGRPCStreams = 1, 1 },
			floor:   1, ceiling: 64 << 20, signed: true,
		},
		{
			name:    "OPCDA_GRPC_MAX_SEND_BYTES",
			set:     func(c *Config, v int64) { c.MaxGRPCSendBytes = int(v) },
			isolate: func(c *Config) { c.MaxConcurrentGRPCRPCs = 1 },
			floor:   1, ceiling: 64 << 20, signed: true,
		},
		{
			name: "OPCDA_GRPC_MAX_CONNECTIONS",
			set:  func(c *Config, v int64) { c.MaxGRPCConnections = int(v) },
			isolate: func(c *Config) {
				c.MaxGRPCReceiveBytes, c.MaxGRPCMetadataBytes, c.MaxGRPCStreams = 1, 1, 1
			},
			floor: 1, ceiling: 2048, signed: true,
		},
		{
			name:    "OPCDA_GRPC_MAX_CONCURRENT_RPCS",
			set:     func(c *Config, v int64) { c.MaxConcurrentGRPCRPCs = int(v) },
			isolate: func(c *Config) { c.MaxGRPCSendBytes = 1 },
			floor:   1, ceiling: 1024, signed: true,
		},
		{
			name: "OPCDA_GRPC_MAX_STREAMS",
			set:  func(c *Config, v int64) { c.MaxGRPCStreams = uint32(v) },
			isolate: func(c *Config) {
				c.MaxGRPCReceiveBytes, c.MaxGRPCMetadataBytes, c.MaxGRPCConnections = 1, 1, 1
			},
			floor: 1, ceiling: 4096,
		},
		{
			name:    "OPCDA_GRPC_MAX_METADATA_BYTES",
			set:     func(c *Config, v int64) { c.MaxGRPCMetadataBytes = uint32(v) },
			isolate: func(c *Config) { c.MaxGRPCConnections, c.MaxGRPCStreams = 1, 1 },
			floor:   1, ceiling: 1 << 20,
		},
		{
			name:  "OPCDA_GRPC_CONNECTION_TIMEOUT",
			set:   durationBound(func(c *Config, v time.Duration) { c.GRPCConnectionTimeout = v }),
			floor: 1, ceiling: oneDay, signed: true,
		},
		{
			name:  "OPCDA_GRPC_MAX_CONNECTION_IDLE",
			set:   durationBound(func(c *Config, v time.Duration) { c.GRPCMaxConnectionIdle = v }),
			floor: 1, ceiling: oneDay, signed: true,
		},
		{
			// The grace period may not outlast the age, so the age's own floor
			// is only reachable once the grace has been brought down to it.
			name:    "OPCDA_GRPC_MAX_CONNECTION_AGE",
			set:     durationBound(func(c *Config, v time.Duration) { c.GRPCMaxConnectionAge = v }),
			isolate: func(c *Config) { c.GRPCMaxConnectionGrace = 1 },
			floor:   1, ceiling: oneDay, signed: true,
		},
		{
			name:    "OPCDA_GRPC_MAX_CONNECTION_GRACE",
			set:     durationBound(func(c *Config, v time.Duration) { c.GRPCMaxConnectionGrace = v }),
			isolate: func(c *Config) { c.GRPCMaxConnectionAge = time.Duration(oneDay) },
			floor:   1, ceiling: oneDay, signed: true,
		},
		{
			name:  "OPCDA_GRPC_KEEPALIVE_MIN_TIME",
			set:   durationBound(func(c *Config, v time.Duration) { c.GRPCKeepaliveMinTime = v }),
			floor: 1, ceiling: oneDay, signed: true,
		},
	}
}

func TestEveryConfiguredBoundIsAcceptedAtBothItsEnds(t *testing.T) {
	base := DefaultConfig()
	if err := base.finalizeAndValidate(); err != nil {
		t.Fatalf("the default configuration is invalid, so every case below proves nothing: %v", err)
	}

	for _, bound := range configBounds() {
		t.Run(bound.name, func(t *testing.T) {
			attempt := func(value int64) error {
				config := DefaultConfig()
				if bound.isolate != nil {
					bound.isolate(&config)
				}
				bound.set(&config, value)
				return config.finalizeAndValidate()
			}

			if err := attempt(bound.floor); err != nil {
				t.Errorf("exactly the floor (%d) was refused: %v", bound.floor, err)
			}
			if err := attempt(bound.ceiling); err != nil {
				t.Errorf("exactly the ceiling (%d) was refused: %v", bound.ceiling, err)
			}
			if err := attempt(bound.floor - 1); err == nil {
				t.Errorf("one below the floor (%d) was accepted", bound.floor-1)
			}
			if err := attempt(bound.ceiling + 1); err == nil {
				t.Errorf("one past the ceiling (%d) was accepted", bound.ceiling+1)
			}
			if bound.signed {
				if err := attempt(-1); err == nil {
					t.Error("a negative value was accepted")
				}
			}
		})
	}
}

// The five aggregate budgets are the rules that stop a configuration whose
// individual bounds are each legal from reserving more memory than the adapter
// may hold at once. Each is a product, so each has one boundary: exactly the
// budget is the largest configuration that may run.
func TestEveryAggregateBudgetIsUsableUpToItsLastByte(t *testing.T) {
	for _, testCase := range []struct {
		name string
		// at sets a configuration whose product equals the budget exactly.
		at func(*Config)
		// past sets the same configuration with one more byte in the product.
		past func(*Config)
	}{
		{
			name: "HTTP body",
			at:   func(c *Config) { c.MaxHTTPBodyBytes, c.MaxConcurrentRequests = 1<<20, 256 },
			past: func(c *Config) { c.MaxHTTPBodyBytes, c.MaxConcurrentRequests = 1<<20+1, 256 },
		},
		{
			name: "HTTP header",
			at:   func(c *Config) { c.MaxHTTPHeaderBytes, c.MaxHTTPConnections = 32<<10, 2048 },
			past: func(c *Config) { c.MaxHTTPHeaderBytes, c.MaxHTTPConnections = 32<<10+1, 2048 },
		},
		{
			name: "gRPC receive",
			at: func(c *Config) {
				c.MaxGRPCReceiveBytes, c.MaxGRPCConnections, c.MaxGRPCStreams = 1<<20, 16, 16
			},
			past: func(c *Config) {
				c.MaxGRPCReceiveBytes, c.MaxGRPCConnections, c.MaxGRPCStreams = 1<<20+1, 16, 16
			},
		},
		{
			name: "gRPC send",
			at:   func(c *Config) { c.MaxGRPCSendBytes, c.MaxConcurrentGRPCRPCs = 1<<20, 256 },
			past: func(c *Config) { c.MaxGRPCSendBytes, c.MaxConcurrentGRPCRPCs = 1<<20+1, 256 },
		},
		{
			// The connection and stream counts this case needs are also factors
			// in the receive budget, so the receive bound comes down with them:
			// left at its default it would be the rule that refuses, and the
			// metadata budget would never be reached.
			name: "gRPC metadata",
			at: func(c *Config) {
				c.MaxGRPCReceiveBytes = 4 << 10
				c.MaxGRPCMetadataBytes, c.MaxGRPCConnections, c.MaxGRPCStreams = 32<<10, 64, 32
			},
			past: func(c *Config) {
				c.MaxGRPCReceiveBytes = 4 << 10
				c.MaxGRPCMetadataBytes, c.MaxGRPCConnections, c.MaxGRPCStreams = 32<<10+1, 64, 32
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			at := DefaultConfig()
			testCase.at(&at)
			if err := at.finalizeAndValidate(); err != nil {
				t.Errorf("a configuration filling the budget exactly was refused: %v", err)
			}
			past := DefaultConfig()
			testCase.past(&past)
			if err := past.finalizeAndValidate(); err == nil {
				t.Error("a configuration one byte past the budget was accepted")
			}
		})
	}
}

// Two rules relate one setting to another rather than to a constant, and
// neither had a case at the point where the two are equal -- which is the
// value each rule permits.
func TestASettingBoundedByAnotherMayEqualIt(t *testing.T) {
	t.Run("the grace period may equal the age", func(t *testing.T) {
		equal := DefaultConfig()
		equal.GRPCMaxConnectionAge = 10 * time.Minute
		equal.GRPCMaxConnectionGrace = 10 * time.Minute
		if err := equal.finalizeAndValidate(); err != nil {
			t.Errorf("a grace equal to the age was refused: %v", err)
		}
		past := DefaultConfig()
		past.GRPCMaxConnectionAge = 10 * time.Minute
		past.GRPCMaxConnectionGrace = 10*time.Minute + time.Nanosecond
		if err := past.finalizeAndValidate(); err == nil {
			t.Error("a grace outlasting the age was accepted")
		}
	})

	t.Run("the initial backoff may equal the maximum", func(t *testing.T) {
		equal := DefaultConfig()
		equal.Runtime.ReconnectInitial = 5 * time.Second
		equal.Runtime.ReconnectMax = 5 * time.Second
		if err := equal.finalizeAndValidate(); err != nil {
			t.Errorf("an initial backoff equal to the maximum was refused: %v", err)
		}
		past := DefaultConfig()
		past.Runtime.ReconnectInitial = 5*time.Second + time.Nanosecond
		past.Runtime.ReconnectMax = 5 * time.Second
		if err := past.finalizeAndValidate(); err == nil {
			t.Error("an initial backoff past the maximum was accepted")
		}
	})
}

// The listen address is parsed rather than bounded, and the highest port is the
// value that decides whether the parse is right: 65535 is addressable and 65536
// is not a port at all.
func TestTheListenPortReachesTheTopOfItsRange(t *testing.T) {
	for _, testCase := range []struct {
		address  string
		accepted bool
	}{
		{"127.0.0.1:65535", true},
		{"127.0.0.1:0", true},
		{"127.0.0.1:65536", false},
		{"127.0.0.1:-1", false},
		{"127.0.0.1:http", false},
		{"127.0.0.1", false},
	} {
		t.Run(testCase.address, func(t *testing.T) {
			config := DefaultConfig()
			config.HTTPListenAddress = testCase.address
			if accepted := config.finalizeAndValidate() == nil; accepted != testCase.accepted {
				t.Errorf("accepted = %v, want %v", accepted, testCase.accepted)
			}
		})
	}
}

// The three runtime durations have a floor but no ceiling of their own, so
// they sit outside the table above. A zero or negative one is not a slow
// adapter but a broken one: a zero watchdog would abandon every COM call the
// moment it started, and a zero backoff would reconnect in a tight loop.
func TestEveryRuntimeDurationMustBePositive(t *testing.T) {
	for _, testCase := range []struct {
		name string
		set  func(*Config, time.Duration)
	}{
		{"OPCDA_RECONNECT_INITIAL", func(c *Config, v time.Duration) { c.Runtime.ReconnectInitial = v }},
		{"OPCDA_RECONNECT_MAX", func(c *Config, v time.Duration) {
			// The initial backoff may not exceed the maximum, so it comes down
			// with it; otherwise that rule refuses before this one is reached.
			c.Runtime.ReconnectInitial, c.Runtime.ReconnectMax = v, v
		}},
		{"OPCDA_COM_CALL_WATCHDOG", func(c *Config, v time.Duration) { c.Runtime.COMCallWatchdog = v }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			attempt := func(value time.Duration) error {
				config := DefaultConfig()
				testCase.set(&config, value)
				return config.finalizeAndValidate()
			}
			if err := attempt(time.Nanosecond); err != nil {
				t.Errorf("the smallest positive duration was refused: %v", err)
			}
			if err := attempt(0); err == nil {
				t.Error("a zero duration was accepted")
			}
			if err := attempt(-time.Second); err == nil {
				t.Error("a negative duration was accepted")
			}
		})
	}

	// A maximum of zero is refused whichever way round the rules are read: the
	// ordering rule would catch it too, since any positive initial backoff then
	// exceeds it. What the positivity check adds is which variable the operator
	// is told about, and that is the whole value of the message -- so the case
	// asserts the reason rather than only the refusal.
	zeroMaximum := DefaultConfig()
	zeroMaximum.Runtime.ReconnectInitial = time.Nanosecond
	zeroMaximum.Runtime.ReconnectMax = 0
	err := zeroMaximum.finalizeAndValidate()
	if err == nil {
		t.Fatal("a zero reconnect maximum was accepted")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("a zero reconnect maximum was refused as %v, which names the ordering rule "+
			"rather than the variable the operator set", err)
	}

	// The gRPC connection age is the same shape: a zero age is refused either
	// way, because any positive grace period then outlasts it. Again what the
	// positivity check adds is which variable the operator is told about.
	zeroAge := DefaultConfig()
	zeroAge.GRPCMaxConnectionGrace = time.Nanosecond
	zeroAge.GRPCMaxConnectionAge = 0
	ageErr := zeroAge.finalizeAndValidate()
	if ageErr == nil {
		t.Fatal("a zero gRPC connection age was accepted")
	}
	if !strings.Contains(ageErr.Error(), "positive") {
		t.Errorf("a zero gRPC connection age was refused as %v, which names the grace rule "+
			"rather than the variable the operator set", ageErr)
	}
}
