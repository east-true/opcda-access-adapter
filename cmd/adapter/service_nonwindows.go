//go:build !windows

package main

import "fmt"

func installAndStartWindowsService(serviceInstallOptions) error {
	return fmt.Errorf("Windows Service installation requires Windows")
}

func uninstallWindowsService(string) error {
	return fmt.Errorf("Windows Service removal requires Windows")
}

func runWindowsServiceDispatcher(serviceInstallOptions) error {
	return fmt.Errorf("Windows Service dispatcher requires Windows")
}
