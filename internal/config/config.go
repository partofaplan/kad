// Package config defines kad.yaml: the single declaration of an environment.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// APIVersion is the only schema version this build understands.
const APIVersion = "kad.aviture.dev/v1alpha1"

// Kind is the only document kind this build understands.
const Kind = "Environment"

// ProfilePrefix namespaces every minikube profile kad creates.
//
// This is the guardrail that makes teardown safe: kad refuses to start, touch
// or delete any profile that does not carry the prefix, so it can never delete
// a cluster somebody else made.
const ProfilePrefix = "kad-"

// Config is a parsed kad.yaml.
type Config struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Name       string   `yaml:"name"`
	Cluster    Cluster  `yaml:"cluster"`
	Registry   Registry `yaml:"registry"`
	Tools      []string `yaml:"tools"`
	Apps       []App    `yaml:"apps"`
}

// Cluster is the minikube shape. Every field is guardrailed: kad picks a
// working default for anything omitted and rejects values that produce a
// cluster too small to hydrate.
type Cluster struct {
	Driver     string   `yaml:"driver"`
	Kubernetes string   `yaml:"kubernetes"`
	CPUs       int      `yaml:"cpus"`
	Memory     string   `yaml:"memory"`
	Disk       string   `yaml:"disk"`
	Addons     []string `yaml:"addons"`
}

// Registry redirects image and chart pulls at a mirror.
//
// Nothing in the catalog hardcodes docker.io or a public chart host; every
// reference is rewritten through this struct at resolve time. Leaving it empty
// pulls from the upstream defaults, so the airgapped path is a configuration
// change rather than a code change.
type Registry struct {
	// Mirror prefixes every container image, e.g. "harbor.internal/proxy".
	Mirror string `yaml:"mirror"`
	// ChartMirror replaces every chart repository, e.g.
	// "oci://harbor.internal/charts". Chart names are preserved.
	ChartMirror string `yaml:"chartMirror"`
}

// App is an arbitrary Helm release the user wants, for anything the catalog
// does not cover.
type App struct {
	Name      string         `yaml:"name"`
	Chart     string         `yaml:"chart"`
	Version   string         `yaml:"version"`
	Repo      string         `yaml:"repo"`
	Namespace string         `yaml:"namespace"`
	Wave      int            `yaml:"wave"`
	Values    map[string]any `yaml:"values"`
}

// Defaults are applied to anything the declaration leaves out.
var Defaults = Cluster{
	Driver:     "docker",
	Kubernetes: "v1.31.0",
	CPUs:       4,
	Memory:     "8Gi",
	Disk:       "40Gi",
	Addons:     []string{"storage-provisioner", "default-storageclass"},
}

// SupportedDrivers is the guardrailed set. Anything outside it is rejected
// rather than passed through to minikube, because a driver kad has not been
// exercised against produces failures that look like kad bugs.
var SupportedDrivers = []string{"docker", "podman", "qemu2", "vfkit", "hyperkit"}

// minimums below which hydration reliably fails rather than merely running slow.
const (
	minCPUs     = 2
	minMemoryMi = 4096
	minDiskMi   = 20480
)

var nameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Load reads and validates a kad.yaml.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse validates a kad.yaml already in memory.
func Parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // a typo'd key is an error, not a silent default
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse kad.yaml: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.APIVersion == "" {
		c.APIVersion = APIVersion
	}
	if c.Kind == "" {
		c.Kind = Kind
	}
	if c.Cluster.Driver == "" {
		c.Cluster.Driver = Defaults.Driver
	}
	if c.Cluster.Kubernetes == "" {
		c.Cluster.Kubernetes = Defaults.Kubernetes
	}
	if c.Cluster.CPUs == 0 {
		c.Cluster.CPUs = Defaults.CPUs
	}
	if c.Cluster.Memory == "" {
		c.Cluster.Memory = Defaults.Memory
	}
	if c.Cluster.Disk == "" {
		c.Cluster.Disk = Defaults.Disk
	}
	if len(c.Cluster.Addons) == 0 {
		c.Cluster.Addons = append([]string(nil), Defaults.Addons...)
	}
	for i := range c.Apps {
		if c.Apps[i].Namespace == "" {
			c.Apps[i].Namespace = "default"
		}
	}
}

// Validate reports every problem with the declaration at once, so a user fixes
// their file in one pass rather than one error per run.
func (c *Config) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if c.APIVersion != APIVersion {
		add("apiVersion: got %q, want %q", c.APIVersion, APIVersion)
	}
	if c.Kind != Kind {
		add("kind: got %q, want %q", c.Kind, Kind)
	}
	switch {
	case c.Name == "":
		add("name: required")
	case !nameRE.MatchString(c.Name):
		add("name %q: must be lowercase alphanumeric or '-', and start and end alphanumeric", c.Name)
	case len(c.Name) > 40:
		add("name %q: must be 40 characters or fewer", c.Name)
	}

	if !contains(SupportedDrivers, c.Cluster.Driver) {
		add("cluster.driver %q: not supported (one of: %s)", c.Cluster.Driver, strings.Join(SupportedDrivers, ", "))
	}
	if !strings.HasPrefix(c.Cluster.Kubernetes, "v") {
		add("cluster.kubernetes %q: must be a pinned version like v1.31.0", c.Cluster.Kubernetes)
	}
	if c.Cluster.CPUs < minCPUs {
		add("cluster.cpus %d: need at least %d", c.Cluster.CPUs, minCPUs)
	}
	if mi, err := ParseQuantityMi(c.Cluster.Memory); err != nil {
		add("cluster.memory %q: %v", c.Cluster.Memory, err)
	} else if mi < minMemoryMi {
		add("cluster.memory %q: need at least %dMi to hydrate anything", c.Cluster.Memory, minMemoryMi)
	}
	if mi, err := ParseQuantityMi(c.Cluster.Disk); err != nil {
		add("cluster.disk %q: %v", c.Cluster.Disk, err)
	} else if mi < minDiskMi {
		add("cluster.disk %q: need at least %dMi", c.Cluster.Disk, minDiskMi)
	}

	seen := map[string]string{}
	for _, t := range c.Tools {
		if prev, dup := seen[t]; dup {
			add("tools: %q listed twice", prev)
		}
		seen[t] = t
	}
	for i, a := range c.Apps {
		switch {
		case a.Name == "":
			add("apps[%d].name: required", i)
		case !nameRE.MatchString(a.Name):
			add("apps[%d].name %q: must be a valid DNS label", i, a.Name)
		}
		if a.Chart == "" {
			add("apps[%d] (%s).chart: required", i, a.Name)
		}
		if _, dup := seen[a.Name]; dup {
			add("apps[%d]: %q collides with another release name", i, a.Name)
		}
		seen[a.Name] = a.Name
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid kad.yaml:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// Profile is the minikube profile name for this environment.
func (c *Config) Profile() string { return ProfilePrefix + c.Name }

// OwnsProfile reports whether a profile name is one kad may act on.
func OwnsProfile(profile string) bool {
	return strings.HasPrefix(profile, ProfilePrefix) && len(profile) > len(ProfilePrefix)
}

var quantityRE = regexp.MustCompile(`^(\d+)(Mi|Gi|m|g|M|G)?$`)

// ParseQuantityMi converts "8Gi", "8g" or "8192" to mebibytes. Bare numbers are
// read as mebibytes, matching minikube's own --memory flag.
func ParseQuantityMi(s string) (int, error) {
	m := quantityRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("not a quantity like 8Gi or 8192")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("not a quantity like 8Gi or 8192")
	}
	switch m[2] {
	case "Gi", "g", "G":
		return n * 1024, nil
	default:
		return n, nil
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
