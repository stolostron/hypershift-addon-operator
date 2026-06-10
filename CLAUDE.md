# HyperShift Addon Operator — Claude Context

## Project

Go module: `github.com/stolostron/hypershift-addon-operator`

Deploys the HyperShift operator onto managed clusters as an OCM addon for ACM / multicluster-engine.

- Hub manager: `pkg/manager/`
- Spoke agent: `pkg/agent/`
- Install lifecycle: `pkg/install/`

## Key Commands

```bash
make build          # vendor + fmt + vet
make test           # fmt + vet + envtest (excludes e2e)
make vendor         # go mod tidy + go mod vendor
make docker-build   # build container image
```

## CVE / Vulnerability Workflow

See `.cursor/rules/cve-workflow.mdc` for the full step-by-step process.

Once a CVE fix lands on main: verify the PR, find the Jira ticket, add a handoff comment (PR links + affected versions + backport notes), reassign to `ocp-sustaining-admins`, transition to `In Progress`.

Parent initiative: [ACM-27405](https://redhat.atlassian.net/browse/ACM-27405)
