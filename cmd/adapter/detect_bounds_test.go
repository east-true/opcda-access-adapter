package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// detect's own argument checks run before the detector is called, and the
// sweep found their bounds unpinned. The existing cases prove a clearly
// invalid value is refused; none proved a valid one is accepted, so each
// bound was free to move inward and refuse a value the flag's own help text
// offers.
//
// The timeout bound matters most: the help says at most 24h, and an operator
// who asks for exactly that must get it rather than an error naming a limit
// they did not exceed.

func detectWith(t *testing.T, arguments ...string) (limits opcda.LocalDetectionLimits, calls int, exit int, stderr string) {
	t.Helper()
	detector := func(_ context.Context, received opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		limits = received
		calls++
		return nil, nil
	}
	var output, errorOutput bytes.Buffer
	exit = runDetect(arguments, &output, &errorOutput, detector)
	return limits, calls, exit, errorOutput.String()
}

func TestDetectAcceptsEveryValueItsOwnHelpOffers(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		argument []string
		accepted bool
	}{
		{"the default arguments", nil, true},
		{"the largest timeout the help names", []string{"--timeout", "24h"}, true},
		{"one step past it", []string{"--timeout", "24h1ns"}, false},
		{"the smallest expressible timeout", []string{"--timeout", "1ns"}, true},
		{"no timeout at all", []string{"--timeout", "0s"}, false},
		{"a negative timeout", []string{"--timeout", "-1s"}, false},

		{"a single result", []string{"--max-results", "1"}, true},
		{"no results allowed", []string{"--max-results", "0"}, false},
		{"a negative result count", []string{"--max-results", "-1"}, false},

		{"a single ProgID code unit", []string{"--max-progid-code-units", "1"}, true},
		{"no ProgID code units allowed", []string{"--max-progid-code-units", "0"}, false},
		{"a negative ProgID length", []string{"--max-progid-code-units", "-1"}, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, calls, exit, stderr := detectWith(t, testCase.argument...)
			if accepted := exit == 0; accepted != testCase.accepted {
				t.Fatalf("exit = %d (accepted = %v), want accepted = %v: %s",
					exit, accepted, testCase.accepted, stderr)
			}
			// A refused argument must not reach the source: detection opens a
			// COM connection, and an argument the adapter rejected is not one
			// it should act on first.
			if want := 0; !testCase.accepted && calls != want {
				t.Errorf("the detector was called %d times for a refused argument", calls)
			}
			if testCase.accepted && calls != 1 {
				t.Errorf("the detector was called %d times for an accepted argument", calls)
			}
		})
	}
}

// Each limit is checked in its own arm of a disjunction, so a zero in one must
// be refused while the other stays valid -- otherwise one arm could be removed
// and the other would cover for it.
func TestEachDetectLimitIsCheckedOnItsOwn(t *testing.T) {
	limits, _, exit, stderr := detectWith(t, "--max-results", "1", "--max-progid-code-units", "1")
	if exit != 0 {
		t.Fatalf("the smallest valid pair was refused: %s", stderr)
	}
	if limits.MaxServers != 1 || limits.MaxProgIDCodeUnits != 1 {
		t.Fatalf("the limits reached the detector as %+v", limits)
	}
	if _, _, exit, _ := detectWith(t, "--max-results", "0", "--max-progid-code-units", "1"); exit == 0 {
		t.Error("a zero result limit was accepted beside a valid ProgID limit")
	}
	if _, _, exit, _ := detectWith(t, "--max-results", "1", "--max-progid-code-units", "0"); exit == 0 {
		t.Error("a zero ProgID limit was accepted beside a valid result limit")
	}
}

// help is a whole command, not a word that may appear beside others. Treating
// "help" plus something else as help would print usage and exit zero for an
// invocation the operator meant as a command, which is the one case where
// exiting zero hides that nothing ran.
func TestHelpIsTheWholeCommandOrNoneOfIt(t *testing.T) {
	detector := func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
		t.Fatal("the detector was called while handling help")
		return nil, nil
	}
	for _, testCase := range []struct {
		arguments []string
		isHelp    bool
	}{
		{[]string{"help"}, true},
		{[]string{"--help"}, true},
		{[]string{"-h"}, true},
		{[]string{"help", "detect"}, false},
		{[]string{"--help", "--timeout", "1s"}, false},
		{[]string{"detect", "--help"}, false},
	} {
		t.Run(strings.Join(testCase.arguments, " "), func(t *testing.T) {
			var output, errorOutput bytes.Buffer
			handled, exit := handleUtilityCommand(testCase.arguments,
				strings.NewReader(""), &output, &errorOutput,
				utilityDependencies{detect: detector})
			isHelp := handled && exit == 0 && strings.Contains(output.String(), "usage")
			if isHelp != testCase.isHelp {
				t.Errorf("handled=%v exit=%d output=%q; treated as the help command = %v, want %v",
					handled, exit, output.String(), isHelp, testCase.isHelp)
			}
		})
	}
}
