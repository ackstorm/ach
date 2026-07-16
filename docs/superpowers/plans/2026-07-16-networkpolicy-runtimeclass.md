# Agent NetworkPolicy + runtimeClass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give ACHAgent pods an enforced, default-deny egress boundary (so a compromised or
misbehaving opencode `bash` tool cannot exfiltrate outside ACH), and confirm + document that
sandboxed runtimes (gVisor/Kata) already work today via `runtimeClassName`.

**Architecture:** Two halves, deliberately asymmetric.
*runtimeClass needs zero production code* — `AgentProfile.spec.podTemplate` is already a
`PreserveUnknownFields` raw strategic-merge overlay (`api/ach/v1alpha1/agentprofile_types.go:147-156`,
merged by `applyPodTemplateOverlay` at `internal/controller/ach/achagent_workload.go:279-303`), and
`runtimeClassName` is a plain PodSpec scalar, so it merges user-wins today. Task 1 only pins that
behaviour with a test and documents it.
*NetworkPolicy is new*: a `networkPolicy` block on `AgentProfileSpec` makes the operator render one
`networking.k8s.io/v1` NetworkPolicy per agent, selecting the agent pod by its operator-owned labels,
`policyTypes: [Egress]` only, with an always-present DNS rule plus the profile author's declared
egress rules. It follows the existing child-object pattern exactly (build → `apply` → prune when
un-desired), mirroring `buildService`/`needsService`/`pruneService`.

**Tech Stack:** Go 1.x, kubebuilder/controller-runtime, `k8s.io/api/networking/v1`, envtest,
stdlib `testing`, Helm.

## Global Constraints

- **License header:** every new/modified `.go` file starts with `// SPDX-License-Identifier: Apache-2.0`.
- **API group:** `ach.ackstorm.ai/v1alpha1`. Kinds: `ACHAgent`, `AgentProfile`.
- **Child naming:** every operator-created child is `achagent-<agentName>` via `agentResourceName()`.
- **No new scheme registration:** `clientgoscheme.AddToScheme` (`cmd/ach/cmd/operator.go:105`,
  `internal/controller/ach/suite_test.go:184`) already registers `networking.k8s.io/v1`. Do NOT add
  an `AddToScheme` call.
- **Egress-only:** the rendered policy MUST NOT include `Ingress` in `policyTypes`. Adding Ingress
  would silently break `expose.service` / gateway→agent routing (`internal/gateway/agents.go`).
- **Config hash is pod-template inputs only:** do NOT feed the networkPolicy block into
  `computeConfigHash` (`achagent_workload.go:68-76`). A policy edit must not roll the pod.
- **Never mutate the cached profile:** `profile` comes from the informer cache. Build new slices;
  never `append` into `p.Spec.*` in place.
- **Backwards compatible:** an omitted `networkPolicy` block means no NetworkPolicy and unrestricted
  egress — exactly today's behaviour. No existing profile changes meaning.
- **Codegen is generated, never hand-edited:** after any `api/` change run `make gen-code gen-manifests`
  and commit the results (`api/ach/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/*`,
  `config/rbac/role.yaml`).
- **Commands run in the devtools container** via `make` unless `IN_DEVTOOLS=1`.

## Design decisions (locked — do not relitigate mid-task)

1. **Block lives on `AgentProfile`, not `ACHAgent`.** All agent egress goes through `ACH_BASE_URL`
   (model, MCP, A2A all via the forwarder), so the peer set is identical for every agent of one ACH
   deployment. That is textbook "reusable infra" = profile.
2. **Egress rules are declared by the author, not derived by the operator.** Upstream NetworkPolicy
   has **no FQDN peer type**, and `ACH_BASE_URL` is a URL. The operator physically cannot translate
   it into a peer portably. The operator contributes only what it alone knows: the pod selector
   (operator-owned labels), the DNS rule, and lifecycle.
3. **Presence is the opt-in.** `networkPolicy: *NetworkPolicySpec` — nil means no policy; `{}` means
   deny-all-except-DNS. No `enabled: bool` (skipped: it only buys "keep rules but switch off", which
   nobody asked for — add it when someone does).
4. **DNS is port-53-to-anywhere**, not a kube-dns podSelector. CoreDNS labels and node-local DNS
   cache addresses vary per distro; a wrong selector fails closed and silently, which is the worst
   possible failure mode for a security feature. Known ceiling: DNS-tunnel exfiltration is not
   covered. Marked with a `ponytail:` comment naming the upgrade path.
5. **Ingress restriction is out of scope** (YAGNI). The exfiltration path is egress; ingress is
   already deny-by-default in the sense that `expose.service` is opt-in.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/controller/ach/achagent_envtest_test.go` | +1 test: podTemplate sets `runtimeClassName` | 1 |
| `examples/agent-runtime/profile.yaml` | runtimeClassName + networkPolicy examples | 1, 4 |
| `examples/agent-runtime/README.md` | Document both features | 1, 4 |
| `api/ach/v1alpha1/agentprofile_types.go` | `NetworkPolicySpec` type + `NetworkPolicy` field | 2 |
| `api/ach/v1alpha1/zz_generated.deepcopy.go` | generated | 2 |
| `config/crd/bases/*agentprofiles.yaml` | generated | 2 |
| `internal/controller/ach/achagent_workload.go` | `dnsEgressRule`, `buildNetworkPolicy`, `needsNetworkPolicy`, `copySpec` case | 2, 3 |
| `internal/controller/ach/achagent_workload_test.go` | builder unit tests | 2 |
| `internal/controller/ach/achagent_controller.go` | apply/prune wiring, RBAC marker, `Owns` | 3 |
| `config/rbac/role.yaml` | generated from the RBAC marker | 3 |
| `deploy/helm/ach/templates/operator-rbac.yaml` | hand-written ClusterRole — networkpolicies rule | 3 |
| `CHANGELOG.md` | `[Unreleased] → Added` entries | 4 |

---

### Task 1: Pin + document runtimeClass (zero production code)

**Why this task exists:** `runtimeClassName` already works. The risk is not that it is missing — it
is that it is *undiscovered* and *unprotected against regression* (someone adds field guardrails to
the overlay and silently breaks it). One envtest and two doc edits close both.

**Files:**
- Test: `internal/controller/ach/achagent_envtest_test.go` (append after
  `TestACHAgent_PodTemplateOverlay_MergesIntoDeployment`, which ends at line 204)
- Modify: `examples/agent-runtime/profile.yaml`
- Modify: `examples/agent-runtime/README.md`

**Interfaces:**
- Consumes: existing envtest harness — `mustApply`, `waitAgentCond`, `k8sClient`, `WatchNamespace`,
  `condWorkloadApplied`, `agentResourceName` (all already in package `ach`).
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/ach/achagent_envtest_test.go`:

```go
// runtimeClassName needs no operator field: podTemplate is a PreserveUnknownFields
// raw strategic-merge overlay, so a PodSpec scalar merges user-wins. This test is
// the regression guard for that contract — sandboxed runtimes (gVisor/Kata) are an
// operator feature only because nothing in the overlay path filters the field.
func TestACHAgent_PodTemplateOverlay_SetsRuntimeClassName(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-rc", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	mustApply(t, ctx, &achv1alpha1.AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-rc", Namespace: WatchNamespace},
		Spec: achv1alpha1.AgentProfileSpec{
			Image:       "img:test",
			Ach:         achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
			Model:       &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
			PodTemplate: &apiextensionsv1.JSON{Raw: []byte(`{"spec":{"runtimeClassName":"gvisor"}}`)},
		},
	})
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-rc", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-rc"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-rc", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	})
	waitAgentCond(t, ctx, "aa-rc", condWorkloadApplied, metav1.ConditionTrue)

	var dep appsv1.Deployment
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-rc")}, &dep); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	rc := dep.Spec.Template.Spec.RuntimeClassName
	if rc == nil || *rc != "gvisor" {
		t.Fatalf("runtimeClassName = %v, want \"gvisor\" (CRD pruning or overlay filtering regressed)", rc)
	}
	if sc := dep.Spec.Template.Spec.SecurityContext; sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("operator runAsNonRoot lost after overlay round-trip")
	}
}
```

- [ ] **Step 2: Run the test — it should PASS immediately**

Run: `make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestACHAgent_PodTemplateOverlay_SetsRuntimeClassName`
Expected: **PASS**.

This is the one intentional inversion of the usual red-green cycle: the test documents behaviour
that already exists, so green on the first run *is* the finding. If it FAILS, stop — that means
`runtimeClassName` is being pruned or filtered somewhere, the plan's core premise is wrong, and the
rest of the runtimeClass story needs a real code change. Report before continuing.

- [ ] **Step 3: Document it in the profile example**

In `examples/agent-runtime/profile.yaml`, replace the `podTemplate` block (currently the last block
in the file) with:

```yaml
  # Optional raw overlay strategic-merged over the operator-rendered pod template.
  # Pass-through: anything a PodTemplateSpec accepts; containers merge by name ("agent").
  podTemplate:
    spec:
      securityContext:
        fsGroup: 1000
        fsGroupChangePolicy: OnRootMismatch
      # Sandboxed runtime: the agent container runs opencode, which has a shell tool.
      # runtimeClassName puts the whole pod behind a gVisor/Kata boundary instead of a
      # shared host kernel. Needs the RuntimeClass to exist in the cluster; an unknown
      # name leaves the pod Pending (visible as WorkloadReady=False).
      # runtimeClassName: gvisor
```

- [ ] **Step 4: Document it in the README**

Append this section to `examples/agent-runtime/README.md`:

```markdown
## Hardening the agent pod

The agent container runs opencode, which has a shell tool. Two knobs bound what that shell can
reach. Both live on the `AgentProfile` (reusable infra), not on the `ACHAgent`.

### Sandboxed runtime (`runtimeClassName`)

No dedicated field — `spec.podTemplate` is a raw strategic-merge overlay, so set it directly:

```yaml
spec:
  podTemplate:
    spec:
      runtimeClassName: gvisor
```

The RuntimeClass must already exist in the cluster. An unknown name leaves the pod Pending, which
surfaces as `WorkloadReady=False` on the ACHAgent.

### Egress allowlist (`networkPolicy`)

See `spec.networkPolicy` in `profile.yaml`. Omitted → no policy, unrestricted egress.
```

- [ ] **Step 5: Commit**

```bash
git add internal/controller/ach/achagent_envtest_test.go examples/agent-runtime/profile.yaml examples/agent-runtime/README.md
git commit -m "test(achagent): pin runtimeClassName support via podTemplate overlay"
```

---

### Task 2: NetworkPolicySpec API type + builder

**Files:**
- Modify: `api/ach/v1alpha1/agentprofile_types.go` (add type near `PersistenceSpec` at :93-110; add
  field to `AgentProfileSpec` :113-157)
- Modify: `internal/controller/ach/achagent_workload.go` (add builders after `buildService`, which
  ends at :159)
- Test: `internal/controller/ach/achagent_workload_test.go`
- Generated: `api/ach/v1alpha1/zz_generated.deepcopy.go`, `config/crd/bases/*agentprofiles.yaml`,
  `config/rbac/role.yaml`

**Interfaces:**
- Consumes: `agentResourceName(string) string`, `agentLabels(*achv1alpha1.ACHAgent) map[string]string`,
  `agentSelectorLabels(string) map[string]string` — all in `achagent_workload.go:44-56`.
- Produces, for Task 3:
  - `achv1alpha1.NetworkPolicySpec` — struct with one field `Egress []networkingv1.NetworkPolicyEgressRule`
  - `achv1alpha1.AgentProfileSpec.NetworkPolicy *NetworkPolicySpec`
  - `needsNetworkPolicy(p *achv1alpha1.AgentProfile) bool`
  - `buildNetworkPolicy(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) *networkingv1.NetworkPolicy`

- [ ] **Step 1: Add the API type**

In `api/ach/v1alpha1/agentprofile_types.go`, add this import to the existing import block
(`agentprofile_types.go:5-9`), keeping gofmt grouping:

```go
	networkingv1 "k8s.io/api/networking/v1"
```

Then insert this type immediately after `PersistenceSpec` (which closes at line 110), before
`AgentProfileSpec`:

```go
// NetworkPolicySpec renders a default-deny EGRESS NetworkPolicy selecting the agent pod.
// Presence is the opt-in: an omitted block means no policy at all (the agent keeps
// unrestricted egress — the pre-feature behaviour). An empty block (`networkPolicy: {}`)
// is deny-all-except-DNS.
//
// Egress-only by design: policyTypes never includes Ingress, so expose.service /
// gateway→agent routing is untouched.
//
// Rules are DECLARED here, not derived from ach.baseUrl: upstream NetworkPolicy has no
// FQDN peer type and ACH_BASE_URL is a URL, so the operator cannot translate the ACH
// endpoint into a peer portably. Declare the forwarder/gateway peer yourself — an
// in-cluster podSelector+namespaceSelector, or an ipBlock CIDR for an external endpoint.
// The operator contributes what only it knows: the pod selector (operator-owned labels),
// the DNS rule, and lifecycle (created/pruned/GC'd with the agent).
type NetworkPolicySpec struct {
	// Egress rules appended after the operator's DNS rule. Raw networking.k8s.io/v1
	// egress rules, pass-through (same contract as podTemplate: the profile author
	// already controls spec.image, so no field guardrails here). Empty → DNS only,
	// i.e. every other outbound connection is denied.
	// +optional
	Egress []networkingv1.NetworkPolicyEgressRule `json:"egress,omitempty"`
}
```

Then add this field to `AgentProfileSpec`, immediately after the `Persistence` field
(`agentprofile_types.go:142-143`):

```go
	// NetworkPolicy renders a default-deny egress NetworkPolicy for the agent pod.
	// Omitted → no policy (unrestricted egress). See NetworkPolicySpec.
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`
```

- [ ] **Step 2: Regenerate deepcopy + CRDs**

Run: `make gen-code gen-manifests`
Expected: exit 0. `api/ach/v1alpha1/zz_generated.deepcopy.go` gains a `NetworkPolicySpec` DeepCopy
and `config/crd/bases/ach.ackstorm.ai_agentprofiles.yaml` gains a `networkPolicy` property.

Verify the CRD schema actually materialized:

```bash
grep -c "networkPolicy" config/crd/bases/ach.ackstorm.ai_agentprofiles.yaml
```
Expected: a non-zero count. If it is 0, the type did not get picked up — do not proceed.

- [ ] **Step 3: Write the failing builder tests**

Append to `internal/controller/ach/achagent_workload_test.go`. Add these imports to its existing
import block (`achagent_workload_test.go:3-12`):

```go
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
```

Then append:

```go
func TestNeedsNetworkPolicy_PresenceIsTheOptIn(t *testing.T) {
	p := &achv1alpha1.AgentProfile{}
	if needsNetworkPolicy(p) {
		t.Error("nil networkPolicy must render no policy (pre-feature behaviour)")
	}
	p.Spec.NetworkPolicy = &achv1alpha1.NetworkPolicySpec{}
	if !needsNetworkPolicy(p) {
		t.Error("empty networkPolicy block must render a deny-all-except-DNS policy")
	}
}

func TestBuildNetworkPolicy_EmptyBlockIsDNSOnlyAndEgressOnly(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	p := &achv1alpha1.AgentProfile{}
	p.Spec.NetworkPolicy = &achv1alpha1.NetworkPolicySpec{}

	np := buildNetworkPolicy(a, p)

	if np.Name != "achagent-demo" || np.Namespace != "ns" {
		t.Fatalf("np name/ns = %q/%q, want achagent-demo/ns", np.Name, np.Namespace)
	}
	if got := np.Spec.PodSelector.MatchLabels[agentLabelKey]; got != "demo" {
		t.Errorf("podSelector = %v, want %s=demo", np.Spec.PodSelector.MatchLabels, agentLabelKey)
	}
	// Ingress must stay untouched or expose.service/gateway routing breaks.
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("policyTypes = %v, want [Egress] only", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Egress) != 1 {
		t.Fatalf("egress rules = %d, want 1 (DNS only)", len(np.Spec.Egress))
	}
	dns := np.Spec.Egress[0]
	if len(dns.To) != 0 {
		t.Errorf("DNS rule To = %v, want empty (port-53-to-anywhere)", dns.To)
	}
	if len(dns.Ports) != 2 {
		t.Fatalf("DNS rule ports = %d, want 2 (udp + tcp)", len(dns.Ports))
	}
	protos := map[corev1.Protocol]bool{}
	for _, pt := range dns.Ports {
		if pt.Port == nil || pt.Port.IntValue() != 53 {
			t.Errorf("DNS port = %v, want 53", pt.Port)
		}
		if pt.Protocol == nil {
			t.Fatal("DNS port protocol must be explicit")
		}
		protos[*pt.Protocol] = true
	}
	if !protos[corev1.ProtocolUDP] || !protos[corev1.ProtocolTCP] {
		t.Errorf("DNS protocols = %v, want both UDP and TCP", protos)
	}
}

func TestBuildNetworkPolicy_ProfileRulesAppendedAfterDNS(t *testing.T) {
	a := &achv1alpha1.ACHAgent{}
	a.Name, a.Namespace = "demo", "ns"
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt(443)
	p := &achv1alpha1.AgentProfile{}
	p.Spec.NetworkPolicy = &achv1alpha1.NetworkPolicySpec{
		Egress: []networkingv1.NetworkPolicyEgressRule{{
			To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/8"}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
		}},
	}

	np := buildNetworkPolicy(a, p)

	if len(np.Spec.Egress) != 2 {
		t.Fatalf("egress rules = %d, want 2 (dns + profile rule)", len(np.Spec.Egress))
	}
	if len(np.Spec.Egress[0].To) != 0 {
		t.Error("DNS rule must stay first — a profile rule was prepended")
	}
	if np.Spec.Egress[1].To[0].IPBlock.CIDR != "10.0.0.0/8" {
		t.Errorf("profile rule lost: %+v", np.Spec.Egress[1])
	}
	// The profile comes from the informer cache — the builder must never append into it.
	if len(p.Spec.NetworkPolicy.Egress) != 1 {
		t.Errorf("builder mutated the cached profile's egress slice: len = %d, want 1", len(p.Spec.NetworkPolicy.Egress))
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `make test-unit-pkg PKG=./internal/controller/ach/...`
Expected: FAIL — compile error, `undefined: needsNetworkPolicy` and `undefined: buildNetworkPolicy`.

- [ ] **Step 5: Write the builders**

In `internal/controller/ach/achagent_workload.go`, add to the import block (`achagent_workload.go:5-23`):

```go
	networkingv1 "k8s.io/api/networking/v1"
```

Insert after `buildService` (which closes at line 159), before the `needsService` comment block:

```go
// dnsEgressRule allows name resolution. Always first in every rendered policy: without
// it, a default-deny egress policy breaks DNS and every outbound call fails with a
// resolution error that looks nothing like a policy denial — the #1 NetworkPolicy footgun.
//
// ponytail: port-53-to-anywhere rather than a kube-dns podSelector. CoreDNS labels and
// node-local DNS cache addresses vary per distro, and a wrong selector fails closed and
// silently, which is the worst failure mode for a security control. Known ceiling: this
// does not stop DNS-tunnel exfiltration. If a deployment needs DNS pinned to kube-dns,
// add a `dns:` knob to NetworkPolicySpec — do not widen this rule.
func dnsEgressRule() networkingv1.NetworkPolicyEgressRule {
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	port := intstr.FromInt(53)
	return networkingv1.NetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &udp, Port: &port},
			{Protocol: &tcp, Port: &port},
		},
	}
}

// needsNetworkPolicy reports whether the operator renders the agent's egress policy.
// Presence of the block is the opt-in; absence keeps the pre-feature unrestricted egress.
func needsNetworkPolicy(p *achv1alpha1.AgentProfile) bool {
	return p.Spec.NetworkPolicy != nil
}

// buildNetworkPolicy renders the agent's egress allowlist: DNS plus the profile-declared
// rules, denying everything else. Egress-only — policyTypes omits Ingress so gateway→agent
// routing (expose.service) is unaffected.
//
// The operator does not derive the ACH peer: ach.baseUrl is a URL and upstream
// NetworkPolicy has no FQDN peer type. The profile author declares peers; the operator
// contributes the pod selector (its own labels), DNS, and lifecycle.
//
// Deliberately NOT part of computeConfigHash: the policy is not a pod-template input, so
// editing it must not roll the pod.
func buildNetworkPolicy(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) *networkingv1.NetworkPolicy {
	// Fresh slice: p comes from the informer cache and must never be appended into.
	egress := []networkingv1.NetworkPolicyEgressRule{dnsEgressRule()}
	egress = append(egress, p.Spec.NetworkPolicy.Egress...)
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace, Labels: agentLabels(a)},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: agentSelectorLabels(a.Name)},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      egress,
		},
	}
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `make test-unit-pkg PKG=./internal/controller/ach/...`
Expected: PASS — including `TestNeedsNetworkPolicy_PresenceIsTheOptIn`,
`TestBuildNetworkPolicy_EmptyBlockIsDNSOnlyAndEgressOnly`,
`TestBuildNetworkPolicy_ProfileRulesAppendedAfterDNS`.

- [ ] **Step 7: Lint**

Run: `make fmt vet qa-lint-changed`
Expected: exit 0, no findings.

- [ ] **Step 8: Commit**

```bash
git add api/ach/v1alpha1/agentprofile_types.go api/ach/v1alpha1/zz_generated.deepcopy.go config/crd/bases config/rbac/role.yaml internal/controller/ach/achagent_workload.go internal/controller/ach/achagent_workload_test.go
git commit -m "feat(achagent): add AgentProfile.networkPolicy egress block + builder"
```

---

### Task 3: Wire the NetworkPolicy into reconcile (apply, prune, RBAC, watch)

**Files:**
- Modify: `internal/controller/ach/achagent_controller.go` (RBAC marker block :54-63; apply section
  :198-207; prune helper after `pruneService` :248-254; `SetupWithManager` :417-429)
- Modify: `internal/controller/ach/achagent_workload.go` (`copySpec` switch :307-338)
- Modify: `deploy/helm/ach/templates/operator-rbac.yaml` (ClusterRole rules)
- Test: `internal/controller/ach/achagent_envtest_test.go`
- Generated: `config/rbac/role.yaml`

**Interfaces:**
- Consumes from Task 2: `needsNetworkPolicy(*achv1alpha1.AgentProfile) bool`,
  `buildNetworkPolicy(*achv1alpha1.ACHAgent, *achv1alpha1.AgentProfile) *networkingv1.NetworkPolicy`,
  `achv1alpha1.NetworkPolicySpec`.
- Consumes existing: `r.apply(ctx, owner, desired) error` (:237-244), `r.applyFail(...)`,
  `copySpec(existing, desired)` (:307), `pruneService` as the prune pattern (:248-254).
- Produces: `(*ACHAgentReconciler).pruneNetworkPolicy(ctx, *achv1alpha1.ACHAgent) error`.

- [ ] **Step 1: Write the failing envtest**

Append to `internal/controller/ach/achagent_envtest_test.go`. Add BOTH of these to its import block
(`achagent_envtest_test.go:11-27`) — the file currently has neither:

```go
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
```

Then append:

```go
// Renders on presence, prunes on removal. The flip edits the AgentProfile, so this also
// exercises the profile→agents reverse-enqueue watch.
func TestACHAgent_NetworkPolicy_RendersAndPrunes(t *testing.T) {
	ctx := context.Background()
	mustApply(t, ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "aa-ek-np", Namespace: WatchNamespace}, Data: map[string][]byte{"ek": []byte("ek_test")}})
	tcp := corev1.ProtocolTCP
	port := intstr.FromInt(443)
	mustApply(t, ctx, &achv1alpha1.AgentProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-prof-np", Namespace: WatchNamespace},
		Spec: achv1alpha1.AgentProfileSpec{
			Image: "img:test",
			Ach:   achv1alpha1.AchEndpointSpec{BaseURL: "https://ach"},
			Model: &achv1alpha1.ModelSpec{Name: "m", Type: "openai"},
			NetworkPolicy: &achv1alpha1.NetworkPolicySpec{
				Egress: []networkingv1.NetworkPolicyEgressRule{{
					To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/8"}}},
					Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
				}},
			},
		},
	})
	mustApply(t, ctx, &achv1alpha1.ACHAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "aa-np", Namespace: WatchNamespace},
		Spec: achv1alpha1.ACHAgentSpec{
			ProfileRef: achv1alpha1.LocalObjectRef{Name: "aa-prof-np"},
			Identity:   achv1alpha1.IdentitySpec{SecretRef: achv1alpha1.SecretKeyRef{Name: "aa-ek-np", Key: "ek"}},
			Capability: achv1alpha1.CapabilitySpec{Environment: "prod"},
			Channels:   []achv1alpha1.ChannelSpec{{Name: "c", Type: "cron", Cron: &achv1alpha1.CronSpec{Schedule: "* * * * *"}}},
		},
	})
	waitAgentCond(t, ctx, "aa-np", condWorkloadApplied, metav1.ConditionTrue)

	npKey := types.NamespacedName{Namespace: WatchNamespace, Name: agentResourceName("aa-np")}
	var np networkingv1.NetworkPolicy
	if !Eventually(func() bool { return k8sClient.Get(ctx, npKey, &np) == nil }, 10*time.Second, 200*time.Millisecond) {
		t.Fatal("NetworkPolicy must exist when the profile declares networkPolicy")
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("policyTypes = %v, want [Egress] only", np.Spec.PolicyTypes)
	}
	if np.Spec.PodSelector.MatchLabels[agentLabelKey] != "aa-np" {
		t.Errorf("podSelector = %v, want %s=aa-np", np.Spec.PodSelector.MatchLabels, agentLabelKey)
	}
	if len(np.Spec.Egress) != 2 {
		t.Fatalf("egress rules = %d, want 2 (dns + profile rule)", len(np.Spec.Egress))
	}
	if len(np.OwnerReferences) == 0 {
		t.Error("NetworkPolicy must carry an owner ref (GC on ACHAgent delete)")
	}

	// Remove the block from the profile — the policy must be pruned (owner-ref GC only
	// fires on ACHAgent delete, not when the owner stops desiring the child).
	var prof achv1alpha1.AgentProfile
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: WatchNamespace, Name: "aa-prof-np"}, &prof); err != nil {
		t.Fatalf("get profile: %v", err)
	}
	prof.Spec.NetworkPolicy = nil
	if err := k8sClient.Update(ctx, &prof); err != nil {
		t.Fatalf("remove networkPolicy: %v", err)
	}

	if !Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, npKey, &networkingv1.NetworkPolicy{}))
	}, 10*time.Second, 200*time.Millisecond) {
		t.Fatal("NetworkPolicy must be pruned once the profile drops the block")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestACHAgent_NetworkPolicy_RendersAndPrunes`
Expected: FAIL — "NetworkPolicy must exist when the profile declares networkPolicy" (nothing applies it yet).

- [ ] **Step 3: Add the copySpec case**

In `internal/controller/ach/achagent_workload.go`, add this case to the `copySpec` switch, after the
`*corev1.Service` case (which closes at line 337, just before the switch's closing brace):

```go
	case *networkingv1.NetworkPolicy:
		d := desired.(*networkingv1.NetworkPolicy)
		e.Labels = d.Labels
		e.Spec = d.Spec
```

- [ ] **Step 4: Add the RBAC marker**

In `internal/controller/ach/achagent_controller.go`, add after the pods marker (line 63):

```go
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
```

- [ ] **Step 5: Wire apply + prune into Reconcile**

In `internal/controller/ach/achagent_controller.go`, insert this block immediately after the Service
apply/prune block (which closes at line 206) and immediately before
`setCond(&conds, condWorkloadApplied, metav1.ConditionTrue, "WorkloadApplied", "", agent.Generation)`
(line 207):

```go
	if needsNetworkPolicy(&profile) {
		if err := r.apply(ctx, &agent, buildNetworkPolicy(&agent, &profile)); err != nil {
			return r.applyFail(ctx, &agent, conds, "NetworkPolicy", err)
		}
	} else if err := r.pruneNetworkPolicy(ctx, &agent); err != nil {
		// Converge profile networkPolicy present→absent, same as pruneService: owner-ref
		// GC only fires on ACHAgent delete, not when the owner stops desiring the child.
		return r.applyFail(ctx, &agent, conds, "NetworkPolicy", err)
	}
```

- [ ] **Step 6: Add the prune helper**

In `internal/controller/ach/achagent_controller.go`, add the import to the existing block:

```go
	networkingv1 "k8s.io/api/networking/v1"
```

Then insert immediately after `pruneService` (which closes at line 254):

```go
// pruneNetworkPolicy deletes the egress policy when the profile no longer declares a
// networkPolicy block. Idempotent — NotFound is a no-op.
func (r *ACHAgentReconciler) pruneNetworkPolicy(ctx context.Context, a *achv1alpha1.ACHAgent) error {
	np := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace}}
	if err := r.Delete(ctx, np); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}
```

- [ ] **Step 7: Own the child in SetupWithManager**

In `internal/controller/ach/achagent_controller.go`, add to the builder chain after
`Owns(&corev1.ServiceAccount{}).` (line 424):

```go
		Owns(&networkingv1.NetworkPolicy{}).
```

- [ ] **Step 8: Regenerate RBAC and verify the marker landed**

Run: `make gen-manifests`
Then: `grep -A3 "networking.k8s.io" config/rbac/role.yaml`
Expected: a rule block granting `networkpolicies` with create/delete/get/list/patch/update/watch.

- [ ] **Step 9: Add the Helm ClusterRole rule**

envtest bypasses RBAC entirely, so this step is the only thing standing between the feature and a
403 in a real cluster. In `deploy/helm/ach/templates/operator-rbac.yaml`, insert immediately after
the `apps`/`deployments` rule (which ends with its `verbs:` line, before the
`- apiGroups: ["ach.ackstorm.ai"]` block):

```yaml
# ACHAgent egress policy (AgentProfile.spec.networkPolicy). Rendered per agent,
# pruned when the profile drops the block.
- apiGroups: ["networking.k8s.io"]
  resources: ["networkpolicies"]
  verbs: ["create", "delete", "get", "list", "patch", "update", "watch"]
```

- [ ] **Step 10: Verify the chart still templates**

Run: `helm template deploy/helm/ach --set operator.enabled=true | grep -A3 "networkpolicies"`
Expected: the rule appears in the rendered `ClusterRole`. If `helm` is unavailable on the host, run
it inside devtools: `make shell` then the same command.

- [ ] **Step 11: Run the envtest to verify it passes**

Run: `make test-envtest-pkg PKG=./internal/controller/ach/... FOCUS=TestACHAgent_NetworkPolicy_RendersAndPrunes`
Expected: PASS.

- [ ] **Step 12: Run the full package suite for regressions**

Run: `make test-unit-pkg PKG=./internal/controller/ach/...` then
`make test-envtest-pkg PKG=./internal/controller/ach/...`
Expected: PASS. Pay attention to `TestACHAgent_HappyPath_AppliesConfigMapAndDeployment` and
`TestACHAgent_ExposeServiceDisabled_PrunesService` — those profiles declare no `networkPolicy`, so
they exercise the prune-on-absent path on every reconcile and prove the no-op default is clean.

- [ ] **Step 13: Lint**

Run: `make fmt vet qa-lint-changed`
Expected: exit 0.

- [ ] **Step 14: Commit**

```bash
git add internal/controller/ach/achagent_controller.go internal/controller/ach/achagent_workload.go internal/controller/ach/achagent_envtest_test.go config/rbac/role.yaml deploy/helm/ach/templates/operator-rbac.yaml
git commit -m "feat(achagent): render + prune per-agent egress NetworkPolicy"
```

---

### Task 4: Example, docs, CHANGELOG

**Files:**
- Modify: `examples/agent-runtime/profile.yaml`
- Modify: `examples/agent-runtime/README.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the `networkPolicy` field shape from Task 2.
- Produces: nothing.

- [ ] **Step 1: Add the networkPolicy example**

In `examples/agent-runtime/profile.yaml`, insert this block immediately after the `persistence:`
block and before the `podTemplate:` comment:

```yaml
  # Optional egress allowlist. Omitted → no NetworkPolicy at all (unrestricted egress).
  # Present → default-deny egress: DNS (port 53, added by the operator) plus exactly the
  # rules below. Ingress is never touched, so expose.service/gateway routing still works.
  #
  # Rules are declared, not derived: NetworkPolicy has no FQDN peer type, so the operator
  # cannot turn ach.baseUrl into a peer for you. Point these at your ACH forwarder/gateway.
  # `networkPolicy: {}` (no egress:) is the strictest setting — DNS only.
  #
  # Requires a CNI that enforces NetworkPolicy (Calico, Cilium, ...). On a CNI that ignores
  # it the object is created and enforces nothing — verify before relying on it.
  networkPolicy:
    egress:
      # In-cluster ACH: the forwarder/gateway Service pods.
      - to:
          - namespaceSelector:
              matchLabels:
                kubernetes.io/metadata.name: ach-system
            podSelector:
              matchLabels:
                app.kubernetes.io/name: ach
        ports:
          - protocol: TCP
            port: 8080
      # External ACH behind an ingress/LB: allow the CIDR instead (no FQDN peers exist).
      # - to:
      #     - ipBlock:
      #         cidr: 203.0.113.10/32
      #   ports:
      #     - protocol: TCP
      #       port: 443
```

- [ ] **Step 2: Expand the README section**

In `examples/agent-runtime/README.md`, replace the `### Egress allowlist (\`networkPolicy\`)` section
added in Task 1 with:

```markdown
### Egress allowlist (`networkPolicy`)

The harness fronts every model and MCP call through a localhost proxy that injects the `ek_`, but
that proxy is *cooperative* — opencode's shell tool can reach anything the pod's network reaches.
`spec.networkPolicy` makes the boundary enforced instead of assumed.

```yaml
spec:
  networkPolicy:
    egress:
      - to:
          - namespaceSelector:
              matchLabels:
                kubernetes.io/metadata.name: ach-system
            podSelector:
              matchLabels:
                app.kubernetes.io/name: ach
        ports:
          - protocol: TCP
            port: 8080
```

- **Omitted** → no policy, unrestricted egress (the default, unchanged from before this feature).
- **`networkPolicy: {}`** → deny-all egress except DNS.
- The operator always prepends a DNS rule (UDP+TCP port 53, any destination). Without it a
  default-deny policy breaks name resolution, and the failure looks like a DNS bug rather than a
  policy denial. Consequence: DNS-tunnel exfiltration is not covered by this policy.
- **Egress only.** `policyTypes` never includes `Ingress`, so `expose.service` and gateway→agent
  routing are unaffected.
- Rules are **declared, not derived**. NetworkPolicy has no FQDN peer type and `ach.baseUrl` is a
  URL, so the operator cannot compute the ACH peer for you. Use a `podSelector` +
  `namespaceSelector` for in-cluster ACH, or an `ipBlock` CIDR for an external endpoint.
- **Requires a CNI that enforces NetworkPolicy** (Calico, Cilium, …). On a CNI that ignores it, the
  object exists and enforces nothing — verify in your cluster before relying on it.
- Editing the block does **not** roll the pod (the policy is not a pod-template input); the new
  rules take effect as soon as the CNI picks the object up.
```

- [ ] **Step 3: Add CHANGELOG entries**

In `CHANGELOG.md`, append to the existing `## [Unreleased]` → `### Added` list:

```markdown
- `AgentProfile.spec.networkPolicy`: renders a per-agent default-deny **egress** NetworkPolicy (operator-added DNS rule + author-declared peers), selecting the agent pod by its operator-owned labels, owner-ref'd and pruned when the block is dropped. Omitted → no policy (unchanged behaviour). Egress-only, so `expose.service`/gateway routing is unaffected. Requires a NetworkPolicy-enforcing CNI.
- Documented + regression-tested `runtimeClassName` (gVisor/Kata) support for agent pods via the existing `AgentProfile.spec.podTemplate` overlay — no new API field.
```

- [ ] **Step 4: Verify the example is valid against the real CRD**

Run: `kubectl apply --dry-run=server -f examples/agent-runtime/profile.yaml`
Expected: `agentprofile.ach.ackstorm.ai/standard configured (server dry run)`.

This needs a cluster with the updated CRD installed. If none is available, fall back to a client-side
schema check and say so in the commit — do not skip silently:
`kubectl apply --dry-run=client -f examples/agent-runtime/profile.yaml`

- [ ] **Step 5: Commit**

```bash
git add examples/agent-runtime/profile.yaml examples/agent-runtime/README.md CHANGELOG.md
git commit -m "docs(achagent): document networkPolicy egress + runtimeClass hardening"
```

---

## Verification (whole feature)

- [ ] `make test-full` — all non-cluster tests (unit + envtest, race-enabled). Expected: PASS.
- [ ] `make qa-lint` — full golangci sweep. Expected: exit 0.
- [ ] `helm template deploy/helm/ach --set operator.enabled=true` renders the `networkpolicies`
      ClusterRole rule.
- [ ] `git diff --stat main` touches only the files in the File Structure table.

## Out of scope (deliberate — do not add)

- **Ingress restriction.** The exfiltration path is egress; `expose.service` is already opt-in.
- **`enabled: bool` on the block.** Presence is the opt-in; the only thing a bool buys is "keep the
  rules but switch them off", which nobody asked for.
- **Auto-deriving the ACH peer from `ach.baseUrl`.** Impossible portably — no FQDN peers upstream.
- **A kube-dns-pinned DNS rule.** Distro-variant and fails closed silently; see the `ponytail:`
  comment on `dnsEgressRule` for the upgrade path.
- **CNI enforcement detection.** The operator cannot reliably tell whether the CNI honours policy;
  documented as an operator-of-the-cluster responsibility instead.
- **Cutting a release.** Follow the repo's release ritual separately when the maintainer decides;
  this plan leaves the entries under `[Unreleased]`.
