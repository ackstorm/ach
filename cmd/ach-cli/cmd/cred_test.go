// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/config"
	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/synthetic"
)

// Well-formed ek-… keys (ek- + 64 base64url chars) for the cmd tests.
var (
	testEK      = "ek-" + strings.Repeat("A", 60) + "abcd"
	testEKSaved = "ek-" + strings.Repeat("B", 60) + "save"
	testEKEnv   = "ek-" + strings.Repeat("C", 60) + "envk"
)

// credTestEnv points XDG_CONFIG_HOME at a temp dir and clears every ACH_*
// variable resolveCred reads.
func credTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, k := range []string{"ACH_URL", "ACH_KEY", "ACH_PROFILE", "ACH_INSECURE"} {
		t.Setenv(k, "")
	}
	return dir
}

// seedProfile writes a one-profile config (the default) and returns its path.
func seedProfile(t *testing.T, name string, p *config.Profile) string {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(path, &config.File{Default: name, Profiles: map[string]*config.Profile{name: p}}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return path
}

// testOAuth is a session whose access token ("h.p.s") is fresh, so no
// refresh call is made.
func testOAuth() *config.OAuthCreds {
	return &config.OAuthCreds{ClientID: "oc_1", AccessToken: "h.p.s", RefreshToken: "r",
		ExpiresAt: time.Now().Add(time.Hour)}
}

// oauthProfile is a profile signed in with testOAuth.
func oauthProfile(url string) *config.Profile {
	return &config.Profile{URL: url, OAuth: testOAuth()}
}

func wantExit(t *testing.T, err error, code exit.Code, sub string) {
	t.Helper()
	var ce *exit.CodedError
	if !errors.As(err, &ce) || ce.Code != code || !strings.Contains(ce.Msg, sub) {
		t.Fatalf("err = %v; want exit %d containing %q", err, code, sub)
	}
}

func savedKeyProfile() *config.Profile {
	return &config.Profile{URL: "https://h", Keys: map[string]config.SavedKey{"laptop": {ID: "ekid_1", Key: testEKSaved}}}
}

func TestResolveCred_ProfilePrecedence(t *testing.T) {
	cases := []struct {
		name    string
		profile *config.Profile
		flagKey string
		envKey  string
		want    string
		wantErr string
	}{
		{name: "oauth session when nothing else", profile: oauthProfile("https://h"), want: "h.p.s"},
		{name: "profile Key without oauth", profile: &config.Profile{URL: "https://h", Key: testEK}, want: testEK},
		{name: "raw --key beats ACH_KEY", profile: oauthProfile("https://h"),
			flagKey: testEK, envKey: testEKEnv, want: testEK},
		{name: "ACH_KEY raw", profile: oauthProfile("https://h"), envKey: testEKEnv, want: testEKEnv},
		{name: "--key name from saved keys", profile: savedKeyProfile(),
			flagKey: "laptop", want: testEKSaved},
		{name: "ACH_KEY name from saved keys", profile: savedKeyProfile(),
			envKey: "laptop", want: testEKSaved},
		{name: "unknown name", profile: oauthProfile("https://h"), flagKey: "x", wantErr: `no key saved in profile "p"`},
		{name: "no credential", profile: &config.Profile{URL: "https://h"}, wantErr: `profile "p" has no credential`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credTestEnv(t)
			t.Setenv("ACH_KEY", tc.envKey)
			seedProfile(t, "p", tc.profile)
			c, err := resolveCred(context.Background(), credFlags{Key: tc.flagKey}, synthetic.GateAPI)
			if tc.wantErr != "" {
				wantExit(t, err, exit.General, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Bearer != tc.want || c.BaseURL != "https://h" || c.ProfileName != "p" || c.Profile == nil {
				t.Fatalf("got %+v; want bearer %q", c, tc.want)
			}
		})
	}
}

func TestResolveCred_Synthetic(t *testing.T) {
	credTestEnv(t)
	t.Setenv("ACH_URL", "https://synth")
	t.Setenv("ACH_KEY", testEKEnv)

	c, err := resolveCred(context.Background(), credFlags{}, synthetic.GateAPI)
	if err != nil || c.BaseURL != "https://synth" || c.Bearer != testEKEnv || c.Profile != nil {
		t.Fatalf("env credential: %+v %v", c, err)
	}
	c, err = resolveCred(context.Background(), credFlags{Key: testEK}, synthetic.GateAPI)
	if err != nil || c.Bearer != testEK {
		t.Fatalf("raw --key must override ACH_KEY: %+v %v", c, err)
	}
	_, err = resolveCred(context.Background(), credFlags{Profile: "p"}, synthetic.GateAPI)
	wantExit(t, err, exit.General, "--profile")
	_, err = resolveCred(context.Background(), credFlags{Key: "laptop"}, synthetic.GateAPI)
	wantExit(t, err, exit.General, "saved key name needs a profile")
}

func TestResolveCred_ACHURLAloneUsesProfile(t *testing.T) {
	credTestEnv(t)
	t.Setenv("ACH_URL", "https://prefill-only")
	seedProfile(t, "p", &config.Profile{URL: "https://h", Key: testEK})
	c, err := resolveCred(context.Background(), credFlags{}, synthetic.GateAPI)
	if err != nil || c.BaseURL != "https://h" || c.Bearer != testEK {
		t.Fatalf("got %+v %v", c, err)
	}
}

func TestResolveCred_NoConfig(t *testing.T) {
	credTestEnv(t)
	_, err := resolveCred(context.Background(), credFlags{}, synthetic.GateAPI)
	wantExit(t, err, exit.General, "ach-cli login")
}

func TestRegisterOutputFlag_RejectsOutsideAllowed(t *testing.T) {
	var out outputFlag
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	registerOutputFlag(cmd, &out, "table", "json")
	_, _, code, err := executeCommand(t, cmd, "-o", "yaml")
	if code != exit.General || err == nil || !strings.Contains(err.Error(), "-o must be one of table|json") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, _, code, err = executeCommand(t, cmd, "-o", "json"); err != nil || out.v != "json" {
		t.Fatalf("json: code=%d err=%v v=%q", code, err, out.v)
	}
}
