// Package cluster owns the minikube profile an environment runs on.
package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/partofaplan/kad/internal/config"
	"github.com/partofaplan/kad/internal/runner"
)

// StartArgs builds the 'minikube start' invocation for a config.
//
// It is a pure function so the flags can be asserted in a unit test without
// minikube installed — the flags are the part most likely to regress.
func StartArgs(cfg *config.Config) []string {
	mem, _ := config.ParseQuantityMi(cfg.Cluster.Memory)
	disk, _ := config.ParseQuantityMi(cfg.Cluster.Disk)

	args := []string{
		"start",
		"--profile=" + cfg.Profile(),
		"--driver=" + cfg.Cluster.Driver,
		"--kubernetes-version=" + cfg.Cluster.Kubernetes,
		fmt.Sprintf("--cpus=%d", cfg.Cluster.CPUs),
		fmt.Sprintf("--memory=%dmb", mem),
		fmt.Sprintf("--disk-size=%dmb", disk),
		// Keep kad's kubeconfig entry out of the user's current context until
		// they ask for it. 'kad up' should never silently repoint kubectl at a
		// new cluster mid-session.
		"--keep-context=true",
		"--interactive=false",
	}
	if cfg.Registry.Mirror != "" {
		args = append(args, "--insecure-registry="+cfg.Registry.Mirror)
	}
	return args
}

// Exists reports whether this environment's profile is already present.
func Exists(ctx context.Context, r runner.Runner, cfg *config.Config) bool {
	res, err := r.Run(ctx, "minikube", "profile", "list", "-o", "json")
	if err != nil {
		return false
	}
	return strings.Contains(res.Stdout, `"Name":"`+cfg.Profile()+`"`) ||
		strings.Contains(res.Stdout, `"Name": "`+cfg.Profile()+`"`)
}

// Start creates or resumes the profile.
func Start(ctx context.Context, r runner.Runner, cfg *config.Config) error {
	if err := r.Stream(ctx, "minikube", StartArgs(cfg)...); err != nil {
		return fmt.Errorf("start cluster %s: %w", cfg.Profile(), err)
	}
	return nil
}

// EnableAddon turns on one minikube addon.
func EnableAddon(ctx context.Context, r runner.Runner, cfg *config.Config, addon string) error {
	if _, err := r.Run(ctx, "minikube", "addons", "enable", addon, "--profile="+cfg.Profile()); err != nil {
		return fmt.Errorf("enable addon %s: %w", addon, err)
	}
	return nil
}

// Delete removes the profile and everything in it.
//
// The prefix check is the guardrail that makes teardown safe to run without
// reading it twice: kad can only ever delete a profile it could have created.
func Delete(ctx context.Context, r runner.Runner, profile string) error {
	if !config.OwnsProfile(profile) {
		return fmt.Errorf("refusing to delete profile %q: kad only manages profiles prefixed %q",
			profile, config.ProfilePrefix)
	}
	if err := r.Stream(ctx, "minikube", "delete", "--profile="+profile); err != nil {
		return fmt.Errorf("delete cluster %s: %w", profile, err)
	}
	return nil
}

// HelmContextArgs targets this environment's cluster rather than whatever the
// user's current context is.
//
// helm and kubectl spell this flag differently — helm has --kube-context,
// kubectl has --context — and there is deliberately no single helper for both.
// One existed, was used for both, and kubectl rejected it silently.
func HelmContextArgs(cfg *config.Config) []string {
	return []string{"--kube-context", cfg.Profile()}
}

// KubectlContextArgs is the kubectl spelling of the same thing.
func KubectlContextArgs(cfg *config.Config) []string {
	return []string{"--context", cfg.Profile()}
}
