# Editions

ACH ships as one image, `ghcr.io/ackstorm/ach`, and one console. The Enterprise code is
always in the image; it runs only when you enable it.

| | Core | Enterprise (`enterprise.enabled: true`) |
|---|------|------------|
| Licence | Apache-2.0 | Core plus code under the ACKstorm Enterprise License |
| Operator, platform API, forwarder, content service, gateway, console | yes | yes |
| Overview from LiteLLM: spend, tokens, latency, allowance, request outcome, spend by key, usage by model | yes | yes |
| Overview generation evidence: signals, input/output ratio, cache-read share, top apps, tool uses, recent sessions | no | yes |
| `ach classifier` (conversation classification) | no | yes |
| Classification results, Traces and sessions | no | yes |
| Cost, Performance, Reliability, Runtime, Skills & context, Workloads analytics | no | yes |

## Enabling Enterprise

Set it in the Helm values:

```yaml
enterprise:
  enabled: true
```

The chart passes it to platform-api and the classifier as `ACH_ENTERPRISE`. Running the
Enterprise features in production requires an ACKstorm Enterprise licence agreement.

## With Enterprise disabled

- Every `/platform/observability/*` route (traces, sessions, context, workloads, cost, latency,
  performance, classification) answers 403 `enterprise_only`.
- `ach classifier` exits with a message that it is an Enterprise feature.
- platform-api refuses to start when `ACH_OBSERVABILITY_CLICKHOUSE_URL`,
  `ACH_OBSERVABILITY_THANOS_URL` or `ACH_CLASSIFIER_TAXONOMY_FILE` is set.
- The chart keeps the `classifier` and `platformApi.observability` values but renders
  nothing from them, so turning Enterprise on is the single value above.
- The console lists the Enterprise pages as "Enterprise only" and its Overview is the LiteLLM one.

## Licence

The core is under the [Apache License 2.0](https://github.com/ackstorm/ach/blob/main/LICENSE).
The code under `ee/`, `ui/src/ee/` and `cmd/ach/ee.go` is under the ACKstorm Enterprise
License ([`ee/LICENSE`](https://github.com/ackstorm/ach/blob/main/ee/LICENSE),
[`ui/src/ee/LICENSE`](https://github.com/ackstorm/ach/blob/main/ui/src/ee/LICENSE)),
a draft pending legal review.
