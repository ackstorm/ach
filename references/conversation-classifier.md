# Conversation classifier

`ach classifier` labels each work session with one taxonomy category and writes the
label to Langfuse as a score (`ach_classification_category`). It is the first step
toward evals. It is an Enterprise feature: the chart renders it only with Helm `enterprise.enabled: true`.
Code: `ee/classifier/`, command: `ee/cmd/classifier.go`.

Read before changing the classifier's reads, prompt, taxonomy handling or model.
Deploying it and writing a custom taxonomy: `docs/platform/enable-enterprise.md`.

## 1. How it works

1. **Read.** Every `ACH_CLASSIFIER_INTERVAL`, it lists sessions idle for longer than
   `ACH_CLASSIFIER_IDLE` from Langfuse ClickHouse, as the read-only user
   `ach_classification_ro`. A session that already has an
   `ach_classification_category` score for the current taxonomy version is skipped,
   so each session is labelled (and paid for) once: a restart or a session that
   resumes does not re-label it. A `trivial` label is the exception: it cost no model
   call, and a session that resumes may turn into real work, so it is looked at again.
   Changing `Builtin.Version` re-labels on purpose; a custom taxonomy gets a new
   version by itself (`docs/platform/enable-enterprise.md`, "Your own categories").
   This reads `default.scores`, so the user needs `SELECT` on it as well as on
   `events_full`; without that grant the listing fails and no model call is made.
2. **Convert.** It reads one session at a time and converts the session to OpenTelemetry
   GenAI messages (`otel.go`). That is the contract: only the reader knows how Langfuse
   or a client stores a conversation.
3. **Build the model input.** It builds a text from the person's turns and the session's
   tool calls, capped at 24,000 characters (`build.go`).
4. **Skip trivial sessions.** A session with one model call, or with little person text
   and no tool calls, is labelled `trivial` without asking the model.
5. **Classify.** One model call returns `{"category", "confidence"}`.
6. **Write.** It writes a NUMERIC score. The value is the category's index in
   `Taxonomy.Categories`; the metadata holds `category`, `domain`, `taxonomy`, `model` and
   `confidence`. NUMERIC because Langfuse 4.5 in `events_only` mode dropped the text of
   CATEGORICAL and TEXT scores; 4.71 keeps it (measured), and NUMERIC works on both.

Without `ACH_CLASSIFIER_LANGFUSE_URL` it is a dry run: it classifies and logs, and
writes nothing. With it, both Langfuse keys are required. Logs carry ids, labels and
counts, never conversation content.

## 2. Model choice

The default is `ackstorm.smart`, called through the gateway's `/v1/chat/completions`.
It stays inside our infrastructure and costs about $0.0043 per session.

Measured on 2026-10-27 over 34 sessions mixed by client and user. There are no human
labels, so the figures are agreement with `smart`, not accuracy:

| Model | Same category as `smart` | Same domain | Cost per session |
|---|---|---|---|
| `ackstorm.smart` (gemini-3.8-flash) | reference | reference | $0.0043 |
| `ackstorm.lite` (gpt-oss-120b) | 19/34 | 23/34 | $0.0006 |
| `typesafe/jev-1.14` (decision model) | 27/34 | 29/34 | $0.00015 |

## 3. Decision models (not used yet)

A decision model returns a choice with a probability for every option, not generated
text. TypeSafe Jev is one. Classifying a session is exactly a single-choice question,
so it fits:

```json
{
  "model": "jev-latest",
  "state": "<the BuildText output>",
  "questions": {
    "category": {
      "type": "choice",
      "instructions": "<the rules of the system prompt>",
      "criteria": {"infrastructure_ops": "<Fits / Not fits>", "...": "..."}
    }
  }
}
```

Its response: `answers.category.{choice, probabilities, confidence}` and
`usage.{input_tokens, output_tokens}`. `probabilities` gives a calibrated confidence
for free; the chat model's `confidence` is self-reported.

**Why it was not adopted (2026-10-27).** The `smart` model's cost is small: 1,000
sessions a month cost about $4.30. Jev adds a third party that receives every user's
conversation content. Revisit this if volume grows or if we want calibrated
confidence.

**Two ways to reach it:**

| Route | Endpoint | Notes |
|---|---|---|
| Through LiteLLM (preferred) | `POST <gateway>/typesafe/v1/systemone` (pass-through) or the unified `/v1/systemone` and `/v1/decisions` | The proxy holds `TYPESAFE_API_KEY`; callers use a virtual key. Cost tracking and logging work; budget tags only through `metadata.tags` in the body. The blog announcing it names LiteLLM `v1.103.0-rc`, and production runs `v1.102.1` (gitops `apps/litellm/base/helm.yaml`), so it needs an upgrade. |
| OpenRouter directly | `POST https://openrouter.ai/api/alpha/decisions`, model `typesafe/jev-1.14` (`~typesafe/jev-latest` also resolves) | `state` is a list of `{type:"text", text}`. An `alpha` path, outside the gateway: no LiteLLM key, budget or log. This is the route the measurement in section 2 used. |

**To adopt it:** add a second implementation next to `Model` in `model.go` that sends
the request above and maps `answers.category` to a `Label`. Select it with
`ACH_CLASSIFIER_MODEL`. The taxonomy needs no change: `Taxonomy.Categories[].Fits`
and `NotFits` become the `criteria`. Re-run the golden-set comparison before switching.

References:
[LiteLLM blog: TypeSafe Jev](https://docs.litellm.ai/blog/typesafe_jev),
[LiteLLM TypeSafe pass-through](https://docs.litellm.ai/docs/pass_through/typesafe),
[OpenRouter Jev guide](https://openrouter.ai/docs/guides/community/jev).
