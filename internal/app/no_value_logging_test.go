package app

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The adapter must never log a process value. Today that holds for a reason
// stronger than discipline, and this test is what keeps the reason true.
//
// The packages that handle values -- internal/opcda, internal/opcua and the
// frontends -- do not log at all. Every logging call in the adapter is in
// cmd/adapter, ten of them, and none carries a value: they log an address, a
// frontend name, a CLI argument, and errors. So the only way a value could
// reach a log is inside an error message, and no error message carries one --
// they carry the VARTYPE instead, which is the thing worth reporting.
//
// The real-DA validation greps the adapter's log files for a leak, but a grep
// can only find the shapes it was told to look for, and it runs on one server
// on one runner. This runs everywhere, every time, and fails when the property
// stops being structural rather than when a leak happens to be observed.
func TestValueHandlingPackagesDoNotLog(t *testing.T) {
	root := repositoryRoot(t)
	fileSet := token.NewFileSet()
	found := 0

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// The validation probes are test harnesses: they print their own
			// findings, and that they print no value is asserted separately by
			// the run that executes them.
			if entry.Name() == "validation" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if importPath == "log" || strings.HasPrefix(importPath, "log/") {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %q.\n"+
					"This package handles process values, and the adapter's guarantee that it "+
					"never logs one rests on these packages not logging at all. If this file "+
					"genuinely needs to log, the guarantee has to be re-established some other "+
					"way first -- and the real-DA log grep is not it, since it only matches the "+
					"shapes it was told to look for.", relative, importPath)
			}
			found++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}
	if found == 0 {
		t.Fatal("no imports were examined, so this test proved nothing")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("no go.mod above the working directory")
		}
		directory = parent
	}
}
