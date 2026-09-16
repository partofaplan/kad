package hydrate

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/partofaplan/kad/internal/catalog"
	"github.com/partofaplan/kad/internal/config"
	"github.com/partofaplan/kad/internal/runner"
)

func parse(t *testing.T, y string) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte("apiVersion: kad.aviture.dev/v1alpha1\nkind: Environment\nname: demo\n" + y))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return c
}

func TestPlanOrdersInfraBeforeApps(t *testing.T) {
	plan, err := Plan(parse(t, `
tools: [postgres, harbor]
apps:
  - name: web
    chart: web
`))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(plan); i++ {
		if plan[i].Wave < plan[i-1].Wave {
			t.Fatalf("plan out of wave order: %+v", plan)
		}
	}
	if plan[0].Name != "ingress" {
		t.Errorf("first release is %q, want the ingress controller harbor depends on", plan[0].Name)
	}
}

func TestPlanDefaultsAppsToTheApplicationWave(t *testing.T) {
	plan, err := Plan(parse(t, "apps:\n  - name: web\n    chart: web\n"))
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Wave != catalog.WaveApp {
		t.Errorf("app wave = %d, want %d", plan[0].Wave, catalog.WaveApp)
	}
}

func TestPlanHonoursAnExplicitAppWave(t *testing.T) {
	plan, err := Plan(parse(t, "apps:\n  - name: web\n    chart: web\n    wave: 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Wave != 5 {
		t.Errorf("app wave = %d, want the declared 5", plan[0].Wave)
	}
}

// An airgapped run must need no per-app edits.
func TestChartMirrorOverridesAnAppsOwnRepo(t *testing.T) {
	plan, err := Plan(parse(t, `
registry:
  chartMirror: oci://harbor.internal/charts
apps:
  - name: web
    chart: web
    repo: https://charts.example.com
`))
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Repo != "oci://harbor.internal/charts" {
		t.Errorf("app repo = %q, want the mirror", plan[0].Repo)
	}
}

func TestWavesGroupsContiguously(t *testing.T) {
	rels := []Release{
		{Name: "a", Wave: 10}, {Name: "b", Wave: 10},
		{Name: "c", Wave: 20},
		{Name: "d", Wave: 30}, {Name: "e", Wave: 30},
	}
	got := Waves(rels)
	if len(got) != 3 {
		t.Fatalf("Waves returned %d groups, want 3: %+v", len(got), got)
	}
	if len(got[0]) != 2 || len(got[1]) != 1 || len(got[2]) != 2 {
		t.Errorf("wave sizes = %d/%d/%d, want 2/1/2", len(got[0]), len(got[1]), len(got[2]))
	}
}

func TestWavesOfEmptyPlan(t *testing.T) {
	if got := Waves(nil); len(got) != 0 {
		t.Errorf("Waves(nil) = %v, want empty", got)
	}
}

func TestPlanRejectsUnknownTool(t *testing.T) {
	if _, err := Plan(parse(t, "tools: [nope]\n")); err == nil {
		t.Fatal("want an error for an unknown tool")
	}
}

// 'helm repo add' writes to the user's ~/.config/helm and survives 'kad down'.
// Everything kad creates must live inside the profile, so charts are resolved
// with --repo instead.
func TestInstallLeavesNoGlobalHelmRepoState(t *testing.T) {
	cfg := parse(t, `
apps:
  - name: web
    chart: nginx
    repo: https://charts.example.com
    version: 1.0.0
`)
	plan, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := runner.NewFake()
	if err := Apply(context.Background(), io.Discard, r, cfg, plan, "1m"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, call := range r.Calls {
		if strings.Contains(call, "helm repo add") {
			t.Errorf("Apply wrote global helm state: %q", call)
		}
	}
	if !r.Called("--repo https://charts.example.com") {
		t.Errorf("chart was not resolved with --repo; calls: %v", r.Calls)
	}
}

func TestInstallUsesOCIRefDirectly(t *testing.T) {
	cfg := parse(t, `
apps:
  - name: web
    chart: nginx
    repo: oci://ghcr.io/example/charts
    version: 1.0.0
`)
	plan, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := runner.NewFake()
	if err := Apply(context.Background(), io.Discard, r, cfg, plan, "1m"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !r.Called("oci://ghcr.io/example/charts/nginx") {
		t.Errorf("OCI chart not addressed by URL; calls: %v", r.Calls)
	}
	if r.Called("--repo") {
		t.Errorf("OCI chart should not use --repo; calls: %v", r.Calls)
	}
}
