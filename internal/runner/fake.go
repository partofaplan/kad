package runner

import (
	"context"
	"fmt"
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
	for k, err := range f.Errors {
		if strings.Contains(line, k) {
			return Result{Code: 1}, err
		}
	}
	for k, res := range f.Responses {
		if strings.Contains(line, k) {
			return res, nil
		}
	}
	return Result{}, nil
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
