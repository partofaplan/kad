package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/partofaplan/kad/internal/config"
	"github.com/partofaplan/kad/internal/runner"
)

func cfg(t *testing.T, extra string) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte("apiVersion: kad.aviture.dev/v1alpha1\nkind: Environment\nname: demo\n" + extra))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return c
}

// setBudget answers every platform's resource probe at once — the host ones and
// the single `docker/podman info --format` call — so a test states the machine
// it means once and asserts the same thing on whichever runner CI gives it.
func setBudget(f *runner.Fake, cpus, memGi int) {
	memBytes := int64(memGi) * 1024 * 1024 * 1024

	f.Responses["sysctl -n hw.ncpu"] = runner.Result{Stdout: fmt.Sprintf("%d\n", cpus)}
	f.Responses["nproc"] = runner.Result{Stdout: fmt.Sprintf("%d\n", cpus)}
	f.Responses["NumberOfLogicalProcessors"] = runner.Result{Stdout: fmt.Sprintf("%d\n", cpus)}

	f.Responses["sysctl -n hw.memsize"] = runner.Result{Stdout: fmt.Sprintf("%d\n", memBytes)}
	f.Responses["grep MemTotal"] = runner.Result{Stdout: fmt.Sprintf("%d\n", int64(memGi)*1024*1024)}
	f.Responses["TotalPhysicalMemory"] = runner.Result{Stdout: fmt.Sprintf("%d\n", memBytes)}

	// Both fields come from one call now, so the fake answers with both.
	f.Responses["docker info --format"] = runner.Result{Stdout: fmt.Sprintf("%d %d\n", memBytes, cpus)}
	f.Responses["podman info --format"] = runner.Result{Stdout: fmt.Sprintf("%d %d\n", memBytes, cpus)}
}

// setDockerBudget overrides only what Docker reports, leaving the host large —
// the shape of the bug these checks exist for.
func setDockerBudget(f *runner.Fake, cpus int, memBytes int64) {
	f.Responses["docker info --format"] = runner.Result{Stdout: fmt.Sprintf("%d %d\n", memBytes, cpus)}
}

// A healthy host, as the fake sees it.
func healthy() *runner.Fake {
	f := runner.NewFake()
	f.Responses["minikube version"] = runner.Result{Stdout: "minikube version: v1.33.1"}
	f.Responses["kubectl version"] = runner.Result{Stdout: "Client Version: v1.31.0"}
	f.Responses["helm version"] = runner.Result{Stdout: "v3.16.2+g9a1b2c3"}
	f.Responses["profile list"] = runner.Result{Stdout: `{"valid":[]}`}
	setBudget(f, 10, 32)
	return f
}

func find(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, r.Checks)
	return Check{}
}

func TestHealthyHostPasses(t *testing.T) {
	rep := Run(context.Background(), healthy(), cfg(t, ""))
	if !rep.OK() {
		t.Errorf("healthy host failed preflight: %+v", rep.Failures())
	}
}

func TestMissingBinaryFailsWithAFix(t *testing.T) {
	f := healthy()
	f.Missing = []string{"helm"}

	c := find(t, Run(context.Background(), f, cfg(t, "")), "helm")
	if c.Status != Fail {
		t.Errorf("missing helm status = %v, want fail", c.Status)
	}
	if c.Fix == "" {
		t.Error("a failing check must tell the user how to fix it")
	}
}

func TestOldToolFails(t *testing.T) {
	f := healthy()
	f.Responses["minikube version"] = runner.Result{Stdout: "minikube version: v1.20.0"}

	c := find(t, Run(context.Background(), f, cfg(t, "")), "minikube")
	if c.Status != Fail {
		t.Errorf("minikube v1.20.0 status = %v, want fail", c.Status)
	}
}

// Helm 4 is ahead of what most charts are tested against, but is not
// known-broken: it must warn without blocking the build.
func TestHelm4WarnsButDoesNotBlock(t *testing.T) {
	f := healthy()
	f.Responses["helm version"] = runner.Result{Stdout: "v4.2.3+g43e8b7f"}

	rep := Run(context.Background(), f, cfg(t, ""))
	c := find(t, rep, "helm")
	if c.Status != Warn {
		t.Errorf("helm 4 status = %v, want warn", c.Status)
	}
	if !rep.OK() {
		t.Error("a warning must not block 'kad up'")
	}
}

func TestStoppedRuntimeFails(t *testing.T) {
	f := healthy()
	f.Errors["docker info"] = context.DeadlineExceeded

	c := find(t, Run(context.Background(), f, cfg(t, "")), "runtime/docker")
	if c.Status != Fail {
		t.Errorf("stopped docker status = %v, want fail", c.Status)
	}
	if !strings.Contains(c.Fix, "start") {
		t.Errorf("fix does not tell the user to start it: %q", c.Fix)
	}
}

// The condition on the machine this was written on: four runtimes installed,
// any of which minikube might pick.
func TestCompetingRuntimesWarn(t *testing.T) {
	rep := Run(context.Background(), healthy(), cfg(t, ""))
	c := find(t, rep, "runtime/ambiguity")
	if c.Status != Warn {
		t.Errorf("status = %v, want warn when several runtimes are installed", c.Status)
	}
	if !strings.Contains(c.Fix, "docker context") {
		t.Errorf("fix does not name the remedy: %q", c.Fix)
	}
	if !rep.OK() {
		t.Error("ambiguity is a warning, not a blocker")
	}
}

func TestSingleRuntimeDoesNotWarn(t *testing.T) {
	f := healthy()
	f.Missing = []string{"podman", "colima", "rancher-desktop", "lima"}

	c := find(t, Run(context.Background(), f, cfg(t, "")), "runtime/ambiguity")
	if c.Status != OK {
		t.Errorf("status = %v, want ok with one runtime installed", c.Status)
	}
}

func TestOvercommittedMemoryFails(t *testing.T) {
	f := healthy()
	setBudget(f, 10, 8)

	c := find(t, Run(context.Background(), f, cfg(t, "cluster:\n  memory: 16Gi\n")), "budget/docker/memory")
	if c.Status != Fail {
		t.Errorf("status = %v, want fail when asking for more memory than the host has", c.Status)
	}
	if !strings.Contains(c.Fix, "kad.yaml") {
		t.Errorf("fix does not say where to change it: %q", c.Fix)
	}
}

func TestMemoryOverThreeQuartersWarns(t *testing.T) {
	f := healthy()
	setBudget(f, 10, 16)

	c := find(t, Run(context.Background(), f, cfg(t, "cluster:\n  memory: 14Gi\n")), "budget/docker/memory")
	if c.Status != Warn {
		t.Errorf("status = %v, want warn at 14 of 16Gi", c.Status)
	}
}

func TestClaimingEveryCoreWarns(t *testing.T) {
	f := healthy()
	setBudget(f, 4, 32)

	c := find(t, Run(context.Background(), f, cfg(t, "cluster:\n  cpus: 4\n")), "budget/docker/cpu")
	if c.Status != Warn {
		t.Errorf("status = %v, want warn when the cluster claims every core", c.Status)
	}
}

func TestMoreCoresThanTheHostHasFails(t *testing.T) {
	f := healthy()
	setBudget(f, 2, 32)

	c := find(t, Run(context.Background(), f, cfg(t, "cluster:\n  cpus: 8\n")), "budget/docker/cpu")
	if c.Status != Fail {
		t.Errorf("status = %v, want fail", c.Status)
	}
}

func TestExistingProfileWarns(t *testing.T) {
	f := healthy()
	f.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"kad-demo"}]}`}

	c := find(t, Run(context.Background(), f, cfg(t, "")), "cluster/profile")
	if c.Status != Warn {
		t.Errorf("status = %v, want warn for an existing profile", c.Status)
	}
}

// First run has no profiles at all, and minikube exits non-zero for it. That
// is the normal case and must not be reported as a problem.
//
// The error has to be a *runner.ExitError, which is what the real runner
// returns for a command that ran and exited non-zero. It used to stand in as
// context.Canceled, which was harmless while every error meant the same
// thing — but "minikube said no" and "minikube never ran" are now different
// answers, and only the first of them is this test's subject.
func TestNoProfilesAtAllIsFine(t *testing.T) {
	f := healthy()
	f.Errors["profile list"] = &runner.ExitError{
		Cmd:    "minikube profile list -o json",
		Result: runner.Result{Code: 1, Stderr: "no minikube profile was found"},
	}

	c := find(t, Run(context.Background(), f, cfg(t, "")), "cluster/profile")
	if c.Status != OK {
		t.Errorf("status = %v, want ok on a machine with no minikube profiles", c.Status)
	}
}

func TestVMDriverSkipsTheRuntimeCheck(t *testing.T) {
	f := healthy()
	f.Missing = []string{"docker", "podman"}

	rep := Run(context.Background(), f, cfg(t, "cluster:\n  driver: qemu2\n"))
	c := find(t, rep, "runtime/qemu2")
	if c.Status != OK {
		t.Errorf("status = %v, want ok: a VM driver needs no container runtime", c.Status)
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string]version{
		"minikube version: v1.33.1":                  {1, 33, 1},
		"v4.2.3+g43e8b7f":                            {4, 2, 3},
		"Client Version: v1.31.0\nKustomize: v5.4.2": {1, 31, 0},
		"3.16.2": {3, 16, 2},
	}
	for in, want := range cases {
		got, err := parseVersion(in)
		if err != nil {
			t.Errorf("parseVersion(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseVersion(%q) = %v, want %v", in, got, want)
		}
	}
	if _, err := parseVersion("no version here"); err == nil {
		t.Error("want an error when there is no version to find")
	}
}

func TestVersionOrdering(t *testing.T) {
	if !(version{1, 2, 3}).less(version{1, 3, 0}) {
		t.Error("1.2.3 should be less than 1.3.0")
	}
	if (version{2, 0, 0}).less(version{1, 99, 99}) {
		t.Error("2.0.0 should not be less than 1.99.99")
	}
	if (version{1, 2, 3}).less(version{1, 2, 3}) {
		t.Error("a version is not less than itself")
	}
}

// Every failing check must carry a remedy; a report that only says "no" has
// moved the confusion rather than removed it.
func TestEveryFailureNamesAFix(t *testing.T) {
	f := healthy()
	f.Missing = []string{"minikube", "kubectl", "helm", "docker"}
	setBudget(f, 1, 32)

	rep := Run(context.Background(), f, cfg(t, ""))
	if len(rep.Failures()) == 0 {
		t.Fatal("expected failures on a host missing everything")
	}
	for _, c := range rep.Failures() {
		if strings.TrimSpace(c.Fix) == "" {
			t.Errorf("check %q failed without telling the user what to do", c.Name)
		}
	}
}

// The bug this check exists for: with the docker driver, minikube runs the node
// inside Docker's VM, so the ceiling is whatever that VM was given — not the
// host's RAM. Checking the host reported "8Gi of 36864Mi requested" and passed
// on a machine where Docker Desktop had 7834Mi and minikube then refused.
func TestDockerDriverIsBudgetedByDockerNotTheHost(t *testing.T) {
	f := healthy()
	setBudget(f, 12, 64)               // a big host...
	setDockerBudget(f, 12, 8214585344) // ...with only 7834Mi given to Docker

	rep := Run(context.Background(), f, cfg(t, "cluster:\n  memory: 8Gi\n"))
	c := find(t, rep, "budget/docker/memory")

	if c.Status != Fail {
		t.Errorf("status = %v, want fail: 8Gi does not fit in Docker's 7834Mi", c.Status)
	}
	if !strings.Contains(c.Detail, "Docker Desktop") {
		t.Errorf("detail does not name the real constraint: %q", c.Detail)
	}
	if !strings.Contains(c.Fix, "Docker Desktop") {
		t.Errorf("fix does not say where to raise it: %q", c.Fix)
	}
	if rep.OK() {
		t.Error("doctor passed a configuration minikube will reject")
	}
}

// A VM driver really is bounded by the host, so that path must not regress.
func TestVMDriverIsBudgetedByTheHost(t *testing.T) {
	f := healthy()
	setBudget(f, 12, 64)
	setDockerBudget(f, 12, 8214585344)

	rep := Run(context.Background(), f, cfg(t, "cluster:\n  driver: qemu2\n  memory: 16Gi\n"))
	c := find(t, rep, "budget/qemu2/memory")
	if c.Status != OK {
		t.Errorf("status = %v (%s), want ok: 16Gi fits in a 64Gi host", c.Status, c.Detail)
	}
}

// Not being able to ask must not block: minikube will still refuse if it does
// not fit, and a warning is the honest report of "kad could not check".
func TestUnknownBudgetWarnsRatherThanBlocks(t *testing.T) {
	f := healthy()
	f.Errors["docker info --format"] = context.DeadlineExceeded

	rep := Run(context.Background(), f, cfg(t, ""))
	c := find(t, rep, "budget/docker")
	if c.Status != Warn {
		t.Errorf("status = %v, want warn", c.Status)
	}
	if rep.OK() == false {
		t.Error("an unknown budget blocked the build")
	}
}

// doctor must never advise a value config.Validate would reject. That is the
// same failure as the bug this check exists for — kad refusing its own advice —
// and it became routinely reachable once the pool was Docker's rather than the
// host's, because a host is almost always over the minimum and Docker often is not.
func TestSuggestedMemoryIsAlwaysAcceptedByConfig(t *testing.T) {
	for poolMi := 1024; poolMi <= 65536; poolMi += 257 {
		got := suggestMemoryMi(poolMi)
		if got == 0 {
			if poolMi >= config.MinMemoryMi {
				t.Errorf("pool %dMi: refused to suggest anything despite clearing the minimum", poolMi)
			}
			continue
		}
		if got < config.MinMemoryMi {
			t.Errorf("pool %dMi: suggested %dMi, below kad's own minimum of %dMi",
				poolMi, got, config.MinMemoryMi)
		}
		if got > poolMi {
			t.Errorf("pool %dMi: suggested %dMi, more than the pool holds", poolMi, got)
		}
	}
}

// When nothing valid fits, say that rather than naming a number.
func TestTooSmallAPoolSaysSoInsteadOfSuggestingAnImpossibleValue(t *testing.T) {
	f := healthy()
	setDockerBudget(f, 4, 2147483648) // 2Gi

	c := find(t, Run(context.Background(), f, cfg(t, "")), "budget/docker/memory")
	if c.Status != Fail {
		t.Fatalf("status = %v, want fail on a 2Gi pool", c.Status)
	}
	if !strings.Contains(c.Detail, "at least") {
		t.Errorf("detail does not state kad's minimum: %q", c.Detail)
	}
	if strings.Contains(c.Fix, "1536") {
		t.Errorf("fix suggested a value below kad's minimum: %q", c.Fix)
	}
}

// Being unable to ask minikube is not the same as there being no profile. Said
// as "no existing profile", it sends someone into 'kad up' expecting a clean
// build on a machine that may already have one.
func TestUncheckableProfileWarnsRatherThanClaimingAbsence(t *testing.T) {
	f := healthy()
	f.Errors["profile list"] = errors.New(`minikube: executable file not found in $PATH`)

	rep := Run(context.Background(), f, cfg(t, ""))
	c := find(t, rep, "cluster/profile")
	if c.Status != Warn {
		t.Errorf("status = %v, want warn when kad could not check", c.Status)
	}
	if strings.Contains(c.Detail, "no existing profile") {
		t.Errorf("claimed the profile is absent without checking: %q", c.Detail)
	}
	if !rep.OK() {
		t.Error("an uncheckable profile blocked the build; the minikube check already reports the cause")
	}
}
