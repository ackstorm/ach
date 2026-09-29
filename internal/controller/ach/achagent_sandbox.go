// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/agentrender"
)

// agent-sandbox (kubernetes-sigs, v1.0.x) is a cluster prerequisite; its kinds are handled as
// unstructured so ach takes no Go dependency on it. Field paths: SandboxTemplate.spec.{podTemplate,
// networkPolicyManagement}, SandboxWarmPool.spec.{replicas,sandboxTemplateRef.name}.
var (
	sandboxTemplateGVK = schema.GroupVersionKind{Group: "extensions.agents.x-k8s.io", Version: "v1beta1", Kind: "SandboxTemplate"}
	sandboxWarmPoolGVK = schema.GroupVersionKind{Group: "extensions.agents.x-k8s.io", Version: "v1beta1", Kind: "SandboxWarmPool"}
)

const (
	sandboxRoleLabelKey = "ach.ackstorm.ai/role"
	sandboxKeyDataKey   = "key"
	sandboxContainer    = "engine"
)

const reasonSandboxSAMissing = "SandboxServiceAccountMissing"

var mcpEnvRef = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

// sandboxKeySecretName is the per-agent Secret holding K, the root of every sandbox credential.
func sandboxKeySecretName(agentName string) string {
	return agentResourceName(agentName) + "-sandbox-key"
}

// ensureSandboxKey returns K, creating the Secret once if absent. It never rotates: rotation is
// deleting the Secret by hand (the salted config hash rolls the harness pod on the next pass).
func (r *ACHAgentReconciler) ensureSandboxKey(ctx context.Context, a *achv1alpha1.ACHAgent) (string, error) {
	key := types.NamespacedName{Namespace: a.Namespace, Name: sandboxKeySecretName(a.Name)}
	var s corev1.Secret
	err := r.APIReader.Get(ctx, key, &s)
	if err == nil {
		if k := string(s.Data[sandboxKeyDataKey]); k != "" {
			return k, nil
		}
		return "", fmt.Errorf("secret %q has no %q key", key.Name, sandboxKeyDataKey)
	}
	if !apierrors.IsNotFound(err) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	k := hex.EncodeToString(raw) // 64 hex chars; the string is the HMAC key (ach-agent contract)
	s = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: a.Namespace, Labels: agentLabels(a)},
		Data:       map[string][]byte{sandboxKeyDataKey: []byte(k)},
	}
	if err := controllerutil.SetControllerReference(a, &s, r.Scheme); err != nil {
		return "", err
	}
	if err := r.Create(ctx, &s); err != nil {
		if apierrors.IsAlreadyExists(err) { // raced a concurrent reconcile: use the winner
			return r.ensureSandboxKey(ctx, a)
		}
		return "", err
	}
	return k, nil
}

// checkSandboxPrereqs returns a WorkloadApplied=False reason when the sandboxed agent cannot be
// applied: agent-sandbox CRDs not served, or the shared harness ServiceAccount absent.
func (r *ACHAgentReconciler) checkSandboxPrereqs(ctx context.Context, a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) (reason, msg string, err error) {
	if !r.sandboxCRDs {
		return "SandboxCRDsMissing", "agent-sandbox CRDs (extensions.agents.x-k8s.io) are not installed, or were installed after the operator started", nil
	}
	name := p.Spec.Sandbox.ServiceAccountName
	if err := r.Get(ctx, types.NamespacedName{Namespace: a.Namespace, Name: name}, &corev1.ServiceAccount{}); err != nil {
		if apierrors.IsNotFound(err) {
			return reasonSandboxSAMissing, fmt.Sprintf("ServiceAccount %q not found (chart agentSandbox.enabled creates it)", name), nil
		}
		return "", "", err
	}
	return "", "", nil
}

// applySandbox upserts the SandboxTemplate + SandboxWarmPool, or prunes them when the agent is
// not sandboxed (owner-ref GC only fires on ACHAgent delete).
func (r *ACHAgentReconciler) applySandbox(ctx context.Context, a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, key string) error {
	if !agentrender.SandboxedPlacement(*p, *a) {
		if !r.sandboxCRDs {
			return nil
		}
		for _, gvk := range []schema.GroupVersionKind{sandboxTemplateGVK, sandboxWarmPoolGVK} {
			u := &unstructured.Unstructured{}
			u.SetGroupVersionKind(gvk)
			u.SetNamespace(a.Namespace)
			u.SetName(agentrender.SandboxName(a.Name))
			if err := r.prune(ctx, u); err != nil {
				return err
			}
		}
		return nil
	}
	for _, desired := range []*unstructured.Unstructured{buildSandboxTemplate(a, p, key), buildSandboxWarmPool(a, p)} {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(desired.GroupVersionKind())
		obj.SetNamespace(desired.GetNamespace())
		obj.SetName(desired.GetName())
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
			obj.SetLabels(desired.GetLabels())
			obj.Object["spec"] = desired.Object["spec"]
			return controllerutil.SetControllerReference(a, obj, r.Scheme)
		}); err != nil {
			return fmt.Errorf("%s: %w", desired.GetKind(), err)
		}
	}
	return nil
}

func buildSandboxWarmPool(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) *unstructured.Unstructured {
	replicas := int64(1)
	if r := p.Spec.Sandbox.WarmPoolReplicas; r != nil {
		replicas = int64(*r)
	}
	u := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
		"replicas":           replicas,
		"sandboxTemplateRef": map[string]any{"name": agentrender.SandboxName(a.Name)},
	}}}
	u.SetGroupVersionKind(sandboxWarmPoolGVK)
	u.SetNamespace(a.Namespace)
	u.SetName(agentrender.SandboxName(a.Name))
	u.SetLabels(agentLabels(a))
	return u
}

// sandboxProxyEnv are delivered per launch by the harness (egress), never baked into the template.
var sandboxProxyEnv = map[string]bool{
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true,
}

// sandboxEnvNames is the closed set of pod-level env the engine may see: engine.forwardEnv plus
// the env names mcpServers reference. These are the only secrets allowed in the sandbox (S10).
func sandboxEnvNames(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) []string {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n == "" || seen[n] || strings.HasPrefix(n, "ACH_") || sandboxProxyEnv[strings.ToUpper(n)] {
			return
		}
		seen[n] = true
		names = append(names, n)
	}
	if eng := agentrender.ResolveEngine(a.Spec.Engine, p.Spec.Achagent.Engine); eng != nil {
		for _, n := range eng.ForwardEnv {
			add(n)
		}
	}
	for _, s := range a.Spec.MCPServers {
		if s.Local != nil {
			for _, n := range s.Local.Env {
				add(n)
			}
		}
		if s.Remote != nil {
			for _, h := range s.Remote.Headers {
				for _, m := range mcpEnvRef.FindAllStringSubmatch(h, -1) {
					add(m[1])
				}
			}
		}
	}
	return names
}

func buildSandboxTemplate(a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, key string) *unstructured.Unstructured {
	sb := p.Spec.Sandbox
	env := []corev1.EnvVar{
		{Name: "ACH_ENGINE_LISTEN", Value: "tcp"},
		{Name: "ACH_ENGINE_EXEC_PORT", Value: fmt.Sprint(agentrender.SandboxEnginePort)},
		{Name: "ACH_ENGINE_HEALTH_PORT", Value: fmt.Sprint(agentrender.SandboxHealthPort)},
		{Name: "ACH_SANDBOX_HOME", Value: agentrender.SandboxHome},
		{Name: "HOME", Value: agentrender.SandboxHome},
		{Name: "ACH_SANDBOX_VERIFY_KEY", Value: agentrender.SandboxVerifyKey(key)},
	}
	if sb.Sessions.MaxArchiveBytes != nil {
		env = append(env, corev1.EnvVar{Name: "ACH_SANDBOX_MAX_ARCHIVE_BYTES", Value: fmt.Sprint(*sb.Sessions.MaxArchiveBytes)})
	}
	podEnv := map[string]corev1.EnvVar{}
	for _, e := range agentrender.ResolveEnv(a.Spec.Env, p.Spec.Env) {
		podEnv[e.Name] = e
	}
	for _, n := range sandboxEnvNames(a, p) {
		if e, ok := podEnv[n]; ok {
			env = append(env, e)
		}
	}

	falseVal, trueVal := false, true
	uid := agentUID
	container := corev1.Container{
		Name:  sandboxContainer,
		Image: agentrender.ResolveImage(a.Spec.Image, p.Spec.Achagent.Image),
		Args:  []string{"--role", "engine"},
		Env:   env,
		Ports: []corev1.ContainerPort{
			{Name: "engine", ContainerPort: agentrender.SandboxEnginePort, Protocol: corev1.ProtocolTCP},
			{Name: "health", ContainerPort: agentrender.SandboxHealthPort, Protocol: corev1.ProtocolTCP},
		},
		ReadinessProbe: sandboxProbe("/readyz"),
		LivenessProbe:  sandboxProbe("/healthz"),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &falseVal,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
	if sb.Resources != nil {
		container.Resources = *sb.Resources.DeepCopy()
	}
	spec := corev1.PodSpec{
		AutomountServiceAccountToken: &falseVal,
		EnableServiceLinks:           &falseVal,
		ImagePullSecrets:             p.Spec.ImagePullSecrets,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: &trueVal, RunAsUser: &uid, RunAsGroup: &uid, FSGroup: &uid,
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		Containers: []corev1.Container{container},
	}
	if sb.RuntimeClassName != "" {
		spec.RuntimeClassName = &sb.RuntimeClassName
	}
	specMap, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&spec)

	u := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
		// Unmanaged: the Managed default blocks RFC1918 egress and admits ingress only from
		// sandbox-router, which would cut sandbox⇄harness if enforcement ever runs (no NetworkPolicy here).
		"networkPolicyManagement": "Unmanaged",
		"podTemplate": map[string]any{
			"metadata": map[string]any{"labels": map[string]any{
				agentLabelKey:       a.Name,
				sandboxRoleLabelKey: "sandbox",
			}},
			"spec": specMap,
		},
	}}}
	u.SetGroupVersionKind(sandboxTemplateGVK)
	u.SetNamespace(a.Namespace)
	u.SetName(agentrender.SandboxName(a.Name))
	u.SetLabels(agentLabels(a))
	return u
}

func sandboxProbe(path string) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:  corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(agentrender.SandboxHealthPort)}},
		PeriodSeconds: 10, FailureThreshold: 3,
	}
}

// sandboxCRDsServed reports whether the agent-sandbox extension kinds are served. Checked once at
// startup: an operator on a cluster without agent-sandbox still runs; sandboxed agents get
// WorkloadApplied=False/SandboxCRDsMissing.
func sandboxCRDsServed(m meta.RESTMapper) bool {
	for _, gvk := range []schema.GroupVersionKind{sandboxTemplateGVK, sandboxWarmPoolGVK} {
		if _, err := m.RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
			return false
		}
	}
	return true
}
