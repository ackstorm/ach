# Enable Enterprise features

The ACH image always contains the Enterprise code, and one Helm value turns it
on. This page turns it on and configures its two parts: personal
observability and the conversation classifier.
[Editions](../overview/editions.md) lists what each edition includes.

You need:

- A signed agreement with ACKstorm. It is required to enable Enterprise in any
  environment, evaluation included.
- A running [ACH install](install-the-chart.md).
- [Langfuse](https://langfuse.com/) receiving the traces of your LiteLLM, and
  access to its ClickHouse database.

## What the switch changes

```yaml
enterprise:
  enabled: true
```

With it on:

- The console shows the Enterprise pages: Traces, the analytics pages, and the
  extra evidence on the Overview. Each person sees their own data.
- The chart renders the `platformApi.observability` and `classifier` values
  below.

With it off, the chart keeps those values in your file and renders nothing
from them.

## Observability values

platform-api reads each user's own traces from the Langfuse ClickHouse
database, and can read LiteLLM metrics from a Prometheus-compatible query API.

```yaml
platformApi:
  observability:
    clickhouseUrl: http://clickhouse.langfuse.svc:8123
    secretName: ach-observability-clickhouse
    projectIds: [default]                                  # Langfuse projects to read
    thanosUrl: http://thanos-query.monitoring.svc:10902    # optional
```

| Value | Notes |
|---|---|
| `clickhouseUrl` | Empty leaves the pages without data. When set, `secretName` and at least one entry in `projectIds` are required |
| `secretName` | A Secret in the ACH namespace with the keys `ACH_OBSERVABILITY_CLICKHOUSE_USER` and `ACH_OBSERVABILITY_CLICKHOUSE_PASSWORD` |
| `thanosUrl` | Empty leaves the Performance page without gateway metrics. It reads LiteLLM series labelled `user=<email>` |

Create a ClickHouse user for this, read-only, with access to the metadata
columns and not to the text of conversations. On a replicated ClickHouse,
create the user on every replica, or sign-in to ClickHouse fails now and then.

## Deploy the classifier

The classifier labels each finished work session with one category and writes
the label to Langfuse as a score. The console shows how work divides across
the categories.

```yaml
classifier:
  enabled: true
  secretName: ach-classifier
  clickhouseUrl: http://clickhouse.langfuse.svc:8123
  projectId: default
  modelUrl: https://ach.example.com/v1     # an OpenAI-compatible address
  model: <a model of the classifier's Environment>
  langfuseUrl: ""                          # empty: label and log, write nothing
  onlyUser: ""                             # label only this user's sessions
```

The Secret named by `secretName` holds:

| Key | Content |
|---|---|
| `ACH_CLASSIFIER_CLICKHOUSE_USER`, `ACH_CLASSIFIER_CLICKHOUSE_PASSWORD` | A ClickHouse user that can read `input` and `output` on `events_full` and `SELECT` from `scores`. Not the observability user |
| `ACH_CLASSIFIER_MODEL_KEY` | A key accepted at `modelUrl` |
| `ACH_CLASSIFIER_LANGFUSE_PUBLIC_KEY`, `ACH_CLASSIFIER_LANGFUSE_SECRET_KEY` | A Langfuse key pair. Required when `langfuseUrl` is set |

Keep these in that Secret and never in `extraEnv`. `extraEnv` reaches every
component, and this ClickHouse user can read conversations.

Send the model calls through ACH: create an Environment for the classifier
with one model and a budget, create an environment key for it, and use that
key as `ACH_CLASSIFIER_MODEL_KEY`. The classifier's spend then counts against
that Environment.

Roll it out in three steps:

1. Leave `langfuseUrl` empty. The classifier labels sessions and logs the
   result without writing anything. Check its log:
   `kubectl -n ach logs deploy/ach-classifier`.
2. Set `langfuseUrl` and `onlyUser` to write scores for one person. Restart
   the Pod once and check that it does not label a session a second time.
3. Clear `onlyUser`.

The log holds session ids, labels and counts, never conversation text.

## Your own categories

The built-in categories describe ACKstorm's work. Replace them with
`classifier.taxonomy`; `{}`, the default, keeps the built-in ones.

```yaml
classifier:
  taxonomy:
    rules:
      - "If the work is for a specific client account, classify by what was done for it, not by the client."
    categories:
      - id: operations.fleet_planning
        fits: "Routes, vehicle assignment, load planning, delivery schedules, warehouse slots."
        notFits: "Software that does the planning -> technology.software_engineering."
      - id: operations.incidents
        fits: "Delays, damaged goods, claims, carrier incidents and how they were handled."
      - id: customer.service
        fits: "Answering a customer: emails, tickets, delivery status, complaints."
        notFits: "A commercial offer -> sales.offers."
      - id: sales.offers
        fits: "Quotes, tenders, rate cards and proposals for a prospect or customer."
      - id: finance.billing
        fits: "Invoices, payments, costs, budgets, reconciliations."
      - id: technology.software_engineering
        fits: "Code, scripts, automations, spreadsheets with formulas built as tools."
      - id: general.research_learning
        fits: "Getting information where nothing is produced: explaining, comparing, looking up facts."
```

| You set | The classifier always adds |
|---|---|
| `categories`: `id`, `fits`, and optionally `notFits` | An introduction that tells the model the session is data, never instructions |
| `rules`: extra rules, numbered from 4 | Rules 1 to 3: classify by subject, not by task or format; a mixed session takes the category of most of it; `other` only when nothing fits |
| | The categories `other` and `trivial`. Do not list them |

**Write each category as the model reads it.** A category becomes one line of
the prompt: `- <id>: <fits> Does not fit: <notFits>`. Say what belongs in it,
and name the category it is most often confused with.

**Ids** have the form `domain.name`, in lower-case letters, digits and `_`.

**Every change is a new version.** A custom taxonomy has the version `custom-`
followed by 8 characters derived from the prompt it produces. Changing an id,
a word of `fits`, a rule or the order makes a new version. The classifier then
labels again every session inside `lookback` (72 hours by default), and each
of those model calls is paid again. Comments and YAML layout do not change the
version.

**Report by `metadata.category`.** The score value is the position of the
category in the list, so the same number means another category in another
version. Group reports by `metadata.category` and `metadata.taxonomy`.

A change to `taxonomy` restarts the classifier and platform-api, which reads
the same categories for the console. The classifier does not start when the
taxonomy has no categories, an unknown field, a repeated or malformed id, an
empty `fits` or an empty rule.

## With the switch off

- The Enterprise pages of the console are marked "Enterprise only", and their
  API routes answer 403 `enterprise_only`.
- The classifier is not deployed.
- platform-api does not start if `ACH_OBSERVABILITY_CLICKHOUSE_URL`,
  `ACH_OBSERVABILITY_THANOS_URL` or `ACH_CLASSIFIER_TAXONOMY_FILE` is set
  through `extraEnv`. The chart never sets them with the switch off.

## Verify

```bash
kubectl -n ach rollout status deploy/ach-platform-api
kubectl -n ach logs deploy/ach-classifier --tail=20     # the taxonomy version and the category count
```

Open the console at `https://ach.example.com`: Traces lists your own sessions,
and the pages are no longer marked "Enterprise only".

## Related

- [`classifier`](../reference/helm-values.md#classifier) and
  [`platformApi`](../reference/helm-values.md#platformapi) in the Helm values
  reference.
- [Editions](../overview/editions.md)
