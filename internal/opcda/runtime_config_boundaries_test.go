package opcda

import (
	"strings"
	"testing"
	"time"
)

// Config.validate guards the three durations the runtime is built on, and
// ADR-0001 records their ceiling. #170 pinned Limits.validate; this is the
// other half of the same function, and the sweep found it in the same state:
// every comparison loosened by one survived, and so did splitting the || chain,
// because no case ever moved one duration on its own.
//
// The ceiling here is 24 hours. A watchdog longer than that is a watchdog that
// will not fire inside any shift, which is the reason the bound exists.

func validConfig() Config {
	return Config{
		Source:           SourceConfig{ProgID: "Vendor.Server.1"},
		Limits:           DefaultLimits(),
		ReconnectInitial: time.Second,
		ReconnectMax:     30 * time.Second,
		COMCallWatchdog:  30 * time.Second,
	}
}

func TestEveryRuntimeDurationMustBePositive(t *testing.T) {
	if err := validConfig().validate(); err != nil {
		t.Fatalf("the baseline config is invalid, so every case below proves nothing: %v", err)
	}
	for _, field := range []struct {
		name string
		set  func(*Config, time.Duration)
	}{
		{"ReconnectInitial", func(c *Config, d time.Duration) { c.ReconnectInitial = d }},
		{"ReconnectMax", func(c *Config, d time.Duration) { c.ReconnectMax = d }},
		{"COMCallWatchdog", func(c *Config, d time.Duration) { c.COMCallWatchdog = d }},
	} {
		t.Run(field.name, func(t *testing.T) {
			// One field at a time, which is what separates the clauses of the
			// disjunction: a case that zeroes several proves nothing about any.
			// The reason matters, not just the refusal. ReconnectMax = 0 is
			// also caught by the "initial must not exceed maximum" rule, so a
			// test that only asks whether it was refused cannot tell that the
			// positivity check did any work -- and that is exactly the
			// mutation that survived here.
			const positivity = "must be positive"
			config := validConfig()
			field.set(&config, 0)
			err := config.validate()
			if err == nil {
				t.Fatalf("%s = 0 was accepted", field.name)
			}
			if !strings.Contains(err.Error(), positivity) {
				t.Errorf("%s = 0 was refused as %q, not for being non-positive", field.name, err)
			}
			config = validConfig()
			field.set(&config, -time.Second)
			err = config.validate()
			if err == nil {
				t.Fatalf("%s = -1s was accepted", field.name)
			}
			if !strings.Contains(err.Error(), positivity) {
				t.Errorf("%s = -1s was refused as %q, not for being non-positive", field.name, err)
			}
		})
	}
}

func TestReconnectDelaysMayBeEqualButNotInverted(t *testing.T) {
	// Equal is legal: a runtime that never backs off past its first delay is a
	// configuration, not a mistake, and the rule is "must not exceed".
	config := validConfig()
	config.ReconnectInitial = 5 * time.Second
	config.ReconnectMax = 5 * time.Second
	if err := config.validate(); err != nil {
		t.Errorf("an initial delay equal to the maximum was refused: %v", err)
	}

	config.ReconnectInitial = 5*time.Second + time.Nanosecond
	if err := config.validate(); err == nil {
		t.Error("an initial delay one nanosecond past the maximum was accepted")
	}
}

func TestTheRuntimeDurationCeilingIsTwentyFourHours(t *testing.T) {
	for _, field := range []struct {
		name string
		set  func(*Config, time.Duration)
	}{
		{"ReconnectMax", func(c *Config, d time.Duration) { c.ReconnectMax = d }},
		{"COMCallWatchdog", func(c *Config, d time.Duration) { c.COMCallWatchdog = d }},
	} {
		t.Run(field.name, func(t *testing.T) {
			config := validConfig()
			field.set(&config, maximumRuntimeDuration)
			if err := config.validate(); err != nil {
				t.Errorf("%s of exactly 24 hours was refused: %v", field.name, err)
			}
			config = validConfig()
			field.set(&config, maximumRuntimeDuration+time.Nanosecond)
			if err := config.validate(); err == nil {
				t.Errorf("%s one nanosecond past 24 hours was accepted", field.name)
			}
		})
	}
}

// The limits are validated first, and their failure has to be reported rather
// than swallowed: a config whose limits are unusable is unusable whatever its
// durations say.
func TestConfigValidationReportsAFailureFromTheLimits(t *testing.T) {
	config := validConfig()
	config.Limits.CommandQueue = 0
	if err := config.validate(); err == nil {
		t.Fatal("a config carrying invalid limits was accepted")
	}
}
