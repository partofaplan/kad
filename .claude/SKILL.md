---
name: kad-sdlc
description: Build and iterate on kad using Gitflow, automated testing and CI/CD. Use this skill whenever working on the kad codebase — writing code, setting up tests, configuring CI, or deploying. This skill covers the complete SDLC from feature branch to production readiness.
---

# kad Development Workflow

**Every rule in this document is stated exactly once.** If you are looking for a
rule and find only a link, follow the link — the target is authoritative and the
place you are standing is not a second copy that can drift out of agreement with
it. Source files (`Makefile`, `.github/workflows/*.yml`, and so on) are likewise
never reproduced here, only described and linked.

---

## Core Principle: everything kad creates lives inside one minikube profile it owns

`kad up` may create exactly one thing on a machine: a minikube profile named
`kad-<name>`. Everything else it does happens *inside* that profile. `kad down`
is therefore `minikube delete`, and it is complete by construction rather than
by diligence.

In practice this prohibits:

- **Never write outside the profile.** No global `helm repo add`, no edits to
  the user's kubeconfig current-context, no files in `$HOME`, no host entries.
  If `kad down` cannot reclaim it, `kad up` must not create it.
- **Never act on a profile without the `kad-` prefix.** Not to read it, not to
  install into it, and above all not to delete it.
- **Never require a tool that `kad doctor` does not check for.** A missing
  dependency must surface as a named check with a fix, not as a crash in the
  middle of a build.
- **Never install anything unpinned.** Two runs of one `kad.yaml` that produce
  different clusters defeat the entire purpose of the tool.
- **Never hardcode a registry or chart host.** Every reference resolves through
  `registry.mirror` / `registry.chartMirror`, so the air-gapped path is a
  configuration change and not a code change.

The falsifiable check: **if `minikube delete -p kad-<name>` does not remove it,
kad should not have created it.** Any change that makes that sentence false is
wrong, whatever else it improves.

## The Loop: Branch → Done → Review → Merge → Validate

Work proceeds in **loops**. One loop is one unit of change: a feature, a fix, a
refactor. Every loop follows the same five gates, in order, with no step skipped
and no step reordered. These are requirements, not suggestions.

```
  1. BRANCH    cut feature/<name> from develop
       ↓
  2. DONE      every cluster-free test layer passes; merge request opened;
               every CI check green
       ↓
  3. REVIEW    evaluated by a DIFFERENT agent than the author
       ↓
  4. MERGE     only on approval → CI tags the version → publishes
       ↓
  5. VALIDATE  AUTOMATIC, after the merge, against the validation runner
```

### Nothing before the merge touches a real cluster

**A feature branch must not touch a real cluster** — not the shared one, not
a colleague's, not a throwaway one inside a CI runner.

What a merge request runs is therefore exactly the self-contained set:
`make lint`, `make test`, `make dist`, and the version-arithmetic suite. Every
shell-out in kad goes through an injectable `Runner`, which is what makes the
whole tool testable without the things it orchestrates.

Everything needing the real thing happens **after** the merge, automatically, in
Gate 5.

**Know what this costs, because it is a deliberate trade and not a free one.** A
change that breaks the core behaviour now merges before anything catches it. Be
explicit about which post-merge failures are contained and which are not — the
ones that fail *before* publishing cost a bad commit, and the ones that fail
*after* publishing have already shipped an artifact that cannot be unshipped.

Write that distinction down for this project, because someone will read the
first case and assume it covers both.

An on-demand integration command against a cluster of your own is a **tool, not
a gate** — reach for it deliberately when changing risky behaviour and you want
the feedback early.

### Where work comes from

The backlog is **GitHub Issues**, not a file in this repository.

```bash
gh issue list                    # what is waiting
gh issue view <n>                # why, and what was already decided
gh issue create --label enhancement --title "..." --body "..."
```

That is a deliberate choice about cost. Every merge goes through the five gates
— a branch, an independent review, CI, a version bump and a deploy. Correct for
changing the software, absurd for writing down an idea: a checked-in backlog
would cut a version every time someone had a thought, and would rot the moment
one entry went stale.

Issues cost nothing to open and carry the reasoning next to the item.

**When a decision defers something, open an issue before the context is lost,**
and say in the merge request that you did. A deferral recorded only in a commit
message is a deferral nobody will find.

Write the issue so the next person starts from an answer rather than a hunt: what
it is, how to reproduce it, what was already tried, and **why it was left**.

### Closing issues

**Close an issue by hand as soon as its fix merges to `develop`.**

Not at the next promotion, and not after post-merge validation. An issue that is
fixed but still open makes `gh issue list` useless — it stops showing what is
actually outstanding, which is the only thing it is for. If a later check fails,
reopen it; that is rarer than the confusion caused by holding it open.

```bash
gh pr merge <n> --merge --delete-branch
gh issue close <n> -c "Fixed in #<pr>, merged to \`develop\`."
```

GitHub will not do this for you. A closing keyword acts only on the **default
branch**, so `Closes #14` in a merge request body or a commit message leaves the
issue open through the integration merge and fires only at the next promotion —
too late to be useful.

Keep writing `Closes #14` in the **commit message** anyway: it records the link,
and it survives because merge requests here are merged rather than squashed. The
keyword firing again at promotion is a harmless no-op on an already-closed issue.

### Gate 1 — One branch per loop

**Every change starts on a feature branch cut from `develop`.** No
exceptions, including one-line fixes, documentation edits and changes made by an
AI assistant. `develop` and `main` are never committed
to directly.

```bash
git checkout develop
git pull --ff-only
git checkout -b feature/<short-description>
```

Branch names describe the change, not the author or the tool.

Why this is a hard rule rather than a preference: every push to
`develop` cuts a version and publishes a version tag. A direct
commit is therefore a release *and* a deployment, made without review, that
cannot be undone without burning a version number.

Feature branches are short-lived and single-purpose: one loop, one branch, one
merge request. Do not continue a finished loop's branch into the next, and do not
accumulate unrelated changes on one — **a reviewer cannot meaningfully approve a
branch that does three things.**

Both branches are protected and reject direct pushes, so the rule is enforced by
the forge rather than by discipline alone.

### Gate 2 — Definition of Done

A loop is **done** when the self-contained checks pass and CI agrees. All of
these MUST be true before the change is offered for review:

| Check | Command | Proves |
| --- | --- | --- |
| Tests | `make test` | The Go suite and the version arithmetic. Needs no cluster, no helm, no network |
| Lint | `make lint` | No new correctness or style violations |
| Generated code current | `go mod tidy` leaves no diff | Committed artefacts match source |
| CI green | On the open merge request | It passes somewhere other than your machine |

**"Tests pass" means you ran them and read the output.** A loop is not done
because the change looks right, because it compiled, or because the tests were
passing before you started. Green locally but red in CI is **not done**.

If a check cannot pass for a defensible reason, say so explicitly in the merge
request. Do not open one claiming done when it is not.

### Gate 3 — Independent agent review

Every merge MUST go through a merge request. There are no direct merges and no
exceptions for small changes.

**The merge request MUST be evaluated by a different agent than the one that
wrote the change.** This is the central rule of this gate.

An agent reviewing its own work is not review. It re-derives the same
assumptions that produced the code, so the failure modes it missed while writing
are exactly the ones it will miss while reading. Worse, it already believes the
change is correct — it wrote it — so it reads to confirm rather than to falsify.

The reviewing agent MUST:

- Start from the **diff and the merge request description**, not the authoring
  conversation. A fresh context is the point.
- **Independently verify the Definition of Done** rather than trusting the
  description. Run the tests. A merge request asserting "tests pass" is a claim
  to check, not evidence.
- Look for what the author could not see: unhandled error paths, missing
  coverage for the change, assumptions that hold only on the author's machine,
  breaking changes to a published interface.
- Return an explicit verdict — **APPROVE**, or **REQUEST CHANGES** with
  specific, actionable findings. "Looks good" is not a verdict.

The authoring agent MUST NOT approve its own merge request, and MUST NOT merge on
the strength of its own assessment.

```bash
/code-review <pr-number>           # a different agent evaluates it
```

Blocking findings are fixed **on the same feature branch** and re-reviewed. A
finding is not resolved by arguing it away in a comment: either change the code,
or explain in the merge request why it does not apply and let the reviewer decide.

#### Knowing when to stop reviewing

A review loop does not converge on its own, and a long one is a signal rather
than a virtue. Two things to watch for:

- **A fix that breaks the previous fix.** When a round's findings are about
  code the *last* round introduced, the loop is generating roughly as many
  problems as it removes. Stop fixing and start filing.
- **Findings that need equipment this project does not have.** A fault requiring
  infrastructure nobody runs cannot be reproduced, cannot be verified fixed, and
  is not worth blocking a release on.

The useful question is not *is this technically a defect* — that has no natural
stopping point — but **does this break `declare, preflight, build, tear down`**. Fix what does. File what
does not, with the reproduction and the reason it was left.

Say plainly in the merge request which findings were fixed and which were filed.

### Gate 4 — Merge, tag, publish

Only an approved merge request may be merged. The merge is what authorises the
version.

**Merge; never squash.** The closing keywords and the reasoning live in
individual commit messages, and a squash replaces them with one synthetic
commit. Use `--merge`, and if a branch has fallen behind, rebase and force-push
with `--force-with-lease` rather than squashing to resolve it.

Nothing is tagged or released that did not pass an independent review.

### Gate 5 — Automatic post-merge validation

[`.github/workflows/validate.yml`](../.github/workflows/validate.yml) runs on
every push to `develop`. It is the only place the core path is exercised end to
end, because nothing before a merge may touch a real cluster.

It builds `kad`, writes a small declaration, and runs
`doctor` → `up` → `status` → `down` against a throwaway minikube on a
GitHub-hosted runner. It then asserts the Core Principle directly: the profile
is gone, `~/.config/helm/repositories.yaml` has no `kad-` entries, and the
runner's `kubectl` current-context was never repointed. Finally it runs `down`
a second time, because teardown must be idempotent.

**Every failure on `develop` is contained.** The only thing a `develop` merge
publishes is a git tag, and a tag costs a version number and nothing else. No
binary reaches a user until somebody dispatches
[`release.yml`](../.github/workflows/release.yml) from `main` by hand — which is
the whole reason cutting a release is a separate, manual act rather than a side
effect of a merge. The uncontained case that the template warns about does not
arise here, and if that ever changes — an image published on merge, a Homebrew
tap updated automatically — this paragraph is the first thing that must be
rewritten.

This gate **cannot block a merge**: it runs after one. It catches what escaped,
it does not prevent escape.

### Working unattended

When working without someone to ask, make the call and **write down why**.

Every autonomous decision goes in a comment on the issue before it is closed:
the option taken, the options rejected, and what would change the answer. The
reasoning has to outlive the session it happened in, and an issue closed with
`Fixed in #12` teaches nobody anything.

State the judgement calls in the merge request too, especially the ones a
reviewer would otherwise have to reverse-engineer from the diff.

### Documentation-only changes

A change touching only documentation may skip the build, test and release
pipeline. Documentation cannot alter what is built, and running the suite on one
only delays the merge.

Documentation here means `*.md`, `LICENSE`, and `.claude/**`. Everything else is
code — including `.github/workflows/**`, the `Makefile`, `hack/**` and
`scripts/**`, which decide what gets built, tagged and published.

Note that `README.md` is shipped inside every release archive, so a docs-only
change does still alter the artifact's contents. It cannot alter the binary,
which is what the pipeline exists to check.

**This is not implemented.** The suite is fast and cluster-free, so skipping it
buys a minute and costs a gate; the traps below are why the shortcut is worth
less than it looks. Read them before adding it.

**Two traps, if you implement this.** Both come from the same fact: a job
*skipped* by an `if:` condition **satisfies** a required status check.

- Do not use `paths-ignore`. A workflow that never runs leaves required checks
  pending forever and the merge request permanently unmergeable, with no
  override if admin enforcement is on.
- Gate on a **definite** answer only. A condition like `== 'true'` is implicitly
  ANDed with `success()`, so a *failed* classification job skips every gated job
  — and skipped satisfies the requirement, letting a code change merge with
  nothing having run. Use `!cancelled() && ... != 'false'` so only an explicit
  negative skips anything.

The classifier itself must fail open: anything ambiguous runs the full pipeline.
The cost of being wrong is one wasted run, never an unchecked merge.

### What never to do

This is the complete list.

- **Never commit directly to `develop` or `main`,**
  including fast-forward merges.
- **Never open a merge request before the Definition of Done is met.**
- **Never make `a real cluster` part of a merge request's checks,** and never
  point a feature branch at anything anyone else uses.
- **Never keep anything you care about on `the validation runner`.** It tracks
  `develop` automatically and its test data is deleted on every run.
- **Never review your own change.** If you wrote it, a different agent reviews it.
- **Never merge an unreviewed or change-requested merge request.**
- **Never squash a merge request.**
- **Never create a version tag by hand.** CI derives the next version from the
  highest existing tag; a hand-made tag silently reassigns everything after it.
- **Never delete or move a published tag.** The artifact is already published
  against it; the tag and the artifact would disagree.
- **Never tag or release a commit that did not come through an approved merge
  request.**

---

## Versioning: RELEASE.MAJOR.MINOR

Three places, each moving on a different event:

| Event | Effect | Example |
| --- | --- | --- |
| Merge into `develop` | MINOR increments | `1.2.3` → `1.2.4` |
| Merge into `main` | MAJOR increments, MINOR resets | `1.2.4` → `1.3.0` |
| A release package is cut | RELEASE increments, the rest reset | `1.3.0` → `2.0.0` |

**This is not SemVer, and the middle place in particular does not mean what
SemVer means by MAJOR.** A promotion moves it whatever the change contained.
Communicate breaking changes in the release notes, because no place in the
version number signals them.

**RELEASE moves only when a release package is made**, which is a decision rather
than a side effect of a merge. That is the whole reason it is a separate place:
promoting readies work, and cutting a release ships it, and those are not the
same act.

### Who assigns versions

CI does, never a human. The version is derived from the highest existing tag, so
**the tags are the source of truth** — no VERSION file to drift, and no commit
written back to a branch.

Keep the arithmetic in **one script with its own tests**, not inline in a
workflow. Two workflows need the same rules, and two copies eventually disagree
about what "next" means.

---

## Gitflow procedures

```bash
# start a loop
git checkout develop && git pull
git checkout -b feature/my-change

# finish it: Definition of Done first, then a reviewed merge request
gh pr create --base develop --fill
/code-review <pr-number>          # a DIFFERENT agent
# merge only on APPROVE — the merge cuts the next MINOR
```

### Promoting develop to main

A promotion **is a release; it gets *more* scrutiny than a feature, not less.**

```bash
gh pr create --base main --head develop \
  --title "Release: promote develop to main" --fill
/code-review <pr-number>
# merge only on APPROVE
```

A promotion promotes **reviewed work**; it does not introduce new work. Findings
raised against it are fixed on `develop` through the normal loop,
and the promotion picks them up.

Post-merge validation usually runs on `develop` only, so a promotion
gets no validation run of its own — **the gate is the validation run on the
tree being promoted.** Point at a specific run, and re-point it if the branch
moves. Do not restate that claim from memory after fixes land.

Then bring `main` back so the branches do not diverge:

```bash
git checkout -b chore/back-merge develop
git merge --no-ff main
gh pr create --base develop --title "Back-merge" --fill
```

`develop` is protected and rejects direct pushes. It also matters
for a second reason: a hotfix reaches it **only** through this back-merge, and
pushing directly would carry hotfix code in with no review.

### Hotfixes

A production fix still goes through a branch, but branches from
`main`, and reaches `develop` through the back-merge
above — never by cherry-pick, which leaves the two branches claiming different
histories for the same fix.

---

## Local development setup

Go 1.25 or later, and nothing else to run the suite.

```bash
make test     # the full suite: no cluster, no helm, no network
make lint     # go vet, gofmt, shellcheck, go.mod tidiness
make build    # ./bin/kad
make dist     # release archives for all six platform targets
```

To exercise the tool for real you also need `minikube`, `kubectl`, `helm` and a
container runtime — but only for running it, never for testing it. That
distinction is the whole architecture; see **Testing strategy** below.

`make catalog-verify` checks every pinned chart version still resolves upstream.
It needs the network and `helm`, which is exactly why it is not part of
`make test`.

## Architecture and code patterns

```
cmd/kad            main, nothing else
internal/config    kad.yaml: schema, defaults, validation
internal/catalog    the built-in tools, pinned, with laptop-sized values
internal/doctor    preflight checks
internal/cluster   the minikube profile lifecycle
internal/hydrate   planning and installing releases in waves
internal/cli       the command surface
internal/runner    the seam: every external command goes through this
hack/              version arithmetic, called by CI
scripts/           developer tools that need the network
```

**Every shell-out goes through `runner.Runner`.** This is the single most
important shape in the codebase, and the reason the whole tool is testable
without minikube, helm, Docker or a network. A function that calls `exec.Command`
directly has broken Gate 2 for every test that reaches it. Take a `Runner`,
and use `runner.Fake` in tests.

**Prefer a pure function that builds arguments over a function that runs them.**
`cluster.StartArgs` returns a `[]string` and is asserted directly in a test;
`cluster.Start` is the thin wrapper that hands it to a `Runner`. The flags are
the part most likely to regress, and that shape makes them cheap to pin down.

**Validation reports everything at once.** `config.Validate` accumulates errors
rather than returning the first, so a user fixes their file in one pass. Follow
that when adding fields.

**Every doctor check that fails names its fix.** A check that reports a problem
without saying what to do about it has moved the confusion rather than removed
it. There is a test asserting this for every failing check; do not exempt one.

**Warnings never block.** `Fail` stops `kad up`; `Warn` does not. A guardrail
people routinely disable with `-skip-doctor` is worse than no guardrail, so
reserve `Fail` for conditions that genuinely cannot produce a working cluster.

**Never mutate the builtin catalog.** `catalog.Resolve` deep-copies before
applying a mirror. Two resolves in one process must not affect each other.

## Testing strategy

| Layer | Command | Needs | Gate |
| --- | --- | --- | --- |
| Go unit tests | `make test` | nothing | 2 |
| Version arithmetic | `hack/next-version_test.sh` | git | 2 |
| Cross-compilation | `make dist` | nothing | 2 |
| Chart pins resolve | `make catalog-verify` | network, helm | on demand |
| Core path end to end | `validate.yml` | a real cluster | 5 |

**The boundary is the first three rows against the last.** Everything in Gate 2
runs on a machine with none of the software kad orchestrates, which is what lets
a merge request be checked anywhere, including on a Windows runner. The moment a
unit test needs a cluster, that property is gone for good — so it is not a
preference, it is the line.

The Go suite runs on Linux, macOS and Windows in CI, because kad ships a binary
for all three. `-race` runs on Linux only: it needs cgo and a C toolchain, and
the races it would find are not platform-specific.

## Build tooling

[`Makefile`](../Makefile) is the entry point for everything.

`make dist` cross-compiles six targets (darwin, linux and windows on amd64 and
arm64), stages each with the binary, `README.md` and `LICENSE`, archives it
(`.tar.gz`, or `.zip` for Windows) and writes `checksums.txt`. It runs on every
pull request, so a platform that stops building is caught then rather than at
release time.

[`hack/next-version.sh`](../hack/next-version.sh) owns the version arithmetic
and has [its own suite](../hack/next-version_test.sh). It is shell rather than
Go because CI calls it directly, and its mistakes are the only irreversible ones
in this repository: a wrong answer mints an immutable tag.

## CI/CD pipeline

| Workflow | Trigger | Does |
| --- | --- | --- |
| [`ci.yml`](../.github/workflows/ci.yml) | PR + push to `develop`/`main` | lint, test on 3 OSes, version arithmetic, cross-compile, then tag on a push |
| [`validate.yml`](../.github/workflows/validate.yml) | push to `develop` | Gate 5: the core path against a real cluster |
| [`release.yml`](../.github/workflows/release.yml) | manual, `main` only | cuts RELEASE, builds archives, publishes a GitHub Release |

Decisions in there that look wrong until you know why:

- **`ci.yml` triggers on `push` only for `develop` and `main`.** Feature
  branches are covered by `pull_request`, which tests the merge result rather
  than the branch tip. Listing them in both ran everything twice per commit.

- **`develop`/`main` runs are never cancelled.** They mint tags, and a
  half-finished tagging run is worse than a slow queue. PR runs are cancellable.

- **`release.yml` normalises its `dry_run` input through a `case` before using
  it.** `gh workflow run -f dry_run=false` sends the *string* `"false"`, and in
  GitHub expressions every non-empty string is truthy — so `!inputs.dry_run`
  would be false and a real release would silently do nothing while reporting
  success. It fails safe: only an explicit `false` publishes.

- **The `dry_run` value reaches the script through `env:`, not interpolation.**
  That job holds `contents: write`, and dispatch is the one way into it that
  does not pass review.

- **Action SHAs are pinned, with the version in a trailing comment.** A tag can
  be moved; a SHA cannot.
