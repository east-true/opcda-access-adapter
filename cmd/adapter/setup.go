package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

const (
	defaultSetupConfigPath = "opcda-access-adapter.json"
	defaultServiceName     = "OPCDAAccessAdapter"
	maximumPromptLineBytes = 4096
	maximumPromptAttempts  = 3
)

var serviceNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

type guidedSetupDependencies struct {
	detect          localDetector
	writeConfig     func(string, app.Config) error
	runForeground   func(string) error
	installAndStart func(serviceInstallOptions) error
}

type setupExecution int

const (
	setupRunForeground setupExecution = iota + 1
	setupInstallService
	setupSaveOnly
)

func runSetup(
	arguments []string,
	input io.Reader,
	output io.Writer,
	errorOutput io.Writer,
	dependencies guidedSetupDependencies,
) int {
	limits := opcda.DefaultLocalDetectionLimits()
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	configPath := flags.String("config", defaultSetupConfigPath, "new configuration file path (must not already exist)")
	listenAddress := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	grpcListenAddress := flags.String("grpc-listen", "127.0.0.1:50051", "gRPC listen address")
	writeEnabled := flags.Bool("enable-write", false, "explicitly enable strict typed value Write")
	serviceName := flags.String("service-name", defaultServiceName, "Windows Service name")
	timeout := flags.Duration("timeout", defaultDetectionTimeout, "local registration detection deadline")
	flags.IntVar(&limits.MaxServers, "max-results", limits.MaxServers, "maximum detected registrations")
	flags.IntVar(&limits.MaxProgIDCodeUnits, "max-progid-code-units", limits.MaxProgIDCodeUnits, "maximum registered ProgID UTF-16 code units")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: opcda-access-adapter setup [options]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errorOutput, "setup does not accept positional arguments")
		return 2
	}
	if *timeout <= 0 || *timeout > maximumDetectionTimeout {
		fmt.Fprintln(errorOutput, "setup timeout must be positive and at most 24h")
		return 2
	}
	if err := limits.Validate(); err != nil {
		fmt.Fprintf(errorOutput, "invalid setup detection limits: %v\n", err)
		return 2
	}
	if !serviceNamePattern.MatchString(*serviceName) {
		fmt.Fprintln(errorOutput, "service name must start with a letter and contain at most 64 ASCII letters, digits, dot, underscore, or hyphen")
		return 2
	}
	if err := app.ValidateConfigFilePath(*configPath); err != nil {
		fmt.Fprintf(errorOutput, "invalid setup configuration path: %v\n", err)
		return 2
	}
	if _, err := app.GuidedSetupConfig(
		opcda.SourceConfig{CLSID: "{00000000-0000-0000-0000-000000000000}"},
		*listenAddress,
		*writeEnabled,
	); err != nil {
		fmt.Fprintf(errorOutput, "invalid setup frontend configuration: %v\n", err)
		return 2
	}
	if _, err := app.GuidedSetupFrontendConfig(
		opcda.SourceConfig{CLSID: "{00000000-0000-0000-0000-000000000000}"},
		app.FrontendGRPC,
		*grpcListenAddress,
		*writeEnabled,
	); err != nil {
		fmt.Fprintf(errorOutput, "invalid setup gRPC frontend configuration: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	servers, err := dependencies.detect(ctx, limits)
	if err != nil {
		fmt.Fprintf(errorOutput, "detect local OPC DA servers for setup: %v\n", err)
		return 1
	}
	if len(servers) == 0 {
		fmt.Fprintln(errorOutput, "no local OPC DA 2.0 registrations were detected; try the matching 386/amd64 build or configure a source explicitly")
		return 1
	}

	prompt := newBoundedPrompt(input, output)
	fmt.Fprintf(output, "OPC DA Access Adapter guided setup (%s)\n\n", runtime.GOARCH)
	fmt.Fprintln(output, "Local OPC DA 2.0 registrations:")
	for index, server := range servers {
		name := server.ProgID
		if name == "" {
			name = "(ProgID unavailable)"
		}
		fmt.Fprintf(output, "  %d) %s  %s\n", index+1, name, server.CLSID)
	}
	selectedIndex, err := prompt.selectNumber("Select one source", len(servers))
	if err != nil {
		fmt.Fprintf(errorOutput, "select source: %v\n", err)
		return 1
	}
	selected := servers[selectedIndex-1]

	fmt.Fprintln(output, "\nAvailable frontends:")
	fmt.Fprintln(output, "  1) HTTP/JSON (v0)")
	fmt.Fprintln(output, "  2) gRPC (typed DA-native unary API)")
	frontendChoice, err := prompt.selectNumber("Select one frontend", 2)
	if err != nil {
		fmt.Fprintf(errorOutput, "select frontend: %v\n", err)
		return 1
	}
	frontend := app.FrontendHTTP
	frontendLabel := "HTTP/JSON"
	selectedListenAddress := *listenAddress
	if frontendChoice == 2 {
		frontend = app.FrontendGRPC
		frontendLabel = "gRPC"
		selectedListenAddress = *grpcListenAddress
	}

	config, err := app.GuidedSetupFrontendConfig(
		opcda.SourceConfig{CLSID: selected.CLSID},
		frontend,
		selectedListenAddress,
		*writeEnabled,
	)
	if err != nil {
		fmt.Fprintf(errorOutput, "build guided configuration: %v\n", err)
		return 2
	}

	fmt.Fprintln(output, "\nAfter saving:")
	fmt.Fprintln(output, "  1) Run in this terminal")
	fmt.Fprintln(output, "  2) Install and start a Windows Service (background; Administrator required)")
	fmt.Fprintln(output, "  3) Save configuration only")
	executionChoice, err := prompt.selectNumber("Select one action", 3)
	if err != nil {
		fmt.Fprintf(errorOutput, "select action: %v\n", err)
		return 1
	}
	execution := setupExecution(executionChoice)

	fmt.Fprintln(output, "\nReview:")
	fmt.Fprintf(output, "  source ProgID: %s\n", displayOptional(selected.ProgID))
	fmt.Fprintf(output, "  source CLSID: %s\n", selected.CLSID)
	fmt.Fprintf(output, "  frontend: %s\n", frontendLabel)
	fmt.Fprintf(output, "  listen: %s\n", selectedListenAddress)
	fmt.Fprintf(output, "  typed value Write enabled: %t\n", config.WriteEnabled)
	fmt.Fprintf(output, "  configuration: %s\n", *configPath)
	if execution == setupInstallService {
		fmt.Fprintf(output, "  Windows Service: %s (LocalService account)\n", *serviceName)
		fmt.Fprintln(output, "  DCOM launch/access permissions for that account remain vendor and machine policy.")
	}
	confirmed, err := prompt.confirm("Create this configuration? [y/N]")
	if err != nil {
		fmt.Fprintf(errorOutput, "confirm setup: %v\n", err)
		return 1
	}
	if !confirmed {
		fmt.Fprintln(output, "Setup cancelled; no configuration or service was created.")
		return 0
	}

	if err := dependencies.writeConfig(*configPath, config); err != nil {
		fmt.Fprintf(errorOutput, "write guided configuration: %v\n", err)
		return 1
	}
	fmt.Fprintf(output, "Configuration created: %s\n", *configPath)

	switch execution {
	case setupRunForeground:
		fmt.Fprintln(output, "Starting the adapter in this terminal. Press Ctrl+C to stop.")
		if err := dependencies.runForeground(*configPath); err != nil {
			fmt.Fprintf(errorOutput, "run configured adapter: %v\n", err)
			return 1
		}
	case setupInstallService:
		if err := dependencies.installAndStart(serviceInstallOptions{Name: *serviceName, ConfigPath: *configPath}); err != nil {
			fmt.Fprintf(errorOutput, "install and start Windows Service: %v\n", err)
			fmt.Fprintln(errorOutput, "the reviewed configuration was retained; no foreground fallback or automatic retry was attempted")
			return 1
		}
		fmt.Fprintf(output, "Windows Service %s installed and started.\n", *serviceName)
		if config.Frontend == app.FrontendHTTP {
			fmt.Fprintf(output, "Status: http://%s/v1/status\n", config.HTTPListenAddress)
		} else {
			fmt.Fprintf(output, "gRPC endpoint: %s (opcda.access.v1.OPCDAAccess/Status)\n", config.GRPCListenAddress)
		}
	case setupSaveOnly:
		fmt.Fprintf(output, "Run later with: .\\opcda-access-adapter.exe run --config %s\n", quotePowerShellLiteral(*configPath))
	default:
		fmt.Fprintln(errorOutput, "internal setup action mismatch")
		return 1
	}
	return 0
}

func quotePowerShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func displayOptional(value string) string {
	if value == "" {
		return "(unavailable; exact CLSID selected)"
	}
	return value
}

type boundedPrompt struct {
	reader *bufio.Reader
	output io.Writer
}

func newBoundedPrompt(input io.Reader, output io.Writer) *boundedPrompt {
	return &boundedPrompt{reader: bufio.NewReaderSize(input, maximumPromptLineBytes), output: output}
}

func (prompt *boundedPrompt) selectNumber(label string, maximum int) (int, error) {
	for attempt := 0; attempt < maximumPromptAttempts; attempt++ {
		fmt.Fprintf(prompt.output, "%s [1-%d]: ", label, maximum)
		line, err := prompt.readLine()
		if err != nil {
			return 0, err
		}
		selection, err := strconv.Atoi(line)
		if err == nil && selection >= 1 && selection <= maximum {
			return selection, nil
		}
		fmt.Fprintln(prompt.output, "Invalid selection.")
	}
	return 0, fmt.Errorf("too many invalid selections")
}

func (prompt *boundedPrompt) confirm(label string) (bool, error) {
	fmt.Fprintf(prompt.output, "%s ", label)
	line, err := prompt.readLine()
	if err != nil {
		return false, err
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return true, nil
	case "", "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("confirmation must be yes or no")
	}
}

func (prompt *boundedPrompt) readLine() (string, error) {
	line, isPrefix, err := prompt.reader.ReadLine()
	if isPrefix {
		return "", fmt.Errorf("input line exceeds %d bytes", maximumPromptLineBytes)
	}
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) != 0 {
			return strings.TrimSpace(string(line)), nil
		}
		return "", err
	}
	return strings.TrimSpace(string(line)), nil
}
