# ACHAgent environment forwarding design

**Date:** 2026-09-02  
**Status:** Approved  
**Compatibility:** Breaking change to the `v1alpha1` CRD

## Goal

Give each `ACHAgent` its own Pod environment without requiring a dedicated
`AgentProfile`, then let the engine and each channel preparation hook opt into
only the variables they need.

Declaring a variable does not forward it. `engine.forwardEnv` and
`channels[].prepare.forwardEnv` are independent allowlists.

## User API

Both profiles and agents expose the same Pod-native `env` shape:

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: AgentProfile
spec:
  env:
    - name: HTTPS_PROXY
      value: http://proxy.example.com
```

```yaml
apiVersion: ach.ackstorm.ai/v1alpha1
kind: ACHAgent
spec:
  profileRef: {name: default}
  env:
    - name: GITLAB_TOKEN
      valueFrom:
        secretKeyRef:
          name: ach-agent-gitlab-ro
          key: GITLAB_TOKEN
    - name: GITLAB_BASE_URL
      value: https://git.ackstorm.com

  engine:
    forwardEnv: [SOME_AGENT_VAR]

  channels:
    - name: gitlab-mr-review
      type: webhook
      prepare:
        forwardEnv: [GITLAB_TOKEN, GITLAB_BASE_URL]
```

`AgentProfile.spec.extraEnv` is removed and replaced by
`AgentProfile.spec.env`. `ACHAgent.spec.env` is new. This is intentionally
breaking; no compatibility alias is retained.

`PrepareSpec.env` and `PrepareSpec.secretEnv` are removed from the operator CRD
and replaced by `PrepareSpec.forwardEnv`. The rendered `ach-agent` JSON contract
does not change.

## Resolution

The operator resolves the profile and agent environment by variable name:

1. Start with `AgentProfile.spec.env`.
2. Replace matching names with entries from `ACHAgent.spec.env`.
3. Append agent-only names.

An agent replacement is atomic: its complete `EnvVar` wins, including changing
a profile literal into a `secretKeyRef` or a profile secret into a literal.

Order is deterministic: inherited profile order is retained, replacements stay
in place, and agent-only entries follow in agent order. Both lists use
`listType=map` with `name` as the key, so duplicate names within one resource
are rejected by the API server.

Reserved `ACH_*` names remain forbidden in both resources. The operator owns
`ACH_TOKEN`, `ACH_BASE_URL`, `ACH_ENVIRONMENT`, `ACH_CONFIG_PATH`, and future
members of that namespace. User variables use application prefixes such as
`GITLAB_*`.

Every resolved entry is injected into the harness container. The engine still
receives only names selected by `engine.forwardEnv`.

## Prepare rendering

`channels[].prepare.forwardEnv` is operator-facing syntax. It is fully resolved
before the harness configuration is written:

| Resolved source | Rendered `ach-agent` configuration |
|---|---|
| `value` | `prepare.env[NAME] = VALUE` |
| `valueFrom.secretKeyRef` | `prepare.secretEnv[NAME] = {env: GENERATED_ALIAS}` |

For a secret, the Deployment receives a second environment entry named
`ACH_SECRET_<CHANNEL>_PREPARE_<NAME>` pointing at the same `secretKeyRef`. The
generated alias is rendered into `prepare.secretEnv`; plaintext never enters
the ConfigMap. Keeping this per-channel alias preserves the harness's existing
secret redaction and prevents a prepare secret from accidentally stripping an
explicit engine forwarding of the original variable name.

A `forwardEnv` name missing from the merged environment is ignored, matching
`engine.forwardEnv`: the variable remains unset rather than being defined as an
empty string. An empty list forwards nothing. Repeated names are rejected as a
set.

Only literal values and `valueFrom.secretKeyRef` are supported. Other Pod
`EnvVarSource` variants are rejected so the renderer has exactly two output
paths and does not grow speculative behavior.

## Secret lifecycle

Secret references from profile or agent `env` join the reconciler's referenced
secret set. Existing same-namespace key validation, salted secret hash rollout,
and Secret watch behavior apply unchanged.

Removing or changing a referenced Secret rolls the Pod through the existing
config hash. Missing Secrets or keys fail reconciliation before a workload is
reported ready.

## Failure behavior

- Invalid env shape or reserved `ACH_*` name: admission rejection.
- Missing `prepare.forwardEnv` name: ignored; the variable remains unset.
- Missing Secret/key: existing secret-resolution failure.
- A prepare script failure remains fail-closed in `ach-agent`.

## Implementation surfaces

- `api/ach/v1alpha1`: replace profile `extraEnv`, add agent `env`, replace
  prepare `env`/`secretEnv` with `forwardEnv`.
- `internal/agentrender`: merge environments, resolve prepare forwarding, render
  the unchanged harness `env`/`secretEnv`, and collect referenced Secrets.
- `internal/controller/ach`: build the Pod environment from the merged result
  plus generated prepare aliases.
- Generated deepcopy, CRDs, Helm CRD mirror, API reference, examples, field-shape
  golden, and vendored harness schema drift tests.

## Verification

Tests must pin:

1. Profile inheritance and agent override by name.
2. Reserved-name and unsupported-source admission failures.
3. Literal prepare forwarding into rendered `prepare.env`.
4. Secret prepare forwarding into a generated Pod alias and rendered
   `prepare.secretEnv`, with no plaintext in the ConfigMap.
5. Missing forward names are ignored and remain unset.
6. Secret reference validation, watch, and config-hash rollout include both
   profile and agent env sources.
7. Existing engine forwarding remains allowlisted and independent.
8. Generated CRDs, API docs, Helm mirror, examples, and field-shape golden agree.
