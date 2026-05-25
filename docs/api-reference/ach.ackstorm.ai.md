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
- [Plugin](#plugin)
- [PluginList](#pluginlist)
- [PluginMarketplace](#pluginmarketplace)
- [PluginMarketplaceList](#pluginmarketplacelist)
- [Prompt](#prompt)
- [PromptList](#promptlist)



#### Artifact



Artifact is the Schema for the artifacts API.



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



ArtifactSpec defines the desired state of Artifact.



_Appears in:_
- [Artifact](#artifact)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `foo` _string_ | Foo is an example field of Artifact. Edit artifact_types.go to remove/update |  |  |


#### ArtifactStatus



ArtifactStatus defines the observed state of Artifact.



_Appears in:_
- [Artifact](#artifact)



#### BackendIdentityPolicy



BackendIdentityPolicy is the Schema for the backendidentitypolicies API.



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



BackendIdentityPolicySpec defines the desired state of BackendIdentityPolicy.



_Appears in:_
- [BackendIdentityPolicy](#backendidentitypolicy)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `foo` _string_ | Foo is an example field of BackendIdentityPolicy. Edit backendidentitypolicy_types.go to remove/update |  |  |


#### BackendIdentityPolicyStatus



BackendIdentityPolicyStatus defines the observed state of BackendIdentityPolicy.



_Appears in:_
- [BackendIdentityPolicy](#backendidentitypolicy)



#### Environment



Environment is the Schema for the environments API.



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



EnvironmentSpec defines the desired state of Environment.



_Appears in:_
- [Environment](#environment)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `foo` _string_ | Foo is an example field of Environment. Edit environment_types.go to remove/update |  |  |


#### EnvironmentStatus



EnvironmentStatus defines the observed state of Environment.



_Appears in:_
- [Environment](#environment)



#### Plugin



Plugin is the Schema for the plugins API.



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



PluginMarketplace is the Schema for the pluginmarketplaces API.



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



PluginMarketplaceSpec defines the desired state of PluginMarketplace.



_Appears in:_
- [PluginMarketplace](#pluginmarketplace)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `foo` _string_ | Foo is an example field of PluginMarketplace. Edit pluginmarketplace_types.go to remove/update |  |  |


#### PluginMarketplaceStatus



PluginMarketplaceStatus defines the observed state of PluginMarketplace.



_Appears in:_
- [PluginMarketplace](#pluginmarketplace)



#### PluginSpec



PluginSpec defines the desired state of Plugin.



_Appears in:_
- [Plugin](#plugin)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `foo` _string_ | Foo is an example field of Plugin. Edit plugin_types.go to remove/update |  |  |


#### PluginStatus



PluginStatus defines the observed state of Plugin.



_Appears in:_
- [Plugin](#plugin)



#### Prompt



Prompt is the Schema for the prompts API.



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



PromptSpec defines the desired state of Prompt.



_Appears in:_
- [Prompt](#prompt)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `foo` _string_ | Foo is an example field of Prompt. Edit prompt_types.go to remove/update |  |  |


#### PromptStatus



PromptStatus defines the observed state of Prompt.



_Appears in:_
- [Prompt](#prompt)



