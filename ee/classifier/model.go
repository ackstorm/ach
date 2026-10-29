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

// Model calls an OpenAI-compatible chat completions endpoint (the AI gateway).
type Model struct {
	URL, Key, Name string // URL is the base, e.g. https://api.example.com/v1
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
// here. One retry with a stricter reminder: a reasoning model sometimes
// answers outside JSON (seen once in 34 sessions).
func (m Model) Classify(ctx context.Context, tax Taxonomy, text string) (Label, error) {
	var last error
	for _, r := range []string{reminder, reminderStrict} {
		out, cost, err := m.complete(ctx, tax.Prompt(), "<pasted_message>\n"+text+"\n</pasted_message>\n\n"+r)
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

func (m Model) complete(ctx context.Context, system, user string) (string, float64, error) {
	body, _ := json.Marshal(map[string]any{
		"model": m.Name, "temperature": 0, "max_tokens": 3000,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": roleUser, "content": user},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", 0, errors.New("classifier: bad model request")
	}
	req.Header.Set("Authorization", "Bearer "+m.Key)
	req.Header.Set("Content-Type", "application/json")
	hc := m.HC
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", 0, errors.New("classifier: model request failed") // the error embeds the URL
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, errors.New("classifier: read model body")
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("classifier: model status %d", resp.StatusCode)
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
	cost, _ := strconv.ParseFloat(resp.Header.Get("x-litellm-response-cost"), 64)
	return r.Choices[0].Message.Content, cost, nil
}
