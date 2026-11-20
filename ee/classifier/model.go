// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package classifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

const (
	reminder       = "Classify the session above. Reply with ONE JSON object and nothing else."
	reminderStrict = `Reply with ONLY a JSON object that starts with { and has keys category and confidence.`
)

// The APIs a Model can call.
const (
	APIChat      = "chat"      // POST /chat/completions, the answer is JSON text
	APIDecisions = "decisions" // POST /decisions, the answer is one of the choices
)

// Model calls the AI gateway: an OpenAI-compatible chat completions endpoint,
// or LiteLLM's decisions endpoint for a decision model.
type Model struct {
	URL, Key, Name string // URL is the base, e.g. https://api.example.com/v1
	API            string // APIChat or APIDecisions
	HC             *http.Client
}

// Label is the classifier's answer for one session.
type Label struct {
	Category   string
	Confidence float64
	Cost       float64 // USD, from x-litellm-response-cost when the gateway sends it
}

var jsonObject = regexp.MustCompile(`(?s)\{.*\}`)

// Classify returns the category for text. A trivial-sized session never gets
// here. A chat model gets one retry with a stricter reminder: a reasoning
// model sometimes answers outside JSON (seen once in 34 sessions).
func (m Model) Classify(ctx context.Context, tax Taxonomy, text string) (Label, error) {
	pasted := "<pasted_message>\n" + text + "\n</pasted_message>"
	if m.API == APIDecisions {
		return m.decide(ctx, tax, pasted)
	}
	var last error
	for _, r := range []string{reminder, reminderStrict} {
		out, cost, err := m.complete(ctx, tax.Prompt(), pasted+"\n\n"+r)
		if err != nil {
			return Label{}, err
		}
		var a struct {
			Category   string  `json:"category"`
			Confidence float64 `json:"confidence"`
		}
		if j := jsonObject.FindString(out); j != "" && json.Unmarshal([]byte(j), &a) == nil {
			if i := tax.Index(a.Category); i >= 0 && a.Category != Trivial {
				return Label{Category: a.Category, Confidence: a.Confidence, Cost: cost}, nil
			}
		}
		last = errors.New("classifier: model answer is not a category")
	}
	return Label{}, last
}

// decide asks one choice question. The confidence is the model's probability
// for the choice it made; a refusal carries no choice and is an error.
func (m Model) decide(ctx context.Context, tax Taxonomy, input string) (Label, error) {
	choices := make([]map[string]string, 0, len(tax.Categories))
	for _, c := range tax.Categories {
		if c.ID != Trivial {
			choices = append(choices, map[string]string{"value": c.ID, "description": c.describe()})
		}
	}
	raw, cost, err := m.post(ctx, "/decisions", map[string]any{
		"model": m.Name, "input": input,
		"questions": []map[string]any{
			{"type": "choice", "name": "category", "instructions": tax.Rules(), "choices": choices},
		},
	})
	if err != nil {
		return Label{}, err
	}
	var r struct {
		Answers []struct {
			Choice     string  `json:"choice"`
			Confidence float64 `json:"confidence"`
		} `json:"answers"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.Answers) != 1 {
		return Label{}, errors.New("classifier: decode model body")
	}
	a := r.Answers[0]
	if tax.Index(a.Choice) < 0 || a.Choice == Trivial {
		return Label{}, errors.New("classifier: model answer is not a category")
	}
	return Label{Category: a.Choice, Confidence: a.Confidence, Cost: cost}, nil
}

func (m Model) complete(ctx context.Context, system, user string) (string, float64, error) {
	raw, cost, err := m.post(ctx, "/chat/completions", map[string]any{
		"model": m.Name, "temperature": 0, "max_tokens": 3000,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": roleUser, "content": user},
		},
	})
	if err != nil {
		return "", 0, err
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.Choices) == 0 {
		return "", 0, errors.New("classifier: decode model body")
	}
	return r.Choices[0].Message.Content, cost, nil
}

// post sends one JSON request to the gateway and returns the body of a 200
// with the cost the gateway reports.
func (m Model) post(ctx context.Context, path string, payload any) ([]byte, float64, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, errors.New("classifier: bad model request")
	}
	req.Header.Set("Authorization", "Bearer "+m.Key)
	req.Header.Set("Content-Type", "application/json")
	hc := m.HC
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, errors.New("classifier: model request failed") // the error embeds the URL
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 0, errors.New("classifier: read model body")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("classifier: model status %d", resp.StatusCode)
	}
	cost, _ := strconv.ParseFloat(resp.Header.Get("x-litellm-response-cost"), 64)
	return raw, cost, nil
}
