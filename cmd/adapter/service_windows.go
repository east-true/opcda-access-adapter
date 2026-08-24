//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/app"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	windowsServiceAccount = `NT AUTHORITY\LocalService`
	serviceStateTimeout   = 30 * time.Second
)

func installAndStartWindowsService(options serviceInstallOptions) error {
	resolved, executable, err := resolveServiceFiles(options)
	if err != nil {
		return err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to local Service Control Manager (Administrator required): %w", err)
	}
	defer manager.Disconnect()

	if existing, openErr := manager.OpenService(resolved.Name); openErr == nil {
		_ = existing.Close()
		return fmt.Errorf("Windows Service %q already exists; setup never replaces an existing service", resolved.Name)
	} else if !errors.Is(openErr, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return fmt.Errorf("check existing Windows Service: %w", openErr)
	}

	service, err := manager.CreateService(
		resolved.Name,
		executable,
		mgr.Config{
			StartType:        mgr.StartAutomatic,
			ErrorControl:     mgr.ErrorNormal,
			ServiceStartName: windowsServiceAccount,
			DisplayName:      "OPC DA Access Adapter (" + resolved.Name + ")",
			Description:      "DA-native HTTP access to one explicitly configured local OPC DA server",
		},
		"service", "run", "--name", resolved.Name, "--config", resolved.ConfigPath,
	)
	if err != nil {
		return fmt.Errorf("create Windows Service: %w", err)
	}
	defer service.Close()
	if err := eventlog.InstallAsEventCreate(resolved.Name, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		deleteErr := service.Delete()
		return errors.Join(fmt.Errorf("install Windows Event Log source: %w", err), wrapCleanup("delete service after Event Log setup failure", deleteErr))
	}
	if err := service.Start(); err != nil {
		deleteErr := service.Delete()
		eventErr := eventlog.Remove(resolved.Name)
		return errors.Join(
			fmt.Errorf("start Windows Service: %w", err),
			wrapCleanup("delete service after start failure", deleteErr),
			wrapCleanup("remove Event Log source after start failure", eventErr),
		)
	}
	if _, err := waitForServiceState(service, svc.Running, serviceStateTimeout); err != nil {
		cleanupErr := stopAndDeleteService(service)
		eventErr := eventlog.Remove(resolved.Name)
		return errors.Join(fmt.Errorf("wait for Windows Service startup: %w", err), cleanupErr, wrapCleanup("remove Event Log source after startup failure", eventErr))
	}
	return nil
}

func uninstallWindowsService(name string) error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to local Service Control Manager (Administrator required): %w", err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(name)
	if err != nil {
		return fmt.Errorf("open Windows Service %q: %w", name, err)
	}
	defer service.Close()
	serviceErr := stopAndDeleteService(service)
	if serviceErr != nil {
		return serviceErr
	}
	if err := eventlog.Remove(name); err != nil {
		return fmt.Errorf("remove Windows Event Log source: %w", err)
	}
	return nil
}

func stopAndDeleteService(service *mgr.Service) error {
	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query Windows Service before removal: %w", err)
	}
	var stopErr error
	if status.State != svc.Stopped {
		if _, err := service.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			stopErr = fmt.Errorf("request Windows Service stop: %w", err)
		} else if _, err := waitForServiceState(service, svc.Stopped, serviceStateTimeout); err != nil {
			stopErr = fmt.Errorf("wait for Windows Service stop: %w", err)
		}
	}
	if stopErr != nil {
		return stopErr
	}
	if err := service.Delete(); err != nil {
		return fmt.Errorf("delete Windows Service: %w", err)
	}
	return nil
}

func waitForServiceState(service *mgr.Service, wanted svc.State, timeout time.Duration) (svc.Status, error) {
	deadline := time.Now().Add(timeout)
	for {
		status, err := service.Query()
		if err != nil {
			return svc.Status{}, err
		}
		if status.State == wanted {
			return status, nil
		}
		if wanted == svc.Running && status.State == svc.Stopped {
			return status, fmt.Errorf("service stopped during startup (win32=%d service=%d)", status.Win32ExitCode, status.ServiceSpecificExitCode)
		}
		if time.Now().After(deadline) {
			return status, fmt.Errorf("timed out in state %d waiting for %d", status.State, wanted)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func resolveServiceFiles(options serviceInstallOptions) (serviceInstallOptions, string, error) {
	if !serviceNamePattern.MatchString(options.Name) {
		return serviceInstallOptions{}, "", fmt.Errorf("invalid Windows Service name")
	}
	configPath, err := filepath.Abs(options.ConfigPath)
	if err != nil {
		return serviceInstallOptions{}, "", fmt.Errorf("resolve configuration path: %w", err)
	}
	if _, err := app.LoadConfigFile(configPath); err != nil {
		return serviceInstallOptions{}, "", fmt.Errorf("validate service configuration: %w", err)
	}
	configInfo, err := os.Stat(configPath)
	if err != nil {
		return serviceInstallOptions{}, "", fmt.Errorf("inspect service configuration: %w", err)
	}
	if !configInfo.Mode().IsRegular() {
		return serviceInstallOptions{}, "", fmt.Errorf("service configuration must be a regular file")
	}
	executable, err := os.Executable()
	if err != nil {
		return serviceInstallOptions{}, "", fmt.Errorf("locate adapter executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return serviceInstallOptions{}, "", fmt.Errorf("resolve adapter executable: %w", err)
	}
	return serviceInstallOptions{Name: options.Name, ConfigPath: configPath}, executable, nil
}

func runWindowsServiceDispatcher(options serviceInstallOptions) error {
	if !serviceNamePattern.MatchString(options.Name) {
		return fmt.Errorf("invalid Windows Service name")
	}
	configPath, err := filepath.Abs(options.ConfigPath)
	if err != nil {
		return fmt.Errorf("resolve service configuration: %w", err)
	}
	return svc.Run(options.Name, &adapterServiceHandler{serviceName: options.Name, configPath: configPath})
}

type adapterServiceHandler struct {
	serviceName string
	configPath  string
}

func (handler *adapterServiceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	log, logErr := eventlog.Open(handler.serviceName)
	if logErr == nil {
		defer log.Close()
	}
	statuses <- svc.Status{State: svc.StartPending, WaitHint: 15000}
	config, err := app.LoadConfigFile(handler.configPath)
	if err != nil {
		slog.Error("load service configuration", "error", err)
		writeServiceEventError(log, "load service configuration: "+err.Error())
		return false, 1
	}
	service, err := app.New(config, nil)
	if err != nil {
		slog.Error("create adapter service", "error", err)
		writeServiceEventError(log, "create adapter: "+err.Error())
		return false, 1
	}
	if err := service.Start(); err != nil {
		slog.Error("start adapter service", "error", err)
		writeServiceEventError(log, "start adapter: "+err.Error())
		return false, 1
	}
	current := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	statuses <- current
	slog.Info("adapter Windows Service started", "address", service.Address())
	writeServiceEventInfo(log, "OPC DA Access Adapter started on "+service.Address())

	exitCode := uint32(0)
	running := true
	for running {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- current
			case svc.Stop, svc.Shutdown:
				running = false
			}
		case err := <-service.Errors():
			slog.Error("adapter HTTP listener failed", "error", err)
			writeServiceEventError(log, "HTTP listener failed: "+err.Error())
			exitCode = 1
			running = false
		}
	}

	statuses <- svc.Status{State: svc.StopPending, WaitHint: 15000}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownContext); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("shutdown adapter Windows Service", "error", err)
		writeServiceEventError(log, "adapter shutdown failed: "+err.Error())
		exitCode = 1
	}
	if exitCode == 0 {
		writeServiceEventInfo(log, "OPC DA Access Adapter stopped")
	}
	return false, exitCode
}

func writeServiceEventInfo(log *eventlog.Log, message string) {
	if log != nil {
		_ = log.Info(1, message)
	}
}

func writeServiceEventError(log *eventlog.Log, message string) {
	if log != nil {
		_ = log.Error(2, message)
	}
}

func wrapCleanup(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
