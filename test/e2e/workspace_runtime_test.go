//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sigsyaml "sigs.k8s.io/yaml"
)

const (
	workspaceRuntimeFixturePath = "cluster/06-agent/workspace-fixture.yaml"
	workspaceHarnessFinalizer   = "e2e.ach.ackstorm.ai/hold"
	workspaceInitialImage       = "ghcr.io/ackstorm/ach-runtime-execution:0.1.6"
	workspaceRefreshImage       = "ghcr.io/ackstorm/ach-runtime-execution:0.1.7"
	workspaceReadyDeadline      = 120 * time.Second
)

var workspaceRuntimeContext context.Context

type workspaceRuntimeOwner struct {
	profile    string
	profileUID string
	agent      string
	uid        string
	ref        string
	name       string
}

type workspaceRuntimeFixture struct {
	main              workspaceRuntimeOwner
	drift             workspaceRuntimeOwner
	harnessSA         string
	ownedPods         map[string]string
	createdCRs        map[string]bool
	workspaceTemplate map[string]any
}

func TestWorkspaceRuntimeKubernetes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), workspaceReadyDeadline)
	workspaceRuntimeContext = ctx

	fixture := &workspaceRuntimeFixture{
		main: workspaceRuntimeOwner{
			profile: "workspace-kube-e2e-profile",
			agent:   "workspace-kube-e2e-agent",
			ref:     workspaceDigest("workspace-kube-e2e-main"),
		},
		drift: workspaceRuntimeOwner{
			profile: "workspace-kube-e2e-drift-profile",
			agent:   "workspace-kube-e2e-drift-agent",
			ref:     workspaceDigest("workspace-kube-e2e-drift"),
		},
		ownedPods:  map[string]string{},
		createdCRs: map[string]bool{},
	}
	fixture.main.name = workspaceResourceNameFromIdentity(fixture.main.agent, fixture.main.ref)
	fixture.drift.name = workspaceResourceNameFromIdentity(fixture.drift.agent, fixture.drift.ref)

	if err := fixture.applyOwnerTemplate(t, &fixture.main); err != nil {
		t.Fatal(err)
	}
	fixture.main.uid = fixture.awaitAgentScaffold(t, fixture.main.agent)
	fixture.main.name = workspaceResourceNameFromIdentity(fixture.main.agent, fixture.main.ref)
	fixture.harnessSA = fixture.effectiveHarnessServiceAccount(t, fixture.main)

	// Every exit releases only the test-owned Pod finalizer. On failure, retain
	// fixture CRs and children for Root's forensic pass. On success, close and
	// remove only the two disposable owner trees created by this test.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		workspaceRuntimeContext = cleanupCtx
		defer cleanupCancel()
		defer func() { workspaceRuntimeContext = nil; cancel() }()
		fixture.releaseHeldPods(t)
		if t.Failed() {
			return
		}
		fixture.cleanupOwner(t, fixture.drift)
		fixture.cleanupOwner(t, fixture.main)
	})

	t.Run("HarnessRBAC", func(t *testing.T) {
		fixture.assertHarnessRBAC(t)
	})
	t.Run("CreatesOperatorChildren", func(t *testing.T) {
		fixture.assertInitialChildren(t, fixture.main, workspaceInitialImage, "10m", "32Mi", "100m", "128Mi")
		fixture.activate(t, fixture.main)
	})
	t.Run("AdoptsActiveLegacyShape", func(t *testing.T) {
		fixture.adoptActive(t, fixture.main)
	})
	t.Run("AdoptsDormantLegacyShape", func(t *testing.T) {
		fixture.adoptDormant(t, fixture.main)
	})
	t.Run("ProfileDriftWaitsForNoPod", func(t *testing.T) {
		fixture.profileDriftWaitsForIdle(t)
	})
	t.Run("RequestPreconditions", func(t *testing.T) {
		fixture.requestPreconditions(t, fixture.main)
	})
	t.Run("FullIdentityConflict", func(t *testing.T) {
		fixture.fullIdentityConflict(t, fixture.main)
	})
}

func (f *workspaceRuntimeFixture) applyOwnerTemplate(t *testing.T, owner *workspaceRuntimeOwner) error {
	t.Helper()
	path := filepath.Join("cluster", "06-agent", "workspace-fixture.yaml")
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read disposable Workspace fixture %s: %w", path, err)
	}
	docs := strings.Split(string(contents), "\n---\n")
	if len(docs) != 3 {
		return fmt.Errorf("fixture has %d YAML documents; want AgentProfile, ACHAgent, Workspace template", len(docs))
	}
	workspaceJSON, err := sigsyaml.YAMLToJSON([]byte(docs[2]))
	if err != nil {
		return fmt.Errorf("decode Workspace placeholder template: %w", err)
	}
	if err := json.Unmarshal(workspaceJSON, &f.workspaceTemplate); err != nil {
		return fmt.Errorf("unmarshal Workspace placeholder template: %w", err)
	}
	for _, fixture := range []struct{ kind, name string }{{"agentprofile", owner.profile}, {"achagent", owner.agent}} {
		out, err := workspaceKubectl(workspaceRuntimeContext, t, "", "get", fixture.kind, fixture.name, "-n", namespace, "--ignore-not-found=true", "-o", "name")
		if err != nil {
			return fmt.Errorf("check disposable fixture %s/%s: %w", fixture.kind, fixture.name, err)
		}
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("refusing to overwrite existing test-owned %s %s", fixture.kind, fixture.name)
		}
	}
	if _, err := workspaceKubectlInput(workspaceRuntimeContext, t, "", []string{"create", "-f", "-", "-n", namespace}, strings.Join(docs[:2], "\n---\n")); err != nil {
		return fmt.Errorf("create disposable owner template: %w", err)
	}
	profile, err := workspaceGetObject(t, "agentprofile", owner.profile)
	if err != nil {
		return fmt.Errorf("read newly created disposable profile %s: %w", owner.profile, err)
	}
	owner.profileUID = nestedString(profile, "metadata", "uid")
	return nil
}

func (f *workspaceRuntimeFixture) awaitAgentScaffold(t *testing.T, name string) string {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			t.Fatalf("ACHAgent %s did not reach WorkloadApplied before the shared 120s deadline: %v", name, err)
		}
		obj, err := workspaceGetObject(t, "achagent", name)
		if err == nil {
			uid := nestedString(obj, "metadata", "uid")
			condition := objectCondition(obj, "WorkloadApplied")
			generation := nestedInt64(obj, "metadata", "generation")
			if condition["status"] == "True" && int64Value(condition["observedGeneration"]) == generation && uid != "" {
				return uid
			}
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) effectiveHarnessServiceAccount(t *testing.T, owner workspaceRuntimeOwner) string {
	t.Helper()
	ref := controlStatefulSetRef(owner.agent)
	name := strings.TrimPrefix(ref, "statefulset/")
	obj := workspaceMustGetObject(t, "statefulset", name)
	sa := nestedString(obj, "spec", "template", "spec", "serviceAccountName")
	if sa == "" {
		t.Fatalf("control StatefulSet %s has no effective serviceAccountName", name)
	}
	return sa
}

func (f *workspaceRuntimeFixture) assertHarnessRBAC(t *testing.T) {
	t.Helper()
	apiResources, err := workspaceKubectl(workspaceRuntimeContext, t, "", "api-resources", "--api-group=ach.ackstorm.ai", "-o", "name")
	if err != nil || !containsLine(apiResources, "workspaces.ach.ackstorm.ai") {
		t.Fatalf("Workspace API discovery missing workspaces.ach.ackstorm.ai: err=%v output=%q", err, apiResources)
	}
	principal := "system:serviceaccount:" + namespace + ":" + f.harnessSA
	for _, check := range []struct{ verb, resource string }{
		{"get", "workspaces"}, {"list", "workspaces"}, {"watch", "workspaces"},
		{"create", "workspaces"}, {"update", "workspaces"}, {"patch", "workspaces"}, {"delete", "workspaces"},
		{"get", "statefulsets"}, {"list", "statefulsets"}, {"watch", "statefulsets"},
		{"get", "services"}, {"list", "services"}, {"watch", "services"},
		{"get", "pods"}, {"list", "pods"}, {"watch", "pods"}, {"delete", "pods"},
	} {
		f.assertCanI(t, principal, check.verb, check.resource, true)
	}
	for _, check := range []struct{ verb, resource string }{
		{"create", "statefulsets"}, {"update", "statefulsets"}, {"patch", "statefulsets"}, {"delete", "statefulsets"},
		{"update", "statefulsets/scale"}, {"patch", "statefulsets/scale"},
		{"create", "services"}, {"update", "services"}, {"patch", "services"}, {"delete", "services"},
		{"update", "workspaces/status"}, {"patch", "workspaces/status"},
		{"get", "secrets"}, {"create", "roles"}, {"create", "rolebindings"},
		{"create", "pods"}, {"create", "jobs"}, {"create", "pods/exec"},
	} {
		f.assertCanI(t, principal, check.verb, check.resource, false)
	}
	created := f.workspaceObject(f.main, 0, "", "")
	result, err := workspaceTestCreateAsHarness(t, f.main.uid, created)
	if err != nil {
		t.Fatalf("Harness could not create its authorized Workspace request: %v", err)
	}
	f.createdCRs[f.main.name] = true
	f.awaitApplied(t, f.main.name, nestedInt64(result, "metadata", "generation"), "True", "")
	sts := workspaceMustGetObject(t, "statefulset", f.main.name)
	stsUID := nestedString(sts, "metadata", "uid")
	denied, patchErr := workspaceKubectl(workspaceRuntimeContext, t, f.principal(), "patch", "statefulset/"+f.main.name,
		"-n", namespace, "--type=merge", "-p", "{}", "-o", "name")
	if patchErr == nil || !strings.Contains(strings.ToLower(denied), "forbidden") {
		t.Fatalf("Harness StatefulSet patch must be denied with Forbidden (uid=%s): err=%v output=%q", stsUID, patchErr, denied)
	}
	executionPrincipal := "system:serviceaccount:" + namespace + ":" + nestedString(sts, "spec", "template", "spec", "serviceAccountName")
	for _, check := range []struct{ verb, resource string }{
		{"get", "workspaces"}, {"list", "workspaces"}, {"watch", "workspaces"},
		{"create", "workspaces"}, {"update", "workspaces"}, {"patch", "workspaces"}, {"delete", "workspaces"},
		{"get", "statefulsets"}, {"list", "statefulsets"}, {"watch", "statefulsets"},
		{"get", "services"}, {"list", "services"}, {"watch", "services"},
		{"get", "pods"}, {"list", "pods"}, {"watch", "pods"}, {"delete", "pods"},
	} {
		f.assertCanI(t, executionPrincipal, check.verb, check.resource, false)
	}
	expectedExecutionSA := "ach-execution-" + f.main.uid
	if got := nestedString(sts, "spec", "template", "spec", "serviceAccountName"); got != expectedExecutionSA {
		t.Fatalf("execution ServiceAccount=%q, want scaffold identity %q", got, expectedExecutionSA)
	}
	if got, ok := nestedMap(sts, "spec", "template", "spec")["automountServiceAccountToken"].(bool); !ok || got {
		t.Fatalf("execution ServiceAccount token automount must be explicitly disabled")
	}
	f.assertExecutionServiceAccountUnbound(t, expectedExecutionSA)
}

func (f *workspaceRuntimeFixture) assertCanI(t *testing.T, principal, verb, resource string, want bool) {
	t.Helper()
	args := workspaceCanIArgs(verb, resource, namespace, principal)
	out, stderr, err := workspaceKubectlStdout(workspaceRuntimeContext, args...)
	if !workspaceCanIResult(out, stderr, err, want) {
		t.Fatalf("effective permission %s %s as %s = stdout %q, stderr %q, error %v, want %t", verb, resource, principal, strings.TrimSpace(out), stderr, err, want)
	}
}

func (f *workspaceRuntimeFixture) assertExecutionServiceAccountUnbound(t *testing.T, serviceAccount string) {
	t.Helper()
	roleBindings, err := workspaceKubectl(workspaceRuntimeContext, t, "", "get", "rolebindings", "-n", namespace, "-o", "json")
	if err != nil {
		t.Fatalf("list namespace RoleBindings for execution identity: %v", err)
	}
	clusterRoleBindings, err := workspaceKubectl(workspaceRuntimeContext, t, "", "get", "clusterrolebindings", "-o", "json")
	if err != nil {
		t.Fatalf("list ClusterRoleBindings for execution identity: %v", err)
	}
	for kind, output := range map[string]string{"RoleBinding": roleBindings, "ClusterRoleBinding": clusterRoleBindings} {
		var list struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(output), &list); err != nil {
			t.Fatalf("decode %s list: %v", kind, err)
		}
		for _, binding := range list.Items {
			for _, subject := range nestedSliceMap(binding, "subjects") {
				if subject["kind"] == "ServiceAccount" && subject["name"] == serviceAccount && subject["namespace"] == namespace {
					t.Fatalf("execution ServiceAccount %s/%s is directly bound by %s %s", namespace, serviceAccount, kind, nestedString(binding, "metadata", "name"))
				}
			}
		}
	}
}

func workspaceCanIArgs(verb, resource, namespace, principal string) []string {
	args := []string{"auth", "can-i", verb}
	if resourceType, subresource, found := strings.Cut(resource, "/"); found {
		args = append(args, resourceType, "--subresource="+subresource)
	} else {
		args = append(args, resource)
	}
	return append(args, "-n", namespace, "--as="+principal)
}

func workspaceCanIResult(stdout, stderr string, err error, want bool) bool {
	exitCode, hasError := 0, err != nil
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return workspaceCanIClassification(stdout, exitCode, hasError, stderr != "", want)
}

func workspaceCanIClassification(stdout string, exitCode int, hasError, hasStderr, want bool) bool {
	result := strings.TrimSpace(stdout)
	if want {
		return result == "yes" && !hasError && !hasStderr
	}
	return result == "no" && hasError && exitCode == 1 && !hasStderr
}

func workspaceKubectlStdout(ctx context.Context, args ...string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	output, err := cmd.Output()
	return string(output), stderr.String(), err
}

func (f *workspaceRuntimeFixture) principal() string {
	return "system:serviceaccount:" + namespace + ":" + f.harnessSA
}

func (f *workspaceRuntimeFixture) createWorkspaceAsHarness(t *testing.T, owner workspaceRuntimeOwner, replicas int32, stsUID, podUID string) {
	t.Helper()
	if owner.uid == "" {
		owner.uid = workspaceMustGetObject(t, "achagent", owner.agent)["metadata"].(map[string]any)["uid"].(string)
	}
	if owner.name == "" || strings.Contains(owner.name, "{{") {
		owner.name = workspaceResourceNameFromIdentity(owner.agent, owner.ref)
	}
	obj := f.workspaceObject(owner, replicas, stsUID, podUID)
	sa := fEffectiveHarnessSA(t, owner.uid)
	result, err := workspaceTestCreateAsHarness(t, owner.uid, obj)
	if err != nil {
		t.Fatalf("create Workspace %s as effective Harness SA %s: %v", owner.name, sa, err)
	}
	f.createdCRs[owner.name] = true
	f.awaitApplied(t, owner.name, nestedInt64(result, "metadata", "generation"), "True", "")
}

func (f *workspaceRuntimeFixture) workspaceObject(owner workspaceRuntimeOwner, replicas int32, stsUID, podUID string) map[string]any {
	encoded, _ := json.Marshal(f.workspaceTemplate)
	var object map[string]any
	_ = json.Unmarshal(encoded, &object)
	metadata := nestedMap(object, "metadata")
	labels := nestedMap(metadata, "labels")
	annotations := nestedMap(metadata, "annotations")
	ownerRefs := nestedSliceMap(metadata, "ownerReferences")
	metadata["name"] = owner.name
	metadata["namespace"] = namespace
	labels["runtime.ach.ackstorm.ai/agent-uid"] = owner.uid
	labels["runtime.ach.ackstorm.ai/workspace-name"] = owner.name
	annotations["runtime.ach.ackstorm.ai/workspace-ref"] = owner.ref
	if len(ownerRefs) > 0 {
		ownerRefs[0]["name"] = owner.agent
		ownerRefs[0]["uid"] = owner.uid
	}
	spec := nestedMap(object, "spec")
	agentRef := nestedMap(spec, "agentRef")
	agentRef["name"] = owner.agent
	agentRef["uid"] = owner.uid
	spec["workspaceRef"] = owner.ref
	spec["replicas"] = replicas
	if stsUID != "" {
		spec["expectedStatefulSetUID"] = stsUID
	} else {
		delete(spec, "expectedStatefulSetUID")
	}
	if podUID != "" {
		spec["expectedPodUID"] = podUID
	} else {
		delete(spec, "expectedPodUID")
	}
	return object
}

func workspaceTestCreateAsHarness(t *testing.T, agentUID string, object map[string]any) (map[string]any, error) {
	t.Helper()
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	args := []string{"create", "-f", "-", "-n", namespace, "-o", "json"}
	serviceAccount := fEffectiveHarnessSA(t, agentUID)
	out, err := workspaceKubectlInput(workspaceRuntimeContext, t, "system:serviceaccount:"+namespace+":"+serviceAccount, args, string(encoded))
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, fmt.Errorf("decode kubectl create output: %w; output=%q", err, out)
	}
	return result, nil
}

func (f *workspaceRuntimeFixture) awaitApplied(t *testing.T, name string, generation int64, wantStatus, wantReason string) map[string]any {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			last, _ := workspaceGetObject(t, "workspace", name)
			children := f.describeIdentity(t, name, "")
			t.Fatalf("Workspace %s generation %d did not reach WorkloadApplied=%s reason=%q before the shared deadline (%v); last CR=%v; children=%s", name, generation, wantStatus, wantReason, err, last, children)
		}
		obj, err := workspaceGetObject(t, "workspace", name)
		if err == nil {
			condition := objectCondition(obj, "WorkloadApplied")
			if nestedInt64(obj, "status", "observedGeneration") == generation && int64Value(condition["observedGeneration"]) == generation && condition["status"] == wantStatus && (wantReason == "" || condition["reason"] == wantReason) {
				return obj
			}
		}
		time.Sleep(time.Second)
	}
}

func workspaceTestRequest(t *testing.T, agentUID, name string, replicas int32, stsUID, podUID string) int64 {
	t.Helper()
	current := workspaceMustGetObject(t, "workspace", name)
	metadata := current["metadata"].(map[string]any)
	resourceVersion := metadata["resourceVersion"].(string)
	patch := map[string]any{
		"metadata": map[string]any{"resourceVersion": resourceVersion},
		"spec":     map[string]any{"replicas": replicas, "expectedStatefulSetUID": stsUID, "expectedPodUID": podUID},
	}
	encoded, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal Workspace request patch: %v", err)
	}
	args := []string{"patch", "workspace/" + name, "-n", namespace, "--type=merge", "--patch-file=/dev/stdin", "-o", "json"}
	out, err := workspaceKubectlInput(workspaceRuntimeContext, t, "system:serviceaccount:"+namespace+":"+fEffectiveHarnessSA(t, agentUID), args, string(encoded))
	if err != nil {
		t.Fatalf("patch Workspace %s at resourceVersion %s: %v", name, resourceVersion, err)
	}
	var updated map[string]any
	if err := json.Unmarshal([]byte(out), &updated); err != nil {
		t.Fatalf("decode Workspace patch result: %v output=%q", err, out)
	}
	return nestedInt64(updated, "metadata", "generation")
}

func workspaceTestAwaitApplied(t *testing.T, name string, generation int64, wantStatus, wantReason string) {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			last, _ := workspaceGetObject(t, "workspace", name)
			t.Fatalf("Workspace %s generation %d status=%s reason=%q timed out: %v; last=%v", name, generation, wantStatus, wantReason, err, last)
		}
		obj, err := workspaceGetObject(t, "workspace", name)
		if err == nil {
			condition := objectCondition(obj, "WorkloadApplied")
			if nestedInt64(obj, "status", "observedGeneration") == generation && int64Value(condition["observedGeneration"]) == generation && condition["status"] == wantStatus && (wantReason == "" || condition["reason"] == wantReason) {
				return
			}
		}
		time.Sleep(time.Second)
	}
}

func workspaceTestAwaitNoPod(t *testing.T, name, agentUID, workspaceRef string) {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			pods, _ := workspaceMatchingPods(t, name, agentUID)
			last, _ := workspaceGetObject(t, "workspace", name)
			t.Fatalf("matching Workspace Pod remains at shared deadline: workspace=%s agentUID=%s ref=%s pods=%v lastCR=%v: %v", name, agentUID, workspaceRef, pods, last, err)
		}
		pods, err := workspaceMatchingPods(t, name, agentUID)
		if err == nil && len(pods) == 0 {
			return
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) assertInitialChildren(t *testing.T, owner workspaceRuntimeOwner, image, reqCPU, reqMem, limitCPU, limitMem string) {
	t.Helper()
	sts := workspaceMustGetObject(t, "statefulset", owner.name)
	service := workspaceMustGetObject(t, "service", owner.name)
	if replicas := nestedInt64(sts, "spec", "replicas"); replicas != 0 {
		t.Fatalf("new Workspace created live template without an event: replicas=%d", replicas)
	}
	if pods, err := workspaceMatchingPods(t, owner.name, owner.uid); err != nil || len(pods) != 0 {
		t.Fatalf("new sleeping Workspace has Pods: err=%v pods=%v", err, pods)
	}
	f.assertChildIdentity(t, owner, sts, service)
	if nestedString(sts, "spec", "updateStrategy", "type") != "OnDelete" {
		t.Fatalf("StatefulSet updateStrategy=%q, want OnDelete", nestedString(sts, "spec", "updateStrategy", "type"))
	}
	workspace := workspaceMustGetObject(t, "workspace", owner.name)
	labels := map[string]string{
		"runtime.ach.ackstorm.ai/agent-uid":      owner.uid,
		"runtime.ach.ackstorm.ai/workspace-name": owner.name,
	}
	if !stringMapContains(nestedStringMap(workspace, "metadata", "labels"), labels) || nestedString(workspace, "metadata", "annotations", "runtime.ach.ackstorm.ai/workspace-ref") != owner.ref || !hasOwnerReference(workspace, owner.agent, owner.uid, "ACHAgent") {
		t.Fatalf("Workspace request metadata does not preserve full owner/workspace identity: %v", workspace["metadata"])
	}
	if got, want := nestedString(workspace, "status", "statefulSetUID"), nestedString(sts, "metadata", "uid"); got != want {
		t.Fatalf("Workspace status StatefulSet UID=%q, live StatefulSet UID=%q", got, want)
	}
	if nestedString(sts, "spec", "serviceName") != owner.name || nestedString(service, "spec", "clusterIP") != "None" {
		t.Fatalf("StatefulSet/Service headless identity mismatch: sts=%v service=%v", sts["spec"], service["spec"])
	}
	containers := nestedSliceMap(sts, "spec", "template", "spec", "containers")
	if len(containers) != 1 || containers[0]["name"] != "execution" || containers[0]["image"] != image {
		t.Fatalf("execution container shape/image mismatch: %+v", containers)
	}
	c := containers[0]
	podSpec := nestedMap(sts, "spec", "template", "spec")
	if nestedString(sts, "spec", "template", "spec", "serviceAccountName") == "" || nestedBool(sts, "spec", "template", "spec", "automountServiceAccountToken") || podSpec["automountServiceAccountToken"] != false {
		t.Fatalf("execution identity must use a named SA with automount disabled: podSpec=%v", nestedMap(sts, "spec", "template", "spec"))
	}
	if nestedInt64(sts, "spec", "template", "spec", "securityContext", "runAsUser") != 10001 || nestedInt64(sts, "spec", "template", "spec", "securityContext", "runAsGroup") != 10001 || nestedInt64(sts, "spec", "template", "spec", "securityContext", "fsGroup") != 10001 {
		t.Fatalf("execution pod identity is not uid/gid/fsGroup 10001: %v", nestedMap(sts, "spec", "template", "spec", "securityContext"))
	}
	if !nestedBool(sts, "spec", "template", "spec", "securityContext", "runAsNonRoot") || nestedString(sts, "spec", "template", "spec", "securityContext", "seccompProfile", "type") != "RuntimeDefault" || nestedBool(sts, "spec", "template", "spec", "enableServiceLinks") {
		t.Fatalf("execution pod hardening differs from runtime 0.1.8: %v", nestedMap(sts, "spec", "template", "spec"))
	}
	containerSecurity := nestedMap(c, "securityContext")
	capabilities := nestedMap(containerSecurity, "capabilities")
	dropped := nestedStringSlice(capabilities["drop"])
	if containerSecurity["allowPrivilegeEscalation"] != false || len(dropped) != 1 || dropped[0] != "ALL" || len(nestedStringSlice(capabilities["add"])) != 0 {
		t.Fatalf("execution container security context differs from runtime 0.1.8: %v", containerSecurity)
	}
	if !hasVolumeConfigMap(sts, "bootstrap", "ach-execution-"+owner.uid) {
		t.Fatalf("Workspace bootstrap ConfigMap reference missing: %v", nestedSliceMap(sts, "spec", "template", "spec", "volumes"))
	}
	if nestedString(c, "readinessProbe", "httpGet", "path") != "/readyz" || nestedString(c, "livenessProbe", "httpGet", "path") != "/healthz" {
		t.Fatalf("execution health probes differ from contract: %+v", c)
	}
	resources := nestedMap(c, "resources")
	assertQuantity(t, resources, "requests", "cpu", reqCPU)
	assertQuantity(t, resources, "requests", "memory", reqMem)
	assertQuantity(t, resources, "limits", "cpu", limitCPU)
	assertQuantity(t, resources, "limits", "memory", limitMem)
	if nestedInt64(sts, "spec", "template", "spec", "terminationGracePeriodSeconds") != 330 {
		t.Fatalf("termination grace=%d, want fixture profile value 330", nestedInt64(sts, "spec", "template", "spec", "terminationGracePeriodSeconds"))
	}
	if got := nestedSliceMap(sts, "spec", "template", "spec", "imagePullSecrets"); len(got) != 0 {
		t.Fatalf("unexpected fixture imagePullSecrets: %v", got)
	}
	if got := nestedMap(sts, "spec", "template", "spec", "nodeSelector"); len(got) != 0 {
		t.Fatalf("unexpected fixture nodeSelector: %v", got)
	}
	if got := nestedSliceMap(sts, "spec", "template", "spec", "tolerations"); len(got) != 0 {
		t.Fatalf("unexpected fixture tolerations: %v", got)
	}
	volumes := nestedSliceMap(sts, "spec", "template", "spec", "volumes")
	if len(volumes) != 3 || volumeSize(volumes, "workspace") != "1Gi" || volumeSize(volumes, "sessions") != "1Gi" {
		t.Fatalf("execution storage differs from fixture ephemeral-storage contract: %v", volumes)
	}
	mounts := nestedSliceMap(c, "volumeMounts")
	if len(mounts) != 3 || !hasMount(mounts, "bootstrap", "/etc/ach-runtime", true) || !hasMount(mounts, "workspace", "/workspace", false) || !hasMount(mounts, "sessions", "/var/lib/ach-runtime/sessions", false) {
		t.Fatalf("execution container mounts differ from fixture storage contract: %v", mounts)
	}
}

func (f *workspaceRuntimeFixture) assertChildIdentity(t *testing.T, owner workspaceRuntimeOwner, sts, service map[string]any) {
	t.Helper()
	labels := map[string]string{
		"runtime.ach.ackstorm.ai/agent-uid":      owner.uid,
		"runtime.ach.ackstorm.ai/workspace-name": owner.name,
	}
	for _, obj := range []map[string]any{sts, service} {
		if nestedString(obj, "metadata", "namespace") != namespace || !reflect.DeepEqual(nestedStringMap(obj, "metadata", "labels"), labels) || nestedString(obj, "metadata", "annotations", "runtime.ach.ackstorm.ai/workspace-ref") != owner.ref || !hasOwnerReference(obj, owner.agent, owner.uid, "ACHAgent") {
			t.Fatalf("child metadata does not preserve full owner/workspace identity: %v", obj["metadata"])
		}
	}
	selector := nestedStringMap(sts, "spec", "selector", "matchLabels")
	podLabels := map[string]string{"runtime.ach.ackstorm.ai/agent-uid": owner.uid, "runtime.ach.ackstorm.ai/workspace-name": owner.name, "ach.ackstorm.ai/agent": owner.agent}
	if !reflect.DeepEqual(selector, labels) || !reflect.DeepEqual(nestedStringMap(service, "spec", "selector"), labels) || !reflect.DeepEqual(nestedStringMap(sts, "spec", "template", "metadata", "labels"), podLabels) {
		t.Fatalf("child selectors do not match the full identity: sts=%v service=%v template=%v", selector, nestedStringMap(service, "spec", "selector"), nestedStringMap(sts, "spec", "template", "metadata", "labels"))
	}
}

func assertProtectedIdentityMetadata(t *testing.T, before, after map[string]any) {
	t.Helper()
	for _, field := range []string{"labels", "annotations", "ownerReferences"} {
		if !reflect.DeepEqual(nestedMap(before, "metadata")[field], nestedMap(after, "metadata")[field]) {
			t.Fatalf("protected metadata.%s changed: before=%v after=%v", field, nestedMap(before, "metadata")[field], nestedMap(after, "metadata")[field])
		}
	}
	if nestedString(before, "metadata", "namespace") != nestedString(after, "metadata", "namespace") || nestedString(before, "metadata", "name") != nestedString(after, "metadata", "name") {
		t.Fatalf("protected metadata name/namespace changed: before=%v after=%v", before["metadata"], after["metadata"])
	}
}

func (f *workspaceRuntimeFixture) activate(t *testing.T, owner workspaceRuntimeOwner) string {
	t.Helper()
	sts := workspaceMustGetObject(t, "statefulset", owner.name)
	gen := workspaceTestRequest(t, owner.uid, owner.name, 1, nestedString(sts, "metadata", "uid"), "")
	f.awaitApplied(t, owner.name, gen, "True", "")
	if replicas := nestedInt64(workspaceMustGetObject(t, "statefulset", owner.name), "spec", "replicas"); replicas != 1 {
		t.Fatalf("activation request left StatefulSet replicas=%d, want 1", replicas)
	}
	pod := f.awaitOnePod(t, owner)
	if nestedString(pod, "metadata", "uid") == "" {
		t.Fatalf("active Workspace Pod has no UID: %v", pod)
	}
	f.assertPodOwner(t, owner, sts, pod)
	return nestedString(pod, "metadata", "uid")
}

func (f *workspaceRuntimeFixture) adoptActive(t *testing.T, owner workspaceRuntimeOwner) {
	t.Helper()
	stsBefore := workspaceMustGetObject(t, "statefulset", owner.name)
	serviceBefore := workspaceMustGetObject(t, "service", owner.name)
	podBefore := f.awaitOnePod(t, owner)
	f.awaitExecutionRunning(t, owner, podBefore)
	podName := nestedString(podBefore, "metadata", "name")
	markerPath := "/workspace/workspace-cr-e2e-live-marker"
	stdout, stderr, err := kubectlExecWorkload(workspaceRuntimeContext, namespace, "pod/"+podName, "execution", "python", "-c",
		"import os; from pathlib import Path; Path("+fmt.Sprintf("%q", markerPath)+").write_bytes(b'workspace-cr-e2e-live-marker'); print(f'{os.getuid()}:{os.getgid()}')")
	if err != nil {
		t.Fatalf("write active marker as execution uid: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	f.assertObservedPodUnchanged(t, podBefore)
	if strings.TrimSpace(stdout) != "10001:10001" {
		t.Fatalf("active marker was not written as uid/gid 10001: %q", stdout)
	}
	if out, err := workspaceKubectl(workspaceRuntimeContext, t, f.principal(), "delete", "workspace/"+owner.name, "-n", namespace, "--wait=true"); err != nil {
		t.Fatalf("delete only active Workspace CR: %v %s", err, out)
	}
	f.waitObjectAbsent(t, "workspace", owner.name)
	if got := nestedString(workspaceMustGetObject(t, "statefulset", owner.name), "metadata", "uid"); got != nestedString(stsBefore, "metadata", "uid") {
		t.Fatalf("CR-only delete changed StatefulSet UID: before=%s after=%s", nestedString(stsBefore, "metadata", "uid"), got)
	}
	if got := nestedString(workspaceMustGetObject(t, "service", owner.name), "metadata", "uid"); got != nestedString(serviceBefore, "metadata", "uid") {
		t.Fatalf("CR-only delete changed Service UID: before=%s after=%s", nestedString(serviceBefore, "metadata", "uid"), got)
	}
	newWorkspace := f.workspaceObject(owner, 1, nestedString(stsBefore, "metadata", "uid"), nestedString(podBefore, "metadata", "uid"))
	created, err := workspaceTestCreateAsHarness(t, owner.uid, newWorkspace)
	if err != nil {
		t.Fatalf("recreate active Workspace for identity-matching adoption: %v", err)
	}
	f.createdCRs[owner.name] = true
	f.awaitApplied(t, owner.name, nestedInt64(created, "metadata", "generation"), "True", "")
	stsAfter := workspaceMustGetObject(t, "statefulset", owner.name)
	serviceAfter := workspaceMustGetObject(t, "service", owner.name)
	podAfter := f.awaitOnePod(t, owner)
	if nestedString(stsAfter, "metadata", "uid") != nestedString(stsBefore, "metadata", "uid") || nestedString(serviceAfter, "metadata", "uid") != nestedString(serviceBefore, "metadata", "uid") || nestedString(podAfter, "metadata", "uid") != nestedString(podBefore, "metadata", "uid") {
		t.Fatalf("active legacy adoption replaced child UID(s): sts=%s/%s service=%s/%s pod=%s/%s", nestedString(stsBefore, "metadata", "uid"), nestedString(stsAfter, "metadata", "uid"), nestedString(serviceBefore, "metadata", "uid"), nestedString(serviceAfter, "metadata", "uid"), nestedString(podBefore, "metadata", "uid"), nestedString(podAfter, "metadata", "uid"))
	}
	if !reflect.DeepEqual(stsBefore["spec"], stsAfter["spec"]) || !reflect.DeepEqual(serviceBefore["spec"], serviceAfter["spec"]) || !reflect.DeepEqual(podBefore["spec"], podAfter["spec"]) {
		t.Fatalf("active adoption mutated live child shapes")
	}
	assertProtectedIdentityMetadata(t, stsBefore, stsAfter)
	assertProtectedIdentityMetadata(t, serviceBefore, serviceAfter)
	assertProtectedIdentityMetadata(t, podBefore, podAfter)
	f.assertChildIdentity(t, owner, stsAfter, serviceAfter)
	f.assertPodOwner(t, owner, stsAfter, podAfter)
	f.awaitExecutionRunning(t, owner, podAfter)
	stdout, stderr, err = kubectlExecWorkload(workspaceRuntimeContext, namespace, "pod/"+podName, "execution", "python", "-c",
		"from pathlib import Path; print(Path("+fmt.Sprintf("%q", markerPath)+").read_bytes().decode())")
	if err != nil || strings.TrimSpace(stdout) != "workspace-cr-e2e-live-marker" {
		t.Fatalf("active adoption did not preserve live marker bytes: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	f.assertObservedPodUnchanged(t, podAfter)
}

func (f *workspaceRuntimeFixture) adoptDormant(t *testing.T, owner workspaceRuntimeOwner) {
	t.Helper()
	sts := workspaceMustGetObject(t, "statefulset", owner.name)
	pod, err := workspaceMatchingPods(t, owner.name, owner.uid)
	if err != nil || len(pod) != 1 {
		t.Fatalf("expected one live Pod before dormant adoption setup: err=%v pods=%v", err, pod)
	}
	gen := workspaceTestRequest(t, owner.uid, owner.name, 0, nestedString(sts, "metadata", "uid"), nestedString(pod[0], "metadata", "uid"))
	f.awaitApplied(t, owner.name, gen, "True", "")
	workspaceTestAwaitNoPod(t, owner.name, owner.uid, owner.ref)
	stsBefore := workspaceMustGetObject(t, "statefulset", owner.name)
	serviceBefore := workspaceMustGetObject(t, "service", owner.name)
	if nestedInt64(stsBefore, "spec", "replicas") != 0 {
		t.Fatalf("dormant adoption baseline has replicas=%d, want 0", nestedInt64(stsBefore, "spec", "replicas"))
	}
	if out, err := workspaceKubectl(workspaceRuntimeContext, t, f.principal(), "delete", "workspace/"+owner.name, "-n", namespace, "--wait=true"); err != nil {
		t.Fatalf("delete only dormant Workspace CR: %v %s", err, out)
	}
	f.waitObjectAbsent(t, "workspace", owner.name)
	if !sameUID(stsBefore, workspaceMustGetObject(t, "statefulset", owner.name)) || !sameUID(serviceBefore, workspaceMustGetObject(t, "service", owner.name)) {
		t.Fatalf("dormant CR-only delete did not preserve children")
	}
	f.createWorkspaceAsHarness(t, owner, 0, nestedString(stsBefore, "metadata", "uid"), "")
	stsAfter := workspaceMustGetObject(t, "statefulset", owner.name)
	serviceAfter := workspaceMustGetObject(t, "service", owner.name)
	if !sameUID(stsBefore, stsAfter) || !sameUID(serviceBefore, serviceAfter) || !reflect.DeepEqual(stsBefore["spec"], stsAfter["spec"]) || !reflect.DeepEqual(serviceBefore["spec"], serviceAfter["spec"]) {
		t.Fatalf("dormant legacy adoption replaced a child")
	}
	assertProtectedIdentityMetadata(t, stsBefore, stsAfter)
	assertProtectedIdentityMetadata(t, serviceBefore, serviceAfter)
	if nestedInt64(stsAfter, "spec", "replicas") != 0 {
		t.Fatalf("dormant legacy adoption changed replicas to %d", nestedInt64(stsAfter, "spec", "replicas"))
	}
	f.assertChildIdentity(t, owner, stsAfter, serviceAfter)
	workspaceTestAwaitNoPod(t, owner.name, owner.uid, owner.ref)
}

func (f *workspaceRuntimeFixture) profileDriftWaitsForIdle(t *testing.T) {
	t.Helper()
	if err := f.cloneOwner(t); err != nil {
		t.Fatal(err)
	}
	f.drift.uid = f.awaitAgentScaffold(t, f.drift.agent)
	f.drift.name = workspaceResourceNameFromIdentity(f.drift.agent, f.drift.ref)
	f.createWorkspaceAsHarness(t, f.drift, 0, "", "")
	f.assertInitialChildren(t, f.drift, workspaceInitialImage, "10m", "32Mi", "100m", "128Mi")
	oldPodUID := f.activate(t, f.drift)
	stsBefore := workspaceMustGetObject(t, "statefulset", f.drift.name)
	serviceBefore := workspaceMustGetObject(t, "service", f.drift.name)
	templateBefore, _ := json.Marshal(nestedMap(stsBefore, "spec", "template"))
	patch := map[string]any{"spec": map[string]any{
		"execution": map[string]any{
			"image": workspaceRefreshImage,
			"resources": map[string]any{
				"requests": map[string]string{"cpu": "20m", "memory": "48Mi"},
				"limits":   map[string]string{"cpu": "200m", "memory": "160Mi"},
			},
		},
	}}
	if _, err := f.adminPatch("agentprofile", f.drift.profile, patch); err != nil {
		t.Fatalf("patch disposable profile for drift: %v", err)
	}
	f.awaitUpdatePending(t, f.drift.name)
	stsDuring := workspaceMustGetObject(t, "statefulset", f.drift.name)
	templateDuring, _ := json.Marshal(nestedMap(stsDuring, "spec", "template"))
	if !bytesEqual(templateBefore, templateDuring) || !reflect.DeepEqual(stsBefore["spec"], stsDuring["spec"]) || nestedString(stsDuring, "metadata", "uid") != nestedString(stsBefore, "metadata", "uid") {
		t.Fatalf("profile drift changed a live template or StatefulSet UID")
	}
	serviceDuringUpdate := workspaceMustGetObject(t, "service", f.drift.name)
	if !sameUID(serviceBefore, serviceDuringUpdate) || !reflect.DeepEqual(serviceBefore["spec"], serviceDuringUpdate["spec"]) {
		t.Fatalf("profile drift changed the Service")
	}
	assertProtectedIdentityMetadata(t, stsBefore, stsDuring)
	assertProtectedIdentityMetadata(t, serviceBefore, serviceDuringUpdate)
	f.assertChildIdentity(t, f.drift, stsDuring, serviceDuringUpdate)
	pod := f.awaitOnePod(t, f.drift)
	if nestedString(pod, "metadata", "uid") != oldPodUID {
		t.Fatalf("profile drift replaced live Pod UID")
	}
	f.addHoldFinalizer(t, pod)
	stsUID := nestedString(stsDuring, "metadata", "uid")
	gen := workspaceTestRequest(t, f.drift.uid, f.drift.name, 0, stsUID, oldPodUID)
	f.waitForHeldScaleDown(t, f.drift, gen)
	terminating := workspaceMustGetObject(t, "statefulset", f.drift.name)
	terminatingTemplate, _ := json.Marshal(nestedMap(terminating, "spec", "template"))
	if !bytesEqual(templateBefore, terminatingTemplate) || !reflect.DeepEqual(statefulSetSpecWithReplicas(stsBefore, 0), terminating["spec"]) || !sameUID(stsBefore, terminating) {
		t.Fatalf("terminating held Pod changed the live execution-template/StatefulSet identity")
	}
	serviceDuring := workspaceMustGetObject(t, "service", f.drift.name)
	if !sameUID(serviceBefore, serviceDuring) || !reflect.DeepEqual(serviceBefore["spec"], serviceDuring["spec"]) {
		t.Fatalf("profile drift replaced the live Service")
	}
	f.assertChildIdentity(t, f.drift, terminating, serviceDuring)
	f.releaseHeldPods(t)
	workspaceTestAwaitNoPod(t, f.drift.name, f.drift.uid, f.drift.ref)
	f.awaitTemplate(t, f.drift, workspaceRefreshImage, "20m", "48Mi", "200m", "160Mi")
	newPodUID := f.activate(t, f.drift)
	if newPodUID == oldPodUID {
		t.Fatalf("activation after idle refresh reused old Pod UID %s", oldPodUID)
	}
	f.assertTemplate(t, f.drift, workspaceRefreshImage, "20m", "48Mi", "200m", "160Mi")
	newPod := f.awaitOnePod(t, f.drift)
	if nestedString(newPod, "metadata", "uid") != newPodUID {
		t.Fatalf("active refreshed Pod UID changed after activation")
	}
	f.assertPodOwner(t, f.drift, workspaceMustGetObject(t, "statefulset", f.drift.name), newPod)
	f.assertPodExecutionSettings(t, newPod, workspaceRefreshImage, "20m", "48Mi", "200m", "160Mi")
	if !sameUID(stsBefore, workspaceMustGetObject(t, "statefulset", f.drift.name)) || !sameUID(serviceBefore, workspaceMustGetObject(t, "service", f.drift.name)) {
		t.Fatalf("profile refresh replaced a protected child UID")
	}
	refreshedSTS := workspaceMustGetObject(t, "statefulset", f.drift.name)
	refreshedService := workspaceMustGetObject(t, "service", f.drift.name)
	assertProtectedIdentityMetadata(t, stsBefore, refreshedSTS)
	assertProtectedIdentityMetadata(t, serviceBefore, refreshedService)
	f.assertChildIdentity(t, f.drift, refreshedSTS, refreshedService)
}

func (f *workspaceRuntimeFixture) requestPreconditions(t *testing.T, owner workspaceRuntimeOwner) {
	t.Helper()
	sts := workspaceMustGetObject(t, "statefulset", owner.name)
	service := workspaceMustGetObject(t, "service", owner.name)
	beforeTemplate, _ := json.Marshal(nestedMap(sts, "spec", "template"))
	wrongSTS := "00000000-0000-0000-0000-000000000000"
	gen := workspaceTestRequest(t, owner.uid, owner.name, 1, wrongSTS, "")
	workspaceTestAwaitApplied(t, owner.name, gen, "False", "WorkspaceConflict")
	currentSTS := workspaceMustGetObject(t, "statefulset", owner.name)
	currentService := workspaceMustGetObject(t, "service", owner.name)
	if nestedInt64(currentSTS, "spec", "replicas") != 0 || !sameUID(sts, currentSTS) || !sameUID(service, currentService) || !reflect.DeepEqual(sts["spec"], currentSTS["spec"]) || !reflect.DeepEqual(service["spec"], currentService["spec"]) {
		t.Fatalf("wrong StatefulSet UID precondition mutated child identity/replicas")
	}
	templateAfter, _ := json.Marshal(nestedMap(currentSTS, "spec", "template"))
	if !bytesEqual(beforeTemplate, templateAfter) {
		t.Fatalf("wrong StatefulSet UID precondition mutated template")
	}
	assertProtectedIdentityMetadata(t, sts, currentSTS)
	assertProtectedIdentityMetadata(t, service, currentService)
	f.assertChildIdentity(t, owner, currentSTS, currentService)
	workspaceTestAwaitNoPod(t, owner.name, owner.uid, owner.ref)
	if got := f.activate(t, owner); got == "" {
		t.Fatal("exact StatefulSet UID precondition did not activate")
	}
	pod := f.awaitOnePod(t, owner)
	sts = workspaceMustGetObject(t, "statefulset", owner.name)
	gen = workspaceTestRequest(t, owner.uid, owner.name, 0, nestedString(sts, "metadata", "uid"), "wrong-pod-uid")
	workspaceTestAwaitApplied(t, owner.name, gen, "False", "WorkspaceConflict")
	unchanged := workspaceMustGetObject(t, "statefulset", owner.name)
	currentPod := f.awaitOnePod(t, owner)
	unchangedService := workspaceMustGetObject(t, "service", owner.name)
	if nestedInt64(unchanged, "spec", "replicas") != 1 || !sameUID(pod, currentPod) || !sameUID(sts, unchanged) || !sameUID(service, unchangedService) || !reflect.DeepEqual(sts["spec"], unchanged["spec"]) || !reflect.DeepEqual(service["spec"], unchangedService["spec"]) || !reflect.DeepEqual(pod["spec"], currentPod["spec"]) {
		t.Fatalf("wrong Pod UID precondition mutated live workload")
	}
	unchangedTemplate, _ := json.Marshal(nestedMap(unchanged, "spec", "template"))
	if !bytesEqual(beforeTemplate, unchangedTemplate) {
		t.Fatalf("wrong Pod UID precondition mutated live template")
	}
	assertProtectedIdentityMetadata(t, sts, unchanged)
	assertProtectedIdentityMetadata(t, service, unchangedService)
	assertProtectedIdentityMetadata(t, pod, currentPod)
	f.assertChildIdentity(t, owner, unchanged, unchangedService)
	gen = workspaceTestRequest(t, owner.uid, owner.name, 0, nestedString(sts, "metadata", "uid"), nestedString(pod, "metadata", "uid"))
	f.awaitApplied(t, owner.name, gen, "True", "")
	workspaceTestAwaitNoPod(t, owner.name, owner.uid, owner.ref)
}

func (f *workspaceRuntimeFixture) fullIdentityConflict(t *testing.T, owner workspaceRuntimeOwner) {
	t.Helper()
	stsBefore := workspaceMustGetObject(t, "statefulset", owner.name)
	serviceBefore := workspaceMustGetObject(t, "service", owner.name)
	if out, err := workspaceKubectl(workspaceRuntimeContext, t, f.principal(), "delete", "workspace/"+owner.name, "-n", namespace, "--wait=true"); err != nil {
		t.Fatalf("delete only current Workspace request: %v %s", err, out)
	}
	f.waitObjectAbsent(t, "workspace", owner.name)
	conflicting := owner
	conflicting.ref = owner.ref[:20] + strings.Repeat("f", 44)
	if conflicting.ref == owner.ref {
		conflicting.ref = owner.ref[:20] + strings.Repeat("e", 44)
	}
	if workspaceResourceNameFromIdentity(owner.agent, conflicting.ref) != owner.name {
		t.Fatalf("conflict fixture does not preserve shortened child name")
	}
	obj := f.workspaceObject(conflicting, 0, nestedString(stsBefore, "metadata", "uid"), "")
	created, err := workspaceTestCreateAsHarness(t, owner.uid, obj)
	if err != nil {
		t.Fatalf("create same-name Workspace with altered full digest: %v", err)
	}
	f.createdCRs[owner.name] = true
	conflictGeneration := nestedInt64(created, "metadata", "generation")
	f.awaitApplied(t, owner.name, conflictGeneration, "False", "WorkspaceConflict")
	if !sameUID(stsBefore, workspaceMustGetObject(t, "statefulset", owner.name)) || !sameUID(serviceBefore, workspaceMustGetObject(t, "service", owner.name)) {
		t.Fatalf("full-digest identity collision mutated children")
	}
	stsConflict := workspaceMustGetObject(t, "statefulset", owner.name)
	serviceConflict := workspaceMustGetObject(t, "service", owner.name)
	if !reflect.DeepEqual(stsBefore["spec"], stsConflict["spec"]) || !reflect.DeepEqual(serviceBefore["spec"], serviceConflict["spec"]) {
		t.Fatalf("full-digest identity collision changed protected child specs")
	}
	assertProtectedIdentityMetadata(t, stsBefore, stsConflict)
	assertProtectedIdentityMetadata(t, serviceBefore, serviceConflict)
	f.assertChildIdentity(t, owner, stsConflict, serviceConflict)
	if out, err := workspaceKubectl(workspaceRuntimeContext, t, f.principal(), "delete", "workspace/"+owner.name, "-n", namespace, "--wait=true"); err != nil {
		t.Fatalf("delete rejected identity-conflict request: %v %s", err, out)
	}
	f.waitObjectAbsent(t, "workspace", owner.name)
	f.createWorkspaceAsHarness(t, owner, 0, nestedString(stsBefore, "metadata", "uid"), "")
	stsRestored := workspaceMustGetObject(t, "statefulset", owner.name)
	serviceRestored := workspaceMustGetObject(t, "service", owner.name)
	if !sameUID(stsBefore, stsRestored) || !sameUID(serviceBefore, serviceRestored) || !reflect.DeepEqual(stsBefore["spec"], stsRestored["spec"]) || !reflect.DeepEqual(serviceBefore["spec"], serviceRestored["spec"]) {
		t.Fatalf("restoring original full identity replaced existing children")
	}
	assertProtectedIdentityMetadata(t, stsBefore, stsRestored)
	assertProtectedIdentityMetadata(t, serviceBefore, serviceRestored)
	f.assertChildIdentity(t, owner, stsRestored, serviceRestored)
}

func (f *workspaceRuntimeFixture) cloneOwner(t *testing.T) error {
	t.Helper()
	for _, name := range []string{f.drift.profile, f.drift.agent} {
		kind := "agentprofile"
		if strings.Contains(name, "-agent") {
			kind = "achagent"
		}
		out, err := workspaceKubectl(workspaceRuntimeContext, t, "", "get", kind, name, "-n", namespace, "--ignore-not-found=true", "-o", "name")
		if err != nil {
			return fmt.Errorf("check disposable drift %s/%s: %w", kind, name, err)
		}
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("refusing to overwrite existing test-owned %s %s", kind, name)
		}
	}
	profile := workspaceMustGetObject(t, "agentprofile", f.main.profile)
	resetObjectMetadata(profile, f.drift.profile)
	if err := workspaceCreateObject(t, profile); err != nil {
		return fmt.Errorf("create disposable drift profile: %w", err)
	}
	profileCreated, err := workspaceGetObject(t, "agentprofile", f.drift.profile)
	if err != nil {
		return fmt.Errorf("read newly created disposable drift profile: %w", err)
	}
	f.drift.profileUID = nestedString(profileCreated, "metadata", "uid")
	agent := workspaceMustGetObject(t, "achagent", f.main.agent)
	resetObjectMetadata(agent, f.drift.agent)
	spec := nestedMap(agent, "spec")
	nestedMap(spec, "profileRef")["name"] = f.drift.profile
	if err := workspaceCreateObject(t, agent); err != nil {
		return fmt.Errorf("create disposable drift agent: %w", err)
	}
	return nil
}

func (f *workspaceRuntimeFixture) awaitUpdatePending(t *testing.T, name string) {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			t.Fatalf("Workspace %s did not report UpdatePending before deadline: %v", name, err)
		}
		obj, err := workspaceGetObject(t, "workspace", name)
		if err == nil {
			c := objectCondition(obj, "UpdatePending")
			generation := nestedInt64(obj, "metadata", "generation")
			if c["status"] == "True" && nestedInt64(obj, "status", "observedGeneration") == generation && int64Value(c["observedGeneration"]) == generation {
				return
			}
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) awaitTemplate(t *testing.T, owner workspaceRuntimeOwner, image, reqCPU, reqMem, limitCPU, limitMem string) {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			t.Fatalf("sleeping Workspace %s did not refresh template before deadline: %v", owner.name, err)
		}
		sts, err := workspaceGetObject(t, "statefulset", owner.name)
		if err == nil && nestedInt64(sts, "spec", "replicas") == 0 {
			c := nestedSliceMap(sts, "spec", "template", "spec", "containers")
			if len(c) == 1 && c[0]["image"] == image {
				f.assertResources(t, c[0], reqCPU, reqMem, limitCPU, limitMem)
				return
			}
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) assertTemplate(t *testing.T, owner workspaceRuntimeOwner, image, reqCPU, reqMem, limitCPU, limitMem string) {
	t.Helper()
	sts := workspaceMustGetObject(t, "statefulset", owner.name)
	c := nestedSliceMap(sts, "spec", "template", "spec", "containers")
	if len(c) != 1 || c[0]["image"] != image {
		t.Fatalf("template image=%v, want %s", c, image)
	}
	f.assertResources(t, c[0], reqCPU, reqMem, limitCPU, limitMem)
}

func (f *workspaceRuntimeFixture) assertResources(t *testing.T, c map[string]any, reqCPU, reqMem, limitCPU, limitMem string) {
	t.Helper()
	r := nestedMap(c, "resources")
	assertQuantity(t, r, "requests", "cpu", reqCPU)
	assertQuantity(t, r, "requests", "memory", reqMem)
	assertQuantity(t, r, "limits", "cpu", limitCPU)
	assertQuantity(t, r, "limits", "memory", limitMem)
}

func (f *workspaceRuntimeFixture) assertPodExecutionSettings(t *testing.T, pod map[string]any, image, reqCPU, reqMem, limitCPU, limitMem string) {
	t.Helper()
	containers := nestedSliceMap(pod, "spec", "containers")
	if len(containers) != 1 || containers[0]["name"] != "execution" || containers[0]["image"] != image {
		t.Fatalf("live execution Pod image/shape=%v, want image %s", containers, image)
	}
	f.assertResources(t, containers[0], reqCPU, reqMem, limitCPU, limitMem)
}

func (f *workspaceRuntimeFixture) awaitOnePod(t *testing.T, owner workspaceRuntimeOwner) map[string]any {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			pods, _ := workspaceMatchingPods(t, owner.name, owner.uid)
			t.Fatalf("Workspace %s did not create one matching Pod: %v; pods=%v", owner.name, err, pods)
		}
		pods, err := workspaceMatchingPods(t, owner.name, owner.uid)
		if err == nil && len(pods) == 1 && nestedString(pods[0], "metadata", "deletionTimestamp") == "" {
			return pods[0]
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) awaitExecutionRunning(t *testing.T, owner workspaceRuntimeOwner, observed map[string]any) {
	t.Helper()
	name := nestedString(observed, "metadata", "name")
	uid := nestedString(observed, "metadata", "uid")
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			t.Fatalf("execution container in Pod %s/%s did not start before the shared deadline: %v", namespace, name, err)
		}
		pod, err := workspaceGetObject(t, "pod", name)
		if err == nil {
			if nestedString(pod, "metadata", "uid") != uid || nestedString(pod, "metadata", "deletionTimestamp") != "" {
				t.Fatalf("observed Pod %s was replaced or is deleting before exec", name)
			}
			for _, status := range nestedSliceMap(pod, "status", "containerStatuses") {
				if status["name"] == "execution" {
					state, ok := status["state"].(map[string]any)
					if running, hasRunning := state["running"]; ok && hasRunning && running != nil {
						return
					}
				}
			}
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) assertObservedPodUnchanged(t *testing.T, observed map[string]any) {
	t.Helper()
	name := nestedString(observed, "metadata", "name")
	current := workspaceMustGetObject(t, "pod", name)
	if nestedString(current, "metadata", "namespace") != namespace || nestedString(current, "metadata", "uid") != nestedString(observed, "metadata", "uid") || nestedString(current, "metadata", "deletionTimestamp") != "" {
		t.Fatalf("exec target Pod %s was replaced or began deletion", name)
	}
}

func (f *workspaceRuntimeFixture) assertPodOwner(t *testing.T, owner workspaceRuntimeOwner, sts, pod map[string]any) {
	t.Helper()
	labels := map[string]string{
		"runtime.ach.ackstorm.ai/agent-uid":      owner.uid,
		"runtime.ach.ackstorm.ai/workspace-name": owner.name,
	}
	if !stringMapContains(nestedStringMap(pod, "metadata", "labels"), labels) || nestedString(pod, "metadata", "annotations", "runtime.ach.ackstorm.ai/workspace-ref") != owner.ref || !hasOwnerReference(pod, nestedString(sts, "metadata", "name"), nestedString(sts, "metadata", "uid"), "StatefulSet") {
		t.Fatalf("Pod does not preserve full identity/StatefulSet owner: metadata=%v owners=%v", pod["metadata"], pod["metadata"].(map[string]any)["ownerReferences"])
	}
}

func (f *workspaceRuntimeFixture) addHoldFinalizer(t *testing.T, pod map[string]any) {
	t.Helper()
	name := nestedString(pod, "metadata", "name")
	obj := workspaceMustGetObject(t, "pod", name)
	metadata := nestedMap(obj, "metadata")
	finalizers := nestedStringSlice(metadata["finalizers"])
	if !workspaceStringSliceContains(finalizers, workspaceHarnessFinalizer) {
		finalizers = append(finalizers, workspaceHarnessFinalizer)
	}
	patch := map[string]any{"metadata": map[string]any{
		"resourceVersion": metadata["resourceVersion"], "finalizers": finalizers,
	}}
	if _, err := workspacePatchObject(t, "pod", name, patch); err != nil {
		t.Fatalf("add test-only held-Pod finalizer: %v", err)
	}
	f.ownedPods[name] = nestedString(obj, "metadata", "uid")
}

func (f *workspaceRuntimeFixture) releaseHeldPods(t *testing.T) {
	t.Helper()
	for name, uid := range f.ownedPods {
		obj, err := workspaceGetObject(t, "pod", name)
		if err != nil {
			continue
		}
		if nestedString(obj, "metadata", "uid") != uid || nestedString(obj, "metadata", "namespace") != namespace {
			t.Errorf("refusing to remove test finalizer from replacement Pod %s", name)
			continue
		}
		metadata := nestedMap(obj, "metadata")
		finalizers := nestedStringSlice(metadata["finalizers"])
		filtered := finalizers[:0]
		for _, finalizer := range finalizers {
			if finalizer != workspaceHarnessFinalizer {
				filtered = append(filtered, finalizer)
			}
		}
		if len(filtered) == len(finalizers) {
			continue
		}
		patch := map[string]any{"metadata": map[string]any{
			"resourceVersion": metadata["resourceVersion"], "finalizers": filtered,
		}}
		if _, err := workspacePatchObject(t, "pod", name, patch); err != nil {
			t.Errorf("remove test-only finalizer from owned Pod %s: %v", name, err)
		}
	}
}

func (f *workspaceRuntimeFixture) waitForTerminatingPod(t *testing.T, owner workspaceRuntimeOwner) {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			t.Fatalf("held Pod for %s did not enter terminating state: %v", owner.name, err)
		}
		pods, err := workspaceMatchingPods(t, owner.name, owner.uid)
		if err == nil && len(pods) == 1 && nestedString(pods[0], "metadata", "deletionTimestamp") != "" {
			return
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) waitForHeldScaleDown(t *testing.T, owner workspaceRuntimeOwner, generation int64) {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			t.Fatalf("Workspace %s did not reach held scale-zero state before deadline: %v", owner.name, err)
		}
		workspace, workspaceErr := workspaceGetObject(t, "workspace", owner.name)
		sts, stsErr := workspaceGetObject(t, "statefulset", owner.name)
		pods, podErr := workspaceMatchingPods(t, owner.name, owner.uid)
		if workspaceErr == nil && stsErr == nil && podErr == nil &&
			nestedInt64(workspace, "metadata", "generation") == generation && nestedInt64(workspace, "spec", "replicas") == 0 &&
			nestedInt64(sts, "spec", "replicas") == 0 && len(pods) == 1 && nestedString(pods[0], "metadata", "deletionTimestamp") != "" {
			return
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) waitObjectAbsent(t *testing.T, kind, name string) {
	t.Helper()
	for {
		if err := workspaceRuntimeContext.Err(); err != nil {
			t.Fatalf("%s/%s still exists at shared deadline: %v", kind, name, err)
		}
		_, err := workspaceGetObject(t, kind, name)
		if err != nil {
			return
		}
		time.Sleep(time.Second)
	}
}

func (f *workspaceRuntimeFixture) describeIdentity(t *testing.T, name, uid string) string {
	t.Helper()
	args := []string{"get", "pods", "-o", "json"}
	if uid != "" {
		args = append(args, "-l", "runtime.ach.ackstorm.ai/agent-uid="+uid)
	}
	out, err := workspaceKubectl(workspaceRuntimeContext, t, "", append([]string{"-n", namespace}, args...)...)
	return fmt.Sprintf("err=%v output=%s", err, out)
}

func (f *workspaceRuntimeFixture) adminPatch(kind, name string, patch map[string]any) (string, error) {
	return workspacePatchObject(nil, kind, name, patch)
}

func (f *workspaceRuntimeFixture) cleanupOwner(t *testing.T, owner workspaceRuntimeOwner) {
	t.Helper()
	if owner.uid == "" {
		return
	}
	obj, err := workspaceGetObject(t, "workspace", owner.name)
	if err != nil {
		t.Errorf("re-read test-owned Workspace %s/%s before cleanup: %v", namespace, owner.name, err)
		return
	}
	if nestedString(obj, "metadata", "namespace") != namespace || nestedString(obj, "metadata", "labels", "runtime.ach.ackstorm.ai/agent-uid") != owner.uid || nestedString(obj, "metadata", "annotations", "runtime.ach.ackstorm.ai/workspace-ref") != owner.ref || !hasOwnerReference(obj, owner.agent, owner.uid, "ACHAgent") {
		t.Errorf("refusing to clean Workspace %s/%s with mismatched owner identity", namespace, owner.name)
		return
	}
	sts := workspaceGetObjectMustValue(t, "statefulset", owner.name)
	stsUID := nestedString(sts, "metadata", "uid")
	podUID := ""
	pods, _ := workspaceMatchingPods(t, owner.name, owner.uid)
	if len(pods) == 1 {
		podUID = nestedString(pods[0], "metadata", "uid")
	}
	gen := workspaceTestRequest(t, owner.uid, owner.name, 0, stsUID, podUID)
	f.awaitApplied(t, owner.name, gen, "True", "")
	workspaceTestAwaitNoPod(t, owner.name, owner.uid, owner.ref)
	principal := "system:serviceaccount:" + namespace + ":" + fEffectiveHarnessSA(t, owner.uid)
	if out, err := workspaceKubectl(workspaceRuntimeContext, t, principal, "delete", "workspace/"+owner.name, "-n", namespace, "--wait=true"); err != nil {
		t.Errorf("delete test-owned Workspace %s after sleep: %v %s", owner.name, err, out)
	}
	for _, target := range []struct{ kind, name, uid string }{
		{"achagent", owner.agent, owner.uid}, {"agentprofile", owner.profile, owner.profileUID},
	} {
		if target.uid == "" {
			t.Errorf("refusing to delete %s/%s without its recorded UID", target.kind, target.name)
			continue
		}
		out, err := workspaceKubectl(workspaceRuntimeContext, t, "", "get", target.kind, target.name, "-n", namespace, "--ignore-not-found=true", "-o", "json")
		if err != nil {
			t.Errorf("re-read test-owned %s/%s before cleanup: %v %s", target.kind, target.name, err, out)
			continue
		}
		if strings.TrimSpace(out) == "" {
			continue
		}
		var current map[string]any
		if err := json.Unmarshal([]byte(out), &current); err != nil {
			t.Errorf("decode test-owned %s/%s before cleanup: %v", target.kind, target.name, err)
			continue
		}
		if nestedString(current, "metadata", "namespace") != namespace || nestedString(current, "metadata", "uid") != target.uid {
			t.Errorf("refusing to delete %s/%s: live namespace/UID no longer matches recorded identity", target.kind, target.name)
			continue
		}
		resource := target.kind + "/" + target.name
		var plural string
		switch target.kind {
		case "achagent":
			plural = "achagents"
		case "agentprofile":
			plural = "agentprofiles"
		default:
			t.Errorf("refusing to delete unsupported test-owned resource kind %q", target.kind)
			continue
		}
		deleteOptions, err := json.Marshal(map[string]any{
			"apiVersion": "v1",
			"kind":       "DeleteOptions",
			"preconditions": map[string]any{
				"uid": target.uid,
			},
			"propagationPolicy": "Background",
		})
		if err != nil {
			t.Errorf("encode UID-preconditioned delete for test-owned %s: %v", resource, err)
			continue
		}
		path := fmt.Sprintf("/apis/ach.ackstorm.ai/v1alpha1/namespaces/%s/%s/%s", namespace, plural, target.name)
		if out, err := workspaceKubectlInput(workspaceRuntimeContext, t, "", []string{"delete", "--raw=" + path, "-f", "-"}, string(deleteOptions)); err != nil {
			t.Errorf("delete test-owned %s: %v %s", resource, err, out)
		}
	}
}

func fEffectiveHarnessSA(t *testing.T, agentUID string) string {
	t.Helper()
	for _, name := range []string{"workspace-kube-e2e-agent", "workspace-kube-e2e-drift-agent"} {
		obj, err := workspaceGetObject(t, "achagent", name)
		if err != nil || nestedString(obj, "metadata", "uid") != agentUID {
			continue
		}
		// Resolve the actual workload reference through the existing helper; the
		// ServiceAccount name is read from its live effective Pod template.
		ref := controlStatefulSetRef(name)
		stsName := strings.TrimPrefix(ref, "statefulset/")
		return nestedString(workspaceMustGetObject(t, "statefulset", stsName), "spec", "template", "spec", "serviceAccountName")
	}
	t.Fatalf("agent UID %s is not one of this test's disposable owners", agentUID)
	return ""
}

func workspacePatchObject(t *testing.T, kind, name string, patch map[string]any) (string, error) {
	if t != nil {
		t.Helper()
	}
	current, err := workspaceGetObjectOptional(kind, name)
	if err != nil {
		return "", err
	}
	resourceVersion := nestedString(current, "metadata", "resourceVersion")
	meta := nestedMap(patch, "metadata")
	meta["resourceVersion"] = resourceVersion
	encoded, err := json.Marshal(patch)
	if err != nil {
		return "", err
	}
	ctx := workspaceRuntimeContext
	if ctx == nil {
		ctx = context.Background()
	}
	return workspaceKubectlInput(ctx, t, "", []string{"patch", kind + "/" + name, "-n", namespace, "--type=merge", "--patch-file=/dev/stdin", "-o", "json"}, string(encoded))
}

func workspaceCreateObject(t *testing.T, object map[string]any) error {
	t.Helper()
	encoded, err := json.Marshal(object)
	if err != nil {
		return err
	}
	_, err = workspaceKubectlInput(workspaceRuntimeContext, t, "", []string{"create", "-f", "-", "-n", namespace}, string(encoded))
	return err
}

func resetObjectMetadata(object map[string]any, name string) {
	metadata := nestedMap(object, "metadata")
	for _, key := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "managedFields", "deletionTimestamp", "deletionGracePeriodSeconds"} {
		delete(metadata, key)
	}
	metadata["name"] = name
	delete(object, "status")
}

func workspaceKubectl(ctx context.Context, t *testing.T, principal string, args ...string) (string, error) {
	return workspaceKubectlInput(ctx, t, principal, args, "")
}

func workspaceKubectlInput(ctx context.Context, t *testing.T, principal string, args []string, input string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	commandArgs := make([]string, 0, len(args)+2)
	if principal != "" {
		commandArgs = append(commandArgs, "--as="+principal)
	}
	commandArgs = append(commandArgs, args...)
	cmd := exec.CommandContext(ctx, "kubectl", commandArgs...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("kubectl %s: %w: %s", strings.Join(commandArgs, " "), err, output)
	}
	return string(output), nil
}

func workspaceGetObject(t *testing.T, kind, name string) (map[string]any, error) {
	if t != nil {
		t.Helper()
	}
	return workspaceGetObjectContext(workspaceRuntimeContext, t, kind, name)
}

func workspaceGetObjectContext(ctx context.Context, t *testing.T, kind, name string) (map[string]any, error) {
	out, err := workspaceKubectl(ctx, t, "", "get", kind, name, "-n", namespace, "-o", "json", "--request-timeout=10s")
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(out), &object); err != nil {
		return nil, fmt.Errorf("decode %s/%s: %w", kind, name, err)
	}
	return object, nil
}

func workspaceGetObjectOptional(kind, name string) (map[string]any, error) {
	return workspaceGetObjectContext(workspaceRuntimeContext, nil, kind, name)
}

func workspaceMustGetObject(t *testing.T, kind, name string) map[string]any {
	t.Helper()
	obj, err := workspaceGetObject(t, kind, name)
	if err != nil {
		t.Fatalf("get %s/%s: %v", kind, name, err)
	}
	return obj
}

func workspaceTestGet(t *testing.T, kind, name string) map[string]any {
	t.Helper()
	return workspaceMustGetObject(t, kind, name)
}

func workspaceGetObjectMustValue(t *testing.T, kind, name string) map[string]any {
	return workspaceMustGetObject(t, kind, name)
}

func workspaceMatchingPods(t *testing.T, name, agentUID string) ([]map[string]any, error) {
	t.Helper()
	out, err := workspaceKubectl(workspaceRuntimeContext, t, "", "get", "pods", "-n", namespace, "-o", "json", "--request-timeout=10s")
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, err
	}
	matched := []map[string]any{}
	for _, pod := range list.Items {
		podName := nestedString(pod, "metadata", "name")
		labels := nestedStringMap(pod, "metadata", "labels")
		if podName == name+"-0" || (labels["runtime.ach.ackstorm.ai/agent-uid"] == agentUID && labels["runtime.ach.ackstorm.ai/workspace-name"] == name) {
			matched = append(matched, pod)
		}
	}
	return matched, nil
}

func workspaceDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestWorkspaceResourceNameFromAgentName(t *testing.T) {
	ref := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tt := range []struct{ agentName, want string }{
		{"demo", "ach-ws-demo-0123456789abcdef0123"},
		{"demo.agent", "ach-ws-demo-agent-0123456789abcdef0123"},
		{strings.Repeat("a", 24), "ach-ws-aaaaaaaaaaaaaaaaaaaaaaaa-0123456789abcdef0123"},
		{strings.Repeat("a", 25), "ach-ws-aaaaaaaaaaaaaaa-2f521e2a-0123456789abcdef0123"},
		{"abcdefghijklmn-abcdefghij", "ach-ws-abcdefghijklmn-c58c443f-0123456789abcdef0123"},
		{"abcdefghijklmn.abcdefghij", "ach-ws-abcdefghijklmn-bacb9c3c-0123456789abcdef0123"},
	} {
		got := workspaceResourceNameFromIdentity(tt.agentName, ref)
		if got != tt.want {
			t.Errorf("workspaceResourceNameFromIdentity(%q) = %q, want %q", tt.agentName, got, tt.want)
		}
		if len(got) > 52 {
			t.Errorf("workspace name length = %d, want at most 52", len(got))
		}
	}
}

func workspaceResourceNameFromIdentity(agentName, workspaceRef string) string {
	namePart := strings.ReplaceAll(agentName, ".", "-")
	if len(namePart) > 24 {
		hash := sha256.Sum256([]byte(agentName))
		namePart = strings.TrimRight(namePart[:15], "-") + fmt.Sprintf("-%x", hash[:4])
	}
	refPrefix := workspaceRef
	if len(refPrefix) > 20 {
		refPrefix = refPrefix[:20]
	}
	return "ach-ws-" + namePart + "-" + refPrefix
}

func objectCondition(object map[string]any, conditionType string) map[string]any {
	conditions := nestedSliceMap(object, "status", "conditions")
	for _, condition := range conditions {
		if condition["type"] == conditionType {
			return condition
		}
	}
	return map[string]any{}
}

func nestedMap(object map[string]any, path ...string) map[string]any {
	current := object
	for _, key := range path {
		value, ok := current[key].(map[string]any)
		if !ok {
			value = map[string]any{}
			current[key] = value
		}
		current = value
	}
	return current
}

func nestedSliceMap(object map[string]any, path ...string) []map[string]any {
	var current any = object
	for _, key := range path {
		parent, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = parent[key]
	}
	values, ok := current.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if entry, ok := value.(map[string]any); ok {
			result = append(result, entry)
		}
	}
	return result
}

func nestedString(object map[string]any, path ...string) string {
	var current any = object
	for _, key := range path {
		parent, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = parent[key]
	}
	value, _ := current.(string)
	return value
}

func nestedInt64(object map[string]any, path ...string) int64 {
	var current any = object
	for _, key := range path {
		parent, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current = parent[key]
	}
	return int64Value(current)
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func nestedBool(object map[string]any, path ...string) bool {
	var current any = object
	for _, key := range path {
		parent, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current = parent[key]
	}
	value, _ := current.(bool)
	return value
}

func nestedStringMap(object map[string]any, path ...string) map[string]string {
	var current any = object
	for _, key := range path {
		parent, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = parent[key]
	}
	values, ok := current.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		if text, ok := value.(string); ok {
			result[key] = text
		}
	}
	return result
}

func nestedStringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func stringMapContains(actual, expected map[string]string) bool {
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func hasOwnerReference(object map[string]any, name, uid, kind string) bool {
	for _, ref := range nestedSliceMap(object, "metadata", "ownerReferences") {
		if ref["name"] == name && ref["uid"] == uid && ref["kind"] == kind && ref["controller"] == true {
			return true
		}
	}
	return false
}

func hasVolumeConfigMap(sts map[string]any, volumeName, configMapName string) bool {
	for _, volume := range nestedSliceMap(sts, "spec", "template", "spec", "volumes") {
		if volume["name"] == volumeName && nestedString(volume, "configMap", "name") == configMapName {
			return true
		}
	}
	return false
}

func volumeSize(volumes []map[string]any, name string) string {
	for _, volume := range volumes {
		if volume["name"] == name {
			return nestedString(volume, "emptyDir", "sizeLimit")
		}
	}
	return ""
}

func hasMount(mounts []map[string]any, name, path string, readOnly bool) bool {
	for _, mount := range mounts {
		if mount["name"] != name || mount["mountPath"] != path {
			continue
		}
		actualReadOnly := false
		if value, present := mount["readOnly"]; present {
			var ok bool
			actualReadOnly, ok = value.(bool)
			if !ok {
				return false
			}
		}
		return actualReadOnly == readOnly
	}
	return false
}

func TestWorkspaceHasMountReadOnlySerialization(t *testing.T) {
	for _, test := range []struct {
		name     string
		field    any
		present  bool
		wantRead bool
		want     bool
	}{
		{name: "omitted means false", want: true},
		{name: "explicit false", field: false, present: true, want: true},
		{name: "true", field: true, present: true, wantRead: true, want: true},
		{name: "wrong type rejected", field: "false", present: true, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			mount := map[string]any{"name": "bootstrap", "mountPath": "/etc/ach-runtime"}
			if test.present {
				mount["readOnly"] = test.field
			}
			if got := hasMount([]map[string]any{mount}, "bootstrap", "/etc/ach-runtime", test.wantRead); got != test.want {
				t.Fatalf("hasMount()=%t, want %t", got, test.want)
			}
		})
	}
	if hasMount([]map[string]any{{"name": "bootstrap", "mountPath": "/etc/ach-runtime"}}, "bootstrap", "/etc/ach-runtime", true) {
		t.Fatal("bootstrap mount without explicit readOnly=true was accepted")
	}
}

func TestWorkspaceCanIArguments(t *testing.T) {
	for _, test := range []struct {
		resource string
		want     []string
	}{
		{"statefulsets/scale", []string{"auth", "can-i", "update", "statefulsets", "--subresource=scale", "-n", "ach-system", "--as=system:serviceaccount:ach-system:agent"}},
		{"workspaces/status", []string{"auth", "can-i", "patch", "workspaces", "--subresource=status", "-n", "ach-system", "--as=system:serviceaccount:ach-system:agent"}},
		{"pods/exec", []string{"auth", "can-i", "create", "pods", "--subresource=exec", "-n", "ach-system", "--as=system:serviceaccount:ach-system:agent"}},
		{"services", []string{"auth", "can-i", "get", "services", "-n", "ach-system", "--as=system:serviceaccount:ach-system:agent"}},
	} {
		got := workspaceCanIArgs(map[string]string{"statefulsets/scale": "update", "workspaces/status": "patch", "pods/exec": "create", "services": "get"}[test.resource], test.resource, "ach-system", "system:serviceaccount:ach-system:agent")
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("workspaceCanIArgs(%s)=%v, want %v", test.resource, got, test.want)
		}
	}
}

func TestWorkspaceCanIResultClassification(t *testing.T) {
	for _, test := range []struct {
		name      string
		out       string
		exit      int
		hasErr    bool
		hasStderr bool
		want      bool
		valid     bool
	}{
		{name: "yes accepted", out: "yes\n", want: true, valid: true},
		{name: "no exit one is denial", out: "no\n", exit: 1, hasErr: true, want: false, valid: true},
		{name: "no exit two rejected", out: "no\n", exit: 2, hasErr: true, want: false},
		{name: "yes with error rejected", out: "yes\n", exit: 1, hasErr: true, want: true},
		{name: "no with stderr rejected", out: "no\n", exit: 1, hasErr: true, hasStderr: true, want: false},
		{name: "no without error rejected", out: "no\n", want: false},
		{name: "unexpected output rejected", out: "maybe\n", exit: 1, hasErr: true, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := workspaceCanIClassification(test.out, test.exit, test.hasErr, test.hasStderr, test.want); got != test.valid {
				t.Fatalf("classification=%t, want %t", got, test.valid)
			}
		})
	}
}

func assertQuantity(t *testing.T, resources map[string]any, group, name, want string) {
	t.Helper()
	got := nestedString(resources, group, name)
	if got != want {
		t.Fatalf("resources.%s.%s=%q, want %q", group, name, got, want)
	}
}

func containsLine(text, want string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func workspaceStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sameUID(a, b map[string]any) bool {
	return nestedString(a, "metadata", "uid") != "" && nestedString(a, "metadata", "uid") == nestedString(b, "metadata", "uid")
}

func bytesEqual(a, b []byte) bool { return reflect.DeepEqual(a, b) }

func statefulSetSpecWithReplicas(sts map[string]any, replicas float64) map[string]any {
	encoded, _ := json.Marshal(nestedMap(sts, "spec"))
	var spec map[string]any
	_ = json.Unmarshal(encoded, &spec)
	spec["replicas"] = replicas
	return spec
}
