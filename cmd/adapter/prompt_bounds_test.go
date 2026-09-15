package main

import (
	"bytes"
	"strings"
	"testing"
)

// The guided prompt is the one place an operator types into, and both of its
// bounds were unpinned. selectNumber accepts a selection inside a range and
// gives up after a fixed number of tries; readLine bounds a line and has to
// decide what a final line with no newline is. A prompt that quietly narrowed
// its range would refuse a server the operator was just shown, and one that
// quietly widened it would select a server that is not on the list.

func promptOver(text string) (*boundedPrompt, *bytes.Buffer) {
	var output bytes.Buffer
	return newBoundedPrompt(strings.NewReader(text), &output), &output
}

func TestASelectionMayBeEitherEndOfTheRangeItWasOffered(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		typed     string
		maximum   int
		selection int
		accepted  bool
	}{
		{"the first of several", "1\n", 3, 1, true},
		{"the last of several", "3\n", 3, 3, true},
		{"the only one offered", "1\n", 1, 1, true},
		{"one below the first", "0\n", 3, 0, false},
		{"one past the last", "4\n", 3, 0, false},
		{"a negative selection", "-1\n", 3, 0, false},
		{"not a number at all", "two\n", 3, 0, false},
		{"nothing at all", "\n", 3, 0, false},
		// Atoi accepts a leading plus and surrounding spaces are trimmed, so
		// these are the same selection typed differently rather than new rules.
		{"the last one typed with a sign", "+3\n", 3, 3, true},
		{"the last one typed with spaces", "  3  \n", 3, 3, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Enough repetitions that a rejected entry exhausts the attempts
			// rather than running out of input first, which would be a
			// different failure wearing the same exit.
			prompt, _ := promptOver(strings.Repeat(testCase.typed, maximumPromptAttempts+1))
			selection, err := prompt.selectNumber("choose", testCase.maximum)
			if accepted := err == nil; accepted != testCase.accepted {
				t.Fatalf("accepted = %v, want %v (%v)", accepted, testCase.accepted, err)
			}
			if testCase.accepted && selection != testCase.selection {
				t.Errorf("selection = %d, want %d", selection, testCase.selection)
			}
		})
	}
}

// The attempt limit is what stops a prompt fed nonsense from looping forever.
// The last allowed attempt must still be read, or an operator who mistypes
// twice is refused on the try the limit says they have.
func TestTheLastAllowedAttemptIsStillRead(t *testing.T) {
	invalid := strings.Repeat("0\n", maximumPromptAttempts-1)
	prompt, output := promptOver(invalid + "2\n")
	selection, err := prompt.selectNumber("choose", 3)
	if err != nil {
		t.Fatalf("a valid selection on the last allowed attempt was refused: %v", err)
	}
	if selection != 2 {
		t.Errorf("selection = %d, want 2", selection)
	}
	if got := strings.Count(output.String(), "Invalid selection."); got != maximumPromptAttempts-1 {
		t.Errorf("the prompt complained %d times, want %d", got, maximumPromptAttempts-1)
	}

	// One more invalid entry than the limit allows ends it, and the entry
	// after that is never read.
	tooMany, _ := promptOver(strings.Repeat("0\n", maximumPromptAttempts) + "2\n")
	if _, err := tooMany.selectNumber("choose", 3); err == nil {
		t.Error("a selection was accepted after the attempts ran out")
	}
}

// A file or a pipe may end without a trailing newline, and the last line is
// still a line. Discarding it would refuse the final answer of an otherwise
// complete script -- while an empty read at the end is genuinely the end.
func TestAFinalLineWithoutANewlineIsStillALine(t *testing.T) {
	prompt, _ := promptOver("answer")
	line, err := prompt.readLine()
	if err != nil {
		t.Fatalf("a final line without a newline was refused: %v", err)
	}
	if line != "answer" {
		t.Errorf("line = %q, want %q", line, "answer")
	}

	empty, _ := promptOver("")
	if _, err := empty.readLine(); err == nil {
		t.Error("an input that ended with nothing in it was read as a line")
	}

	// A line at the buffer's size is refused rather than silently cut, because
	// half an answer is an answer the operator did not give.
	overLong, _ := promptOver(strings.Repeat("x", maximumPromptLineBytes+1) + "\n")
	if _, err := overLong.readLine(); err == nil {
		t.Error("a line past the buffer was accepted")
	}
}

// A registered server need not have a ProgID, and the setup list shows a
// placeholder when it does not. Showing an empty column instead would leave
// the operator selecting by position alone with nothing saying why.
func TestAMissingProgIDIsNamedRatherThanLeftBlank(t *testing.T) {
	if got := displayOptional(""); got == "" {
		t.Error("an absent value displayed as nothing")
	}
	if got := displayOptional("Vendor.Server.1"); got != "Vendor.Server.1" {
		t.Errorf("a present value displayed as %q", got)
	}
}
