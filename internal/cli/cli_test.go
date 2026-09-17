package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/partofaplan/kad/internal/catalog"
	"github.com/partofaplan/kad/internal/config"
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
	c.env = &Env{Runner: c.fake, In: strings.NewReader(""), Out: c.out, Err: c.err}
	return c
}

// answers wires typed input to the command's stdin.
func (c *capture) answers(s string) *capture {
	c.env.In = strings.NewReader(s)
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

// 'kad init' must never write a file that 'kad up' then refuses — that is the
// same broken promise as a preflight check passing a config minikube rejects.
// Swept across pool sizes because the floor, the 75% ratio and the 8Gi cap each
// take over in a different range.
func TestInitNeverWritesAConfigItsOwnParserRejects(t *testing.T) {
	for _, poolGi := range []int{1, 2, 4, 5, 6, 8, 12, 16, 32, 64} {
		for _, cpus := range []int{1, 2, 4, 12, 32} {
			name := fmt.Sprintf("%dGi/%dcpu", poolGi, cpus)
			t.Run(name, func(t *testing.T) {
				inDir(t, func(dir string) {
					c := newCapture()
					c.fake.Responses["docker info --format"] = runner.Result{
						Stdout: fmt.Sprintf("%d %d\n", int64(poolGi)*1024*1024*1024, cpus),
					}
					if code := Run(c.env, []string{"init", "-name", "sized"}); code != 0 {
						t.Fatalf("init exit = %d: %s", code, c.err)
					}

					body, err := os.ReadFile(filepath.Join(dir, "kad.yaml"))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := config.Parse(body); err != nil {
						t.Fatalf("init wrote a config its own parser rejects:\n%v\n\n%s", err, body)
					}
				})
			})
		}
	}
}

// The note explains where the number came from, so it must not talk a user out
// of a machine that works. A 5000Mi pool clamps to kad's 4096Mi minimum, but
// 5000Mi is perfectly usable and must not be called "not enough".
func TestInitDoesNotCallAUsableMachineTooSmall(t *testing.T) {
	inDir(t, func(dir string) {
		c := newCapture()
		c.fake.Responses["docker info --format"] = runner.Result{
			Stdout: fmt.Sprintf("%d 4\n", 5000*1024*1024), // 5000Mi: over kad's minimum
		}
		if code := Run(c.env, []string{"init", "-name", "sized"}); code != 0 {
			t.Fatalf("init exit = %d: %s", code, c.err)
		}
		body, _ := os.ReadFile(filepath.Join(dir, "kad.yaml"))
		if strings.Contains(string(body), "not enough") {
			t.Errorf("a 5000Mi pool was described as insufficient:\n%s", body)
		}
	})
}

// --- the confirmation guard on the one irreversible thing kad does ---

// The happy path: the name typed matches, so the cluster goes.
func TestDownDeletesWhenTheNameIsTyped(t *testing.T) {
	inDir(t, func(dir string) {
		Run(newCapture().env, []string{"init", "-name", "demo"})

		c := newCapture().answers("demo\n")
		c.fake.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"kad-demo"}]}`}
		if code := Run(c.env, []string{"down"}); code != 0 {
			t.Fatalf("exit = %d: %s", code, c.err)
		}
		if !c.fake.Called("minikube delete --profile=kad-demo") {
			t.Errorf("a confirmed teardown did not run: %v", c.fake.Calls)
		}
	})
}

// Everything that is not the environment name must abort, and must abort
// BEFORE minikube is asked to delete anything.
func TestDownAbortsWithoutTheRightAnswer(t *testing.T) {
	cases := map[string]string{
		"a different name": "prod\n",
		"an empty line":    "\n",
		"whitespace":       "   \n",
		"the profile name": "kad-demo\n",
		"yes":              "y\n",
		"eof":              "",
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			inDir(t, func(dir string) {
				Run(newCapture().env, []string{"init", "-name", "demo"})

				c := newCapture().answers(answer)
				c.fake.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"kad-demo"}]}`}
				if code := Run(c.env, []string{"down"}); code == 0 {
					t.Errorf("exit = 0 for answer %q; the teardown was not confirmed", answer)
				}
				if c.fake.Called("minikube delete") {
					t.Errorf("answer %q deleted the cluster anyway: %v", answer, c.fake.Calls)
				}
			})
		})
	}
}

// A prompt nobody can answer is not consent.
func TestDownAbortsWhenTheAnswerCannotBeRead(t *testing.T) {
	inDir(t, func(dir string) {
		Run(newCapture().env, []string{"init", "-name", "demo"})

		c := newCapture()
		c.env.In = errReader{}
		c.fake.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"kad-demo"}]}`}
		if code := Run(c.env, []string{"down"}); code == 0 {
			t.Error("exit = 0 although the confirmation could not be read")
		}
		if c.fake.Called("minikube delete") {
			t.Errorf("deleted the cluster without a readable answer: %v", c.fake.Calls)
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("stdin is gone") }

// The bug: any minikube failure read as "absent", so 'kad down' reported a
// successful teardown it had not performed. Exit 0 here means the user stops
// looking for a cluster that is still running.
func TestDownFailsWhenItCannotTellWhetherTheClusterExists(t *testing.T) {
	inDir(t, func(dir string) {
		Run(newCapture().env, []string{"init", "-name", "demo"})

		c := newCapture()
		c.fake.Errors["profile list"] = errors.New(`minikube: executable file not found in $PATH`)
		if code := Run(c.env, []string{"down", "-y"}); code == 0 {
			t.Fatal("down exited 0 without establishing whether the cluster was there")
		}
		if !strings.Contains(c.err.String(), "may still exist") {
			t.Errorf("the failure does not warn the cluster may survive: %q", c.err)
		}
		if c.fake.Called("minikube delete") {
			t.Errorf("tried to delete regardless: %v", c.fake.Calls)
		}
	})
}

// Every kubectl command 'kad status' prints has to target the cluster kad
// built. 'kad up' passes --keep-context=true on purpose, so the user's
// current context is deliberately not this one.
func TestStatusPrintsNoContextlessKubectl(t *testing.T) {
	inDir(t, func(dir string) {
		Run(newCapture().env, []string{"init", "-name", "demo"})
		if err := os.WriteFile("kad.yaml", []byte(`apiVersion: kad.aviture.dev/v1alpha1
kind: Environment
name: demo
cluster:
  driver: docker
  kubernetes: v1.31.0
  cpus: 2
  memory: 4Gi
  disk: 40Gi
tools: [ingress, nexus, minio]
`), 0o644); err != nil {
			t.Fatal(err)
		}

		c := newCapture()
		c.fake.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"kad-demo"}]}`}
		if code := Run(c.env, []string{"status"}); code != 0 {
			t.Fatalf("exit = %d: %s", code, c.err)
		}

		out := c.out.String()
		if strings.Contains(out, catalog.ContextPlaceholder) {
			t.Errorf("status printed an unsubstituted placeholder:\n%s", out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "kubectl ") && !strings.Contains(line, "--context kad-demo") {
				t.Errorf("kubectl command with no context, so it runs against the wrong cluster:\n  %s", line)
			}
		}
		// The note is the line that regressed; prove it was rendered at all.
		if !strings.Contains(out, "kubectl --context kad-demo exec -n nexus") {
			t.Errorf("nexus note missing or unrendered:\n%s", out)
		}
	})
}
