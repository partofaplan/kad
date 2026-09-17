// Package cli is kad's command surface.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/partofaplan/kad/internal/config"
	"github.com/partofaplan/kad/internal/runner"
)

// Version is stamped at build time by the Makefile.
var Version = "dev"

const defaultConfigFile = "kad.yaml"

// ErrUsage signals a usage problem, which exits 2 rather than 1.
var ErrUsage = errors.New("usage")

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, env *Env, args []string) error
}

// Env is everything a command needs from the outside world.
type Env struct {
	Runner runner.Runner
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
}

var commands = []command{
	{"init", "write a starter kad.yaml", runInit},
	{"doctor", "check this machine can build the environment", runDoctor},
	{"up", "create the cluster and install everything declared", runUp},
	{"status", "show what is running and how to reach it", runStatus},
	{"down", "delete the cluster and everything in it", runDown},
	{"catalog", "list the tools available by name", runCatalog},
	{"version", "print the kad version", runVersion},
}

// Main is the entry point. It returns a process exit code.
func Main(args []string) int {
	return Run(&Env{Runner: &runner.Exec{}, In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, args)
}

// Run dispatches one command against an explicit environment, so the command
// surface can be exercised in tests without a cluster or a real terminal.
func Run(env *Env, args []string) int {
	if len(args) < 1 {
		usage(env.Err)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(env.Out)
		return 0
	}

	for _, c := range commands {
		if c.name != args[0] {
			continue
		}
		err := c.run(context.Background(), env, args[1:])
		switch {
		case err == nil:
			return 0
		case errors.Is(err, ErrUsage):
			fmt.Fprintf(env.Err, "kad %s: %v\n", c.name, err)
			return 2
		default:
			fmt.Fprintf(env.Err, "\nkad %s: %v\n", c.name, err)
			return 1
		}
	}

	fmt.Fprintf(env.Err, "kad: unknown command %q\n\n", args[0])
	usage(env.Err)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "kad %s — instant Kubernetes development environments\n\n", Version)
	fmt.Fprintf(w, "usage: kad <command> [flags]\n\n")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(w, "\nThe usual path:  kad init  %[1]s  kad doctor  %[1]s  kad up  %[1]s  kad down\n", arrow)
}

// loadConfig resolves the declaration for a command, with a message that says
// what to do when it is missing rather than just reporting ENOENT.
func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		if os.IsNotExist(errors.Unwrap(err)) {
			return nil, fmt.Errorf("no %s here — run 'kad init' to write one", path)
		}
		return nil, err
	}
	return cfg, nil
}

// fileFlag registers the shared -f flag.
func fileFlag(fs *flag.FlagSet) *string {
	return fs.String("f", defaultConfigFile, "path to the environment declaration")
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", ErrUsage, err)
	}
	return nil
}

// confirm reads one typed line of confirmation.
//
// It reads from Env rather than os.Stdin because this guard stands in front of
// the only irreversible thing kad does, and a prompt wired straight to the
// process's stdin cannot be exercised by a test at all.
//
// Every failure to read is an abort. An unanswerable prompt is not consent.
func confirm(in io.Reader) (string, error) {
	if in == nil {
		return "", errors.New("no input stream to read the answer from")
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	// A final line with no trailing newline comes back as (data, io.EOF), and
	// that is a real answer. EOF with nothing before it is not.
	if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func runVersion(_ context.Context, env *Env, _ []string) error {
	fmt.Fprintln(env.Out, Version)
	return nil
}
