// SPDX-License-Identifier: Apache-2.0

package envkeys

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	authnv1 "k8s.io/api/authentication/v1"

	"github.com/ackstorm/ach/internal/audit"
	"github.com/ackstorm/ach/internal/db"
	"github.com/ackstorm/ach/internal/forwarder/jwt"
	"github.com/ackstorm/ach/internal/keystore"
	achmetrics "github.com/ackstorm/ach/internal/metrics"
	"github.com/ackstorm/ach/internal/platformapi/middleware"
)

const (
	exIssuer    = "https://ach.test/"
	exAudience  = "https://ach.test"
	exIdentity  = "ai.run@ackstorm.com"
	exRequester = "agents/agent-k8s-doctor"
	exUser      = "system:serviceaccount:agents:agent-k8s-doctor"
	okBody      = `{"identity":"AI.Run@ackstorm.com","environment":"prod"}`
)

// saToken is a compact JWS whose payload names aud: the guard reads it
// unverified, the faked TokenReview decides the rest.
func saToken(aud string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(`{"aud":["` + aud + `"],"sub":"` + exUser + `"}`))
	return "eyJhbGciOiJSUzI1NiJ9." + p + ".c2ln"
}

type fakeExchangeDB struct {
	grants   []db.KeyGrantRow
	existing *db.EkKeyInfo
	winner   *db.EkKeyInfo // answered once an insert race has been lost
	finds    int
	owner    string // owner the last FindExchangeKey received
}

func (f *fakeExchangeDB) ListKeyGrantsForIdentity(_ context.Context, _ string, identity string) ([]db.KeyGrantRow, error) {
	var out []db.KeyGrantRow
	for _, g := range f.grants {
		if g.Identity == identity {
			out = append(out, g)
		}
	}
	return out, nil
}

func (f *fakeExchangeDB) FindExchangeKey(_ context.Context, owner, _, _ string) (*db.EkKeyInfo, error) {
	f.finds++
	f.owner = owner
	if f.finds > 1 && f.winner != nil {
		return f.winner, nil
	}
	return f.existing, nil
}

func exGrant(name string, envs, sas []string, budget *float64) db.KeyGrantRow {
	return db.KeyGrantRow{Namespace: "ach", Name: name, Identity: exIdentity, Environments: envs, ServiceAccounts: sas, MaxBudget: budget}
}

func reviewAs(username string, podBound bool) TokenReviewFunc {
	return func(_ context.Context, _ string, aud string) (authnv1.TokenReviewStatus, error) {
		st := authnv1.TokenReviewStatus{Authenticated: true, Audiences: []string{aud}, User: authnv1.UserInfo{Username: username}}
		if podBound {
			st.User.Extra = map[string]authnv1.ExtraValue{extraPodName: {"agent-k8s-doctor-0"}, extraPodUID: {"uid-1"}}
		}
		return st, nil
	}
}

type exchangeFixture struct {
	deps         ExchangeDeps
	fdb          *fakeEkDB
	xdb          *fakeExchangeDB
	llm          *captureLiteLLM
	signer       *jwt.Ed25519Signer
	audit        *bytes.Buffer
	review       TokenReviewFunc
	reviews      int
	verify       func(ctx context.Context, token string) error
	lastAudience string
	handler      http.HandlerFunc
}

func newExchangeFixture(t *testing.T) *exchangeFixture {
	t.Helper()
	fdb := &fakeEkDB{}
	deps, llm := newCreateDeps(fdb)
	deps.Issuer = exIssuer
	// 0 with fakeEkDB's zero allowance would refuse any CreateHandler call:
	// the exchange tests pass only because exchange never consults max_keys.
	deps.DefaultMaxKeys = 0
	f := &exchangeFixture{fdb: fdb, llm: llm, audit: &bytes.Buffer{}, review: reviewAs(exUser, true),
		xdb: &fakeExchangeDB{grants: []db.KeyGrantRow{exGrant("ai-run-doctor", []string{"prod"}, []string{exRequester}, nil)}}}
	deps.Audit = slog.New(slog.NewJSONHandler(f.audit, nil))
	f.signer = jwt.NewEd25519Signer()
	if err := jwt.LoadSeed(f.signer, "k1", make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	f.deps = ExchangeDeps{Deps: deps, ExchangeDB: f.xdb, Signer: f.signer, TTL: 15 * time.Minute,
		VerifySA: func(ctx context.Context, tok string) error {
			if f.verify != nil {
				return f.verify(ctx, tok)
			}
			return nil
		},
		TokenReview: func(ctx context.Context, tok, aud string) (authnv1.TokenReviewStatus, error) {
			f.reviews++
			f.lastAudience = aud
			return f.review(ctx, tok, aud)
		}}
	return f
}

func (f *exchangeFixture) do(t *testing.T, bearer, body, remote string) *httptest.ResponseRecorder {
	t.Helper()
	if f.handler == nil {
		f.handler = ExchangeHandler(f.deps)
	}
	req := httptest.NewRequest(http.MethodPost, "/platform/keys/exchange", strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if remote != "" {
		req.RemoteAddr = remote
	}
	req = req.WithContext(middleware.WithRequestID(req.Context(), "req_test"))
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

func decodeExchange(t *testing.T, rec *httptest.ResponseRecorder) ExchangeResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp ExchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestExchange_GuardsNeverCallTokenReview(t *testing.T) {
	f := newExchangeFixture(t)
	for name, bearer := range map[string]string{
		"no bearer":        "",
		"ek_ key":          "ek-" + strings.Repeat("a", 64),
		"foreign audience": saToken("https://elsewhere.example"),
		"ACH OAuth JWT":    saToken("ach"),
		"exchange JWT":     saToken(keystore.ExchangeAudience),
	} {
		rec := f.do(t, bearer, okBody, "")
		if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "invalid_token" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if f.reviews != 0 {
		t.Fatalf("TokenReview called %d times for bearers that cannot be ours", f.reviews)
	}
}

func TestExchange_AudienceIgnoresTrailingSlash(t *testing.T) {
	for _, issuer := range []string{"https://ach.test/", "https://ach.test"} {
		f := newExchangeFixture(t)
		f.deps.Issuer = issuer
		decodeExchange(t, f.do(t, saToken("https://ach.test"), okBody, ""))
		if f.lastAudience != "https://ach.test" {
			t.Fatalf("issuer %q: TokenReview audience %q", issuer, f.lastAudience)
		}
	}
}

type unsyncedEnvStore struct{ fakeEnvStore }

func (*unsyncedEnvStore) AccessGroupSyncedFromRow(*db.EnvironmentRow) bool { return false }

func TestExchange_ChecksInOrder(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(f *exchangeFixture)
		status int
		code   string
		reason string
	}{
		{"unbound token", func(f *exchangeFixture) { f.review = reviewAs(exUser, false) },
			http.StatusUnauthorized, "invalid_token", "not_pod_bound"},
		{"audience not reviewed", func(f *exchangeFixture) {
			ok := reviewAs(exUser, true)
			f.review = func(ctx context.Context, tok, aud string) (authnv1.TokenReviewStatus, error) {
				st, err := ok(ctx, tok, aud)
				st.Audiences = []string{"https://kubernetes.default.svc"}
				return st, err
			}
		}, http.StatusUnauthorized, "invalid_token", "audience"},
		{"not a ServiceAccount", func(f *exchangeFixture) { f.review = reviewAs("jane@ackstorm.com", true) },
			http.StatusUnauthorized, "invalid_token", "not_service_account"},
		{"unauthenticated", func(f *exchangeFixture) {
			f.review = func(context.Context, string, string) (authnv1.TokenReviewStatus, error) {
				return authnv1.TokenReviewStatus{}, nil
			}
		}, http.StatusUnauthorized, "invalid_token", "unauthenticated"},
		{"no grant", func(f *exchangeFixture) { f.xdb.grants = nil },
			http.StatusForbidden, "not_granted", "no_grant_for_identity"},
		{"grant names the Environment but not the requester", func(f *exchangeFixture) {
			f.xdb.grants = []db.KeyGrantRow{exGrant("g", []string{"prod"}, []string{"agents/other"}, nil)}
		}, http.StatusForbidden, "not_granted", "requester_not_granted"},
		{"lists only match when two grants are mixed", func(f *exchangeFixture) {
			f.xdb.grants = []db.KeyGrantRow{
				exGrant("a", []string{"prod"}, []string{"agents/other"}, nil),
				exGrant("b", []string{"dev"}, []string{exRequester}, nil),
			}
		}, http.StatusForbidden, "not_granted", "requester_not_granted"},
		{"Environment not synced", func(f *exchangeFixture) { f.deps.Store = &unsyncedEnvStore{fakeEnvStore{env: prodEnv()}} },
			http.StatusServiceUnavailable, "not_ready", ""},
		{"Environment missing", func(f *exchangeFixture) { f.deps.Store = &fakeEnvStore{} },
			http.StatusNotFound, "environment_not_found", ""},
		{"team mismatch", func(f *exchangeFixture) {
			f.deps.Store = &fakeEnvStore{env: &db.EnvironmentRow{Namespace: "ach", Name: "prod", AuthorizedTeams: []string{"finance"}}}
		}, http.StatusForbidden, "unauthorized_team", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newExchangeFixture(t)
			tc.setup(f)
			rec := f.do(t, saToken(exAudience), okBody, "")
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body, tc.status, tc.code)
			}
			if f.fdb.inserted != nil {
				t.Fatal("a refused exchange minted a key")
			}
			if tc.reason != "" && !strings.Contains(f.audit.String(), `"exchange.reason":"`+tc.reason+`"`) {
				t.Fatalf("audit lacks reason %q: %s", tc.reason, f.audit)
			}
		})
	}
}

func TestExchange_FirstMintsThenReuses(t *testing.T) {
	f := newExchangeFixture(t)
	resp := decodeExchange(t, f.do(t, saToken(exAudience), okBody, ""))
	if !strings.HasPrefix(resp.KeyID, "ekid_") || resp.ExpiresIn != 900 {
		t.Fatalf("%+v", resp)
	}
	if f.xdb.owner != exIdentity {
		t.Fatalf("FindExchangeKey owner %q, want the lower-cased identity", f.xdb.owner)
	}
	ins := f.fdb.inserted
	if ins == nil || ins.IssuedVia != "exchange" || ins.Requester == nil || *ins.Requester != exRequester ||
		ins.OwnerEmail != exIdentity || ins.Name != "exchange:"+exRequester || ins.KeyID != resp.KeyID {
		t.Fatalf("inserted %+v", ins)
	}
	md := f.llm.lastKeyGenerateReq.Metadata
	if md["ach_issuer"] != exIssuer || md["ach_issued_via"] != "exchange" || md["ach_requester"] != exRequester {
		t.Fatalf("LiteLLM metadata %v", md)
	}
	sub, exp, err := f.signer.VerifyExp(resp.Token, exAudience, keystore.ExchangeAudience)
	if err != nil || sub != resp.KeyID {
		t.Fatalf("JWT sub=%q err=%v", sub, err)
	}
	if d := time.Until(exp); d < 14*time.Minute || d > 15*time.Minute {
		t.Fatalf("exp in %v", d)
	}

	f.xdb.existing = &db.EkKeyInfo{KeyID: resp.KeyID, Status: "active"}
	f.fdb.inserted = nil
	again := decodeExchange(t, f.do(t, saToken(exAudience), okBody, ""))
	if again.KeyID != resp.KeyID || f.fdb.inserted != nil || again.Token == resp.Token {
		t.Fatalf("second exchange must reuse %s with a new JWT: %+v, inserted %+v", resp.KeyID, again, f.fdb.inserted)
	}
}

func TestExchange_BudgetIsLowestCoveringGrant(t *testing.T) {
	f := newExchangeFixture(t)
	fifty, twenty := 50.0, 20.0
	f.xdb.grants = []db.KeyGrantRow{
		exGrant("a", []string{"prod"}, []string{exRequester}, &fifty),
		exGrant("b", []string{"prod"}, []string{exRequester}, &twenty),
	}
	resp := decodeExchange(t, f.do(t, saToken(exAudience), okBody, ""))
	if b, ok := f.llm.upsertedTags["key:"+resp.KeyID]; !ok || b.MaxBudget != 20 {
		t.Fatalf("key budget = %+v (set=%v), want 20", b, ok)
	}
}

type raceEkDB struct{ fakeEkDB }

func (*raceEkDB) InsertEnvironmentKey(context.Context, db.EkInsertRow) error {
	return fmt.Errorf("db: InsertEnvironmentKey(x): %w", &pgconn.PgError{Code: "23505", ConstraintName: db.ExchangeTupleConstraint})
}

type revokeCapture struct {
	*captureLiteLLM
	revoked []string
}

func (c *revokeCapture) RevokeKey(_ context.Context, token string) error {
	c.revoked = append(c.revoked, token)
	return nil
}

func TestExchange_RaceLoserRevokesItsKeyAndAnswersWithWinner(t *testing.T) {
	f := newExchangeFixture(t)
	f.deps.DB = &raceEkDB{}
	llm := &revokeCapture{captureLiteLLM: f.llm}
	f.deps.LiteLLM = llm
	f.xdb.winner = &db.EkKeyInfo{KeyID: "ekid_winner", Status: "active"}
	resp := decodeExchange(t, f.do(t, saToken(exAudience), okBody, ""))
	if resp.KeyID != "ekid_winner" {
		t.Fatalf("key_id %s, want the winner's", resp.KeyID)
	}
	if len(llm.revoked) != 1 {
		t.Fatalf("the race loser must revoke the LiteLLM key it minted: %v", llm.revoked)
	}
}

func TestExchange_SuspendedKeyIsRefused(t *testing.T) {
	f := newExchangeFixture(t)
	f.xdb.existing = &db.EkKeyInfo{KeyID: "ekid_s", Status: "suspended"}
	rec := f.do(t, saToken(exAudience), okBody, "")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "key_suspended" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestExchange_RateLimited(t *testing.T) {
	f := newExchangeFixture(t)
	f.review = func(context.Context, string, string) (authnv1.TokenReviewStatus, error) {
		return authnv1.TokenReviewStatus{}, nil // every admitted request is a cheap 401
	}
	var last *httptest.ResponseRecorder
	for i := 0; i <= exchangePerClientBurst; i++ {
		last = f.do(t, saToken(exAudience), okBody, fmt.Sprintf("10.0.0.7:%d", 40000+i)) // new port, same client
	}
	if last.Code != http.StatusTooManyRequests || errorCode(t, last) != "rate_limited" {
		t.Fatalf("request %d: %d %s", exchangePerClientBurst+1, last.Code, last.Body)
	}
	if f.reviews != exchangePerClientBurst {
		t.Fatalf("TokenReview calls = %d, want %d", f.reviews, exchangePerClientBurst)
	}
	if rec := f.do(t, saToken(exAudience), okBody, "10.0.0.8:1"); rec.Code == http.StatusTooManyRequests {
		t.Fatal("another client must not share the first one's budget")
	}

	g := newExchangeFixture(t)
	g.review = f.review
	limited := false
	for i := 0; i < exchangeGlobalBurst+10; i++ {
		if g.do(t, saToken(exAudience), okBody, fmt.Sprintf("10.1.%d.%d:1", i/250, i%250)).Code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Fatal("the global limit never answered 429")
	}
}

func TestExchange_AuditOneEventPerRequestWithoutCredentials(t *testing.T) {
	f := newExchangeFixture(t)
	resp := decodeExchange(t, f.do(t, saToken(exAudience), okBody, ""))
	f.xdb.grants = nil
	f.do(t, saToken(exAudience), okBody, "")

	lines := strings.Split(strings.TrimSpace(f.audit.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 audit events, got %d: %s", len(lines), f.audit)
	}
	for _, want := range []string{`"action":"platform.ek.exchange"`, `"outcome":"issued"`, `"key.id":"` + resp.KeyID + `"`,
		`"actor":"` + exUser + `"`, `"exchange.requester":"` + exRequester + `"`, `"exchange.pod_uid":"uid-1"`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("issued event lacks %s: %s", want, lines[0])
		}
	}
	if !strings.Contains(lines[1], `"outcome":"not_granted"`) {
		t.Errorf("denied event: %s", lines[1])
	}
	for _, secret := range []string{saToken(exAudience), resp.Token} {
		if strings.Contains(f.audit.String(), secret) {
			t.Fatal("a credential reached the audit log")
		}
	}
}

func TestExchange_ExistingKeyReappliesBudget(t *testing.T) {
	f := newExchangeFixture(t)
	ten := 10.0
	f.xdb.grants = []db.KeyGrantRow{exGrant("g", []string{"prod"}, []string{exRequester}, &ten)}
	f.xdb.existing = &db.EkKeyInfo{KeyID: "ekid_live", Status: "active"}
	decodeExchange(t, f.do(t, saToken(exAudience), okBody, ""))
	if b := f.llm.upsertedTags["key:ekid_live"]; b.MaxBudget != 10 {
		t.Fatalf("budget not re-applied: %+v", f.llm.upsertedTags)
	}

	f = newExchangeFixture(t)
	f.xdb.grants = []db.KeyGrantRow{exGrant("g", []string{"prod"}, []string{exRequester}, &ten)}
	f.xdb.existing = &db.EkKeyInfo{KeyID: "ekid_live", Status: "active"}
	f.llm.upsertTagErr = fmt.Errorf("boom")
	rec := f.do(t, saToken(exAudience), okBody, "")
	if rec.Code < 500 || strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatalf("a refused budget write must not issue a token: %d %s", rec.Code, rec.Body)
	}
}

func TestExchange_BodyLimits(t *testing.T) {
	for name, body := range map[string]string{
		"oversize":       `{"identity":"` + strings.Repeat("a", 5000) + `","environment":"prod"}`,
		"long identity":  `{"identity":"` + strings.Repeat("a", 257) + `","environment":"prod"}`,
		"long env":       `{"identity":"a@b.c","environment":"` + strings.Repeat("a", 257) + `"}`,
		"trailing JSON":  okBody + `{}`,
		"trailing brace": okBody + `}`,
	} {
		f := newExchangeFixture(t)
		rec := f.do(t, saToken(exAudience), body, "")
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_argument" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if f.fdb.inserted != nil {
			t.Errorf("%s: minted", name)
		}
	}
}

func TestExchange_RateLimitedSendsRetryAfter(t *testing.T) {
	f := newExchangeFixture(t)
	var last *httptest.ResponseRecorder
	for i := 0; i <= exchangePerClientBurst; i++ {
		last = f.do(t, saToken(exAudience), `{}`, "10.0.0.9:1")
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") != "1" {
		t.Fatalf("%d %v", last.Code, last.Header())
	}
}

func TestExchange_RaceWinnerSuspendedIsRefused(t *testing.T) {
	f := newExchangeFixture(t)
	f.deps.DB = &raceEkDB{}
	f.deps.LiteLLM = &revokeCapture{captureLiteLLM: f.llm}
	f.xdb.winner = &db.EkKeyInfo{KeyID: "ekid_winner", Status: "suspended"}
	rec := f.do(t, saToken(exAudience), okBody, "")
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "key_suspended" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestExchange_NewKeyBudgetWriteFailureAuditsOnce(t *testing.T) {
	ten := 10.0
	f := newExchangeFixture(t)
	f.xdb.grants = []db.KeyGrantRow{exGrant("g", []string{"prod"}, []string{exRequester}, &ten)}
	f.llm.upsertTagErr = fmt.Errorf("boom")
	rec := f.do(t, saToken(exAudience), okBody, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d %s", rec.Code, rec.Body)
	}
	if n := strings.Count(f.audit.String(), `"action":"`+audit.ActionEkExchange+`"`); n != 1 {
		t.Fatalf("want exactly one exchange audit event, got %d: %s", n, f.audit)
	}
}

func TestExchange_UnverifiedTokenNeverReachesGlobalLimitOrTokenReview(t *testing.T) {
	f := newExchangeFixture(t)
	f.verify = func(context.Context, string) error { return ErrSATokenInvalid }
	for i := 0; i < exchangeGlobalBurst+20; i++ {
		rec := f.do(t, saToken(exAudience), okBody, fmt.Sprintf("10.2.%d.%d:1", i/250, i%250))
		if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "invalid_token" {
			t.Fatalf("request %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if f.reviews != 0 {
		t.Fatalf("TokenReview called %d times for unverified tokens", f.reviews)
	}
	f.verify = nil
	if rec := f.do(t, saToken(exAudience), okBody, "10.9.9.9:1"); rec.Code != http.StatusOK {
		t.Fatalf("a real agent was starved by forged tokens: %d %s", rec.Code, rec.Body)
	}
	if strings.Count(f.audit.String(), "\n") != 1 {
		t.Fatalf("rejected requests must not be audited; log: %s", f.audit)
	}
}

func TestExchange_KeysUnavailableIs503NotReady(t *testing.T) {
	f := newExchangeFixture(t)
	f.verify = func(context.Context, string) error { return ErrSAKeysUnavailable }
	rec := f.do(t, saToken(exAudience), okBody, "")
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "not_ready" || f.reviews != 0 {
		t.Fatalf("%d %s reviews=%d", rec.Code, rec.Body, f.reviews)
	}
}

func TestExchange_RejectionsAreCounted(t *testing.T) {
	f := newExchangeFixture(t)
	reg := prometheus.NewRegistry()
	f.deps.Metrics = achmetrics.NewPlatformAPICollectors(reg)
	f.verify = func(context.Context, string) error { return ErrSATokenInvalid }
	f.do(t, "", okBody, "")
	f.do(t, saToken(exAudience), okBody, "")
	for reason, want := range map[string]float64{"guard": 1, "invalid_signature": 1} {
		if got := testutil.ToFloat64(f.deps.Metrics.KeyExchangeRejected.WithLabelValues(reason)); got != want {
			t.Errorf("%s = %v, want %v", reason, got, want)
		}
	}
}
