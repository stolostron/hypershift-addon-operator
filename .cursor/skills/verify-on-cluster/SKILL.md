---
name: verify-on-cluster
description: Verify a Jira bug fix on the user's daily development cluster. Fetches issue details, runs cluster-side checks, simulates the failure scenario if needed, confirms the fix works, and posts findings back to Jira. Use when asked to "verify [JIRA-ID] on the cluster", "test the fix", or "check the cluster state for [issue]".
---

# Verify Bug Fix on Cluster

## Workflow

### Step 1 — Get cluster context
```bash
oc whoami --show-server      # confirm which cluster
oc get mce -o name           # confirm MCE is present
```
If the cluster is unreachable, ask the user for a new API URL + token.

### Step 2 — Fetch the Jira issue
Use `user-jira-mcp-server` → `get_issue` with the issue key.

Look for test/reproduction steps in these locations (in priority order):

1. **Comments** — scan all comments for phrases like "steps to reproduce", "how to test",
   "verification steps", "to reproduce", "test plan". These are usually written by the
   reporter or the engineer who fixed the bug.
2. **Description** — look for a "Steps to Reproduce" or "How to verify" section.
3. **Acceptance Criteria** field — if present, treat each item as a verification checkpoint.

Extract and list all steps found before proceeding to Step 3.

### Step 3 — Run the verification

**If Jira has explicit reproduction/test steps:**
Follow them exactly in order. For each step:
- Run the command or action described
- Capture the output
- Note whether the result matches what the bug described (failure) or what the fix expects (success)

**If Jira has no steps, derive them from the description:**
- **What is the bug state?** Simulate it if safe (reversible patch, temp secret, rollout restart).
- **What is the expected state after the fix?** Assert it is now true.
- **What does the fix code do?** Confirm the fix code path is reached in logs or cluster state.

Common cluster commands for this repo:
```bash
# Addon agent logs
oc logs -n open-cluster-management-agent-addon \
  -l app=hypershift-addon-agent -c hypershift-addon-agent --tail=50

# HyperShift operator deployment args
oc get deployment operator -n hypershift \
  -o jsonpath='{.spec.template.spec.containers[0].args}' | tr ',' '\n'

# Addon agent image
oc get deployment hypershift-addon-agent -n open-cluster-management-agent-addon \
  -o jsonpath='{range .spec.template.spec.containers[*]}{.name}: {.image}{"\n"}{end}'

# MCE version
oc get mce multiclusterengine -o jsonpath='{.status.currentVersion}'
```

### Building and Deploying a Dev Image (when testing local code changes)

Use the OpenShift in-cluster build system — no local container runtime required.

#### One-time setup (create the BuildConfig)
```bash
cd /path/to/hypershift-addon-operator
oc new-build --name=hypershift-addon-dev --binary --strategy=docker -n multicluster-engine
```

#### Build and deploy loop (repeat after each code change)
```bash
# 1. Pause MCE so it doesn't revert your image change
MCE_NAME=$(kubectl get mce -o jsonpath='{.items[0].metadata.name}')
kubectl annotate mce ${MCE_NAME} installer.multicluster.openshift.io/pause=true --overwrite

# 2. Scale down the in-cluster manager to prevent reconcile conflicts
kubectl scale deployment hypershift-addon-manager -n multicluster-engine --replicas=0

# 3. Fix any vet issues, then trigger the in-cluster build (uploads local source)
oc start-build hypershift-addon-dev --from-dir=. --follow -n multicluster-engine

# 4. Get the newly built image reference
DEV_IMAGE=$(oc get istag hypershift-addon-dev:latest -n multicluster-engine \
  -o jsonpath='{.image.dockerImageReference}')
echo "Built image: ${DEV_IMAGE}"

# 5. Scale the manager back up with the new image
kubectl scale deployment hypershift-addon-manager -n multicluster-engine --replicas=1
kubectl set image deployment/hypershift-addon-manager \
  -n multicluster-engine hypershift-addon-manager="${DEV_IMAGE}"

# 6. Overwrite HYPERSHIFT_ADDON_IMAGE_NAME so the spoke agent also uses the dev image
kubectl set env deployment/hypershift-addon-manager \
  -n multicluster-engine HYPERSHIFT_ADDON_IMAGE_NAME="${DEV_IMAGE}"

# 7. Wait for rollout
kubectl rollout status deployment/hypershift-addon-manager -n multicluster-engine

# 8. Verify the new image is running
kubectl get pod -n multicluster-engine -l app=hypershift-addon-manager \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[0].image}{"\n"}{end}'

# 9. Check the agent on local-cluster picked up the new image
oc get deployment hypershift-addon-agent -n open-cluster-management-agent-addon \
  -o jsonpath='{range .spec.template.spec.containers[*]}{.name}: {.image}{"\n"}{end}'
```

#### Run make test against the cluster
```bash
# Unit + integration tests (no cluster required)
make test

# Verify go vet passes before building
make vet

# If testing e2e (requires cluster, builds e2e binary first)
make build-e2e
# then run the e2e binary with your cluster kubeconfig
```

#### Teardown — restore MCE control
```bash
# Remove the dev image override (MCE will restore the original image on next reconcile)
MCE_NAME=$(kubectl get mce -o jsonpath='{.items[0].metadata.name}')
kubectl annotate mce ${MCE_NAME} installer.multicluster.openshift.io/pause- --overwrite
echo "MCE resumed — will restore managed images"
```

---

### Pausing the MCE Operator (required before patching managed resources)

The MCE operator continuously reconciles its managed components and will revert manual changes.
Pause it before making any temporary patches to deployments, images, or config:

```bash
# Get the MCE instance name
MCE_NAME=$(kubectl get mce -o jsonpath='{.items[0].metadata.name}')

# Pause MCE reconciliation
kubectl annotate mce ${MCE_NAME} installer.multicluster.openshift.io/pause=true --overwrite
echo "MCE paused — operator will no longer reconcile changes"

# ... make your changes / run verification ...

# Resume MCE reconciliation when done
kubectl annotate mce ${MCE_NAME} installer.multicluster.openshift.io/pause- --overwrite
echo "MCE resumed"
```

> **Important:** Always resume MCE after verification. Leaving it paused will prevent MCE from
> reconciling legitimate changes (upgrades, config updates, health recovery).

### Step 4 — Clean up
Revert any temporary resources created during simulation (secrets, patches, restarts).

### Step 5 — Post findings to Jira
Use `user-jira-mcp-server` → `add_comment` with:
- Cluster URL and MCE version
- Addon agent image SHA
- Fix commit / PR that was verified
- What was tested (commands run, simulation steps)
- Result: ✅ fix confirmed / ❌ bug still present
- Any scope notes (e.g., fix only effective at startup, not mid-run)

## Simulation Patterns

| Bug type | Safe simulation |
|---|---|
| Secret missing → addon skips step | `oc create secret ... --from-literal=...` then delete after |
| Deployment args stripped by MCE | `oc patch deployment --type=json` to remove args; restore by restarting addon |
| Feature flag / config state | Edit `hypershift-operator-install-flags` configmap; revert after |
| Addon agent startup path | `oc rollout restart deployment/hypershift-addon-agent -n open-cluster-management-agent-addon` |

## Scope Notes to Include

Always note if the fix only applies at:
- **startup=true** (pod restart required to trigger)
- **secret/image change** (periodic loop detects it automatically)
- **always** (periodic loop detects it without any trigger)

## Example Comment Template

```
h2. Fix Verification — [JIRA-ID]

*Cluster:* <API URL>
*MCE version:* <version>
*Addon agent image:* <image SHA>
*Fix commit:* <sha> (PR #NNN, merged YYYY-MM-DD)

h3. Test Procedure
[Steps taken, commands run, any simulations]

h3. Result
[Log excerpts or cluster state confirming the fix]

*Fix verified. Marking ticket for closure.*
```
