# Enterprise features

ACH is open core. The image `ghcr.io/ackstorm/ach` always contains the
Enterprise code; it runs only when the chart enables it. Running the
Enterprise features in production requires an ACKstorm Enterprise licence
agreement: confirm the deployment has one before turning this on, and say so
to the user if it is unclear.

## Contents

- What the switch changes
- Personal observability (ClickHouse, Thanos)
- Conversation classifier
- What breaks when the switch is off

## What the switch changes

```yaml
enterprise:
  enabled: true
```

The chart passes it to platform-api and the classifier as `ACH_ENTERPRISE`.
With it on:

- `/platform/observability/*` (traces, sessions, cost, latency, performance,
  workloads, context, classification) serves each person's own data, and the
  console shows the Enterprise analytics and Overview evidence.
- `classifier.*` and `platformApi.observability.*` are rendered.

With it off, those values are kept in the values file but render nothing, so
turning Enterprise on later is this one value.

## Personal observability

Reads each user's own LLM traces from the Langfuse ClickHouse, and optionally
LiteLLM gateway metrics from a Prometheus-compatible query API.

```yaml
platformApi:
  observability:
    clickhouseUrl: http://clickhouse.langfuse.svc:8123
    secretName: ach-observability-clickhouse     # holds the read-only user
    projectIds: [default]                        # Langfuse project allow-list
    thanosUrl: http://thanos-query.monitoring.svc:10902   # optional
```

- The Secret must hold the keys `ACH_OBSERVABILITY_CLICKHOUSE_USER` and
  `ACH_OBSERVABILITY_CLICKHOUSE_PASSWORD`. Use a dedicated read-only ClickHouse
  user with access to metadata columns only, never input/output text.
- `clickhouseUrl` empty: the routes answer 503 `not_ready`. Set: `secretName`
  and a non-empty `projectIds` are required (the render fails otherwise).
- `thanosUrl` empty: the Performance view reports `not_instrumented`. It
  queries LiteLLM series labelled `user=<email>`.
- In a replicated ClickHouse, create the user on every replica (or with
  `ON CLUSTER`); a user that exists on one replica gives intermittent
  authentication failures.
- A Secret cannot be read across namespaces: sync a copy into the ACH release
  namespace.

## Conversation classifier

An async job that labels idle Langfuse sessions with one category from a
taxonomy and writes a Langfuse score; the console shows the distribution.

```yaml
classifier:
  enabled: true
  secretName: ach-classifier
  clickhouseUrl: http://clickhouse.langfuse.svc:8123
  projectId: default
  modelUrl: http://litellm.litellm.svc:4000/v1   # OpenAI-compatible base URL
  model: <a model name in LiteLLM>
  langfuseUrl: ""            # empty = dry run (classify and log, write nothing)
  onlyUser: ""               # label only this user's sessions during rollout
  interval: 10m
  idle: 30m
  lookback: 72h
  batch: 50
  taxonomy: {}               # empty = built-in taxonomy
```

- `secretName`, `clickhouseUrl` and `modelUrl` are required when enabled.
- The Secret holds `ACH_CLASSIFIER_CLICKHOUSE_USER`,
  `ACH_CLASSIFIER_CLICKHOUSE_PASSWORD` (a different ClickHouse user from the
  observability one: it needs `input`/`output` on `events_full` and `SELECT` on
  `scores`), `ACH_CLASSIFIER_MODEL_KEY` (a key accepted at `modelUrl`) and, to
  write scores, `ACH_CLASSIFIER_LANGFUSE_PUBLIC_KEY` /
  `ACH_CLASSIFIER_LANGFUSE_SECRET_KEY`. `langfuseUrl` set without the key pair
  refuses to start. Never put these in `extraEnv`: that reaches every
  Deployment, and this user can read conversation content.
- Rollout: dry run (check the logs) → `langfuseUrl` + `onlyUser` → clear
  `onlyUser`.
- `taxonomy` replaces the built-in categories. It is also mounted into
  platform-api so the console reads the same version; a change restarts
  platform-api and re-labels sessions inside `lookback`. Category ids are
  `domain.name` in `[a-z0-9_]`; never list `other` or `trivial`.

```yaml
classifier:
  taxonomy:
    rules:
      - "If the work is for a client account, classify by what was done, not by the client."
    categories:
      - id: engineering.software
        fits: "Code, scripts, automations."
      - id: operations.planning
        fits: "Schedules, capacity, logistics."
        notFits: "Planning software -> engineering.software."
```

## What breaks when the switch is off

platform-api refuses to start if any Enterprise-only variable is set while
`ACH_ENTERPRISE` is false: `ACH_OBSERVABILITY_CLICKHOUSE_URL`,
`ACH_OBSERVABILITY_THANOS_URL`, `ACH_CLASSIFIER_TAXONOMY_FILE`. The chart never
renders them in that case, so this only happens when someone adds them through
`extraEnv`; remove them or enable Enterprise. Every `/platform/observability/*`
route answers 403 `enterprise_only`, and `ach classifier` exits with a pointer to
`enterprise.enabled`.
