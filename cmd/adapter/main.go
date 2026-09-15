package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// mainDependencies is what the top-level command needs from outside itself.
// Every other command in this package already takes its effects this way; main
// was the one that reached for them directly, which left its exit codes -- the
// only thing an operator's supervisor sees -- untestable.
type mainDependencies struct {
	utility    utilityDependencies
	loadConfig func() (app.Config, error)
	runAdapter func(app.Config) error
}

func productionDependencies() mainDependencies {
	return mainDependencies{
		utility: utilityDependencies{
			detect:        opcda.DetectLocalServers,
			writeConfig:   app.WriteConfigFileExclusive,
			runForeground: runForegroundConfig,
			service: serviceCommandDependencies{
				installAndStart: installAndStartWindowsService,
				uninstall:       uninstallWindowsService,
				runDispatcher:   runWindowsServiceDispatcher,
			},
		},
		loadConfig: app.LoadConfig,
		runAdapter: runForeground,
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, productionDependencies()))
}

// run reports the process exit code rather than ending the process, so every
// path out of it can be exercised.
func run(arguments []string, input io.Reader, output, errorOutput io.Writer, dependencies mainDependencies) int {
	if printVersion(arguments, output) {
		return 0
	}
	if handled, exitCode := handleUtilityCommand(arguments, input, output, errorOutput, dependencies.utility); handled {
		return exitCode
	}
	if len(arguments) != 0 {
		slog.Error("unknown command or argument", "argument", arguments[0])
		return 2
	}
	config, err := dependencies.loadConfig()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		return 2
	}
	if err := dependencies.runAdapter(config); err != nil {
		slog.Error("adapter stopped with an error", "error", err)
		return 1
	}
	return 0
}

func runForegroundConfig(path string) error {
	config, err := app.LoadConfigFile(path)
	if err != nil {
		return err
	}
	return runForeground(withBuildInfo(config))
}

func runForeground(config app.Config) error {
	service, err := app.New(config, nil)
	if err != nil {
		return fmt.Errorf("create adapter: %w", err)
	}
	if err := service.Start(); err != nil {
		return fmt.Errorf("start adapter: %w", err)
	}
	slog.Info("frontend listener started", "frontend", service.Frontend(), "address", service.Address())

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	var listenerErr error
	select {
	case <-signals:
	case listenerErr = <-service.Errors():
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := service.Shutdown(shutdownContext)
	if errors.Is(shutdownErr, context.Canceled) {
		shutdownErr = nil
	}
	return errors.Join(listenerErr, shutdownErr)
}
