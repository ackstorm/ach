# KeyGrant

Lets pods running a named ServiceAccount obtain an Environment credential for one
identity without anyone minting an `ek-` by hand. The pod posts its ServiceAccount
token to platform-api and receives a short-lived ACH JWT standing for one stable `ek_`
held inside ACH.

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: KeyGrant
metadata:
  name: ai-run-k8s-doctor
  namespace: ach
spec:
  identity: ai.run@ackstorm.com        # exactly one email, lower-case
  environments: [agent-k8s-doctor]     # exact Environment names
  requesters:
    serviceAccounts:                   # exact "<namespace>/<name>"
      - ach-agent/ach-agent-control
  budget:                              # optional, per key this grant covers
    maxBudget: 50
    budgetDuration: 30d
```

- No wildcards or patterns. A grant allows every combination of its `environments` and
  `serviceAccounts`; lists from different grants are never combined.
- **A grant naming a shared ServiceAccount is usable by every pod running it.** Name the
  most specific ServiceAccount the agent has.
- **Writing a grant is acting as its identity.** Only platform admins may author them
  (the chart's `<release>-keygrant-editor` ClusterRole, bound to nobody by default;
  `admin` and `edit` do not include it). Never grant an identity on behalf of someone
  without their say-so, and do not widen an existing grant to make an error go away.
- The identity must have logged in to ACH once, and its teams must intersect the
  Environment's `authorizedTeams`.
- `spec.budget` caps each exchange key through its own `key:<id>` tag; with several
  covering grants the lowest `maxBudget` applies. Removing `spec.budget` leaves the last
  ceiling in place.
- Removing a grant entry cuts access within about 60 s and the operator then revokes the
  key; adding it back makes a new key with fresh spend history.

## Ready condition

| Reason | Cause and fix |
|--------|---------------|
| `IdentityUnknown` | `spec.identity` has never logged in to ACH. Have that user sign in once. |
| `EnvironmentMissing` | a name in `spec.environments` has no Environment CR. Fix the name or create the Environment. |

```bash
kubectl get keygrants -n <ns>
kubectl get keygrant <name> -n <ns> -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
```

Full contract: `docs/user-guide/key-exchange.md` in the ACH repo.
