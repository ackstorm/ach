# ACH — Agent Configuration Hub

Multi-service Kubernetes control plane for managing AI agent configurations:
operator + platform API + forwarder + content service + CLI. All shipped as a
single Go binary (`ach`) with cobra subcommands.

## Quick start

Once the Hub is deployed and reachable at `https://ach.local.test` (the
standard kind+Helm fixture host — see `deploy/helm/ach/values.yaml` for
the `ACH_BASE_URL` default and `examples/04-environment-demo.yaml` for
the `demo` Environment the example uses):

```bash
ach login                                      # one-time device-code SSO
ach hydrate --environment demo > hydrate.json
```

The `hydrate.json` byte output reproduces `examples/hydrate.json`
verbatim against the standard fixture cluster (modulo platform-api
host substitution; the CLI e2e suite normalizes this automatically —
see `test/e2e/cli_login_hydrate_test.go`). See `examples/README.md`
for the full demo walkthrough.

## Quick links

- [Documentation](https://ackstorm.github.io/ach/)
- [Installation](https://ackstorm.github.io/ach/getting-started/installation/)
- [Architecture](https://ackstorm.github.io/ach/developer-guide/architecture/)
- [Release process](https://ackstorm.github.io/ach/developer-guide/release-process/)
- [CONTRIBUTING](CONTRIBUTING.md)
- [SECURITY](SECURITY.md)
- [MAINTAINERS](MAINTAINERS.md)
- [CHANGELOG](CHANGELOG.md)

## License

Apache-2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
