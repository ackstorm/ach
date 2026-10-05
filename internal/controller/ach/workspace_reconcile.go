// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

const workspaceUIDAnnotation = "runtime.ach.ackstorm.ai/workspace-uid"
const workspaceDataVolumeName = "workspace"
const workspaceSessionsVolumeName = "sessions"
const workspaceExecutionContainerName = "execution"

type workspaceObservation struct {
	statefulSet *appsv1.StatefulSet
	service     *corev1.Service
	pods        []corev1.Pod
}

func validateWorkspaceIdentity(w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent) error {
	if w == nil || a == nil {
		return fmt.Errorf("Workspace and ACHAgent are required")
	}
	if w.UID == "" {
		return fmt.Errorf("Workspace UID is required")
	}
	if a.UID == "" || !workspaceCanonicalUUID.MatchString(string(a.UID)) || string(a.UID) != w.Spec.AgentRef.UID {
		return fmt.Errorf("Workspace agentRef UID does not match the current ACHAgent")
	}
	if a.DeletionTimestamp != nil {
		return fmt.Errorf("ACHAgent is deleting")
	}
	if w.Namespace == "" || w.Namespace != a.Namespace || w.Spec.AgentRef.Name != a.Name {
		return fmt.Errorf("Workspace agentRef name and namespace do not match the current ACHAgent")
	}
	if !workspaceDigestPattern.MatchString(w.Spec.WorkspaceRef) {
		return fmt.Errorf("Workspace workspaceRef must be a full lowercase SHA-256 digest")
	}
	if w.Name != workspaceResourceName(a.Name, w.Spec.WorkspaceRef) {
		return fmt.Errorf("Workspace metadata.name does not match its full identity")
	}
	if !hasWorkspaceLabels(w.Labels, workspaceLabels(string(a.UID), w.Name)) || w.Annotations[workspaceRefAnnotation] != w.Spec.WorkspaceRef {
		return fmt.Errorf("Workspace metadata labels or annotation do not match its full identity")
	}
	if !hasControllerOwner(w.OwnerReferences, a) {
		return fmt.Errorf("Workspace is not controlled by the current ACHAgent")
	}
	if w.Spec.Replicas != 0 && w.Spec.Replicas != 1 {
		return fmt.Errorf("Workspace replicas must be 0 or 1")
	}
	return nil
}

func validateWorkspaceChildren(w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, observed *workspaceObservation) error {
	if observed == nil {
		return fmt.Errorf("Workspace observation is required")
	}
	name := workspaceResourceName(a.Name, w.Spec.WorkspaceRef)
	labelsExpected := workspaceLabels(w.Spec.AgentRef.UID, name)
	if err := validateWorkspaceStatefulSet(w, a, observed.statefulSet, name, labelsExpected); err != nil {
		return err
	}
	if err := validateWorkspaceService(w, a, observed.service, name, labelsExpected); err != nil {
		return err
	}
	return validateWorkspacePods(w, observed, name, labelsExpected)
}

func validateWorkspaceStatefulSet(w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, sts *appsv1.StatefulSet, name string, labelsExpected map[string]string) error {
	if sts == nil {
		return nil
	}
	if sts.DeletionTimestamp != nil {
		return fmt.Errorf("Workspace StatefulSet is deleting")
	}
	if sts.Namespace != w.Namespace || sts.Name != name || !hasWorkspaceLabels(sts.Labels, labelsExpected) || sts.Annotations[workspaceRefAnnotation] != w.Spec.WorkspaceRef {
		return fmt.Errorf("Workspace StatefulSet identity conflicts with the requested full identity")
	}
	if !hasControllerOwner(sts.OwnerReferences, a) {
		return fmt.Errorf("Workspace StatefulSet is not controlled by the current ACHAgent")
	}
	if sts.Spec.Selector == nil || !sameWorkspaceLabels(sts.Spec.Selector.MatchLabels, labelsExpected) || sts.Spec.ServiceName != name || sts.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType {
		return fmt.Errorf("Workspace StatefulSet selector, service, or update strategy conflicts")
	}
	if sts.Spec.Replicas == nil || (*sts.Spec.Replicas != 0 && *sts.Spec.Replicas != 1) {
		return fmt.Errorf("Workspace StatefulSet replica shape conflicts")
	}
	if !hasWorkspaceLabels(sts.Spec.Template.Labels, labelsExpected) || sts.Spec.Template.Annotations[workspaceRefAnnotation] != w.Spec.WorkspaceRef {
		return fmt.Errorf("Workspace StatefulSet pod template identity conflicts")
	}
	return nil
}

func validateWorkspaceService(w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, svc *corev1.Service, name string, labelsExpected map[string]string) error {
	if svc == nil {
		return nil
	}
	if svc.DeletionTimestamp != nil {
		return fmt.Errorf("Workspace Service is deleting")
	}
	if svc.Namespace != w.Namespace || svc.Name != name || !hasWorkspaceLabels(svc.Labels, labelsExpected) || svc.Annotations[workspaceRefAnnotation] != w.Spec.WorkspaceRef {
		return fmt.Errorf("Workspace Service identity conflicts with the requested full identity")
	}
	if !hasControllerOwner(svc.OwnerReferences, a) || !sameWorkspaceLabels(svc.Spec.Selector, labelsExpected) || svc.Spec.ClusterIP != corev1.ClusterIPNone || !svc.Spec.PublishNotReadyAddresses {
		return fmt.Errorf("Workspace Service ownership or selector conflicts")
	}
	return nil
}

func validateWorkspacePods(w *achv1alpha1.Workspace, observed *workspaceObservation, name string, labelsExpected map[string]string) error {
	for i := range observed.pods {
		pod := &observed.pods[i]
		if pod.DeletionTimestamp != nil {
			return fmt.Errorf("Workspace Pod %s is deleting and still blocks lifecycle changes", pod.Name)
		}
		if pod.Namespace != w.Namespace || !(pod.Name == name+"-0" || hasWorkspaceLabels(pod.Labels, labelsExpected)) {
			return fmt.Errorf("Workspace Pod identity conflicts")
		}
		if pod.Annotations[workspaceRefAnnotation] != w.Spec.WorkspaceRef {
			return fmt.Errorf("Workspace Pod full digest identity conflicts")
		}
		if sts := observed.statefulSet; sts != nil && !hasStatefulSetOwner(pod.OwnerReferences, sts) {
			return fmt.Errorf("Workspace Pod %s does not have the observed StatefulSet owner", pod.Name)
		}
		if !hasWorkspaceLabels(pod.Labels, labelsExpected) {
			// The exact ordinal name is itself a safety observation. Do not treat a
			// malformed Pod as idle or delete it to make room for a replacement.
			return fmt.Errorf("Workspace ordinal Pod %s has missing or conflicting identity labels", pod.Name)
		}
	}
	return nil
}

func validateWorkspacePreconditions(w *achv1alpha1.Workspace, observed *workspaceObservation) error {
	if observed == nil {
		return fmt.Errorf("Workspace observation is required")
	}
	sts := observed.statefulSet
	if err := validateExplicitWorkspaceUIDs(w, observed, sts); err != nil {
		return err
	}
	if sts == nil {
		if w.Spec.ExpectedStatefulSetUID != "" || len(observed.pods) != 0 {
			return fmt.Errorf("Workspace expected StatefulSet is absent or Pods remain without it")
		}
		return nil
	}
	return validateExistingWorkspacePreconditions(w, observed, sts)
}

func validateExplicitWorkspaceUIDs(w *achv1alpha1.Workspace, observed *workspaceObservation, sts *appsv1.StatefulSet) error {
	if w.Spec.ExpectedStatefulSetUID != "" {
		if sts == nil || string(sts.UID) != w.Spec.ExpectedStatefulSetUID {
			return fmt.Errorf("Workspace expected StatefulSet UID does not match the current child")
		}
	}
	if w.Spec.ExpectedPodUID != "" {
		found := false
		for i := range observed.pods {
			if string(observed.pods[i].UID) == w.Spec.ExpectedPodUID {
				found = true
			}
		}
		if !found && (len(observed.pods) != 0 || w.Spec.Replicas != 0) {
			return fmt.Errorf("Workspace expected Pod UID does not match the current Pod")
		}
		if found && len(observed.pods) != 1 {
			return fmt.Errorf("Workspace Pod precondition requires exactly one matching Pod")
		}
	}
	return nil
}

func validateExistingWorkspacePreconditions(w *achv1alpha1.Workspace, observed *workspaceObservation, sts *appsv1.StatefulSet) error {
	markerOwns := sts.Annotations[workspaceUIDAnnotation] != "" && sts.Annotations[workspaceUIDAnnotation] == string(w.UID)
	currentReplicas := int32(-1)
	if sts.Spec.Replicas != nil {
		currentReplicas = *sts.Spec.Replicas
	}
	if markerOwns && currentReplicas == w.Spec.Replicas {
		return nil
	}
	if currentReplicas == 0 && w.Spec.Replicas == 0 && len(observed.pods) == 0 && w.Status.StatefulSetUID != "" && w.Status.StatefulSetUID == string(sts.UID) {
		return nil
	}
	if w.Spec.ExpectedStatefulSetUID == "" || string(sts.UID) != w.Spec.ExpectedStatefulSetUID {
		return fmt.Errorf("Workspace existing StatefulSet requires its expected UID precondition")
	}
	if currentReplicas == 1 && w.Spec.Replicas == 1 && len(observed.pods) > 0 && w.Spec.ExpectedPodUID == "" {
		return fmt.Errorf("Workspace active adoption requires the observed Pod UID precondition")
	}
	if w.Spec.Replicas == 0 && len(observed.pods) > 0 {
		if len(observed.pods) != 1 || w.Spec.ExpectedPodUID == "" || string(observed.pods[0].UID) != w.Spec.ExpectedPodUID {
			return fmt.Errorf("Workspace close requires the exact observed Pod UID precondition")
		}
	}
	if w.Spec.Replicas == 1 && currentReplicas == 0 && len(observed.pods) != 0 {
		return fmt.Errorf("Workspace activation requires zero replicas and no observed Pod")
	}
	return nil
}

func (r *WorkspaceReconciler) observeWorkspace(ctx context.Context, w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent) (*workspaceObservation, error) {
	name := workspaceResourceName(w.Spec.AgentRef.Name, w.Spec.WorkspaceRef)
	obs := &workspaceObservation{}
	var sts appsv1.StatefulSet
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: w.Namespace, Name: name}, &sts); err == nil {
		obs.statefulSet = &sts
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}
	var svc corev1.Service
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: w.Namespace, Name: name}, &svc); err == nil {
		obs.service = &svc
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}
	var pods corev1.PodList
	if err := r.APIReader.List(ctx, &pods, client.InNamespace(w.Namespace)); err != nil {
		return nil, err
	}
	identity := workspaceLabels(w.Spec.AgentRef.UID, name)
	seen := map[types.UID]bool{}
	for i := range pods.Items {
		pod := pods.Items[i]
		if pod.Name != name+"-0" && pod.Labels[workspaceNameLabel] == "" && !hasWorkspaceLabels(pod.Labels, identity) {
			continue
		}
		if pod.Labels[workspaceNameLabel] != name && pod.Name != name+"-0" {
			continue
		}
		if pod.Labels[workspaceAgentUIDLabel] != w.Spec.AgentRef.UID && pod.Name != name+"-0" {
			continue
		}
		if pod.UID != "" && seen[pod.UID] {
			continue
		}
		if pod.UID != "" {
			seen[pod.UID] = true
		}
		obs.pods = append(obs.pods, pod)
	}
	_ = a
	return obs, nil
}

func (r *WorkspaceReconciler) reconcileWorkspaceWorkload(ctx context.Context, w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, desiredSTS *appsv1.StatefulSet, desiredService *corev1.Service) (*appsv1.StatefulSet, bool, error) {
	if err := validateWorkspaceIdentity(w, a); err != nil {
		return nil, false, workspaceConflictError{err: err}
	}
	var freshWorkspace achv1alpha1.Workspace
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(w), &freshWorkspace); err != nil {
		return nil, false, err
	}
	if freshWorkspace.UID != w.UID || freshWorkspace.Generation != w.Generation || freshWorkspace.ResourceVersion != w.ResourceVersion || !apiequality.Semantic.DeepEqual(freshWorkspace.Spec, w.Spec) {
		return nil, false, fmt.Errorf("Workspace changed during evaluation; reconcile the latest generation")
	}
	var freshAgent achv1alpha1.ACHAgent
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: a.Name}, &freshAgent); err != nil {
		return nil, false, err
	}
	if freshAgent.UID != a.UID || freshAgent.ResourceVersion != a.ResourceVersion || freshAgent.Spec.ProfileRef.Name != a.Spec.ProfileRef.Name || freshAgent.DeletionTimestamp != nil {
		return nil, false, fmt.Errorf("ACHAgent changed during Workspace evaluation; reconcile again")
	}
	obs, err := r.observeWorkspace(ctx, w, a)
	if err != nil {
		return nil, false, err
	}
	return r.applyObservedWorkspaceWorkload(ctx, w, a, desiredSTS, desiredService, obs)
}

func (r *WorkspaceReconciler) applyObservedWorkspaceWorkload(ctx context.Context, w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, desiredSTS *appsv1.StatefulSet, desiredService *corev1.Service, obs *workspaceObservation) (*appsv1.StatefulSet, bool, error) {
	if err := validateWorkspaceChildren(w, a, obs); err != nil {
		return obs.statefulSet, workspaceTemplateDrift(obs.statefulSet, desiredSTS), workspaceConflictError{err: err}
	}
	if obs.statefulSet != nil {
		if err := validateWorkspaceTemplateStorage(obs.statefulSet.Spec.Template, desiredSTS.Spec.Template); err != nil {
			return obs.statefulSet, workspaceTemplateDrift(obs.statefulSet, desiredSTS), workspaceConflictError{err: err}
		}
	}
	if err := validateWorkspacePreconditions(w, obs); err != nil {
		return obs.statefulSet, workspaceTemplateDrift(obs.statefulSet, desiredSTS), workspaceConflictError{err: err}
	}
	if obs.statefulSet == nil {
		return r.createWorkspaceWorkload(ctx, w, a, obs, desiredSTS, desiredService)
	}
	return r.updateExistingWorkspaceWorkload(ctx, w, a, obs, desiredSTS, desiredService)
}

func (r *WorkspaceReconciler) createWorkspaceWorkload(ctx context.Context, w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, obs *workspaceObservation, desiredSTS *appsv1.StatefulSet, desiredService *corev1.Service) (*appsv1.StatefulSet, bool, error) {
	if len(obs.pods) != 0 {
		return nil, false, workspaceConflictError{err: fmt.Errorf("Workspace Pods remain without a StatefulSet")}
	}
	if obs.service == nil {
		if err := r.Client.Create(ctx, desiredService); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return nil, false, err
			}
			return nil, false, err
		}
		fresh, err := r.revalidateWorkspaceForFollowOnWrite(ctx, w, a)
		if err != nil {
			return nil, false, err
		}
		if fresh.statefulSet != nil || len(fresh.pods) != 0 {
			return nil, false, workspaceConflictError{err: fmt.Errorf("Workspace children changed after Service creation")}
		}
	}
	created := desiredSTS.DeepCopy()
	if created.Annotations == nil {
		created.Annotations = map[string]string{}
	}
	created.Annotations[workspaceUIDAnnotation] = string(w.UID)
	if err := r.Client.Create(ctx, created); err != nil {
		return nil, false, err
	}
	return created, false, nil
}

func (r *WorkspaceReconciler) revalidateWorkspaceForFollowOnWrite(ctx context.Context, evaluated *achv1alpha1.Workspace, evaluatedAgent *achv1alpha1.ACHAgent) (*workspaceObservation, error) {
	var fresh achv1alpha1.Workspace
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(evaluated), &fresh); err != nil {
		return nil, err
	}
	if fresh.UID != evaluated.UID || fresh.Generation != evaluated.Generation || fresh.ResourceVersion != evaluated.ResourceVersion || !apiequality.Semantic.DeepEqual(fresh.Spec, evaluated.Spec) || fresh.DeletionTimestamp != nil {
		return nil, fmt.Errorf("Workspace changed after a child write; stop this evaluated request")
	}
	var agent achv1alpha1.ACHAgent
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: fresh.Namespace, Name: fresh.Spec.AgentRef.Name}, &agent); err != nil {
		return nil, err
	}
	if agent.UID != types.UID(fresh.Spec.AgentRef.UID) || agent.UID != evaluatedAgent.UID || agent.ResourceVersion != evaluatedAgent.ResourceVersion || agent.Spec.ProfileRef.Name != evaluatedAgent.Spec.ProfileRef.Name || agent.DeletionTimestamp != nil {
		return nil, workspaceConflictError{err: fmt.Errorf("Workspace owner changed after a child write")}
	}
	if err := validateWorkspaceIdentity(&fresh, &agent); err != nil {
		return nil, workspaceConflictError{err: err}
	}
	obs, err := r.observeWorkspace(ctx, &fresh, &agent)
	if err != nil {
		return nil, err
	}
	if err := validateWorkspaceChildren(&fresh, &agent, obs); err != nil {
		return nil, workspaceConflictError{err: err}
	}
	if err := validateWorkspacePreconditions(&fresh, obs); err != nil {
		return nil, workspaceConflictError{err: err}
	}
	return obs, nil
}

func (r *WorkspaceReconciler) updateExistingWorkspaceWorkload(ctx context.Context, w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent, obs *workspaceObservation, desiredSTS *appsv1.StatefulSet, desiredService *corev1.Service) (*appsv1.StatefulSet, bool, error) {
	sts := obs.statefulSet.DeepCopy()
	missingService := obs.service == nil
	if err := r.repairMissingWorkspaceService(ctx, w, sts, obs.service, desiredService); err != nil {
		return sts, false, err
	}
	if missingService {
		fresh, err := r.revalidateWorkspaceForFollowOnWrite(ctx, w, a)
		if err != nil {
			return sts, false, err
		}
		if fresh.statefulSet == nil || fresh.statefulSet.UID != sts.UID {
			return sts, false, workspaceConflictError{err: fmt.Errorf("Workspace StatefulSet changed after Service repair")}
		}
		obs = fresh
		sts = fresh.statefulSet.DeepCopy()
	}

	plan, pending, err := planWorkspaceStatefulSetUpdate(w, sts, desiredSTS, obs)
	if err != nil {
		return sts, pending, workspaceConflictError{err: err}
	}
	if !plan.replicas && !plan.template {
		return sts, pending, nil
	}
	updated := sts.DeepCopy()
	if plan.replicas {
		updated.Spec.Replicas = &w.Spec.Replicas
	}
	if plan.template {
		updatedTemplate, err := buildUpdatedWorkspaceTemplate(sts.Spec.Template, desiredSTS.Spec.Template)
		if err != nil {
			return sts, pending, workspaceConflictError{err: err}
		}
		updated.Spec.Template = updatedTemplate
	}
	if err := r.Client.Update(ctx, updated); err != nil {
		return sts, pending, err
	}
	return updated, pending, nil
}

func validateWorkspaceTemplateStorage(live, desired corev1.PodTemplateSpec) error {
	liveVolumes := make(map[string]corev1.Volume, len(live.Spec.Volumes))
	for _, volume := range live.Spec.Volumes {
		liveVolumes[volume.Name] = volume
	}
	for _, expected := range desired.Spec.Volumes {
		actual, found := liveVolumes[expected.Name]
		if !found {
			return fmt.Errorf("Workspace template is missing required volume %q", expected.Name)
		}
		actualTemplate := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Volumes: []corev1.Volume{actual}}}
		expectedTemplate := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Volumes: []corev1.Volume{expected}}}
		actualNormalized := normalizeWorkspaceTemplate(actualTemplate).Spec.Volumes[0]
		expectedNormalized := normalizeWorkspaceTemplate(expectedTemplate).Spec.Volumes[0]
		if expected.Name == workspaceDataVolumeName || expected.Name == workspaceSessionsVolumeName {
			if actualNormalized.EmptyDir == nil || expectedNormalized.EmptyDir == nil || actualNormalized.EmptyDir.Medium != expectedNormalized.EmptyDir.Medium {
				return fmt.Errorf("Workspace template volume %q has an incompatible storage source", expected.Name)
			}
			continue
		}
		if !apiequality.Semantic.DeepEqual(actualNormalized.VolumeSource, expectedNormalized.VolumeSource) {
			return fmt.Errorf("Workspace template volume %q has an incompatible source or bootstrap reference", expected.Name)
		}
	}
	for _, expected := range workspaceExecutionContainer(desired.Spec.Containers).VolumeMounts {
		actual := workspaceExecutionContainer(live.Spec.Containers)
		if actual == nil || !hasWorkspaceVolumeMount(actual.VolumeMounts, expected) {
			return fmt.Errorf("Workspace template is missing or changes required mount %q at %q", expected.Name, expected.MountPath)
		}
	}
	return nil
}

func workspaceExecutionContainer(containers []corev1.Container) *corev1.Container {
	for i := range containers {
		if containers[i].Name == workspaceExecutionContainerName {
			return &containers[i]
		}
	}
	return nil
}

func hasWorkspaceVolumeMount(mounts []corev1.VolumeMount, expected corev1.VolumeMount) bool {
	for _, mount := range mounts {
		if mount.Name == expected.Name && mount.MountPath == expected.MountPath && mount.SubPath == expected.SubPath && mount.SubPathExpr == expected.SubPathExpr && mount.ReadOnly == expected.ReadOnly && apiequality.Semantic.DeepEqual(mount.MountPropagation, expected.MountPropagation) {
			return true
		}
	}
	return false
}

func buildUpdatedWorkspaceTemplate(live, desired corev1.PodTemplateSpec) (corev1.PodTemplateSpec, error) {
	if err := validateWorkspaceTemplateStorage(live, desired); err != nil {
		return corev1.PodTemplateSpec{}, err
	}
	updated := *desired.DeepCopy()
	if updated.Labels == nil {
		updated.Labels = map[string]string{}
	}
	for key, value := range live.Labels {
		if _, owned := updated.Labels[key]; !owned {
			updated.Labels[key] = value
		}
	}
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	for key, value := range live.Annotations {
		if _, owned := updated.Annotations[key]; !owned {
			updated.Annotations[key] = value
		}
	}
	liveVolumes := make(map[string]corev1.Volume, len(live.Spec.Volumes))
	for _, volume := range live.Spec.Volumes {
		liveVolumes[volume.Name] = volume
	}
	for i := range updated.Spec.Volumes {
		previous := liveVolumes[updated.Spec.Volumes[i].Name]
		if updated.Spec.Volumes[i].Name == workspaceDataVolumeName || updated.Spec.Volumes[i].Name == workspaceSessionsVolumeName {
			sizeLimit := updated.Spec.Volumes[i].EmptyDir.SizeLimit.DeepCopy()
			updated.Spec.Volumes[i].EmptyDir.SizeLimit = &sizeLimit
		}
		if updated.Spec.Volumes[i].Name == workspaceBootstrapVolume {
			updated.Spec.Volumes[i].VolumeSource = *previous.VolumeSource.DeepCopy()
		}
	}
	for _, volume := range live.Spec.Volumes {
		if _, included := liveVolumes[volume.Name]; included && !workspaceHasVolume(updated.Spec.Volumes, volume.Name) {
			updated.Spec.Volumes = append(updated.Spec.Volumes, *volume.DeepCopy())
		}
	}
	if liveExecution, desiredExecution := workspaceExecutionContainer(live.Spec.Containers), workspaceExecutionContainer(updated.Spec.Containers); liveExecution != nil && desiredExecution != nil {
		for _, mount := range liveExecution.VolumeMounts {
			if !workspaceHasVolumeMount(desiredExecution.VolumeMounts, mount.Name, mount.MountPath) {
				desiredExecution.VolumeMounts = append(desiredExecution.VolumeMounts, *mount.DeepCopy())
			}
		}
	}
	return updated, nil
}

func workspaceHasVolume(volumes []corev1.Volume, name string) bool {
	for _, volume := range volumes {
		if volume.Name == name {
			return true
		}
	}
	return false
}

func workspaceHasVolumeMount(mounts []corev1.VolumeMount, name, path string) bool {
	for _, mount := range mounts {
		if mount.Name == name && mount.MountPath == path {
			return true
		}
	}
	return false
}

type workspaceUpdatePlan struct {
	replicas bool
	template bool
}

func planWorkspaceStatefulSetUpdate(w *achv1alpha1.Workspace, sts, desired *appsv1.StatefulSet, obs *workspaceObservation) (workspaceUpdatePlan, bool, error) {
	currentReplicas, err := workspaceStatefulSetReplicas(sts)
	if err != nil {
		return workspaceUpdatePlan{}, false, err
	}
	effectiveDesired, err := buildUpdatedWorkspaceTemplate(sts.Spec.Template, desired.Spec.Template)
	if err != nil {
		return workspaceUpdatePlan{}, false, err
	}
	templateDrift := !apiequality.Semantic.DeepEqual(normalizeWorkspaceTemplate(sts.Spec.Template), normalizeWorkspaceTemplate(effectiveDesired))
	updateTemplate, err := planWorkspaceTemplate(w, sts, obs, currentReplicas, templateDrift)
	if err != nil {
		return workspaceUpdatePlan{}, templateDrift, err
	}
	updateReplicas := currentReplicas != w.Spec.Replicas
	if err := validateWorkspaceReplicaUpdate(w, sts, obs, updateReplicas); err != nil {
		return workspaceUpdatePlan{}, templateDrift && !updateTemplate, err
	}
	return workspaceUpdatePlan{replicas: updateReplicas, template: updateTemplate}, templateDrift && !updateTemplate, nil
}

func planWorkspaceTemplate(w *achv1alpha1.Workspace, sts *appsv1.StatefulSet, obs *workspaceObservation, replicas int32, drift bool) (bool, error) {
	if w.Spec.Replicas == 1 && replicas == 0 {
		if w.Spec.ExpectedStatefulSetUID == "" || w.Spec.ExpectedStatefulSetUID != string(sts.UID) || len(obs.pods) != 0 {
			return false, fmt.Errorf("Workspace activation requires the current expected StatefulSet UID and no Pod")
		}
		return drift, nil
	}
	if replicas == 0 && len(obs.pods) == 0 && drift {
		statusBound := w.Status.StatefulSetUID != "" && w.Status.StatefulSetUID == string(sts.UID)
		expectedBound := w.Spec.ExpectedStatefulSetUID != "" && w.Spec.ExpectedStatefulSetUID == string(sts.UID)
		if !statusBound && !expectedBound {
			createdByThisWorkspace := sts.Annotations[workspaceUIDAnnotation] == string(w.UID)
			if createdByThisWorkspace && w.Status.StatefulSetUID == "" && w.Spec.ExpectedStatefulSetUID == "" {
				return false, nil
			}
			return false, fmt.Errorf("Workspace idle template refresh requires a bound StatefulSet UID")
		}
		return true, nil
	}
	return false, nil
}

func validateWorkspaceReplicaUpdate(w *achv1alpha1.Workspace, sts *appsv1.StatefulSet, obs *workspaceObservation, update bool) error {
	if !update {
		return nil
	}
	if w.Spec.ExpectedStatefulSetUID == "" || w.Spec.ExpectedStatefulSetUID != string(sts.UID) {
		return fmt.Errorf("Workspace replica change requires the current expected StatefulSet UID")
	}
	if w.Spec.Replicas == 0 && len(obs.pods) > 0 && (len(obs.pods) != 1 || w.Spec.ExpectedPodUID == "" || w.Spec.ExpectedPodUID != string(obs.pods[0].UID)) {
		return fmt.Errorf("Workspace close requires the exact current Pod UID")
	}
	if w.Spec.Replicas == 1 && len(obs.pods) != 0 {
		return fmt.Errorf("Workspace activation is blocked while a Pod exists")
	}
	return nil
}

func workspaceStatefulSetReplicas(sts *appsv1.StatefulSet) (int32, error) {
	if sts.Spec.Replicas == nil {
		return 0, fmt.Errorf("Workspace StatefulSet replicas are missing")
	}
	return *sts.Spec.Replicas, nil
}

func (r *WorkspaceReconciler) repairMissingWorkspaceService(ctx context.Context, w *achv1alpha1.Workspace, sts *appsv1.StatefulSet, service *corev1.Service, desired *corev1.Service) error {
	if service != nil {
		return nil
	}
	statusBound := w.Status.StatefulSetUID != "" && w.Status.StatefulSetUID == string(sts.UID)
	expectedBound := w.Spec.ExpectedStatefulSetUID != "" && w.Spec.ExpectedStatefulSetUID == string(sts.UID)
	if !statusBound && !expectedBound {
		return workspaceConflictError{err: fmt.Errorf("Workspace Service repair requires a bound StatefulSet UID")}
	}
	return r.Client.Create(ctx, desired.DeepCopy())
}

func hasWorkspaceLabels(got, expected map[string]string) bool {
	for key, value := range expected {
		if got[key] != value {
			return false
		}
	}
	return true
}

func sameWorkspaceLabels(got, expected map[string]string) bool {
	return len(got) == len(expected) && hasWorkspaceLabels(got, expected)
}

func hasControllerOwner(refs []metav1.OwnerReference, a *achv1alpha1.ACHAgent) bool {
	for _, ref := range refs {
		if ref.Controller != nil && *ref.Controller && ref.APIVersion == achv1alpha1.GroupVersion.String() && ref.Kind == achAgentOwnerKind && ref.Name == a.Name && ref.UID == a.UID {
			return true
		}
	}
	return false
}

func hasStatefulSetOwner(refs []metav1.OwnerReference, sts *appsv1.StatefulSet) bool {
	for _, ref := range refs {
		if ref.Kind == "StatefulSet" && ref.Name == sts.Name && ref.UID == sts.UID {
			return true
		}
	}
	return false
}

func workspacePodIsReady(pod *corev1.Pod) bool {
	if pod == nil || pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func workspaceTemplateDrift(live, desired *appsv1.StatefulSet) bool {
	if live == nil || desired == nil {
		return true
	}
	effectiveDesired, err := buildUpdatedWorkspaceTemplate(live.Spec.Template, desired.Spec.Template)
	if err != nil {
		return true
	}
	return !apiequality.Semantic.DeepEqual(normalizeWorkspaceTemplate(live.Spec.Template), normalizeWorkspaceTemplate(effectiveDesired))
}

func normalizeWorkspaceTemplate(template corev1.PodTemplateSpec) corev1.PodTemplateSpec {
	normalized := *template.DeepCopy()
	spec := &normalized.Spec
	if spec.RestartPolicy == "" {
		spec.RestartPolicy = corev1.RestartPolicyAlways
	}
	if spec.DNSPolicy == "" {
		spec.DNSPolicy = corev1.DNSClusterFirst
	}
	if spec.TerminationGracePeriodSeconds == nil {
		value := int64(30)
		spec.TerminationGracePeriodSeconds = &value
	}
	if spec.SchedulerName == "" {
		spec.SchedulerName = "default-scheduler"
	}
	if spec.DeprecatedServiceAccount == "" {
		spec.DeprecatedServiceAccount = spec.ServiceAccountName
	}
	if spec.EnableServiceLinks == nil {
		value := true
		spec.EnableServiceLinks = &value
	}
	for i := range spec.Containers {
		normalizeWorkspaceContainerDefaults(&spec.Containers[i])
	}
	for i := range spec.InitContainers {
		normalizeWorkspaceContainerDefaults(&spec.InitContainers[i])
	}
	for i := range spec.Volumes {
		volume := &spec.Volumes[i]
		if volume.ConfigMap != nil && volume.ConfigMap.DefaultMode == nil {
			value := int32(0644)
			volume.ConfigMap.DefaultMode = &value
		}
		if volume.Secret != nil && volume.Secret.DefaultMode == nil {
			value := int32(0644)
			volume.Secret.DefaultMode = &value
		}
	}
	return normalized
}

func normalizeWorkspaceContainerDefaults(container *corev1.Container) {
	if container.ImagePullPolicy == "" {
		image := container.Image
		lastSlash, lastColon := strings.LastIndex(image, "/"), strings.LastIndex(image, ":")
		if lastColon > lastSlash && image[lastColon+1:] == "latest" || lastColon <= lastSlash {
			container.ImagePullPolicy = corev1.PullAlways
		} else {
			container.ImagePullPolicy = corev1.PullIfNotPresent
		}
	}
	if container.TerminationMessagePath == "" {
		container.TerminationMessagePath = "/dev/termination-log"
	}
	if container.TerminationMessagePolicy == "" {
		container.TerminationMessagePolicy = corev1.TerminationMessageReadFile
	}
	for i := range container.Ports {
		if container.Ports[i].Protocol == "" {
			container.Ports[i].Protocol = corev1.ProtocolTCP
		}
	}
	for i := range container.Env {
		if source := container.Env[i].ValueFrom; source != nil && source.FieldRef != nil && source.FieldRef.APIVersion == "" {
			source.FieldRef.APIVersion = "v1"
		}
	}
	for _, probe := range []*corev1.Probe{container.ReadinessProbe, container.LivenessProbe, container.StartupProbe} {
		if probe == nil {
			continue
		}
		if probe.TimeoutSeconds == 0 {
			probe.TimeoutSeconds = 1
		}
		if probe.PeriodSeconds == 0 {
			probe.PeriodSeconds = 10
		}
		if probe.SuccessThreshold == 0 {
			probe.SuccessThreshold = 1
		}
		if probe.FailureThreshold == 0 {
			probe.FailureThreshold = 3
		}
		if probe.HTTPGet != nil && probe.HTTPGet.Scheme == "" {
			probe.HTTPGet.Scheme = corev1.URISchemeHTTP
		}
	}
}
