// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/keycrypt"
	"github.com/ackstorm/ach/internal/litellm"
)

// TestPkScopeMeasure — unified-console spec D-25 / §10.2. NOT part of the
// suite: runs only with ACH_MEASURE=1 (make e2e-focus RUN=TestPkScopeMeasure).
// It answers "does a user's OWN LiteLLM key return only that user's catalog,
// spend and budget data?" for every read the console needs. A refusal is a
// finding (logged); returning ANOTHER user's rows is a failure (leak).
//
// User A is the real Dex mock user: its purpose='oauth' pk_ row is minted by
// /token and its sk- is decrypted from the ACH DB with the dev DEK — exactly
// the server-side path the console will use. User B is a LiteLLM user + key
// in a deny-all user shell team, minted with the master key: LiteLLM cannot
// tell it from a pk_.
func TestPkScopeMeasure(t *testing.T) {
	if os.Getenv("ACH_MEASURE") == "" {
		t.Skip("measurement only: set ACH_MEASURE=1")
	}
	const (
		userA  = "kilgore@kilgore.trout"
		userB  = "measure-b@example.test"
		devDEK = "YWNoLWRldi1kZWstZG8tbm90LXVzZS1pbi1wcm9kISE=" // 02-ach/secrets dev DEK
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	gateway := "http://" + phase4GatewayAuthority(t)

	llURL := fmt.Sprintf("http://127.0.0.1:%d", startPortForward(t, sc5LiteLLMNS, sc5LiteLLMSvc, 4000))
	achDB := fmt.Sprintf("postgres://ach:ach@127.0.0.1:%d/ach?sslmode=disable", startPortForward(t, namespace, sc5ACHPgSvc, 5432))
	admin := litellm.NewRESTClient(llURL, sc5MasterKey, logr.Discard())

	// --- user A: real OAuth login → oauth pk_ row → decrypt its sk-.
	jwtA := oauthLogin(t, gateway, "").Access
	pool, err := pgxpool.New(ctx, achDB)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rowA, err := db.ActiveOAuthPK(ctx, pool, userA)
	if err != nil || rowA == nil || rowA.LiteLLMKeyMaterial == nil {
		t.Fatalf("oauth pk_ row for %s: %v %+v", userA, err, rowA)
	}
	dek, _ := base64.StdEncoding.DecodeString(devDEK)
	skABytes, err := keycrypt.Open(dek, *rowA.LiteLLMKeyMaterial)
	if err != nil {
		t.Fatalf("decrypt A material: %v", err)
	}
	skA := string(skABytes)
	ekA := mustAcquireEkBoundToEnv(t, "demo")

	// --- user B: shaped like a pk_ (deny-all user shell team), minted directly.
	teamBID := litellm.UserShellAlias(userB)
	if teamB, err := admin.CreateTeam(ctx, litellm.NewUserShellRequest(userB)); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("shell team B: %v", err)
		}
	} else if teamB.TeamID != "" {
		teamBID = teamB.TeamID
	}
	keyB, err := admin.KeyGenerate(ctx, &litellm.KeyGenerateRequest{UserID: userB, TeamID: teamBID, Duration: "1h",
		Metadata: map[string]string{"created_by": "pk-scope-measure"}})
	if err != nil {
		t.Fatalf("key B: %v", err)
	}
	t.Cleanup(func() {
		_ = admin.RevokeKey(context.Background(), keyB.Token)
		_ = admin.DeleteTeam(context.Background(), teamBID)
	})
	skB := keyB.Key

	// --- traffic so the spend tables have rows for both users (+ EK rows for A).
	chat := func(base, header, cred string) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions",
			strings.NewReader(`{"model":"demo-model","messages":[{"role":"user","content":"measure"}]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(header, cred)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("chat via %s: %v", base, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	t.Logf("traffic A/pk via forwarder: %d", chat(gateway, "Authorization", "Bearer "+jwtA))
	t.Logf("traffic A/ek via forwarder: %d", chat(gateway, "x-ach-key", ekA))
	t.Logf("traffic B/sk direct: %d", chat(llURL, "Authorization", "Bearer "+skB))
	time.Sleep(15 * time.Second) // LiteLLM writes spend logs asynchronously

	// LiteLLM's end_date is exclusive on /spend/logs/v2 (midnight), so the
	// window ends tomorrow to include today's rows.
	tomorrow := time.Now().UTC().Add(24 * time.Hour).Format("2006-01-02")
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02")
	get := func(cred, path string) (int, []byte) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, llURL+path, nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return resp.StatusCode, b
	}
	// usersIn collects every user-identifying string value in a JSON body, any depth.
	usersIn := func(b []byte) map[string]int {
		var v any
		_ = json.Unmarshal(b, &v)
		out := map[string]int{}
		var walk func(any)
		walk = func(x any) {
			switch n := x.(type) {
			case map[string]any:
				for k, val := range n {
					if s, ok := val.(string); ok && (k == "user_id" || k == "user" || k == "end_user" || k == "user_email") {
						out[s]++
					}
					walk(val)
				}
			case []any:
				for _, e := range n {
					walk(e)
				}
			}
		}
		walk(v)
		return out
	}
	probe := func(name, cred, path, self, other string) {
		t.Run(name, func(t *testing.T) {
			code, body := get(cred, path)
			seen := usersIn(body)
			t.Logf("%s → %d, bytes=%d, users=%v, body=%s", path, code, len(body), seen, truncateBody(body, 400))
			if code >= 300 {
				t.Logf("FINDING: refused (%d)", code)
				return
			}
			if other != "" && seen[other] > 0 {
				t.Errorf("LEAK: %s returned %d row(s) of %s to %s", path, seen[other], other, self)
			}
		})
	}
	q := url.QueryEscape
	dates := "start_date=" + yesterday + "&end_date=" + tomorrow
	// --- A's own key, own scope.
	probe("A/daily_activity_self", skA, "/user/daily/activity?"+dates+"&page=1&page_size=50", userA, userB)
	probe("A/spend_logs_v2_self", skA, "/spend/logs/v2?user_id="+q(userA)+"&"+dates+"&page=1&page_size=50", userA, userB)
	probe("A/spend_logs_v2_no_user_param", skA, "/spend/logs/v2?"+dates+"&page=1&page_size=50", userA, userB)
	probe("A/user_info_self", skA, "/user/info", userA, userB)
	probe("A/user_info_by_id", skA, "/user/info?user_id="+q(userA), userA, userB)
	probe("A/key_info_self", skA, "/key/info", userA, userB)
	probe("A/key_list_self", skA, "/key/list?user_id="+q(userA)+"&return_full_object=true", userA, userB)
	probe("A/team_info_own_shell", skA, "/team/info?team_id="+q(litellm.UserShellAlias(userA)), userA, userB)
	probe("A/models", skA, "/v1/models", userA, userB)
	probe("A/model_group_info", skA, "/model_group/info", userA, userB)
	probe("A/mcp_servers", skA, "/v1/mcp/server", userA, userB)
	probe("A/a2a_agents", skA, "/v1/agents?health_check=false", userA, userB)
	// --- A's key asking for B (must refuse or return nothing of B).
	probe("A/daily_activity_as_B", skA, "/user/daily/activity?user_id="+q(userB)+"&"+dates, userA, userB)
	probe("A/spend_logs_v2_as_B", skA, "/spend/logs/v2?user_id="+q(userB)+"&"+dates, userA, userB)
	probe("A/user_info_as_B", skA, "/user/info?user_id="+q(userB), userA, userB)
	probe("A/key_list_as_B", skA, "/key/list?user_id="+q(userB)+"&return_full_object=true", userA, userB)
	probe("A/team_info_B_shell", skA, "/team/info?team_id="+q(teamBID), userA, userB)
	// --- symmetric: B's key asking for A.
	probe("B/daily_activity_as_A", skB, "/user/daily/activity?user_id="+q(userA)+"&"+dates, userB, userA)
	probe("B/spend_logs_v2_as_A", skB, "/spend/logs/v2?user_id="+q(userA)+"&"+dates, userB, userA)
	probe("B/spend_logs_v2_no_user_param", skB, "/spend/logs/v2?"+dates+"&page=1&page_size=50", userB, userA)
	// --- does A's global view include the EK traffic? (AC-16)
	t.Run("A/ek_traffic_included", func(t *testing.T) {
		code, body := get(skA, "/user/daily/activity?"+dates)
		if code >= 300 {
			t.Logf("FINDING: refused (%d)", code)
			return
		}
		var v struct {
			Results []struct {
				Breakdown struct {
					APIKeys map[string]any `json:"api_keys"`
				} `json:"breakdown"`
			} `json:"results"`
		}
		_ = json.Unmarshal(body, &v)
		keys := 0
		for _, r := range v.Results {
			keys += len(r.Breakdown.APIKeys)
		}
		t.Logf("distinct api_keys in A's daily activity: %d (want ≥2: the oauth pk_ and the ek_)", keys)
	})
}

func truncateBody(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
