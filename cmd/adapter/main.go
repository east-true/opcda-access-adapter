package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func main() {
	arguments := os.Args[1:]
	if printVersion(arguments, os.Stdout) {
		return
	}
	dependencies := utilityDependencies{
		detect:        opcda.DetectLocalServers,
		writeConfig:   app.WriteConfigFileExclusive,
		runForeground: runForegroundConfig,
		service: serviceCommandDependencies{
			installAndStart: installAndStartWindowsService,
			uninstall:       uninstallWindowsService,
			runDispatcher:   runWindowsServiceDispatcher,
		},
	}
	if handled, exitCode := handleUtilityCommand(arguments, os.Stdin, os.Stdout, os.Stderr, dependencies); handled {
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return
	}
	if len(arguments) != 0 {
		slog.Error("unknown command or argument", "argument", arguments[0])
		os.Exit(2)
	}
	config, err := app.LoadConfig()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	if err := runForeground(config); err != nil {
		slog.Error("adapter stopped with an error", "error", err)
		os.Exit(1)
	}
}

func runForegroundConfig(path string) error {
	config, err := app.LoadConfigFile(path)
	if err != nil {
		return err
	}
	return runForeground(config)
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
