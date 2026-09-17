package runner

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Fake is a Runner for tests. Responses are matched against the full command
// line by substring, LONGEST match wins, so a specific pattern beats a general
// one regardless of map order; unmatched commands succeed silently so a test
// only has to describe the calls it cares about.
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

	// Errors and Responses COMPOSE rather than shadowing one another. A
	// command that exits non-zero still produces output, and the real runner
	// hands back both — so a fake that drops the output on error cannot
	// describe the case that matters most: a tool failing while saying why.
	// It silently turned one such test into a vacuous pass.
	var res Result
	if k, ok := bestMatch(line, keysOfResults(f.Responses)); ok {
		res = f.Responses[k]
	}
	if k, ok := bestMatch(line, keysOfErrors(f.Errors)); ok {
		if res.Code == 0 {
			res.Code = 1
		}
		return res, f.Errors[k]
	}
	return res, nil
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
