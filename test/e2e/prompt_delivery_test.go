//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Prompt delivery e2e — the demo Environment lists the Prompt
// claude-code-system-prompt: hydrate returns it in context.prompts, its
// content route serves a 1-entry tar.gz as application/gzip, and the
// Environment's readiness gate on Prompts is clear.

package e2e

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const demoPromptName = "claude-code-system-prompt"

func TestPromptDelivery(t *testing.T) {
	phase7SuiteGuard(t)
	phase7DemoEnvironmentReady(t)

	base := strings.TrimRight(phase7BaseURL(), "/")
	key := phase7AcquirePk(t).Access

	t.Run("hydrate lists the prompt", func(t *testing.T) {
		body := strings.NewReader(`{"environment":"` + phase7DemoEnvironment + `"}`)
		req, err := http.NewRequest(http.MethodPost, base+"/platform/hydrate", body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-ach-key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var doc struct {
			Context struct {
				Prompts []struct {
					Name        string `json:"name"`
					DownloadURL string `json:"downloadUrl"`
				} `json:"prompts"`
			} `json:"context"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("hydrate: status %d, decode err %v", resp.StatusCode, err)
		}
		found := false
		for _, p := range doc.Context.Prompts {
			if p.Name == demoPromptName {
				found = strings.HasSuffix(p.DownloadURL, "/content/prompt/"+demoPromptName)
			}
		}
		if !found {
			t.Fatalf("context.prompts lacks %s with its download URL: %+v", demoPromptName, doc.Context.Prompts)
		}
	})

	t.Run("content serves a one-entry tar.gz", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, base+"/content/prompt/"+demoPromptName, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("x-ach-key", key)
		req.Header.Set("x-ach-environment", phase7DemoEnvironment)
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
			t.Fatalf("Content-Type %q, want application/gzip", ct)
		}
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("body is not gzip: %v", err)
		}
		tr := tar.NewReader(gz)
		if _, err := tr.Next(); err != nil {
			t.Fatalf("tar entry: %v", err)
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Next(); err != io.EOF {
			t.Fatalf("tar has more than one entry (err %v)", err)
		}
	})

	t.Run("readiness gate is clear", func(t *testing.T) {
		out, err := runCmdLonger(30*time.Second, "kubectl", "get",
			"environment/"+phase7DemoEnvironment, "-n", phase7Namespace,
			"-o", "jsonpath={.status.unresolvedContextPrompts}")
		if err != nil {
			t.Fatalf("kubectl get: %v\n%s", err, out)
		}
		if got := strings.TrimSpace(out); got != "" && got != "[]" {
			t.Fatalf("status.unresolvedContextPrompts = %q, want empty", got)
		}
	})
}
