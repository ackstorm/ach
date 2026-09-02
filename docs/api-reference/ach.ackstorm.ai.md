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
| `image` _string_ |  |  |  |
| `ach` _[AchEndpointSpec](#achendpointspec)_ |  |  |  |
| `model` _[ModelSpec](#modelspec)_ |  |  |  |
| `engine` _[EngineSpec](#enginespec)_ |  |  |  |
| `limits` _[LimitsSpec](#limitsspec)_ |  |  |  |
| `health` _[HealthSpec](#healthspec)_ |  |  |  |
| `cost` _[CostSpec](#costspec)_ |  |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#envvar-v1-core) array_ | Env are pod-level environment variables merged over AgentProfile.spec.env by name.<br />An agent entry replaces the complete inherited EnvVar. Reserved ACH_* names are<br />forbidden; only literal values and secretKeyRef sources are supported. |  |  |
| `capability` _[CapabilitySpec](#capabilityspec)_ | Capability is optional: both of its fields are optional, so the block<br />validates nothing on its own. Render always emits a capability block<br />(the harness schema requires one) — capability.ach.baseUrl comes from<br />agentrender.ResolveAchBaseURL, never from here. |  |  |
| `prompt` _[AgentPromptSpec](#agentpromptspec)_ |  |  |  |
| `memory` _[MemorySpec](#memoryspec)_ |  |  |  |
| `expose` _[ExposeSpec](#exposespec)_ | Expose controls reachability (Service + gateway route). Omit for a fully<br />private agent (no Service, no public URL). |  |  |
| `channels` _[ChannelSpec](#channelspec) array_ |  |  | MinItems: 1 <br />Required: \{\} <br /> |
| `mcpServers` _[McpServerSpec](#mcpserverspec) array_ | MCPServers are harness-managed MCP servers (repoCheckout / local / remote)<br />rendered into the config's mcpServers map. Presence = enabled; omit for none. |  |  |


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
BaseURL is optional: it resolves as ACHAgent.spec.ach.baseUrl ?? AgentProfile.spec.achagent.ach.baseUrl ??
operator ACH_BASE_URL env (agentrender.ResolveAchBaseURL). An empty result blocks the agent.



_Appears in:_
- [ACHAgentSpec](#achagentspec)
- [AgentDefaults](#agentdefaults)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `baseUrl` _string_ |  |  |  |


#### AgentDefaults



AgentDefaults is the shared set of profile defaults that an agent may override.
AgentProfile.spec.achagent names these defaults, and ACHAgentSpec embeds this
type inline. Resolution is a per-field deep merge: a field set on the agent
wins, while an omitted field inherits from the profile. Slices, maps, and
nested blocks such as engine.pi and cost are atomic and are not recursively merged.
The resolvers are the source of truth for this behavior. Image is required on
the profile, but optional on the agent and inherited when omitted.



_Appears in:_
- [ACHAgentSpec](#achagentspec)
- [AgentProfileSpec](#agentprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _string_ |  |  |  |
| `ach` _[AchEndpointSpec](#achendpointspec)_ |  |  |  |
| `model` _[ModelSpec](#modelspec)_ |  |  |  |
| `engine` _[EngineSpec](#enginespec)_ |  |  |  |
| `limits` _[LimitsSpec](#limitsspec)_ |  |  |  |
| `health` _[HealthSpec](#healthspec)_ |  |  |  |
| `cost` _[CostSpec](#costspec)_ |  |  |  |


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



AgentProfileSpec is the reusable infra + defaults half. Agent-scoped defaults
(image/ach/model/engine/limits/health/cost) live under the named achagent block and
deep-merge with an ACHAgent's inline AgentDefaults (agent field wins);
everything else here is profile-only infrastructure an agent cannot override.



_Appears in:_
- [AgentProfile](#agentprofile)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `achagent` _[AgentDefaults](#agentdefaults)_ | Achagent holds the agent-overridable defaults. image is required here<br />(object-level CEL); the other fields are optional defaults. |  | Required: \{\} <br /> |
| `imagePullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#localobjectreference-v1-core) array_ |  |  |  |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#resourcerequirements-v1-core)_ |  |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#envvar-v1-core) array_ | Env are pod-level environment variables inherited by ACHAgents using this profile.<br />Reserved ACH_* names are forbidden because the operator owns that namespace. Only<br />literal values and secretKeyRef sources are supported. |  |  |
| `nodeSelector` _object (keys:string, values:string)_ |  |  |  |
| `tolerations` _[Toleration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#toleration-v1-core) array_ |  |  |  |
| `persistence` _[PersistenceSpec](#persistencespec)_ |  |  |  |
| `networkPolicy` _[NetworkPolicySpec](#networkpolicyspec)_ | NetworkPolicy renders a default-deny egress NetworkPolicy for the agent pod.<br />Omitted → no policy (unrestricted egress). See NetworkPolicySpec. |  |  |
| `terminationGracePeriodSeconds` _integer_ |  |  | Minimum: 0 <br /> |
| `podTemplate` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#json-v1-apiextensions-k8s-io)_ | PodTemplate is a raw strategic-merge-patch overlay applied over the operator-rendered pod<br />template (containers/env/volumes merge by name, scalars user-wins). Pass-through by design<br />(ponytail: no field guardrails — the profile author already controls spec.achagent.image, i.e.<br />everything that runs in the pod). A malformed overlay surfaces as WorkloadApplied=False<br />(PodTemplateInvalid); a merged-but-broken pod surfaces as a failing rollout. Note the<br />env ACH_* CEL guard does NOT inspect this overlay. After the merge the operator<br />re-pins the selector label and the config-hash annotation. |  |  |


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
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef is optional. When set, the Secret named here MUST<br />exist in the CR's namespace at reconcile time and the operator<br />reads the bearer token from the named key (see SourceAuthSecretRef.Key).<br />When nil, the upstream fetch is anonymous — supported only for<br />public repositories. |  |  |


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
| `prepare` _[PrepareSpec](#preparespec)_ | Prepare is the per-invocation workspace hook (see PrepareSpec). Valid for every<br />channel type, hence outside the type↔block coherence the harness enforces. |  |  |


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


#### CostSpec



CostSpec selects where the per-invocation cost figure comes from (config: cost.source).
Free string ("engine"|"litellm_usage"|"litellm_headers"|"none"); the harness validates and
hard-fails on an unknown value. Omitted → harness default (engine, today's behavior).
litellm_usage prices per-response usage against LiteLLM's table via GET /v2/model/info;
litellm_headers reads x-litellm-response-cost and is non-streaming only. Requires an
ach-agent image >= v0.10.0.



_Appears in:_
- [ACHAgentSpec](#achagentspec)
- [AgentDefaults](#agentdefaults)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `source` _string_ |  |  |  |


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
- [ACHAgentSpec](#achagentspec)
- [AgentDefaults](#agentdefaults)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `home` _string_ |  |  |  |
| `workDir` _string_ |  |  |  |
| `forwardEnv` _string array_ |  |  |  |
| `idleTtlSeconds` _integer_ |  |  | Minimum: 0 <br /> |
| `startupTimeoutSeconds` _integer_ |  |  | Minimum: 1 <br /> |
| `maxToolCalls` _integer_ |  |  | Minimum: 0 <br /> |
| `type` _string_ | Type selects the engine. Free string ("opencode"\|"pi"); the harness validates and<br />hard-fails on an unknown value. Omitted → harness default (opencode). |  |  |
| `pi` _[PiEngineSpec](#pienginespec)_ | Pi configures the Pi engine; consulted only when Type == "pi". |  |  |


#### Environment



Environment is the Schema for the environments API (Hub §6).

An Environment is the ACH product boundary: a bundle of runtime
(models, mcpServers, a2aAgents, guardrails) and context (prompts, plugins,
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
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef is optional. When set, the Secret named here MUST<br />exist in the CR's namespace at reconcile time and the operator<br />reads the bearer token from the named key (see SourceAuthSecretRef.Key).<br />When nil, the upstream fetch is anonymous — supported only for<br />public repositories. |  |  |


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
| `authSecretRef` _[SourceAuthSecretRef](#sourceauthsecretref)_ | AuthSecretRef is optional. When set, the Secret named here MUST<br />exist in the CR's namespace at reconcile time and the operator<br />reads the bearer token from the named key (see SourceAuthSecretRef.Key).<br />When nil, the upstream fetch is anonymous — supported only for<br />public projects. |  |  |


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
- [ACHAgentSpec](#achagentspec)
- [AgentDefaults](#agentdefaults)

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
| `bank` _string_ | Bank is the static, harness-owned memory bank id. NEVER template it from<br />inbound payload (untrusted → cross-tenant memory); per-repo partitioning is<br />via tags, harness-side. |  |  |
| `auth` _[SecretKeyRef](#secretkeyref)_ | Auth is the admin secret for the harness→Hindsight path (Bearer, NOT the ek_).<br />Same env-only secretKeyRef mechanism as webhook/a2a: the operator injects the<br />value into the pod from this Secret and renders only the env NAME. Omit for an<br />internal/no-auth Hindsight URL. |  |  |
| `mission` _string_ | Mission is passed to create_bank at provisioning (free text). |  |  |
| `mentalModels` _[MentalModelSpec](#mentalmodelspec) array_ |  |  |  |


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
- [AgentDefaults](#agentdefaults)

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


#### LocalMcpSpec



LocalMcpSpec is a passthrough stdio MCP server opencode launches as a subprocess.
env lists extra var NAMES to forward to the subprocess (ACH_*/ek_ are stripped
defensively); wire their values into the pod via profile/agent spec.env.



_Appears in:_
- [McpServerSpec](#mcpserverspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `command` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `args` _string array_ |  |  |  |
| `env` _string array_ |  |  |  |


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


#### McpServerSpec



McpServerSpec is one harness-managed MCP server (rendered into config
mcpServers[<name>]). Discriminated by type: repoCheckout is HARNESS-HOSTED (the
harness runs a checkout_repo facade, injecting the agent's ek_); local/remote are
PASSTHROUGH (opencode launches a stdio subprocess / connects to a remote endpoint
directly, NOT via the ACH proxy). The operator renders the list into the config's
mcpServers map keyed by name. Distinct from the Environment's ACH-fronted MCP set
(hydrated as runtime.mcpServers) — different namespace, no collision.



_Appears in:_
- [ACHAgentSpec](#achagentspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `type` _string_ |  |  | Enum: [repoCheckout local remote] <br />Required: \{\} <br /> |
| `repoCheckout` _[RepoCheckoutSpec](#repocheckoutspec)_ |  |  |  |
| `local` _[LocalMcpSpec](#localmcpspec)_ |  |  |  |
| `remote` _[RemoteMcpSpec](#remotemcpspec)_ |  |  |  |


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


#### MentalModelSpec



MentalModelSpec is one Hindsight mental model the harness provisions at boot
(config: memory.hindsight.mentalModels[]). Was a bare id string pre-facade.



_Appears in:_
- [HindsightSpec](#hindsightspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `id` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `name` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `sourceQuery` _string_ | SourceQuery is the question the harness runs to build/refresh the model. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `autoRefresh` _boolean_ | AutoRefresh triggers a refresh after consolidation (harness default false). |  |  |
| `maxTokens` _integer_ | MaxTokens caps the rendered summary (harness default 2048). Omit to use it. |  | Minimum: 1 <br /> |


#### ModelSpec



ModelSpec selects the ACH-served model (config: model{name,type,params,thinking}).



_Appears in:_
- [ACHAgentSpec](#achagentspec)
- [AgentDefaults](#agentdefaults)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `type` _string_ |  |  | Enum: [openai gemini anthropic] <br />Required: \{\} <br /> |
| `params` _[JSON](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#json-v1-apiextensions-k8s-io)_ | Params is an open, unvalidated dict splatted to the model client. |  |  |
| `thinking` _[ThinkingSpec](#thinkingspec)_ | Thinking is the normalized model-level reasoning intent (config: model.thinking).<br />Free-form (no Enum) — ach-agent's Pydantic ThinkingBlock is the single enforcer<br />(D-2 precedent): effort one of minimal\|low\|medium\|high\|xhigh, requires enabled=true. |  |  |


#### NetworkPolicySpec



NetworkPolicySpec renders a default-deny EGRESS NetworkPolicy selecting the agent pod.
Presence is the opt-in: an omitted block means no policy at all (the agent keeps
unrestricted egress — the pre-feature behaviour). An empty block (`networkPolicy: {}`)
is deny-all-except-DNS.

Egress-only by design: policyTypes never includes Ingress, so expose.service /
gateway→agent routing is untouched.

Rules are DECLARED here, not derived from ach.baseUrl: upstream NetworkPolicy has no
FQDN peer type and ACH_BASE_URL is a URL, so the operator cannot translate the ACH
endpoint into a peer portably. Declare the forwarder/gateway peer yourself — an
in-cluster podSelector+namespaceSelector, or an ipBlock CIDR for an external endpoint.
The operator contributes what only it knows: the pod selector (operator-owned labels),
the DNS rule, and lifecycle (created/pruned/GC'd with the agent).



_Appears in:_
- [AgentProfileSpec](#agentprofilespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `egress` _[NetworkPolicyEgressRule](https://kubernetes.io/docs/reference/generated/kubernetes-api/v/#networkpolicyegressrule-v1-networking) array_ | Egress rules appended after the operator's DNS rule. Raw networking.k8s.io/v1<br />egress rules, pass-through (same contract as podTemplate: the profile author<br />already controls spec.achagent.image, so no field guardrails here). Empty → DNS only,<br />i.e. every other outbound connection is denied. |  |  |


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


#### PiEngineSpec



PiEngineSpec is the harness-local Pi engine block (config: engine.pi.*) — executable
knobs ONLY (model identity and thinking intent live in ModelSpec). All fields are
optional; empty binaryPath/mcpAdapterPath fall back to the image defaults (pi on PATH;
the vendored adapter at /opt/pi-mcp-adapter/node_modules/pi-mcp-adapter). The
v0.8.1-only model and thinking-level fields were removed for ach-agent v0.9.0.



_Appears in:_
- [EngineSpec](#enginespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `binaryPath` _string_ |  |  |  |
| `mcpAdapterPath` _string_ |  |  |  |


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


#### PrepareSpec



PrepareSpec is the per-invocation workspace hook (config: channels[].prepare;
ach-agent CONTRACT v3 §9.1). A /bin/sh script the harness runs on the router lane —
after dedup + backpressure admitted the event, before the engine exists — with cwd set
to that session's workspace, which then becomes the engine's cwd. Its canonical use is
cloning the repo a merge-request event names, so the agent reviews a real checkout and
the clone credential stays in the harness process instead of being handed to the agent.

Script is STATIC text: the harness does not render {{ }} in it, and no webhook payload
value is ever interpolated into shell source. Event data reaches the script only as
environment variables (ACH_WORKSPACE, ACH_EVENT_*), which is what makes handing a
webhook payload to a shell hook safe — there is no injection surface.

Valid on ANY channel type (a cron channel may want a workspace too).



_Appears in:_
- [ChannelSpec](#channelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `script` _string_ | Script is the /bin/sh program, run as `sh -eu` fed on stdin. It re-runs for every<br />event on the same session_key against a workspace that persists (the clone cache),<br />so it MUST be idempotent — clone-or-fetch, not clone. A non-zero exit abandons the<br />invocation (fail-closed): nothing is posted. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `forwardEnv` _string array_ | ForwardEnv selects names from the merged AgentProfile.spec.env + ACHAgent.spec.env.<br />Literal values become prepare.env; secretKeyRef values become prepare.secretEnv via<br />generated Pod aliases. Unknown names are ignored and remain unset. |  | items:Pattern: ^[A-Za-z_][A-Za-z0-9_]*$ <br /> |
| `timeoutSeconds` _integer_ | TimeoutSeconds bounds the script; on expiry the harness SIGKILLs its process group<br />and abandons the invocation. Harness default is 120 when omitted. |  | Maximum: 3600 <br />Minimum: 1 <br /> |


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


#### RemoteMcpSpec



RemoteMcpSpec is a passthrough remote MCP endpoint opencode connects to directly.
headers values are ${env:NAME} refs (NAMES, never secret values); wire the env into
the pod via profile/agent spec.env. SECURITY: opencode receives the
resolved header, so a co-resident same-uid agent CAN read it — front the server via
ACH hydrate instead if that is unacceptable.



_Appears in:_
- [McpServerSpec](#mcpserverspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `url` _string_ |  |  | MinLength: 1 <br />Required: \{\} <br /> |
| `headers` _object (keys:string, values:string)_ |  |  |  |


#### RepoCheckoutSpec



RepoCheckoutSpec configures the harness-hosted checkout_repo tool. The harness reads
gitlab://{project}/archive/{ref} from the hydrated MCP server named by
sourceMcpServerId (with the agent's ek_, harness-side) and extracts it into a
per-checkout dir under tmpBase, TTL-swept. A sourceMcpServerId that names no MCP
server the agent's Environment exposes makes the tool fail-soft at runtime (no
crash); ACH does not cross-validate it at admission (see the 2026-07-07 addendum).



_Appears in:_
- [McpServerSpec](#mcpserverspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `sourceMcpServerId` _string_ | SourceMcpServerID is the hydrated runtime.mcpServers[].id whose endpoint serves<br />the gitlab archive resource. |  | MinLength: 1 <br />Required: \{\} <br /> |
| `tmpBase` _string_ | TmpBase is the parent dir for per-checkout tmp dirs (harness default /tmp/gitlab). |  |  |
| `ttlSeconds` _integer_ | TTLSeconds bounds how long a stale checkout lingers before the next call sweeps<br />it (harness default 3600). |  | Minimum: 0 <br /> |


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
| `guardrails` _string array_ | Guardrails lists LiteLLM guardrail names (guardrail_name) that LiteLLM<br />runs on this Environment's ek_ traffic.<br />REQUIRES A LITELLM ENTERPRISE LICENCE. Team-scoped guardrails are premium-<br />gated: without LITELLM_LICENSE set on the proxy, a non-empty list here is<br />rejected 403 TWICE — when the operator attaches it to the shell team<br />(AccessGroupSynced=False, so the Environment never goes Available and NEW<br />ek_ minting is blocked) and again on every request a key in that team<br />makes. Leaving this empty is exempt and is the only supported shape on an<br />unlicensed proxy. Measured on LiteLLM v1.93.0, 2026-07-29; see<br />references/litellm-permission-model.md §11.<br />GLOBAL default_on guardrails are NOT gated and run on every request with<br />no licence and no ACH configuration — check `ach-cli runtime guardrails<br />list` before adding a name here, because naming a default_on guardrail<br />changes nothing except your exposure to the gate above.<br />WHICH traffic is each guardrail's own business, not ACH's: a guardrail<br />declares one or more modes, and ACH only attaches the name. A<br />pre_call/post_call guardrail inspects LLM completions and does NOT run on<br />/mcp traffic — that needs a guardrail configured with pre_mcp_call or<br />during_mcp_call. Listing a name here is therefore not a blanket promise<br />of coverage; `ach-cli runtime guardrails` prints each one's MODE.<br />This axis INVERTS the semantics of its siblings: models, mcpServers and<br />a2aAgents are additive grants, a guardrail is a constraint. ACH attaches<br />the names to the Environment's deny-all shell team; LiteLLM unions them<br />across key, team and request body, so a caller can ADD to the set but<br />never subtract from it.<br />Coverage is EK-only. ek_ keys live in this Environment's shell team and<br />inherit its guardrails; pk_ keys live in ach-user-<email> and reach the<br />Environment through the access group, which carries no guardrail field.<br />See references/litellm-permission-model.md.<br />An unresolved name blocks AccessGroupSynced, which blocks NEW ek_<br />minting — it does NOT stop existing keys, hydrate, or forwarded traffic.<br />Entries must be unique: the operator compares the attached set against<br />LiteLLM's stored list to decide whether a repair is needed, and<br />duplicates make that comparison undecidable (LiteLLM's own duplicate<br />handling differs between its storage and enforcement paths).<br />ACH never creates, updates or deletes guardrail definitions. | \{  \} | MaxItems: 50 <br />items:MaxLength: 253 <br />items:Pattern: ^[^/\\?#%\s\x00-\x1f\x7f]+$ <br /> |


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
- [HindsightSpec](#hindsightspec)
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


#### ThinkingSpec



ThinkingSpec is the normalized reasoning intent each engine translates for itself
(pi: models.json reasoning + --thinking; opencode: per-call providerOptions).



_Appears in:_
- [ModelSpec](#modelspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ |  |  |  |
| `effort` _string_ |  |  |  |


#### UnresolvedRuntime



UnresolvedRuntime mirrors the four runtime reference lists (§6.4) and
names the specific entries that did not resolve against LiteLLM.



_Appears in:_
- [EnvironmentStatus](#environmentstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `models` _string array_ |  | \{  \} |  |
| `mcpServers` _string array_ |  | \{  \} |  |
| `a2aAgents` _string array_ |  | \{  \} |  |
| `guardrails` _string array_ | Guardrails lists spec.runtime.guardrails entries not registered in<br />LiteLLM. LiteLLM accepts unknown guardrail names silently and never runs<br />them, so an unlisted name is a fail-OPEN hole. Surfacing it here and<br />failing AccessGroupSynced converts that into a blocked ek_ mint. | \{  \} |  |


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
| `botUsername` _string_ | BotUsername is the GitLab username the agent posts AS (the egress PAT's<br />user — a distinct fact from the agent name). When set, the harness drops<br />inbound events authored by this user plus gitlab-generated system notes<br />pre-enqueue (loop-guard). Omit → guard off. gitlab source only; ignored<br />for github/generic. Rendered verbatim to channels[].webhook.botUsername. |  |  |
| `triggerUsers` _string array_ | TriggerUsers is an actor allowlist: only these GitLab usernames may<br />trigger the agent (every routed kind: mr/issue/note). Omit → any author<br />triggers. gitlab source only; ignored for github/generic. Rendered<br />verbatim to channels[].webhook.triggerUsers. |  |  |


