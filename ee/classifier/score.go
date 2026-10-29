// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package classifier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// ScoreName is the Langfuse score carrying the label.
const ScoreName = "ach_classification_category"

// Langfuse writes the label as one NUMERIC score per session. Langfuse 4.5
// (events_only) stores a CATEGORICAL or TEXT score with value 0 and drops its
// text; a NUMERIC score keeps value and metadata (measured). 4.71 keeps a
// CATEGORICAL score's text, but NUMERIC stays: it works on both. value is the
// category index; metadata carries the readable id.
type Langfuse struct {
	URL, PublicKey, SecretKey string // URL is the host, e.g. https://langfuse.example.com
	HC                        *http.Client
}

// ScoreID is deterministic per session and taxonomy: a re-run overwrites.
func ScoreID(taxonomy, sessionID string) string {
	h := sha256.Sum256([]byte(ScoreName + "/" + taxonomy + "/" + sessionID))
	return hex.EncodeToString(h[:16])
}

func (l Langfuse) Write(ctx context.Context, tax Taxonomy, sessionID string, lb Label, model string) error {
	i := tax.Index(lb.Category)
	if i < 0 {
		return errors.New("classifier: unknown category")
	}
	body, _ := json.Marshal(map[string]any{
		"id": ScoreID(tax.Version, sessionID), "sessionId": sessionID, "name": ScoreName,
		"dataType": "NUMERIC", "value": i,
		"metadata": map[string]string{
			"category": lb.Category, "domain": Domain(lb.Category), "taxonomy": tax.Version,
			"model": model, "confidence": strconv.FormatFloat(lb.Confidence, 'f', 2, 64),
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.URL+"/api/public/scores", bytes.NewReader(body))
	if err != nil {
		return errors.New("classifier: bad score request")
	}
	req.SetBasicAuth(l.PublicKey, l.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	hc := l.HC
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New("classifier: score request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("classifier: score status %d", resp.StatusCode)
	}
	return nil
}
