package catalog

import (
	"strings"
	"testing"

	"github.com/partofaplan/kad/internal/config"
)

func TestResolvePullsInRequirements(t *testing.T) {
	// harbor requires ingress; asking for harbor alone must yield both.
	got, err := Resolve([]string{"harbor"}, config.Registry{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := map[string]bool{}
	for _, t := range got {
		names[t.Name] = true
	}
	if !names["ingress"] {
		t.Errorf("harbor did not pull in ingress; got %v", names)
	}
}

func TestResolveOrdersByWave(t *testing.T) {
	got, err := Resolve([]string{"postgres", "harbor", "cert-manager"}, config.Registry{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Wave < got[i-1].Wave {
			t.Fatalf("wave order violated: %s(%d) after %s(%d)",
				got[i].Name, got[i].Wave, got[i-1].Name, got[i-1].Wave)
		}
	}
	// Infra must precede the platform tool that depends on it.
	if got[0].Wave != WaveInfra {
		t.Errorf("first entry %s is wave %d, want infra", got[0].Name, got[0].Wave)
	}
}

func TestResolveDeduplicates(t *testing.T) {
	got, err := Resolve([]string{"ingress", "harbor", "ingress"}, config.Registry{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	seen := map[string]int{}
	for _, tool := range got {
		seen[tool.Name]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("%s resolved %d times, want 1", name, n)
		}
	}
}

func TestResolveUnknownToolSuggests(t *testing.T) {
	_, err := Resolve([]string{"harbour"}, config.Registry{})
	if err == nil {
		t.Fatal("want an error for an unknown tool")
	}
	if !strings.Contains(err.Error(), `did you mean "harbor"`) {
		t.Errorf("error does not suggest harbor: %v", err)
	}
	if !strings.Contains(err.Error(), "available:") {
		t.Errorf("error does not list the catalog: %v", err)
	}
}

func TestChartMirrorOverridesRepo(t *testing.T) {
	reg := config.Registry{ChartMirror: "oci://harbor.internal/charts"}
	got, err := Resolve([]string{"postgres"}, reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range got {
		if tool.IsAddon() {
			continue
		}
		if tool.Repo != reg.ChartMirror {
			t.Errorf("%s repo = %q, want the mirror %q", tool.Name, tool.Repo, reg.ChartMirror)
		}
	}
}

func TestImageMirrorIsWrittenIntoValues(t *testing.T) {
	reg := config.Registry{Mirror: "harbor.internal/proxy"}
	got, err := Resolve([]string{"postgres"}, reg)
	if err != nil {
		t.Fatal(err)
	}
	var pg Tool
	for _, tool := range got {
		if tool.Name == "postgres" {
			pg = tool
		}
	}
	global, ok := pg.Values["global"].(map[string]any)
	if !ok {
		t.Fatalf("postgres values have no global map: %#v", pg.Values)
	}
	if global["imageRegistry"] != reg.Mirror {
		t.Errorf("global.imageRegistry = %v, want %q", global["imageRegistry"], reg.Mirror)
	}
}

// Resolving with a mirror must not corrupt the package-level catalog for the
// next call — that would make a second Resolve in one process wrong.
func TestMirrorDoesNotMutateBuiltinCatalog(t *testing.T) {
	if _, err := Resolve([]string{"postgres"}, config.Registry{Mirror: "mirror.example"}); err != nil {
		t.Fatal(err)
	}
	clean, err := Resolve([]string{"postgres"}, config.Registry{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range clean {
		if tool.Name != "postgres" {
			continue
		}
		if g, ok := tool.Values["global"].(map[string]any); ok {
			if _, polluted := g["imageRegistry"]; polluted {
				t.Fatal("a mirrored Resolve leaked into the builtin catalog")
			}
		}
		if tool.Repo == "mirror.example" {
			t.Fatal("a mirrored Resolve leaked the repo into the builtin catalog")
		}
	}
}

// Every entry must be pinned. An unpinned chart makes two runs of the same
// kad.yaml produce different clusters, which is what kad exists to prevent.
func TestEveryChartIsPinned(t *testing.T) {
	for _, tool := range All() {
		if tool.IsAddon() {
			continue
		}
		if tool.Version == "" {
			t.Errorf("%s has no pinned chart version", tool.Name)
		}
		if tool.Chart == "" {
			t.Errorf("%s has no chart name", tool.Name)
		}
		if tool.Repo == "" {
			t.Errorf("%s has no chart repo", tool.Name)
		}
		if tool.Namespace == "" {
			t.Errorf("%s has no namespace", tool.Name)
		}
	}
}

// Requirements must name real entries, or Resolve fails at runtime on a
// dependency the user never typed.
func TestRequirementsExist(t *testing.T) {
	for _, tool := range All() {
		for _, req := range tool.Requires {
			if _, ok := Get(req); !ok {
				t.Errorf("%s requires %q, which is not in the catalog", tool.Name, req)
			}
		}
	}
}

func TestSetPathCreatesNestedMaps(t *testing.T) {
	m := map[string]any{}
	setPath(m, "a.b.c", "v")
	a := m["a"].(map[string]any)
	b := a["b"].(map[string]any)
	if b["c"] != "v" {
		t.Errorf("setPath did not write the leaf: %#v", m)
	}
}

// Credentials must not be committed. Beyond the obvious, a default password in
// the catalog is kad asserting a weak credential rather than letting the chart
// generate one, and it trains people to accept whatever kad ships with.
func TestCatalogShipsNoCredentials(t *testing.T) {
	suspect := []string{"password", "passwd", "rootuser", "secretkey", "token", "apikey"}
	for _, tool := range All() {
		walk(t, tool.Name, tool.Values, func(path string, v any) {
			s, ok := v.(string)
			if !ok || s == "" {
				return
			}
			lower := strings.ToLower(path)
			for _, word := range suspect {
				if strings.Contains(lower, word) {
					t.Errorf("%s sets %s to a literal value; let the chart generate it", tool.Name, path)
				}
			}
		})
	}
}

// A SecretRef is rendered into a kubectl command the user is told to run, so a
// malformed one hands them something that cannot work.
func TestSecretRefsAreWellFormed(t *testing.T) {
	for _, tool := range All() {
		if tool.Access == nil || tool.Access.SecretRef == "" {
			continue
		}
		parts := strings.Split(tool.Access.SecretRef, "/")
		if len(parts) != 3 {
			t.Errorf("%s SecretRef %q is not namespace/name/key", tool.Name, tool.Access.SecretRef)
			continue
		}
		if parts[0] != tool.Namespace {
			t.Errorf("%s SecretRef namespace %q does not match the tool's namespace %q",
				tool.Name, parts[0], tool.Namespace)
		}
	}
}

func walk(t *testing.T, prefix string, m map[string]any, fn func(path string, v any)) {
	t.Helper()
	for k, v := range m {
		path := prefix + "." + k
		if nested, ok := v.(map[string]any); ok {
			walk(t, path, nested, fn)
			continue
		}
		fn(path, v)
	}
}

// Notes are copy-paste instructions, and 'kad up' passes --keep-context=true
// on purpose — so the user's current kubectl context is deliberately NOT the
// kad cluster. A kubectl command in a Note without a context therefore runs
// against whatever else is active. Two entries shipped exactly that.
//
// This is the assertion that stops it coming back: the Note field is a static
// string with no access to the profile name, so nothing else would catch it.
func TestNotesWithKubectlCommandsCarryAContext(t *testing.T) {
	for _, tool := range All() {
		if tool.Access == nil || !strings.Contains(tool.Access.Note, "kubectl ") {
			continue
		}
		if !strings.Contains(tool.Access.Note, "--context "+ContextPlaceholder) {
			t.Errorf("%s: note runs kubectl against the user's current context, not the kad cluster.\n"+
				"  add '--context %s' to it:\n  %s", tool.Name, ContextPlaceholder, tool.Access.Note)
		}
	}
}

// The same trap with a different tool: 'minikube tunnel' needs -p, and the
// ingress note used to carry a literal "<profile>" nobody substituted.
func TestNotesHaveNoUnsubstitutedPlaceholders(t *testing.T) {
	for _, tool := range All() {
		if tool.Access == nil {
			continue
		}
		for _, bad := range []string{"<profile>", "<context>", "<name>", "{{.Profile}}"} {
			if strings.Contains(tool.Access.Note, bad) {
				t.Errorf("%s: note contains %q, which nothing substitutes; use %s:\n  %s",
					tool.Name, bad, ContextPlaceholder, tool.Access.Note)
			}
		}
	}
}

func TestRenderNoteSubstitutesTheContext(t *testing.T) {
	nexus, _ := Get("nexus")
	got := RenderNote(nexus.Access.Note, "kad-demo")

	if strings.Contains(got, ContextPlaceholder) {
		t.Errorf("placeholder survived rendering: %s", got)
	}
	if !strings.Contains(got, "--context kad-demo") {
		t.Errorf("rendered note does not target the kad cluster: %s", got)
	}
	// minio's note contains a jsonpath expression in single braces. Rendering
	// must not touch it.
	minio, _ := Get("minio")
	if out := RenderNote(minio.Access.Note, "kad-demo"); !strings.Contains(out, "{.data.rootUser}") {
		t.Errorf("rendering mangled the jsonpath expression: %s", out)
	}
}
