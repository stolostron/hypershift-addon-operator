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
Read: summary, description, comments (especially reproduction/verification steps written by the team).

### Step 3 — Run the verification
Follow any documented steps in the issue comments.
If no steps exist, derive them from the description:
- **What is the expected state?** Assert it is true.
- **What is the bug state?** Simulate it if safe (reversible patch, temp secret, rollout restart).
- **What does the fix do?** Confirm the fix code path is reached in logs or cluster state.

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
| Deployment args stripped by MCE | `oc patch deployment --type=json` to remove args; restore by re-patching the original args back (a pod restart alone will NOT restore mutated spec — the addon manager will reconcile the original spec on its next loop, or you can force it with `oc rollout restart`) |
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

*Fix verified.*
```

> **CVE / Vulnerability tickets:** Do NOT mark for closure yourself. Leave status as **In Progress**,
> reassign to `ocp-sustaining-admins`, and follow `cve-workflow.mdc`. Only Sustaining Engineering
> closes Vulnerability tickets after verifying the backport.
