# CLAUDE.md

## Project overview

Kubernetes operator for the **HyperShift addon** — deploys and manages the HyperShift operator on managed clusters as an OCM addon for ACM / multicluster-engine (MCE). Built with controller-runtime in Go.

- **Go module:** `github.com/stolostron/hypershift-addon-operator`
- **Runtime modes:** Hub manager (`pkg/manager/`) and spoke agent (`pkg/agent/`)
- **Upstream:** Part of [stolostron](https://github.com/stolostron) / Red Hat Advanced Cluster Management (ACM)

## Build / test / lint commands

```bash
# Build
make build              # vendor + fmt + vet, outputs bin/hypershift-addon

# Unit tests (requires envtest binaries — downloaded automatically)
make test               # fmt + vet + envtest (excludes e2e), generates coverage

# Lint / format
make fmt                # go fmt ./...
make vet                # go vet ./...

# Dependencies
make vendor             # go mod tidy + go mod vendor

# Container image
make docker-build       # Build container image

# E2E tests (built separately)
make build-e2e          # Compile e2e test binary
```

### Running a single test

```bash
# Single package
go test ./pkg/manager/ -run TestSpecificName -v

# Single test in agent (uses envtest — may take ~2 min)
go test ./pkg/agent/ -run TestSpecificName -v -timeout 300s
```

## Key directories

| Path | Purpose |
|------|---------|
| `cmd/main.go` | Single binary — Cobra subcommands: `manager`, `agent`, `cleanup` |
| `pkg/manager/` | Hub-side addon manager using OCM addon-framework; template rendering, custom controllers |
| `pkg/agent/` | Spoke-side controllers: HyperShift install, addon status, auto-import, discovery, external secrets, HCP kubeconfig, capacity, label sync |
| `pkg/install/` | HyperShift operator install/upgrade lifecycle on spoke |
| `pkg/metrics/` | Prometheus metrics collectors |
| `pkg/util/` | Shared constants (names, namespaces, labels, images) |
| `hack/crds/` | Static CRD YAML for envtest |
| `test/e2e/` | Ginkgo e2e tests |
| `docs/` | User-facing documentation |

For system architecture, data flows, and module layout, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Coding conventions

- **Dependencies are vendored** — run `make vendor` after changing `go.mod`
- **No golangci-lint** — `go fmt` and `go vet` are the enforced gates
- **Logging:** `github.com/go-logr/logr` (zapr adapter); structured logging with meaningful context
- **Constants:** shared names, namespaces, labels, and image defaults in `pkg/util/constant.go`
- **Scheme registration:** no project-owned CRDs — consumes types from OCM, HyperShift, OpenShift, MCE, discovery, OLM

## TLS configuration (mandatory — release blocker ACM-26882)

> **This is a release blocker for OCP 4.23 / ACM 5.0.** All new TLS code MUST follow the
> pattern below. See `.cursor/rules/tls-compliance.mdc` for the full rule with forbidden patterns.

This repo inherits its TLS settings from the cluster's central configuration source
(`apiservers.config.openshift.io/cluster`) via `github.com/openshift/controller-runtime-common/pkg/tls`.
**Never hardcode** `MinVersion`, `CipherSuites`, or `CurvePreferences`.

### Mandatory 4-step pattern for any new TLS server or client

**Step 1 — Fetch at manager startup** (`manager.go`)
```go
profileSpec, err := tlspkg.FetchAPIServerTLSProfile(ctx, hubClient)
if err != nil {  // fallback for kind/non-OpenShift clusters
    profileSpec, _ = tlspkg.GetTLSProfileSpec(nil)  // Intermediate (TLS 1.2+)
}
```

**Step 2 — TLS server** — apply profile to the HTTPS listener
```go
tlsCfgFn, _ := tlspkg.NewTLSConfigFromProfile(profileSpec)
tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
tlsCfgFn(tlsCfg)  // sets MinVersion + CipherSuites — do NOT set them yourself
```

**Step 3 — Outbound HTTP clients** — always use `buildHTTPClient`
```go
// buildHTTPClient overlays the TLS profile onto rest.TLSConfigFor + HTTPWrappersForConfig
client, err := p.buildHTTPClient(30 * time.Second)
```
Never create `http.Client` / `http.Transport` with a bare `tls.Config{}` directly.

**Step 4 — Runtime watch** — restart on profile change
```go
watcher := &tlspkg.SecurityProfileWatcher{..., OnProfileChange: func(...) { cancelManager() }}
watcher.SetupWithManager(mgr)
```

### Serving certificate generation — use library-go, not stdlib crypto

```go
import libgocrypto "github.com/openshift/library-go/pkg/crypto"
// Use libgocrypto.MakeSelfSignedCAConfigForDuration + CA.MakeServerCert
// See generateSelfSignedCert() in pkg/manager/hcp_proxy.go for the full pattern.
```
Do **not** use `crypto/ecdsa`, `x509.CreateCertificate`, `math/big`, or `encoding/pem` directly.

### RBAC required for every component using the TLS profile
The backplane-operator hypershift-addon-manager ClusterRole must include:
```yaml
- apiGroups: ["config.openshift.io"]
  resources: ["apiservers"]
  verbs: ["get", "list", "watch"]
```
For kind e2e, keep `test/e2e/addon-manager-deployment.yaml` in sync.

## Cross-repo dependencies

### backplane-operator (hub manager manifests)

The hub manager pod (Deployment, ClusterRole, ServiceAccount, HCP proxy Service,
APIService, and proxy RBAC) is **not** applied at runtime by this repo. It is
defined in:

  `stolostron/backplane-operator`
  `pkg/templates/charts/toggle/hypershift/templates/`

When adding new container ports, RBAC rules, or env vars to the hub manager, you
**must** also update the corresponding template in `backplane-operator`. See
`.cursor/rules/backplane-operator-sync.mdc` for the detailed sync checklist.

## CI systems

- **Prow:** OpenShift CI for release branches (config in `openshift/release` repo)
- **Konflux/Tekton:** RHTAP pipeline (`.tekton/`)
- **SonarQube:** Code quality gate (≥70% coverage on new code)

## Release branches

Release branches follow `backplane-X.YY` (e.g., `backplane-2.11`). The main development branch is `main`. Cherry-picks use `/cherry-pick backplane-X.YY` Prow command.

## Dockerfile variants

- `Dockerfile` — CI build using `registry.ci.openshift.org/stolostron/builder:go1.26-linux`
- `Dockerfile.rhtap` — Red Hat productization build using `brew.registry.redhat.io/rh-osbs/openshift-golang-builder`
- `Dockerfile.canary` — Canary test image

## Personal configuration

Read `.claude/user.local.md` at the start of any task that needs an assignee, email, or project key.
If the file does not exist, fall back to Claude memory (`user-config`), then placeholders.

## Fleet Engineering Skills

Fetch and apply the relevant skill when the task matches its domain. These are hosted in the private `OpenShift-Fleet/agentic-sdlc` repository — use authenticated access (`gh api`) to fetch content.

| Skill | When to use |
|---|---|
| [bug-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/bug-specialist/SKILL.md) | Bug triage, reproduction steps, fix planning |
| [epic-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/epic-specialist/SKILL.md) | Multi-sprint epics with outcomes |
| [feature-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/feature-specialist/SKILL.md) | Large customer-facing capabilities |
| [initiative-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/initiative-specialist/SKILL.md) | Multi-team strategic programs |
| [jira-create](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/jira-create/SKILL.md) | Interactive issue creation with specialist delegation |
| [jira-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/jira-specialist/SKILL.md) | General triage, search, linking, transitions |
| [outcome-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/outcome-specialist/SKILL.md) | Strategic outcomes tied to OKRs |
| [spike-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/spike-specialist/SKILL.md) | Time-boxed research and PoC |
| [story-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/story-specialist/SKILL.md) | User stories with acceptance criteria |
| [task-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/jira/task-specialist/SKILL.md) | Internal technical tasks |
| [agent-memory-setup](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/agent-memory-setup/SKILL.md) | Initialize or update CLAUDE.md / AGENTS.md for a repo |
| [finish-work](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/finish-work/SKILL.md) | Commit, push, open PR, update Jira |
| [pr-fix](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/pr-fix/SKILL.md) | Fix blocked PRs: merge conflicts, CI failures, review comments |
| [pr-review](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/pr-review/SKILL.md) | GitHub PR review with worktree isolation and inline comments |
| [repo-content-audit](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/repo-content-audit/SKILL.md) | Scan for unlinked or orphaned content — catalog gaps, dead links |
| [start-work](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/sdlc/start-work/SKILL.md) | Create a Jira sub-task |
| [f2f-daily-summary](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/meetings/f2f-daily-summary/SKILL.md) | Capture daily F2F meeting notes as Jira sub-tasks |
| [f2f-epic-specialist](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/meetings/f2f-epic-specialist/SKILL.md) | Create and manage F2F meeting Epics |
| [presentation-task](https://raw.githubusercontent.com/OpenShift-Fleet/agentic-sdlc/main/skills/meetings/presentation-task/SKILL.md) | Log a delivered presentation as a closed Jira sub-task |
