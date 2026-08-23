package main

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
)

func TestPrintVersion(t *testing.T) {
	previousVersion, previousCommit := version, commit
	version, commit = "v0.0.0-test", "0123456789abcdef0123456789abcdef01234567"
	t.Cleanup(func() { version, commit = previousVersion, previousCommit })

	var output bytes.Buffer
	if !printVersion([]string{"--version"}, &output) {
		t.Fatal("--version was not handled")
	}
	want := fmt.Sprintf("opcda-access-adapter version=v0.0.0-test commit=0123456789abcdef0123456789abcdef01234567 goos=%s goarch=%s\n",
		runtime.GOOS, runtime.GOARCH)
	if output.String() != want {
		t.Fatalf("version output = %q, want %q", output.String(), want)
	}
}

func TestPrintVersionIgnoresOtherArguments(t *testing.T) {
	var output bytes.Buffer
	if printVersion(nil, &output) || printVersion([]string{"--version", "extra"}, &output) ||
		printVersion([]string{"--help"}, &output) {
		t.Fatal("non-version arguments were handled")
	}
	if output.Len() != 0 {
		t.Fatalf("unexpected output %q", output.String())
	}
}
