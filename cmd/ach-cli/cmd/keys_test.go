// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
)

// keysReq is one request the fake Hub saw.
type keysReq struct {
	Method, Path, Query, Body string
}

// keysTestServer is a fake Hub for `keys *`: it records every request and
// answers from the configurable fields.
type keysTestServer struct {
	*httptest.Server
	mu         sync.Mutex
	reqs       []keysReq
	envs       []string         // GET /platform/environments names
	listItems  []map[string]any // GET /platform/keys items
	status     int              // status for POST/DELETE/PATCH on /platform/keys/…; 0 → 204
	errCode    string           // error code when status >= 400
	createResp map[string]any   // POST /platform/keys body; nil → a full key
}

func newKeysTestServer(t *testing.T) *keysTestServer {
	t.Helper()
	srv := &keysTestServer{}
	srv.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		srv.mu.Lock()
		srv.reqs = append(srv.reqs, keysReq{r.Method, r.URL.Path, r.URL.RawQuery, string(b)})
		srv.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/platform/environments":
			items := []map[string]any{}
			for _, n := range srv.envs {
				items = append(items, map[string]any{"name": n})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case r.URL.Path == "/platform/keys" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"items": srv.listItems})
		case r.URL.Path == "/platform/keys" && r.Method == http.MethodPost:
			if srv.createResp != nil {
				_ = json.NewEncoder(w).Encode(srv.createResp)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"key_id": "ekid_new", "plaintext": testEKSaved})
		default:
			if srv.status >= 400 {
				w.WriteHeader(srv.status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{"code": srv.errCode, "message": "nope"}, "request_id": "req_t",
				})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	swapHTTPClientForTest(t, &keysHTTPClient, srv.Client())
	return srv
}

// writes returns the non-GET requests.
func (s *keysTestServer) writes() []keysReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []keysReq
	for _, r := range s.reqs {
		if r.Method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

func (s *keysTestServer) lastGet(path string) keysReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.reqs) - 1; i >= 0; i-- {
		if s.reqs[i].Method == http.MethodGet && s.reqs[i].Path == path {
			return s.reqs[i]
		}
	}
	return keysReq{}
}

// seedKeysProfile writes profile "p" (a session) with the given saved keys.
func seedKeysProfile(t *testing.T, url string, saved map[string]config.SavedKey) string {
	t.Helper()
	p := oauthProfile(url)
	p.Keys = saved
	return seedProfile(t, "p", p)
}

func loadProfileP(t *testing.T, path string) *config.Profile {
	t.Helper()
	f, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return f.Profiles["p"]
}

func executeKeys(t *testing.T, stdin string, args ...string) (string, string, exit.Code, error) {
	t.Helper()
	cmd := newKeysCmd()
	cmd.SetIn(strings.NewReader(stdin))
	return executeCommand(t, cmd, args...)
}

// newRootCmdForTest is a bare root for tests that resolve commands through it.
func newRootCmdForTest() *cobra.Command {
	root := &cobra.Command{Use: "ach-cli", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newKeysCmd())
	return root
}

func TestKeysCreate_SavesByNameAndPrintsOnce(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	path := seedKeysProfile(t, srv.URL, nil)

	stdout, _, code, err := executeKeys(t, "", "create", "demo")
	if err != nil || code != exit.OK {
		t.Fatalf("code=%d err=%v", code, err)
	}
	w := srv.writes()
	if len(w) != 1 || w[0].Method != http.MethodPost || w[0].Path != "/platform/keys" {
		t.Fatalf("writes = %+v", w)
	}
	if got := strings.TrimSpace(w[0].Body); got != `{"environment":"demo","name":"demo"}` {
		t.Errorf("body = %s", got)
	}
	if stdout != testEKSaved+"\n" {
		t.Errorf("stdout = %q; want the plaintext exactly once", stdout)
	}
	if got := loadProfileP(t, path).Keys["demo"]; got != (config.SavedKey{ID: "ekid_new", Key: testEKSaved}) {
		t.Errorf("saved = %+v", got)
	}
}

// A 2xx without key_id/plaintext (e.g. a redirected POST answered by the
// list endpoint) must fail loudly: nothing printed, nothing saved.
func TestKeysCreate_IncompleteResponseSavesNothing(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	srv.createResp = map[string]any{}
	path := seedKeysProfile(t, srv.URL, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	cmd := newKeysCmd()
	cmd.SilenceUsage = true // as the real root does; keep stdout to what RunE writes
	stdout, _, code, err := executeCommand(t, cmd, "create", "demo")
	if code != exit.General || err == nil || !strings.Contains(err.Error(), "incomplete response") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q; want empty", stdout)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("config file rewritten:\n%s", after)
	}
}

func TestKeysCreate_RefusesSavedName(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	seedKeysProfile(t, srv.URL, map[string]config.SavedKey{"demo": {ID: "ekid_old", Key: testEK}})

	_, _, code, err := executeKeys(t, "", "create", "demo")
	if code != exit.General || err == nil ||
		!strings.Contains(err.Error(), `a key named "demo" is already saved in profile "p"; pick --name or revoke it`) {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(srv.writes()) != 0 {
		t.Errorf("no POST may be sent: %+v", srv.writes())
	}
}

func TestKeysCreate_ExpiresAndBudget(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	seedKeysProfile(t, srv.URL, nil)

	if _, _, _, err := executeKeys(t, "", "create", "demo", "--expires", "90d",
		"--max-budget", "20", "--budget-duration", "30d", "--no-save"); err != nil {
		t.Fatal(err)
	}
	var body struct {
		ExpiresAt string `json:"expires_at"`
		Budget    struct {
			MaxBudget      float64 `json:"max_budget"`
			BudgetDuration string  `json:"budget_duration"`
		} `json:"budget"`
	}
	if err := json.Unmarshal([]byte(srv.writes()[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	exp, err := time.Parse(time.RFC3339, body.ExpiresAt)
	if err != nil || exp.Sub(time.Now().AddDate(0, 0, 90)).Abs() > time.Minute {
		t.Errorf("expires_at = %q; want ≈ now+90d", body.ExpiresAt)
	}
	if body.Budget.MaxBudget != 20 || body.Budget.BudgetDuration != "30d" {
		t.Errorf("budget = %+v", body.Budget)
	}
}

func TestParseExpires(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"90d":                  now.AddDate(0, 0, 90),
		"720h":                 now.Add(720 * time.Hour),
		"2026-12-31T00:00:00Z": time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	} {
		if got, err := parseExpires(in, now); err != nil || !got.Equal(want) {
			t.Errorf("%s → %v %v; want %v", in, got, err, want)
		}
	}
	if _, err := parseExpires("soon", now); err == nil {
		t.Error("garbage must be rejected")
	}
}

func TestKeysCreate_BudgetDurationNeedsMaxBudget(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	seedKeysProfile(t, srv.URL, nil)
	_, _, code, err := executeKeys(t, "", "create", "demo", "--budget-duration", "30d")
	if code != exit.General || err == nil || !strings.Contains(err.Error(), "--max-budget") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(srv.reqs) != 0 {
		t.Errorf("no request may be sent: %+v", srv.reqs)
	}
}

func TestKeysCreate_MissingOrUnknownEnvListsYours(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	srv.envs = []string{"frontend-dev", "platform"}
	seedKeysProfile(t, srv.URL, nil)

	for _, args := range [][]string{{"create"}, {"create", "ghost"}} {
		_, _, code, err := executeKeys(t, "", args...)
		if code != exit.General || err == nil || !strings.Contains(err.Error(), "frontend-dev, platform") {
			t.Errorf("%v: code=%d err=%v", args, code, err)
		}
	}
	if len(srv.writes()) != 0 {
		t.Errorf("no POST may be sent: %+v", srv.writes())
	}
}

func TestKeysCreate_EnvironmentFlagRemoved(t *testing.T) {
	credTestEnv(t)
	_, _, _, err := executeKeys(t, "", "create", "--environment", "demo")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err = %v; want unknown flag", err)
	}
}

func TestKeysList_QueryStatusAndColumns(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	srv.listItems = []map[string]any{{"key_id": "ekid_1", "type": "ek", "environment": "demo", "name": "laptop",
		"status": "active", "created_at": "2026-01-01T00:00:00Z"}}
	seedKeysProfile(t, srv.URL, nil)

	out, _, _, err := executeKeys(t, "", "list")
	if err != nil {
		t.Fatal(err)
	}
	if q := srv.lastGet("/platform/keys").Query; q != "status=active&type=ek" {
		t.Errorf("query = %q", q)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if strings.Join(strings.Fields(lines[0]), " ") != "NAME ENVIRONMENT STATUS EXPIRES ID" ||
		strings.Join(strings.Fields(lines[1]), " ") != "laptop demo active never ekid_1" {
		t.Errorf("table:\n%s", out)
	}

	if _, _, _, err := executeKeys(t, "", "list", "--status", "invalid", "--env", "demo"); err != nil {
		t.Fatal(err)
	}
	if q := srv.lastGet("/platform/keys").Query; q != "environment=demo&status=invalid&type=ek" {
		t.Errorf("query = %q", q)
	}
	if _, _, _, err := executeKeys(t, "", "list", "--status", "all"); err != nil {
		t.Fatal(err)
	}
	if q := srv.lastGet("/platform/keys").Query; q != "type=ek" {
		t.Errorf("all: query = %q", q)
	}

	out, _, _, err = executeKeys(t, "", "list", "-o", "json")
	if err != nil || !strings.Contains(out, `"key_id": "ekid_1"`) {
		t.Errorf("json: %v\n%s", err, out)
	}
}

func TestKeysList_BogusStatus(t *testing.T) {
	credTestEnv(t)
	_, _, code, err := executeKeys(t, "", "list", "--status", "bogus")
	if code != exit.General || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	for _, s := range []string{"active", "suspended", "expired", "invalid", "revoked", "all"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error must list %q: %v", s, err)
		}
	}
}

func TestKeysRevoke_SavedName(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	path := seedKeysProfile(t, srv.URL, map[string]config.SavedKey{
		"laptop": {ID: "ekid_lap", Key: testEK}, "ci": {ID: "ekid_ci", Key: testEKSaved},
	})

	out, _, _, err := executeKeys(t, "y\n", "revoke", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if w := srv.writes(); len(w) != 1 || w[0].Method != http.MethodDelete || w[0].Path != "/platform/keys/ekid_lap" {
		t.Fatalf("writes = %+v", w)
	}
	keys := loadProfileP(t, path).Keys
	if _, ok := keys["laptop"]; ok || keys["ci"].ID != "ekid_ci" {
		t.Errorf("saved keys after revoke = %+v", keys)
	}
	if !strings.Contains(out, "Revoked laptop (ekid_lap)") {
		t.Errorf("stdout = %q", out)
	}
}

func TestKeysRevoke_IDRemovesSavedCopy(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	path := seedKeysProfile(t, srv.URL, map[string]config.SavedKey{"laptop": {ID: "ekid_x", Key: testEK}})

	if _, _, _, err := executeKeys(t, "", "revoke", "ekid_x", "--yes"); err != nil {
		t.Fatal(err)
	}
	if w := srv.writes(); len(w) != 1 || w[0].Path != "/platform/keys/ekid_x" {
		t.Fatalf("writes = %+v", w)
	}
	if len(loadProfileP(t, path).Keys) != 0 {
		t.Error("the saved entry holding that id must be removed")
	}
}

func TestKeysRevoke_ServerNameLookup(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	seedKeysProfile(t, srv.URL, nil)
	row := func(id, status string) map[string]any {
		return map[string]any{"key_id": id, "name": "ci", "status": status}
	}

	srv.listItems = []map[string]any{row("ekid_old", "revoked"), row("ekid_live", "active")}
	if _, _, _, err := executeKeys(t, "", "revoke", "ci", "--yes"); err != nil {
		t.Fatal(err)
	}
	if q := srv.lastGet("/platform/keys").Query; q != "type=ek" {
		t.Errorf("lookup query = %q; want type=ek (status all)", q)
	}
	if w := srv.writes(); len(w) != 1 || w[0].Path != "/platform/keys/ekid_live" {
		t.Fatalf("writes = %+v", w)
	}

	srv.listItems = nil
	_, _, code, err := executeKeys(t, "", "revoke", "ci", "--yes")
	if code != exit.General || err == nil || !strings.Contains(err.Error(), `no key named "ci"`) {
		t.Fatalf("zero: code=%d err=%v", code, err)
	}

	srv.listItems = []map[string]any{row("ekid_a", "active"), row("ekid_b", "suspended")}
	_, _, code, err = executeKeys(t, "", "revoke", "ci", "--yes")
	if code != exit.General || err == nil ||
		!strings.Contains(err.Error(), "ekid_a") || !strings.Contains(err.Error(), "ekid_b") {
		t.Fatalf("many: code=%d err=%v", code, err)
	}
	if len(srv.writes()) != 1 {
		t.Errorf("ambiguous/absent names must not revoke: %+v", srv.writes())
	}
}

func TestKeysRevoke_ConfirmNoCancels(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	seedKeysProfile(t, srv.URL, nil)
	if _, _, code, err := executeKeys(t, "n\n", "revoke", "ekid_x"); err == nil || code != exit.General {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(srv.writes()) != 0 {
		t.Error("declined confirmation must not revoke")
	}
}

func TestKeysRevoke_RejectsPlaintextAndFriendly404(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	seedKeysProfile(t, srv.URL, nil)
	_, _, _, err := executeKeys(t, "", "revoke", testEK, "--yes")
	if err == nil || !strings.Contains(err.Error(), "not the key itself") {
		t.Fatalf("plaintext: err=%v", err)
	}
	srv.status, srv.errCode = 404, "not_found"
	_, _, code, err := executeKeys(t, "", "revoke", "ekid_missing", "--yes")
	if code != exit.General || err == nil || !strings.Contains(err.Error(), "not found, or not owned by you") {
		t.Fatalf("404: code=%d err=%v", code, err)
	}
}

func TestKeysSuspendResume(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	seedKeysProfile(t, srv.URL, map[string]config.SavedKey{"laptop": {ID: "ekid_lap", Key: testEK}})

	for _, verb := range []string{"suspend", "resume"} {
		if _, _, _, err := executeKeys(t, "", verb, "laptop"); err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
	}
	w := srv.writes()
	if len(w) != 2 || w[0].Method != http.MethodPost || w[0].Path != "/platform/keys/ekid_lap/suspend" ||
		w[1].Path != "/platform/keys/ekid_lap/resume" {
		t.Fatalf("writes = %+v", w)
	}

	srv.status, srv.errCode = 409, "key_revoked"
	_, _, code, err := executeKeys(t, "", "resume", "laptop")
	if code != exit.General || err == nil || !strings.Contains(err.Error(), "key is revoked") {
		t.Fatalf("409: code=%d err=%v", code, err)
	}
}

func TestKeysBudget(t *testing.T) {
	credTestEnv(t)
	srv := newKeysTestServer(t)
	seedKeysProfile(t, srv.URL, map[string]config.SavedKey{"laptop": {ID: "ekid_lap", Key: testEK}})

	if _, _, _, err := executeKeys(t, "", "budget", "laptop", "--max-budget", "20"); err != nil {
		t.Fatal(err)
	}
	w := srv.writes()
	if len(w) != 1 || w[0].Method != http.MethodPatch || w[0].Path != "/platform/keys/ekid_lap/budget" ||
		strings.TrimSpace(w[0].Body) != `{"max_budget":20}` {
		t.Fatalf("writes = %+v", w)
	}
	var budget *cobra.Command
	for _, c := range newKeysCmd().Commands() {
		if c.Name() == "budget" {
			budget = c
		}
	}
	if budget == nil || !strings.Contains(budget.Long, "your own total budget still applies") {
		t.Error("budget help must say the user total still applies")
	}
}

func TestKeysPruneRemoved(t *testing.T) {
	credTestEnv(t)
	_, _, _, err := executeCommand(t, newRootCmdForTest(), "keys", "prune")
	if err == nil {
		t.Fatal("keys prune must be an unknown command")
	}
}
