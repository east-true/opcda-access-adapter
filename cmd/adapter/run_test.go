package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The exit code is the whole of what a supervisor sees. A Windows Service
// manager, a container runtime and a shell script all decide whether to
// restart the adapter from it and from nothing else, so each way out of the
// top-level command has to report the one an operator can act on: 2 for an
// invocation that was wrong, 1 for an adapter that ran and stopped badly, 0
// for work that was done.
//
// None of it was reachable: main called os.Exit and built its own effects, so
// every nil check in it survived the sweep. It now takes what it needs the way
// every other command in this package already does.

func stubDependencies() mainDependencies {
	return mainDependencies{
		utility: utilityDependencies{
			detect: func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error) {
				return nil, nil
			},
			writeConfig:   func(string, app.Config) error { return nil },
			runForeground: func(string) error { return nil },
		},
		loadConfig: func() (app.Config, error) { return app.DefaultConfig(), nil },
		runAdapter: func(app.Config) error { return nil },
	}
}

func TestEachWayOutOfTheCommandReportsItsOwnExitCode(t *testing.T) {
	loadFailure := errors.New("OPCDA_MAX_BODY_BYTES is not a number")
	adapterFailure := errors.New("the listener stopped")

	for _, testCase := range []struct {
		name      string
		arguments []string
		adjust    func(*mainDependencies)
		exit      int
	}{
		{name: "the adapter ran and stopped cleanly", exit: 0},
		{name: "the version was asked for", arguments: []string{"--version"}, exit: 0},
		{name: "a utility command that succeeded", arguments: []string{"help"}, exit: 0},
		{
			name:      "a utility command that refused its arguments",
			arguments: []string{"detect", "--max-results", "0"},
			exit:      2,
		},
		{
			name:      "an argument that is not a command",
			arguments: []string{"strt"},
			exit:      2,
		},
		{
			name: "a configuration that could not be read",
			adjust: func(d *mainDependencies) {
				d.loadConfig = func() (app.Config, error) { return app.Config{}, loadFailure }
			},
			exit: 2,
		},
		{
			name:   "an adapter that ran and stopped badly",
			adjust: func(d *mainDependencies) { d.runAdapter = func(app.Config) error { return adapterFailure } },
			exit:   1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dependencies := stubDependencies()
			if testCase.adjust != nil {
				testCase.adjust(&dependencies)
			}
			var output, errorOutput bytes.Buffer
			exit := run(testCase.arguments, strings.NewReader(""), &output, &errorOutput, dependencies)
			if exit != testCase.exit {
				t.Errorf("exit = %d, want %d (stdout=%q stderr=%q)",
					exit, testCase.exit, output.String(), errorOutput.String())
			}
		})
	}
}

// The order the command reads its arguments in is a contract of its own. A
// version request is answered before anything else is built, and a utility
// command is handled before the configuration is read -- so `detect` works on
// a machine whose adapter configuration is wrong, which is the machine someone
// runs detect on.
func TestAUtilityCommandIsAnsweredBeforeTheConfigurationIsRead(t *testing.T) {
	dependencies := stubDependencies()
	loads, adapters := 0, 0
	dependencies.loadConfig = func() (app.Config, error) {
		loads++
		return app.Config{}, errors.New("the configuration on this machine is wrong")
	}
	dependencies.runAdapter = func(app.Config) error {
		adapters++
		return nil
	}

	for _, arguments := range [][]string{{"--version"}, {"help"}, {"detect"}} {
		var output, errorOutput bytes.Buffer
		if exit := run(arguments, strings.NewReader(""), &output, &errorOutput, dependencies); exit != 0 {
			t.Errorf("%v exited %d on a machine with a bad configuration: %s",
				arguments, exit, errorOutput.String())
		}
	}
	if loads != 0 {
		t.Errorf("the configuration was read %d times while handling a utility command", loads)
	}
	if adapters != 0 {
		t.Errorf("the adapter was started %d times while handling a utility command", adapters)
	}

	// And the adapter is started only once nothing else claimed the arguments.
	var output, errorOutput bytes.Buffer
	dependencies.loadConfig = func() (app.Config, error) { return app.DefaultConfig(), nil }
	if exit := run(nil, strings.NewReader(""), &output, &errorOutput, dependencies); exit != 0 {
		t.Fatalf("the plain invocation exited %d: %s", exit, errorOutput.String())
	}
	if adapters != 1 {
		t.Errorf("the adapter was started %d times, want once", adapters)
	}
}

// A version request writes to the stream it was handed rather than to the
// process's own stdout, which is what lets it be checked at all.
func TestTheVersionIsWrittenToTheStreamItWasGiven(t *testing.T) {
	var output, errorOutput bytes.Buffer
	if exit := run([]string{"--version"}, strings.NewReader(""), &output, &errorOutput, stubDependencies()); exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	if output.Len() == 0 {
		t.Error("the version was not written to the given stream")
	}
	if errorOutput.Len() != 0 {
		t.Errorf("the version wrote to the error stream: %q", errorOutput.String())
	}
}

// The two failures that happen before an adapter exists are reachable without
// one, and each has to be named for what went wrong: a file that could not be
// read is not the same problem as a configuration that was read and refused.
func TestTheForegroundRunNamesWhatFailedBeforeItStarted(t *testing.T) {
	missing := runForegroundConfig(filepath.Join(t.TempDir(), "absent.json"))
	if missing == nil {
		t.Fatal("running a configuration file that does not exist succeeded")
	}
	// Carrying on with a configuration that was never read would fail too, one
	// step later and under a name that sends the operator to the adapter
	// instead of to the path they typed. The reason is the whole difference.
	if strings.Contains(missing.Error(), "create adapter") {
		t.Errorf("a missing configuration file was reported as %v, which blames the "+
			"adapter rather than the file", missing)
	}

	// app.New validates before it allocates a runtime, so an invalid
	// configuration fails here without any DA connection being attempted.
	invalid := app.DefaultConfig()
	invalid.MaxHTTPBodyBytes = 0
	err := runForeground(invalid)
	if err == nil {
		t.Fatal("running an invalid configuration succeeded")
	}
	if !strings.Contains(err.Error(), "create adapter") {
		t.Errorf("an invalid configuration failed as %v, which does not say the adapter "+
			"was never created", err)
	}
}

// The third failure in runForeground -- a service that was created and then
// could not start -- is not covered here. Reaching it needs app.New to build a
// real DA runtime, which on Windows means touching COM in the test process:
// the one thing the unit suite deliberately leaves to the validation
// scenarios. It is a gap rather than an equivalent mutant.
