package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/partofaplan/kad/internal/runner"
)

type capture struct {
	env  *Env
	out  *bytes.Buffer
	err  *bytes.Buffer
	fake *runner.Fake
}

func newCapture() *capture {
	c := &capture{out: &bytes.Buffer{}, err: &bytes.Buffer{}, fake: runner.NewFake()}
	c.env = &Env{Runner: c.fake, Out: c.out, Err: c.err}
	return c
}

// inDir runs f with the process working directory set to a fresh temp dir.
func inDir(t *testing.T, f func(dir string)) {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prev) })
	f(dir)
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	c := newCapture()
	if code := Run(c.env, []string{"launch"}); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(c.err.String(), "unknown command") {
		t.Errorf("stderr does not explain: %q", c.err)
	}
}

func TestNoArgsShowsUsage(t *testing.T) {
	c := newCapture()
	if code := Run(c.env, nil); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestHelpExitsZero(t *testing.T) {
	c := newCapture()
	if code := Run(c.env, []string{"--help"}); code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if !strings.Contains(c.out.String(), "kad init") {
		t.Errorf("help does not show the usual path: %q", c.out)
	}
}

// The file 'kad init' writes must be one 'kad up' accepts. These are the two
// halves of the first thing every user does.
func TestInitWritesAValidDeclaration(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		if code := Run(c.env, []string{"init", "-name", "demo"}); code != 0 {
			t.Fatalf("init exit = %d: %s", code, c.err)
		}
		if _, err := os.Stat(filepath.Join(dir, "kad.yaml")); err != nil {
			t.Fatalf("init wrote no kad.yaml: %v", err)
		}

		c2 := newCapture()
		if code := Run(c2.env, []string{"up", "-dry-run"}); code != 0 {
			t.Fatalf("up -dry-run rejected the file init wrote: exit %d: %s", code, c2.err)
		}
		if !strings.Contains(c2.out.String(), "kad-demo") {
			t.Errorf("plan does not name the profile: %q", c2.out)
		}
	})
}

func TestInitDerivesTheNameFromTheDirectory(t *testing.T) {
	inDir(t, func(dir string) {
		sub := filepath.Join(dir, "My_Project.v2")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(sub); err != nil {
			t.Fatal(err)
		}

		c := newCapture()
		if code := Run(c.env, []string{"init"}); code != 0 {
			t.Fatalf("init exit = %d: %s", code, c.err)
		}
		body, err := os.ReadFile(filepath.Join(sub, "kad.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "name: my-project-v2") {
			t.Errorf("name not sanitised from the directory:\n%s", body)
		}
	})
}

func TestInitRefusesToClobber(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		Run(c.env, []string{"init", "-name", "demo"})

		c2 := newCapture()
		if code := Run(c2.env, []string{"init", "-name", "other"}); code == 0 {
			t.Error("init overwrote an existing declaration without -force")
		}

		c3 := newCapture()
		if code := Run(c3.env, []string{"init", "-name", "other", "-force"}); code != 0 {
			t.Errorf("init -force exit = %d: %s", code, c3.err)
		}
	})
}

// A dry run must not touch anything.
func TestDryRunShellsOutToNothing(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		Run(c.env, []string{"init", "-name", "demo"})

		c2 := newCapture()
		if code := Run(c2.env, []string{"up", "-dry-run"}); code != 0 {
			t.Fatalf("exit = %d: %s", code, c2.err)
		}
		if len(c2.fake.Calls) != 0 {
			t.Errorf("dry run executed commands: %v", c2.fake.Calls)
		}
	})
}

func TestCommandsWithoutADeclarationSayWhatToDo(t *testing.T) {
	for _, cmd := range []string{"up", "down", "status", "doctor"} {
		t.Run(cmd, func(t *testing.T) {
			inDir(t, func(dir string) {
				c := newCapture()
				if code := Run(c.env, []string{cmd}); code == 0 {
					t.Fatalf("%s succeeded with no kad.yaml", cmd)
				}
				if !strings.Contains(c.err.String(), "kad init") {
					t.Errorf("%s does not suggest 'kad init': %q", cmd, c.err)
				}
			})
		})
	}
}

func TestDownOnAnAbsentClusterIsNotAnError(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		Run(c.env, []string{"init", "-name", "demo"})

		c2 := newCapture()
		c2.fake.Responses["profile list"] = runner.Result{Stdout: `{"valid":[]}`}
		if code := Run(c2.env, []string{"down", "-y"}); code != 0 {
			t.Errorf("exit = %d, want 0 when there is nothing to remove: %s", code, c2.err)
		}
		if c2.fake.Called("minikube delete") {
			t.Error("down tried to delete a cluster that does not exist")
		}
	})
}

func TestDownDeletesOnlyItsOwnProfile(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		Run(c.env, []string{"init", "-name", "demo"})

		c2 := newCapture()
		c2.fake.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"kad-demo"},{"Name":"minikube"}]}`}
		if code := Run(c2.env, []string{"down", "-y"}); code != 0 {
			t.Fatalf("exit = %d: %s", code, c2.err)
		}
		if !c2.fake.Called("minikube delete --profile=kad-demo") {
			t.Errorf("down did not delete its profile: %v", c2.fake.Calls)
		}
		for _, call := range c2.fake.Calls {
			if strings.Contains(call, "delete") && !strings.Contains(call, "kad-demo") {
				t.Errorf("down deleted something else: %q", call)
			}
		}
	})
}

func TestCatalogListsEveryTool(t *testing.T) {
	c := newCapture()
	if code := Run(c.env, []string{"catalog"}); code != 0 {
		t.Fatalf("exit = %d: %s", code, c.err)
	}
	for _, want := range []string{"harbor", "observability", "postgres", "ingress"} {
		if !strings.Contains(c.out.String(), want) {
			t.Errorf("catalog output is missing %q:\n%s", want, c.out)
		}
	}
}

func TestBadDeclarationFailsBeforeTouchingAnything(t *testing.T) {
	inDir(t, func(dir string) {
		os.WriteFile("kad.yaml", []byte("apiVersion: kad.aviture.dev/v1alpha1\nkind: Environment\nname: BAD NAME\n"), 0o644)

		c := newCapture()
		if code := Run(c.env, []string{"up"}); code == 0 {
			t.Fatal("up accepted an invalid declaration")
		}
		if len(c.fake.Calls) != 0 {
			t.Errorf("up ran commands despite an invalid declaration: %v", c.fake.Calls)
		}
	})
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"My_Project.v2": "my-project-v2",
		"kad":           "kad",
		"---":           "dev",
		"":              "dev",
		"Team Alpha":    "team-alpha",
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

// A name the user typed is their input, not a kad defect. Reporting it as an
// internal bug sends people to file issues against their own typing.
func TestInitRejectsABadNameAsUsageNotAsAKadBug(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		code := Run(c.env, []string{"init", "-name", "MyProject"})

		if code != 2 {
			t.Errorf("exit = %d, want 2 (usage)", code)
		}
		if strings.Contains(c.err.String(), "kad bug") {
			t.Errorf("blamed kad for the user's input: %q", c.err)
		}
		if !strings.Contains(c.err.String(), "myproject") {
			t.Errorf("error does not suggest a valid name: %q", c.err)
		}
		if _, err := os.Stat(filepath.Join(dir, "kad.yaml")); err == nil {
			t.Error("a rejected name still wrote a kad.yaml")
		}
	})
}

// kubectl rejects --kube-context outright, and the error used to be swallowed,
// so 'kad status' printed port-forward commands and nothing else while the user
// had no idea the cluster had never been reached.
func TestStatusAsksKubectlWithTheFlagKubectlUnderstands(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		Run(c.env, []string{"init", "-name", "demo"})

		c2 := newCapture()
		c2.fake.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"kad-demo"}]}`}
		if code := Run(c2.env, []string{"status"}); code != 0 {
			t.Fatalf("exit = %d: %s", code, c2.err)
		}
		for _, call := range c2.fake.Calls {
			if strings.HasPrefix(call, "kubectl ") && strings.Contains(call, "--kube-context") {
				t.Errorf("kubectl invoked with helm's flag spelling: %q", call)
			}
		}
		if !c2.fake.Called("kubectl get pods -A --no-headers --context kad-demo") {
			t.Errorf("status did not query pods with --context; calls: %v", c2.fake.Calls)
		}
	})
}

func TestStatusSaysSoWhenItCannotReachTheCluster(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		Run(c.env, []string{"init", "-name", "demo"})

		c2 := newCapture()
		c2.fake.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"kad-demo"}]}`}
		c2.fake.Errors["kubectl get pods"] = errors.New("connection refused")
		if code := Run(c2.env, []string{"status"}); code != 0 {
			t.Fatalf("exit = %d: %s", code, c2.err)
		}
		if !strings.Contains(c2.out.String(), "Could not reach the cluster") {
			t.Errorf("status hid an unreachable cluster:\n%s", c2.out)
		}
	})
}
