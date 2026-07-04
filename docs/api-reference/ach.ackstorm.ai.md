# API Reference

## Packages
- [ach.ackstorm.ai/v1alpha1](#achackstormaiv1alpha1)


## ach.ackstorm.ai/v1alpha1

Package v1alpha1 contains API Schema definitions for the ach v1alpha1 API group.

### Resource Types
- [ACHAgent](#achagent)
- [ACHAgentList](#achagentlist)
- [AgentProfile](#agentprofile)
- [AgentProfileList](#agentprofilelist)
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
- [Skill](#skill)
- [SkillList](#skilllist)
- [SkillMarketplace](#skillmarketplace)
- [SkillMarketplaceList](#skillmarketplacelist)



#### A2AAuthSpec



A2AAuthSpec configures a2a inbound auth (config: channels[].a2a.auth; secretRef → secretPath).



_Appears in:_
- [A2ASpec](#a2aspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `header` _string_ |  | x-a2a-custom-api-key |  |
| `secretRef` _[SecretKeyRef](#secretkeyref)_ |  |  | Required: \{\} <br /> |


#### A2ASpec



A2ASpec configures an a2a channel (config: channels[].a2a; mode async-only in v1).



_Appears in:_
- [ChannelSpec](#channelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `auth` _[A2AAuthSpec](#a2aauthspec)_ |  |  | Required: \{\} <br /> |


#### ACHAgent



ACHAgent is a running agent instance.



_Appears in:_
- [ACHAgentList](#achagentlist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `ACHAgent` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ACHAgentSpec](#achagentspec)_ |  |  |  |
| `status` _[ACHAgentStatus](#achagentstatus)_ |  |  |  |


#### ACHAgentList



ACHAgentList contains a list of ACHAgent.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `ACHAgentList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[ACHAgent](#achagent) array_ |  |  |  |


#### ACHAgentSpec



ACHAgentSpec defines the desired state of an agent instance.



_Appears in:_
- [ACHAgent](#achagent)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `profileRef` _[LocalObjectRef](#localobjectref)_ |  |  | Required: \{\} <br /> |
| `identity` _[IdentitySpec](#identityspec)_ |  |  | Required: \{\} <br /> |
| `capability` _[CapabilitySpec](#capabilityspec)_ |  |  | Required: \{\} <br /> |
| `model` _[ModelSpec](#modelspec)_ | Model overrides the profile's default model. |  |  |
| `limits` _[LimitsSpec](#limitsspec)_ | Limits overrides the profile's default limits. |  |  |
| `prompt` _[AgentPromptSpec](#agentpromptspec)_ |  |  |  |
| `memory` _[MemorySpec](#memoryspec)_ |  |  |  |
| `expose` _[ExposeSpec](#exposespec)_ | Expose controls reachability (Service + gateway route). Omit for a fully<br />private agent (no Service, no public URL). |  |  |
| `channels` _[ChannelSpec](#channelspec) array_ |  |  | MinItems: 1 <br />Required: \{\} <br /> |


#### ACHAgentStatus



ACHAgentStatus is the observed state.



_Appears in:_
- [ACHAgent](#achagent)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ |  |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ |  |  |  |
| `gatewayURL` _string_ | GatewayURL is the inbound base URL for this agent on the shared gateway,<br />e.g. https://ach.example.com/agents/ach-system/achagent-gh. The last<br />segment is the agent's Service name; the gateway forwards anything<br />after it verbatim to that Service (append the harness route you need,<br />e.g. /channels/\{name\}/events for a webhook channel, or the a2a path).<br />Set only when the agent opts into gateway exposure (expose.gateway).<br />The host segment is only populated when the operator has<br />ACH_PUBLIC_BASE_URL (or, as a fallback, ACH_BASE_URL) configured;<br />otherwise this is the path-only form for the caller to prefix with<br />their own ingress host. |  |  |


#### AchEndpointSpec



AchEndpointSpec is the ACH platform coordinate (config: capability.ach.baseUrl + ACH_BASE_URL env).



_Appears in:_
- [AgentProfileSpec](#agentprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `baseUrl` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |


#### AgentProfile



AgentProfile is the reusable infra + defaults for a class of agents.



_Appears in:_
- [AgentProfileList](#agentprofilelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `AgentProfile` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[AgentProfileSpec](#agentprofilespec)_ |  |  |  |
| `status` _[AgentProfileStatus](#agentprofilestatus)_ |  |  |  |


#### AgentProfileList



AgentProfileList contains a list of AgentProfile.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `AgentProfileList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[AgentProfile](#agentprofile) array_ |  |  |  |


#### AgentProfileSpec



AgentProfileSpec is the reusable infra + defaults half.



_Appears in:_
- [AgentProfile](#agentprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#localobjectreference-v1-core) array_ |  |  |  |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#resourcerequirements-v1-core)_ |  |  |  |
| `extraEnv` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#envvar-v1-core) array_ | ExtraEnv are additional pod-level env vars (e.g. HTTPS_PROXY). Reserved ACH_* names are<br />forbidden — the operator owns them (the ek arrives via identity.secretRef as ACH_TOKEN). |  |  |
| `nodeSelector` _object (keys:string, values:string)_ |  |  |  |
| `tolerations` _[Toleration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#toleration-v1-core) array_ |  |  |  |
| `ach` _[AchEndpointSpec](#achendpointspec)_ |  |  | Required: \{\} <br /> |
| `model` _[ModelSpec](#modelspec)_ |  |  |  |
| `engine` _[EngineSpec](#enginespec)_ |  |  |  |
| `limits` _[LimitsSpec](#limitsspec)_ |  |  |  |
| `health` _[HealthSpec](#healthspec)_ |  |  |  |
| `persistence` _[PersistenceSpec](#persistencespec)_ |  |  |  |
| `terminationGracePeriodSeconds` _integer_ |  |  | Minimum: 0 <br /> |
| `podTemplate` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#json-v1-apiextensions-k8s-io)_ | PodTemplate is a raw strategic-merge-patch overlay applied over the operator-rendered pod<br />template (containers/env/volumes merge by name, scalars user-wins). Pass-through by design<br />(ponytail: no field guardrails — the profile author already controls spec.image, i.e.<br />everything that runs in the pod). A malformed overlay surfaces as WorkloadApplied=False<br />(PodTemplateInvalid); a merged-but-broken pod surfaces as a failing rollout. Note the<br />extraEnv ACH_* CEL guard does NOT inspect this overlay. After the merge the operator<br />re-pins the selector label and the config-hash annotation. |  |  |


#### AgentProfileStatus



AgentProfileStatus is minimal — profiles are read by ACHAgent; they have no side effects.



_Appears in:_
- [AgentProfile](#agentprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ |  |  |  |


#### AgentPromptSpec



AgentPromptSpec configures the system prompt (config: prompt).



_Appears in:_
- [ACHAgentSpec](#achagentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `system` _[PromptSystemSpec](#promptsystemspec)_ |  |  | Required: \{\} <br /> |
| `compose` _string_ |  | append | Enum: [replace append] <br /> |


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
| `name` _string_ | Name is the bare route segment the Forwarder sees as <name> in<br />/mcp/<name> or /a2a/<name>. MUST satisfy DNS-1123 subdomain rules<br />(≤253 chars, `[a-z0-9]([-a-z0-9.]*[a-z0-9])?`). Pattern enforced<br />per CRD-08 / Hub §9.3. |  | MaxLength: 253 <br />MinLength: 1 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$` <br />Required: \{\} <br /> |


#### BitbucketSource



BitbucketSource describes a bitbucket-hosted upstream (Hub §10.1).



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)
- [SkillMarketplaceSpec](#skillmarketplacespec)
- [SkillSpec](#skillspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `workspace` _string_ | Workspace name on Bitbucket. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `repo` _string_ | Repo within the workspace. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `path` _string_ | Path within the repo, honored at fetch time (F1; see GitHubSource.Path<br />for the directory-vs-file + marketplace semantics). |  |  |
| `ref` _string_ | Ref is a branch or tag name. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef is optional. When set, the Secret named here MUST<br />exist in the CR's namespace at reconcile time and the operator<br />reads the bearer token from the named key (see SourceAuthSecretRef.Key).<br />When nil, the upstream fetch is anonymous — supported only for<br />public repositories on the git transport (transport=rest paired<br />with no auth typically fails because most Bitbucket Cloud REST<br />endpoints require auth even for public repos). Bitbucket Cloud<br />anonymous REST quota: 60 req/h/IP. |  |  |
| `transport` _string_ | Transport selects the wire protocol used to fetch from this upstream.<br />  "git"  — use git ls-remote + git clone (no per-IP REST rate-limit;<br />           recommended; default).<br />  "rest" — use the provider's REST API. Subject to per-IP anonymous<br />           quotas (GitHub: 60/h; GitLab: 60/min; Bitbucket: 60/h).<br />           Retained as a one-release escape hatch; will be removed. | git | Enum: [git rest] <br /> |


#### CapabilitySpec



CapabilitySpec is the per-agent capability block (config: capability{type:ach,ach.environment,filter}).



_Appears in:_
- [ACHAgentSpec](#achagentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `environment` _string_ | Environment is the ACH Hub Environment name, for documentation/intent only.<br />The ek already scopes the environment server-side, and the harness reads the<br />hydrated environment (manifest.environment) — NOT this field. Optional: omit<br />to let the ek decide; when set it is rendered but never authoritative. |  |  |
| `filter` _[FilterSpec](#filterspec)_ |  |  |  |


#### ChannelSpec



ChannelSpec is one inbound channel (config: channels[]).



_Appears in:_
- [ACHAgentSpec](#achagentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `type` _string_ |  |  | Enum: [webhook cron queue a2a] <br />Required: \{\} <br /> |
| `source` _string_ |  |  | Enum: [gitlab github generic] <br /> |
| `concurrency` _integer_ |  | 1 | Minimum: 1 <br /> |
| `session` _[SessionSpec](#sessionspec)_ |  |  |  |
| `prompt` _string_ |  |  |  |
| `webhook` _[WebhookSpec](#webhookspec)_ |  |  |  |
| `cron` _[CronSpec](#cronspec)_ |  |  |  |
| `queue` _[QueueSpec](#queuespec)_ |  |  |  |
| `a2a` _[A2ASpec](#a2aspec)_ |  |  |  |


#### CodememSpec



CodememSpec is the codemem memory backend (config: memory.codemem). All fields optional.



_Appears in:_
- [MemorySpec](#memoryspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `dbPath` _string_ |  |  |  |
| `project` _string_ |  |  |  |


#### ContextBlock



ContextBlock is the content-resource half of an Environment (Hub §6, §6.1).
Names reference ACH-owned content objects (Prompt, Plugin, Artifact CRDs
or marketplace_plugins rows) and are served by the ACH Content Service.



_Appears in:_
- [EnvironmentSpec](#environmentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `prompts` _string array_ | Prompts lists referenced Prompt names. Context names map to content<br />filenames served by the Content Service, so the strict deny-pattern<br />forbids "/" and "\" (path-traversal) in addition to ? # % whitespace,<br />control chars (U+0000-U+001F), and DEL (U+007F) (S2 defense-in-depth). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f\x7f]+$ <br /> |
| `plugins` _string array_ | Plugins lists referenced plugin names. A bare name ("code-review")<br />resolves to an internal Plugin CRD; a scoped name<br />("code-review@anthropics-official") resolves to that<br />PluginMarketplace's plugin by exact (marketplace, name). Same strict<br />deny-pattern as Prompts (no "/" "\" ? # % whitespace, control chars<br />or DEL); "@" is permitted as the marketplace separator. | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f\x7f]+$ <br /> |
| `artifacts` _string array_ | Artifacts lists referenced Artifact names.<br />Same strict deny-pattern as Prompts (no "/" "\" ? # % whitespace<br />control chars or DEL). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f\x7f]+$ <br /> |
| `skills` _string array_ | Skills lists referenced Skill names. A bare name ("pdf-processing")<br />resolves to a Skill CR in the operator namespace; a scoped<br />"name@marketplace" ("branding@ackstorm") resolves a skill discovered<br />inside a SkillMarketplace (the final "@" separates name from marketplace,<br />mirroring context.plugins). Content-gated like Plugins. Same strict<br />deny-pattern as Plugins (no "/" "\" ? # % whitespace, control chars or<br />DEL). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f\x7f]+$ <br /> |


#### CronSpec



CronSpec configures a cron channel (config: channels[].cron).



_Appears in:_
- [ChannelSpec](#channelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `schedule` _string_ |  |  | Required: \{\} <br /> |
| `timezone` _string_ |  | UTC |  |


#### EngineSpec



EngineSpec is the harness-local engine block (config: engine.*). Unset fields are omitted
(the harness defaults them).



_Appears in:_
- [AgentProfileSpec](#agentprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `home` _string_ |  |  |  |
| `workDir` _string_ |  |  |  |
| `forwardEnv` _string array_ |  |  |  |
| `idleTtlSeconds` _integer_ |  |  | Minimum: 0 <br /> |
| `startupTimeoutSeconds` _integer_ |  |  | Minimum: 1 <br /> |
| `maxToolCalls` _integer_ |  |  | Minimum: 0 <br /> |


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
| `runtime` _[RuntimeBlock](#runtimeblock)_ | Runtime is the execution-resource bundle projected into the LiteLLM<br />access group (§6.2). Optional in the manifest; defaults to an empty<br />block whose list fields backfill to [] so CRD-02 ("always present in<br />the hydrate response") holds even when the author omits it. | \{  \} |  |
| `context` _[ContextBlock](#contextblock)_ | Context is the content-resource bundle served by Content Service<br />(§10, §15.6). Optional in the manifest; defaults to an empty block<br />whose list fields backfill to [] so CRD-02 ("always present in the<br />hydrate response") holds even when the author omits it. | \{  \} |  |
| `authorizedTeams` _string array_ | AuthorizedTeams references LiteLLM Team aliases (§6.1). The Environment<br />is unusable when no entry resolves to an existing LiteLLM Team;<br />admission requires at least one entry per Hub §6 (informational —<br />reconcile-time existence is verified per §6.4). |  | MinItems: 1 <br />Required: \{\} <br /> |
| `notice` _string_ | Notice is an optional free-text advisory shown to the user ONLY after<br />`ach-cli env hydrate` (it is not surfaced in `env list` / `env describe`<br />— use Description for catalog metadata). Use it for operational reminders<br />("re-login after key rotation") or model guidance ("works best with the<br />openai-* models"). Plain text, not interpreted; empty renders nothing. |  | MaxLength: 2048 <br /> |
| `description` _string_ | Description is optional catalog metadata describing what this Environment<br />is. It is surfaced in `ach-cli env list` (truncated) and `env describe`<br />(full) — the browse-time "what is this" text, distinct from Notice's<br />post-hydrate advisory. Plain text, not interpreted; empty renders nothing. |  | MaxLength: 2048 <br /> |


#### EnvironmentStatus



EnvironmentStatus defines the observed state of Environment (Hub §6.4, §6.6).



_Appears in:_
- [Environment](#environment)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the reconciler<br />most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions carries Environment condition types per §6.6 closed set:<br />Available, ContentReady, ExecutionResourcesResolved, AccessGroupSynced. |  |  |
| `unresolvedRuntime` _[UnresolvedRuntime](#unresolvedruntime)_ | UnresolvedRuntime lists runtime references not currently registered in<br />LiteLLM. Surfaced for `kubectl describe environment` per §6.4. The<br />field contract belongs here from Phase 1; the reconciler in Phase 2<br />rewrites it on every reconcile. |  |  |
| `unresolvedContextPlugins` _string array_ | UnresolvedContextPlugins lists context.plugins entries whose content<br />has not yet been synced (last_successful_refresh IS NULL in the<br />plugins or marketplace_plugins projection row). A non-empty list<br />blocks ExecutionResourcesResolved from becoming True — prevents the<br />Available=True false-green that would otherwise let hydrate issue a<br />404 at runtime.<br />Bare entries (no @) resolve against the plugins (CRD) projection<br />table; scoped entries (name@marketplace) resolve against<br />marketplace_plugins. Both arms require last_successful_refresh to<br />be non-null before the plugin is considered content-present. | \{  \} |  |
| `unresolvedContextSkills` _string array_ | UnresolvedContextSkills lists context.skills entries whose content has<br />not yet been synced (last_successful_refresh IS NULL in the skills<br />projection row). A non-empty list blocks ExecutionResourcesResolved<br />from becoming True — same content-gating as UnresolvedContextPlugins. | \{  \} |  |
| `litellmAccessGroup` _string_ | LitellmAccessGroup is the synced LiteLLM access group name (§6.4).<br />Echoed for operator visibility; equals metadata.name when set. |  |  |


#### ExcludeSpec



ExcludeSpec is the governance gate ABOVE the model (config: capability.filter.exclude).



_Appears in:_
- [FilterSpec](#filterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `tools` _string array_ |  |  |  |
| `mcpServers` _string array_ |  |  |  |
| `skills` _string array_ |  |  |  |


#### ExposeSpec



ExposeSpec controls how an agent is reachable. Both axes default false —
an agent is fully private (harness Pod only, no Service, no public route)
unless it explicitly opts in. gateway requires service (the gateway proxies
to the Service; there is nothing to route to without it).



_Appears in:_
- [ACHAgentSpec](#achagentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `service` _boolean_ | Service creates the ClusterIP Service (achagent-<name>) so in-cluster<br />peers (a2a) or your own ingress can reach the harness. Required for any<br />inbound HTTP channel (webhook/a2a) to be reachable at all. |  |  |
| `gateway` _boolean_ | Gateway publishes the agent on the shared ACH gateway<br />(/agents/\{ns\}/\{service\} route + status.gatewayURL). Requires service. |  |  |


#### ExternalRefStatus



ExternalRefStatus is the shared status surface for external-reference
resources (Plugin, Prompt, Artifact, PluginMarketplace) per Hub §6.6.



_Appears in:_
- [ArtifactStatus](#artifactstatus)
- [PluginMarketplaceStatus](#pluginmarketplacestatus)
- [PluginStatus](#pluginstatus)
- [PromptStatus](#promptstatus)
- [SkillMarketplaceStatus](#skillmarketplacestatus)
- [SkillStatus](#skillstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the<br />reconciler most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions exposes SourceReachable (and, for PluginMarketplace,<br />Synced) per §6.6. |  |  |
| `storageLocation` _string_ | StorageLocation is the cached filesystem path the Content Service<br />serves from after the last successful refresh (§10.3). Empty until<br />the first successful refresh. |  |  |
| `lastSuccessfulRefresh` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | LastSuccessfulRefresh is the wall-clock time of the most recent<br />successful upstream fetch + atomic publish (§10.3 step 5). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the per-source revision identifier the most recent<br />successful refresh recorded — for git sources this is the resolved<br />commit SHA; for S3 it is the object ETag; for GCS the object<br />generation; for HTTP a composite of ETag and Last-Modified<br />separated by a literal pipe. The Phase 2 reconciler reads this<br />value to pass as PriorRev on the next fetch for conditional-GET /<br />not-modified detection. Empty before the first successful refresh. |  |  |


#### FilterSpec



FilterSpec wraps the exclude gate.



_Appears in:_
- [CapabilitySpec](#capabilityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `exclude` _[ExcludeSpec](#excludespec)_ |  |  |  |


#### GCSSource



GCSSource describes a Google Cloud Storage upstream (Hub §10.1).
No ref field — refresh polls the object's generation (single object)
or the prefix listing (directory scope).



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)
- [SkillMarketplaceSpec](#skillmarketplacespec)
- [SkillSpec](#skillspec)

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
- [SkillMarketplaceSpec](#skillmarketplacespec)
- [SkillSpec](#skillspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `repo` _string_ | Repo is the "<owner>/<name>" GitHub identifier. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `path` _string_ | Path within the repo, honored at fetch time (F1): a directory narrows the<br />fetched content to that subtree; a single file (Prompt, Artifact<br />scope=object) returns that file's raw bytes. Empty → whole repo.<br />PluginMarketplace and SkillMarketplace are DISCOVERY kinds and ignore this<br />as a fetch narrow — they fetch the whole repo (SkillMarketplace uses path<br />only as the post-fetch skills-root walk hint; PluginMarketplace discovers<br />.claude-plugin/marketplace.json conventionally). |  |  |
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
- [SkillMarketplaceSpec](#skillmarketplacespec)
- [SkillSpec](#skillspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `host` _string_ | Host of the GitLab instance. Accepts a bare host ("git.example.com")<br />or a full "https://git.example.com" form — the scheme is stripped and<br />the clone/REST URL is always built as https://<host>. Behaves<br />identically for every consumer (Artifact / Plugin / Prompt /<br />PluginMarketplace). Defaults to gitlab.com when empty. |  |  |
| `project` _string_ | Project is the "<namespace>/<project>" GitLab identifier. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `path` _string_ | Path within the project repo, honored at fetch time (F1; see<br />GitHubSource.Path for the directory-vs-file + marketplace semantics). |  |  |
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
- [SkillMarketplaceSpec](#skillmarketplacespec)
- [SkillSpec](#skillspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ | URL of the upstream resource. Accepts http:// or https://. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef optionally attaches an authentication header<br />(e.g. Authorization: Bearer ...). The data key named via<br />.headerValueKey supplies the header value at request time. |  |  |


#### HealthSpec



HealthSpec is the harness HTTP surface (config: health{host,port}). Also drives the
Service targetPort and the container probes. Harness default port is 8080.



_Appears in:_
- [AgentProfileSpec](#agentprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `host` _string_ |  |  |  |
| `port` _integer_ |  |  | Maximum: 65535 <br />Minimum: 1 <br /> |


#### HindsightSpec



HindsightSpec is the hindsight memory backend (config: memory.hindsight).



_Appears in:_
- [MemorySpec](#memoryspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `endpoint` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `bank` _string_ |  |  |  |
| `mentalModels` _string array_ |  |  |  |


#### IdentitySpec



IdentitySpec carries the ACH ek_ (config: injected as ACH_TOKEN env via secretKeyRef).



_Appears in:_
- [ACHAgentSpec](#achagentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `secretRef` _[SecretKeyRef](#secretkeyref)_ | SecretRef points at a Secret holding the ek_ (create it yourself, e.g. `ach-cli keys create`). |  | Required: \{\} <br /> |


#### LimitsSpec



LimitsSpec bounds invocations (config: limits.*). Unset → harness default.



_Appears in:_
- [ACHAgentSpec](#achagentspec)
- [AgentProfileSpec](#agentprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `maxConcurrentInvocations` _integer_ |  |  | Minimum: 1 <br /> |
| `maxInvocationSeconds` _integer_ |  |  | Minimum: 1 <br /> |
| `maxQueuedTotal` _integer_ |  |  | Minimum: 1 <br /> |
| `idempotencyWindowSeconds` _integer_ |  |  | Minimum: 1 <br /> |
| `maxSteps` _integer_ |  |  | Minimum: 1 <br /> |
| `terminalOutputRetries` _integer_ |  |  | Minimum: 0 <br /> |


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


#### LocalObjectRef



LocalObjectRef references a resource by name in the CR's namespace.



_Appears in:_
- [ACHAgentSpec](#achagentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |


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
- [SkillMarketplaceSpec](#skillmarketplacespec)

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


#### MemorySpec



MemorySpec is a discriminated memory backend (config: memory). Omit for no memory (fail-open).
Asymmetry is intentional and mirrors the schema: HindsightMemory REQUIRES the hindsight block
(endpoint has no default); CodememMemory requires only `type` — {"type":"codemem"} is valid
(dbPath/project are derived/defaulted by the harness).



_Appears in:_
- [ACHAgentSpec](#achagentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ |  |  | Enum: [hindsight codemem] <br />Required: \{\} <br /> |
| `hindsight` _[HindsightSpec](#hindsightspec)_ |  |  |  |
| `codemem` _[CodememSpec](#codememspec)_ |  |  |  |


#### ModelSpec



ModelSpec selects the ACH-served model (config: model{name,type,params}).



_Appears in:_
- [ACHAgentSpec](#achagentspec)
- [AgentProfileSpec](#agentprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `type` _string_ |  |  | Enum: [openai gemini anthropic] <br />Required: \{\} <br /> |
| `params` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#json-v1-apiextensions-k8s-io)_ | Params is an open, unvalidated dict splatted to the model client. |  |  |


#### PersistenceSpec



PersistenceSpec configures PVC-backed durable state (config: persistence{enabled,mountPath}).



_Appears in:_
- [AgentProfileSpec](#agentprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ |  |  | Required: \{\} <br /> |
| `size` _string_ |  |  |  |
| `storageClassName` _string_ |  |  |  |
| `mountPath` _string_ |  | /var/lib/ach-agent |  |
| `retainPolicy` _string_ | RetainPolicy controls PVC lifecycle on ACHAgent deletion. Retain → the PVC is created<br />WITHOUT a controller owner-ref, so it survives agent deletion (operator-managed cleanup). |  | Enum: [Retain Delete] <br /> |


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
Synced condition (§6.6) with reasons UpstreamInvalid, InvalidConfig,
UnsupportedPluginSource (plus per-plugin soft-skip reasons in the
message such as DuplicateName), plus the materialized plugin set
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


#### PromptSystemSpec



PromptSystemSpec is a discriminated persona source (config: prompt.system).
The ach form MAY carry an optional achFile (rendered as system.file — the schema's SystemAch
allows `file` as an optional subpath within the named prompt).



_Appears in:_
- [AgentPromptSpec](#agentpromptspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ |  |  | Enum: [text file ach] <br />Required: \{\} <br /> |
| `text` _string_ |  |  |  |
| `file` _string_ |  |  |  |
| `ach` _string_ |  |  |  |
| `achFile` _string_ | AchFile is an optional subpath within an `ach` prompt (rendered as prompt.system.file). |  |  |


#### QueueSpec



QueueSpec configures a redis queue channel (config: channels[].queue; type/ackMode are constants).



_Appears in:_
- [ChannelSpec](#channelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `key` _string_ |  |  | Required: \{\} <br /> |


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
- [SkillMarketplaceSpec](#skillmarketplacespec)
- [SkillSpec](#skillspec)

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
| `models` _string array_ | Models lists LiteLLM model names (model_name) included in this Environment.<br />Names are projected into LiteLLM API request bodies (not ACH URL routing),<br />so the looser deny-pattern admits provider-prefixed ("openai/gpt-4") and<br />tagged ("gpt-4o:latest") names while forbidding URL-injection metacharacters<br />? # % plus whitespace, control chars (U+0000-U+001F), and DEL (U+007F)<br />(S2 defense-in-depth). Only Models uses the loose pattern; see MCPServers<br />and A2AAgents below for why those must use the strict (no-slash) pattern. | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^?#%\s\x00-\x1f\x7f]+$ <br /> |
| `mcpServers` _string array_ | MCPServers lists LiteLLM MCP server names (server_name).<br />Names are used as chi route parameters at the forwarder (/mcp/\{name\});<br />a slash-containing name would be admitted but always 403 (chi matches<br />raw "%2F"-encoded segment against the decoded DB value — never matches).<br />The strict deny-pattern therefore also forbids "/" and "\" in addition<br />to ? # % whitespace, control chars (U+0000-U+001F), and DEL (U+007F)<br />(S2 defense-in-depth). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f\x7f]+$ <br /> |
| `a2aAgents` _string array_ | A2AAgents lists LiteLLM A2A agent names (agent_name).<br />Names are used as chi route parameters at the forwarder (/a2a/\{name\});<br />same routing constraint as MCPServers — slash-containing names always 403.<br />The strict deny-pattern forbids "/" and "\" in addition to ? # %<br />whitespace, control chars (U+0000-U+001F), and DEL (U+007F)<br />(S2 defense-in-depth). | \{  \} | items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f\x7f]+$ <br /> |


#### S3Source



S3Source describes an S3-compatible object store upstream (Hub §10.1).
No ref field — refresh polls the object's ETag (single key) or the
prefix listing (directory scope).



_Appears in:_
- [ArtifactSpec](#artifactspec)
- [PluginMarketplaceSpec](#pluginmarketplacespec)
- [PluginSpec](#pluginspec)
- [PromptSpec](#promptspec)
- [SkillMarketplaceSpec](#skillmarketplacespec)
- [SkillSpec](#skillspec)

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
- [A2AAuthSpec](#a2aauthspec)
- [IdentitySpec](#identityspec)
- [LiteLLMConnectionSpec](#litellmconnectionspec)
- [WebhookAuthSpec](#webhookauthspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the Kubernetes Secret name. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `key` _string_ | Key is the data key inside the Secret. |  | MinLength: 1 <br />Required: \{\} <br /> |


#### SessionSpec



SessionSpec selects which opencode conversation a channel turn reuses and
bounds its growth (config: channels[].session). type is the discriminator;
key is the {{ }} template, valid ONLY when type==custom. Omitting the whole
block lets the harness apply its own default (type: none). This changes only
which session a turn reuses — the router lane key (event.session_key) is
unaffected.



_Appears in:_
- [ChannelSpec](#channelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | none: fresh session per event, deleted post-turn. auto: reuse the<br />channel-derived session_key. custom: reuse the session named by key. | none | Enum: [auto none custom] <br /> |
| `key` _string_ | Key is the \{\{ \}\} session template (payload.* / internal.*). REQUIRED iff<br />type==custom, FORBIDDEN otherwise. An empty render falls back to none + WARN. |  |  |
| `maxTokens` _integer_ | MaxTokens caps growth: once the previous turn's input_tokens exceed it,<br />apply overflow (auto/custom only; ignored for none). |  | Minimum: 1 <br /> |
| `overflow` _string_ | Overflow: compact summarizes the session in place; rotate starts a fresh<br />session and deletes the old one. | compact | Enum: [compact rotate] <br /> |


#### Skill



Skill is the Schema for the skills API.



_Appears in:_
- [SkillList](#skilllist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `Skill` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SkillSpec](#skillspec)_ |  |  |  |
| `status` _[SkillStatus](#skillstatus)_ |  |  |  |


#### SkillList



SkillList contains a list of Skill.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `SkillList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[Skill](#skill) array_ |  |  |  |


#### SkillMarketplace



SkillMarketplace is the Schema for the skillmarketplaces API. One upstream
repo of agent skills (discovered by convention) → many skills.



_Appears in:_
- [SkillMarketplaceList](#skillmarketplacelist)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `SkillMarketplace` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[SkillMarketplaceSpec](#skillmarketplacespec)_ |  |  |  |
| `status` _[SkillMarketplaceStatus](#skillmarketplacestatus)_ |  |  |  |


#### SkillMarketplaceList



SkillMarketplaceList contains a list of SkillMarketplace.





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `ach.ackstorm.ai/v1alpha1` | | |
| `kind` _string_ | `SkillMarketplaceList` | | |
| `kind` _string_ | Kind is a string value representing the REST resource this object represents.<br />Servers may infer this from the endpoint the client submits requests to.<br />Cannot be updated.<br />In CamelCase.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds |  |  |
| `apiVersion` _string_ | APIVersion defines the versioned schema of this representation of an object.<br />Servers should convert recognized schemas to the latest internal value, and<br />may reject unrecognized values.<br />More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources |  |  |
| `metadata` _[ListMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#listmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `items` _[SkillMarketplace](#skillmarketplace) array_ |  |  |  |


#### SkillMarketplaceSkillRef



SkillMarketplaceSkillRef is the per-skill entry surfaced on
SkillMarketplace status — operators reading the CR need at-a-glance
visibility into which skill names the most recent reconcile materialized AND
the upstream revision they pin against.



_Appears in:_
- [SkillMarketplaceStatus](#skillmarketplacestatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | Name is the skill's identifier within the collection<br />(the SKILL.md frontmatter name == the top-level directory basename). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the resolved revision the materialized tarball<br />was fetched at — a 40-hex commit SHA for git-backed sources, an<br />S3 ETag for S3, a generation for GCS, an ETag\|Last-Modified<br />composite for HTTP. Empty only when the upstream fetcher did not<br />report a revision. |  |  |


#### SkillMarketplaceSpec



SkillMarketplaceSpec defines the desired state of SkillMarketplace.

A SkillMarketplace fetches ONE upstream repo subtree as a single tar.gz and
discovers many agent skills inside it by convention (agentskills.io has NO
marketplace.json index — unlike PluginMarketplace). Body handling depends on
the source type:

  - github / gitlab / bitbucket: the fetcher returns the repo archive
    (a tar.gz of the repo subtree). Stage-1 walks it for every top-level
    directory containing a valid SKILL.md (name == dir basename); the REST
    "<repo>-<sha>/" archive-root wrapper is stripped automatically.
  - s3 / gcs / http: spec.<type>.key/object/url MUST point at a pre-archived
    `.tar.gz` body directly; these fetchers do NOT walk directories. Stage-1
    validates the fetched body is gzip (malformed → Synced=False,
    reason=UpstreamInvalid).

CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
CRD-04: spec.refresh.maxStaleness is REQUIRED.



_Appears in:_
- [SkillMarketplace](#skillmarketplace)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ | Type names the upstream source kind for the marketplace archive. |  | Enum: [github gitlab bitbucket s3 gcs http] <br />Required: \{\} <br /> |
| `refresh` _[RefreshBlock](#refreshblock)_ | Refresh declares poll cadence and staleness bound (CRD-04). |  | Required: \{\} <br /> |
| `filters` _[MarketplaceFilters](#marketplacefilters)_ | Filters narrows the discovered skill set via anchored RE2 patterns.<br />Optional. |  |  |
| `github` _[GitHubSource](#githubsource)_ | GitHub source. Required when spec.type == "github". |  |  |
| `gitlab` _[GitLabSource](#gitlabsource)_ | GitLab source. Required when spec.type == "gitlab". |  |  |
| `bitbucket` _[BitbucketSource](#bitbucketsource)_ | Bitbucket source. Required when spec.type == "bitbucket". |  |  |
| `s3` _[S3Source](#s3source)_ | S3 source. Required when spec.type == "s3". |  |  |
| `gcs` _[GCSSource](#gcssource)_ | GCS source. Required when spec.type == "gcs". |  |  |
| `http` _[HTTPSource](#httpsource)_ | HTTP source. Required when spec.type == "http". |  |  |


#### SkillMarketplaceStatus



SkillMarketplaceStatus defines the observed state of SkillMarketplace.

In addition to the shared ExternalRefStatus, SkillMarketplace exposes a
Synced condition with reasons UpstreamInvalid, InvalidConfig (plus per-skill
soft-skip reasons in the message), plus the materialized skill set
(Skills / SkillsCount) populated on each successful reconcile.



_Appears in:_
- [SkillMarketplace](#skillmarketplace)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the<br />reconciler most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions exposes SourceReachable (and, for PluginMarketplace,<br />Synced) per §6.6. |  |  |
| `storageLocation` _string_ | StorageLocation is the cached filesystem path the Content Service<br />serves from after the last successful refresh (§10.3). Empty until<br />the first successful refresh. |  |  |
| `lastSuccessfulRefresh` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | LastSuccessfulRefresh is the wall-clock time of the most recent<br />successful upstream fetch + atomic publish (§10.3 step 5). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the per-source revision identifier the most recent<br />successful refresh recorded — for git sources this is the resolved<br />commit SHA; for S3 it is the object ETag; for GCS the object<br />generation; for HTTP a composite of ETag and Last-Modified<br />separated by a literal pipe. The Phase 2 reconciler reads this<br />value to pass as PriorRev on the next fetch for conditional-GET /<br />not-modified detection. Empty before the first successful refresh. |  |  |
| `skills` _[SkillMarketplaceSkillRef](#skillmarketplaceskillref) array_ | Skills lists the entries in the upstream collection that the most<br />recent reconcile successfully materialized into skill_marketplace_skills<br />(+ the per-marketplace cache). Ordered by Name. Entries that failed<br />Stage-2 are NOT included here — those surface in the Synced condition's<br />message field. Empty before the first successful reconcile. |  |  |
| `skillsCount` _integer_ | SkillsCount is the size of Skills, denormalized so the kubectl print<br />column can show it without a JSONPath length() expression. Equal to<br />len(Skills). |  |  |


#### SkillSpec



SkillSpec defines the desired state of Skill.

A Skill references an upstream location whose subtree contains an agent
skill directory (a root directory with a SKILL.md manifest plus optional
scripts/, references/, and assets/ subdirectories; see
https://agentskills.io/specification). ACH fetches the subtree and serves
it as a .tar.gz archive. Unlike Plugin, no component filter is applied —
the fetched skill tree is served verbatim.

CRD-03: spec.type's matching subobject MUST be present (CEL-enforced).
CRD-04: spec.refresh.maxStaleness is REQUIRED; spec.refresh.interval,
when set, MUST NOT exceed spec.refresh.maxStaleness.



_Appears in:_
- [Skill](#skill)

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


#### SkillStatus



SkillStatus defines the observed state of Skill.



_Appears in:_
- [Skill](#skill)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ | ObservedGeneration is the metadata.generation of the CR the<br />reconciler most recently processed. |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#condition-v1-meta) array_ | Conditions exposes SourceReachable (and, for PluginMarketplace,<br />Synced) per §6.6. |  |  |
| `storageLocation` _string_ | StorageLocation is the cached filesystem path the Content Service<br />serves from after the last successful refresh (§10.3). Empty until<br />the first successful refresh. |  |  |
| `lastSuccessfulRefresh` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#time-v1-meta)_ | LastSuccessfulRefresh is the wall-clock time of the most recent<br />successful upstream fetch + atomic publish (§10.3 step 5). |  |  |
| `upstreamRev` _string_ | UpstreamRev is the per-source revision identifier the most recent<br />successful refresh recorded — for git sources this is the resolved<br />commit SHA; for S3 it is the object ETag; for GCS the object<br />generation; for HTTP a composite of ETag and Last-Modified<br />separated by a literal pipe. The Phase 2 reconciler reads this<br />value to pass as PriorRev on the next fetch for conditional-GET /<br />not-modified detection. Empty before the first successful refresh. |  |  |


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


#### WebhookAuthSpec



WebhookAuthSpec configures webhook auth (config: channels[].webhook.auth; secretRef → secretPath).



_Appears in:_
- [WebhookSpec](#webhookspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _string_ |  |  | Enum: [gitlab_token hmac header_token none] <br />Required: \{\} <br /> |
| `header` _string_ |  |  |  |
| `secretRef` _[SecretKeyRef](#secretkeyref)_ |  |  |  |


#### WebhookSpec



WebhookSpec configures a webhook channel (config: channels[].webhook + channels[].source).



_Appears in:_
- [ChannelSpec](#channelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `auth` _[WebhookAuthSpec](#webhookauthspec)_ |  |  | Required: \{\} <br /> |
| `gitlabEvents` _string array_ |  |  | items:Enum: [merge_request issue note] <br /> |


