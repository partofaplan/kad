package runner

import (
	"context"
	"testing"
)

// The Fake matches canned responses by substring, and a general pattern will
// often also match a line a specific pattern was written for. Iterating a map
// to decide between them let Go's randomised order pick the winner, so a test
// could pass or fail depending on the run.
//
// This is pinned here rather than relied on incidentally: the one test that
// happened to exercise it was later rewritten to have no overlapping keys, and
// nothing then held the property at all.
func TestLongestMatchWinsRegardlessOfMapOrder(t *testing.T) {
	// Run repeatedly: one pass proves nothing when the bug is a coin flip.
	for i := 0; i < 50; i++ {
		f := NewFake()
		f.Responses["{{.MemTotal}}"] = Result{Stdout: "general"}
		f.Responses["docker info --format {{.MemTotal}}"] = Result{Stdout: "specific"}

		res, err := f.Run(context.Background(), "docker", "info", "--format", "{{.MemTotal}}")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.Stdout != "specific" {
			t.Fatalf("run %d: got %q, want the more specific pattern to win", i, res.Stdout)
		}
	}
}

func TestEqualLengthKeysResolveTheSameWayEveryTime(t *testing.T) {
	first := ""
	for i := 0; i < 50; i++ {
		f := NewFake()
		f.Responses["aaaa"] = Result{Stdout: "A"}
		f.Responses["bbbb"] = Result{Stdout: "B"}

		res, _ := f.Run(context.Background(), "x", "aaaa", "bbbb")
		if i == 0 {
			first = res.Stdout
			continue
		}
		if res.Stdout != first {
			t.Fatalf("run %d: got %q, first run got %q — tiebreak is not stable", i, res.Stdout, first)
		}
	}
}

func TestUnmatchedCommandSucceedsSilently(t *testing.T) {
	f := NewFake()
	res, err := f.Run(context.Background(), "anything", "at", "all")
	if err != nil || res.Stdout != "" {
		t.Errorf("unmatched command: got (%q, %v), want empty success", res.Stdout, err)
	}
	if !f.Called("anything at all") {
		t.Error("the call was not recorded")
	}
}

// A command that fails still says why on stdout or stderr, and the real
// runner returns both the output and the error. The fake dropped the output,
// so a test could not describe "minikube exited 80 AND printed an error
// document" — and one that tried passed for the wrong reason.
func TestFakeReturnsOutputAlongsideAnError(t *testing.T) {
	f := NewFake()
	f.Responses["minikube profile list"] = Result{Stdout: `{"error":{"Err":2}}`, Code: 80}
	f.Errors["minikube profile list"] = &ExitError{Cmd: "minikube profile list"}

	res, err := f.Run(context.Background(), "minikube", "profile", "list")
	if err == nil {
		t.Fatal("no error returned")
	}
	if res.Stdout != `{"error":{"Err":2}}` {
		t.Errorf("output dropped on error: %q", res.Stdout)
	}
	if res.Code != 80 {
		t.Errorf("exit code = %d, want 80", res.Code)
	}
}

// With no Responses entry, a matched error still reports a non-zero exit.
func TestFakeErrorWithoutAResponseStillFails(t *testing.T) {
	f := NewFake()
	f.Errors["helm install"] = &ExitError{Cmd: "helm install"}

	res, err := f.Run(context.Background(), "helm", "install", "x")
	if err == nil {
		t.Fatal("no error returned")
	}
	if res.Code == 0 {
		t.Error("exit code = 0 alongside an error")
	}
}
