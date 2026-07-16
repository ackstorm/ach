// SPDX-License-Identifier: Apache-2.0

// Package ach — ACHAgentReconciler renders an ACHAgent (+ its AgentProfile) into the
// agent-config-v1 ConfigMap and a single-replica Deployment. The ach-agent harness
// self-hydrates against ACH at boot, so this reconciler owns NO init container and derives
// WorkloadReady from pod.status (probe-backed) only.
package ach

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	"github.com/ackstorm/ach/internal/agentrender"
	achdb "github.com/ackstorm/ach/internal/db"
)

const (
	condReady                  = "Ready"
	condProfileResolved        = "ProfileResolved"
	condIdentityResolved       = "IdentityResolved"
	condChannelSecretsResolved = "ChannelSecretsResolved"
	condWorkloadApplied        = "WorkloadApplied"
	condWorkloadReady          = "WorkloadReady"
)

var requiredConds = []string{condProfileResolved, condIdentityResolved, condChannelSecretsResolved, condWorkloadApplied, condWorkloadReady}

// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=achagents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=achagents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=agentprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

// ACHAgentReconciler reconciles ACHAgent objects. APIReader is the UNCACHED client used for
// Secret content reads (so Secret bodies are never cached in this shared binary).
type ACHAgentReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme

	// DB is the Postgres pool for the achagents projection. Nil in envtest
	// (k8s-only suite) — every projection call is nil-gated.
	DB *pgxpool.Pool

	// PublicBaseURL is the externally reachable gateway origin (e.g.
	// https://ach.example.com), used to render status.gatewayURL as a full
	// URL. Empty => status.gatewayURL is the path-only form. Caller resolves
	// this from ACH_PUBLIC_BASE_URL, falling back to ACH_BASE_URL (same
	// origin in the common single-ingress deployment; ACH_PUBLIC_BASE_URL
	// stays available to override for a split-origin setup where the public
	// webhook ingress differs from platform-api's own origin).
	PublicBaseURL string

	// DefaultAchBaseURL is the operator-level fallback ACH base URL (from
	// ACH_BASE_URL) used when neither ACHAgent.spec.ach nor AgentProfile.spec.ach
	// sets baseUrl. Empty + no per-object override => the agent is blocked (Render
	// errors) because it has no ACH to hydrate against.
	DefaultAchBaseURL string
}

//nolint:gocyclo // Single linear resolve→render→apply→status flow; splitting scatters status ordering.
func (r *ACHAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("achagent", req.NamespacedName)

	var agent achv1alpha1.ACHAgent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		if apierrors.IsNotFound(err) {
			// CR gone: drop its projection row so the gateway/UI stop
			// listing it. Key comes from req — no spec needed.
			return r.deleteAgentProjection(ctx, req.Namespace, req.Name)
		}
		return ctrl.Result{}, fmt.Errorf("get achagent: %w", err)
	}
	if !agent.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	logger.Info("reconciling achagent")

	var conds []metav1.Condition

	// 1. AgentProfile.
	var profile achv1alpha1.AgentProfile
	if err := r.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: agent.Spec.ProfileRef.Name}, &profile); err != nil {
		if apierrors.IsNotFound(err) {
			setCond(&conds, condProfileResolved, metav1.ConditionFalse, "ProfileNotFound", fmt.Sprintf("AgentProfile %q not found", agent.Spec.ProfileRef.Name), agent.Generation)
			return r.finish(ctx, &agent, conds)
		}
		return ctrl.Result{}, fmt.Errorf("get agentprofile: %w", err)
	}
	setCond(&conds, condProfileResolved, metav1.ConditionTrue, "ProfileFound", "", agent.Generation)

	// 2. Identity Secret + key (APIReader — uncached).
	var ekSecret corev1.Secret
	if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: agent.Spec.Identity.SecretRef.Name}, &ekSecret); err != nil {
		if apierrors.IsNotFound(err) {
			setCond(&conds, condIdentityResolved, metav1.ConditionFalse, "IdentitySecretNotFound", fmt.Sprintf("identity secret %q not found", agent.Spec.Identity.SecretRef.Name), agent.Generation)
			return r.finish(ctx, &agent, conds)
		}
		return ctrl.Result{}, fmt.Errorf("get identity secret: %w", err)
	}
	if _, ok := ekSecret.Data[agent.Spec.Identity.SecretRef.Key]; !ok {
		setCond(&conds, condIdentityResolved, metav1.ConditionFalse, "IdentityKeyMissing", fmt.Sprintf("secret %q has no key %q", agent.Spec.Identity.SecretRef.Name, agent.Spec.Identity.SecretRef.Key), agent.Generation)
		return r.finish(ctx, &agent, conds)
	}
	setCond(&conds, condIdentityResolved, metav1.ConditionTrue, "IdentityFound", "", agent.Generation)

	// 3. Channel Secrets + keys.
	refSecrets := agentrender.ReferencedSecrets(agent)
	if failReason, failMsg := r.checkChannelSecrets(ctx, agent.Namespace, refSecrets); failReason != "" {
		setCond(&conds, condChannelSecretsResolved, metav1.ConditionFalse, failReason, failMsg, agent.Generation)
		return r.finish(ctx, &agent, conds)
	}
	setCond(&conds, condChannelSecretsResolved, metav1.ConditionTrue, "ChannelSecretsFound", "", agent.Generation)

	// 4. Render.
	cfg, err := agentrender.Render(profile, agent, r.DefaultAchBaseURL)
	if err != nil {
		setCond(&conds, condWorkloadApplied, metav1.ConditionFalse, "RenderFailed", err.Error(), agent.Generation)
		return r.finish(ctx, &agent, conds)
	}
	configJSON, err := agentrender.Marshal(cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("marshal config: %w", err)
	}

	// 5. Env (once) + salted secret hash + config hash.
	env := buildAgentEnv(&agent, &profile, r.DefaultAchBaseURL)
	envJSON, err := json.Marshal(env)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("marshal env: %w", err)
	}
	secretHash, err := r.hashSecrets(ctx, &agent, refSecrets)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("hash secrets: %w", err)
	}
	var podTemplateJSON []byte
	if profile.Spec.PodTemplate != nil {
		podTemplateJSON = profile.Spec.PodTemplate.Raw
	}
	configHash := computeConfigHash(configJSON, envJSON, podTemplateJSON, profile.Spec.Image, secretHash)

	// buildDeployment currently fails only on the podTemplate overlay, so mapping every error to
	// reason PodTemplateInvalid is correct today — revisit if the builder gains other error paths.
	// Built (and validated) before any child is applied, so a bad overlay leaves nothing applied
	// this pass — no ConfigMap/ServiceAccount/PVC mutation while the Deployment stays untouched.
	dep, err := buildDeployment(&agent, &profile, configHash, env)
	if err != nil {
		setCond(&conds, condWorkloadApplied, metav1.ConditionFalse, "PodTemplateInvalid", err.Error(), agent.Generation)
		return r.finish(ctx, &agent, conds)
	}

	// 6. Apply children.
	if err := r.apply(ctx, &agent, buildConfigMap(&agent, configJSON)); err != nil {
		return r.applyFail(ctx, &agent, conds, "ConfigMap", err)
	}
	if err := r.apply(ctx, &agent, buildServiceAccount(&agent)); err != nil {
		return r.applyFail(ctx, &agent, conds, "ServiceAccount", err)
	}
	if profile.Spec.Persistence != nil && profile.Spec.Persistence.Enabled {
		if err := r.applyPVC(ctx, &agent, &profile); err != nil {
			return r.applyFail(ctx, &agent, conds, "PVC", err)
		}
	}
	if err := r.apply(ctx, &agent, dep); err != nil {
		return r.applyFail(ctx, &agent, conds, "Deployment", err)
	}
	if needsService(&agent) {
		if err := r.apply(ctx, &agent, buildService(&agent, &profile)); err != nil {
			return r.applyFail(ctx, &agent, conds, "Service", err)
		}
	} else if err := r.pruneService(ctx, &agent); err != nil {
		// Converge expose.service true→false (and pre-feat orphans): owner-ref GC
		// only fires on ACHAgent delete, not when the owner stops desiring the child.
		return r.applyFail(ctx, &agent, conds, "Service", err)
	}
	if needsNetworkPolicy(&profile) {
		if err := r.apply(ctx, &agent, buildNetworkPolicy(&agent, &profile)); err != nil {
			return r.applyFail(ctx, &agent, conds, "NetworkPolicy", err)
		}
	} else if err := r.pruneNetworkPolicy(ctx, &agent); err != nil {
		// Converge profile networkPolicy present→absent, same as pruneService: owner-ref
		// GC only fires on ACHAgent delete, not when the owner stops desiring the child.
		return r.applyFail(ctx, &agent, conds, "NetworkPolicy", err)
	}
	setCond(&conds, condWorkloadApplied, metav1.ConditionTrue, "WorkloadApplied", "", agent.Generation)

	// 7. WorkloadReady from pod.status (probe-backed).
	r.deriveWorkloadReady(ctx, &agent, configHash, &conds)

	// 8. Ready is aggregated in finish() every pass.
	return r.finish(ctx, &agent, conds)
}

func (r *ACHAgentReconciler) checkChannelSecrets(ctx context.Context, ns string, refSecrets map[string][]string) (reason, msg string) {
	names := slices.Sorted(maps.Keys(refSecrets))
	for _, name := range names {
		var s corev1.Secret
		if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &s); err != nil {
			if apierrors.IsNotFound(err) {
				return "ChannelSecretNotFound", fmt.Sprintf("channel secret %q not found", name)
			}
			// Transient — surface as a non-terminal error via the caller returning it.
			return "ChannelSecretError", fmt.Sprintf("get channel secret %q: %v", name, err)
		}
		for _, key := range refSecrets[name] {
			if _, ok := s.Data[key]; !ok {
				return "ChannelSecretKeyMissing", fmt.Sprintf("secret %q has no key %q", name, key)
			}
		}
	}
	return "", ""
}

// apply upserts a child with an owner ref (GC on ACHAgent delete).
func (r *ACHAgentReconciler) apply(ctx context.Context, owner *achv1alpha1.ACHAgent, desired client.Object) error {
	obj := desired.DeepCopyObject().(client.Object)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, obj, func() error {
		copySpec(obj, desired)
		return controllerutil.SetControllerReference(owner, obj, r.Scheme)
	})
	return err
}

// pruneService deletes the ClusterIP Service when the agent no longer opts in
// (expose.service absent/false). Idempotent — NotFound is a no-op.
func (r *ACHAgentReconciler) pruneService(ctx context.Context, a *achv1alpha1.ACHAgent) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace}}
	if err := r.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// pruneNetworkPolicy deletes the egress policy when the profile no longer declares a
// networkPolicy block. Idempotent — NotFound is a no-op.
func (r *ACHAgentReconciler) pruneNetworkPolicy(ctx context.Context, a *achv1alpha1.ACHAgent) error {
	np := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: agentResourceName(a.Name), Namespace: a.Namespace}}
	if err := r.Delete(ctx, np); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// applyPVC creates the PVC once. When retainPolicy=Retain the PVC gets NO owner-ref, so it
// survives ACHAgent deletion (create-once; PVC spec is immutable). A changed size is logged,
// not auto-applied.
func (r *ACHAgentReconciler) applyPVC(ctx context.Context, owner *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile) error {
	pvc, err := buildPVC(owner, p)
	if err != nil {
		return err
	}
	var existing corev1.PersistentVolumeClaim
	getErr := r.Get(ctx, types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}, &existing)
	if getErr == nil {
		want := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		have := existing.Spec.Resources.Requests[corev1.ResourceStorage]
		if want.Cmp(have) != 0 {
			log.FromContext(ctx).Info("PVC size change is not auto-applied (create-once); expand manually if the storage class allows it", "pvc", pvc.Name, "want", want.String(), "have", have.String())
		}
		return nil
	}
	if !apierrors.IsNotFound(getErr) {
		return getErr
	}
	retain := p.Spec.Persistence.RetainPolicy != nil && *p.Spec.Persistence.RetainPolicy == "Retain"
	if !retain {
		if err := controllerutil.SetControllerReference(owner, pvc, r.Scheme); err != nil {
			return err
		}
	}
	if err := r.Create(ctx, pvc); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *ACHAgentReconciler) applyFail(ctx context.Context, a *achv1alpha1.ACHAgent, conds []metav1.Condition, what string, err error) (ctrl.Result, error) {
	setCond(&conds, condWorkloadApplied, metav1.ConditionFalse, "ApplyFailed", fmt.Sprintf("apply %s: %v", what, err), a.Generation)
	return r.finish(ctx, a, conds)
}

func (r *ACHAgentReconciler) deriveWorkloadReady(ctx context.Context, a *achv1alpha1.ACHAgent, configHash string, conds *[]metav1.Condition) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(a.Namespace), client.MatchingLabels{agentLabelKey: a.Name}); err != nil {
		setCond(conds, condWorkloadReady, metav1.ConditionFalse, "ListFailed", err.Error(), a.Generation)
		return
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Annotations[configHashAnnotation] != configHash {
			continue // stale generation
		}
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				setCond(conds, condWorkloadReady, metav1.ConditionTrue, "PodReady", "", a.Generation)
				return
			}
		}
	}
	setCond(conds, condWorkloadReady, metav1.ConditionFalse, "PodNotReady", "no current-generation pod is Ready", a.Generation)
}

// hashSecrets returns a salted (HMAC keyed by the ACHAgent UID) digest over the .Data of the
// identity secret + referenced channel-secret keys. A content rotation rolls the pod; the digest
// is not reversible to the token values. Reads via APIReader (uncached).
func (r *ACHAgentReconciler) hashSecrets(ctx context.Context, a *achv1alpha1.ACHAgent, refSecrets map[string][]string) (string, error) {
	// name → keys to hash (identity secret's key + each channel secret's referenced keys).
	want := map[string]map[string]struct{}{}
	add := func(name, key string) {
		if want[name] == nil {
			want[name] = map[string]struct{}{}
		}
		want[name][key] = struct{}{}
	}
	add(a.Spec.Identity.SecretRef.Name, a.Spec.Identity.SecretRef.Key)
	for name, keys := range refSecrets {
		for _, k := range keys {
			add(name, k)
		}
	}
	names := slices.Sorted(maps.Keys(want))

	mac := hmac.New(sha256.New, []byte(a.UID))
	for _, name := range names {
		var s corev1.Secret
		if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: a.Namespace, Name: name}, &s); err != nil {
			if apierrors.IsNotFound(err) {
				mac.Write([]byte(name))
				mac.Write([]byte("\x00missing\x00"))
				continue
			}
			return "", err
		}
		keys := slices.Sorted(maps.Keys(want[name]))
		for _, k := range keys {
			mac.Write([]byte(name))
			mac.Write([]byte{0})
			mac.Write([]byte(k))
			mac.Write([]byte{0})
			mac.Write(s.Data[k]) // absent key → nil bytes; content rotation still changes the mac
			mac.Write([]byte{0})
		}
	}
	return hex.EncodeToString(mac.Sum(nil))[:16], nil
}

// finish merges this-pass conditions, forces any un-evaluated required condition to Unknown
// (so it cannot read stale-True), recomputes Ready from the merged set, and persists status.
func (r *ACHAgentReconciler) finish(ctx context.Context, a *achv1alpha1.ACHAgent, conds []metav1.Condition) (ctrl.Result, error) {
	for _, t := range requiredConds {
		if apimeta.FindStatusCondition(conds, t) == nil {
			setCond(&conds, t, metav1.ConditionUnknown, "NotEvaluated", "reconcile bailed before evaluating this condition", a.Generation)
		}
	}
	var fresh achv1alpha1.ACHAgent
	if err := r.Get(ctx, types.NamespacedName{Namespace: a.Namespace, Name: a.Name}, &fresh); err != nil {
		return ctrl.Result{}, err
	}
	for _, c := range conds {
		setCond(&fresh.Status.Conditions, c.Type, c.Status, c.Reason, c.Message, c.ObservedGeneration)
	}
	if allTrue(fresh.Status.Conditions, requiredConds...) {
		setCond(&fresh.Status.Conditions, condReady, metav1.ConditionTrue, "Ready", "", a.Generation)
	} else {
		reason, msg := "NotReady", "one or more required conditions are not satisfied"
		for _, t := range requiredConds {
			if c := apimeta.FindStatusCondition(fresh.Status.Conditions, t); c == nil || c.Status != metav1.ConditionTrue {
				if c != nil {
					reason, msg = c.Reason, c.Message
				}
				break
			}
		}
		setCond(&fresh.Status.Conditions, condReady, metav1.ConditionFalse, reason, msg, a.Generation)
	}
	fresh.Status.ObservedGeneration = fresh.Generation
	fresh.Status.GatewayURL = agentGatewayURL(&fresh, r.PublicBaseURL)

	// Dual-write the achagents projection (read model for gateway + UI).
	ready := apimeta.IsStatusConditionTrue(fresh.Status.Conditions, condReady)
	if err := r.writeAgentProjection(ctx, &fresh, ready); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Status().Update(ctx, &fresh); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}
	return ctrl.Result{}, nil
}

func setCond(conds *[]metav1.Condition, t string, s metav1.ConditionStatus, reason, msg string, gen int64) {
	apimeta.SetStatusCondition(conds, metav1.Condition{Type: t, Status: s, Reason: reason, Message: msg, ObservedGeneration: gen})
}

func allTrue(conds []metav1.Condition, condTypes ...string) bool {
	for _, t := range condTypes {
		c := apimeta.FindStatusCondition(conds, t)
		if c == nil || c.Status != metav1.ConditionTrue {
			return false
		}
	}
	return true
}

func (r *ACHAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.ACHAgent{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Watches(&achv1alpha1.AgentProfile{}, handler.EnqueueRequestsFromMapFunc(r.agentsForProfile)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.agentsForSecret), builder.OnlyMetadata).
		Named("achagent").
		Complete(r)
}

func (r *ACHAgentReconciler) agentsForProfile(ctx context.Context, obj client.Object) []reconcile.Request {
	var list achv1alpha1.ACHAgentList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		if list.Items[i].Spec.ProfileRef.Name == obj.GetName() {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: list.Items[i].Namespace, Name: list.Items[i].Name}})
		}
	}
	return reqs
}

func (r *ACHAgentReconciler) agentsForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	var list achv1alpha1.ACHAgentList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	name := obj.GetName()
	var reqs []reconcile.Request
	for i := range list.Items {
		a := &list.Items[i]
		hit := a.Spec.Identity.SecretRef.Name == name
		if !hit {
			if _, ok := agentrender.ReferencedSecrets(*a)[name]; ok {
				hit = true
			}
		}
		if hit {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: a.Namespace, Name: a.Name}})
		}
	}
	return reqs
}

// agentGatewayURL returns the inbound gateway URL for an agent, or "" when the
// agent has not opted into gateway exposure (expose.gateway). The path carries
// the agent's Service name (agentResourceName), not the CR name — the gateway
// forwards /agents/{ns}/{service}/… to that Service verbatim (webhook or a2a).
// baseURL (from PublicBaseURL) is optional: when set, the result is a full URL;
// when empty, the result is the path-only form the caller prefixes with their
// own ingress host. Pure (no I/O) so it is unit-tested directly.
func agentGatewayURL(a *achv1alpha1.ACHAgent, baseURL string) string {
	if !exposeGateway(a) {
		return ""
	}
	path := fmt.Sprintf("/agents/%s/%s", a.Namespace, agentResourceName(a.Name))
	if baseURL == "" {
		return path
	}
	return strings.TrimRight(baseURL, "/") + path
}

// agentProjectionRow builds the achagents projection row from an ACHAgent's
// spec + its aggregate Ready status. Pure (no I/O) so it is unit-tested
// without a DB. Service coords are derived from the same helpers the
// reconciler uses to build the Service, so they match what buildService emits.
func agentProjectionRow(a *achv1alpha1.ACHAgent, ready bool) achdb.AgentRow {
	row := achdb.AgentRow{
		Namespace:       a.Namespace,
		Name:            a.Name,
		ProfileRef:      a.Spec.ProfileRef.Name,
		Exposed:         exposeGateway(a),
		Ready:           ready,
		ResourceVersion: a.ResourceVersion,
	}
	for _, ch := range a.Spec.Channels {
		row.Channels = append(row.Channels, achdb.ChannelSummary{Name: ch.Name, Type: ch.Type, Source: ch.Source})
	}
	if needsService(a) {
		row.ServiceName = agentResourceName(a.Name)
		row.ServicePort = 8080 // buildService pins the Service port to 8080
	}
	return row
}

// writeAgentProjection upserts the achagents row + NOTIFY in one tx. Nil-gated.
func (r *ACHAgentReconciler) writeAgentProjection(ctx context.Context, a *achv1alpha1.ACHAgent, ready bool) error {
	if r.DB == nil {
		return nil
	}
	row := agentProjectionRow(a, ready)
	payload := fmt.Sprintf("%s/%s", a.Namespace, a.Name)
	if err := achdb.WithTxNotify(ctx, r.DB, achdb.AgentsChannel, payload, func(tx pgx.Tx) error {
		return achdb.UpsertAgentTx(ctx, tx, row)
	}); err != nil {
		return fmt.Errorf("db upsert agent projection: %w", err)
	}
	return nil
}

// deleteAgentProjection removes the achagents row + NOTIFY when the CR is gone.
// Keyed by (ns, name) from the reconcile request — no spec needed. Nil-gated.
func (r *ACHAgentReconciler) deleteAgentProjection(ctx context.Context, ns, name string) (ctrl.Result, error) {
	if r.DB == nil {
		return ctrl.Result{}, nil
	}
	payload := ns + "/" + name
	if err := achdb.WithTxNotify(ctx, r.DB, achdb.AgentsChannel, payload, func(tx pgx.Tx) error {
		return achdb.DeleteAgentTx(ctx, tx, ns, name)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("db delete agent projection: %w", err)
	}
	return ctrl.Result{}, nil
}
