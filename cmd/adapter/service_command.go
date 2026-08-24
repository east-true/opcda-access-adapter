package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

type serviceInstallOptions struct {
	Name       string
	ConfigPath string
}

type serviceCommandDependencies struct {
	installAndStart func(serviceInstallOptions) error
	uninstall       func(string) error
	runDispatcher   func(serviceInstallOptions) error
}

func runServiceCommand(arguments []string, output, errorOutput io.Writer, dependencies serviceCommandDependencies) int {
	if len(arguments) == 0 {
		writeServiceUsage(errorOutput)
		return 2
	}
	switch arguments[0] {
	case "install":
		options, code := parseServiceOptions("install", arguments[1:], errorOutput, true)
		if code < 0 {
			return 0
		}
		if code != 0 {
			return code
		}
		if err := dependencies.installAndStart(options); err != nil {
			fmt.Fprintf(errorOutput, "install and start Windows Service: %v\n", err)
			return 1
		}
		fmt.Fprintf(output, "Windows Service %s installed and started.\n", options.Name)
		return 0
	case "uninstall":
		flags := flag.NewFlagSet("service uninstall", flag.ContinueOnError)
		flags.SetOutput(errorOutput)
		name := flags.String("name", defaultServiceName, "Windows Service name")
		if err := flags.Parse(arguments[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if flags.NArg() != 0 || !serviceNamePattern.MatchString(*name) {
			fmt.Fprintln(errorOutput, "service uninstall requires a valid --name and no positional arguments")
			return 2
		}
		if err := dependencies.uninstall(*name); err != nil {
			fmt.Fprintf(errorOutput, "uninstall Windows Service: %v\n", err)
			return 1
		}
		fmt.Fprintf(output, "Windows Service %s stopped and uninstalled.\n", *name)
		return 0
	case "run":
		options, code := parseServiceOptions("run", arguments[1:], errorOutput, true)
		if code < 0 {
			return 0
		}
		if code != 0 {
			return code
		}
		if err := dependencies.runDispatcher(options); err != nil {
			fmt.Fprintf(errorOutput, "run Windows Service dispatcher: %v\n", err)
			return 1
		}
		return 0
	default:
		writeServiceUsage(errorOutput)
		return 2
	}
}

func parseServiceOptions(command string, arguments []string, errorOutput io.Writer, requireConfig bool) (serviceInstallOptions, int) {
	flags := flag.NewFlagSet("service "+command, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	name := flags.String("name", defaultServiceName, "Windows Service name")
	configPath := flags.String("config", "", "configuration file path")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return serviceInstallOptions{}, -1
		}
		return serviceInstallOptions{}, 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(errorOutput, "service %s does not accept positional arguments\n", command)
		return serviceInstallOptions{}, 2
	}
	if !serviceNamePattern.MatchString(*name) {
		fmt.Fprintln(errorOutput, "invalid Windows Service name")
		return serviceInstallOptions{}, 2
	}
	if requireConfig && *configPath == "" {
		fmt.Fprintf(errorOutput, "service %s requires --config\n", command)
		return serviceInstallOptions{}, 2
	}
	return serviceInstallOptions{Name: *name, ConfigPath: *configPath}, 0
}

func writeServiceUsage(output io.Writer) {
	fmt.Fprintln(output, "usage:")
	fmt.Fprintln(output, "  opcda-access-adapter service install --config FILE [--name NAME]")
	fmt.Fprintln(output, "  opcda-access-adapter service uninstall [--name NAME]")
	fmt.Fprintln(output, "  opcda-access-adapter service run --config FILE --name NAME  (SCM internal)")
}
