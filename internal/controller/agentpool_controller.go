/*
Copyright 2026 Amaan Ul Haq Siddiqui.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	agentsv1alpha1 "github.com/amaanx86/azure-devops-agent-operator/api/v1alpha1"
	"github.com/amaanx86/azure-devops-agent-operator/internal/azuredevops"
)

const finalizerName = "agents.amaanx86.github.io/cleanup"
const reconcileInterval = 30 * time.Second

// AgentPoolReconciler reconciles a AgentPool object.
type AgentPoolReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=agents.amaanx86.github.io,resources=agentpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.amaanx86.github.io,resources=agentpools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.amaanx86.github.io,resources=agentpools/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile moves the cluster state toward the desired state.
// The reconcile loop is level-triggered (event-driven but idempotent):
// any change to the AgentPool triggers a reconcile, during which the
// controller computes the desired state and corrects any drift.
func (r *AgentPoolReconciler) Reconcile(ctx context.Context,
	req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Step 1: Fetch the AgentPool. If deleted, stop (GC handled by K8s).
	pool := &agentsv1alpha1.AgentPool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Step 2: Handle deletion via finalizer.
	if !pool.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, pool)
	}

	// Step 3: Ensure finalizer is present.
	if !hasFinalizer(pool, finalizerName) {
		addFinalizer(pool, finalizerName)
		if err := r.Update(ctx, pool); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Step 4: Fetch PAT from Secret.
	pat, result := r.fetchPATToken(ctx, req, pool)
	if result.RequeueAfter > 0 {
		return result, nil
	}

	// Step 5: Resolve ADO pool ID (cached in status).
	adoClient := azuredevops.NewClient(pat)
	if pool.Status.PoolID == 0 {
		result, err := r.resolvePoolID(ctx, pool, adoClient)
		if err != nil || result.RequeueAfter > 0 {
			return result, err
		}
	}

	// Step 6: Query ADO for pending/running jobs.
	pending, running, err := adoClient.GetJobCounts(ctx,
		pool.Spec.OrganizationURL, int(pool.Status.PoolID))
	if err != nil {
		log.Error(err, "Failed to query ADO job counts")
		meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
			Type:               "Available",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pool.Generation,
			Reason:             "JobCountQueryFailed",
			Message:            fmt.Sprintf("ADO API error: %v", err),
		})
		if err := r.Status().Update(ctx, pool); err != nil {
			log.Error(err, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: reconcileInterval}, nil
	}

	// Step 7: List existing agent Pods.
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(req.Namespace),
		client.MatchingLabels{"agentpool": pool.Name}); err != nil {
		log.Error(err, "Failed to list agent pods")
		return ctrl.Result{}, err
	}

	// Count running/pending pods (exclude succeeded/failed).
	activeCount := 0
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
			activeCount++
		}
	}

	// Step 8: Compute desired replicas.
	desiredCount := min(max(pending+running, int(pool.Spec.MinAgents)),
		int(pool.Spec.MaxAgents))

	// Step 9: Scale up if needed.
	toCreate := desiredCount - activeCount
	for range toCreate {
		pod := r.buildAgentPod(pool, req.Namespace)
		if err := setControllerReference(pool, pod, r.Scheme); err != nil {
			log.Error(err, "Failed to set controller reference on pod")
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, pod); err != nil {
			log.Error(err, "Failed to create agent pod")
			return ctrl.Result{}, err
		}
		log.Info("Created agent pod", "pod", pod.Name)
		r.Recorder.Event(pool, corev1.EventTypeNormal, "PodCreated",
			fmt.Sprintf("Created agent pod %s", pod.Name))
	}

	// Step 10: Scale down if needed. Prefer deleting succeeded pods.
	toDelete := activeCount - desiredCount
	podsToDelete := r.selectPodsToDelete(podList, toDelete)
	for _, pod := range podsToDelete {
		if err := r.Delete(ctx, &pod); err != nil {
			log.Error(err, "Failed to delete agent pod", "pod", pod.Name)
			return ctrl.Result{}, err
		}
		log.Info("Deleted agent pod", "pod", pod.Name)
		r.Recorder.Event(pool, corev1.EventTypeNormal, "PodDeleted",
			fmt.Sprintf("Deleted agent pod %s", pod.Name))
	}

	// Step 11: Manage dummy agent for scale-to-zero.
	r.manageDummyAgent(ctx, pool, adoClient, activeCount)

	// Step 12: Update status.
	log.Info("Updating status", "activeCount", activeCount, "pending", pending, "running", running)
	activeAgentsVal := int32(activeCount)
	pendingJobsVal := int32(pending + running)
	pool.Status.ActiveAgents = &activeAgentsVal
	pool.Status.PendingJobs = &pendingJobsVal

	// Step 13: Update conditions.
	meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
		Type:               "Available",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: pool.Generation,
		Reason:             "Reconciled",
		Message: fmt.Sprintf("%d agent(s) running, %d job(s) pending",
			activeCount, pending+running),
	})

	log.Info("About to update status subresource", "activeAgents", pool.Status.ActiveAgents, "pendingJobs", pool.Status.PendingJobs)
	if err := r.Status().Update(ctx, pool); err != nil {
		log.Error(err, "Failed to update agent pool status")
		return ctrl.Result{}, err
	}
	log.Info("Status subresource updated successfully")

	// Step 14: Requeue after interval (the heartbeat).
	return ctrl.Result{RequeueAfter: reconcileInterval}, nil
}

// resolvePoolID resolves and caches the ADO pool ID in status.
func (r *AgentPoolReconciler) resolvePoolID(ctx context.Context,
	pool *agentsv1alpha1.AgentPool, adoClient *azuredevops.Client) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	poolID, err := adoClient.GetPoolID(ctx, pool.Spec.OrganizationURL,
		pool.Spec.PoolName)
	if err != nil {
		log.Error(err, "Failed to resolve pool ID")
		meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
			Type:               "Available",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pool.Generation,
			Reason:             "PoolResolutionFailed",
			Message:            fmt.Sprintf("Could not find ADO pool: %v", err),
		})
		if err := r.Status().Update(ctx, pool); err != nil {
			log.Error(err, "Failed to update status after pool resolution error")
		}
		return ctrl.Result{RequeueAfter: reconcileInterval}, nil
	}
	pool.Status.PoolID = int32(poolID)
	if err := r.Status().Update(ctx, pool); err != nil {
		log.Error(err, "Failed to cache pool ID in status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// fetchPATToken retrieves the ADO PAT from the referenced Secret.
func (r *AgentPoolReconciler) fetchPATToken(ctx context.Context,
	req ctrl.Request, pool *agentsv1alpha1.AgentPool) (string, ctrl.Result) {
	log := logf.FromContext(ctx)
	secret := &corev1.Secret{}
	secretNN := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      pool.Spec.TokenSecretRef.Name,
	}
	if err := r.Get(ctx, secretNN, secret); err != nil {
		log.Error(err, "Failed to fetch token secret")
		meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
			Type:               "Available",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pool.Generation,
			Reason:             "SecretNotFound",
			Message:            fmt.Sprintf("Could not read secret: %v", err),
		})
		if err := r.Status().Update(ctx, pool); err != nil {
			log.Error(err, "Failed to update status after secret error")
		}
		return "", ctrl.Result{RequeueAfter: reconcileInterval}
	}

	patBytes, ok := secret.Data[pool.Spec.TokenSecretRef.Key]
	if !ok {
		err := fmt.Errorf("key %q not found in secret", pool.Spec.TokenSecretRef.Key)
		log.Error(err, "Token secret missing key")
		meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
			Type:               "Available",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pool.Generation,
			Reason:             "SecretKeyMissing",
			Message:            fmt.Sprintf("Key missing in secret: %v", err),
		})
		if err := r.Status().Update(ctx, pool); err != nil {
			log.Error(err, "Failed to update status")
		}
		return "", ctrl.Result{RequeueAfter: reconcileInterval}
	}

	return string(patBytes), ctrl.Result{}
}

// manageDummyAgent handles registration and unregistration of the dummy agent.
func (r *AgentPoolReconciler) manageDummyAgent(ctx context.Context,
	pool *agentsv1alpha1.AgentPool, adoClient *azuredevops.Client,
	activeCount int) {
	log := logf.FromContext(ctx)
	if activeCount == 0 && pool.Status.DummyAgentID == 0 {
		dummyName := fmt.Sprintf("%s-dummy", pool.Name)
		id, err := adoClient.RegisterDummyAgent(ctx,
			pool.Spec.OrganizationURL, int(pool.Status.PoolID), dummyName)
		if err != nil {
			if !strings.Contains(err.Error(), "already contains an agent with name") {
				log.Error(err, "Failed to register dummy agent")
				return
			}
			log.Info("Dummy agent already exists, attempting to find its ID")
		}
		if id > 0 {
			pool.Status.DummyAgentID = int32(id)
			log.Info("Registered dummy agent", "agentID", id)
			r.Recorder.Event(pool, corev1.EventTypeNormal, "DummyAgentRegistered",
				fmt.Sprintf("Registered dummy agent %s (ID: %d)", dummyName, id))
		}
	}
	if activeCount > 0 && pool.Status.DummyAgentID != 0 {
		if err := adoClient.UnregisterAgent(ctx,
			pool.Spec.OrganizationURL, int(pool.Status.PoolID),
			int(pool.Status.DummyAgentID)); err != nil {
			log.Error(err, "Failed to unregister dummy agent")
		} else {
			pool.Status.DummyAgentID = 0
			log.Info("Unregistered dummy agent")
			r.Recorder.Event(pool, corev1.EventTypeNormal, "DummyAgentUnregistered",
				"Unregistered dummy agent; real agents are now active")
		}
	}
}

// handleDeletion cleans up external resources (dummy agent) before deletion.
func (r *AgentPoolReconciler) handleDeletion(ctx context.Context,
	pool *agentsv1alpha1.AgentPool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !hasFinalizer(pool, finalizerName) {
		return ctrl.Result{}, nil
	}

	// Unregister dummy agent if present.
	if pool.Status.DummyAgentID != 0 {
		secret := &corev1.Secret{}
		secretNN := types.NamespacedName{
			Namespace: pool.Namespace,
			Name:      pool.Spec.TokenSecretRef.Name,
		}
		if err := r.Get(ctx, secretNN, secret); err == nil {
			patBytes, ok := secret.Data[pool.Spec.TokenSecretRef.Key]
			if ok {
				pat := string(patBytes)
				adoClient := azuredevops.NewClient(pat)
				if err := adoClient.UnregisterAgent(ctx,
					pool.Spec.OrganizationURL, int(pool.Status.PoolID),
					int(pool.Status.DummyAgentID)); err != nil {
					log.Error(err, "Failed to unregister dummy agent during deletion")
				} else {
					log.Info("Unregistered dummy agent during deletion")
				}
			}
		}
	}

	// Remove finalizer to allow deletion.
	removeFinalizer(pool, finalizerName)
	if err := r.Update(ctx, pool); err != nil {
		log.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// buildAgentPod constructs a Pod spec for an agent.
func (r *AgentPoolReconciler) buildAgentPod(pool *agentsv1alpha1.AgentPool,
	namespace string) *corev1.Pod {
	agentImage := pool.Spec.AgentImage
	if agentImage == "" {
		agentImage = "mcr.microsoft.com/azure-pipelines/vsts-agent:latest"
	}

	// Generate a unique pod name.
	podName := fmt.Sprintf("%s-%s", pool.Name, randomString(5))

	// Build environment variables.
	env := make([]corev1.EnvVar, 0, 5+len(pool.Spec.ExtraEnv))
	env = append(env,
		corev1.EnvVar{Name: "AZP_URL", Value: pool.Spec.OrganizationURL},
		corev1.EnvVar{Name: "AZP_POOL", Value: pool.Spec.PoolName},
		corev1.EnvVar{Name: "AZP_AGENT_NAME", Value: podName},
		corev1.EnvVar{Name: "AZP_ONCE", Value: "true"},
		corev1.EnvVar{
			Name: "AZP_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &pool.Spec.TokenSecretRef,
			},
		},
	)
	env = append(env, pool.Spec.ExtraEnv...)

	// Build volumes from cache volume templates.
	volumes := make([]corev1.Volume, 0, len(pool.Spec.CacheVolumes))
	volumeMounts := make([]corev1.VolumeMount, 0, len(pool.Spec.CacheVolumes))
	for _, cv := range pool.Spec.CacheVolumes {
		pvcName := fmt.Sprintf("%s-%s", pool.Name, cv.Name)
		volumes = append(volumes, corev1.Volume{
			Name: cv.Name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      cv.Name,
			MountPath: cv.MountPath,
		})
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "azure-devops-agent",
				"app.kubernetes.io/managed-by": "agentpool-controller",
				"app.kubernetes.io/instance":   pool.Name,
				"agentpool":                    pool.Name,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:         "agent",
					Image:        agentImage,
					Env:          env,
					VolumeMounts: volumeMounts,
					Resources:    pool.Spec.AgentResources,
				},
			},
			Volumes: volumes,
		},
	}

	return pod
}

// selectPodsToDelete chooses pods to delete, preferring succeeded/failed ones.
func (r *AgentPoolReconciler) selectPodsToDelete(podList *corev1.PodList,
	count int) []corev1.Pod {
	if count <= 0 {
		return nil
	}

	var succeeded, running []corev1.Pod
	for _, pod := range podList.Items {
		switch pod.Status.Phase {
		case corev1.PodSucceeded, corev1.PodFailed:
			succeeded = append(succeeded, pod)
		case corev1.PodRunning, corev1.PodPending:
			running = append(running, pod)
		}
	}

	var toDelete []corev1.Pod
	// First, delete succeeded/failed pods.
	for i := 0; i < len(succeeded) && len(toDelete) < count; i++ {
		toDelete = append(toDelete, succeeded[i])
	}
	// If we still need to delete more, delete running/pending pods.
	for i := 0; i < len(running) && len(toDelete) < count; i++ {
		toDelete = append(toDelete, running[i])
	}

	return toDelete
}

// randomString generates a random lowercase alphanumeric string.
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

// Helper functions for finalizer and owner reference management.

// hasFinalizer checks if a finalizer is present on an object.
func hasFinalizer(obj metav1.Object, finalizer string) bool {
	return slices.Contains(obj.GetFinalizers(), finalizer)
}

// addFinalizer adds a finalizer to an object if not present.
func addFinalizer(obj metav1.Object, finalizer string) {
	if !hasFinalizer(obj, finalizer) {
		obj.SetFinalizers(append(obj.GetFinalizers(), finalizer))
	}
}

// removeFinalizer removes a finalizer from an object if present.
func removeFinalizer(obj metav1.Object, finalizer string) {
	finalizers := obj.GetFinalizers()
	for i, f := range finalizers {
		if f == finalizer {
			finalizers = append(finalizers[:i], finalizers[i+1:]...)
			obj.SetFinalizers(finalizers)
			return
		}
	}
}

// setControllerReference sets the controller reference on an owned object.
func setControllerReference(owner, obj metav1.Object,
	scheme *runtime.Scheme) error {
	gvk, err := apiVersionAndKindForObject(owner, scheme)
	if err != nil {
		return err
	}

	// Set owner reference
	ownerRef := metav1.OwnerReference{
		APIVersion:         gvk.GroupVersion().String(),
		Kind:               gvk.Kind,
		Name:               owner.GetName(),
		UID:                owner.GetUID(),
		Controller:         boolPtr(true),
		BlockOwnerDeletion: boolPtr(true),
	}

	ownerRefs := obj.GetOwnerReferences()
	// Replace existing owner reference or append
	found := false
	for i, ref := range ownerRefs {
		if ref.UID == ownerRef.UID {
			ownerRefs[i] = ownerRef
			found = true
			break
		}
	}
	if !found {
		ownerRefs = append(ownerRefs, ownerRef)
	}

	obj.SetOwnerReferences(ownerRefs)
	return nil
}

// apiVersionAndKindForObject returns the API version and kind for an object.
func apiVersionAndKindForObject(obj metav1.Object,
	scheme *runtime.Scheme) (schema.GroupVersionKind, error) {
	objVal := reflect.ValueOf(obj)
	if objVal.Kind() == reflect.Ptr {
		objVal = objVal.Elem()
	}
	objType := objVal.Type()

	for gvk, t := range scheme.AllKnownTypes() {
		if t == objType {
			return gvk, nil
		}
	}

	return schema.GroupVersionKind{}, fmt.Errorf("could not determine GVK for %T",
		obj)
}

// boolPtr returns a pointer to the given bool.
func boolPtr(b bool) *bool {
	return &b
}

// SetupWithManager sets up the controller with the Manager.
func (r *AgentPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.AgentPool{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Named("agentpool").
		Complete(r)
}
