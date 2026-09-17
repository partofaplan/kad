package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/partofaplan/kad/internal/catalog"
	"github.com/partofaplan/kad/internal/cluster"
	"github.com/partofaplan/kad/internal/config"
	"github.com/partofaplan/kad/internal/doctor"
	"github.com/partofaplan/kad/internal/hydrate"
)

const starter = `# kad environment declaration.
#
# Everything about this environment lives in this file. 'kad up' builds it,
# 'kad down' removes it, and nothing kad creates lives outside the minikube
# profile named kad-<name>.
apiVersion: kad.aviture.dev/v1alpha1
kind: Environment
name: %s

cluster:
  driver: docker          # docker | podman | qemu2 | vfkit | hyperkit
  kubernetes: v1.31.0     # pinned on purpose: two runs must agree
  cpus: %d
  memory: %dMi  # %s
  disk: 40Gi

# Point image and chart pulls at an internal mirror. Leave empty to use the
# upstream public repositories.
registry:
  mirror: ""              # e.g. harbor.internal/proxy
  chartMirror: ""         # e.g. oci://harbor.internal/charts

# Named catalog entries. Run 'kad catalog' to see them all.
tools:
  - ingress
  - observability

# Anything the catalog does not cover, as a plain Helm release.
apps: []
#  - name: myapp
#    chart: myapp
#    repo: oci://ghcr.io/example/charts
#    version: 1.2.3
#    namespace: default
#    values:
#      replicaCount: 1
`

func runInit(ctx context.Context, env *Env, args []string) error {
	fs := newFlagSet("init")
	path := fileFlag(fs)
	name := fs.String("name", "", "environment name (default: the current directory)")
	force := fs.Bool("force", false, "overwrite an existing declaration")
	if err := parse(fs, args); err != nil {
		return err
	}

	if _, err := os.Stat(*path); err == nil && !*force {
		return fmt.Errorf("%s already exists — pass -force to overwrite it", *path)
	}

	envName := *name
	if envName == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determine environment name: %w", err)
		}
		envName = sanitize(filepath.Base(wd))
	} else if clean := sanitize(envName); clean != envName {
		// A name the user typed is their input, not a kad defect. Reporting it
		// as "this is a kad bug" — which is what validating only the rendered
		// file did — sends people to file issues against their own typing.
		return fmt.Errorf("%w: environment name %q must be lowercase alphanumeric or '-'; try -name %q",
			ErrUsage, envName, clean)
	}

	// Size the declaration to the machine it is being written on.
	//
	// The defaults used to be fixed at 4 cpu / 8Gi, which does not fit a stock
	// Docker Desktop — so the very first 'kad up' failed with a minikube error
	// about memory, on a file kad had just written itself. Asking is cheap and
	// makes the generated file correct where it is generated.
	cpus, memMi, note := starterSizing(ctx, env)
	body := fmt.Sprintf(starter, envName, cpus, memMi, note)
	// Validate what we are about to write, so 'init' can never produce a file
	// that 'up' will reject.
	if _, err := config.Parse([]byte(stripComments(body))); err != nil {
		return fmt.Errorf("generated declaration is invalid (this is a kad bug): %w", err)
	}
	if err := os.WriteFile(*path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *path, err)
	}

	fmt.Fprintf(env.Out, "Wrote %s for environment %q.\n\n", *path, envName)
	fmt.Fprintf(env.Out, "Next:  kad doctor    # check this machine can build it\n")
	fmt.Fprintf(env.Out, "       kad up        # build it\n")
	return nil
}

// starterSizing picks cluster dimensions that fit where kad is running, and a
// comment explaining where the number came from.
func starterSizing(ctx context.Context, env *Env) (cpus, memMi int, note string) {
	const (
		fallbackCPUs  = config.MinCPUs
		fallbackMemMi = config.MinMemoryMi
	)

	b, err := doctor.ResourceBudget(ctx, env.Runner, config.Defaults.Driver)
	if err != nil {
		return fallbackCPUs, fallbackMemMi, "conservative default: kad could not measure this machine"
	}

	// Three quarters, matching what doctor considers healthy, capped at 8Gi
	// because nothing in the catalog needs more on a laptop.
	memMi = b.MemMi * 3 / 4
	if memMi > 8192 {
		memMi = 8192
	}

	cpus = b.CPUs - 1
	if cpus > 4 {
		cpus = 4
	}
	if cpus < fallbackCPUs {
		cpus = fallbackCPUs
	}

	// The note has to describe the number actually written, and the two ways
	// the floor engages are not the same situation.
	//
	// Gate on the POOL, not on three quarters of it: a 5000Mi pool yields 3750
	// before clamping, but 5000Mi is perfectly usable — kad runs at 4096Mi. An
	// earlier version tested the clamped value and so told those users their
	// machine "is not enough", talking them out of something that works.
	switch {
	case b.MemMi < config.MinMemoryMi:
		return cpus, config.MinMemoryMi, fmt.Sprintf(
			"kad's minimum — %s has only %dMi, which is not enough; raise it before running 'kad up'",
			b.Source, b.MemMi)
	case memMi < config.MinMemoryMi:
		return cpus, config.MinMemoryMi, fmt.Sprintf(
			"kad's minimum, which is most of the %dMi %s has", b.MemMi, b.Source)
	}

	return cpus, memMi, fmt.Sprintf("75%% of the %dMi %s has", b.MemMi, b.Source)
}

func runDoctor(ctx context.Context, env *Env, args []string) error {
	fs := newFlagSet("doctor")
	path := fileFlag(fs)
	if err := parse(fs, args); err != nil {
		return err
	}

	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "Checking this machine can build %q:\n\n", cfg.Name)
	rep := doctor.Run(ctx, env.Runner, cfg)
	for _, c := range rep.Checks {
		fmt.Fprintf(env.Out, "  %s  %-20s %s\n", mark(c.Status), c.Name, c.Detail)
		if c.Fix != "" && c.Status != doctor.OK {
			fmt.Fprintf(env.Out, "     %s%s %s\n", strings.Repeat(" ", 20), fixArrow, c.Fix)
		}
	}

	if !rep.OK() {
		return fmt.Errorf("%d check(s) must be fixed before 'kad up' will work", len(rep.Failures()))
	}
	fmt.Fprintf(env.Out, "\nReady. Run 'kad up'.\n")
	return nil
}

func runUp(ctx context.Context, env *Env, args []string) error {
	fs := newFlagSet("up")
	path := fileFlag(fs)
	dryRun := fs.Bool("dry-run", false, "print the plan without building anything")
	skipDoctor := fs.Bool("skip-doctor", false, "build even if preflight checks fail")
	timeout := fs.String("timeout", "10m", "per-release install timeout")
	if err := parse(fs, args); err != nil {
		return err
	}

	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	plan, err := hydrate.Plan(cfg)
	if err != nil {
		return err
	}

	if *dryRun {
		printPlan(env, cfg, plan)
		return nil
	}

	if !*skipDoctor {
		rep := doctor.Run(ctx, env.Runner, cfg)
		if !rep.OK() {
			fmt.Fprintf(env.Err, "Preflight failed:\n\n")
			for _, c := range rep.Failures() {
				fmt.Fprintf(env.Err, "  %s %-20s %s\n", markFail, c.Name, c.Detail)
				if c.Fix != "" {
					fmt.Fprintf(env.Err, "    %s %s\n", fixArrow, c.Fix)
				}
			}
			return fmt.Errorf("run 'kad doctor' for the full report, or 'kad up -skip-doctor' to build anyway")
		}
	}

	fmt.Fprintf(env.Out, "Building %q on profile %s.\n\n", cfg.Name, cfg.Profile())
	if err := cluster.Start(ctx, env.Runner, cfg); err != nil {
		return err
	}
	for _, addon := range cfg.Cluster.Addons {
		if err := cluster.EnableAddon(ctx, env.Runner, cfg, addon); err != nil {
			return err
		}
	}

	if len(plan) > 0 {
		fmt.Fprintf(env.Out, "\nInstalling %d release(s).\n", len(plan))
		if err := hydrate.Apply(ctx, env.Out, env.Runner, cfg, plan, *timeout); err != nil {
			return err
		}
	}

	fmt.Fprintf(env.Out, "\n%q is up. 'kad status' shows how to reach it.\n", cfg.Name)
	return nil
}

func runStatus(ctx context.Context, env *Env, args []string) error {
	fs := newFlagSet("status")
	path := fileFlag(fs)
	if err := parse(fs, args); err != nil {
		return err
	}

	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "Environment %q  (profile %s)\n\n", cfg.Name, cfg.Profile())
	exists, err := cluster.Exists(ctx, env.Runner, cfg)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(env.Out, "  Not built. Run 'kad up'.\n")
		return nil
	}

	// A status command that cannot reach the cluster must say so. Swallowing
	// this error turned 'kad status' into a list of port-forward commands with
	// no indication that nothing had been checked.
	res, err := env.Runner.Run(ctx, "kubectl", append([]string{"get", "pods", "-A",
		"--no-headers"}, cluster.KubectlContextArgs(cfg)...)...)
	if err != nil {
		fmt.Fprintf(env.Out, "  Could not reach the cluster: %v\n", err)
		fmt.Fprintf(env.Out, "  The profile exists, so try 'minikube start -p %s'.\n\n", cfg.Profile())
	} else {
		total, ready := 0, 0
		for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
			if line == "" {
				continue
			}
			total++
			if strings.Contains(line, "Running") || strings.Contains(line, "Completed") {
				ready++
			}
		}
		fmt.Fprintf(env.Out, "  Pods: %d/%d running\n\n", ready, total)
	}

	tools, err := catalog.Resolve(cfg.Tools, cfg.Registry)
	if err != nil {
		return err
	}
	if len(tools) > 0 {
		fmt.Fprintf(env.Out, "  Reaching your tools:\n\n")
		for _, t := range tools {
			if t.Access == nil {
				continue
			}
			fmt.Fprintf(env.Out, "    %s\n", t.Name)
			if t.Access.Service != "" {
				fmt.Fprintf(env.Out, "      kubectl port-forward -n %s svc/%s %d:%d --context %s\n",
					t.Namespace, t.Access.Service, t.Access.Port, t.Access.Port, cfg.Profile())
			}
			if t.Access.User != "" {
				fmt.Fprintf(env.Out, "      user: %s\n", t.Access.User)
			}
			if t.Access.SecretRef != "" {
				if ns, name, key, ok := splitSecretRef(t.Access.SecretRef); ok {
					fmt.Fprintf(env.Out,
						"      password: kubectl get secret -n %s %s -o jsonpath='{.data.%s}' --context %s | base64 -d\n",
						ns, name, key, cfg.Profile())
				}
			}
			if t.Access.Note != "" {
				fmt.Fprintf(env.Out, "      note: %s\n", catalog.RenderNote(t.Access.Note, cfg.Profile()))
			}
			fmt.Fprintln(env.Out)
		}
	}

	fmt.Fprintf(env.Out, "  kubectl --context %s get all -A\n", cfg.Profile())
	return nil
}

func runDown(ctx context.Context, env *Env, args []string) error {
	fs := newFlagSet("down")
	path := fileFlag(fs)
	yes := fs.Bool("y", false, "do not ask for confirmation")
	if err := parse(fs, args); err != nil {
		return err
	}

	cfg, err := loadConfig(*path)
	if err != nil {
		return err
	}
	// Not "does it exist" but "can kad tell": reporting a successful teardown
	// because minikube could not be reached is the one outcome worse than
	// failing, because the user stops looking.
	exists, err := cluster.Exists(ctx, env.Runner, cfg)
	if err != nil {
		return fmt.Errorf("%w\nprofile %s may still exist; nothing was removed", err, cfg.Profile())
	}
	if !exists {
		fmt.Fprintf(env.Out, "Nothing to remove: profile %s does not exist.\n", cfg.Profile())
		return nil
	}

	if !*yes {
		fmt.Fprintf(env.Out, "This deletes profile %s and everything in it, permanently.\n", cfg.Profile())
		fmt.Fprintf(env.Out, "Type the environment name (%s) to confirm: ", cfg.Name)
		answer, err := confirm(env.In)
		if err != nil {
			return fmt.Errorf("could not read the confirmation; nothing was removed: %w", err)
		}
		if answer != cfg.Name {
			return fmt.Errorf("not confirmed; nothing was removed")
		}
	}

	if err := cluster.Delete(ctx, env.Runner, cfg.Profile()); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "\nRemoved %q.\n", cfg.Name)
	return nil
}

func runCatalog(_ context.Context, env *Env, args []string) error {
	fs := newFlagSet("catalog")
	format := fs.String("o", "text", "output format: text | tsv")
	if err := parse(fs, args); err != nil {
		return err
	}

	// tsv exists so scripts/catalog-verify.sh can check the pins against the
	// upstream repositories without keeping a second copy of them.
	if *format == "tsv" {
		for _, t := range catalog.All() {
			if t.IsAddon() {
				continue
			}
			fmt.Fprintf(env.Out, "%s\t%s\t%s\t%s\n", t.Name, t.Chart, t.Version, t.Repo)
		}
		return nil
	}
	if *format != "text" {
		return fmt.Errorf("%w: unknown format %q", ErrUsage, *format)
	}

	fmt.Fprintf(env.Out, "Tools available by name in kad.yaml:\n\n")
	for _, t := range catalog.All() {
		version := t.Version
		if t.IsAddon() {
			version = "(minikube addon)"
		}
		fmt.Fprintf(env.Out, "  %-15s %-18s %s\n", t.Name, version, t.Description)
	}
	fmt.Fprintf(env.Out, "\nAnything not listed goes under 'apps:' as a plain Helm release.\n")
	return nil
}

func printPlan(env *Env, cfg *config.Config, plan []hydrate.Release) {
	mem, _ := config.ParseQuantityMi(cfg.Cluster.Memory)
	fmt.Fprintf(env.Out, "Plan for %q:\n\n", cfg.Name)
	fmt.Fprintf(env.Out, "  cluster  minikube profile %s\n", cfg.Profile())
	fmt.Fprintf(env.Out, "           %s driver, kubernetes %s, %d cpu, %dMi\n\n",
		cfg.Cluster.Driver, cfg.Cluster.Kubernetes, cfg.Cluster.CPUs, mem)

	if len(plan) == 0 {
		fmt.Fprintf(env.Out, "  nothing to install\n")
		return
	}
	for i, wave := range hydrate.Waves(plan) {
		fmt.Fprintf(env.Out, "  wave %d\n", i+1)
		for _, rel := range wave {
			if rel.IsAddon() {
				fmt.Fprintf(env.Out, "    %-15s minikube addon %s\n", rel.Name, rel.Addon)
				continue
			}
			fmt.Fprintf(env.Out, "    %-15s %s %s %s ns/%s\n", rel.Name, rel.Chart, rel.Version, arrow, rel.Namespace)
		}
	}
}

func mark(s doctor.Status) string {
	switch s {
	case doctor.OK:
		return markOK
	case doctor.Warn:
		return markWarn
	default:
		return markFail
	}
}

func splitSecretRef(ref string) (ns, name, key string, ok bool) {
	parts := strings.Split(ref, "/")
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// sanitize coerces a directory name into a valid environment name.
func sanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "dev"
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// stripComments removes the explanatory comments so the starter file can be
// validated as the config it will parse to.
func stripComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
