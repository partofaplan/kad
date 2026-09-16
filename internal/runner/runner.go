// Package runner wraps external command execution behind an interface.
//
// Every shell-out in kad goes through a Runner. That is what lets the rest of
// the codebase be tested without minikube, helm or a cluster being present —
// the SDLC gate for this project requires the self-contained checks to pass on
// a machine that has none of them.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Result is the outcome of one command.
type Result struct {
	Stdout string
	Stderr string
	Code   int
}

// Runner executes external commands and locates binaries on PATH.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
	// Stream runs a command with its output attached to the terminal, for
	// long operations where progress matters more than capture.
	Stream(ctx context.Context, name string, args ...string) error
	// Look reports the absolute path of a binary, or an error if absent.
	Look(name string) (string, error)
}

// Exec is the real Runner.
type Exec struct {
	// Env, when non-nil, replaces the environment for every command.
	Env []string
}

func (e *Exec) Run(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if e.Env != nil {
		cmd.Env = e.Env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Code:   cmd.ProcessState.ExitCode(),
	}
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return res, fmt.Errorf("%s: %w", name, err)
		}
		// A non-zero exit is data, not a transport failure; callers decide.
		return res, &ExitError{Cmd: name + " " + strings.Join(args, " "), Result: res}
	}
	return res, nil
}

func (e *Exec) Stream(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if e.Env != nil {
		cmd.Env = e.Env
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (e *Exec) Look(name string) (string, error) { return exec.LookPath(name) }

// ExitError reports a command that ran but exited non-zero.
type ExitError struct {
	Cmd    string
	Result Result
}

func (x *ExitError) Error() string {
	msg := strings.TrimSpace(x.Result.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(x.Result.Stdout)
	}
	return fmt.Sprintf("%s: exit %d: %s", x.Cmd, x.Result.Code, msg)
}
