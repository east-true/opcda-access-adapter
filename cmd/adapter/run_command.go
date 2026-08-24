package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

func runConfigCommand(arguments []string, _ io.Writer, errorOutput io.Writer, run func(string) error) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	configPath := flags.String("config", "", "reviewed setup configuration file")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errorOutput, "run does not accept positional arguments")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(errorOutput, "run requires --config")
		return 2
	}
	if err := run(*configPath); err != nil {
		fmt.Fprintf(errorOutput, "run configured adapter: %v\n", err)
		return 1
	}
	return 0
}
