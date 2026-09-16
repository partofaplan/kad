// Package hydrate installs the declared tools and apps into a running cluster.
package hydrate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/partofaplan/kad/internal/catalog"
	"github.com/partofaplan/kad/internal/cluster"
	"github.com/partofaplan/kad/internal/config"
	"github.com/partofaplan/kad/internal/runner"
)

// Release is one thing to install: a catalog tool or a user-declared app,
// flattened into the single shape the installer works with.
type Release struct {
	Name      string
	Chart     string
	Version   string
	Repo      string
	Namespace string
	Wave      int
	Values    map[string]any
	Addon     string
}

// IsAddon reports whether this release installs via minikube rather than Helm.
func (r Release) IsAddon() bool { return r.Addon != "" }

// Plan flattens a config into an ordered install plan.
//
// Returning the plan rather than installing directly is what makes 'kad up
// --dry-run' possible and what lets ordering be tested without a cluster.
func Plan(cfg *config.Config) ([]Release, error) {
	tools, err := catalog.Resolve(cfg.Tools, cfg.Registry)
	if err != nil {
		return nil, err
	}

	var rels []Release
	for _, t := range tools {
		rels = append(rels, Release{
			Name:      t.Name,
			Chart:     t.Chart,
			Version:   t.Version,
			Repo:      t.Repo,
			Namespace: t.Namespace,
			Wave:      t.Wave,
			Values:    t.Values,
			Addon:     t.Addon,
		})
	}
	for _, a := range cfg.Apps {
		wave := a.Wave
		if wave == 0 {
			wave = catalog.WaveApp
		}
		repo := a.Repo
		// An app declares its own repo, but the mirror still overrides it, so
		// an airgapped run needs no per-app edits.
		if cfg.Registry.ChartMirror != "" {
			repo = cfg.Registry.ChartMirror
		}
		rels = append(rels, Release{
			Name:      a.Name,
			Chart:     a.Chart,
			Version:   a.Version,
			Repo:      repo,
			Namespace: a.Namespace,
			Wave:      wave,
			Values:    a.Values,
		})
	}

	sort.SliceStable(rels, func(i, j int) bool { return rels[i].Wave < rels[j].Wave })
	return rels, nil
}

// Waves groups a plan into its ordered waves.
func Waves(rels []Release) [][]Release {
	var out [][]Release
	for i := 0; i < len(rels); {
		j := i
		for j < len(rels) && rels[j].Wave == rels[i].Wave {
			j++
		}
		out = append(out, rels[i:j])
		i = j
	}
	return out
}

// Apply installs every release, wave by wave.
//
// Waves are sequential because later entries assume earlier ones exist —
// Harbor needs an ingress controller before its Ingress resolves. Within a
// wave order does not matter, but installs are still serial: parallel helm
// runs against one laptop cluster reliably time each other out.
func Apply(ctx context.Context, out io.Writer, r runner.Runner, cfg *config.Config, rels []Release, timeout string) error {
	tmp, err := os.MkdirTemp("", "kad-values-")
	if err != nil {
		return fmt.Errorf("values scratch dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	waves := Waves(rels)
	for i, wave := range waves {
		fmt.Fprintf(out, "\n  wave %d of %d\n", i+1, len(waves))
		for _, rel := range wave {
			if err := install(ctx, out, r, cfg, rel, tmp, timeout); err != nil {
				return err
			}
		}
	}
	return nil
}

func install(ctx context.Context, out io.Writer, r runner.Runner, cfg *config.Config, rel Release, tmp, timeout string) error {
	if rel.IsAddon() {
		fmt.Fprintf(out, "    %-16s minikube addon\n", rel.Name)
		return cluster.EnableAddon(ctx, r, cfg, rel.Addon)
	}

	fmt.Fprintf(out, "    %-16s %s %s\n", rel.Name, rel.Chart, rel.Version)

	// An OCI repository addresses the chart by URL. A classic HTTP repository
	// is passed with --repo rather than 'helm repo add', deliberately: repo add
	// writes to the user's ~/.config/helm/repositories.yaml, which outlives
	// 'kad down' and would leave state on the machine that deleting the
	// profile cannot reclaim. --repo resolves the same chart with no global
	// side effect.
	chartRef := rel.Chart
	var repoArgs []string
	if strings.HasPrefix(rel.Repo, "oci://") {
		chartRef = strings.TrimSuffix(rel.Repo, "/") + "/" + rel.Chart
	} else if rel.Repo != "" {
		repoArgs = []string{"--repo", rel.Repo}
	}

	args := []string{
		"upgrade", "--install", rel.Name, chartRef,
		"--namespace", rel.Namespace,
		"--create-namespace",
		"--wait",
		"--timeout", timeout,
	}
	args = append(args, repoArgs...)
	args = append(args, cluster.HelmContextArgs(cfg)...)
	if rel.Version != "" {
		args = append(args, "--version", rel.Version)
	}
	if len(rel.Values) > 0 {
		path := filepath.Join(tmp, rel.Name+".yaml")
		data, err := yaml.Marshal(rel.Values)
		if err != nil {
			return fmt.Errorf("render values for %s: %w", rel.Name, err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("write values for %s: %w", rel.Name, err)
		}
		args = append(args, "--values", path)
	}

	if err := r.Stream(ctx, "helm", args...); err != nil {
		// A release left mid-install blocks the next attempt with "another
		// operation in progress", so say what to do rather than leaving the
		// user to discover helm's error on their own.
		return fmt.Errorf("install %s: %w\n"+
			"  re-run 'kad up' to retry; if it reports another operation in progress, "+
			"clear the stuck release with 'helm uninstall %s -n %s --kube-context %s'",
			rel.Name, err, rel.Name, rel.Namespace, cfg.Profile())
	}
	return nil
}
