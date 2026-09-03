package http

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// canonicalRequestFields is what rejects a case-variant field spelling: a body
// sending "itemid" or "ITEMID" is refused rather than silently ignored, so a
// client with a typo learns about it instead of having its value dropped.
//
// The list is written by hand, and its scope is therefore whatever somebody
// remembered to add. A request field missing from it keeps working and quietly
// loses that protection -- nothing fails, and the gap is invisible from either
// side. This is the same shape as the documentation gaps found across this
// package: a check whose coverage is defined by a maintained list rather than
// by the thing it is meant to cover.
//
// So the list is held to the request structures in both directions, and every
// field is required to appear in the reference a client reads.
func TestCanonicalRequestFieldsCoverEveryRequestField(t *testing.T) {
	guarded := canonicalFieldSet()
	declared := requestFieldTags(t)
	if len(declared) < 8 {
		t.Fatalf("only %d request fields were found; the structures moved", len(declared))
	}

	for field := range declared {
		if !guarded[field] {
			t.Errorf("%q is a request field and is not in canonicalRequestFields, "+
				"so a case-variant spelling of it is ignored rather than refused", field)
		}
	}
	for field := range guarded {
		if !declared[field] {
			t.Errorf("canonicalRequestFields guards %q, which is not a field of any request", field)
		}
	}

	reference := readNormalised(t, repositoryRootFromTest(t), "docs", "http-api.md")
	for field := range declared {
		if !strings.Contains(reference, `"`+field+`"`) {
			t.Errorf("%q is a request field and the HTTP reference never shows it", field)
		}
	}
}

func canonicalFieldSet() map[string]bool {
	set := map[string]bool{}
	for _, field := range canonicalRequestFields {
		set[field] = true
	}
	return set
}

// requestFieldTags is every JSON field name a request structure declares, read
// from the source so a new field is covered by being written rather than by
// being remembered.
func requestFieldTags(t *testing.T) map[string]bool {
	t.Helper()
	directory := filepath.Dir(testFile(t))
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read the frontend package: %v", err)
	}
	structure := regexp.MustCompile(`(?s)type (\w*[Rr]equest\w*) struct \{(.*?)\n\}`)
	tag := regexp.MustCompile(`json:"([a-zA-Z0-9_]+)`)
	fields := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || isTestFile(name) {
			continue
		}
		for _, match := range structure.FindAllStringSubmatch(readNormalised(t, directory, name), -1) {
			for _, field := range tag.FindAllStringSubmatch(match[2], -1) {
				fields[field[1]] = true
			}
		}
	}
	return fields
}
