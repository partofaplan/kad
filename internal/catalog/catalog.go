// Package catalog is the built-in set of things a user can ask for by name.
//
// The catalog is what makes the declaration short. "harbor" in kad.yaml
// expands to a pinned chart, a namespace, an install ordering and a set of
// values already sized for a laptop rather than a datacentre. Without it every
// user rediscovers the same hundred lines of values.yaml.
package catalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/partofaplan/kad/internal/config"
)

// Install waves. Everything in a wave installs in parallel; a wave completes
// before the next begins. The numbers are spaced so entries can be inserted
// between them without renumbering.
const (
	WaveInfra    = 10 // cluster services other things assume: ingress, certs
	WavePlatform = 20 // the tools a team shares: registry, CI, observability
	WaveApp      = 30 // backing services for the code under development
)

// ContextPlaceholder is substituted with the environment's kubectl context
// wherever it appears in a Note.
//
// Notes are static strings and the catalog has no access to the profile name,
// which is how two entries came to carry copy-paste kubectl commands with no
// --context. 'kad up' passes --keep-context=true on purpose, so the user's
// current context is deliberately NOT the kad cluster and those commands ran
// against whatever else was active. Service and SecretRef never had the
// problem because runStatus formats them with the profile; Note now goes
// through the same door.
const ContextPlaceholder = "{{.Context}}"

// RenderNote substitutes the environment's context into a Note.
func RenderNote(note, profile string) string {
	return strings.ReplaceAll(note, ContextPlaceholder, profile)
}

// Access describes how a human reaches a tool once it is running.
type Access struct {
	Service   string // service name to port-forward
	Port      int
	Path      string
	User      string
	SecretRef string // namespace/name/key holding the password
	Note      string
}

// Tool is one catalog entry.
type Tool struct {
	Name        string
	Description string

	// Addon, when set, means this is a minikube addon rather than a chart.
	Addon string

	Chart     string
	Version   string
	Repo      string
	Namespace string
	Wave      int
	Values    map[string]any

	// RegistryValues are dotted value paths that receive the image registry
	// prefix when a mirror is configured. Charts disagree about where this
	// lives, so it is declared per entry rather than guessed.
	RegistryValues []string

	// Requires names other catalog entries that must be present. kad adds
	// them automatically rather than failing.
	Requires []string

	Access *Access
}

// IsAddon reports whether this entry is installed via minikube rather than Helm.
func (t Tool) IsAddon() bool { return t.Addon != "" }

// builtin is the catalog. Versions are pinned deliberately: an unpinned chart
// makes two runs of the same kad.yaml produce different clusters, which is the
// exact failure this project exists to prevent.
var builtin = map[string]Tool{
	"ingress": {
		Name:        "ingress",
		Description: "NGINX ingress controller (minikube addon)",
		Addon:       "ingress",
		Wave:        WaveInfra,
		Access:      &Access{Note: "Run 'minikube tunnel -p " + ContextPlaceholder + "' to reach Ingress hosts from the host machine."},
	},
	"metrics": {
		Name:        "metrics",
		Description: "metrics-server, for kubectl top and HPA",
		Addon:       "metrics-server",
		Wave:        WaveInfra,
	},
	"cert-manager": {
		Name:        "cert-manager",
		Description: "X.509 certificate management",
		Chart:       "cert-manager",
		Version:     "v1.16.2",
		Repo:        "https://charts.jetstack.io",
		Namespace:   "cert-manager",
		Wave:        WaveInfra,
		Values: map[string]any{
			"crds":      map[string]any{"enabled": true},
			"resources": laptopResources("10m", "32Mi", "100m", "128Mi"),
		},
		RegistryValues: []string{"image.registry"},
	},
	"harbor": {
		Name:        "harbor",
		Description: "container registry with vulnerability scanning",
		Chart:       "harbor",
		Version:     "1.16.2",
		Repo:        "https://helm.goharbor.io",
		Namespace:   "harbor",
		Wave:        WavePlatform,
		Requires:    []string{"ingress"},
		Values: map[string]any{
			"expose": map[string]any{
				"type": "clusterIP",
				"tls":  map[string]any{"enabled": false},
			},
			"externalURL": "http://harbor.kad.local",
			// Trivy roughly doubles Harbor's footprint; off by default on a
			// laptop, and a one-line override for anyone who wants it.
			"trivy":       map[string]any{"enabled": false},
			"notary":      map[string]any{"enabled": false},
			"persistence": map[string]any{"enabled": true},
		},
		Access: &Access{
			Service: "harbor-core", Port: 80, User: "admin",
			Note: "Uses the Harbor chart's own default admin password, which is public. " +
				"Override it with apps[].values.harborAdminPassword for anything someone else can reach.",
		},
	},
	"nexus": {
		Name:        "nexus",
		Description: "Sonatype Nexus artifact repository",
		Chart:       "nexus-repository-manager",
		Version:     "64.1.0",
		Repo:        "https://sonatype.github.io/helm3-charts/",
		Namespace:   "nexus",
		Wave:        WavePlatform,
		Values: map[string]any{
			"nexus":       map[string]any{"resources": laptopResources("200m", "1Gi", "1", "2Gi")},
			"persistence": map[string]any{"enabled": true, "storageSize": "8Gi"},
		},
		Access: &Access{
			Service: "nexus-nexus-repository-manager", Port: 8081, User: "admin",
			Note: "First-run password: kubectl --context " + ContextPlaceholder +
				" exec -n nexus deploy/nexus-nexus-repository-manager -- cat /nexus-data/admin.password",
		},
	},
	"jenkins": {
		Name:        "jenkins",
		Description: "Jenkins CI controller",
		Chart:       "jenkins",
		Version:     "5.8.16",
		Repo:        "https://charts.jenkins.io",
		Namespace:   "jenkins",
		Wave:        WavePlatform,
		Values: map[string]any{
			"controller": map[string]any{
				"resources":      laptopResources("200m", "1Gi", "1", "2Gi"),
				"installPlugins": []any{"kubernetes:4308.vd1a_2dfa_50d9c", "workflow-aggregator:600.vb_57cdd26fdd7"},
				"serviceType":    "ClusterIP",
			},
			"persistence": map[string]any{"enabled": true, "size": "8Gi"},
		},
		Access: &Access{
			Service: "jenkins", Port: 8080, User: "admin",
			SecretRef: "jenkins/jenkins/jenkins-admin-password",
		},
	},
	"observability": {
		Name:        "observability",
		Description: "Prometheus, Alertmanager and Grafana",
		Chart:       "kube-prometheus-stack",
		Version:     "66.2.1",
		Repo:        "https://prometheus-community.github.io/helm-charts",
		Namespace:   "observability",
		Wave:        WavePlatform,
		Values: map[string]any{
			"prometheus": map[string]any{
				"prometheusSpec": map[string]any{
					"retention":      "6h",
					"resources":      laptopResources("100m", "512Mi", "1", "2Gi"),
					"walCompression": true,
				},
			},
			"alertmanager": map[string]any{"enabled": false},
			"grafana": map[string]any{
				"resources": laptopResources("50m", "128Mi", "500m", "512Mi"),
			},
		},
		Access: &Access{
			Service: "observability-grafana", Port: 80, User: "admin",
			SecretRef: "observability/observability-grafana/admin-password",
		},
	},
	"argocd": {
		Name:        "argocd",
		Description: "Argo CD GitOps controller",
		Chart:       "argo-cd",
		Version:     "7.7.11",
		Repo:        "https://argoproj.github.io/argo-helm",
		Namespace:   "argocd",
		Wave:        WavePlatform,
		Values: map[string]any{
			"dex":            map[string]any{"enabled": false},
			"notifications":  map[string]any{"enabled": false},
			"applicationSet": map[string]any{"enabled": false},
		},
		Access: &Access{
			Service: "argocd-server", Port: 80, User: "admin",
			SecretRef: "argocd/argocd-initial-admin-secret/password",
		},
	},
	"minio": {
		Name:        "minio",
		Description: "S3-compatible object storage",
		Chart:       "minio",
		Version:     "5.3.0",
		Repo:        "https://charts.min.io/",
		Namespace:   "minio",
		Wave:        WaveApp,
		Values: map[string]any{
			"mode":        "standalone",
			"replicas":    1,
			"persistence": map[string]any{"enabled": true, "size": "10Gi"},
			"resources":   map[string]any{"requests": map[string]any{"memory": "512Mi"}},
		},
		Access: &Access{
			Service: "minio-console", Port: 9001,
			SecretRef: "minio/minio/rootPassword",
			Note: "Username: kubectl --context " + ContextPlaceholder +
				" get secret -n minio minio -o jsonpath='{.data.rootUser}' | base64 -d",
		},
	},
	"postgres": {
		Name:        "postgres",
		Description: "PostgreSQL 16, single instance",
		Chart:       "postgresql",
		Version:     "16.2.1",
		Repo:        "https://charts.bitnami.com/bitnami",
		Namespace:   "data",
		Wave:        WaveApp,
		Values: map[string]any{
			"auth":         map[string]any{"database": "app"},
			"primary":      map[string]any{"persistence": map[string]any{"size": "8Gi"}},
			"architecture": "standalone",
		},
		RegistryValues: []string{"global.imageRegistry"},
		Access: &Access{
			Service: "postgres-postgresql", Port: 5432, User: "postgres",
			SecretRef: "data/postgres-postgresql/postgres-password",
		},
	},
	"redis": {
		Name:        "redis",
		Description: "Redis 7, single instance",
		Chart:       "redis",
		Version:     "20.6.1",
		Repo:        "https://charts.bitnami.com/bitnami",
		Namespace:   "data",
		Wave:        WaveApp,
		Values: map[string]any{
			"architecture": "standalone",
			"auth":         map[string]any{"enabled": false},
			"master":       map[string]any{"persistence": map[string]any{"size": "4Gi"}},
		},
		RegistryValues: []string{"global.imageRegistry"},
		Access:         &Access{Service: "redis-master", Port: 6379},
	},
}

// Get returns a catalog entry by name.
func Get(name string) (Tool, bool) {
	t, ok := builtin[name]
	return t, ok
}

// Names lists every catalog entry, sorted.
func Names() []string {
	out := make([]string, 0, len(builtin))
	for n := range builtin {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// All returns every catalog entry, sorted by name.
func All() []Tool {
	out := make([]Tool, 0, len(builtin))
	for _, n := range Names() {
		out = append(out, builtin[n])
	}
	return out
}

// Resolve expands the requested names into the full install set: unknown names
// are reported with a suggestion, requirements are pulled in automatically, and
// the result is ordered by wave.
func Resolve(names []string, reg config.Registry) ([]Tool, error) {
	chosen := map[string]bool{}
	var unknown []string

	var add func(string)
	add = func(n string) {
		if chosen[n] {
			return
		}
		t, ok := builtin[n]
		if !ok {
			unknown = append(unknown, n)
			return
		}
		chosen[n] = true
		for _, req := range t.Requires {
			add(req)
		}
	}
	for _, n := range names {
		add(n)
	}

	if len(unknown) > 0 {
		var msgs []string
		for _, u := range unknown {
			msg := fmt.Sprintf("unknown tool %q", u)
			if s := suggest(u); s != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", s)
			}
			msgs = append(msgs, msg)
		}
		return nil, fmt.Errorf("%s\navailable: %s", strings.Join(msgs, "\n"), strings.Join(Names(), ", "))
	}

	out := make([]Tool, 0, len(chosen))
	for _, n := range Names() {
		if chosen[n] {
			out = append(out, applyMirror(builtin[n], reg))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Wave < out[j].Wave })
	return out, nil
}

// applyMirror rewrites a tool's chart repo and image registry through the
// configured mirror. It copies rather than mutating, so the builtin catalog is
// never modified by a run.
func applyMirror(t Tool, reg config.Registry) Tool {
	// Copy unconditionally, not just on the mirror branch. Handing back the
	// builtin map by reference made "two resolves must not affect each other"
	// true only because nothing happened to mutate it.
	t.Values = deepCopy(t.Values)

	if reg.ChartMirror != "" && !t.IsAddon() {
		t.Repo = reg.ChartMirror
	}
	if reg.Mirror != "" {
		for _, path := range t.RegistryValues {
			setPath(t.Values, path, reg.Mirror)
		}
	}
	return t
}

// setPath writes v at a dotted path, creating intermediate maps as needed.
func setPath(m map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

func deepCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			out[k] = deepCopy(nested)
			continue
		}
		out[k] = v
	}
	return out
}

func laptopResources(cpuReq, memReq, cpuLim, memLim string) map[string]any {
	return map[string]any{
		"requests": map[string]any{"cpu": cpuReq, "memory": memReq},
		"limits":   map[string]any{"cpu": cpuLim, "memory": memLim},
	}
}

// suggest returns the closest catalog name within edit distance 3, or "".
func suggest(in string) string {
	best, bestD := "", 4
	for _, n := range Names() {
		if d := distance(in, n); d < bestD {
			best, bestD = n, d
		}
	}
	return best
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
