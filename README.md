# kad

An instant Kubernetes development environment, from one file.

```console
$ kad init
$ kad doctor
$ kad up
$ kad down
```

`kad` builds a local minikube cluster to a declaration you check in, installs
the tools and backing services your team needs, tells you how to reach them,
and deletes the whole thing again in one command.

## Install

`kad` is a single static binary with no runtime dependencies. Download the
archive for your platform from the [latest
release](https://github.com/partofaplan/kad/releases/latest), check it against
`checksums.txt`, unpack it and put `kad` on your PATH.

**macOS and Linux**

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')          # darwin | linux
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
VERSION=$(curl -fsSL https://api.github.com/repos/partofaplan/kad/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')

curl -fsSLO "https://github.com/partofaplan/kad/releases/download/${VERSION}/kad_${VERSION}_${OS}_${ARCH}.tar.gz"
tar -xzf "kad_${VERSION}_${OS}_${ARCH}.tar.gz"
sudo install "kad_${OS}_${ARCH}/kad" /usr/local/bin/kad
```

On macOS, Gatekeeper quarantines downloaded binaries. If it refuses to run:
`xattr -d com.apple.quarantine /usr/local/bin/kad`.

**Windows** (PowerShell)

```powershell
$version = (Invoke-RestMethod https://api.github.com/repos/partofaplan/kad/releases/latest).tag_name
$arch    = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }

Invoke-WebRequest "https://github.com/partofaplan/kad/releases/download/$version/kad_${version}_windows_$arch.zip" -OutFile kad.zip
Expand-Archive kad.zip -DestinationPath $env:LOCALAPPDATA\kad -Force
$env:PATH += ";$env:LOCALAPPDATA\kad\kad_windows_$arch"
```

**From source**, on any platform with Go 1.25 or later:

```bash
go install github.com/partofaplan/kad/cmd/kad@latest
```

### What kad needs installed

`kad` orchestrates `minikube`, `kubectl` and `helm`, and a container runtime for
minikube's `docker` or `podman` driver. It does not install them — but
`kad doctor` checks for each one, checks the version, and tells you how to get
it on your platform. Run that first.

## The declaration

Everything about an environment lives in `kad.yaml`:

```yaml
apiVersion: kad.aviture.dev/v1alpha1
kind: Environment
name: demo

cluster:
  driver: docker          # docker | podman | qemu2 | vfkit | hyperkit
  kubernetes: v1.31.0     # pinned on purpose
  cpus: 4
  memory: 8Gi
  disk: 40Gi

tools:                    # named catalog entries — see 'kad catalog'
  - harbor
  - observability
  - postgres

apps:                     # anything else, as a plain Helm release
  - name: web
    chart: nginx
    repo: https://charts.bitnami.com/bitnami
    version: 18.3.6
```

`kad up -dry-run` shows what that builds before it builds anything:

```
  cluster  minikube profile kad-demo
           docker driver, kubernetes v1.31.0, 4 cpu, 8192Mi

  wave 1
    ingress         minikube addon ingress
  wave 2
    harbor          harbor 1.16.2 → ns/harbor
    observability   kube-prometheus-stack 66.2.1 → ns/observability
  wave 3
    postgres        postgresql 16.2.1 → ns/data
    web             nginx 18.3.6 → ns/default
```

Note that `ingress` is in the plan without being in the file: Harbor requires
it, so `kad` adds it rather than failing on a dependency the user never typed.

## The catalog

`tools:` takes names, not configuration. Each name expands to a pinned chart, a
namespace, an install wave and a set of values already sized for a laptop —
Prometheus retains six hours rather than fifteen days, Harbor's Trivy scanner is
off, Grafana asks for 128Mi. That expansion is the point: it is the difference
between one word and two hundred lines of `values.yaml` that every user
otherwise rediscovers.

```console
$ kad catalog
  argocd          7.7.11             Argo CD GitOps controller
  cert-manager    v1.16.2            X.509 certificate management
  harbor          1.16.2             container registry with vulnerability scanning
  ingress         (minikube addon)   NGINX ingress controller
  jenkins         5.8.16             Jenkins CI controller
  metrics         (minikube addon)   metrics-server, for kubectl top and HPA
  minio           5.3.0              S3-compatible object storage
  nexus           64.1.0             Sonatype Nexus artifact repository
  observability   66.2.1             Prometheus, Alertmanager and Grafana
  postgres        16.2.1             PostgreSQL 16, single instance
  redis           20.6.1             Redis 7, single instance
```

Every version is pinned, and `make catalog-verify` checks that each one still
resolves upstream. An unpinned chart would let two runs of the same `kad.yaml`
produce different clusters, which is the failure this tool exists to prevent.

Anything not in the catalog goes under `apps:` as an ordinary Helm release, so
the catalog never becomes a thing you have to get added to before you can work.

## Guardrails

`kad doctor` runs before every `kad up`, and refuses to build on a host that
cannot support the declaration. Real output:

```
  ✓  minikube             v1.33.1
  ✓  kubectl              v1.34.1
  !  helm                 helm v4.2.3 is a major version ahead of what most charts are tested against
                         ↳ if a chart install fails oddly, retry with a helm 3.x binary before assuming kad is at fault
  ✗  runtime/docker       docker is installed but not responding
                         ↳ start docker (Docker Desktop, Rancher Desktop, 'colima start' or 'podman machine start') and re-run
  !  runtime/ambiguity    4 container runtimes installed (docker, podman, colima, lima); minikube will pick one via the active docker context
                         ↳ pin it explicitly: 'docker context use <name>' before 'kad up'
```

Two rules govern this:

**Every failure names its fix.** A check that reports a problem without saying
what to do about it has moved the confusion rather than removed it.

**Warnings never block.** Helm 4 is ahead of what most charts are tested
against, but it is not known-broken; four installed container runtimes is a
reproducibility hazard, not an error. Blocking on either would train people to
reach for `-skip-doctor`, and a guardrail everyone routinely disables is worse
than none.

## Teardown

`kad down` deletes the minikube profile and everything in it.

That is a single command with no orphan-resource risk, and it is the reason
`kad` puts every environment in its own profile named `kad-<name>`. Teardown is
not a reconciliation problem — it is `minikube delete`. `kad` refuses to act on
any profile without the `kad-` prefix, so it cannot delete a cluster it did not
create, whatever is in the config file.

## Air-gapped and mirrored registries

Nothing in the catalog hardcodes `docker.io` or a public chart host. Point both
at an internal mirror:

```yaml
registry:
  mirror: harbor.internal/proxy
  chartMirror: oci://harbor.internal/charts
```

Chart repositories are replaced wholesale — every catalog entry and every
`apps:` entry — and the mirror is added to the cluster's `--insecure-registry`
list, so an air-gapped run needs no per-app edits to find its charts.

**Image** references are a narrower story, and worth stating precisely: charts
disagree about where the registry lives in their values, so each catalog entry
names the path explicitly. Entries that do not yet name one (`harbor`, `nexus`,
`jenkins`, `observability`, `argocd`, `minio`) will still pull images from their
chart's own default registry. Adding a path is a one-line change per entry; it
has simply not been done for all of them yet.

## Commands

| Command | What it does |
| --- | --- |
| `kad init` | Write a starter `kad.yaml`, named after the directory |
| `kad doctor` | Check this machine can build the declaration |
| `kad up` | Build it. `-dry-run` plans, `-skip-doctor` overrides preflight |
| `kad status` | What is running, and the exact commands to reach each tool |
| `kad down` | Delete the cluster and everything in it |
| `kad catalog` | List the tools available by name |

All commands take `-f` to point at a declaration other than `./kad.yaml`.

## Building

```console
$ make build            # ./bin/kad
$ make install          # onto your PATH
$ make test             # the full suite — needs no cluster, no network
$ make dist             # release archives for all six platform targets
$ make catalog-verify   # needs network + helm: checks the pins against upstream
```

`make dist` cross-compiles for macOS, Linux and Windows on both amd64 and arm64,
and runs on every pull request — so a platform that stops building is caught
then, rather than at release time.

`make test` runs without minikube, helm, Docker or a network, because every
shell-out goes through an injectable `Runner`. That boundary is deliberate: it
is what lets the whole tool be tested on a machine that has none of the things
it orchestrates.

## Why not Nix

Considered and rejected for the critical path, in all three roles it could play
here.

**As the host toolchain** (a flake pinning `minikube`, `kubectl`, `helm`) it
solves a real problem — but the fix is verification, not provisioning. `kad
doctor` checks the versions in about a hundred lines and no new abstraction
layer. Providing them via Nix would add a daemon install, a `/nix` volume that
macOS upgrades break, `sudo` at install time, and an outbound dependency on
`cache.nixos.org` that an air-gapped shop cannot reach. Against "install one
binary", "first install Nix" is the larger ask.

**As the node OS** it would mean abandoning minikube's driver model and
rebuilding `addons`, `tunnel`, `image load` and `mount` — a worse minikube.

**As the packaging format** for the declared apps it would mean Helm → Nix →
YAML, a third representation of the same object, and the loss of every upstream
chart as published.

A `flake.nix` for contributors who already run Nix would be harmless. `kad`
working without one is the requirement.
