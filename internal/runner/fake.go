package runner

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Fake is a Runner for tests. Responses are matched against the full command
// line by substring, first match wins; unmatched commands succeed silently so
// a test only has to describe the calls it cares about.
type Fake struct {
	// Responses maps a command-line substring to a canned result.
	Responses map[string]Result
	// Errors maps a command-line substring to an error to return.
	Errors map[string]error
	// Missing lists binaries Look should report as absent.
	Missing []string
	// Calls records every command line executed, in order.
	Calls []string
}

func NewFake() *Fake {
	return &Fake{Responses: map[string]Result{}, Errors: map[string]error{}}
}

func (f *Fake) line(name string, args []string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

func (f *Fake) Run(_ context.Context, name string, args ...string) (Result, error) {
	line := f.line(name, args)
	f.Calls = append(f.Calls, line)

	if k, ok := bestMatch(line, keysOfErrors(f.Errors)); ok {
		return Result{Code: 1}, f.Errors[k]
	}
	if k, ok := bestMatch(line, keysOfResults(f.Responses)); ok {
		return f.Responses[k], nil
	}
	return Result{}, nil
}

// bestMatch returns the LONGEST key that is a substring of line.
//
// Longest rather than first, because Go randomises map iteration: when two
// patterns both match — say "{{.MemTotal}}" and the more specific
// "docker info --format {{.MemTotal}}" — picking whichever came out of the map
// first made the test's result depend on the run. Most specific wins, and the
// outcome is the same every time.
func bestMatch(line string, keys []string) (string, bool) {
	best, found := "", false
	for _, k := range keys {
		if !strings.Contains(line, k) {
			continue
		}
		if !found || len(k) > len(best) {
			best, found = k, true
		}
	}
	return best, found
}

func keysOfResults(m map[string]Result) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out) // stable tiebreak for equal-length keys
	return out
}

func keysOfErrors(m map[string]error) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (f *Fake) Stream(ctx context.Context, name string, args ...string) error {
	_, err := f.Run(ctx, name, args...)
	return err
}

func (f *Fake) Look(name string) (string, error) {
	for _, m := range f.Missing {
		if m == name {
			return "", fmt.Errorf("exec: %q: executable file not found in $PATH", name)
		}
	}
	return "/usr/local/bin/" + name, nil
}

// Called reports whether any recorded call contains sub.
func (f *Fake) Called(sub string) bool {
	for _, c := range f.Calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}
