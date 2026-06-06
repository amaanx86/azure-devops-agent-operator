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
	"maps"
	"math/rand"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	agentsv1alpha1 "github.com/amaanx86/azure-devops-agent-operator/api/v1alpha1"
	"github.com/amaanx86/azure-devops-agent-operator/internal/azuredevops"
)

const (
	finalizerName     = "agents.amaanx86.github.io/cleanup"
	reconcileInterval = 30 * time.Second

	// defaultAgentImage is used when spec.agentImage is unset.
	defaultAgentImage = "mcr.microsoft.com/azure-pipelines/vsts-agent:latest"
)

// adoClientFace abstracts Azure DevOps API calls to allow test injection.
type adoClientFace interface {
	GetPoolID(ctx context.Context, orgURL, poolName string) (int, error)
	GetJobStatus(ctx context.Context, orgURL string, poolID int) (azuredevops.JobStatus, error)
	RegisterDummyAgent(ctx context.Context, orgURL string, poolID int, name string) (int, error)
	UnregisterAgent(ctx context.Context, orgURL string, poolID, agentID int) error
	GetAgentByName(ctx context.Context, orgURL string, poolID int, name string) (int, error)
}

// AgentPoolReconciler reconciles a AgentPool object.
type AgentPoolReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// newADOClient overrides the default ADO client constructor. Used in tests.
	newADOClient func(pat string) adoClientFace
}

// +kubebuilder:rbac:groups=agents.amaanx86.github.io,resources=agentpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agents.amaanx86.github.io,resources=agentpools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agents.amaanx86.github.io,resources=agentpools/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is the main control loop. It is level-triggered and idempotent:
// each invocation computes desired state from scratch and corrects any drift.
func (r *AgentPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	pool := &agentsv1alpha1.AgentPool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !pool.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, pool)
	}

	if !controllerutil.ContainsFinalizer(pool, finalizerName) {
		controllerutil.AddFinalizer(pool, finalizerName)
		if err := r.Update(ctx, pool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	pat, ok := r.fetchPAT(ctx, pool)
	if !ok {
		return ctrl.Result{RequeueAfter: reconcileInterval}, nil
	}

	adoClient := r.buildADOClient(pat)

	if pool.Status.PoolID == 0 {
		if resolved := r.resolvePoolID(ctx, pool, adoClient); !resolved {
			return ctrl.Result{RequeueAfter: reconcileInterval}, nil
		}
	}

	if err := r.ensurePVCPool(ctx, pool); err != nil {
		log.Error(err, "Failed to ensure PVC pool; continuing without cache volumes")
		recordReconcileError(pool.Name, req.Namespace, "PVCPoolFailed")
	}

	jobStatus, err := adoClient.GetJobStatus(ctx, pool.Spec.OrganizationURL, int(pool.Status.PoolID))
	if err != nil {
		log.Error(err, "Failed to query ADO job status")
		r.setCondition(pool, "Available", metav1.ConditionFalse, "JobStatusQueryFailed",
			fmt.Sprintf("ADO API error: %v", err))
		_ = r.Status().Update(ctx, pool)
		recordReconcileError(pool.Name, req.Namespace, "ADOQueryFailed")
		return ctrl.Result{RequeueAfter: reconcileInterval}, nil
	}

	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(req.Namespace),
		client.MatchingLabels{labelPoolName: pool.Name}); err != nil {
		return ctrl.Result{}, err
	}

	// Release PVCs and delete completed (Succeeded/Failed) pods.
	if err := r.cleanupCompletedPods(ctx, pool, podList.Items); err != nil {
		log.Error(err, "Failed to cleanup completed pods")
	}

	activeCount, activePods := countActivePods(podList.Items)

	desired := clamp(int32(jobStatus.Pending), pool.Spec.MinAgents, pool.Spec.MaxAgents)

	// Scale up.
	toCreate := int(desired) - activeCount
	created := 0
	for range toCreate {
		if err := r.createAgentPod(ctx, pool); err != nil {
			log.Error(err, "Failed to create agent pod")
			recordReconcileError(pool.Name, req.Namespace, "PodCreateFailed")
			break
		}
		created++
	}
	activeCount += created

	// Scale down: only delete idle pods (not actively running a job in ADO).
	toDelete := activeCount - int(desired)
	if toDelete > 0 {
		deleted := r.scaleDown(ctx, pool, activePods, toDelete, jobStatus.BusyAgentNames)
		activeCount -= deleted
	}

	if err := r.manageDummyAgent(ctx, pool, adoClient, desired); err != nil {
		log.Error(err, "Failed to manage dummy agent")
		recordReconcileError(pool.Name, req.Namespace, "DummyAgentFailed")
	}

	freePVCSlots, _ := r.countFreePVCSlots(ctx, pool)

	activeAgentsVal := int32(activeCount)
	pendingJobsVal := int32(jobStatus.Pending)
	pool.Status.ActiveAgents = &activeAgentsVal
	pool.Status.PendingJobs = &pendingJobsVal
	r.setCondition(pool, "Available", metav1.ConditionTrue, "Reconciled",
		fmt.Sprintf("%d agent(s) active, %d job(s) pending", activeCount, jobStatus.Pending))

	if err := r.Status().Update(ctx, pool); err != nil {
		log.Error(err, "Failed to update AgentPool status")
		return ctrl.Result{}, err
	}

	recordMetrics(pool.Name, req.Namespace, activeCount, jobStatus.Pending, freePVCSlots)

	return ctrl.Result{RequeueAfter: reconcileInterval}, nil
}

// handleDeletion runs cleanup when the AgentPool is being deleted.
func (r *AgentPoolReconciler) handleDeletion(ctx context.Context, pool *agentsv1alpha1.AgentPool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(pool, finalizerName) {
		return ctrl.Result{}, nil
	}

	if pool.Status.DummyAgentID != 0 {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: pool.Namespace,
			Name:      pool.Spec.TokenSecretRef.Name,
		}, secret); err == nil {
			if patBytes, ok := secret.Data[pool.Spec.TokenSecretRef.Key]; ok {
				adoClient := r.buildADOClient(string(patBytes))
				if err := adoClient.UnregisterAgent(ctx,
					pool.Spec.OrganizationURL,
					int(pool.Status.PoolID),
					int(pool.Status.DummyAgentID)); err != nil {
					log.Error(err, "Failed to unregister dummy agent during deletion")
				} else {
					log.Info("Unregistered dummy agent during deletion")
				}
			}
		}
	}

	controllerutil.RemoveFinalizer(pool, finalizerName)
	if err := r.Update(ctx, pool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// fetchPAT retrieves the ADO PAT from the referenced Secret.
// Returns the token and true on success; sets a Degraded condition and returns false on failure.
func (r *AgentPoolReconciler) fetchPAT(ctx context.Context, pool *agentsv1alpha1.AgentPool) (string, bool) {
	log := logf.FromContext(ctx)

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: pool.Namespace,
		Name:      pool.Spec.TokenSecretRef.Name,
	}, secret); err != nil {
		log.Error(err, "Failed to fetch token secret")
		r.setCondition(pool, "Available", metav1.ConditionFalse, "SecretNotFound",
			fmt.Sprintf("Could not read secret %q: %v", pool.Spec.TokenSecretRef.Name, err))
		_ = r.Status().Update(ctx, pool)
		return "", false
	}

	patBytes, ok := secret.Data[pool.Spec.TokenSecretRef.Key]
	if !ok {
		log.Error(nil, "Token secret missing key", "key", pool.Spec.TokenSecretRef.Key)
		r.setCondition(pool, "Available", metav1.ConditionFalse, "SecretKeyMissing",
			fmt.Sprintf("Key %q not found in secret %q", pool.Spec.TokenSecretRef.Key, pool.Spec.TokenSecretRef.Name))
		_ = r.Status().Update(ctx, pool)
		return "", false
	}

	return string(patBytes), true
}

// resolvePoolID resolves and caches the ADO pool ID in status.
// Returns true if the pool ID is now set (either already was, or just resolved).
func (r *AgentPoolReconciler) resolvePoolID(
	ctx context.Context,
	pool *agentsv1alpha1.AgentPool,
	adoClient adoClientFace,
) bool {
	log := logf.FromContext(ctx)

	poolID, err := adoClient.GetPoolID(ctx, pool.Spec.OrganizationURL, pool.Spec.PoolName)
	if err != nil {
		log.Error(err, "Failed to resolve ADO pool ID")
		r.setCondition(pool, "Available", metav1.ConditionFalse, "PoolResolutionFailed",
			fmt.Sprintf("Could not find ADO pool %q: %v", pool.Spec.PoolName, err))
		_ = r.Status().Update(ctx, pool)
		return false
	}

	pool.Status.PoolID = int32(poolID)
	if err := r.Status().Update(ctx, pool); err != nil {
		log.Error(err, "Failed to cache pool ID in status")
		return false
	}
	return true
}

// cleanupCompletedPods releases PVCs and deletes Succeeded/Failed pods.
func (r *AgentPoolReconciler) cleanupCompletedPods(
	ctx context.Context,
	pool *agentsv1alpha1.AgentPool,
	pods []corev1.Pod,
) error {
	log := logf.FromContext(ctx)

	var firstErr error
	for i := range pods {
		pod := &pods[i]
		if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			continue
		}

		if len(pool.Spec.CacheVolumes) > 0 {
			if err := r.releasePVCsForPod(ctx, pool, pod.Name); err != nil {
				log.Error(err, "Failed to release PVCs for completed pod", "pod", pod.Name)
				if firstErr == nil {
					firstErr = err
				}
			}
		}

		if err := r.Delete(ctx, pod); err != nil {
			if client.IgnoreNotFound(err) != nil {
				log.Error(err, "Failed to delete completed pod", "pod", pod.Name)
				if firstErr == nil {
					firstErr = err
				}
			}
		} else {
			log.Info("Deleted completed pod", "pod", pod.Name, "phase", pod.Status.Phase)
			r.Recorder.Event(pool, corev1.EventTypeNormal, "PodCleaned",
				fmt.Sprintf("Deleted completed pod %s (%s)", pod.Name, pod.Status.Phase))
		}
	}
	return firstErr
}

// createAgentPod creates one agent pod, assigning PVC slots if cache is configured.
func (r *AgentPoolReconciler) createAgentPod(ctx context.Context, pool *agentsv1alpha1.AgentPool) error {
	log := logf.FromContext(ctx)

	podName := fmt.Sprintf("%s-%s", truncateName(pool.Name, 40), randomString(6))

	assignedPVCs := map[string]string{}
	if len(pool.Spec.CacheVolumes) > 0 {
		var err error
		assignedPVCs, err = r.findAndAssignPVCs(ctx, pool, podName)
		if err != nil {
			return fmt.Errorf("assign PVCs: %w", err)
		}
	}

	pod := r.buildAgentPod(pool, podName, assignedPVCs)
	if err := ctrl.SetControllerReference(pool, pod, r.Scheme); err != nil {
		r.rollbackAssignedPVCs(ctx, pool.Namespace, assignedPVCs)
		return fmt.Errorf("set controller reference: %w", err)
	}

	if err := r.Create(ctx, pod); err != nil {
		r.rollbackAssignedPVCs(ctx, pool.Namespace, assignedPVCs)
		return fmt.Errorf("create pod: %w", err)
	}

	log.Info("Created agent pod", "pod", podName)
	r.Recorder.Event(pool, corev1.EventTypeNormal, "PodCreated",
		fmt.Sprintf("Created agent pod %s", podName))
	return nil
}

// buildAgentPod constructs the Pod spec for an agent.
func (r *AgentPoolReconciler) buildAgentPod(
	pool *agentsv1alpha1.AgentPool,
	podName string,
	assignedPVCs map[string]string,
) *corev1.Pod {
	image := pool.Spec.AgentImage
	if image == "" {
		image = defaultAgentImage
	}

	env := make([]corev1.EnvVar, 0, 5+len(pool.Spec.ExtraEnv))
	env = append(env,
		corev1.EnvVar{Name: "AZP_URL", Value: pool.Spec.OrganizationURL},
		corev1.EnvVar{Name: "AZP_POOL", Value: pool.Spec.PoolName},
		corev1.EnvVar{Name: "AZP_AGENT_NAME", Value: podName},
		corev1.EnvVar{
			Name: "AZP_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &pool.Spec.TokenSecretRef,
			},
		},
	)
	env = append(env, pool.Spec.ExtraEnv...)

	volumes := make([]corev1.Volume, 0, len(pool.Spec.CacheVolumes))
	volumeMounts := make([]corev1.VolumeMount, 0, len(pool.Spec.CacheVolumes))
	for _, cv := range pool.Spec.CacheVolumes {
		pvcName, ok := assignedPVCs[cv.Name]
		if !ok {
			continue
		}
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

	// Merge user-supplied labels on top of operator labels.
	labels := map[string]string{
		"app.kubernetes.io/name":       "azure-devops-agent",
		"app.kubernetes.io/managed-by": "agentpool-controller",
		"app.kubernetes.io/instance":   pool.Name,
		labelPoolName:                  pool.Name,
	}
	maps.Copy(labels, pool.Spec.PodLabels)

	annotations := make(map[string]string, len(pool.Spec.PodAnnotations))
	maps.Copy(annotations, pool.Spec.PodAnnotations)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   pool.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: pool.Spec.ServiceAccountName,
			NodeSelector:       pool.Spec.NodeSelector,
			Tolerations:        pool.Spec.Tolerations,
			Affinity:           pool.Spec.Affinity,
			ImagePullSecrets:   pool.Spec.ImagePullSecrets,
			SecurityContext:    pool.Spec.PodSecurityContext,
			InitContainers:     pool.Spec.InitContainers,
			Containers: []corev1.Container{
				{
					Name:            "agent",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					// --once: exit after completing one job. This is the AZP flag that
					// makes the pod lifecycle match the job lifecycle, enabling safe
					// pod recycling and warm-cache reuse via the PVC pool.
					Args:         []string{"--once"},
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

// scaleDown deletes up to count idle Running/Pending pods.
// Pods whose agent names appear in busyAgentNames (currently executing a job
// per ADO) are skipped to avoid interrupting in-flight work.
// Returns the number of pods actually deleted.
func (r *AgentPoolReconciler) scaleDown(
	ctx context.Context,
	pool *agentsv1alpha1.AgentPool,
	activePods []corev1.Pod,
	count int,
	busyAgentNames map[string]bool,
) int {
	log := logf.FromContext(ctx)
	deleted := 0

	for i := range activePods {
		if deleted >= count {
			break
		}
		pod := &activePods[i]
		agentName := pod.Name
		// AZP_AGENT_NAME is set to pod.Name, so agent names match pod names.
		if busyAgentNames[agentName] {
			log.Info("Skipping pod scale-down: agent is executing a job", "pod", pod.Name)
			continue
		}

		if err := r.Delete(ctx, pod); err != nil {
			if client.IgnoreNotFound(err) != nil {
				log.Error(err, "Failed to delete idle agent pod", "pod", pod.Name)
			}
			continue
		}

		log.Info("Deleted idle agent pod during scale-down", "pod", pod.Name)
		r.Recorder.Event(pool, corev1.EventTypeNormal, "PodScaledDown",
			fmt.Sprintf("Deleted idle agent pod %s", pod.Name))
		deleted++
	}

	return deleted
}

// manageDummyAgent registers an offline placeholder agent when desired == 0
// (so ADO queues jobs rather than failing them) and unregisters it when real
// agents are coming up.
func (r *AgentPoolReconciler) manageDummyAgent(
	ctx context.Context,
	pool *agentsv1alpha1.AgentPool,
	adoClient adoClientFace,
	desired int32,
) error {
	log := logf.FromContext(ctx)
	dummyName := fmt.Sprintf("%s-dummy", pool.Name)

	if desired == 0 && pool.Status.DummyAgentID == 0 {
		// Check if a dummy already exists from a previous controller run that lost its state.
		id, err := adoClient.GetAgentByName(ctx, pool.Spec.OrganizationURL, int(pool.Status.PoolID), dummyName)
		if err != nil {
			log.Error(err, "Failed to look up existing dummy agent")
		} else if id > 0 {
			log.Info("Recovered existing dummy agent from ADO", "agentID", id)
			pool.Status.DummyAgentID = int32(id)
			return nil
		}

		id, err = adoClient.RegisterDummyAgent(ctx, pool.Spec.OrganizationURL, int(pool.Status.PoolID), dummyName)
		if err != nil {
			return fmt.Errorf("register dummy agent: %w", err)
		}
		pool.Status.DummyAgentID = int32(id)
		log.Info("Registered dummy agent", "agentID", id)
		r.Recorder.Event(pool, corev1.EventTypeNormal, "DummyAgentRegistered",
			fmt.Sprintf("Registered dummy agent %s (ID: %d) for scale-to-zero", dummyName, id))
	}

	if desired > 0 && pool.Status.DummyAgentID != 0 {
		if err := adoClient.UnregisterAgent(ctx,
			pool.Spec.OrganizationURL,
			int(pool.Status.PoolID),
			int(pool.Status.DummyAgentID)); err != nil {
			return fmt.Errorf("unregister dummy agent: %w", err)
		}
		pool.Status.DummyAgentID = 0
		log.Info("Unregistered dummy agent; real agents taking over")
		r.Recorder.Event(pool, corev1.EventTypeNormal, "DummyAgentUnregistered",
			"Unregistered dummy agent; real agents are now active")
	}

	return nil
}

// setCondition is a convenience wrapper around meta.SetStatusCondition.
//
//nolint:unparam
func (r *AgentPoolReconciler) setCondition(
	pool *agentsv1alpha1.AgentPool,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: pool.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// buildADOClient returns the ADO client, using the injected factory if set.
func (r *AgentPoolReconciler) buildADOClient(pat string) adoClientFace {
	if r.newADOClient != nil {
		return r.newADOClient(pat)
	}
	return azuredevops.NewClient(pat)
}

// SetupWithManager registers the controller with the Manager.
func (r *AgentPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentsv1alpha1.AgentPool{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Named("agentpool").
		Complete(r)
}

// countActivePods returns the count and slice of Running/Pending pods.
func countActivePods(pods []corev1.Pod) (int, []corev1.Pod) {
	var active []corev1.Pod
	for i := range pods {
		if pods[i].Status.Phase == corev1.PodRunning || pods[i].Status.Phase == corev1.PodPending {
			active = append(active, pods[i])
		}
	}
	return len(active), active
}

// clamp constrains val to [minVal, maxVal].
func clamp(val, minVal, maxVal int32) int32 {
	if val < minVal {
		return minVal
	}
	if val > maxVal {
		return maxVal
	}
	return val
}

// randomString returns a random lowercase alphanumeric string of length n.
// Uses math/rand which is auto-seeded since Go 1.20.
func randomString(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// truncateName truncates s to at most maxLen runes, preserving valid DNS characters.
func truncateName(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
