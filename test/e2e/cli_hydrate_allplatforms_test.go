//go:build e2e

// SPDX-License-Identifier: Apache-2.0

// Phase 7 CLI engine e2e — all-platform projection matrix.
//
// Complements TestPhase7CLIEngine (cli_hydrate_engine_test.go), which
// asserts the per-platform RUNTIME-CONFIG file + state.json schema but
// does NOT verify the CONTEXT projection leg (the plugin's skills /
// agents / commands landing in each adapter's native locations). This
// suite closes that gap: for every supported agent type it hydrates the
// `demo` Environment and asserts the projected files on disk — runtime
// config (JSON / TOML, format-validated), the skill folders, the agent
// files, and the command files — plus the documented hooks-drop gap.
//
// Self-minting pk_: unlike the Phase 6/7 suites (which require an
// engineer to inject ACH_E2E_PHASE7_PK out-of-band and otherwise SKIP),
// this suite mints its own pk_ through the Dex mockCallback SSO flow when
// the env var is absent, so it runs unattended inside the e2e devtools
// container against the standard kind+Helm fixture. ACH_E2E_PHASE7_PK is
// still honored as an override. The mint replicates the documented local
// recipe (references/local-testing-gateway.md §3): drive the browser
// JSON login flow, carrying cookies manually (the __Host- prefix cookie's
// Secure flag is bypassed by sending it ourselves rather than via a jar)
// and forcing every redirect's authority back to the single gateway
// origin (the ach-local-gateway shim multiplexes /dex and /platform on
// one host).
//
// Gating mirrors TestPhase7CLIEngine: phase7SuiteGuard t.Skipf's cleanly
// when the binary or cluster is missing, so a non-e2e run is a clean skip.
//
// Known gap asserted here (NOT a bug): plugin hooks (caveman's src/hooks/)
// are NOT projected for any platform — no adapter ProjectionRule routes src/,
// and src is non-Known metadata so it is now SILENTLY skipped (no warning).
// assertHooksDropped asserts that silence. When hooks support lands it must be
// updated alongside the per-platform hook-destination assertions. See
// .planning/todos/pending/*-support-plugin-hooks-projection.md.

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

// allPlatformExpect is the per-adapter projection contract this suite
// asserts. Paths are workspace-relative (joined under the hydrate
// --output dir). Source-of-truth for the destinations is each adapter's
// ProjectionRules() in internal/cli/adapter/<sub>/<sub>.go, cross-checked
// empirically against the demo fixture (caveman plugin) on 2026-06-02.
type allPlatformExpect struct {
	id          string // canonical platform ID (--platform value)
	runtimePath string // runtime-config file, workspace-relative
	runtimeFmt  string // "json" | "toml"
	skillsDir   string // dir holding <skill>/SKILL.md subtrees
	agentsDir   string // dir holding the projected agent files
	agentExt    string // ".md" (verbatim) | ".toml" (format-converted)
	commandsDir string // dir holding <command>.toml; "" when the adapter has no command rule
}

// allPlatformExpects is the closed set of supported agent types. codex
// is the outlier: skills route to the AGENTS.md-convention .agents/skills/
// (not .codex/skills/), agents are format-converted md→toml, and there is
// no command rule.
var allPlatformExpects = []allPlatformExpect{
	{
		id:          "claude-code",
		runtimePath: ".claude/settings.json",
		runtimeFmt:  "json",
		skillsDir:   ".claude/skills",
		agentsDir:   ".claude/agents",
		agentExt:    ".md",
		commandsDir: ".claude/commands",
	},
	{
		id:          "codex",
		runtimePath: ".codex/config.toml",
		runtimeFmt:  "toml",
		skillsDir:   ".agents/skills",
		agentsDir:   ".codex/agents",
		agentExt:    ".toml",
		commandsDir: "", // codex has no command projection rule
	},
	{
		id:          "gemini-cli",
		runtimePath: ".gemini/settings.json",
		runtimeFmt:  "json",
		skillsDir:   ".gemini/skills",
		agentsDir:   ".gemini/agents",
		agentExt:    ".md",
		commandsDir: ".gemini/commands",
	},
	{
		id:          "opencode",
		runtimePath: ".opencode/opencode.json",
		runtimeFmt:  "json",
		skillsDir:   ".opencode/skills",
		agentsDir:   ".opencode/agents",
		agentExt:    ".md",
		commandsDir: ".opencode/commands",
	},
	{
		// pimono (Pi / pi-mono, Phase 5 D-33): thinnest adapter. skills +
		// commands project verbatim into .pi/agent/; MCP deep-merges into
		// .pi/mcp.json. NO a2aAgents surface; agents are dropped (no rule).
		id:          "pimono",
		runtimePath: ".pi/mcp.json",
		runtimeFmt:  "json",
		skillsDir:   ".pi/agent/skills",
		agentsDir:   "", // pimono has no agent projection rule (dropped)
		agentExt:    "",
		commandsDir: "", // not asserted for pimono
	},
}

// demoPluginSkills are the skill names the demo fixture's `caveman`
// plugin ships. The suite asserts a representative-stable subset is
// present (coupling to the demo plugin is the same trade-off the golden
// hydrate.json takes). caveman currently ships 7 skills.
var demoPluginSkills = []string{"caveman", "cavecrew"}

// demoMCPServerIDs are the runtime MCP servers the demo Environment
// exposes. Both must appear in every adapter's runtime config with the
// bearer credential injected as the x-ach-key header.
var demoMCPServerIDs = []string{"demo-mcp-jwt", "demo-mcp-nojwt"}

// TestPhase7AllPlatformsProjection hydrates the demo Environment for
// every supported agent type and asserts the full projection — runtime
// config + skills + agents + commands — landed in each adapter's native
// locations. Self-mints a pk_ when ACH_E2E_PHASE7_PK is unset.
func TestPhase7AllPlatformsProjection(t *testing.T) {
	phase7SuiteGuard(t)
	phase7DemoEnvironmentReady(t)

	baseURL := phase7BaseURL()
	pk := phase7AcquirePk(t) // env override else self-mint via SSO mock

	for _, pe := range allPlatformExpects {
		pe := pe
		t.Run(pe.id, func(t *testing.T) {
			phase7SuiteGuard(t)

			output := t.TempDir()
			xdg := phase7SeedXdgConfig(t, baseURL, pk)

			stdout, stderr, err := phase7RunAchCli(t, xdg,
				"env", "hydrate", phase7DemoEnvironment,
				"--target", pe.id,
				"--output", output,
			)
			code, runErr := phase7StripExitErr(err)
			if runErr != nil {
				t.Fatalf("hydrate %s: exec error: %v\nstdout=%s\nstderr=%s",
					pe.id, runErr, stdout, stderr)
			}
			if code != 0 {
				t.Fatalf("hydrate %s: exit %d (want 0)\nstdout=%s\nstderr=%s",
					pe.id, code, stdout, stderr)
			}

			assertRuntimeConfig(t, output, pe, pk)
			assertProjectedSkills(t, output, pe)
			assertProjectedAgents(t, output, pe)
			assertProjectedCommands(t, output, pe)
			assertHooksDropped(t, pe.id, stderr)
			assertStateJSON(t, output, pe.id)
			assertPerEnvNamespacing(t, output)
			assertRuntimeMirror(t, output, pk)
		})
	}

	// ek_ credential path: prove `env-keys create` mints an ek_ and that
	// `hydrate --env-key <label>` projects with it. --environment is required
	// (D1: engine state is namespaced by environment) even for ek_. One
	// platform is enough to exercise the credential branch.
	t.Run("ek_path_claude_code", func(t *testing.T) {
		phase7SuiteGuard(t)
		xdg := phase7SeedXdgConfig(t, baseURL, pk)
		ek := phase7CreateEkKey(t, xdg, "allplatforms-ek")
		if !strings.HasPrefix(ek, "ek-") {
			t.Fatalf("ek_path: env-keys create returned %q (want ek_ prefix)", ek)
		}
		output := t.TempDir()
		// --environment is now required for any engine run (D1: the engine
		// namespaces state by environment), incl. the ek_ credential path.
		stdout, stderr, err := phase7RunAchCli(t, xdg,
			"env", "hydrate", phase7DemoEnvironment,
			"--target", "claude-code",
			"--env-key", "allplatforms-ek",
			"--output", output,
		)
		code, runErr := phase7StripExitErr(err)
		if runErr != nil {
			t.Fatalf("ek_path hydrate: exec error: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
		}
		if code != 0 {
			t.Fatalf("ek_path hydrate: exit %d (want 0)\nstdout=%s\nstderr=%s", code, stdout, stderr)
		}
		// The ek_ is injected as x-ach-key exactly like the pk_ path.
		assertProjectedSkills(t, output, allPlatformExpects[0]) // claude-code
		runtime := filepath.Join(output, allPlatformExpects[0].runtimePath)
		b, rErr := os.ReadFile(runtime)
		if rErr != nil {
			t.Fatalf("ek_path: read runtime config %s: %v", runtime, rErr)
		}
		if !bytes.Contains(b, []byte(ek)) {
			t.Errorf("ek_path: runtime config missing ek_ credential in x-ach-key header")
		}
	})

	// --include-runtime regression guard: runtime kinds (model/mcp/a2a) carry
	// an {id,endpoint}, not extractable content. The engine previously fed them
	// to ExtractContent and crashed with "extract content (model): content
	// name: empty". The fix gates extraction to context kinds; this asserts
	// --include-runtime exits 0 and still projects context + runtime config.
	t.Run("include_runtime_no_crash", func(t *testing.T) {
		phase7SuiteGuard(t)
		output := t.TempDir()
		xdg := phase7SeedXdgConfig(t, baseURL, pk)
		stdout, stderr, err := phase7RunAchCli(t, xdg,
			"env", "hydrate", phase7DemoEnvironment,
			"--target", "claude-code",
			"--include-runtime",
			"--output", output,
		)
		code, runErr := phase7StripExitErr(err)
		if runErr != nil {
			t.Fatalf("include_runtime: exec error: %v\nstdout=%s\nstderr=%s", runErr, stdout, stderr)
		}
		if code != 0 {
			t.Fatalf("include_runtime: exit %d (want 0 — runtime kinds must not be extracted)\n"+
				"stdout=%s\nstderr=%s", code, stdout, stderr)
		}
		if bytes.Contains(stderr, []byte("content name: empty")) {
			t.Errorf("include_runtime: regression — runtime entry hit ExtractContent\nstderr=%s", stderr)
		}
		// Context projection + runtime config still land.
		assertProjectedSkills(t, output, allPlatformExpects[0])
		assertRuntimeConfig(t, output, allPlatformExpects[0], pk)
	})
}

// assertRuntimeConfig validates the adapter's runtime-config file exists,
// parses cleanly in its declared format (JSON or TOML), and carries both
// demo MCP servers with the bearer credential injected as x-ach-key.
func assertRuntimeConfig(t *testing.T, output string, pe allPlatformExpect, cred string) {
	t.Helper()
	path := filepath.Join(output, pe.runtimePath)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: read runtime config %s: %v", pe.id, pe.runtimePath, err)
	}

	switch pe.runtimeFmt {
	case "json":
		if !json.Valid(b) {
			t.Errorf("%s: runtime config %s is not valid JSON", pe.id, pe.runtimePath)
		}
	case "toml":
		var sink map[string]any
		if _, err := toml.Decode(string(b), &sink); err != nil {
			t.Errorf("%s: runtime config %s is not valid TOML: %v", pe.id, pe.runtimePath, err)
		}
	default:
		t.Fatalf("%s: unknown runtimeFmt %q", pe.id, pe.runtimeFmt)
	}

	for _, id := range demoMCPServerIDs {
		if !bytes.Contains(b, []byte(id)) {
			t.Errorf("%s: runtime config %s missing MCP server %q", pe.id, pe.runtimePath, id)
		}
	}
	if !bytes.Contains(b, []byte(cred)) {
		t.Errorf("%s: runtime config %s missing injected credential (x-ach-key)", pe.id, pe.runtimePath)
	}
	if !bytes.Contains(b, []byte("/mcp/")) {
		t.Errorf("%s: runtime config %s missing forwarder /mcp/ URL", pe.id, pe.runtimePath)
	}
}

// assertProjectedSkills asserts the plugin's skills projected into the
// adapter's skills dir: each representative skill has a SKILL.md, and the
// total SKILL.md count is at least the known floor.
func assertProjectedSkills(t *testing.T, output string, pe allPlatformExpect) {
	t.Helper()
	skillsRoot := filepath.Join(output, pe.skillsDir)
	if fi, err := os.Stat(skillsRoot); err != nil || !fi.IsDir() {
		t.Fatalf("%s: skills dir %s missing or not a directory: %v", pe.id, pe.skillsDir, err)
	}

	for _, name := range demoPluginSkills {
		skillMd := filepath.Join(skillsRoot, name, "SKILL.md")
		if _, err := os.Stat(skillMd); err != nil {
			t.Errorf("%s: expected projected skill %s/SKILL.md missing: %v",
				pe.id, filepath.Join(pe.skillsDir, name), err)
		}
	}

	const minSkillMd = 5 // caveman ships 7; floor guards against partial projection
	count := countFilesNamed(t, skillsRoot, "SKILL.md")
	if count < minSkillMd {
		t.Errorf("%s: projected %d SKILL.md under %s, want >= %d",
			pe.id, count, pe.skillsDir, minSkillMd)
	}
}

// assertProjectedAgents asserts at least one agent file projected into
// the adapter's agents dir with the adapter's expected extension (verbatim
// .md, or format-converted .toml for codex). caveman ships cavecrew-builder.
func assertProjectedAgents(t *testing.T, output string, pe allPlatformExpect) {
	t.Helper()
	if pe.agentsDir == "" {
		return // adapter has no agent projection rule (e.g. pimono)
	}
	agentsRoot := filepath.Join(output, pe.agentsDir)
	if fi, err := os.Stat(agentsRoot); err != nil || !fi.IsDir() {
		t.Fatalf("%s: agents dir %s missing or not a directory: %v", pe.id, pe.agentsDir, err)
	}
	want := filepath.Join(agentsRoot, "cavecrew-builder"+pe.agentExt)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("%s: expected projected agent cavecrew-builder%s missing under %s: %v",
			pe.id, pe.agentExt, pe.agentsDir, err)
	}
	if n := countFilesWithExt(t, agentsRoot, pe.agentExt); n < 1 {
		t.Errorf("%s: no %s agent files projected under %s", pe.id, pe.agentExt, pe.agentsDir)
	}
}

// assertProjectedCommands asserts a representative command file projected,
// for adapters that declare a command rule. codex (commandsDir == "")
// asserts the inverse: no .codex/commands dir was created.
func assertProjectedCommands(t *testing.T, output string, pe allPlatformExpect) {
	t.Helper()
	if pe.commandsDir == "" {
		return // adapter has no asserted command destination (codex / pimono)
	}
	want := filepath.Join(output, pe.commandsDir, "caveman.toml")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("%s: expected projected command caveman.toml missing under %s: %v",
			pe.id, pe.commandsDir, err)
	}
}

// assertHooksDropped locks in the CURRENT behavior: caveman's plugin hooks
// ship under the src/ top-level, which route.KnownComponentKinds does NOT list,
// so src (and the hooks nested in it) are SILENTLY skipped for every platform —
// they never appear in the attributed drop warning. (Pre-Phase-1 src polluted a
// generic "dropped unsupported components" line; that cry-wolf line is gone.)
// When a real top-level hooks/ kind + a KnownComponentKinds entry land, this
// MUST be updated alongside the per-platform hook-destination assertions — see
// .planning/todos/pending/*-support-plugin-hooks-projection.md.
func assertHooksDropped(t *testing.T, id string, stderr []byte) {
	t.Helper()
	s := string(stderr)
	marker := "warning: platform " + id + " does not support"
	if !strings.Contains(s, marker) {
		return // no attributed warning at all → src/hooks correctly silent
	}
	body := dropBody(s, marker)
	for _, silent := range []string{"src", "hooks"} {
		if strings.Contains(body, silent) {
			t.Errorf("%s: %q must be silently skipped, not warned (metadata-silent contract)\nbody=%q",
				id, silent, body)
		}
	}
}

// stateFile is the minimal projection of state.json this suite asserts.
type stateFile struct {
	SchemaVersion string `json:"schemaVersion"`
	Environment   string `json:"environment"`
	Adapter       struct {
		ID    string `json:"id"`
		Files []struct {
			Target string `json:"target"`
		} `json:"files"`
	} `json:"adapter"`
}

// assertStateJSON asserts the engine wrote a schemaVersion="2" state.json
// bound to the hydrated adapter with at least the runtime-config file row.
func assertStateJSON(t *testing.T, output, id string) {
	t.Helper()
	path := phase7StatePath(output, phase7DemoEnvironment)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: read state.json %s: %v", id, path, err)
	}
	var st stateFile
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("%s: parse state.json: %v\n%s", id, err, b)
	}
	if st.SchemaVersion != "3" {
		t.Errorf("%s: state.json schemaVersion=%q, want \"3\"", id, st.SchemaVersion)
	}
	if st.Adapter.ID != id {
		t.Errorf("%s: state.json adapter.id=%q, want %q", id, st.Adapter.ID, id)
	}
	if len(st.Adapter.Files) < 1 {
		t.Errorf("%s: state.json adapter.files empty (want >= 1 runtime-config row)", id)
	}
}

// assertPerEnvNamespacing asserts the engine wrote state under the
// per-environment layout <output>/.ach/<env>/state.json (spec §8.1) and NOT the
// legacy flat <output>/.ach/state.json.
func assertPerEnvNamespacing(t *testing.T, output string) {
	t.Helper()
	nsState := filepath.Join(output, ".ach", phase7DemoEnvironment, "state.json")
	if _, err := os.Stat(nsState); err != nil {
		t.Errorf("per-env state.json missing at .ach/%s/state.json: %v", phase7DemoEnvironment, err)
	}
	flatState := filepath.Join(output, ".ach", "state.json")
	if _, err := os.Stat(flatState); err == nil {
		t.Errorf("legacy flat .ach/state.json must NOT exist under the namespaced layout")
	}
}

// assertRuntimeMirror asserts the .ach/<env>/runtime/ snapshots exist for the
// demo Environment (which exposes mcp + a2a + model) and are credential-free
// (OBS-02: the bearer lives only in the adapter config, never in the cache).
func assertRuntimeMirror(t *testing.T, output, cred string) {
	t.Helper()
	runtimeDir := filepath.Join(output, ".ach", phase7DemoEnvironment, "runtime")
	for _, name := range []string{"mcp", "a2a", "model"} {
		p := filepath.Join(runtimeDir, name+".json")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("runtime mirror .ach/%s/runtime/%s.json missing: %v", phase7DemoEnvironment, name, err)
			continue
		}
		if !json.Valid(b) {
			t.Errorf("runtime mirror %s.json is not valid JSON", name)
		}
		if bytes.Contains(b, []byte(cred)) || bytes.Contains(b, []byte("x-ach-key")) {
			t.Errorf("runtime mirror %s.json leaked a credential (OBS-02 violation):\n%s", name, b)
		}
	}
}

// ---- helpers -------------------------------------------------------------

// ssoMintPK drives the browser JSON SSO login flow end-to-end against the
// gateway and returns the minted pk_ plaintext. It follows redirects
// manually so it can (a) carry the __Host- prefix cookie back over plain
// http (sending it ourselves bypasses the jar's Secure-over-http refusal)
// and (b) rewrite each redirect's authority to the single gateway origin
// (the dev shim serves both /dex and /platform on one host; Dex's issuer
// emits an in-cluster DNS authority unreachable from the test process).
//
// Mirrors references/local-testing-gateway.md §3 (the documented python
// sso-login.py) in Go. mockCallback + skipApprovalScreen make the round
// trip non-interactive (static user kilgore@kilgore.trout).
func ssoMintPK(t *testing.T, baseURL string) string {
	t.Helper()
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" {
		t.Fatalf("ssoMintPK: parse baseURL %q: %v", baseURL, err)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		// Drive redirects by hand so we control cookie + authority rewrite.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	cookies := map[string]string{}
	next := strings.TrimRight(baseURL, "/") + "/platform/auth/login"

	var lastBody []byte
	var lastStatus int
	const maxHops = 12
	for hop := 0; hop < maxHops; hop++ {
		req, rErr := http.NewRequest(http.MethodGet, next, nil)
		if rErr != nil {
			t.Fatalf("ssoMintPK: new request %q: %v", next, rErr)
		}
		if len(cookies) > 0 {
			req.Header.Set("Cookie", encodeCookieMap(cookies))
		}
		resp, dErr := client.Do(req)
		if dErr != nil {
			t.Fatalf("ssoMintPK: GET %q (hop %d): %v", next, hop, dErr)
		}
		for _, c := range resp.Cookies() {
			cookies[c.Name] = c.Value
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastBody = body
		lastStatus = resp.StatusCode

		loc := resp.Header.Get("Location")
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && loc != "" {
			cur, _ := url.Parse(next)
			ref, pErr := url.Parse(loc)
			if pErr != nil {
				t.Fatalf("ssoMintPK: parse redirect Location %q: %v", loc, pErr)
			}
			abs := cur.ResolveReference(ref)
			// Force the authority back to the single gateway origin. The shim
			// routes /dex/* to Dex and /platform/* to platform-api on the same
			// host, so a host rewrite is safe for every hop.
			abs.Scheme = base.Scheme
			abs.Host = base.Host
			next = abs.String()
			continue
		}
		break
	}

	if lastStatus != http.StatusOK {
		t.Fatalf("ssoMintPK: SSO flow did not terminate at 200 (last status %d). "+
			"Is the Dex mockCallback connector configured (test/e2e/cluster/01-base/dex-config.yaml)?\nbody=%s",
			lastStatus, truncate(lastBody, 600))
	}

	var out struct {
		Plaintext  string `json:"plaintext"`
		OwnerEmail string `json:"owner_email"`
	}
	if err := json.Unmarshal(lastBody, &out); err != nil {
		t.Fatalf("ssoMintPK: parse pk_ JSON from final response: %v\nbody=%s", err, truncate(lastBody, 600))
	}
	if !strings.HasPrefix(out.Plaintext, "pk-") {
		t.Fatalf("ssoMintPK: final response carried no pk_ plaintext\nbody=%s", truncate(lastBody, 600))
	}
	// CLI-04 / OBS-02 no-leak: never log the raw pk_; the owner email is safe.
	t.Logf("ssoMintPK: minted pk_ for %s", out.OwnerEmail)
	return out.Plaintext
}

// encodeCookieMap renders the accumulated cookies into a Cookie header
// value, sending every cookie regardless of its origin Secure flag (the
// manual-send bypass the jar would refuse over plain http).
func encodeCookieMap(cookies map[string]string) string {
	parts := make([]string, 0, len(cookies))
	for name, val := range cookies {
		parts = append(parts, name+"="+val)
	}
	return strings.Join(parts, "; ")
}

// countFilesNamed counts files named exactly `name` under root (recursive).
func countFilesNamed(t *testing.T, root, name string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == name {
			n++
		}
		return nil
	})
	return n
}

// countFilesWithExt counts regular files with extension `ext` directly under
// root (non-recursive — agent files are flat per adapter).
func countFilesWithExt(t *testing.T, root, ext string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
			n++
		}
	}
	return n
}

// truncate caps a byte slice for error messages.
func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(truncated)"
}
