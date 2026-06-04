# Plan — `ach login` URL pre-fill from `ACH_PLATFORM_URL`

**Date:** 2026-06-04
**Author:** planner (separate agent executes; self-contained).
**Goal:** Let `ach login` pre-fill the interactive `URL:` prompt from a new
`ACH_PLATFORM_URL` env var, so users who export it don't retype the hub URL.

## Why a NEW var (not `ACH_BASE_URL`)

`ACH_BASE_URL` is the **synthetic-mode trigger** and CANNOT be reused here:
- `internal/cli/synthetic/synthetic.go:159 IsActive` / `:173 IsHalfSet` key off
  `ACH_BASE_URL`. `login` is in the synthetic **deny-set**
  (`synthetic.go:214 GateLogin`).
- `ACH_BASE_URL` set + credential → `login` denied. Set alone → half-set hard
  error (`synthetic.go:199`). So `ACH_BASE_URL` can never pre-fill login.

`ACH_PLATFORM_URL` is unused anywhere in the repo today (grep-confirmed) → safe,
no synthetic collision. **Do NOT touch `internal/cli/synthetic/`** — its gates
must stay keyed on `ACH_BASE_URL` only.

## Single change — `cmd/ach-cli/cmd/login.go`

Modify `resolveBaseURL` (currently `login.go:311`). New precedence:
**`--base-url` flag → `ACH_PLATFORM_URL` env → interactive prompt.**

Current:
```go
func resolveBaseURL(flagVal string, stdin io.Reader, stdout io.Writer) (string, error) {
	url := strings.TrimSpace(flagVal)
	if url == "" {
		v, err := readLine("URL: ", stdin, stdout)
		if err != nil {
			return "", err
		}
		url = v
	}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", &exit.CodedError{Code: exit.General, Msg: "url must be http:// or https://"}
	}
	return url, nil
}
```

Target:
```go
func resolveBaseURL(flagVal string, stdin io.Reader, stdout io.Writer) (string, error) {
	url := strings.TrimSpace(flagVal)
	if url == "" {
		// Env pre-fill: ACH_PLATFORM_URL is a login-only convenience,
		// distinct from ACH_BASE_URL (the synthetic-mode trigger).
		if env := strings.TrimSpace(os.Getenv("ACH_PLATFORM_URL")); env != "" {
			url = env
			_, _ = fmt.Fprintf(stdout, "URL: %s (from ACH_PLATFORM_URL)\n", url)
		}
	}
	if url == "" {
		v, err := readLine("URL: ", stdin, stdout)
		if err != nil {
			return "", err
		}
		url = v
	}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return "", &exit.CodedError{Code: exit.General, Msg: "url must be http:// or https://"}
	}
	return url, nil
}
```

Notes for the executor:
- `os` and `fmt` are likely already imported in `login.go`; add `os` if not.
  Use plain `os.Getenv` — matches `hydrate.go:339` / `whoami.go:191`
  (`ACH_BASE_URL` reads). Do NOT use the synthetic `Getenv` wrapper.
- Flag still wins over env (more specific). Env still runs the http/https
  validation. Echo the chosen URL so a piped/non-interactive run shows what was
  used.
- The `http://` plaintext warning at `login.go:139` already runs on the
  returned `url`, so an `http://` value from the env still warns. No change
  there.
- `ACH_PLATFORM_URL` does NOT affect the synthetic gate at `login.go:116`. If a
  user sets BOTH `ACH_BASE_URL` and `ACH_PLATFORM_URL`, the synthetic
  half-set/deny fires first (correct, unchanged) — document this, don't code
  around it.

## Tests — `cmd/ach-cli/cmd/login_test.go`

Add `resolveBaseURL` cases (table-driven; the file already manipulates env —
see `login_test.go:87`/`:301`). t.Setenv for isolation:
1. flag set + env set → flag wins.
2. flag empty + `ACH_PLATFORM_URL` set → returns env value, no prompt read
   (pass a stdin that would error/block if read, assert it wasn't).
3. flag empty + env empty → falls through to prompt (existing behavior).
4. env set to a non-http value → `url must be http:// or https://` error.
5. env set to `http://...` → returned + plaintext warning path intact (assert
   at the `runLogin` level if a warning test exists, else just the value).

**Verify:** `make test-unit-pkg PKG=./cmd/ach-cli/...` + `make qa-lint-changed`.

## Docs

- Wherever `ach login` flags are documented (grep `docs/` + `examples/README.md`
  for `--base-url`): add `ACH_PLATFORM_URL` as the pre-fill var + the precedence
  (flag → env → prompt) + the one-line caveat that it is distinct from
  `ACH_BASE_URL` and does not enable synthetic mode.
- No CLAUDE.md change expected (no contract/workflow line shifts).

## Out of scope
- Touching `ACH_BASE_URL` / synthetic gates.
- Pre-filling profile name from env.
- Making the prompt an editable default (env → use directly; keep `readLine`
  plain).

## Commit
`feat(cli): pre-fill ach login URL from ACH_PLATFORM_URL`
(single commit; unit-isolated, CLI-only — no e2e gate needed, but the pre-push
18-gate still applies before push.)
