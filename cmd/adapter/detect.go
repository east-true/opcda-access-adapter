package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

const (
	defaultDetectionTimeout = 10 * time.Second
	maximumDetectionTimeout = 24 * time.Hour
)

type localDetector func(context.Context, opcda.LocalDetectionLimits) ([]opcda.DetectedLocalServer, error)

type utilityDependencies struct {
	detect        localDetector
	writeConfig   func(string, app.Config) error
	runForeground func(string) error
	service       serviceCommandDependencies
}

type localDetectionOutput struct {
	Scope                string                      `json:"scope"`
	Category             string                      `json:"category"`
	CategoryID           string                      `json:"categoryId"`
	DetectorArchitecture string                      `json:"detectorArchitecture"`
	Servers              []localDetectedServerOutput `json:"servers"`
}

type localDetectedServerOutput struct {
	CLSID  string `json:"clsid"`
	ProgID string `json:"progId,omitempty"`
}

func handleUtilityCommand(arguments []string, input io.Reader, output, errorOutput io.Writer, dependencies utilityDependencies) (bool, int) {
	if len(arguments) == 1 && (arguments[0] == "help" || arguments[0] == "--help" || arguments[0] == "-h") {
		writeUsage(output)
		return true, 0
	}
	if len(arguments) == 0 {
		return false, 0
	}
	switch arguments[0] {
	case "detect":
		return true, runDetect(arguments[1:], output, errorOutput, dependencies.detect)
	case "setup":
		return true, runSetup(arguments[1:], input, output, errorOutput, guidedSetupDependencies{
			detect:          dependencies.detect,
			writeConfig:     dependencies.writeConfig,
			runForeground:   dependencies.runForeground,
			installAndStart: dependencies.service.installAndStart,
		})
	case "run":
		return true, runConfigCommand(arguments[1:], output, errorOutput, dependencies.runForeground)
	case "service":
		return true, runServiceCommand(arguments[1:], output, errorOutput, dependencies.service)
	default:
		return false, 0
	}
}

func runDetect(arguments []string, output, errorOutput io.Writer, detect localDetector) int {
	limits := opcda.DefaultLocalDetectionLimits()
	flags := flag.NewFlagSet("detect", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	flags.IntVar(&limits.MaxServers, "max-results", limits.MaxServers, "maximum registered DA 2.0 servers to return")
	flags.IntVar(&limits.MaxProgIDCodeUnits, "max-progid-code-units", limits.MaxProgIDCodeUnits, "maximum UTF-16 code units in a registered ProgID")
	timeout := flags.Duration("timeout", defaultDetectionTimeout, "local registration detection deadline")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: opcda-access-adapter detect [options]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errorOutput, "detect does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 || *timeout > maximumDetectionTimeout {
		fmt.Fprintln(errorOutput, "detect timeout must be positive and at most 24h")
		return 2
	}
	if limits.MaxServers <= 0 || limits.MaxProgIDCodeUnits <= 0 {
		fmt.Fprintln(errorOutput, "detect limits must be positive")
		return 2
	}
	if err := limits.Validate(); err != nil {
		fmt.Fprintf(errorOutput, "invalid detect limits: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	servers, err := detect(ctx, limits)
	if err != nil {
		if adapterError, ok := opcda.AsAdapterError(err); ok {
			fmt.Fprintf(errorOutput, "detect local OPC DA servers: %s: %s\n", adapterError.Code, adapterError.Message)
		} else {
			fmt.Fprintf(errorOutput, "detect local OPC DA servers: %v\n", err)
		}
		return 1
	}
	response := localDetectionOutput{
		Scope:                "local",
		Category:             opcda.OPCDAServer20CategoryName,
		CategoryID:           opcda.OPCDAServer20CategoryID,
		DetectorArchitecture: runtime.GOARCH,
		Servers:              make([]localDetectedServerOutput, len(servers)),
	}
	for index, server := range servers {
		response.Servers[index] = localDetectedServerOutput{CLSID: server.CLSID, ProgID: server.ProgID}
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(response); err != nil {
		fmt.Fprintf(errorOutput, "encode local OPC DA detection result: %v\n", err)
		return 1
	}
	return 0
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, "usage:")
	fmt.Fprintln(output, "  opcda-access-adapter                 run one explicitly configured local DA adapter")
	fmt.Fprintln(output, "  opcda-access-adapter setup           choose a detected source, HTTP frontend, and execution mode")
	fmt.Fprintln(output, "  opcda-access-adapter detect          list local OPC DA 2.0 registrations without activating them")
	fmt.Fprintln(output, "  opcda-access-adapter run --config F  run a reviewed setup configuration in this terminal")
	fmt.Fprintln(output, "  opcda-access-adapter service ...     install or remove a background Windows Service")
	fmt.Fprintln(output, "  opcda-access-adapter --version       print build version metadata")
}
