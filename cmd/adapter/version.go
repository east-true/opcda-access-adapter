package main

import (
	"fmt"
	"io"
	"runtime"

	"github.com/east-true/opcda-access-adapter/internal/app"
)

// version and commit are set only by the release build. Source builds retain
// explicit development values rather than inferring mutable repository state.
var (
	version = "dev"
	commit  = "unknown"
)

// withBuildInfo stamps the build this binary was made from onto a loaded
// configuration. The build describes the binary rather than the deployment, so
// it comes from here and not from the configuration file. The standard OPC UA
// Server BuildInfo reports it, which is where a client looks to find out what
// it is talking to.
func withBuildInfo(config app.Config) app.Config {
	config.OPCUA.SoftwareVersion = version
	config.OPCUA.BuildNumber = commit
	return config
}

func printVersion(arguments []string, output io.Writer) bool {
	if len(arguments) != 1 || arguments[0] != "--version" {
		return false
	}
	fmt.Fprintf(output, "opcda-access-adapter version=%s commit=%s goos=%s goarch=%s\n",
		version, commit, runtime.GOOS, runtime.GOARCH)
	return true
}
