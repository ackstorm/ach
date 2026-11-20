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
5. **Classify.** One model call returns the category and a confidence
   (section 2 has the two ways of calling a model).
6. **Write.** It writes a NUMERIC score. The value is the category's index in
   `Taxonomy.Categories`; the metadata holds `category`, `domain`, `taxonomy`, `model` and
   `confidence`. NUMERIC because Langfuse 4.5 in `events_only` mode dropped the text of
   CATEGORICAL and TEXT scores; 4.71 keeps it (measured), and NUMERIC works on both.

Without `ACH_CLASSIFIER_LANGFUSE_URL` it is a dry run: it classifies and logs, and
writes nothing. With it, both Langfuse keys are required. Logs carry ids, labels and
counts, never conversation content.

## 2. Model

`ACH_CLASSIFIER_MODEL_API` (chart `classifier.modelApi`) says how
`ACH_CLASSIFIER_MODEL` is called. Both go to `ACH_CLASSIFIER_MODEL_URL` with the
same key, and both read the cost from `x-litellm-response-cost` (`model.go`).

| `modelApi` | Request | Answer | Confidence |
|---|---|---|---|
| `chat` (default) | `POST /chat/completions`: the prompt as system message, the session as user message | JSON text `{"category", "confidence"}`; one retry with a stricter reminder when it is not JSON | Self-reported by the model |
| `decisions` | `POST /decisions`: one `choice` question | `answers[0].choice` | `answers[0].confidence`, the model's probability for the choice |

A decision model returns a choice with a probability for every option, not
generated text. The request is LiteLLM's Decisions format; the route exists
from LiteLLM 1.104.2 and `gemini/gemini-decision-1` from 1.111.0-dev.1:

```json
{
  "model": "<ACH_CLASSIFIER_MODEL>",
  "input": "<pasted_message>\n<the BuildText output>\n</pasted_message>",
  "questions": [{
    "type": "choice",
    "name": "category",
    "instructions": "<Taxonomy.Rules(): the prompt up to the category list>",
    "choices": [{"value": "technology.infrastructure_ops", "description": "<fits> Does not fit: <not fits>"}]
  }]
}
```

The choices are every category but `trivial`, with the same description line
the chat prompt lists. The taxonomy and its version are the same for both APIs:
the score's `model` metadata says which model labelled a session. A refusal
(`"type": "refusal"`, no `choice`) or an unknown value fails the session, which
the job retries on the next pass. LiteLLM also serves `/systemone`, a different
body format over the same models; the classifier does not use it.

Measured:

| Model | `modelApi` | Same category as `smart` | Same domain | Cost per session |
|---|---|---|---|---|
| `ackstorm.smart` (gemini-3.8-flash) | `chat` | reference | reference | $0.0043 |
| `ackstorm.lite` (gpt-oss-120b) | `chat` | 19/34 | 23/34 | $0.0006 |
| `typesafe/jev-1.14` (through OpenRouter) | n/a | 27/34 | 29/34 | $0.00015 |
| `gemini/gemini-decision-1` | `decisions` | not measured | not measured | $0.10 per 1M input tokens, no output charge |

The first three rows: 2026-10-27, 34 sessions mixed by client and user. There
are no human labels, so the figures are agreement with `smart`, not accuracy.
`gemini-decision-1`: 2026-11-20, the built-in taxonomy is 1,100 tokens of every
request ($0.00011) and a session capped at 24,000 characters adds its own.

Where the session text goes: `ackstorm.smart` is Vertex AI in the EU;
`gemini-decision-1` is served by the Gemini Developer API only (no Vertex AI
route); Jev is TypeSafe, a third party.

References:
[LiteLLM Decisions](https://docs.litellm.ai/docs/decisions),
[Google Decisions API](https://aistudio.google.com/docs/decisions-api).
