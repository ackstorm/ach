// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"bytes"
	"context"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// The operator owns Workspace request CRUD and status updates. Its StatefulSet, Service,
// and Pod grants are declared with the ACHAgent controller markers and cover child mutations
// here as well as the matching limited permissions delegated to the Harness.
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=workspaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ach.ackstorm.ai,resources=workspaces/status,verbs=get;update;patch

type WorkspaceReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
}

func (r *WorkspaceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var workspace achv1alpha1.Workspace
	if err := r.APIReader.Get(ctx, req.NamespacedName, &workspace); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if workspace.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}
	gen := workspace.Generation
	conds := workspaceUnevaluatedConditions(gen)
	var agent achv1alpha1.ACHAgent
	agentKey := types.NamespacedName{Namespace: workspace.Namespace, Name: workspace.Spec.AgentRef.Name}
	if err := r.APIReader.Get(ctx, agentKey, &agent); err != nil {
		if apierrors.IsNotFound(err) {
			setCond(&conds, "OwnerResolved", metav1.ConditionFalse, "AgentNotFound", "referenced ACHAgent does not exist", gen)
			setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "OwnerUnavailable", "referenced ACHAgent does not exist", gen)
			return r.finishWorkspace(ctx, &workspace, conds, "")
		}
		return reconcile.Result{}, err
	}
	if agent.UID != types.UID(workspace.Spec.AgentRef.UID) {
		setCond(&conds, "OwnerResolved", metav1.ConditionFalse, "AgentUIDMismatch", "referenced ACHAgent UID is not the current object incarnation", gen)
		setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "WorkspaceConflict", "referenced ACHAgent UID is not the current object incarnation", gen)
		return r.finishWorkspace(ctx, &workspace, conds, "")
	}
	if agent.DeletionTimestamp != nil {
		setCond(&conds, "OwnerResolved", metav1.ConditionFalse, "AgentDeleting", "referenced ACHAgent is deleting", gen)
		setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "OwnerUnavailable", "referenced ACHAgent is deleting", gen)
		return r.finishWorkspace(ctx, &workspace, conds, "")
	}
	var profile achv1alpha1.AgentProfile
	profileKey := types.NamespacedName{Namespace: workspace.Namespace, Name: agent.Spec.ProfileRef.Name}
	if agent.Spec.ProfileRef.Name == "" {
		setCond(&conds, "OwnerResolved", metav1.ConditionFalse, "ProfileNotFound", "current AgentProfile is unavailable", gen)
		setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "OwnerUnavailable", "current AgentProfile is unavailable", gen)
		return r.finishWorkspace(ctx, &workspace, conds, "")
	}
	if err := r.APIReader.Get(ctx, profileKey, &profile); err != nil {
		if apierrors.IsNotFound(err) {
			setCond(&conds, "OwnerResolved", metav1.ConditionFalse, "ProfileNotFound", "current AgentProfile is unavailable", gen)
			setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "OwnerUnavailable", "current AgentProfile is unavailable", gen)
			return r.finishWorkspace(ctx, &workspace, conds, "")
		}
		return reconcile.Result{}, err
	}
	if profile.DeletionTimestamp != nil {
		setCond(&conds, "OwnerResolved", metav1.ConditionFalse, "ProfileDeleting", "current AgentProfile is deleting", gen)
		setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "OwnerUnavailable", "current AgentProfile is deleting", gen)
		return r.finishWorkspace(ctx, &workspace, conds, "")
	}
	if err := validateWorkspaceIdentity(&workspace, &agent); err != nil {
		setCond(&conds, "OwnerResolved", metav1.ConditionTrue, "OwnerResolved", "current ACHAgent and AgentProfile resolved", gen)
		setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "WorkspaceConflict", err.Error(), gen)
		return r.finishWorkspace(ctx, &workspace, conds, types.UID(workspace.Status.StatefulSetUID))
	}
	setCond(&conds, "OwnerResolved", metav1.ConditionTrue, "OwnerResolved", "current ACHAgent and AgentProfile resolved", gen)
	key, err := readWorkspaceKey(ctx, r.APIReader, &agent)
	if err != nil {
		setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "WorkspaceKeyUnavailable", "ACHAgent workspace key is unavailable or invalid", gen)
		return r.finishWorkspace(ctx, &workspace, conds, "")
	}
	desiredSTS, err := buildWorkspaceStatefulSet(&workspace, &agent, &profile, workspaceEngineVerifyKey(key))
	if err != nil {
		setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "ExecutionRenderFailed", "execution workload could not be built from the current profile", gen)
		return r.finishWorkspace(ctx, &workspace, conds, "")
	}
	desiredService := buildWorkspaceService(&workspace, &agent)
	var currentProfile achv1alpha1.AgentProfile
	if err := r.APIReader.Get(ctx, profileKey, &currentProfile); err != nil {
		return reconcile.Result{}, err
	}
	var currentAgent achv1alpha1.ACHAgent
	if err := r.APIReader.Get(ctx, agentKey, &currentAgent); err != nil {
		return reconcile.Result{}, err
	}
	currentKey, err := readWorkspaceKey(ctx, r.APIReader, &currentAgent)
	if err != nil {
		return reconcile.Result{}, err
	}
	if currentAgent.UID != agent.UID || currentAgent.ResourceVersion != agent.ResourceVersion || currentAgent.Spec.ProfileRef.Name != agent.Spec.ProfileRef.Name || currentProfile.ResourceVersion != profile.ResourceVersion || !bytes.Equal(currentKey, key) {
		return reconcile.Result{Requeue: true}, nil
	}
	observedSTS, updatePending, err := r.reconcileWorkspaceWorkload(ctx, &workspace, &agent, desiredSTS, desiredService)
	if err != nil {
		if isWorkspaceConflict(err) {
			setCond(&conds, "WorkloadApplied", metav1.ConditionFalse, "WorkspaceConflict", err.Error(), gen)
			if updatePending {
				setCond(&conds, "UpdatePending", metav1.ConditionTrue, "WorkloadDrift", "execution profile template differs and cannot be safely refreshed yet", gen)
			} else {
				setCond(&conds, "UpdatePending", metav1.ConditionFalse, "NoPendingUpdate", "no update was applied", gen)
			}
			// A rejected observation is not evidence that the current StatefulSet
			// is authorized for later idempotent adoption or idle refresh. Preserve
			// only the binding that was already present on this Workspace.
			return r.finishWorkspace(ctx, &workspace, conds, types.UID(workspace.Status.StatefulSetUID))
		}
		return reconcile.Result{}, err
	}
	setCond(&conds, "WorkloadApplied", metav1.ConditionTrue, "Applied", "requested Workspace replica state is observed or applied", gen)
	if updatePending {
		setCond(&conds, "UpdatePending", metav1.ConditionTrue, "WorkloadDrift", "execution profile template differs and cannot be safely refreshed yet", gen)
	} else {
		setCond(&conds, "UpdatePending", metav1.ConditionFalse, "UpToDate", "execution template matches the current profile", gen)
	}
	ready, err := r.workspaceReady(ctx, observedSTS, &agent, &workspace)
	if err != nil {
		return reconcile.Result{}, err
	}
	if ready {
		setCond(&conds, "WorkloadReady", metav1.ConditionTrue, "Ready", "one current execution Pod is Ready", gen)
	} else {
		setCond(&conds, "WorkloadReady", metav1.ConditionFalse, "NotReady", "execution workload is sleeping or not ready", gen)
	}
	return r.finishWorkspace(ctx, &workspace, conds, uidOfStatefulSet(observedSTS))
}

func (r *WorkspaceReconciler) finishWorkspace(ctx context.Context, evaluated *achv1alpha1.Workspace, conds []metav1.Condition, statefulSetUID types.UID) (reconcile.Result, error) {
	var current achv1alpha1.Workspace
	key := types.NamespacedName{Namespace: evaluated.Namespace, Name: evaluated.Name}
	if err := r.APIReader.Get(ctx, key, &current); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if current.UID != evaluated.UID || current.Generation != evaluated.Generation || current.DeletionTimestamp != nil {
		return reconcile.Result{Requeue: true}, nil
	}
	desiredStatus := achv1alpha1.WorkspaceStatus{ObservedGeneration: evaluated.Generation, StatefulSetUID: string(statefulSetUID), Conditions: conds}
	for i := range desiredStatus.Conditions {
		if previous := apimeta.FindStatusCondition(current.Status.Conditions, desiredStatus.Conditions[i].Type); previous != nil && previous.Status == desiredStatus.Conditions[i].Status {
			desiredStatus.Conditions[i].LastTransitionTime = previous.LastTransitionTime
		}
	}
	if apiequality.Semantic.DeepEqual(current.Status, desiredStatus) {
		return reconcile.Result{}, nil
	}
	current.Status = desiredStatus
	if err := r.Status().Update(ctx, &current); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *WorkspaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&achv1alpha1.Workspace{}).
		Watches(&achv1alpha1.ACHAgent{}, handler.EnqueueRequestsFromMapFunc(r.workspacesForAgent)).
		Watches(&achv1alpha1.AgentProfile{}, handler.EnqueueRequestsFromMapFunc(r.workspacesForProfile)).
		Watches(&appsv1.StatefulSet{}, handler.EnqueueRequestsFromMapFunc(r.workspacesForWorkload)).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(r.workspacesForWorkload)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.workspacesForWorkload)).
		Named("workspace").Complete(r)
}

func (r *WorkspaceReconciler) workspacesForAgent(ctx context.Context, obj client.Object) []reconcile.Request {
	agent, ok := obj.(*achv1alpha1.ACHAgent)
	if !ok {
		return nil
	}
	var list achv1alpha1.WorkspaceList
	if err := r.List(ctx, &list, client.InNamespace(agent.Namespace)); err != nil {
		return nil
	}
	var out []reconcile.Request
	for i := range list.Items {
		w := &list.Items[i]
		if w.Spec.AgentRef.Name == agent.Name && w.Spec.AgentRef.UID == string(agent.UID) {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)})
		}
	}
	return out
}

func (r *WorkspaceReconciler) workspacesForProfile(ctx context.Context, obj client.Object) []reconcile.Request {
	profile, ok := obj.(*achv1alpha1.AgentProfile)
	if !ok {
		return nil
	}
	var agents achv1alpha1.ACHAgentList
	if err := r.List(ctx, &agents, client.InNamespace(profile.Namespace)); err != nil {
		return nil
	}
	byUID := make(map[string]*achv1alpha1.ACHAgent, len(agents.Items))
	for i := range agents.Items {
		if agents.Items[i].Spec.ProfileRef.Name == profile.Name {
			byUID[string(agents.Items[i].UID)] = &agents.Items[i]
		}
	}
	var list achv1alpha1.WorkspaceList
	if err := r.List(ctx, &list, client.InNamespace(profile.Namespace)); err != nil {
		return nil
	}
	var out []reconcile.Request
	for i := range list.Items {
		w := &list.Items[i]
		if agent := byUID[w.Spec.AgentRef.UID]; agent != nil && w.Spec.AgentRef.Name == agent.Name {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)})
		}
	}
	return dedupeWorkspaceRequests(out)
}

func (r *WorkspaceReconciler) workspacesForWorkload(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj == nil {
		return nil
	}
	var list achv1alpha1.WorkspaceList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	labels := obj.GetLabels()
	identityName := labels[workspaceNameLabel]
	identityUID := labels[workspaceAgentUIDLabel]
	identityRef := obj.GetAnnotations()[workspaceRefAnnotation]
	var out []reconcile.Request
	for i := range list.Items {
		w := &list.Items[i]
		name := workspaceResourceName(w.Spec.AgentRef.Name, w.Spec.WorkspaceRef)
		ordinalPod := false
		if pod, ok := obj.(*corev1.Pod); ok {
			ordinalPod = pod.Name == name+"-0"
		}
		if ordinalPod || (identityName == name && identityUID == w.Spec.AgentRef.UID && identityRef == w.Spec.WorkspaceRef) {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)})
		}
	}
	return dedupeWorkspaceRequests(out)
}

func workspaceUnevaluatedConditions(gen int64) []metav1.Condition {
	conditions := []metav1.Condition{}
	for _, typ := range []string{"OwnerResolved", "WorkloadApplied", "WorkloadReady", "UpdatePending"} {
		setCond(&conditions, typ, metav1.ConditionUnknown, "NotEvaluated", "condition has not been evaluated for this generation", gen)
	}
	return conditions
}

func (r *WorkspaceReconciler) workspaceReady(ctx context.Context, sts *appsv1.StatefulSet, agent *achv1alpha1.ACHAgent, w *achv1alpha1.Workspace) (bool, error) {
	if sts == nil || sts.DeletionTimestamp != nil || sts.Generation == 0 || sts.Status.ObservedGeneration < sts.Generation || sts.Status.ReadyReplicas != 1 || sts.Spec.Replicas == nil || *sts.Spec.Replicas != 1 {
		return false, nil
	}
	name := workspaceResourceName(w.Spec.AgentRef.Name, w.Spec.WorkspaceRef)
	labelsExpected := workspaceLabels(w.Spec.AgentRef.UID, name)
	var pods corev1.PodList
	if err := r.APIReader.List(ctx, &pods, client.InNamespace(w.Namespace)); err != nil {
		return false, err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Name == name+"-0" && hasWorkspaceLabels(pod.Labels, labelsExpected) && hasControllerOwner(sts.OwnerReferences, agent) && hasStatefulSetOwner(pod.OwnerReferences, sts) && workspacePodIsReady(pod) {
			return true, nil
		}
	}
	return false, nil
}

func uidOfStatefulSet(sts *appsv1.StatefulSet) types.UID {
	if sts == nil {
		return ""
	}
	return sts.UID
}

func dedupeWorkspaceRequests(requests []reconcile.Request) []reconcile.Request {
	seen := map[types.NamespacedName]bool{}
	var out []reconcile.Request
	for _, request := range requests {
		if !seen[request.NamespacedName] {
			seen[request.NamespacedName] = true
			out = append(out, request)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

type workspaceConflictError struct{ err error }

func (e workspaceConflictError) Error() string { return e.err.Error() }
func (e workspaceConflictError) Unwrap() error { return e.err }
func isWorkspaceConflict(err error) bool       { _, ok := err.(workspaceConflictError); return ok }
