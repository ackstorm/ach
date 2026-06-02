# API Reference

## Packages
- [ach.ackstorm.ai/v1alpha1](#achackstormaiv1alpha1)


## ach.ackstorm.ai/v1alpha1

Package v1alpha1 contains API Schema definitions for the ach v1alpha1 API group.

### Resource Types
- [Artifact](#artifact)
- [ArtifactList](#artifactlist)
- [BackendIdentityPolicy](#backendidentitypolicy)
- [BackendIdentityPolicyList](#backendidentitypolicylist)
- [Environment](#environment)
- [EnvironmentList](#environmentlist)
- [LiteLLMConnection](#litellmconnection)
- [LiteLLMConnectionList](#litellmconnectionlist)
- [Plugin](#plugin)
- [PluginList](#pluginlist)
- [PluginMarketplace](#pluginmarketplace)
- [PluginMarketplaceList](#pluginmarketplacelist)
- [Prompt](#prompt)
- [PromptList](#promptlist)



#### Artifact



Artifact is the Schema for the artifacts API (Hub §13).



_Appears in:_
- [ArtifactList](#artifactlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `Artifact` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ArtifactSpec](#artifactspec)_ |  |  |  |
| `status` _[ArtifactStatus](#artifactstatus)_ |  |  |  |


#### ArtifactList



ArtifactList contains a list of Artifact.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `ArtifactList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[Artifact](#artifact) array_ |  |  |  |


#### ArtifactSpec



ArtifactSpec defines the desired state of Artifact (Hub §13).

CRD-05: spec.scope is REQUIRED and is one of {object, directory}. For
type=http only scope=object is permitted; CRD admission rejects
directory scope on http sources.
CRD-04: spec.refresh.maxStaleness is REQUIRED.
CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).



_Appears in:_
- [Artifact](#artifact)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type names the upstream source kind. |  | Enum: [github gitlab bitbucket s3 gcs http] <br />Required: \{\} <br /> |
| `scope` _string_ | Scope picks single-object vs directory-bundle delivery (CRD-05).<br />object: serves exactly one upstream object verbatim.<br />directory: materializes the directory tree into a .tar.gz at refresh time. |  | Enum: [object directory] <br />Required: \{\} <br /> |
| `refresh` _[RefreshBlock](#refreshblock)_ | Refresh declares poll cadence and staleness bound (CRD-04). |  | Required: \{\} <br /> |
| `github` _[GitHubSource](#githubsource)_ | GitHub source. Required when spec.type == "github". |  |  |
| `gitlab` _[GitLabSource](#gitlabsource)_ | GitLab source. Required when spec.type == "gitlab". |  |  |
| `bitbucket` _[BitbucketSource](#bitbucketsource)_ | Bitbucket source. Required when spec.type == "bitbucket". |  |  |
| `s3` _[S3Source](#s3source)_ | S3 source. Required when spec.type == "s3". |  |  |
| `gcs` _[GCSSource](#gcssource)_ | GCS source. Required when spec.type == "gcs". |  |  |
| `http` _[HTTPSource](#httpsource)_ | HTTP source. Required when spec.type == "http". |  |  |


#### ArtifactStatus



ArtifactStatus defines the observed state of Artifact.



_Appears in:_
- [Artifact](#artifact)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the<br />reconciler most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions exposes SourceReachable (and, for PluginMarketplace,<br />Synced) per §6.6. |  |  |
| `storageLocation` _string_ | StorageLocation is the cached filesystem path the Content Service<br />serves from after the last successful refresh (§10.3). Empty until<br />the first successful refresh. |  |  |
| `lastSuccessfulRefresh` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | LastSuccessfulRefresh is the wall-clock time of the most recent<br />successful upstream fetch + atomic publish (§10.3 step 5). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the per-source revision identifier the most recent<br />successful refresh recorded — for git sources this is the resolved<br />commit SHA; for S3 it is the object ETag; for GCS the object<br />generation; for HTTP a composite of ETag and Last-Modified<br />separated by a literal pipe. The Phase 2 reconciler reads this<br />value to pass as PriorRev on the next fetch for conditional-GET /<br />not-modified detection. Empty before the first successful refresh. |  |  |


#### BackendIdentityPolicy



BackendIdentityPolicy is the Schema for the backendidentitypolicies API
(Hub §9.3, CRD-08).



_Appears in:_
- [BackendIdentityPolicyList](#backendidentitypolicylist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `BackendIdentityPolicy` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[BackendIdentityPolicySpec](#backendidentitypolicyspec)_ |  |  |  |
| `status` _[BackendIdentityPolicyStatus](#backendidentitypolicystatus)_ |  |  |  |


#### BackendIdentityPolicyList



BackendIdentityPolicyList contains a list of BackendIdentityPolicy.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `BackendIdentityPolicyList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[BackendIdentityPolicy](#backendidentitypolicy) array_ |  |  |  |


#### BackendIdentityPolicySpec



BackendIdentityPolicySpec defines the desired state of BackendIdentityPolicy
(Hub §9.3, CRD-08).

The CR is the OPT-IN switch for attaching the ACH-signed JWT to forwarded
/mcp/<name> or /a2a/<name> requests. Without a matching CR, the Forwarder
strips the client Authorization header and writes none of its own.

CRD-08: forwardIdentityJWT is REQUIRED — no Go zero-value default,
no kubebuilder default. The explicit false form is a documentation aid;
admission rejects a CR omitting the field via the resource-root CEL rule
on the type below.



_Appears in:_
- [BackendIdentityPolicy](#backendidentitypolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `target` _[BackendTargetRef](#backendtargetref)_ | Target identifies the backend route this policy controls. |  | Required: \{\} <br /> |
| `forwardIdentityJWT` _boolean_ | ForwardIdentityJWT, when true, instructs the Forwarder to sign and<br />attach the §9.1 ACH JWT to forwarded requests for this target. When<br />false, the Forwarder forwards without an Authorization header (an<br />explicit no-JWT declaration; operationally indistinguishable from no<br />CR at all).<br />REQUIRED per CRD-08. There is no default. Admission rejects a CR<br />that omits the field. |  | Required: \{\} <br /> |


#### BackendIdentityPolicyStatus



BackendIdentityPolicyStatus defines the observed state of
BackendIdentityPolicy.

Deliberate scope: the reconciler does NOT perform DuplicateTarget
shadow resolution. Multiple CRs targeting the same (kind, name) are
allowed; the Forwarder's runtime read path resolves the duplicate
deterministically by sorting matching CRs by metadata.name ASC and
using the LAST entry's forwardIdentityJWT value. Operators wanting
different precedence rename their CRs. This is intentional —
operator stays dumb, user owns the CR set, no Synced=DuplicateTarget
condition is written.



_Appears in:_
- [BackendIdentityPolicy](#backendidentitypolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the reconciler<br />most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions is reserved for future per-CR status surfaces. It is<br />NOT populated today — duplicate targets are resolved by the<br />Forwarder at read time (alphabetically-LAST CR wins) without any<br />operator-side status churn. |  |  |


#### BackendTargetRef



BackendTargetRef identifies the route a BackendIdentityPolicy applies to
(Hub §9.3).



_Appears in:_
- [BackendIdentityPolicySpec](#backendidentitypolicyspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `kind` _string_ | Kind is the routed backend type. CEL-enforced enum. |  | Enum: [MCPServer A2AAgent] <br />Required: \{\} <br /> |
| `name` _string_ | Name is the bare route segment the Forwarder sees as <name> in<br />/mcp/<name> or /a2a/<name>. MUST satisfy DNS-1123 subdomain rules<br />(≤253 chars, [a-z0-9]([-a-z0-9.]*[a-z0-9])?). Pattern enforced<br />per CRD-08 / Hub §9.3. |  | MaxLength: 253 <br />MinLength: 1 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$` <br />Required: \{\} <br /> |


#### BitbucketSource



BitbucketSource describes a bitbucket-hosted upstream (Hub §10.1).



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `workspace` _string_ | Workspace name on Bitbucket. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `repo` _string_ | Repo within the workspace. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `path` _string_ | Path within the repo. |  |  |
| `ref` _string_ | Ref is a branch or tag name. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef is optional. When set, the Secret named here MUST<br />exist in the CR's namespace at reconcile time and the operator<br />reads the bearer token from the named key (see SourceAuthSecretRef.Key).<br />When nil, the upstream fetch is anonymous — supported only for<br />public repositories on the git transport (transport=rest paired<br />with no auth typically fails because most Bitbucket Cloud REST<br />endpoints require auth even for public repos). Bitbucket Cloud<br />anonymous REST quota: 60 req/h/IP. |  |  |
| `transport` _string_ | Transport selects the wire protocol used to fetch from this upstream.<br />  "git"  — use git ls-remote + git clone (no per-IP REST rate-limit;<br />           recommended; default).<br />  "rest" — use the provider's REST API. Subject to per-IP anonymous<br />           quotas (GitHub: 60/h; GitLab: 60/min; Bitbucket: 60/h).<br />           Retained as a one-release escape hatch; will be removed. | git | Enum: [git rest] <br /> |


#### ContextBlock



ContextBlock is the content-resource half of an Environment (Hub §6, §6.1).
Names reference ACH-owned content objects (Prompt, Plugin, Artifact CRDs
or marketplace_plugins rows) and are served by the ACH Content Service.



_Appears in:_
- [EnvironmentSpec](#environmentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `prompts` _string array_ | Prompts lists referenced Prompt names. Context names map to content<br />filenames served by the Content Service, so the stricter deny-pattern<br />also forbids "/" and "\" (path-traversal) in addition to ? # %<br />whitespace and control chars (S2 defense-in-depth). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f]+$ <br /> |
| `plugins` _string array_ | Plugins lists referenced Plugin (or marketplace plugin) names. | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f]+$ <br /> |
| `artifacts` _string array_ | Artifacts lists referenced Artifact names. | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f]+$ <br /> |


#### Environment



Environment is the Schema for the environments API (Hub §6).

An Environment is the ACH product boundary: a bundle of runtime
(models, mcpServers, a2aAgents) and context (prompts, plugins,
artifacts) capabilities exposed to authorized Teams. The ACH
Operator reconciles spec.runtime into a LiteLLM access group of
the same name (§6.2). The CEL XValidation rules above enforce
CRD-02 at admission.



_Appears in:_
- [EnvironmentList](#environmentlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `Environment` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[EnvironmentSpec](#environmentspec)_ |  |  |  |
| `status` _[EnvironmentStatus](#environmentstatus)_ |  |  |  |


#### EnvironmentList



EnvironmentList contains a list of Environment.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `EnvironmentList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[Environment](#environment) array_ |  |  |  |


#### EnvironmentSpec



EnvironmentSpec defines the desired state of Environment (CRD-02, Hub §6).



_Appears in:_
- [Environment](#environment)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `runtime` _[RuntimeBlock](#runtimeblock)_ | Runtime is the execution-resource bundle projected into the LiteLLM<br />access group (§6.2). Always present per CRD-02. |  | Required: \{\} <br /> |
| `context` _[ContextBlock](#contextblock)_ | Context is the content-resource bundle served by Content Service<br />(§10, §15.6). Always present per CRD-02. |  | Required: \{\} <br /> |
| `authorizedTeams` _string array_ | AuthorizedTeams references LiteLLM Team aliases (§6.1). The Environment<br />is unusable when no entry resolves to an existing LiteLLM Team;<br />admission requires at least one entry per Hub §6 (informational —<br />reconcile-time existence is verified per §6.4). |  | MinItems: 1 <br />Required: \{\} <br /> |


#### EnvironmentStatus



EnvironmentStatus defines the observed state of Environment (Hub §6.4, §6.6).



_Appears in:_
- [Environment](#environment)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the reconciler<br />most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions carries Environment condition types per §6.6 closed set:<br />Available, ContentReady, ExecutionResourcesResolved, AccessGroupSynced. |  |  |
| `unresolvedRuntime` _[UnresolvedRuntime](#unresolvedruntime)_ | UnresolvedRuntime lists runtime references not currently registered in<br />LiteLLM. Surfaced for `kubectl describe environment` per §6.4. The<br />field contract belongs here from Phase 1; the reconciler in Phase 2<br />rewrites it on every reconcile. |  |  |
| `litellmAccessGroup` _string_ | LitellmAccessGroup is the synced LiteLLM access group name (§6.4).<br />Echoed for operator visibility; equals metadata.name when set. |  |  |


#### ExternalRefStatus



ExternalRefStatus is the shared status surface for external-reference
resources (Plugin, Prompt, Artifact, PluginMarketplace) per Hub §6.6.



_Appears in:_
- [ArtifactStatus](#artifactstatus)
- [PluginMarketplaceStatus](#pluginmarketplacestatus)
- [PluginStatus](#pluginstatus)
- [PromptStatus](#promptstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the<br />reconciler most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions exposes SourceReachable (and, for PluginMarketplace,<br />Synced) per §6.6. |  |  |
| `storageLocation` _string_ | StorageLocation is the cached filesystem path the Content Service<br />serves from after the last successful refresh (§10.3). Empty until<br />the first successful refresh. |  |  |
| `lastSuccessfulRefresh` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | LastSuccessfulRefresh is the wall-clock time of the most recent<br />successful upstream fetch + atomic publish (§10.3 step 5). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the per-source revision identifier the most recent<br />successful refresh recorded — for git sources this is the resolved<br />commit SHA; for S3 it is the object ETag; for GCS the object<br />generation; for HTTP a composite of ETag and Last-Modified<br />separated by a literal pipe. The Phase 2 reconciler reads this<br />value to pass as PriorRev on the next fetch for conditional-GET /<br />not-modified detection. Empty before the first successful refresh. |  |  |


#### GCSSource



GCSSource describes a Google Cloud Storage upstream (Hub §10.1).
No ref field — refresh polls the object's generation (single object)
or the prefix listing (directory scope).



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `bucket` _string_ | Bucket name. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `object` _string_ | Object name (single object) or prefix (directory scope). |  | MinLength: 1 <br />Required: \{\} <br /> |
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef points at the Secret carrying a service-account JSON<br />blob (data key named via .key). |  | Required: \{\} <br /> |


#### GitHubSource



GitHubSource describes a github-hosted upstream (Hub §10.1).



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `repo` _string_ | Repo is the "<owner>/<name>" GitHub identifier. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `path` _string_ | Path within the repo. Per-kind defaults apply (e.g. Plugin defaults<br />to repo root; PluginMarketplace defaults to .claude-plugin/marketplace.json). |  |  |
| `ref` _string_ | Ref is a branch or tag name. No immutable commit refs in v1alpha1<br />(CRD-04, Hub §10). |  | MinLength: 1 <br />Required: \{\} <br /> |
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef is optional. When set, the Secret named here MUST<br />exist in the CR's namespace at reconcile time and the operator<br />reads the bearer token from the named key (see SourceAuthSecretRef.Key).<br />When nil, the upstream fetch is anonymous — supported only for<br />public repositories. Anonymous + transport=rest is also supported<br />but subject to the provider's anonymous REST quota (GitHub:<br />60 req/h/IP) — the bug FIX_GIT.txt fixes by defaulting transport<br />to git. |  |  |
| `transport` _string_ | Transport selects the wire protocol used to fetch from this upstream.<br />  "git"  — use git ls-remote + git clone (no per-IP REST rate-limit;<br />           recommended; default).<br />  "rest" — use the provider's REST API. Subject to per-IP anonymous<br />           quotas (GitHub: 60/h; GitLab: 60/min; Bitbucket: 60/h).<br />           Retained as a one-release escape hatch; will be removed. | git | Enum: [git rest] <br /> |


#### GitLabSource



GitLabSource describes a gitlab-hosted upstream (Hub §10.1).



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `host` _string_ | Host of the GitLab instance. Defaults to gitlab.com when empty. |  |  |
| `project` _string_ | Project is the "<namespace>/<project>" GitLab identifier. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `path` _string_ | Path within the project repo. |  |  |
| `ref` _string_ | Ref is a branch or tag name. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef is optional. When set, the Secret named here MUST<br />exist in the CR's namespace at reconcile time and the operator<br />reads the bearer token from the named key (see SourceAuthSecretRef.Key).<br />When nil, the upstream fetch is anonymous — supported only for<br />public projects. Anonymous + transport=rest is also supported<br />but subject to the provider's anonymous REST quota (GitLab:<br />60 req/min/IP) — the bug FIX_GIT.txt fixes by defaulting transport<br />to git. |  |  |
| `transport` _string_ | Transport selects the wire protocol used to fetch from this upstream.<br />  "git"  — use git ls-remote + git clone (no per-IP REST rate-limit;<br />           recommended; default).<br />  "rest" — use the provider's REST API. Subject to per-IP anonymous<br />           quotas (GitHub: 60/h; GitLab: 60/min; Bitbucket: 60/h).<br />           Retained as a one-release escape hatch; will be removed. | git | Enum: [git rest] <br /> |


#### HTTPSource



HTTPSource describes a generic HTTP/HTTPS upstream (Hub §10.1).
No ref field — refresh issues a conditional GET when the server
supports If-Modified-Since / If-None-Match, otherwise a full GET.

Phase 02.1: the original HTTPS-only invariant (T-02-02-03) was lifted
to admit in-cluster development fixture-servers serving plaintext HTTP.
Production deployments are expected to use https:// URLs by convention,
but the constraint is no longer machine-enforced at admission or fetch.



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL of the upstream resource. Accepts http:// or https://. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef optionally attaches an authentication header<br />(e.g. Authorization: Bearer ...). The data key named via<br />.headerValueKey supplies the header value at request time. |  |  |


#### LiteLLMConnection



LiteLLMConnection is the singleton connection definition used by the ACH
operator. v1alpha1 admits only LiteLLMConnection/default per namespace.



_Appears in:_
- [LiteLLMConnectionList](#litellmconnectionlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `LiteLLMConnection` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[LiteLLMConnectionSpec](#litellmconnectionspec)_ |  |  |  |
| `status` _[LiteLLMConnectionStatus](#litellmconnectionstatus)_ |  |  |  |


#### LiteLLMConnectionList



LiteLLMConnectionList contains a list of LiteLLMConnection.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `LiteLLMConnectionList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[LiteLLMConnection](#litellmconnection) array_ |  |  |  |


#### LiteLLMConnectionSpec



LiteLLMConnectionSpec defines the desired LiteLLM endpoint consumed by ACH.



_Appears in:_
- [LiteLLMConnection](#litellmconnection)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `endpoint` _string_ | Endpoint is the base URL of the LiteLLM instance. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `masterKeySecretRef` _[SecretKeyRef](#secretkeyref)_ | MasterKeySecretRef points to the Secret key that carries the LiteLLM<br />master key. The Secret must live in the same namespace as the CR. |  | Required: \{\} <br /> |


#### LiteLLMConnectionStatus



LiteLLMConnectionStatus defines the observed LiteLLM connection state.



_Appears in:_
- [LiteLLMConnection](#litellmconnection)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ |  |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ |  |  |  |


#### MarketplaceFilters



MarketplaceFilters narrows the upstream marketplace catalog to a curated
subset via anchored RE2 regex include/exclude patterns (Hub §12).

  - filters.include OPTIONAL: when set, only names matched by at least one
    anchored include pattern survive. When absent (or empty), all upstream
    names pass through.
  - filters.exclude OPTIONAL: when set, names matched by any anchored exclude
    pattern are dropped AFTER include. exclude wins on conflict.

CRD admission catches obviously-empty entries; full RE2 compile validation
runs at reconcile (Synced=False, reason=InvalidConfig on failure).



_Appears in:_
- [PluginMarketplaceSpec](#pluginmarketplacespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `include` _string array_ | Include is a list of anchored RE2 patterns that narrow the catalog.<br />When omitted or empty, all upstream names pass through. |  |  |
| `exclude` _string array_ | Exclude is a list of anchored RE2 patterns dropped from the catalog<br />after Include is applied. |  |  |


#### MarketplacePluginRef



MarketplacePluginRef is the per-plugin entry surfaced on
PluginMarketplace status — operators reading the CR need at-a-glance
visibility into which plugin names the most recent reconcile
materialized AND the upstream revision they pin against.



_Appears in:_
- [PluginMarketplaceStatus](#pluginmarketplacestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the plugin's identifier within the catalog<br />(marketplace.json plugins[].name). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the resolved revision the materialized tarball<br />was fetched at — a 40-hex commit SHA for git-backed sources, an<br />S3 ETag for S3, a generation for GCS, an ETag\|Last-Modified<br />composite for HTTP. Empty only when the upstream fetcher did not<br />report a revision for this entry. |  |  |


#### Plugin



Plugin is the Schema for the plugins API (Hub §11).



_Appears in:_
- [PluginList](#pluginlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `Plugin` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[PluginSpec](#pluginspec)_ |  |  |  |
| `status` _[PluginStatus](#pluginstatus)_ |  |  |  |


#### PluginList



PluginList contains a list of Plugin.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `PluginList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[Plugin](#plugin) array_ |  |  |  |


#### PluginMarketplace



PluginMarketplace is the Schema for the pluginmarketplaces API (Hub §12).



_Appears in:_
- [PluginMarketplaceList](#pluginmarketplacelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `PluginMarketplace` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[PluginMarketplaceSpec](#pluginmarketplacespec)_ |  |  |  |
| `status` _[PluginMarketplaceStatus](#pluginmarketplacestatus)_ |  |  |  |


#### PluginMarketplaceList



PluginMarketplaceList contains a list of PluginMarketplace.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `PluginMarketplaceList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[PluginMarketplace](#pluginmarketplace) array_ |  |  |  |


#### PluginMarketplaceSpec



PluginMarketplaceSpec defines the desired state of PluginMarketplace (Hub §12).

The marketplace file is fetched via the chosen source type (§12.1).
Body handling depends on the type:

  - github / gitlab / bitbucket: the fetcher returns the full repo
    tarball (Hub §10.1, Path-subset extraction deferred to v1beta1).
    Stage-1 walks the tarball and extracts the first regular file
    whose path ends with `/.claude-plugin/marketplace.json`. spec.<type>.path
    is IGNORED for marketplaces (the file location is conventional).
  - s3 / gcs / http: spec.<type>.key/object/url MUST point at the
    marketplace.json body directly; the fetcher returns that body
    verbatim with no extraction.

CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
CRD-04: spec.refresh.maxStaleness is REQUIRED.



_Appears in:_
- [PluginMarketplace](#pluginmarketplace)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type names the upstream source kind for the marketplace file. |  | Enum: [github gitlab bitbucket s3 gcs http] <br />Required: \{\} <br /> |
| `refresh` _[RefreshBlock](#refreshblock)_ | Refresh declares poll cadence and staleness bound (CRD-04). |  | Required: \{\} <br /> |
| `filters` _[MarketplaceFilters](#marketplacefilters)_ | Filters narrows the upstream catalog (Hub §12). Optional. |  |  |
| `github` _[GitHubSource](#githubsource)_ | GitHub source. Required when spec.type == "github". |  |  |
| `gitlab` _[GitLabSource](#gitlabsource)_ | GitLab source. Required when spec.type == "gitlab". |  |  |
| `bitbucket` _[BitbucketSource](#bitbucketsource)_ | Bitbucket source. Required when spec.type == "bitbucket". |  |  |
| `s3` _[S3Source](#s3source)_ | S3 source. Required when spec.type == "s3". |  |  |
| `gcs` _[GCSSource](#gcssource)_ | GCS source. Required when spec.type == "gcs". |  |  |
| `http` _[HTTPSource](#httpsource)_ | HTTP source. Required when spec.type == "http". |  |  |


#### PluginMarketplaceStatus



PluginMarketplaceStatus defines the observed state of PluginMarketplace.

In addition to the shared ExternalRefStatus, PluginMarketplace exposes a
Synced condition (§6.6) with reasons NameConflict, UpstreamInvalid,
InvalidConfig, UnsupportedPluginSource, plus the materialized plugin set
(Plugins / PluginsCount) populated on each successful reconcile.



_Appears in:_
- [PluginMarketplace](#pluginmarketplace)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the<br />reconciler most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions exposes SourceReachable (and, for PluginMarketplace,<br />Synced) per §6.6. |  |  |
| `storageLocation` _string_ | StorageLocation is the cached filesystem path the Content Service<br />serves from after the last successful refresh (§10.3). Empty until<br />the first successful refresh. |  |  |
| `lastSuccessfulRefresh` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | LastSuccessfulRefresh is the wall-clock time of the most recent<br />successful upstream fetch + atomic publish (§10.3 step 5). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the per-source revision identifier the most recent<br />successful refresh recorded — for git sources this is the resolved<br />commit SHA; for S3 it is the object ETag; for GCS the object<br />generation; for HTTP a composite of ETag and Last-Modified<br />separated by a literal pipe. The Phase 2 reconciler reads this<br />value to pass as PriorRev on the next fetch for conditional-GET /<br />not-modified detection. Empty before the first successful refresh. |  |  |
| `plugins` _[MarketplacePluginRef](#marketplacepluginref) array_ | Plugins lists the entries in the upstream catalog that the most<br />recent reconcile successfully materialized into marketplace_plugins<br />(+ the per-marketplace cache). Ordered by Name. Entries that<br />failed Stage-2 are NOT included here — those surface in the<br />Synced condition's message field (`stage-2: <N> plugin(s)<br />failed: ...`). Empty before the first successful reconcile. |  |  |
| `pluginsCount` _integer_ | PluginsCount is the size of Plugins, denormalized so the<br />kubectl print column can show it without a JSONPath length()<br />expression. Equal to len(Plugins). |  |  |


#### PluginSpec



PluginSpec defines the desired state of Plugin (Hub §11).

A Plugin references an upstream location whose subtree contains a
Claude Code plugin tree (root directory with .claude-plugin/plugin.json
and component directories). ACH fetches the subtree and serves it as
a .tar.gz archive (CRD-04, CRD-03, Hub §11).

CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
CRD-04: spec.refresh.maxStaleness is REQUIRED; spec.refresh.interval,
when set, MUST NOT exceed spec.refresh.maxStaleness.



_Appears in:_
- [Plugin](#plugin)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type names the upstream source kind. Drives which one of the<br />type-specific subobjects below is required. |  | Enum: [github gitlab bitbucket s3 gcs http] <br />Required: \{\} <br /> |
| `refresh` _[RefreshBlock](#refreshblock)_ | Refresh declares poll cadence and staleness bound (CRD-04). |  | Required: \{\} <br /> |
| `github` _[GitHubSource](#githubsource)_ | GitHub source. Required when spec.type == "github". |  |  |
| `gitlab` _[GitLabSource](#gitlabsource)_ | GitLab source. Required when spec.type == "gitlab". |  |  |
| `bitbucket` _[BitbucketSource](#bitbucketsource)_ | Bitbucket source. Required when spec.type == "bitbucket". |  |  |
| `s3` _[S3Source](#s3source)_ | S3 source. Required when spec.type == "s3". |  |  |
| `gcs` _[GCSSource](#gcssource)_ | GCS source. Required when spec.type == "gcs". |  |  |
| `http` _[HTTPSource](#httpsource)_ | HTTP source. Required when spec.type == "http". |  |  |


#### PluginStatus



PluginStatus defines the observed state of Plugin.



_Appears in:_
- [Plugin](#plugin)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the<br />reconciler most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions exposes SourceReachable (and, for PluginMarketplace,<br />Synced) per §6.6. |  |  |
| `storageLocation` _string_ | StorageLocation is the cached filesystem path the Content Service<br />serves from after the last successful refresh (§10.3). Empty until<br />the first successful refresh. |  |  |
| `lastSuccessfulRefresh` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | LastSuccessfulRefresh is the wall-clock time of the most recent<br />successful upstream fetch + atomic publish (§10.3 step 5). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the per-source revision identifier the most recent<br />successful refresh recorded — for git sources this is the resolved<br />commit SHA; for S3 it is the object ETag; for GCS the object<br />generation; for HTTP a composite of ETag and Last-Modified<br />separated by a literal pipe. The Phase 2 reconciler reads this<br />value to pass as PriorRev on the next fetch for conditional-GET /<br />not-modified detection. Empty before the first successful refresh. |  |  |


#### Prompt



Prompt is the Schema for the prompts API (Hub §14).



_Appears in:_
- [PromptList](#promptlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `Prompt` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[PromptSpec](#promptspec)_ |  |  |  |
| `status` _[PromptStatus](#promptstatus)_ |  |  |  |


#### PromptList



PromptList contains a list of Prompt.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `PromptList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[Prompt](#prompt) array_ |  |  |  |


#### PromptSpec



PromptSpec defines the desired state of Prompt (Hub §14).

A Prompt references a single upstream file served by ACH Content Service.
CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
CRD-04: spec.refresh.maxStaleness is REQUIRED.



_Appears in:_
- [Prompt](#prompt)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type names the upstream source kind. |  | Enum: [github gitlab bitbucket s3 gcs http] <br />Required: \{\} <br /> |
| `refresh` _[RefreshBlock](#refreshblock)_ | Refresh declares poll cadence and staleness bound (CRD-04). |  | Required: \{\} <br /> |
| `contentType` _string_ | ContentType is the HTTP Content-Type the Content Service returns for<br />this prompt. Optional; when empty, Content Service falls back to the<br />upstream response's Content-Type or application/octet-stream<br />(Hub §14). |  |  |
| `github` _[GitHubSource](#githubsource)_ | GitHub source. Required when spec.type == "github". |  |  |
| `gitlab` _[GitLabSource](#gitlabsource)_ | GitLab source. Required when spec.type == "gitlab". |  |  |
| `bitbucket` _[BitbucketSource](#bitbucketsource)_ | Bitbucket source. Required when spec.type == "bitbucket". |  |  |
| `s3` _[S3Source](#s3source)_ | S3 source. Required when spec.type == "s3". |  |  |
| `gcs` _[GCSSource](#gcssource)_ | GCS source. Required when spec.type == "gcs". |  |  |
| `http` _[HTTPSource](#httpsource)_ | HTTP source. Required when spec.type == "http". |  |  |


#### PromptStatus



PromptStatus defines the observed state of Prompt.



_Appears in:_
- [Prompt](#prompt)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the<br />reconciler most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions exposes SourceReachable (and, for PluginMarketplace,<br />Synced) per §6.6. |  |  |
| `storageLocation` _string_ | StorageLocation is the cached filesystem path the Content Service<br />serves from after the last successful refresh (§10.3). Empty until<br />the first successful refresh. |  |  |
| `lastSuccessfulRefresh` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | LastSuccessfulRefresh is the wall-clock time of the most recent<br />successful upstream fetch + atomic publish (§10.3 step 5). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the per-source revision identifier the most recent<br />successful refresh recorded — for git sources this is the resolved<br />commit SHA; for S3 it is the object ETag; for GCS the object<br />generation; for HTTP a composite of ETag and Last-Modified<br />separated by a literal pipe. The Phase 2 reconciler reads this<br />value to pass as PriorRev on the next fetch for conditional-GET /<br />not-modified detection. Empty before the first successful refresh. |  |  |


#### RefreshBlock



RefreshBlock declares how often an external-reference resource is polled
upstream and how stale a cached snapshot may be before content delivery
is refused (Hub §10).

CRD-04: maxStaleness is REQUIRED on every Plugin/PluginMarketplace/
Prompt/Artifact. CRD-03: interval, when set, MUST NOT exceed maxStaleness;
a resource with interval > maxStaleness would always be stale between
refresh attempts.



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `interval` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#duration-v1-meta)_ | Interval is how often the ACH Operator polls the upstream source.<br />Optional; when unset, the Operator uses an implementation default.<br />Format matches Kubernetes Duration (e.g. "15m", "1h", "30s"). |  |  |
| `maxStaleness` _[Duration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#duration-v1-meta)_ | MaxStaleness bounds the age of a served cached snapshot. When<br />now - lastSuccessfulRefresh > maxStaleness, Content Service<br />returns 503 stale_cache_expired for the affected content<br />(§10). REQUIRED per CRD-04 — admission rejects a resource that<br />omits this field. |  | Required: \{\} <br /> |


#### RuntimeBlock



RuntimeBlock is the execution-resource half of an Environment (Hub §6, §6.1).
Names resolve against LiteLLM's runtime state (§17) and are projected by
the ACH Operator into the LiteLLM access group <environment>.

CRD-02: both runtime and context blocks are always present in the hydrate
response, even when one is empty. The list-element fields default to []
so a manifest omitting a sub-field still surfaces an empty slice.



_Appears in:_
- [EnvironmentSpec](#environmentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `models` _string array_ | Models lists LiteLLM model names (model_name) included in this Environment.<br />Names are projected into LiteLLM API URLs (the access-group sync path);<br />the looser runtime deny-pattern admits provider-prefixed ("openai/gpt-4")<br />and tagged ("gpt-4o:latest") names while forbidding the URL-injection<br />metacharacters ? # % plus whitespace and control chars (S2 defense-in-depth). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^?#%\s\x00-\x1f]+$ <br /> |
| `mcpServers` _string array_ | MCPServers lists LiteLLM MCP server names (server_name). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^?#%\s\x00-\x1f]+$ <br /> |
| `a2aAgents` _string array_ | A2AAgents lists LiteLLM A2A agent names (agent_name). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^?#%\s\x00-\x1f]+$ <br /> |


#### S3Source



S3Source describes an S3-compatible object store upstream (Hub §10.1).
No ref field — refresh polls the object's ETag (single key) or the
prefix listing (directory scope).



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `bucket` _string_ | Bucket name. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `key` _string_ | Key is the object key (single object) or prefix (directory scope). |  | MinLength: 1 <br />Required: \{\} <br /> |
| `region` _string_ | Region of the bucket. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `endpoint` _string_ | Endpoint for S3-compatible storage. Optional; defaults to AWS S3<br />when empty. |  |  |
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef points at the Secret carrying access-key-id and<br />secret-access-key (data keys named via accessKeyIdKey /<br />secretAccessKeyKey). |  | Required: \{\} <br /> |


#### SecretKeyRef



SecretKeyRef identifies a key in a same-namespace Secret.



_Appears in:_
- [LiteLLMConnectionSpec](#litellmconnectionspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the Kubernetes Secret name. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `key` _string_ | Key is the data key inside the Secret. |  | MinLength: 1 <br />Required: \{\} <br /> |


#### SourceAuthSecretRef



SourceAuthSecretRef references a Kubernetes Secret carrying credentials
for fetching an upstream source. The Secret MUST live in the same
namespace as the referring CR (no cross-namespace resolution in
v1alpha1).



_Appears in:_
- [BitbucketSource](#bitbucketsource)
- [GCSSource](#gcssource)
- [GitHubSource](#githubsource)
- [GitLabSource](#gitlabsource)
- [HTTPSource](#httpsource)
- [S3Source](#s3source)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name of the Kubernetes Secret in the CR's namespace. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `key` _string_ | Key is the name of the Secret data key holding the bearer token.<br />Optional; when omitted on a git source type the operator falls<br />back to a provider-specific default key name:<br />  - github     → GITHUB_TOKEN<br />  - gitlab     → GITLAB_TOKEN<br />  - bitbucket  → BITBUCKET_TOKEN<br />(Matches the ecosystem env-var convention used by gh, glab,<br />terraform-provider-*, gitlab-runner, etc.) Other source types<br />(s3 / gcs / http) carry their own per-type key fields and do<br />NOT use this fallback. |  | MinLength: 1 <br /> |
| `accessKeyIdKey` _string_ | AccessKeyIDKey is the data-key holding the AWS access-key-id<br />(S3 source only). |  | MinLength: 1 <br /> |
| `secretAccessKeyKey` _string_ | SecretAccessKeyKey is the data-key holding the AWS secret-access-key<br />(S3 source only). |  | MinLength: 1 <br /> |
| `headerName` _string_ | HeaderName is the HTTP header name to attach for http sources<br />(e.g. "Authorization"). |  | MinLength: 1 <br /> |
| `headerValueKey` _string_ | HeaderValueKey is the data-key holding the value for HeaderName<br />(http source only). |  | MinLength: 1 <br /> |


#### UnresolvedRuntime



UnresolvedRuntime mirrors the three runtime reference lists (§6.4) and
names the specific entries that did not resolve against LiteLLM.



_Appears in:_
- [EnvironmentStatus](#environmentstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `models` _string array_ |  | \{  \} |  |
| `mcpServers` _string array_ |  | \{  \} |  |
| `a2aAgents` _string array_ |  | \{  \} |  |


