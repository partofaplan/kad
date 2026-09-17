// Package cluster owns the minikube profile an environment runs on.
package cluster

import (
	"context"
	"encoding/json"
	"errors"
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
func Exists(ctx context.Context, r runner.Runner, cfg *config.Config) (bool, error) {
	return ProfileExists(ctx, r, cfg.Profile())
}

// ProfileExists answers the same question for any profile name.
//
// The error return is the whole point. Mapping every minikube failure to
// "absent" let 'kad down' print "nothing to remove" and exit 0 on a machine
// where the profile was still there and minikube was simply unreachable —
// reporting success for work it had not done. A teardown that cannot verify
// its own result must not claim one.
func ProfileExists(ctx context.Context, r runner.Runner, profile string) (bool, error) {
	res, err := r.Run(ctx, "minikube", "profile", "list", "-o", "json")

	// minikube answering is what settles it, not the exit code: it exits
	// non-zero in states where it still prints a perfectly good document.
	if names, ok := parseProfileNames(res.Stdout); ok {
		for _, n := range names {
			if n == profile {
				return true, nil
			}
		}
		return false, nil
	}

	if err == nil {
		return false, fmt.Errorf("could not read minikube's profile list: %q", trim(res.Stdout))
	}
	// A command that never ran is not an answer. This is the case the bug was
	// filed for: minikube uninstalled while a kad- profile survives.
	var exit *runner.ExitError
	if !errors.As(err, &exit) {
		return false, fmt.Errorf("could not ask minikube which profiles exist: %w", err)
	}
	// minikube ran and printed no profile document at all. That is how it
	// reports having no profiles, which is a real "absent".
	if strings.TrimSpace(res.Stdout) == "" {
		return false, nil
	}
	return false, fmt.Errorf("minikube could not list profiles: %w", err)
}

// profileList is the part of 'minikube profile list -o json' kad reads.
//
// Decoded rather than string-scanned: the document spells "Name" twice per
// profile — once on the profile and once on its nested Config — so a search
// for `"Name":"x"` double-counts and cannot tell the two apart. Invalid
// profiles are included on purpose: a corrupt profile still owns a cluster,
// and 'kad down' has to be able to remove it.
//
// The fields are pointers so their ABSENCE is detectable. Without that, any
// JSON object at all decodes to an empty list and reads as "no profiles" —
// which is precisely the failure this type exists to prevent.
type profileList struct {
	Valid   *[]profileEntry   `json:"valid"`
	Invalid *[]profileEntry   `json:"invalid"`
	Error   *profileListError `json:"error"`
}

type profileEntry struct {
	Name string `json:"Name"`
}

// profileListError is what minikube prints INSTEAD of a list when it cannot
// enumerate profiles. Observed against minikube v1.33.1 with no
// ~/.minikube/profiles directory:
//
//	$ minikube profile list -o json
//	{"error":{"Op":"open","Path":"/home/u/.minikube/profiles","Err":2}}
//	$ echo $?
//	80
//
// Err is the syscall errno.
type profileListError struct {
	Op   string `json:"Op"`
	Path string `json:"Path"`
	Err  int    `json:"Err"`
}

// errnoENOENT is "no such file or directory". On the profiles directory it
// means minikube has never created a profile, which is a real "none" rather
// than a failure to ask. Every other errno is a failure to ask.
const errnoENOENT = 2

// parseProfileNames reports the profile names in minikube's output, and
// whether the output was an answer at all.
func parseProfileNames(stdout string) ([]string, bool) {
	out := strings.TrimSpace(stdout)
	// minikube prints advisory lines above the JSON in some states, so the
	// document starts at the first brace rather than at byte zero.
	i := strings.IndexByte(out, '{')
	if i < 0 {
		return nil, false
	}

	var list profileList
	if err := json.Unmarshal([]byte(out[i:]), &list); err != nil {
		return nil, false
	}

	if list.Error != nil {
		if list.Error.Err == errnoENOENT {
			return nil, true
		}
		return nil, false
	}
	// Neither a list nor an error: some other document, and no evidence of
	// anything. Saying "absent" here is a guess.
	if list.Valid == nil && list.Invalid == nil {
		return nil, false
	}

	var names []string
	for _, group := range []*[]profileEntry{list.Valid, list.Invalid} {
		if group == nil {
			continue
		}
		for _, p := range *group {
			if p.Name != "" {
				names = append(names, p.Name)
			}
		}
	}
	return names, true
}

// trim shortens output for an error message, so a runaway response does not
// become the whole error.
func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
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
