// Package doctor runs the preflight checks that stand between a user and a
// half-built cluster.
//
// The design rule here: every failure names the fix. A check that reports a
// problem without saying what to do about it has moved the confusion rather
// than removed it.
package doctor

import (
	"context"
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/partofaplan/kad/internal/config"
	"github.com/partofaplan/kad/internal/runner"
)

// Status is the outcome of one check.
type Status int

const (
	OK Status = iota
	Warn
	Fail
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	default:
		return "fail"
	}
}

// Check is one preflight result.
type Check struct {
	Name   string
	Status Status
	Detail string
	Fix    string
}

// Report is the full preflight outcome.
type Report struct {
	Checks []Check
}

// OK reports whether anything blocking was found. Warnings do not block.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// Failures returns only the blocking checks.
func (r Report) Failures() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Status == Fail {
			out = append(out, c)
		}
	}
	return out
}

// minimum tool versions. Below these, kad has not been exercised and failures
// surface as confusing errors from the tool rather than from kad.
var minVersions = map[string]version{
	"minikube": {1, 32, 0},
	"kubectl":  {1, 28, 0},
	"helm":     {3, 12, 0},
}

// Run executes every preflight check against the host.
func Run(ctx context.Context, r runner.Runner, cfg *config.Config) Report {
	var rep Report
	add := func(c Check) { rep.Checks = append(rep.Checks, c) }

	for _, bin := range []string{"minikube", "kubectl", "helm"} {
		add(checkBinary(ctx, r, bin))
	}
	add(checkRuntime(ctx, r, cfg))
	add(checkAmbiguousRuntime(r))
	for _, c := range checkResources(ctx, r, cfg) {
		add(c)
	}
	add(checkProfileCollision(ctx, r, cfg))

	return rep
}

func checkBinary(ctx context.Context, r runner.Runner, bin string) Check {
	c := Check{Name: bin}
	if _, err := r.Look(bin); err != nil {
		c.Status = Fail
		c.Detail = "not found on PATH"
		c.Fix = installHint(bin)
		return c
	}

	got, err := toolVersion(ctx, r, bin)
	if err != nil {
		// Present but unreadable version is a warning: it usually still works.
		c.Status = Warn
		c.Detail = "installed, but version could not be determined"
		c.Fix = fmt.Sprintf("run '%s version' by hand to check it works", bin)
		return c
	}

	want := minVersions[bin]
	if got.less(want) {
		c.Status = Fail
		c.Detail = fmt.Sprintf("%s is older than the minimum %s", got, want)
		c.Fix = installHint(bin)
		return c
	}

	// Helm 4 is new enough that many charts and CI images still assume 3.x.
	// It is not known-broken, so this warns rather than blocks.
	if bin == "helm" && got.major >= 4 {
		c.Status = Warn
		c.Detail = fmt.Sprintf("helm %s is a major version ahead of what most charts are tested against", got)
		c.Fix = "if a chart install fails oddly, retry with a helm 3.x binary before assuming kad is at fault"
		return c
	}

	c.Status = OK
	c.Detail = got.String()
	return c
}

// checkRuntime confirms the container runtime backing the chosen driver is
// actually running, which is the single most common reason 'minikube start'
// fails with an unhelpful message.
func checkRuntime(ctx context.Context, r runner.Runner, cfg *config.Config) Check {
	c := Check{Name: "runtime/" + cfg.Cluster.Driver}

	bin := cfg.Cluster.Driver
	if bin != "docker" && bin != "podman" {
		c.Status = OK
		c.Detail = "VM driver; no container runtime required"
		return c
	}

	if _, err := r.Look(bin); err != nil {
		c.Status = Fail
		c.Detail = bin + " not found on PATH"
		c.Fix = fmt.Sprintf("install %s, or set cluster.driver to one of: %s",
			bin, strings.Join(config.SupportedDrivers, ", "))
		return c
	}
	if _, err := r.Run(ctx, bin, "info"); err != nil {
		c.Status = Fail
		c.Detail = bin + " is installed but not responding"
		c.Fix = fmt.Sprintf("start %s (Docker Desktop, Rancher Desktop, 'colima start' or 'podman machine start') and re-run", bin)
		return c
	}

	c.Status = OK
	c.Detail = bin + " is responding"
	return c
}

// runtimes that commonly coexist and fight over the docker socket.
var competingRuntimes = []string{"docker", "podman", "colima", "rancher-desktop", "lima"}

// checkAmbiguousRuntime warns when several container runtimes are installed.
// Each ships its own socket and context, and minikube will silently pick one —
// which is how two developers on identical config get different clusters.
func checkAmbiguousRuntime(r runner.Runner) Check {
	c := Check{Name: "runtime/ambiguity"}

	var found []string
	for _, rt := range competingRuntimes {
		if _, err := r.Look(rt); err == nil {
			found = append(found, rt)
		}
	}
	if len(found) <= 1 {
		c.Status = OK
		c.Detail = "single container runtime on PATH"
		return c
	}

	c.Status = Warn
	c.Detail = fmt.Sprintf("%d container runtimes installed (%s); minikube will pick one via the active docker context",
		len(found), strings.Join(found, ", "))
	c.Fix = "pin it explicitly: 'docker context use <name>' before 'kad up', and record which one in your kad.yaml comments"
	return c
}

// checkResources verifies the cluster fits in whatever pool it is carved from.
//
// One check rather than two, because CPU and memory come from the same place
// and asking for it twice would run 'docker info' twice.
func checkResources(ctx context.Context, r runner.Runner, cfg *config.Config) []Check {
	name := "budget/" + cfg.Cluster.Driver

	b, err := ResourceBudget(ctx, r, cfg.Cluster.Driver)
	if err != nil {
		// Unknown is a warning, not a failure: it means kad could not ask, not
		// that the answer is bad. minikube will still refuse if it does not fit.
		return []Check{{
			Name:   name,
			Status: Warn,
			Detail: "could not determine how much cpu and memory the cluster can use",
			Fix:    "kad cannot pre-check the size; 'kad up' will report it if the cluster does not fit",
		}}
	}

	return []Check{
		cpuCheck(name+"/cpu", cfg, b),
		memoryCheck(name+"/memory", cfg, b),
	}
}

func cpuCheck(name string, cfg *config.Config, b Budget) Check {
	c := Check{Name: name}
	want := cfg.Cluster.CPUs

	switch {
	case want > b.CPUs:
		c.Status = Fail
		c.Detail = fmt.Sprintf("cluster.cpus is %d but %s has %d", want, b.Source, b.CPUs)
		c.Fix = fmt.Sprintf("set cluster.cpus to %d or fewer — %s", max(b.CPUs-1, 2), b.Fix)
	case want == b.CPUs:
		c.Status = Warn
		c.Detail = fmt.Sprintf("cluster.cpus (%d) is every core %s has", want, b.Source)
		c.Fix = "leave one spare; the machine will be unresponsive while the cluster builds"
	default:
		c.Status = OK
		c.Detail = fmt.Sprintf("%d of %d cores available to %s", want, b.CPUs, b.Source)
	}
	return c
}

func memoryCheck(name string, cfg *config.Config, b Budget) Check {
	c := Check{Name: name}

	wantMi, err := config.ParseQuantityMi(cfg.Cluster.Memory)
	if err != nil {
		c.Status = Fail
		c.Detail = fmt.Sprintf("cluster.memory %q: %v", cfg.Cluster.Memory, err)
		c.Fix = "write it as a quantity like 8Gi or 8192 in kad.yaml"
		return c
	}

	// Reserve a quarter for everything else that has to run there; a cluster
	// that swaps is worse than one that refuses to start.
	headroom := b.MemMi * 3 / 4
	switch {
	case wantMi > b.MemMi:
		c.Status = Fail
		c.Detail = fmt.Sprintf("cluster.memory is %s (%dMi) but %s has only %dMi",
			cfg.Cluster.Memory, wantMi, b.Source, b.MemMi)
		c.Fix = fmt.Sprintf("set cluster.memory to about %dMi — %s", headroom, b.Fix)
	case wantMi > headroom:
		c.Status = Warn
		c.Detail = fmt.Sprintf("cluster.memory (%s) is over 75%% of the %dMi %s has",
			cfg.Cluster.Memory, b.MemMi, b.Source)
		c.Fix = fmt.Sprintf("consider %dMi to leave room for everything else", headroom)
	default:
		c.Status = OK
		c.Detail = fmt.Sprintf("%s of %dMi available to %s", cfg.Cluster.Memory, b.MemMi, b.Source)
	}
	return c
}

// checkProfileCollision reports an existing profile of the same name, so 'kad
// up' can tell "resuming yours" apart from "about to adopt something else's".
func checkProfileCollision(ctx context.Context, r runner.Runner, cfg *config.Config) Check {
	c := Check{Name: "cluster/profile"}

	res, err := r.Run(ctx, "minikube", "profile", "list", "-o", "json")
	if err != nil || strings.TrimSpace(res.Stdout) == "" {
		// No profiles at all is the normal first-run case, and minikube exits
		// non-zero for it. Nothing to collide with.
		c.Status = OK
		c.Detail = "no existing profile named " + cfg.Profile()
		return c
	}
	if strings.Contains(res.Stdout, `"Name":"`+cfg.Profile()+`"`) ||
		strings.Contains(res.Stdout, `"Name": "`+cfg.Profile()+`"`) {
		c.Status = Warn
		c.Detail = "profile " + cfg.Profile() + " already exists"
		c.Fix = "'kad up' will reuse it; 'kad down' deletes it first if you want a clean build"
		return c
	}

	c.Status = OK
	c.Detail = "no existing profile named " + cfg.Profile()
	return c
}

// --- where the cluster's resources actually come from ---

// Budget is the pool a cluster is carved out of, and what to call it when
// telling the user it is too small.
type Budget struct {
	Source string // "Docker Desktop", "podman machine", "this host"
	CPUs   int
	MemMi  int
	Fix    string // how to make the pool bigger, if that is possible
}

// ResourceBudget answers the only question that matters: how much can the
// cluster actually get?
//
// For a VM driver that is the host. For the docker and podman drivers it is
// emphatically NOT the host — minikube runs the node inside the runtime's VM,
// so the ceiling is whatever that VM was given. Checking the host instead is
// how 'kad doctor' came to report "8Gi of 36864Mi requested" and pass, on a
// machine where Docker Desktop had 7834Mi and minikube refused to start.
func ResourceBudget(ctx context.Context, r runner.Runner, driver string) (Budget, error) {
	switch driver {
	case "docker":
		b, err := dockerBudget(ctx, r)
		if err != nil {
			return Budget{}, err
		}
		b.Source = "Docker Desktop"
		b.Fix = "raise it in Docker Desktop → Settings → Resources, or lower the value in kad.yaml"
		return b, nil
	case "podman":
		b, err := podmanBudget(ctx, r)
		if err != nil {
			return Budget{}, err
		}
		b.Source = "the podman machine"
		b.Fix = "recreate the machine with more (podman machine set --memory/--cpus), or lower the value in kad.yaml"
		return b, nil
	default:
		b, err := hostBudget(ctx, r)
		if err != nil {
			return Budget{}, err
		}
		b.Source = "this host"
		b.Fix = "lower the value in kad.yaml"
		return b, nil
	}
}

func dockerBudget(ctx context.Context, r runner.Runner) (Budget, error) {
	memRes, err := r.Run(ctx, "docker", "info", "--format", "{{.MemTotal}}")
	if err != nil {
		return Budget{}, err
	}
	bytes, err := strconv.ParseInt(strings.TrimSpace(memRes.Stdout), 10, 64)
	if err != nil {
		return Budget{}, fmt.Errorf("docker reported an unreadable memory total: %q", memRes.Stdout)
	}
	cpuRes, err := r.Run(ctx, "docker", "info", "--format", "{{.NCPU}}")
	if err != nil {
		return Budget{}, err
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(cpuRes.Stdout))
	if err != nil {
		return Budget{}, fmt.Errorf("docker reported an unreadable cpu count: %q", cpuRes.Stdout)
	}
	return Budget{CPUs: cpus, MemMi: int(bytes / 1024 / 1024)}, nil
}

func podmanBudget(ctx context.Context, r runner.Runner) (Budget, error) {
	memRes, err := r.Run(ctx, "podman", "info", "--format", "{{.Host.MemTotal}}")
	if err != nil {
		return Budget{}, err
	}
	bytes, err := strconv.ParseInt(strings.TrimSpace(memRes.Stdout), 10, 64)
	if err != nil {
		return Budget{}, fmt.Errorf("podman reported an unreadable memory total: %q", memRes.Stdout)
	}
	cpuRes, err := r.Run(ctx, "podman", "info", "--format", "{{.Host.CPUs}}")
	if err != nil {
		return Budget{}, err
	}
	cpus, err := strconv.Atoi(strings.TrimSpace(cpuRes.Stdout))
	if err != nil {
		return Budget{}, fmt.Errorf("podman reported an unreadable cpu count: %q", cpuRes.Stdout)
	}
	return Budget{CPUs: cpus, MemMi: int(bytes / 1024 / 1024)}, nil
}

func hostBudget(ctx context.Context, r runner.Runner) (Budget, error) {
	cpus, err := hostCPUs(ctx, r)
	if err != nil {
		return Budget{}, err
	}
	memMi, err := hostMemoryMi(ctx, r)
	if err != nil {
		return Budget{}, err
	}
	return Budget{CPUs: cpus, MemMi: memMi}, nil
}

func hostCPUs(ctx context.Context, r runner.Runner) (int, error) {
	switch runtime.GOOS {
	case "darwin":
		res, err := r.Run(ctx, "sysctl", "-n", "hw.ncpu")
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(res.Stdout))
	case "windows":
		res, err := r.Run(ctx, "powershell", "-NoProfile", "-Command",
			"(Get-CimInstance Win32_ComputerSystem).NumberOfLogicalProcessors")
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(res.Stdout))
	default:
		res, err := r.Run(ctx, "nproc")
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(res.Stdout))
	}
}

func hostMemoryMi(ctx context.Context, r runner.Runner) (int, error) {
	switch runtime.GOOS {
	case "darwin":
		res, err := r.Run(ctx, "sysctl", "-n", "hw.memsize")
		if err != nil {
			return 0, err
		}
		b, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
		if err != nil {
			return 0, err
		}
		return int(b / 1024 / 1024), nil
	case "windows":
		res, err := r.Run(ctx, "powershell", "-NoProfile", "-Command",
			"(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory")
		if err != nil {
			return 0, err
		}
		b, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
		if err != nil {
			return 0, err
		}
		return int(b / 1024 / 1024), nil
	default:
		res, err := r.Run(ctx, "sh", "-c", "grep MemTotal /proc/meminfo | awk '{print $2}'")
		if err != nil {
			return 0, err
		}
		kb, err := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
		if err != nil {
			return 0, err
		}
		return int(kb / 1024), nil
	}
}

// --- versions ---

type version struct{ major, minor, patch int }

func (v version) String() string { return fmt.Sprintf("v%d.%d.%d", v.major, v.minor, v.patch) }

func (v version) less(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

var versionRE = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

// parseVersion pulls the first semver-looking triple out of arbitrary tool
// output. Every tool formats --version differently and several change it
// between releases, so matching loosely is more robust than parsing each.
func parseVersion(s string) (version, error) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return version{}, fmt.Errorf("no version found in %q", strings.TrimSpace(s))
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return version{maj, min, pat}, nil
}

func toolVersion(ctx context.Context, r runner.Runner, bin string) (version, error) {
	var args []string
	switch bin {
	case "minikube":
		args = []string{"version", "--short"}
	case "kubectl":
		args = []string{"version", "--client"}
	default:
		args = []string{"version", "--short"}
	}
	res, err := r.Run(ctx, bin, args...)
	out := res.Stdout + res.Stderr
	if err != nil && strings.TrimSpace(out) == "" {
		return version{}, err
	}
	return parseVersion(out)
}

// installHint names the way this platform actually installs a tool. A macOS
// brew command shown to a Windows user is worse than no hint at all.
func installHint(bin string) string {
	docs := map[string]string{
		"minikube": "https://minikube.sigs.k8s.io/docs/start/",
		"kubectl":  "https://kubernetes.io/docs/tasks/tools/",
		"helm":     "https://helm.sh/docs/intro/install/",
	}
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf("brew install %s  (or see %s)", bin, docs[bin])
	case "windows":
		return fmt.Sprintf("winget install %s  (or see %s)", bin, docs[bin])
	default:
		return fmt.Sprintf("see %s", docs[bin])
	}
}
