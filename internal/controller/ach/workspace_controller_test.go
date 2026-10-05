// SPDX-License-Identifier: Apache-2.0

package ach

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	achv1alpha1 "github.com/ackstorm/ach/api/ach/v1alpha1"
)

const workspaceUIDAnnotationForTest = "runtime.ach.ackstorm.ai/workspace-uid"

type recordingWorkspaceClient struct {
	client.Client
	childCreates int
	childUpdates int
	created      []client.Object
	updated      []client.Object
	afterCreate  func(context.Context, client.Object) error
	failStatus   bool
	conflictNext bool
}

func (c *recordingWorkspaceClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	var child client.Object
	switch child := obj.(type) {
	case *appsv1.StatefulSet:
		c.childCreates++
		if child.UID == "" {
			child.UID = "created-sts-uid"
		}
		copy := child.DeepCopy()
		c.created = append(c.created, copy)
	case *corev1.Service:
		c.childCreates++
		if child.UID == "" {
			child.UID = "created-service-uid"
		}
		copy := child.DeepCopy()
		c.created = append(c.created, copy)
	}
	err := c.Client.Create(ctx, obj, opts...)
	if err == nil && c.afterCreate != nil {
		return c.afterCreate(ctx, obj)
	}
	_ = child
	return err
}

func (c *recordingWorkspaceClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	switch child := obj.(type) {
	case *appsv1.StatefulSet:
		c.childUpdates++
		c.updated = append(c.updated, child.DeepCopy())
	case *corev1.Service:
		c.childUpdates++
		c.updated = append(c.updated, child.DeepCopy())
	}
	if sts, ok := obj.(*appsv1.StatefulSet); ok && c.conflictNext {
		c.conflictNext = false
		return apierrors.NewConflict(appsv1.Resource("statefulsets"), sts.Name, errors.New("observed resourceVersion is stale"))
	}
	return c.Client.Update(ctx, obj, opts...)
}

func (c *recordingWorkspaceClient) Status() client.SubResourceWriter {
	return recordingWorkspaceStatusWriter{SubResourceWriter: c.Client.Status(), parent: c}
}

type recordingWorkspaceStatusWriter struct {
	client.SubResourceWriter
	parent *recordingWorkspaceClient
}

func (w recordingWorkspaceStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if _, ok := obj.(*achv1alpha1.Workspace); ok && w.parent.failStatus {
		w.parent.failStatus = false
		return errors.New("injected first status failure")
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func setWorkspaceControllerMetadata(w *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent) {
	w.Labels = workspaceLabels(w.Spec.AgentRef.UID, w.Name)
	w.Annotations = map[string]string{workspaceRefAnnotation: w.Spec.WorkspaceRef}
	w.OwnerReferences = []metav1.OwnerReference{workspaceOwnerReference(a)}
}

type workspacePodListErrorReader struct {
	client.Reader
	podLists int
	failAt   int
	err      error
}

func (r *workspacePodListErrorReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*corev1.PodList); ok {
		r.podLists++
		if r.failAt != 0 && r.podLists == r.failAt {
			return r.err
		}
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestWorkspaceReadinessPodListErrorRetries(t *testing.T) {
	ctx := context.Background()
	w, agent, profile := workspaceTestObjects()
	w.UID, w.Generation, w.ResourceVersion = "workspace-current", 1, "1"
	profile.Name, profile.Namespace = "profile", w.Namespace
	agent.Spec.ProfileRef.Name = profile.Name
	setWorkspaceControllerMetadata(w, agent)
	secret := workspaceKeyTestSecret(t, agent, "private-literal")
	verifyKey := workspaceEngineVerifyKey([]byte("private-literal"))
	statefulSet, err := buildWorkspaceStatefulSet(w, agent, profile, verifyKey)
	if err != nil {
		t.Fatalf("build desired StatefulSet: %v", err)
	}
	statefulSet.UID = "workspace-statefulset-current"
	statefulSet.Generation = 1
	statefulSet.Status.ObservedGeneration = 1
	statefulSet.Status.ReadyReplicas = 1
	service := buildWorkspaceService(w, agent)
	service.UID = "workspace-service-current"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            w.Name + "-0",
			Namespace:       w.Namespace,
			UID:             "workspace-pod-current",
			Labels:          statefulSet.Spec.Template.Labels,
			Annotations:     statefulSet.Spec.Template.Annotations,
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: statefulSet.Name, UID: statefulSet.UID}},
		},
		Spec:   statefulSet.Spec.Template.Spec,
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}
	w.Spec.ExpectedStatefulSetUID = string(statefulSet.UID)
	w.Spec.ExpectedPodUID = string(pod.UID)
	w.Status = achv1alpha1.WorkspaceStatus{
		ObservedGeneration: w.Generation,
		StatefulSetUID:     string(statefulSet.UID),
		Conditions: []metav1.Condition{{
			Type:               "WorkloadReady",
			Status:             metav1.ConditionTrue,
			Reason:             "Ready",
			Message:            "existing current-generation readiness",
			ObservedGeneration: w.Generation,
		}},
	}

	scheme := workspaceKeyTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, agent, profile, secret, statefulSet, service, pod).Build()
	writer := &recordingWorkspaceClient{Client: base}
	sentinel := errors.New("injected readiness Pod list failure")
	reader := &workspacePodListErrorReader{Reader: writer, failAt: 2, err: sentinel}
	reconciler := &WorkspaceReconciler{Client: writer, APIReader: reader, Scheme: scheme}
	statusBefore := w.Status.DeepCopy()
	result, reconcileErr := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)})
	if !errors.Is(reconcileErr, sentinel) {
		t.Errorf("Reconcile error = %v, want injected Pod list error", reconcileErr)
	}
	if result != (reconcile.Result{}) {
		t.Errorf("Reconcile result = %+v, want zero result on read error", result)
	}
	if reader.podLists != 2 {
		t.Errorf("Pod lists before readiness error = %d, want 2", reader.podLists)
	}
	var afterFailure achv1alpha1.Workspace
	if err := base.Get(ctx, client.ObjectKeyFromObject(w), &afterFailure); err != nil {
		t.Fatalf("read Workspace after readiness error: %v", err)
	}
	if !reflect.DeepEqual(statusBefore, &afterFailure.Status) {
		t.Errorf("read failure changed Workspace status:\n got: %+v\nwant: %+v", afterFailure.Status, statusBefore)
	}
	if writer.childCreates+writer.childUpdates != 0 {
		t.Errorf("read failure wrote children: creates=%d updates=%d", writer.childCreates, writer.childUpdates)
	}

	reader.failAt = 0
	reader.podLists = 0
	result, reconcileErr = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)})
	if reconcileErr != nil || result != (reconcile.Result{}) {
		t.Fatalf("reconcile after read recovery = result %+v, err %v", result, reconcileErr)
	}
	if reader.podLists != 2 {
		t.Fatalf("recovery Pod lists = %d, want 2", reader.podLists)
	}
	var afterRecovery achv1alpha1.Workspace
	if err := base.Get(ctx, client.ObjectKeyFromObject(w), &afterRecovery); err != nil {
		t.Fatalf("read Workspace after readiness recovery: %v", err)
	}
	ready := apimeta.FindStatusCondition(afterRecovery.Status.Conditions, "WorkloadReady")
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.Reason != "Ready" || ready.ObservedGeneration != w.Generation {
		t.Fatalf("recovered readiness condition = %+v, want current-generation Ready", ready)
	}
	if writer.childCreates+writer.childUpdates != 0 {
		t.Fatalf("successful readiness checks wrote children: creates=%d updates=%d", writer.childCreates, writer.childUpdates)
	}
	var afterStatefulSet appsv1.StatefulSet
	var afterPod corev1.Pod
	if err := base.Get(ctx, client.ObjectKeyFromObject(statefulSet), &afterStatefulSet); err != nil {
		t.Fatalf("read StatefulSet after readiness recovery: %v", err)
	}
	if err := base.Get(ctx, client.ObjectKeyFromObject(pod), &afterPod); err != nil {
		t.Fatalf("read Pod after readiness recovery: %v", err)
	}
	if afterStatefulSet.UID != statefulSet.UID || afterPod.UID != pod.UID {
		t.Fatalf("child UIDs changed during readiness check: StatefulSet=%q Pod=%q", afterStatefulSet.UID, afterPod.UID)
	}
}

func TestWorkspaceCRMetadataConflictsBeforeChildWrites(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(*achv1alpha1.Workspace)
	}{
		{name: "wrong label", mutate: func(w *achv1alpha1.Workspace) { w.Labels[workspaceNameLabel] = "collision" }},
		{name: "wrong annotation", mutate: func(w *achv1alpha1.Workspace) { w.Annotations[workspaceRefAnnotation] = strings.Repeat("b", 64) }},
		{name: "missing controller owner", mutate: func(w *achv1alpha1.Workspace) { w.OwnerReferences = nil }},
		{name: "foreign controller owner", mutate: func(w *achv1alpha1.Workspace) { w.OwnerReferences[0].UID = "prior-agent" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, a, p := workspaceTestObjects()
			w.UID, w.Generation, w.ResourceVersion = "workspace-current", 4, "1"
			p.Name, p.Namespace = "profile", w.Namespace
			a.Spec.ProfileRef.Name = p.Name
			setWorkspaceControllerMetadata(w, a)
			w.Status.StatefulSetUID = "previously-authorized"
			tc.mutate(w)
			scheme := workspaceKeyTestScheme(t)
			base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, workspaceKeyTestSecret(t, a, "private-literal")).Build()
			writer := &recordingWorkspaceClient{Client: base}
			r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
			if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}); err != nil {
				t.Fatal(err)
			}
			if writer.childCreates+writer.childUpdates != 0 {
				t.Fatalf("invalid CR metadata attempted %d child writes", writer.childCreates+writer.childUpdates)
			}
			var got achv1alpha1.Workspace
			if err := base.Get(ctx, client.ObjectKeyFromObject(w), &got); err != nil {
				t.Fatal(err)
			}
			condition := apimeta.FindStatusCondition(got.Status.Conditions, "WorkloadApplied")
			if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "WorkspaceConflict" || condition.ObservedGeneration != w.Generation {
				t.Fatalf("condition=%+v, want current-generation WorkspaceConflict", condition)
			}
			if got.Status.StatefulSetUID != "previously-authorized" {
				t.Fatalf("rejected metadata overwrote existing binding: %q", got.Status.StatefulSetUID)
			}
		})
	}
}

func TestWorkspaceRejectedLegacyObservationDoesNotBind(t *testing.T) {
	ctx := context.Background()
	w, a, p := workspaceTestObjects()
	w.UID, w.Generation, w.ResourceVersion = "workspace-current", 1, "1"
	p.Name, p.Namespace = "profile", w.Namespace
	a.Spec.ProfileRef.Name = p.Name
	setWorkspaceControllerMetadata(w, a)
	scheme := workspaceKeyTestScheme(t)
	sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	sts.UID, sts.ResourceVersion = "legacy-sts", "1"
	svc := buildWorkspaceService(w, a)
	svc.UID, svc.ResourceVersion = "legacy-service", "1"
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, sts, svc, workspaceKeyTestSecret(t, a, "private-literal")).Build()
	writer := &recordingWorkspaceClient{Client: base}
	r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := r.Reconcile(ctx, request); err != nil {
			t.Fatal(err)
		}
		var got achv1alpha1.Workspace
		if err := base.Get(ctx, client.ObjectKeyFromObject(w), &got); err != nil {
			t.Fatal(err)
		}
		condition := apimeta.FindStatusCondition(got.Status.Conditions, "WorkloadApplied")
		if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "WorkspaceConflict" || got.Status.StatefulSetUID != "" {
			t.Fatalf("attempt %d established rejected legacy binding: status=%+v", attempt+1, got.Status)
		}
	}
	if writer.childCreates+writer.childUpdates != 0 {
		t.Fatalf("rejected legacy adoption attempted %d child writes", writer.childCreates+writer.childUpdates)
	}
}

func TestWorkspaceRevalidatesAfterServiceCreate(t *testing.T) {
	ctx := context.Background()
	w, a, p := workspaceTestObjects()
	w.UID, w.Generation, w.ResourceVersion = "workspace-current", 1, "1"
	p.Name, p.Namespace = "profile", w.Namespace
	a.Spec.ProfileRef.Name = p.Name
	setWorkspaceControllerMetadata(w, a)
	scheme := workspaceKeyTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, workspaceKeyTestSecret(t, a, "private-literal")).Build()
	writer := &recordingWorkspaceClient{Client: base}
	writer.afterCreate = func(ctx context.Context, obj client.Object) error {
		if _, ok := obj.(*corev1.Service); !ok {
			return nil
		}
		var current achv1alpha1.Workspace
		if err := base.Get(ctx, client.ObjectKeyFromObject(w), &current); err != nil {
			return err
		}
		current.Generation++
		current.Spec.Replicas = 1
		return base.Update(ctx, &current)
	}
	r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}); err == nil {
		t.Fatal("obsolete request continued after Service create changed the Workspace")
	}
	if writer.childCreates != 1 || len(writer.created) != 1 {
		t.Fatalf("child creates=%d, want only the first Service write", writer.childCreates)
	}
	if _, ok := writer.created[0].(*corev1.Service); !ok {
		t.Fatalf("first child write was %T, want Service", writer.created[0])
	}
}

func TestWorkspaceIdleRefreshPreservesMetadataAndStorage(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name                string
		incompatibleStorage bool
	}{
		{name: "preserve custom template data"},
		{name: "reject changed bootstrap reference", incompatibleStorage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, a, profile := workspaceTestObjects()
			w.UID, w.Generation, w.ResourceVersion = "workspace-current", 1, "1"
			w.Spec.Replicas = 0
			w.Spec.ExpectedStatefulSetUID = "sts-current"
			w.Status.StatefulSetUID = "sts-current"
			profile.Name, profile.Namespace = "profile", w.Namespace
			a.Spec.ProfileRef.Name = profile.Name
			setWorkspaceControllerMetadata(w, a)
			oldProfile := profile.DeepCopy()
			oldProfile.Spec.Execution.EphemeralStorage = "2Gi"
			oldSTS, err := buildWorkspaceStatefulSet(w, a, oldProfile, testVerifyKey)
			if err != nil {
				t.Fatal(err)
			}
			oldSTS.UID, oldSTS.ResourceVersion = "sts-current", "1"
			oldSTS.Spec.Template.Labels["example.test/retain"] = "label"
			oldSTS.Spec.Template.Annotations["example.test/retain"] = "annotation"
			oldSTS.Spec.Template.Spec.Volumes = append(oldSTS.Spec.Template.Spec.Volumes, corev1.Volume{Name: "extra-storage", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "extra-config"}}}})
			if tc.incompatibleStorage {
				for i := range oldSTS.Spec.Template.Spec.Volumes {
					if oldSTS.Spec.Template.Spec.Volumes[i].Name == workspaceBootstrapVolume {
						oldSTS.Spec.Template.Spec.Volumes[i].ConfigMap.Name = "foreign-bootstrap"
					}
				}
			}
			service := buildWorkspaceService(w, a)
			profile.Spec.Execution.Image = "registry.example/execution:v2"
			profile.Spec.Execution.EphemeralStorage = "4Gi"
			desired, err := buildWorkspaceStatefulSet(w, a, profile, testVerifyKey)
			if err != nil {
				t.Fatal(err)
			}
			scheme := workspaceKeyTestScheme(t)
			base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(w, a, profile, oldSTS, service).Build()
			writer := &recordingWorkspaceClient{Client: base}
			r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
			before := oldSTS.DeepCopy()
			_, _, err = r.reconcileWorkspaceWorkload(ctx, w, a, desired, service)
			if tc.incompatibleStorage {
				if !isWorkspaceConflict(err) || writer.childUpdates != 0 {
					t.Fatalf("incompatible bootstrap identity err=%v updates=%d", err, writer.childUpdates)
				}
				var after appsv1.StatefulSet
				if err := base.Get(ctx, client.ObjectKeyFromObject(oldSTS), &after); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, &after) {
					t.Fatal("rejected storage identity changed the full StatefulSet")
				}
				return
			}
			if err != nil || writer.childUpdates != 1 {
				t.Fatalf("idle template refresh err=%v updates=%d", err, writer.childUpdates)
			}
			var after appsv1.StatefulSet
			if err := base.Get(ctx, client.ObjectKeyFromObject(oldSTS), &after); err != nil {
				t.Fatal(err)
			}
			if after.UID != before.UID || !reflect.DeepEqual(after.Spec.Selector, before.Spec.Selector) || after.Spec.Template.Labels["example.test/retain"] != "label" || after.Spec.Template.Annotations["example.test/retain"] != "annotation" {
				t.Fatal("idle refresh replaced UID, selector, or unrelated template metadata")
			}
			expectedVolumes := append([]corev1.Volume(nil), before.Spec.Template.Spec.Volumes...)
			for i := range expectedVolumes {
				if expectedVolumes[i].Name == "workspace" || expectedVolumes[i].Name == "sessions" {
					for _, current := range desired.Spec.Template.Spec.Volumes {
						if current.Name == expectedVolumes[i].Name {
							limit := current.EmptyDir.SizeLimit.DeepCopy()
							expectedVolumes[i].EmptyDir.SizeLimit = &limit
						}
					}
				}
			}
			if !reflect.DeepEqual(after.Spec.Template.Spec.Volumes, expectedVolumes) || !reflect.DeepEqual(workspaceExecutionContainer(after.Spec.Template.Spec.Containers).VolumeMounts, workspaceExecutionContainer(before.Spec.Template.Spec.Containers).VolumeMounts) {
				t.Fatal("idle refresh changed required storage or mount identities")
			}
			writesAfterRefresh := writer.childUpdates
			_, pending, err := r.reconcileWorkspaceWorkload(ctx, w, a, desired, service)
			if err != nil || pending || writer.childUpdates != writesAfterRefresh {
				t.Fatalf("preserved effective template did not converge: err=%v pending=%v updates=%d after first refresh=%d", err, pending, writer.childUpdates, writesAfterRefresh)
			}
		})
	}
}

func TestWorkspaceOwnCreatedUnboundIdleDriftIsObservationOnly(t *testing.T) {
	for _, tc := range []struct {
		name           string
		expectedSTSUID string
		statusSTSUID   string
		wantConflict   bool
	}{
		{name: "same request without status binding"},
		{name: "explicit wrong StatefulSet pin", expectedSTSUID: "replacement-sts", wantConflict: true},
		{name: "status binding names replacement", statusSTSUID: "replacement-sts", wantConflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			w, a, profile := workspaceTestObjects()
			w.UID, w.Generation, w.ResourceVersion = "workspace-current", 4, "1"
			w.Spec.Replicas = 0
			w.Spec.ExpectedStatefulSetUID = tc.expectedSTSUID
			w.Status.StatefulSetUID = tc.statusSTSUID
			profile.Name, profile.Namespace = "profile", w.Namespace
			a.Spec.ProfileRef.Name = profile.Name
			setWorkspaceControllerMetadata(w, a)
			oldProfile := profile.DeepCopy()
			oldProfile.Spec.Execution.Image = "registry.example/execution:previous"
			sts, err := buildWorkspaceStatefulSet(w, a, oldProfile, testVerifyKey)
			if err != nil {
				t.Fatal(err)
			}
			sts.UID, sts.ResourceVersion = "created-sts", "1"
			sts.Annotations[workspaceUIDAnnotation] = string(w.UID)
			sts.Spec.Template.Spec.DeprecatedServiceAccount = sts.Spec.Template.Spec.ServiceAccountName
			service := buildWorkspaceService(w, a)
			scheme := workspaceKeyTestScheme(t)
			base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, profile, sts, service, workspaceKeyTestSecret(t, a, "private-literal")).Build()
			writer := &recordingWorkspaceClient{Client: base}
			r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
			before := sts.DeepCopy()
			if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}); err != nil {
				t.Fatal(err)
			}
			if writer.childCreates+writer.childUpdates != 0 {
				t.Fatalf("observation-only reconciliation attempted child writes: creates=%d updates=%d", writer.childCreates, writer.childUpdates)
			}
			var after appsv1.StatefulSet
			if err := base.Get(ctx, client.ObjectKeyFromObject(sts), &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, &after) {
				t.Fatal("observation-only reconciliation changed the full StatefulSet")
			}
			var got achv1alpha1.Workspace
			if err := base.Get(ctx, client.ObjectKeyFromObject(w), &got); err != nil {
				t.Fatal(err)
			}
			condition := apimeta.FindStatusCondition(got.Status.Conditions, "WorkloadApplied")
			if tc.wantConflict {
				if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "WorkspaceConflict" || got.Status.StatefulSetUID != tc.statusSTSUID {
					t.Fatalf("wrong pin did not remain a conflict: condition=%+v binding=%q want-preserved=%q", condition, got.Status.StatefulSetUID, tc.statusSTSUID)
				}
				return
			}
			if condition == nil || condition.Status != metav1.ConditionTrue || condition.Reason != "Applied" || condition.ObservedGeneration != w.Generation || got.Status.ObservedGeneration != w.Generation || got.Status.StatefulSetUID != string(sts.UID) {
				t.Fatalf("own-created idle observation did not complete current generation: condition=%+v status=%+v", condition, got.Status)
			}
			if apimeta.FindStatusCondition(got.Status.Conditions, "UpdatePending").Status != metav1.ConditionTrue {
				t.Fatal("profile drift was not reported while mutation remained unauthorized")
			}
		})
	}
}

func TestWorkspaceActivationUpdatesLatestTemplateAndReplicasOnce(t *testing.T) {
	ctx := context.Background()
	w, a, profile := workspaceTestObjects()
	w.UID, w.Generation, w.ResourceVersion = "workspace-current", 6, "1"
	w.Spec.Replicas = 1
	w.Spec.ExpectedStatefulSetUID = "sts-current"
	w.Status.StatefulSetUID = "sts-current"
	profile.Name, profile.Namespace = "profile", w.Namespace
	a.Spec.ProfileRef.Name = profile.Name
	setWorkspaceControllerMetadata(w, a)
	oldProfile := profile.DeepCopy()
	oldProfile.Spec.Execution.Image = "registry.example/execution:previous"
	sts, err := buildWorkspaceStatefulSet(w, a, oldProfile, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	sts.UID, sts.ResourceVersion = "sts-current", "1"
	zero := int32(0)
	sts.Spec.Replicas = &zero
	sts.Annotations[workspaceUIDAnnotation] = string(w.UID)
	service := buildWorkspaceService(w, a)
	scheme := workspaceKeyTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, profile, sts, service, workspaceKeyTestSecret(t, a, "private-literal")).Build()
	writer := &recordingWorkspaceClient{Client: base}
	r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}); err != nil {
		t.Fatal(err)
	}
	if writer.childCreates != 0 || writer.childUpdates != 1 || len(writer.updated) != 1 {
		t.Fatalf("activation child writes: creates=%d updates=%d recorded=%d, want one StatefulSet update", writer.childCreates, writer.childUpdates, len(writer.updated))
	}
	updated, ok := writer.updated[0].(*appsv1.StatefulSet)
	if !ok {
		t.Fatalf("activation update was %T, want StatefulSet", writer.updated[0])
	}
	if updated.UID != sts.UID || updated.Spec.Replicas == nil || *updated.Spec.Replicas != 1 || workspaceExecutionContainer(updated.Spec.Template.Spec.Containers).Image != profile.Spec.Execution.Image {
		t.Fatalf("single activation update did not include current template+replicas atomically: uid=%s replicas=%v image=%s", updated.UID, updated.Spec.Replicas, workspaceExecutionContainer(updated.Spec.Template.Spec.Containers).Image)
	}
	var after appsv1.StatefulSet
	if err := base.Get(ctx, client.ObjectKeyFromObject(sts), &after); err != nil {
		t.Fatal(err)
	}
	if after.Spec.Replicas == nil || *after.Spec.Replicas != 1 || workspaceExecutionContainer(after.Spec.Template.Spec.Containers).Image != profile.Spec.Execution.Image {
		t.Fatalf("persisted activation update lacks latest template or replicas: %+v", after.Spec)
	}
}

type workspaceOwnerFailureCase struct {
	name           string
	workloadReason string
	ownerReason    string
	omitAgent      bool
	omitProfile    bool
	keyState       string
	invalidRender  bool
}

type workspaceOwnerFailureFixture struct {
	w             *achv1alpha1.Workspace
	a             *achv1alpha1.ACHAgent
	p             *achv1alpha1.AgentProfile
	secretOwner   *achv1alpha1.ACHAgent
	sts           *appsv1.StatefulSet
	service       *corev1.Service
	base          client.Client
	writer        *recordingWorkspaceClient
	reconciler    *WorkspaceReconciler
	request       reconcile.Request
	beforeSTS     *appsv1.StatefulSet
	beforeService *corev1.Service
}

func TestWorkspaceOwnerAndRenderFailureMatrixPreservesChildrenAndRecovers(t *testing.T) {
	cases := []workspaceOwnerFailureCase{
		{name: "missing Agent", ownerReason: "AgentNotFound", workloadReason: "OwnerUnavailable", omitAgent: true},
		{name: "missing Profile", ownerReason: "ProfileNotFound", workloadReason: "OwnerUnavailable", omitProfile: true},
		{name: "missing key", ownerReason: "OwnerResolved", workloadReason: "WorkspaceKeyUnavailable", keyState: "missing"},
		{name: "empty key", ownerReason: "OwnerResolved", workloadReason: "WorkspaceKeyUnavailable", keyState: "empty"},
		{name: "unowned key", ownerReason: "OwnerResolved", workloadReason: "WorkspaceKeyUnavailable", keyState: "unowned"},
		{name: "prior UID key", ownerReason: "OwnerResolved", workloadReason: "WorkspaceKeyUnavailable", keyState: "prior UID"},
		{name: "invalid execution render", ownerReason: "OwnerResolved", workloadReason: "ExecutionRenderFailed", invalidRender: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runWorkspaceOwnerFailureCase(t, tc) })
	}
}

func TestWorkspaceRendererRejectsEmptyExecutionImage(t *testing.T) {
	w, a, profile := workspaceTestObjects()
	profile.Spec.Execution.Image = ""

	if _, err := buildWorkspaceStatefulSet(w, a, profile, testVerifyKey); err == nil || err.Error() != "AgentProfile spec.execution.image is required" {
		t.Fatalf("buildWorkspaceStatefulSet() error = %v, want required-image error", err)
	}
}

func runWorkspaceOwnerFailureCase(t *testing.T, tc workspaceOwnerFailureCase) {
	t.Helper()
	fixture := newWorkspaceOwnerFailureFixture(t, tc)
	if _, err := fixture.reconciler.Reconcile(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	assertWorkspaceFailureWasReadOnly(t, fixture, tc)
	recoverWorkspaceFailureInput(t, fixture, tc)
	if _, err := fixture.reconciler.Reconcile(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if fixture.writer.childCreates+fixture.writer.childUpdates != 0 {
		t.Fatalf("recovered valid input needlessly mutated retained children: creates=%d updates=%d", fixture.writer.childCreates, fixture.writer.childUpdates)
	}
	var recovered achv1alpha1.Workspace
	if err := fixture.base.Get(context.Background(), client.ObjectKeyFromObject(fixture.w), &recovered); err != nil {
		t.Fatal(err)
	}
	condition := apimeta.FindStatusCondition(recovered.Status.Conditions, "WorkloadApplied")
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.Reason != "Applied" || recovered.Status.ObservedGeneration != fixture.w.Generation {
		t.Fatalf("valid input did not recover current generation: %+v", recovered.Status)
	}
}

func newWorkspaceOwnerFailureFixture(t *testing.T, tc workspaceOwnerFailureCase) workspaceOwnerFailureFixture {
	t.Helper()
	w, a, p := workspaceTestObjects()
	w.UID, w.Generation, w.ResourceVersion = "workspace-current", 7, "1"
	p.Name, p.Namespace = "profile", w.Namespace
	a.Spec.ProfileRef.Name = p.Name
	setWorkspaceControllerMetadata(w, a)
	buildAgent := a.DeepCopy()
	buildAgent.UID = types.UID(w.Spec.AgentRef.UID)
	validProfile := p.DeepCopy()
	validProfile.Spec.Execution.EphemeralStorage = "3Gi"
	sts, err := buildWorkspaceStatefulSet(w, buildAgent, validProfile, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	sts.UID, sts.ResourceVersion = "sts-current", "1"
	sts.Annotations[workspaceUIDAnnotation] = string(w.UID)
	service := buildWorkspaceService(w, buildAgent)
	secretOwner := &achv1alpha1.ACHAgent{ObjectMeta: metav1.ObjectMeta{Name: a.Name, Namespace: a.Namespace, UID: types.UID(w.Spec.AgentRef.UID)}}
	secret := workspaceKeyTestSecret(t, secretOwner, "private-literal")
	objects := []client.Object{w, sts, service}
	addWorkspaceFailureObjects(&objects, tc, a, p, secret)
	scheme := workspaceKeyTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(objects...).Build()
	writer := &recordingWorkspaceClient{Client: base}
	return workspaceOwnerFailureFixture{
		w: w, a: a, p: p, secretOwner: secretOwner, sts: sts, service: service,
		base: base, writer: writer, reconciler: &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme},
		request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}, beforeSTS: sts.DeepCopy(), beforeService: service.DeepCopy(),
	}
}

func addWorkspaceFailureObjects(objects *[]client.Object, tc workspaceOwnerFailureCase, a *achv1alpha1.ACHAgent, p *achv1alpha1.AgentProfile, secret *corev1.Secret) {
	if !tc.omitAgent {
		*objects = append(*objects, a)
	}
	if !tc.omitProfile {
		if tc.invalidRender {
			p.Spec.Execution.EphemeralStorage = "bad-quantity"
		}
		*objects = append(*objects, p)
	}
	switch tc.keyState {
	case "missing":
		return
	case "empty":
		secret.Data["key"] = nil
	case "unowned":
		secret.OwnerReferences = nil
	case "prior UID":
		secret.OwnerReferences[0].UID = "prior-agent-uid"
	}
	*objects = append(*objects, secret)
}

func assertWorkspaceFailureWasReadOnly(t *testing.T, fixture workspaceOwnerFailureFixture, tc workspaceOwnerFailureCase) {
	t.Helper()
	if fixture.writer.childCreates+fixture.writer.childUpdates != 0 {
		t.Fatalf("failed evaluation attempted child writes: creates=%d updates=%d", fixture.writer.childCreates, fixture.writer.childUpdates)
	}
	var gotSTS appsv1.StatefulSet
	var gotService corev1.Service
	if err := fixture.base.Get(context.Background(), client.ObjectKeyFromObject(fixture.sts), &gotSTS); err != nil {
		t.Fatal(err)
	}
	if err := fixture.base.Get(context.Background(), client.ObjectKeyFromObject(fixture.service), &gotService); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.beforeSTS, &gotSTS) || !reflect.DeepEqual(fixture.beforeService, &gotService) {
		t.Fatal("failed evaluation changed a full StatefulSet or Service")
	}
	var failed achv1alpha1.Workspace
	if err := fixture.base.Get(context.Background(), client.ObjectKeyFromObject(fixture.w), &failed); err != nil {
		t.Fatal(err)
	}
	workload := apimeta.FindStatusCondition(failed.Status.Conditions, "WorkloadApplied")
	owner := apimeta.FindStatusCondition(failed.Status.Conditions, "OwnerResolved")
	if workload == nil || workload.Status != metav1.ConditionFalse || workload.Reason != tc.workloadReason || owner == nil || owner.Reason != tc.ownerReason {
		t.Fatalf("failure conditions owner=%+v workload=%+v", owner, workload)
	}
	for _, condition := range failed.Status.Conditions {
		if condition.ObservedGeneration != fixture.w.Generation {
			t.Fatalf("failure condition %s observed generation %d, want %d", condition.Type, condition.ObservedGeneration, fixture.w.Generation)
		}
	}
}

func recoverWorkspaceFailureInput(t *testing.T, fixture workspaceOwnerFailureFixture, tc workspaceOwnerFailureCase) {
	t.Helper()
	if tc.omitAgent {
		if err := fixture.base.Create(context.Background(), fixture.a.DeepCopy()); err != nil {
			t.Fatal(err)
		}
	}
	if tc.omitProfile {
		if err := fixture.base.Create(context.Background(), fixture.p.DeepCopy()); err != nil {
			t.Fatal(err)
		}
	}
	recoverWorkspaceKey(t, fixture, tc)
	recoverWorkspaceProfile(t, fixture, tc)
}

func recoverWorkspaceKey(t *testing.T, fixture workspaceOwnerFailureFixture, tc workspaceOwnerFailureCase) {
	if tc.keyState == "" {
		return
	}
	valid := workspaceKeyTestSecret(t, fixture.secretOwner, "private-literal")
	if tc.keyState == "missing" {
		if err := fixture.base.Create(context.Background(), valid); err != nil {
			t.Fatal(err)
		}
		return
	}
	var current corev1.Secret
	if err := fixture.base.Get(context.Background(), client.ObjectKeyFromObject(valid), &current); err != nil {
		t.Fatal(err)
	}
	current.Data, current.OwnerReferences = valid.Data, valid.OwnerReferences
	if err := fixture.base.Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
}

func recoverWorkspaceProfile(t *testing.T, fixture workspaceOwnerFailureFixture, tc workspaceOwnerFailureCase) {
	if !tc.invalidRender {
		return
	}
	var current achv1alpha1.AgentProfile
	if err := fixture.base.Get(context.Background(), client.ObjectKeyFromObject(fixture.p), &current); err != nil {
		t.Fatal(err)
	}
	current.Spec.Execution.EphemeralStorage = "3Gi"
	if err := fixture.base.Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceIdentityConflicts(t *testing.T) {
	w, a, p := workspaceTestObjects()
	w.UID = types.UID("workspace-uid")
	setWorkspaceControllerMetadata(w, a)
	if err := validateWorkspaceIdentity(w, a); err != nil {
		t.Fatalf("valid identity rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*achv1alpha1.Workspace, *achv1alpha1.ACHAgent)
	}{
		{name: "wrong live agent UID", mutate: func(_ *achv1alpha1.Workspace, a *achv1alpha1.ACHAgent) {
			a.UID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		}},
		{name: "wrong CR derived name", mutate: func(w *achv1alpha1.Workspace, _ *achv1alpha1.ACHAgent) { w.Name += "-collision" }},
		{name: "malformed CR identity", mutate: func(w *achv1alpha1.Workspace, _ *achv1alpha1.ACHAgent) { w.Spec.WorkspaceRef = "short" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotW, gotA := w.DeepCopy(), a.DeepCopy()
			tc.mutate(gotW, gotA)
			if err := validateWorkspaceIdentity(gotW, gotA); err == nil {
				t.Fatal("identity conflict accepted")
			}
		})
	}

	sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	svc := buildWorkspaceService(w, a)
	for _, tc := range []struct {
		name   string
		mutate func(*appsv1.StatefulSet, *corev1.Service)
	}{
		{name: "child full digest", mutate: func(s *appsv1.StatefulSet, _ *corev1.Service) { s.Annotations[workspaceRefAnnotation] = "different" }},
		{name: "child controller owner", mutate: func(s *appsv1.StatefulSet, _ *corev1.Service) { s.OwnerReferences[0].UID = "prior-agent" }},
		{name: "selector identity", mutate: func(s *appsv1.StatefulSet, _ *corev1.Service) {
			s.Spec.Selector.MatchLabels[workspaceNameLabel] = "collision"
		}},
		{name: "service selector", mutate: func(_ *appsv1.StatefulSet, s *corev1.Service) { s.Spec.Selector[workspaceNameLabel] = "collision" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotSTS, gotService := sts.DeepCopy(), svc.DeepCopy()
			tc.mutate(gotSTS, gotService)
			beforeSTS, beforeService := gotSTS.DeepCopy(), gotService.DeepCopy()
			obs := &workspaceObservation{statefulSet: gotSTS, service: gotService}
			if err := validateWorkspaceChildren(w, a, obs); err == nil {
				t.Fatal("child identity conflict accepted")
			}
			if !reflect.DeepEqual(beforeSTS, gotSTS) || !reflect.DeepEqual(beforeService, gotService) {
				t.Fatal("validator mutated a full observed child object")
			}
		})
	}

	// A same truncated resource name cannot make another full digest an owner.
	other := w.DeepCopy()
	other.Spec.WorkspaceRef = "0123456789abcdef0123" + "f" + w.Spec.WorkspaceRef[21:]
	if workspaceResourceName(a.Name, other.Spec.WorkspaceRef) != w.Name {
		t.Fatal("test collision no longer shares the truncated child name")
	}
	obs := &workspaceObservation{statefulSet: sts.DeepCopy(), service: svc.DeepCopy()}
	obs.statefulSet.Annotations[workspaceRefAnnotation] = other.Spec.WorkspaceRef
	if err := validateWorkspaceChildren(other, a, obs); err == nil {
		t.Fatal("truncated-name digest collision accepted")
	}
}

func TestWorkspaceIdentityRejectsLegacyUIDName(t *testing.T) {
	ctx := context.Background()
	w, a, p := workspaceTestObjects()
	w.UID, w.Generation, w.ResourceVersion = "workspace-current", 4, "1"
	p.Name, p.Namespace = "profile", w.Namespace
	a.Spec.ProfileRef.Name = p.Name
	setWorkspaceControllerMetadata(w, a)
	w.Name = "ach-ws-3fa0b3b29c7a4e1d8a2f-0123456789abcdef0123"
	scheme := workspaceKeyTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, workspaceKeyTestSecret(t, a, "private-literal")).Build()
	writer := &recordingWorkspaceClient{Client: base}
	r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}); err != nil {
		t.Fatal(err)
	}
	if writer.childCreates+writer.childUpdates != 0 {
		t.Fatalf("legacy UID-derived name attempted %d child writes", writer.childCreates+writer.childUpdates)
	}
	var got achv1alpha1.Workspace
	if err := base.Get(ctx, client.ObjectKeyFromObject(w), &got); err != nil {
		t.Fatal(err)
	}
	condition := apimeta.FindStatusCondition(got.Status.Conditions, "WorkloadApplied")
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "WorkspaceConflict" || condition.ObservedGeneration != w.Generation {
		t.Fatalf("condition=%+v, want current-generation WorkspaceConflict", condition)
	}
}

func TestWorkspaceChildConflictsDoNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*appsv1.StatefulSet, *corev1.Service)
	}{
		{name: "StatefulSet digest", mutate: func(s *appsv1.StatefulSet, _ *corev1.Service) {
			s.Annotations[workspaceRefAnnotation] = strings.Repeat("b", 64)
		}},
		{name: "StatefulSet owner", mutate: func(s *appsv1.StatefulSet, _ *corev1.Service) { s.OwnerReferences[0].UID = "prior-agent" }},
		{name: "StatefulSet selector", mutate: func(s *appsv1.StatefulSet, _ *corev1.Service) {
			s.Spec.Selector.MatchLabels[workspaceNameLabel] = "collision"
		}},
		{name: "Service selector", mutate: func(_ *appsv1.StatefulSet, s *corev1.Service) { s.Spec.Selector[workspaceNameLabel] = "collision" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			w, a, p := workspaceTestObjects()
			w.UID, w.Generation, w.ResourceVersion = "workspace-current", 5, "1"
			p.Name, p.Namespace = "profile", w.Namespace
			a.Spec.ProfileRef.Name = p.Name
			setWorkspaceControllerMetadata(w, a)
			scheme := workspaceKeyTestScheme(t)
			sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
			if err != nil {
				t.Fatal(err)
			}
			sts.UID, sts.ResourceVersion = "sts-current", "1"
			svc := buildWorkspaceService(w, a)
			svc.UID, svc.ResourceVersion = "service-current", "1"
			tc.mutate(sts, svc)
			beforeSTS, beforeService := sts.DeepCopy(), svc.DeepCopy()
			base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, sts, svc, workspaceKeyTestSecret(t, a, "private-literal")).Build()
			writer := &recordingWorkspaceClient{Client: base}
			r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
			if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}); err != nil {
				t.Fatal(err)
			}
			if writer.childCreates+writer.childUpdates != 0 {
				t.Fatalf("rejected child observation attempted %d writes", writer.childCreates+writer.childUpdates)
			}
			var gotSTS appsv1.StatefulSet
			if err := base.Get(ctx, client.ObjectKeyFromObject(beforeSTS), &gotSTS); err != nil {
				t.Fatal(err)
			}
			var gotService corev1.Service
			if err := base.Get(ctx, client.ObjectKeyFromObject(beforeService), &gotService); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeSTS, &gotSTS) || !reflect.DeepEqual(beforeService, &gotService) {
				t.Fatal("rejected child observation changed a full child object")
			}
			var gotWorkspace achv1alpha1.Workspace
			if err := base.Get(ctx, client.ObjectKeyFromObject(w), &gotWorkspace); err != nil {
				t.Fatal(err)
			}
			condition := apimeta.FindStatusCondition(gotWorkspace.Status.Conditions, "WorkloadApplied")
			if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "WorkspaceConflict" || condition.ObservedGeneration != w.Generation {
				t.Fatalf("condition=%+v, want current-generation WorkspaceConflict", condition)
			}
		})
	}
}

func TestWorkspacePreconditionConflicts(t *testing.T) {
	w, a, p := workspaceTestObjects()
	sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	sts.UID = types.UID("sts-current")
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: w.Name + "-0", Namespace: w.Namespace, UID: "pod-current", OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: w.Name, UID: sts.UID}}}}
	tests := []struct {
		name     string
		replicas int32
		sts      *appsv1.StatefulSet
		pods     []corev1.Pod
		expected string
		wantErr  bool
	}{
		{name: "unbound pre-existing StatefulSet", replicas: 1, sts: sts.DeepCopy(), wantErr: true},
		{name: "wrong expected StatefulSet UID", replicas: 1, sts: sts.DeepCopy(), expected: "sts-replacement", wantErr: true},
		{name: "expected UID absent does not authorize create", replicas: 1, expected: "sts-current", wantErr: true},
		{name: "missing pod UID for close", replicas: 0, sts: sts.DeepCopy(), pods: []corev1.Pod{*pod.DeepCopy()}, wantErr: true},
		{name: "wrong pod UID for close", replicas: 0, sts: sts.DeepCopy(), pods: []corev1.Pod{*pod.DeepCopy()}, expected: "sts-current", wantErr: true},
		{name: "vanished expected pod allows zero", replicas: 0, sts: sts.DeepCopy(), expected: "sts-current", wantErr: false},
		{name: "active adoption requires pod UID", replicas: 1, sts: sts.DeepCopy(), pods: []corev1.Pod{*pod.DeepCopy()}, expected: "sts-current", wantErr: true},
		{name: "active adoption wrong pod UID", replicas: 1, sts: sts.DeepCopy(), pods: []corev1.Pod{*pod.DeepCopy()}, expected: "sts-current", wantErr: true},
		{name: "replacement pod conflicts", replicas: 0, sts: sts.DeepCopy(), pods: []corev1.Pod{*pod.DeepCopy()}, expected: "sts-current", wantErr: true},
		{name: "multiple matching pods conflict", replicas: 0, sts: sts.DeepCopy(), pods: []corev1.Pod{*pod.DeepCopy(), *pod.DeepCopy()}, expected: "sts-current", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := w.DeepCopy()
			got.Spec.Replicas = tc.replicas
			got.Spec.ExpectedStatefulSetUID = tc.expected
			if tc.name == "vanished expected pod allows zero" {
				got.Spec.ExpectedPodUID = "pod-previous"
			}
			if tc.name == "missing pod UID for close" {
				got.Spec.ExpectedStatefulSetUID = "sts-current"
			}
			if tc.name == "wrong pod UID for close" {
				got.Spec.ExpectedPodUID = "pod-previous"
			}
			if tc.name == "active adoption requires pod UID" {
				got.Spec.ExpectedPodUID = ""
			}
			if tc.name == "active adoption wrong pod UID" {
				got.Spec.ExpectedPodUID = "pod-previous"
			}
			if tc.name == "replacement pod conflicts" {
				got.Spec.ExpectedPodUID = "pod-replacement"
			}
			if tc.name == "multiple matching pods conflict" {
				got.Spec.ExpectedPodUID = "pod-current"
			}
			obs := &workspaceObservation{statefulSet: tc.sts, pods: tc.pods}
			err := validateWorkspacePreconditions(got, obs)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateWorkspacePreconditions() error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestWorkspacePartialCreateObservation(t *testing.T) {
	ctx := context.Background()
	w, a, p := workspaceTestObjects()
	w.UID, w.Generation = "workspace-uid", 3
	w.ResourceVersion = "1"
	p.Name, p.Namespace = "profile", w.Namespace
	a.Spec.ProfileRef.Name = "profile"
	setWorkspaceControllerMetadata(w, a)
	scheme := workspaceKeyTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, workspaceKeyTestSecret(t, a, "private-literal")).Build()
	observed := &recordingWorkspaceClient{Client: base, failStatus: true}
	r := &WorkspaceReconciler{Client: observed, APIReader: observed, Scheme: scheme}
	request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}
	if _, err := r.Reconcile(ctx, request); err == nil || err.Error() != "injected first status failure" {
		t.Fatalf("first reconcile error=%v, want injected status failure", err)
	}
	key := types.NamespacedName{Namespace: w.Namespace, Name: w.Name}
	var created appsv1.StatefulSet
	if err := base.Get(ctx, key, &created); err != nil {
		t.Fatalf("initial StatefulSet create was lost: %v", err)
	}
	if created.Annotations[workspaceUIDAnnotation] != string(w.UID) {
		t.Fatalf("created marker=%q, want CR UID %q", created.Annotations[workspaceUIDAnnotation], w.UID)
	}
	createdUID := created.UID
	createsAfterFirst := observed.childCreates
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatalf("reconcile after failed status write: %v", err)
	}
	if observed.childCreates != createsAfterFirst || observed.childUpdates != 0 {
		t.Fatal("status retry recreated or patched the controller-created StatefulSet")
	}
	var reported achv1alpha1.Workspace
	if err := base.Get(ctx, client.ObjectKeyFromObject(w), &reported); err != nil {
		t.Fatal(err)
	}
	if reported.Status.ObservedGeneration != w.Generation || reported.Status.StatefulSetUID != string(createdUID) || apimeta.FindStatusCondition(reported.Status.Conditions, "WorkloadApplied").Status != metav1.ConditionTrue {
		t.Fatalf("status after retry=%+v", reported.Status)
	}

	reported.Generation++
	if err := base.Update(ctx, &reported); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, request); err != nil {
		t.Fatalf("generation advance observation: %v", err)
	}
	if observed.childCreates != createsAfterFirst || observed.childUpdates != 0 {
		t.Fatal("generation advance mutated the own-created workload")
	}
	if err := base.Get(ctx, client.ObjectKeyFromObject(w), &reported); err != nil {
		t.Fatal(err)
	}
	if reported.Status.ObservedGeneration != reported.Generation {
		t.Fatalf("observed generation=%d, want %d", reported.Status.ObservedGeneration, reported.Generation)
	}
}

func TestWorkspacePartialCreateNegativeObservations(t *testing.T) {
	for _, tc := range []struct {
		name           string
		marker         string
		replicas       int32
		expectedSTSUID string
	}{
		{name: "absent marker", replicas: 0},
		{name: "different marker", marker: "other-workspace", replicas: 0},
		{name: "changed requested replicas", marker: "workspace-current", replicas: 1},
		{name: "explicit wrong expected UID", marker: "workspace-current", replicas: 0, expectedSTSUID: "replacement-sts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			w, a, p := workspaceTestObjects()
			w.UID, w.Generation, w.ResourceVersion = "workspace-current", 1, "1"
			w.Spec.Replicas = tc.replicas
			w.Spec.ExpectedStatefulSetUID = tc.expectedSTSUID
			p.Name, p.Namespace = "profile", w.Namespace
			a.Spec.ProfileRef.Name = p.Name
			setWorkspaceControllerMetadata(w, a)
			scheme := workspaceKeyTestScheme(t)
			sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
			if err != nil {
				t.Fatal(err)
			}
			sts.UID, sts.ResourceVersion = "sts-current", "1"
			if tc.name == "changed requested replicas" {
				zero := int32(0)
				sts.Spec.Replicas = &zero
			}
			if tc.marker != "" {
				sts.Annotations[workspaceUIDAnnotation] = tc.marker
			}
			beforeSTS := sts.DeepCopy()
			service := buildWorkspaceService(w, a)
			base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, sts, service, workspaceKeyTestSecret(t, a, "private-literal")).Build()
			writer := &recordingWorkspaceClient{Client: base}
			r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
			if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}); err != nil {
				t.Fatal(err)
			}
			var got achv1alpha1.Workspace
			if err := base.Get(ctx, client.ObjectKeyFromObject(w), &got); err != nil {
				t.Fatal(err)
			}
			condition := apimeta.FindStatusCondition(got.Status.Conditions, "WorkloadApplied")
			if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "WorkspaceConflict" || got.Status.StatefulSetUID != "" {
				t.Fatalf("unexpected status after unauthorized observation: %+v", got.Status)
			}
			if writer.childCreates+writer.childUpdates != 0 {
				t.Fatalf("unauthorized observation attempted %d child writes", writer.childCreates+writer.childUpdates)
			}
			var afterSTS appsv1.StatefulSet
			if err := base.Get(ctx, client.ObjectKeyFromObject(sts), &afterSTS); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeSTS, &afterSTS) {
				t.Fatal("unauthorized observation changed a full StatefulSet")
			}
		})
	}
}

func TestWorkspaceTemplateComparisonUsesAPIDefaults(t *testing.T) {
	w, a, p := workspaceTestObjects()
	desired, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	apiRoundTripped := desired.DeepCopy()
	container := &apiRoundTripped.Spec.Template.Spec.Containers[0]
	container.ImagePullPolicy = corev1.PullIfNotPresent
	container.TerminationMessagePath = "/dev/termination-log"
	container.TerminationMessagePolicy = corev1.TerminationMessageReadFile
	container.ReadinessProbe.TimeoutSeconds = 1
	container.ReadinessProbe.PeriodSeconds = 10
	container.ReadinessProbe.SuccessThreshold = 1
	container.ReadinessProbe.FailureThreshold = 3
	container.ReadinessProbe.HTTPGet.Scheme = corev1.URISchemeHTTP
	container.LivenessProbe.TimeoutSeconds = 1
	container.LivenessProbe.PeriodSeconds = 10
	container.LivenessProbe.SuccessThreshold = 1
	container.LivenessProbe.FailureThreshold = 3
	container.LivenessProbe.HTTPGet.Scheme = corev1.URISchemeHTTP
	for i := range container.Ports {
		container.Ports[i].Protocol = corev1.ProtocolTCP
	}
	for i := range container.Env {
		if source := container.Env[i].ValueFrom; source != nil && source.FieldRef != nil {
			source.FieldRef.APIVersion = "v1"
		}
	}
	apiRoundTripped.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
	apiRoundTripped.Spec.Template.Spec.DNSPolicy = corev1.DNSClusterFirst
	apiRoundTripped.Spec.Template.Spec.DeprecatedServiceAccount = apiRoundTripped.Spec.Template.Spec.ServiceAccountName
	if workspaceTemplateDrift(apiRoundTripped, desired) {
		t.Fatal("standard API defaulting alone was treated as Workspace template drift")
	}
	defaultedServiceLinks := apiRoundTripped.DeepCopy()
	serviceLinks := true
	defaultedServiceLinks.Spec.Template.Spec.EnableServiceLinks = &serviceLinks
	if !workspaceTemplateDrift(defaultedServiceLinks, desired) {
		t.Fatal("API-defaulted serviceLinks=true was treated as equivalent to desired false")
	}
}

func TestWorkspaceAPIDefaultsDoNotAttemptChildUpdate(t *testing.T) {
	ctx := context.Background()
	w, a, p := workspaceTestObjects()
	w.UID, w.Generation, w.ResourceVersion = "workspace-current", 2, "1"
	w.Spec.Replicas = 0
	w.Spec.ExpectedStatefulSetUID = "sts-current"
	w.Status.StatefulSetUID = "sts-current"
	p.Name, p.Namespace = "profile", w.Namespace
	a.Spec.ProfileRef.Name = p.Name
	setWorkspaceControllerMetadata(w, a)
	desired, err := buildWorkspaceStatefulSet(w, a, p, workspaceEngineVerifyKey([]byte("private-literal")))
	if err != nil {
		t.Fatal(err)
	}
	sts := desired.DeepCopy()
	sts.UID, sts.ResourceVersion = "sts-current", "1"
	zero := int32(0)
	sts.Spec.Replicas = &zero
	sts.Annotations[workspaceUIDAnnotation] = string(w.UID)
	sts.Spec.Template.Spec.DeprecatedServiceAccount = sts.Spec.Template.Spec.ServiceAccountName
	before := sts.DeepCopy()
	service := buildWorkspaceService(w, a)
	scheme := workspaceKeyTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, sts, service, workspaceKeyTestSecret(t, a, "private-literal")).Build()
	writer := &recordingWorkspaceClient{Client: base}
	r := &WorkspaceReconciler{Client: writer, APIReader: writer, Scheme: scheme}
	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)}); err != nil {
		t.Fatal(err)
	}
	if writer.childCreates+writer.childUpdates != 0 {
		t.Fatalf("API defaults caused child writes: creates=%d updates=%d", writer.childCreates, writer.childUpdates)
	}
	var after appsv1.StatefulSet
	if err := base.Get(ctx, client.ObjectKeyFromObject(sts), &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, &after) {
		t.Fatal("API-defaulted StatefulSet changed during no-drift reconciliation")
	}
	var got achv1alpha1.Workspace
	if err := base.Get(ctx, client.ObjectKeyFromObject(w), &got); err != nil {
		t.Fatal(err)
	}
	if apimeta.FindStatusCondition(got.Status.Conditions, "UpdatePending").Status != metav1.ConditionFalse {
		t.Fatal("API defaults were reported as pending profile drift")
	}
}

func TestWorkspaceFreshReadAndResourceVersionGuard(t *testing.T) {
	ctx := context.Background()
	w, a, p := workspaceTestObjects()
	w.UID, w.Spec.ExpectedStatefulSetUID = "workspace-uid", "sts-old"
	w.Spec.Replicas = 0
	p.Name, p.Namespace = "profile", w.Namespace
	a.Spec.ProfileRef.Name = p.Name
	setWorkspaceControllerMetadata(w, a)
	scheme := workspaceKeyTestScheme(t)
	oldSTS, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	oldSTS.UID, oldSTS.ResourceVersion = "sts-old", "1"
	service := buildWorkspaceService(w, a)
	oldCache := fake.NewClientBuilder().WithScheme(scheme).WithObjects(w, a, p, oldSTS, service).Build()
	freshSTS := oldSTS.DeepCopy()
	freshSTS.UID, freshSTS.ResourceVersion = "sts-replacement", "2"
	freshReader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(w, a, p, freshSTS, service).Build()
	writer := &recordingWorkspaceClient{Client: oldCache}
	r := &WorkspaceReconciler{Client: writer, APIReader: freshReader, Scheme: scheme}
	desired, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = r.reconcileWorkspaceWorkload(ctx, w, a, desired, service)
	if !isWorkspaceConflict(err) || writer.childCreates+writer.childUpdates != 0 {
		t.Fatalf("fresh replacement was not rejected before mutation: err=%v child writes=%d", err, writer.childCreates+writer.childUpdates)
	}

	// A stale-RV update conflict is returned once. The next reconcile performs
	// fresh reads again before applying the newer desired template.
	w.Spec.ExpectedStatefulSetUID = "sts-old"
	w.Status.StatefulSetUID = "sts-old"
	profileChanged := p.DeepCopy()
	profileChanged.Spec.Execution.Image = "registry.test/exec:new"
	desiredNew, err := buildWorkspaceStatefulSet(w, a, profileChanged, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	state := fake.NewClientBuilder().WithScheme(scheme).WithObjects(w, a, p, oldSTS, service).Build()
	writer = &recordingWorkspaceClient{Client: state, conflictNext: true}
	reader := &countingWorkspaceReader{Reader: writer}
	r = &WorkspaceReconciler{Client: writer, APIReader: reader, Scheme: scheme}
	_, _, err = r.reconcileWorkspaceWorkload(ctx, w, a, desiredNew, service)
	if !apierrors.IsConflict(err) || writer.childUpdates != 1 {
		t.Fatalf("stale resourceVersion conflict err=%v updates=%d", err, writer.childUpdates)
	}
	readsAfterConflict := reader.gets
	_, _, err = r.reconcileWorkspaceWorkload(ctx, w, a, desiredNew, service)
	if err != nil {
		t.Fatalf("fresh retry reconcile: %v", err)
	}
	if reader.gets <= readsAfterConflict || writer.childUpdates != 2 {
		t.Fatalf("next reconcile did not repeat reads/update: reads=%d→%d updates=%d", readsAfterConflict, reader.gets, writer.childUpdates)
	}
}

type countingWorkspaceReader struct {
	client.Reader
	gets int
}

func (r *countingWorkspaceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	r.gets++
	return r.Reader.Get(ctx, key, obj, opts...)
}

func TestWorkspaceGenerationRace(t *testing.T) {
	ctx := context.Background()
	w, _, _ := workspaceTestObjects()
	w.UID, w.Generation = "workspace-uid", 2
	scheme := workspaceKeyTestScheme(t)
	current := w.DeepCopy()
	current.Generation = 3
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(current).Build()
	r := &WorkspaceReconciler{Client: c, APIReader: c}
	result, err := r.finishWorkspace(ctx, w, workspaceUnevaluatedConditions(w.Generation), "sts-current")
	if err != nil {
		t.Fatalf("finishWorkspace should reject a newer CR generation, got result=%+v err=%v", result, err)
	}
	var after achv1alpha1.Workspace
	if err := c.Get(ctx, client.ObjectKeyFromObject(w), &after); err != nil {
		t.Fatal(err)
	}
	if after.Status.ObservedGeneration != 0 || after.Status.StatefulSetUID != "" {
		t.Fatalf("stale evaluation wrote status: %+v", after.Status)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*achv1alpha1.Workspace)
	}{
		{name: "generation changes before child write", mutate: func(current *achv1alpha1.Workspace) { current.Generation++; current.ResourceVersion = "2" }},
		{name: "CR incarnation changes before child write", mutate: func(current *achv1alpha1.Workspace) {
			current.UID = "replacement-workspace-uid"
			current.ResourceVersion = "2"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, a, p := workspaceTestObjects()
			w.UID, w.Generation, w.ResourceVersion = "workspace-uid", 1, "1"
			p.Name, p.Namespace = "profile", w.Namespace
			a.Spec.ProfileRef.Name = p.Name
			setWorkspaceControllerMetadata(w, a)
			scheme := workspaceKeyTestScheme(t)
			base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, p, workspaceKeyTestSecret(t, a, "private-literal")).Build()
			changed := w.DeepCopy()
			tc.mutate(changed)
			reader := &workspaceChangingReader{Reader: base, changed: changed, changeAt: 2}
			writer := &recordingWorkspaceClient{Client: base}
			r := &WorkspaceReconciler{Client: writer, APIReader: reader, Scheme: scheme}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)})
			if err == nil {
				t.Fatal("stale request unexpectedly completed without error/requeue")
			}
			if writer.childCreates+writer.childUpdates != 0 {
				t.Fatalf("stale request mutated children: creates=%d updates=%d", writer.childCreates, writer.childUpdates)
			}
			var final achv1alpha1.Workspace
			if err := base.Get(ctx, client.ObjectKeyFromObject(w), &final); err != nil {
				t.Fatal(err)
			}
			if final.Status.ObservedGeneration != 0 {
				t.Fatalf("stale request stamped observedGeneration=%d", final.Status.ObservedGeneration)
			}
		})
	}
}

type workspaceChangingReader struct {
	client.Reader
	changed       *achv1alpha1.Workspace
	changeAt      int
	workspaceGets int
}

func (r *workspaceChangingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*achv1alpha1.Workspace); ok {
		r.workspaceGets++
		if r.workspaceGets == r.changeAt {
			r.changed.DeepCopyInto(obj.(*achv1alpha1.Workspace))
			return nil
		}
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func TestWorkspaceWatchMapping(t *testing.T) {
	w, a, p := workspaceTestObjects()
	a.UID = types.UID(testWorkspaceAgentUID)
	a.Spec.ProfileRef.Name = "profile"
	w.UID = "workspace-uid"
	p.Name, p.Namespace = "profile", w.Namespace
	objects := []client.Object{w, a, p}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := achv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	r := &WorkspaceReconciler{Client: c, APIReader: c, Scheme: scheme}
	want := []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: w.Namespace, Name: w.Name}}}
	if got := r.workspacesForAgent(context.Background(), a); !reflect.DeepEqual(got, want) {
		t.Fatalf("agent mapping=%v, want %v", got, want)
	}
	if got := r.workspacesForProfile(context.Background(), p); !reflect.DeepEqual(got, want) {
		t.Fatalf("profile mapping=%v, want %v", got, want)
	}
	sts, err := buildWorkspaceStatefulSet(w, a, p, testVerifyKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.workspacesForWorkload(context.Background(), sts); !reflect.DeepEqual(got, want) {
		t.Fatalf("StatefulSet mapping=%v, want %v", got, want)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: w.Namespace, Name: w.Name + "-0"}}
	if got := r.workspacesForWorkload(context.Background(), pod); !reflect.DeepEqual(got, want) {
		t.Fatalf("ordinal Pod mapping=%v, want %v", got, want)
	}
	wrongNS := pod.DeepCopy()
	wrongNS.Namespace = "other"
	if got := r.workspacesForWorkload(context.Background(), wrongNS); len(got) != 0 {
		t.Fatalf("wrong namespace mapped unrelated workspaces: %v", got)
	}
}

func TestReadWorkspaceKey_ExistingOwnedLiteral(t *testing.T) {
	ctx := context.Background()
	a := workspaceKeyTestAgent("ns", "agent", testWorkspaceAgentUID)
	const supplied = "operator-supplied-not-format-checked"
	secret := workspaceKeyTestSecret(t, a, supplied)
	scheme := workspaceKeyTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	got, err := readWorkspaceKey(ctx, c, a)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != supplied {
		t.Fatalf("readWorkspaceKey()=%q, want literal bytes %q", got, supplied)
	}
	got[0] = 'X'
	if string(secret.Data[workspaceKeyDataKey]) != supplied {
		t.Fatal("readWorkspaceKey returned aliased Secret bytes")
	}
	for _, tc := range []struct {
		name    string
		owner   *achv1alpha1.ACHAgent
		value   string
		include bool
	}{
		{name: "empty", owner: a, value: "", include: true},
		{name: "unowned", owner: nil, value: "private", include: true},
		{name: "prior UID", owner: workspaceKeyTestAgent("ns", "agent", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"), value: "private", include: true},
		{name: "missing", owner: a, include: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object
			if tc.include {
				var secret *corev1.Secret
				if tc.owner == nil {
					secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: a.Namespace, Name: workspaceKeySecretName(string(a.UID))}, Data: map[string][]byte{workspaceKeyDataKey: []byte(tc.value)}}
				} else {
					secret = workspaceKeyTestSecret(t, tc.owner, tc.value)
				}
				secret.Name = workspaceKeySecretName(string(a.UID))
				objs = append(objs, secret)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			_, err := readWorkspaceKey(ctx, reader, a)
			if err == nil {
				t.Fatal("invalid key Secret accepted")
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatalf("private bytes leaked in error: %v", err)
			}
		})
	}
}

func TestWorkspaceGenerationRace_ProfileChangeDoesNotUseStaleResolution(t *testing.T) {
	ctx := context.Background()
	w, a, oldProfile := workspaceTestObjects()
	w.UID, w.Generation = "workspace-uid", 1
	oldProfile.Name, oldProfile.Namespace = "profile-before", w.Namespace
	newProfile := oldProfile.DeepCopy()
	newProfile.Name = "profile-after"
	a.Spec.ProfileRef.Name = oldProfile.Name
	setWorkspaceControllerMetadata(w, a)
	scheme := workspaceKeyTestScheme(t)
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&achv1alpha1.Workspace{}).WithObjects(w, a, oldProfile, newProfile, workspaceKeyTestSecret(t, a, "private-literal")).Build()
	changedAgent := a.DeepCopy()
	changedAgent.ResourceVersion = "2"
	changedAgent.Spec.ProfileRef.Name = newProfile.Name
	reader := &profileChangingWorkspaceReader{Reader: base, changedAgent: changedAgent}
	writer := &recordingWorkspaceClient{Client: base}
	r := &WorkspaceReconciler{Client: writer, APIReader: reader, Scheme: scheme}
	result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(w)})
	if err != nil {
		t.Fatalf("profile-ref race result=%+v err=%v, want safe requeue", result, err)
	}
	if writer.childCreates+writer.childUpdates != 0 {
		t.Fatalf("stale profile resolution wrote children: creates=%d updates=%d", writer.childCreates, writer.childUpdates)
	}
}

type profileChangingWorkspaceReader struct {
	client.Reader
	agentGets    int
	changedAgent *achv1alpha1.ACHAgent
}

func (r *profileChangingWorkspaceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*achv1alpha1.ACHAgent); ok {
		r.agentGets++
		if r.agentGets == 2 {
			r.changedAgent.DeepCopyInto(obj.(*achv1alpha1.ACHAgent))
			return nil
		}
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}
