package app

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// INV-6 forbids a persistent Process Data store: no historian, no time-series
// database, no last-good-value or tag-value cache database, no event history,
// no persistent telemetry. Bounded runtime state in the process, a COM handle
// cache and transient browse state are all explicitly allowed, which is what
// makes the invariant about *persistence* rather than about caching.
//
// Today it holds for a reason stronger than discipline, and the reason is the
// same one that makes the no-logging guarantee hold: the packages that handle
// values -- internal/opcda, internal/opcua and the frontends -- cannot write
// anywhere. They do not import os, database/sql, or any encoder aimed at a
// file. A value they hold has nowhere to go but back to the client that asked.
//
// Nothing recorded that. An added os.WriteFile for a "small cache", or a
// database/sql import for "just the last value", would have broken a headline
// invariant with no test to notice -- and it would look reasonable in review,
// because the thing it stores is genuinely useful. This makes the property
// structural rather than remembered.
func TestValueHandlingPackagesCannotPersist(t *testing.T) {
	root := repositoryRoot(t)
	fileSet := token.NewFileSet()
	inspected := 0

	// Importing one of these is enough to be a finding: the invariant is about
	// what a package is able to do with a value, not about catching the one
	// call that does it. A package with no way to write cannot acquire one by
	// accident.
	forbidden := map[string]string{
		"database/sql": "a database is a persistent value store",
		"os":           "a package that can open a file can write a value to one",
		"encoding/gob": "gob exists to serialise a value for storage or transport",
	}

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// The validation probes are test harnesses. They write their own
			// findings, and what they may report is asserted by the run that
			// executes them.
			if entry.Name() == "validation" {
				return filepath.SkipDir
			}
			// internal/app itself reads and writes the configuration file,
			// which INV-6 allows by name. It handles no process value.
			if entry.Name() == "app" {
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
		inspected++
		relativePath, _ := filepath.Rel(root, path)
		for _, spec := range parsed.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			if reason, bad := forbidden[imported]; bad {
				t.Errorf("%s imports %q.\n"+
					"This package handles process values, and INV-6 forbids a persistent "+
					"Process Data store. That invariant holds because these packages have no "+
					"way to write one: %s. Bounded state in memory stays allowed.",
					relativePath, imported, reason)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}
	if inspected < 20 {
		t.Fatalf("only %d files were inspected; the walk is not reaching the packages", inspected)
	}
}

// The walk must actually reach the value-handling packages. A test that walks
// nothing passes, and that failure mode has already cost this session two
// rounds elsewhere, so the packages are named rather than assumed.
func TestValueStoreWalkReachesTheValuePackages(t *testing.T) {
	root := repositoryRoot(t)
	for _, pkg := range []string{
		filepath.Join("internal", "opcda"),
		filepath.Join("internal", "opcua"),
		filepath.Join("internal", "frontend", "http"),
		filepath.Join("internal", "frontend", "grpc"),
	} {
		matches, err := filepath.Glob(filepath.Join(root, pkg, "*.go"))
		if err != nil || len(matches) == 0 {
			t.Errorf("%s holds no Go files; the invariant tests are walking past it", pkg)
		}
	}
}
