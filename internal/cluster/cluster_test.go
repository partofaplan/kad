package cluster

import (
	"context"
	"strings"
	"testing"

	"github.com/partofaplan/kad/internal/config"
	"github.com/partofaplan/kad/internal/runner"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte(`
apiVersion: kad.aviture.dev/v1alpha1
kind: Environment
name: demo
cluster:
  driver: docker
  kubernetes: v1.31.0
  cpus: 4
  memory: 8Gi
  disk: 40Gi
`))
	if err != nil {
		t.Fatalf("test config: %v", err)
	}
	return c
}

func TestStartArgs(t *testing.T) {
	args := strings.Join(StartArgs(testConfig(t)), " ")
	for _, want := range []string{
		"--profile=kad-demo",
		"--driver=docker",
		"--kubernetes-version=v1.31.0",
		"--cpus=4",
		"--memory=8192mb",
		"--disk-size=40960mb",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("start args missing %q:\n%s", want, args)
		}
	}
}

// 'kad up' must not silently repoint the user's kubectl at a new cluster
// halfway through their session.
func TestStartKeepsTheUsersContext(t *testing.T) {
	args := strings.Join(StartArgs(testConfig(t)), " ")
	if !strings.Contains(args, "--keep-context=true") {
		t.Errorf("start args do not preserve the current kubectl context:\n%s", args)
	}
}

func TestStartArgsAddInsecureRegistryForAMirror(t *testing.T) {
	cfg := testConfig(t)
	cfg.Registry.Mirror = "harbor.internal/proxy"
	args := strings.Join(StartArgs(cfg), " ")
	if !strings.Contains(args, "--insecure-registry=harbor.internal/proxy") {
		t.Errorf("a configured mirror was not trusted by the cluster:\n%s", args)
	}
}

// The central teardown guardrail.
func TestDeleteRefusesForeignProfiles(t *testing.T) {
	for _, profile := range []string{"minikube", "prod", "", "kad-"} {
		r := runner.NewFake()
		err := Delete(context.Background(), r, profile)
		if err == nil {
			t.Errorf("Delete(%q) succeeded; kad must only delete its own profiles", profile)
		}
		if r.Called("delete") {
			t.Errorf("Delete(%q) shelled out to minikube before refusing", profile)
		}
	}
}

func TestDeleteAcceptsItsOwnProfile(t *testing.T) {
	r := runner.NewFake()
	if err := Delete(context.Background(), r, "kad-demo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !r.Called("minikube delete --profile=kad-demo") {
		t.Errorf("Delete did not invoke minikube; calls: %v", r.Calls)
	}
}

func TestExists(t *testing.T) {
	r := runner.NewFake()
	r.Responses["profile list"] = runner.Result{
		Stdout: `{"valid":[{"Name":"kad-demo","Status":"Running"}]}`,
	}
	if !Exists(context.Background(), r, testConfig(t)) {
		t.Error("Exists = false for a profile that is present")
	}

	r2 := runner.NewFake()
	r2.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"other"}]}`}
	if Exists(context.Background(), r2, testConfig(t)) {
		t.Error("Exists = true for a profile that is absent")
	}
}

// helm and kubectl spell the context flag differently. One shared helper was
// used for both, and kubectl rejects --kube-context outright, so 'kad status'
// silently lost its only live data.
func TestContextFlagsUseEachToolsOwnSpelling(t *testing.T) {
	cfg := testConfig(t)

	if got := strings.Join(HelmContextArgs(cfg), " "); got != "--kube-context kad-demo" {
		t.Errorf("HelmContextArgs = %q, want %q", got, "--kube-context kad-demo")
	}
	if got := strings.Join(KubectlContextArgs(cfg), " "); got != "--context kad-demo" {
		t.Errorf("KubectlContextArgs = %q, want %q", got, "--context kad-demo")
	}
}
