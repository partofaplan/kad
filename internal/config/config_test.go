package config

import (
	"strings"
	"testing"
)

const minimal = `
apiVersion: kad.aviture.dev/v1alpha1
kind: Environment
name: demo
`

func TestParseAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Cluster.Driver != Defaults.Driver {
		t.Errorf("driver = %q, want %q", c.Cluster.Driver, Defaults.Driver)
	}
	if c.Cluster.CPUs != Defaults.CPUs {
		t.Errorf("cpus = %d, want %d", c.Cluster.CPUs, Defaults.CPUs)
	}
	if len(c.Cluster.Addons) != len(Defaults.Addons) {
		t.Errorf("addons = %v, want %v", c.Cluster.Addons, Defaults.Addons)
	}
}

// Defaults must be copied, not shared: mutating one config's addons has
// previously been able to change every later config's defaults.
func TestDefaultAddonsAreNotAliased(t *testing.T) {
	a, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	a.Cluster.Addons[0] = "mutated"

	b, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if b.Cluster.Addons[0] == "mutated" {
		t.Fatal("parsing one config mutated the package defaults")
	}
}

func TestProfileIsPrefixed(t *testing.T) {
	c, _ := Parse([]byte(minimal))
	if got, want := c.Profile(), "kad-demo"; got != want {
		t.Errorf("Profile() = %q, want %q", got, want)
	}
	if !OwnsProfile(c.Profile()) {
		t.Error("kad does not recognise its own profile")
	}
}

func TestOwnsProfileRejectsForeignProfiles(t *testing.T) {
	for _, p := range []string{"minikube", "", "kad-", "prod-cluster", "mykad-thing"} {
		if OwnsProfile(p) {
			t.Errorf("OwnsProfile(%q) = true, want false", p)
		}
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := map[string]struct{ yaml, want string }{
		"bad apiVersion": {"apiVersion: v1\nkind: Environment\nname: a\n", "apiVersion"},
		"missing name":   {"apiVersion: kad.aviture.dev/v1alpha1\nkind: Environment\n", "name: required"},
		"upper name":     {"apiVersion: kad.aviture.dev/v1alpha1\nkind: Environment\nname: Demo\n", "lowercase"},
		"bad driver":     {minimal + "cluster:\n  driver: virtualbox\n", "driver"},
		"tiny memory":    {minimal + "cluster:\n  memory: 1Gi\n", "memory"},
		"tiny cpus":      {minimal + "cluster:\n  cpus: 1\n", "cpus"},
		"unpinned k8s":   {minimal + "cluster:\n  kubernetes: latest\n", "kubernetes"},
		"app no chart":   {minimal + "apps:\n  - name: x\n", "chart: required"},
		"unknown key":    {minimal + "clusterz: {}\n", "field clusterz"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", tc.yaml)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A user should be able to fix their file in one pass, not one error per run.
func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	_, err := Parse([]byte(`
apiVersion: kad.aviture.dev/v1alpha1
kind: Environment
name: Bad_Name
cluster:
  driver: virtualbox
  cpus: 1
`))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"name", "driver", "cpus"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}

func TestReleaseNameCollisionIsRejected(t *testing.T) {
	_, err := Parse([]byte(minimal + `
tools:
  - harbor
apps:
  - name: harbor
    chart: harbor
`))
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("want a collision error, got %v", err)
	}
}

func TestParseQuantityMi(t *testing.T) {
	cases := map[string]int{"8Gi": 8192, "8g": 8192, "8G": 8192, "512Mi": 512, "4096": 4096}
	for in, want := range cases {
		got, err := ParseQuantityMi(in)
		if err != nil {
			t.Errorf("ParseQuantityMi(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseQuantityMi(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "lots", "8GB", "-4Gi", "8.5Gi"} {
		if _, err := ParseQuantityMi(bad); err == nil {
			t.Errorf("ParseQuantityMi(%q) succeeded, want an error", bad)
		}
	}
}
