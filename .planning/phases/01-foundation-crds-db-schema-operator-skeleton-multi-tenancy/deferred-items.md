
## Plan 01-08 — orphaned kubebuilder scaffolding (deferred to Phase 5)

The following kubebuilder-generated files remain on disk under config/default/
but are NOT referenced by the new Phase 1 kustomization.yaml:

- config/default/cert_metrics_manager_patch.yaml
- config/default/manager_metrics_patch.yaml
- config/default/metrics_service.yaml

They will be either re-wired or removed by Phase 5 (OBS-03..06) when
metrics + cert-manager are properly scoped. Out of Plan 01-08 scope.
