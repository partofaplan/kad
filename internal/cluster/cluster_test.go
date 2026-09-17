package cluster

import (
	"context"
	"errors"
	"fmt"
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
	got, err := Exists(context.Background(), r, testConfig(t))
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !got {
		t.Error("Exists = false for a profile that is present")
	}

	r2 := runner.NewFake()
	r2.Responses["profile list"] = runner.Result{Stdout: `{"valid":[{"Name":"other"}]}`}
	got, err = Exists(context.Background(), r2, testConfig(t))
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if got {
		t.Error("Exists = true for a profile that is absent")
	}
}

// Real 'minikube profile list -o json' spells "Name" twice per profile: once
// on the profile, once on its nested Config. A string scan cannot tell a
// profile named X from a profile whose config happens to mention X, which is
// why this decodes. The fixture below is the real shape, trimmed.
func TestExistsDecodesTheRealProfileDocument(t *testing.T) {
	const out = `{"invalid":[],"valid":[{"Name":"kad-demo","Status":"Running","Config":{"Name":"kad-demo","Driver":"docker","Memory":8192},"Active":false}]}`

	r := runner.NewFake()
	r.Responses["profile list"] = runner.Result{Stdout: out}
	got, err := Exists(context.Background(), r, testConfig(t))
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !got {
		t.Error("Exists = false for a profile the real minikube output contains")
	}
}

// A corrupt profile still owns a cluster, so teardown has to be able to find
// it. minikube reports those under "invalid" rather than "valid".
func TestExistsFindsInvalidProfiles(t *testing.T) {
	r := runner.NewFake()
	r.Responses["profile list"] = runner.Result{
		Stdout: `{"invalid":[{"Name":"kad-demo","Status":"Unknown"}],"valid":[]}`,
	}
	got, err := Exists(context.Background(), r, testConfig(t))
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !got {
		t.Error("Exists = false for a profile minikube listed as invalid")
	}
}

// minikube exits non-zero with nothing on stdout when it has no profiles at
// all. That is an answer — "absent" — and must not become an error, or the
// normal first run would fail.
func TestExistsTreatsAnEmptyProfileListAsAbsent(t *testing.T) {
	r := runner.NewFake()
	r.Errors["profile list"] = &runner.ExitError{
		Cmd:    "minikube profile list -o json",
		Result: runner.Result{Code: 1, Stderr: "no minikube profile was found"},
	}
	got, err := Exists(context.Background(), r, testConfig(t))
	if err != nil {
		t.Fatalf("Exists errored on an empty profile list: %v", err)
	}
	if got {
		t.Error("Exists = true with no profiles")
	}
}

// The bug this pair of returns exists for: kad could not ask minikube, and
// reported "absent". 'kad down' then printed nothing-to-remove and exited 0
// with the cluster still on the machine.
func TestExistsReportsAnErrorWhenItCannotAskMinikube(t *testing.T) {
	r := runner.NewFake()
	r.Errors["profile list"] = errors.New(`minikube: exec: "minikube": executable file not found in $PATH`)

	got, err := Exists(context.Background(), r, testConfig(t))
	if err == nil {
		t.Fatal("Exists reported an answer it could not obtain; a teardown would claim success")
	}
	if got {
		t.Error("Exists = true alongside an error")
	}
}

// Output that is not a profile list is not evidence of anything either.
func TestExistsReportsAnErrorForUnreadableOutput(t *testing.T) {
	r := runner.NewFake()
	r.Responses["profile list"] = runner.Result{Stdout: "Error: something went sideways\n"}
	if _, err := Exists(context.Background(), r, testConfig(t)); err == nil {
		t.Error("Exists accepted output that was not a profile list")
	}
}

// minikube prints advisory lines above the JSON in some states.
func TestExistsSkipsAPreambleAboveTheJSON(t *testing.T) {
	r := runner.NewFake()
	r.Responses["profile list"] = runner.Result{
		Stdout: "! docker is taking longer than usual\n{\"valid\":[{\"Name\":\"kad-demo\"}]}\n",
	}
	got, err := Exists(context.Background(), r, testConfig(t))
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !got {
		t.Error("Exists = false because minikube printed a warning above its JSON")
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

// --- fixtures captured from real minikube v1.33.1, not hand-written ---

// An empty profiles directory: the state a machine is in right after 'kad
// down' removes the last profile. This is what CI's idempotent-teardown step
// hits, so getting it wrong turns a second 'kad down' into a failure.
func TestExistsHandlesTheRealEmptyProfileList(t *testing.T) {
	r := runner.NewFake()
	r.Responses["profile list"] = runner.Result{Stdout: `{"invalid":[],"valid":[]}`}

	got, err := Exists(context.Background(), r, testConfig(t))
	if err != nil {
		t.Fatalf("Exists errored on an empty list: %v", err)
	}
	if got {
		t.Error("Exists = true with no profiles")
	}
}

// No ~/.minikube/profiles at all. minikube prints an error document on stdout
// and exits 80. ENOENT on that directory means it has never made a profile,
// so "absent" is the right answer — but it has to be reached by reading the
// error, not by an unknown key decoding to an empty list.
func TestExistsTreatsAMissingProfilesDirectoryAsAbsent(t *testing.T) {
	const out = `{"error":{"Op":"open","Path":"/home/u/.minikube/profiles","Err":2}}`

	r := runner.NewFake()
	r.Responses["profile list"] = runner.Result{Stdout: out}
	r.Errors["profile list"] = &runner.ExitError{
		Cmd:    "minikube profile list -o json",
		Result: runner.Result{Stdout: out, Code: 80},
	}
	got, err := Exists(context.Background(), r, testConfig(t))
	if err != nil {
		t.Fatalf("Exists errored on a never-used minikube: %v", err)
	}
	if got {
		t.Error("Exists = true on a machine with no minikube state")
	}
}

// Any other errno is minikube failing to answer, and must not read as "no
// profiles" — a profile it could not enumerate is still on the machine.
func TestExistsReportsAnErrorForANonENOENTFailure(t *testing.T) {
	for _, errno := range []int{13, 5, 21} { // EACCES, EIO, EISDIR
		out := fmt.Sprintf(`{"error":{"Op":"open","Path":"/home/u/.minikube/profiles","Err":%d}}`, errno)

		r := runner.NewFake()
		r.Responses["profile list"] = runner.Result{Stdout: out}
		r.Errors["profile list"] = &runner.ExitError{
			Cmd:    "minikube profile list -o json",
			Result: runner.Result{Stdout: out, Code: 80},
		}
		if _, err := Exists(context.Background(), r, testConfig(t)); err == nil {
			t.Errorf("errno %d: Exists claimed an answer minikube did not give", errno)
		}
	}
}

// A JSON document that is neither a list nor an error is not evidence that
// there are no profiles. Without the presence check it decoded to an empty
// list and reported "absent" for anything at all.
func TestExistsRejectsJSONThatIsNotAProfileList(t *testing.T) {
	for _, out := range []string{`{}`, `{"something":"else"}`, `{"valid":null}`} {
		r := runner.NewFake()
		r.Responses["profile list"] = runner.Result{Stdout: out}
		if _, err := Exists(context.Background(), r, testConfig(t)); err == nil {
			t.Errorf("Exists accepted %q as proof of no profiles", out)
		}
	}
}

// The real shape of an unhealthy profile, captured from a machine that had
// one: listed under "invalid", with Config null rather than absent.
func TestExistsFindsTheRealInvalidProfileShape(t *testing.T) {
	r := runner.NewFake()
	r.Responses["profile list"] = runner.Result{
		Stdout: `{"invalid":[{"Name":"kad-demo","Status":"","Config":null,"Active":false,"ActiveKubeContext":false}],"valid":[]}`,
	}
	got, err := Exists(context.Background(), r, testConfig(t))
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !got {
		t.Error("Exists = false for a real corrupt profile that still owns a cluster")
	}
}
