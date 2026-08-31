package app

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
)

// The HTTP API reference documents a default for every configuration key. A
// documented default that drifts from the code is a fact that rots, and this
// repository has shipped one: a "current main SHA" that was forty-eight merges
// out of date because nothing was going to keep it current.
//
// This checks the table against the loader itself rather than against a second
// copy of the defaults. Setting a key to its documented default must produce
// exactly the configuration that setting nothing produces, and setting it to
// anything else must not -- which also proves the documented key is real and is
// actually read.
func TestDocumentedDefaultsAreTheRealDefaults(t *testing.T) {
	documented := documentedDefaults(t)
	if len(documented) < 20 {
		t.Fatalf("only %d documented defaults were found; the table moved", len(documented))
	}

	base := loadWithout(t, documented)
	for key, value := range documented {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			same, err := LoadConfig()
			if err != nil {
				t.Fatalf("%s=%q is documented as the default but does not load: %v", key, value, err)
			}
			if !reflect.DeepEqual(same, base) {
				t.Fatalf("%s=%q is documented as the default but changes the configuration", key, value)
			}
			other, ok := anotherValue(key, value)
			if !ok {
				return
			}
			t.Setenv(key, other)
			changed, err := LoadConfig()
			if err != nil {
				t.Fatalf("%s=%q did not load: %v", key, other, err)
			}
			if reflect.DeepEqual(changed, base) {
				t.Fatalf("%s is documented but setting it to %q changes nothing", key, other)
			}
		})
	}
}

// documentedDefaults reads the configuration table out of the reference.
func documentedDefaults(t *testing.T) map[string]string {
	t.Helper()
	root := repositoryRoot(t)
	row := regexp.MustCompile("(?m)^\\| `(OPCDA_[A-Z_]+)` \\| `([^`]+)` \\|")
	defaults := map[string]string{}
	// Both API references document configuration, and either can drift.
	for _, reference := range []string{"http-api.md", "grpc-api.md"} {
		body, err := os.ReadFile(filepath.Join(root, "docs", reference))
		if err != nil {
			t.Fatalf("read %s: %v", reference, err)
		}
		for _, match := range row.FindAllStringSubmatch(string(body), -1) {
			if existing, seen := defaults[match[1]]; seen && existing != match[2] {
				t.Fatalf("%s is documented as %q in one reference and %q in another",
					match[1], existing, match[2])
			}
			defaults[match[1]] = match[2]
		}
	}
	return defaults
}

// loadWithout loads the configuration with every documented key unset, which is
// what "the default" means.
func loadWithout(t *testing.T, keys map[string]string) Config {
	t.Helper()
	for key := range keys {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
	// A source is required before the configuration validates, and it is not
	// one of the documented defaults.
	t.Setenv("OPCDA_SOURCE_PROG_ID", "Example.Server.1")
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("load the default configuration: %v", err)
	}
	return config
}

// anotherValue is a valid value for a key that is not its default, so that a
// key which is documented but never read is caught. It reports false for keys
// whose only other values would be invalid or environment-specific.
func anotherValue(key, current string) (string, bool) {
	switch {
	case current == "true" || current == "false":
		if current == "true" {
			return "false", true
		}
		return "true", true
	case regexp.MustCompile(`^\d+(ms|s|m)$`).MatchString(current):
		// A duration: doubling it stays valid wherever it is bounded.
		return "2" + current, true
	case regexp.MustCompile(`^\d+$`).MatchString(current):
		// An integer bound: one below stays inside every ceiling.
		if current == "1" {
			return "2", true
		}
		return trimLast(current), true
	default:
		// Frontend names and listen addresses have no safe generic alternative.
		return "", false
	}
}

func trimLast(value string) string {
	if len(value) == 1 {
		return "1"
	}
	return value[:len(value)-1]
}
