// SPDX-License-Identifier: Apache-2.0

// `ach-cli repo` manages local package repositories. Four children:
//
//   - add     Register a remote git repo and detect its capabilities.
//   - list    Print all registered repos in a table.
//   - remove  Unregister a repo (idempotent).
//   - update  Re-resolve HEAD and refresh capabilities.
//
// Sources use the github: or git: URI schemes (local: sources are
// deferred to a future version). The default branch when no #ref
// fragment is supplied is "main".
//
// Tokens are stored separately in credentials.json (0600) via
// store.SaveToken / LoadToken / DeleteToken — they are never printed.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ackstorm/ach/internal/cli/exit"
	"github.com/ackstorm/ach/internal/cli/localpkg/discover"
	"github.com/ackstorm/ach/internal/cli/localpkg/source"
	"github.com/ackstorm/ach/internal/cli/localpkg/store"
	"github.com/ackstorm/ach/internal/gitfetch"
	"github.com/ackstorm/ach/internal/sourceserr"
)

// nowFn is a package-level seam so tests can override time.Now.
var nowFn = time.Now

// newRepoCmd returns a fresh `ach-cli repo` parent cobra.Command with
// the four children registered.
func newRepoCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "repo",
		Short: "Manage local package repositories",
		Long: `Manage local package repositories.

Sources use the github: or git: URI schemes. Local paths are not yet
supported. When no #ref fragment is given the default branch is "main".
`,
		RunE: helpOrUnknownSubcommand,
	}
	parent.AddCommand(
		newRepoAddCmd(),
		newRepoListCmd(),
		newRepoRemoveCmd(),
		newRepoUpdateCmd(),
	)
	return parent
}

// newRepoAddCmd returns the `ach-cli repo add <source>` leaf.
func newRepoAddCmd() *cobra.Command {
	var (
		flagName  string
		flagToken string
		flagAuth  string
		flagPath  string
	)
	c := &cobra.Command{
		Use:   "add <source>",
		Short: "Register a remote git repo and detect its capabilities",
		Long: `Register a remote git repo and detect its capabilities.

<source> must use the github: or git: URI scheme, e.g.:
  github:owner/repo[#ref]
  git:https://host/path[.git][#ref]

Local paths (/, ./, ~) are not yet supported.
When no #ref fragment is given the default branch is "main".

Tokens are read from --token; when absent the command falls back to
GITHUB_TOKEN (github: sources) or GITLAB_TOKEN (git: sources).
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			su, err := source.Parse(args[0], flagAuth)
			if err != nil {
				return &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("repo add: %v", err)}
			}
			if su.Kind == source.KindLocal {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  "repo add: local sources are not yet supported (use github:/git:)",
				}
			}

			// Resolve token: flag > env fallback.
			token := flagToken
			if token == "" {
				switch su.Kind {
				case source.KindGitHub:
					token = os.Getenv("GITHUB_TOKEN")
				case source.KindGit:
					token = os.Getenv("GITLAB_TOKEN")
				}
			}

			// Reject duplicate name.
			repos, err := store.LoadRepos()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo add: load repos: %v", err)}
			}
			for _, r := range repos.Repos {
				if r.Name == flagName {
					return &exit.CodedError{
						Code: exit.General,
						Msg:  fmt.Sprintf("repo add: repo %q already registered", flagName),
					}
				}
			}

			sha, caps, skillsRoot, err := resolveAndDetect(ctx, su, token, flagPath)
			if err != nil {
				return err
			}
			if len(caps) == 0 {
				msg := fmt.Sprintf("repo add: no installable plugins or skills found in %q", args[0])
				if flagPath != "" {
					// --path is the skills-marketplace root hint only; v1 does NOT
					// fetch-narrow a direct plugin/skill that lives in a subdirectory
					// (the --path dual-semantics ambiguity is deferred). Point the
					// user at that scope so a subdir plugin repo isn't a silent dead end.
					msg += fmt.Sprintf(" (note: --path %q only sets the skills-marketplace root; "+
						"v1 does not narrow a direct plugin/skill in a subdirectory)", flagPath)
				}
				return &exit.CodedError{Code: exit.General, Msg: msg}
			}

			// Persist token separately.
			if token != "" {
				if err := store.SaveToken(flagName, token); err != nil {
					return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo add: save token: %v", err)}
				}
			}

			// Determine the ref that was used.
			ref := su.GitRef
			if ref == "" {
				ref = "main"
			}

			entry := store.RepoEntry{
				Name:           flagName,
				Source:         args[0],
				Kind:           kindStr(su.Kind),
				CloneURL:       su.CloneURL,
				GitRef:         ref,
				AuthScheme:     su.AuthScheme,
				HasToken:       token != "",
				Provides:       caps,
				SkillsRootHint: skillsRoot,
				DetectedSHA:    sha,
				AddedAt:        nowFn().UTC().Format(time.RFC3339),
			}
			repos.Repos = append(repos.Repos, entry)
			if err := store.SaveRepos(repos); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo add: save repos: %v", err)}
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ repo %q  %s  %s  (provides: %s)\n",
				flagName, kindStr(su.Kind), args[0], formatProvides(caps))
			return nil
		},
	}
	c.Flags().StringVar(&flagName, "name", "", "Name for the registered repo (required)")
	_ = c.MarkFlagRequired("name")
	c.Flags().StringVar(&flagToken, "token", "", "Auth token (falls back to GITHUB_TOKEN / GITLAB_TOKEN)")
	c.Flags().StringVar(&flagAuth, "auth", "", "Auth scheme: bearer (default) or basic-oauth2")
	c.Flags().StringVar(&flagPath, "path", "",
		"Skills-marketplace root hint (subdir holding skills/; v1 does not narrow direct plugin/skill repos)")
	return c
}

// newRepoListCmd returns the `ach-cli repo list` leaf.
func newRepoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Print all registered repos in a table",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repos, err := store.LoadRepos()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo list: load repos: %v", err)}
			}
			if len(repos.Repos) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no repos registered")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tKIND\tSOURCE\tAUTH\tPROVIDES")
			for _, r := range repos.Repos {
				auth := "-"
				if r.HasToken {
					scheme := r.AuthScheme
					if scheme == "" {
						scheme = source.AuthBearer
					}
					auth = scheme + " •••"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					r.Name,
					r.Kind,
					r.Source,
					auth,
					formatProvides(r.Provides),
				)
			}
			_ = w.Flush()
			return nil
		},
	}
}

// newRepoRemoveCmd returns the `ach-cli repo remove <name>` leaf.
func newRepoRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a repo (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			repos, err := store.LoadRepos()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo remove: load repos: %v", err)}
			}

			idx := -1
			for i, r := range repos.Repos {
				if r.Name == name {
					idx = i
					break
				}
			}
			if idx < 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "repo %q not registered\n", name)
				return nil
			}

			repos.Repos = append(repos.Repos[:idx], repos.Repos[idx+1:]...)
			// DeleteToken before SaveRepos: if we crash between the two writes the
			// credential is gone but the repos entry is still present, so a retry
			// re-enters here and re-deletes (idempotent). Acceptable for a
			// local single-user store — no atomicity guarantee is needed.
			if err := store.DeleteToken(name); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo remove: delete token: %v", err)}
			}
			if err := store.SaveRepos(repos); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo remove: save repos: %v", err)}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ removed repo %q\n", name)
			return nil
		},
	}
}

// newRepoUpdateCmd returns the `ach-cli repo update <name>` leaf.
func newRepoUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update <name>",
		Short: "Re-resolve HEAD and refresh capabilities",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			name := args[0]

			repos, err := store.LoadRepos()
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo update: load repos: %v", err)}
			}

			var entry *store.RepoEntry
			var entryIdx int
			for i := range repos.Repos {
				if repos.Repos[i].Name == name {
					entry = &repos.Repos[i]
					entryIdx = i
					break
				}
			}
			if entry == nil {
				return &exit.CodedError{
					Code: exit.General,
					Msg:  fmt.Sprintf("repo update: repo %q not registered", name),
				}
			}

			// Rebuild SourceURI from stored entry.
			su := rebuildSourceURI(entry)

			token, err := store.LoadToken(name)
			if err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo update: load token: %v", err)}
			}

			sha, caps, skillsRoot, err := resolveAndDetect(ctx, su, token, entry.SkillsRootHint)
			if err != nil {
				return err
			}

			if len(caps) == 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "! warning: repo %q now provides no installable plugins or skills\n", name)
			}

			shortSHA := sha
			if len(shortSHA) > 7 {
				shortSHA = shortSHA[:7]
			}

			if sha == entry.DetectedSHA {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "repo %q up to date (%s)\n", name, shortSHA)
				return nil
			}

			repos.Repos[entryIdx].DetectedSHA = sha
			repos.Repos[entryIdx].Provides = caps
			repos.Repos[entryIdx].SkillsRootHint = skillsRoot
			if err := store.SaveRepos(repos); err != nil {
				return &exit.CodedError{Code: exit.ConfigFile, Msg: fmt.Sprintf("repo update: save repos: %v", err)}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ updated repo %q → %s (provides: %s)\n",
				name, shortSHA, formatProvides(caps))
			return nil
		},
	}
}

// resolveAndDetect resolves the HEAD SHA via LsRemote, clones the repo,
// and detects capabilities. flagPath is the optional skills-root hint; the
// returned skillsRoot reports the root Detect actually matched (the explicit
// hint, the autodetected root, or "" when no skill tree matched) so the caller
// can persist it as the repo's SkillsRootHint.
func resolveAndDetect(
	ctx context.Context, su source.SourceURI, token, flagPath string,
) (sha string, caps []store.Capability, skillsRoot string, err error) {
	// Map auth scheme.
	var scheme gitfetch.AuthScheme
	if su.AuthScheme == source.AuthBasicOAuth2 {
		scheme = gitfetch.AuthBasicOAuth2
	} else {
		scheme = gitfetch.AuthBearer
	}

	ref := su.GitRef
	if ref == "" {
		ref = "main"
	}

	sha, err = gitfetch.LsRemote(ctx, su.CloneURL, ref, token, scheme)
	if err != nil {
		return "", nil, "", cloneExitErr("resolve", err)
	}

	fetcher := gitfetch.New(gitfetch.Spec{
		URL:        su.CloneURL,
		Ref:        ref,
		SHA:        sha,
		Token:      token,
		AuthScheme: scheme,
	})
	res, err := fetcher.Fetch(ctx, gitfetch.Request{})
	if err != nil {
		return "", nil, "", cloneExitErr("fetch", err)
	}
	defer func() { _ = res.Body.Close() }()

	tarball, err := io.ReadAll(res.Body)
	if err != nil {
		return "", nil, "", cloneExitErr("read", err)
	}

	caps, skillsRoot, err = discover.Detect(tarball, flagPath)
	if err != nil {
		return "", nil, "", &exit.CodedError{Code: exit.General, Msg: fmt.Sprintf("repo detect: %v", err)}
	}
	return sha, caps, skillsRoot, nil
}

// cloneExitErr maps an error to a CodedError by testing the sourceserr
// sentinels directly via errors.Is, defaulting to exit.General.
//
// This wrapper is used both for genuine source-fetch failures (gitfetch wraps
// the sourceserr sentinels) AND to wrap broad manager.Resolve errors from the
// install/update paths. Those Resolve errors include pure LOGIC failures
// (name mismatch, "plugin/skill not found", verify-fail, "no SKILL.md") that
// carry no sentinel. We must NOT lean on sourceserr.ReasonOf here: its default
// is the conservative "Unreachable" (correct for the fetch domain, where an
// unclassified transport error is retried as transient) which would route such
// logic errors to exit.Network(6). Defaulting to exit.General(1) keeps real
// fetch errors correct (the sentinels still match identically to ReasonOf via
// errors.Is) while sending unclassified logic errors to General.
func cloneExitErr(action string, err error) *exit.CodedError {
	var code exit.Code
	switch {
	case errors.Is(err, sourceserr.ErrUnauthorized):
		code = exit.AuthN
	case errors.Is(err, sourceserr.ErrUnreachable):
		code = exit.Network
	default:
		code = exit.General
	}
	return &exit.CodedError{
		Code:    code,
		Msg:     fmt.Sprintf("repo %s: %v", action, err),
		Wrapped: err,
	}
}

// kindStr converts a source.Kind to its string representation for storage.
func kindStr(k source.Kind) string {
	switch k {
	case source.KindGitHub:
		return "github"
	case source.KindGit:
		return "git"
	case source.KindLocal:
		return "local"
	default:
		return "unknown"
	}
}

// rebuildSourceURI reconstructs a source.SourceURI from a stored RepoEntry.
func rebuildSourceURI(e *store.RepoEntry) source.SourceURI {
	var kind source.Kind
	switch e.Kind {
	case "github":
		kind = source.KindGitHub
	case "git":
		kind = source.KindGit
	case "local":
		kind = source.KindLocal
	}
	return source.SourceURI{
		Kind:       kind,
		CloneURL:   e.CloneURL,
		GitRef:     e.GitRef,
		LocalPath:  e.LocalPath,
		AuthScheme: e.AuthScheme,
	}
}

// formatProvides returns a space-separated "lens:count ..." string.
func formatProvides(caps []store.Capability) string {
	parts := make([]string, len(caps))
	for i, c := range caps {
		parts[i] = fmt.Sprintf("%s:%d", c.Lens, c.Count)
	}
	return strings.Join(parts, " ")
}

// newRepoCmd is registered under the `local` parent (G9) — see local.go.
