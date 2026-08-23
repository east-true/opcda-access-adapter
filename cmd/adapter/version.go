package main

import (
	"fmt"
	"io"
	"runtime"
)

// version and commit are set only by the release build. Source builds retain
// explicit development values rather than inferring mutable repository state.
var (
	version = "dev"
	commit  = "unknown"
)

func printVersion(arguments []string, output io.Writer) bool {
	if len(arguments) != 1 || arguments[0] != "--version" {
		return false
	}
	fmt.Fprintf(output, "opcda-access-adapter version=%s commit=%s goos=%s goarch=%s\n",
		version, commit, runtime.GOOS, runtime.GOARCH)
	return true
}
