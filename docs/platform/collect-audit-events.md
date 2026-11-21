# Collect audit events

ACH and LiteLLM write each [audit event](../reference/audit-events.md) as one
JSON line on stdout, marked `"audit": true`. Whatever already collects your pod
logs collects them too. This page shows how to pick them out, with Grafana Loki
and Grafana Alloy as the worked example.

You need pod logs from the ACH and LiteLLM namespaces in Loki, labelled with
their Kubernetes `namespace`. The examples use `ach` and `litellm`; change them
to yours. For model, tool and agent calls, install
[the LiteLLM audit callback](connect-litellm.md#audit-model-tool-and-agent-calls)
first.

## Query them in Loki

Nothing to configure. Select the two namespaces, keep the lines that carry the
marker, and parse them:

```logql
{namespace=~"ach|litellm"} |= `"audit":` | json | audit=`true`
```

The first filter is a cheap text match that both writers satisfy (ACH writes
`"audit":true`, LiteLLM `"audit": true`). `json` then turns every field of the
event into a label you can filter and group on:

```logql
# Everything one person did. ACH writes the actor as <namespace>/<email>, LiteLLM as <email>.
{namespace=~"ach|litellm"} |= `"audit":` | json | audit=`true` | actor=~`(.+/)?ana@example.com`

# Everything that was refused or failed.
{namespace=~"ach|litellm"} |= `"audit":` | json | audit=`true`
  | outcome!~`created|revoked|suspended|resumed|updated|issued|forwarded|success`

# One readable row per event.
{namespace=~"ach|litellm"} |= `"audit":` | json | audit=`true`
  | line_format `{{.actor}} {{.action}} {{.outcome}} {{.target_name}}`

# Events per action and outcome over the last hour.
sum by (action, outcome) (
  count_over_time({namespace=~"ach|litellm"} |= `"audit":` | json | audit=`true` [1h])
)
```

`json` replaces the dot in a field name with an underscore: `target.name` is
`target_name`, `key.id` is `key_id`.

## Give them their own stream

Stop here if a query is all you need. To treat audit events apart from the
other logs (their own retention, their own alert rules, their own access), have
the collector label them. In Alloy, add one stage to the `loki.process` block
your pod logs go through, after the stage that parses the container runtime
format:

```alloy
loki.process "pod_logs" {
  stage.cri {}

  stage.match {
    selector = `{namespace=~"ach|litellm"} |~ "^\\{.*\"audit\": ?true"`

    stage.static_labels {
      values = { audit = "true" }
    }
  }

  forward_to = [loki.write.default.receiver]
}
```

The label has one value, so it adds at most one stream per container. Every
audit event of every service is then one selector away:

```logql
{audit="true"} | json
```

The queries above work with `{audit="true"} | json` in place of everything up
to and including ``audit=`true` ``.

## Keep them longer than other logs

With the label in place, Loki can hold audit events for their own period. This
keeps them for one year and everything else for 30 days:

```yaml
limits_config:
  retention_period: 720h
  retention_stream:
    - selector: '{audit="true"}'
      priority: 1
      period: 8760h
compactor:
  retention_enabled: true
```

The compactor applies retention; without `retention_enabled` both periods are
ignored. An event holds the caller's email and address, so choose the period
with whoever answers for personal data in your organisation.

## Other collectors and destinations

The rule is the same in Fluent Bit, the OpenTelemetry Collector or Vector:
parse the line as JSON and route on `audit == true`. To send the events to a
SIEM or an object store as well, forward that route to a second output in the
collector. Field names are ACH's own; map them to your SIEM's schema there.

Without a collector, read them straight from the pods:

```bash
kubectl -n ach logs deploy/ach-platform-api | jq -cR 'fromjson? | select(.audit == true)'
kubectl -n litellm logs deploy/litellm | jq -cR 'fromjson? | select(.audit == true)'
```

Delivery is as reliable as your log collection: a line the collector never read
before the node rotated the log is lost.
