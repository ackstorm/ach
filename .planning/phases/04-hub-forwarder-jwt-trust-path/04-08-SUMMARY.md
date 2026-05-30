---
phase: 04-hub-forwarder-jwt-trust-path
plan: 08
plan_id: 04-08
status: complete
completed: 2026-05-26
mode: inline
---

# 04-08 SUMMARY — forwarder integration

Inline execution per Wave-2+ speedup directive.

Files (8): doc.go, server.go, runnable.go (new); cmd/ach/cmd/forwarder.go full rewrite; config/rbac/forwarder_role.yaml; deploy/helm/ach/templates/forwarder-rbac.yaml + forwarder-deployment.yaml + values.yaml.

Key wiring:
- bip.RegisterIndex called BEFORE first BIP GetInformer (D-09 ordering)
- jwt.SecretLoader.LoadOnce: refuse-to-start on missing/malformed
- jwt.SecretLoader.Reload wired via informer FilteringResourceEventHandler
- FWD-10 https-only ACH_BASE_URL refuse-to-start
- D-22 RBAC: namespace-scoped Role + Secret resourceNames carve-out
- D-03 dual-port Runnable, D-04 WriteTimeout=0 for SSE
- /readyz gates on signer.Loaded() + mgr cache sync

Build clean, helm lint clean, helm template renders Role (not ClusterRole) + resourceNames.

Test coverage deferred to e2e (Plan 04-09).
