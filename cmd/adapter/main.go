package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/app"
)

func main() {
	if printVersion(os.Args[1:], os.Stdout) {
		return
	}
	config, err := app.LoadConfig()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	service, err := app.New(config, nil)
	if err != nil {
		slog.Error("create service", "error", err)
		os.Exit(1)
	}
	if err := service.Start(); err != nil {
		slog.Error("start service", "error", err)
		os.Exit(1)
	}
	slog.Info("HTTP listener started", "address", service.Address())

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	exitCode := 0
	select {
	case <-signals:
	case err := <-service.Errors():
		slog.Error("HTTP listener failed", "error", err)
		exitCode = 1
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownContext); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("shutdown service", "error", err)
		exitCode = 1
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
