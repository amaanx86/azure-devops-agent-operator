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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentsv1alpha1 "github.com/amaanx86/azure-devops-agent-operator/api/v1alpha1"
)

const (
	labelPoolName    = "agentpool"
	labelCacheName   = "azp-cache-name"
	labelCacheSlot   = "azp-cache-slot"
	labelPoolState   = "azp-pool-state"
	labelAssignedPod = "azp-assigned-pod"

	pvcStateFree     = "free"
	pvcStateAssigned = "assigned"
)

// pvcSlotName returns a deterministic, DNS-safe PVC name for a given slot.
// Names are truncated to stay well within the 253-char Kubernetes limit.
func pvcSlotName(poolName, cacheName string, slot int) string {
	p := poolName
	if len(p) > 40 {
		p = p[:40]
	}
	c := cacheName
	if len(c) > 20 {
		c = c[:20]
	}
	return fmt.Sprintf("%s-cache-%s-%02d", p, c, slot)
}

// ensurePVCPool ensures that MaxAgents PVCs exist per CacheVolumeTemplate.
// PVCs are labeled with pool metadata and initial state "free".
// Pre-provisioning at MaxAgents means scale-up never waits for PVC binding.
func (r *AgentPoolReconciler) ensurePVCPool(ctx context.Context, pool *agentsv1alpha1.AgentPool) error {
	if len(pool.Spec.CacheVolumes) == 0 {
		return nil
	}

	for _, cv := range pool.Spec.CacheVolumes {
		for slot := 0; slot < int(pool.Spec.MaxAgents); slot++ {
			name := pvcSlotName(pool.Name, cv.Name, slot)

			existing := &corev1.PersistentVolumeClaim{}
			err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: pool.Namespace}, existing)
			if err == nil {
				continue
			}
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("check PVC %s: %w", name, err)
			}

			newPVC := r.buildPVC(pool, cv, name, slot)
			if err := ctrl.SetControllerReference(pool, newPVC, r.Scheme); err != nil {
				return fmt.Errorf("set PVC controller reference: %w", err)
			}

			if err := r.Create(ctx, newPVC); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("create PVC %s: %w", name, err)
			}
		}
	}
	return nil
}

func (r *AgentPoolReconciler) buildPVC(
	pool *agentsv1alpha1.AgentPool,
	cv agentsv1alpha1.CacheVolumeTemplate,
	name string,
	slot int,
) *corev1.PersistentVolumeClaim {
	// ReadWriteOncePod (GA in Kubernetes 1.29) ensures exclusive single-pod mounting.
	// This prevents accidental sharing if two pods somehow reference the same PVC.
	accessMode := corev1.ReadWriteOncePod

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: pool.Namespace,
			Labels: map[string]string{
				labelPoolName:  pool.Name,
				labelCacheName: cv.Name,
				labelCacheSlot: fmt.Sprintf("%d", slot),
				labelPoolState: pvcStateFree,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: cv.Size,
				},
			},
			StorageClassName: cv.StorageClassName,
		},
	}
}

// findAndAssignPVCs atomically claims one free PVC per CacheVolumeTemplate for the
// given pod. Returns a map of template-name -> PVC-name.
//
// If any template has no free PVC, all previously claimed PVCs in this call are
// released and an error is returned (pool is at capacity for that template).
func (r *AgentPoolReconciler) findAndAssignPVCs(
	ctx context.Context,
	pool *agentsv1alpha1.AgentPool,
	podName string,
) (map[string]string, error) {
	if len(pool.Spec.CacheVolumes) == 0 {
		return nil, nil
	}

	assigned := make(map[string]string, len(pool.Spec.CacheVolumes))

	for _, cv := range pool.Spec.CacheVolumes {
		pvcList := &corev1.PersistentVolumeClaimList{}
		if err := r.List(ctx, pvcList,
			client.InNamespace(pool.Namespace),
			client.MatchingLabels{
				labelPoolName:  pool.Name,
				labelCacheName: cv.Name,
				labelPoolState: pvcStateFree,
			}); err != nil {
			r.rollbackAssignedPVCs(ctx, pool.Namespace, assigned)
			return nil, fmt.Errorf("list free PVCs for cache %q: %w", cv.Name, err)
		}

		if len(pvcList.Items) == 0 {
			r.rollbackAssignedPVCs(ctx, pool.Namespace, assigned)
			return nil, fmt.Errorf("no free PVC for cache %q: pool at capacity", cv.Name)
		}

		pvc := &pvcList.Items[0]
		patch := client.MergeFrom(pvc.DeepCopy())
		pvc.Labels[labelPoolState] = pvcStateAssigned
		pvc.Labels[labelAssignedPod] = podName

		if err := r.Patch(ctx, pvc, patch); err != nil {
			r.rollbackAssignedPVCs(ctx, pool.Namespace, assigned)
			return nil, fmt.Errorf("assign PVC %s to pod %s: %w", pvc.Name, podName, err)
		}

		assigned[cv.Name] = pvc.Name
	}

	return assigned, nil
}

// releasePVCsForPod releases all PVCs currently assigned to podName back to the free pool.
func (r *AgentPoolReconciler) releasePVCsForPod(
	ctx context.Context,
	pool *agentsv1alpha1.AgentPool,
	podName string,
) error {
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcList,
		client.InNamespace(pool.Namespace),
		client.MatchingLabels{
			labelPoolName:    pool.Name,
			labelAssignedPod: podName,
		}); err != nil {
		return fmt.Errorf("list PVCs for pod %s: %w", podName, err)
	}

	var firstErr error
	for i := range pvcList.Items {
		if err := r.releasePVC(ctx, &pvcList.Items[i]); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *AgentPoolReconciler) releasePVC(ctx context.Context, pvc *corev1.PersistentVolumeClaim) error {
	patch := client.MergeFrom(pvc.DeepCopy())
	pvc.Labels[labelPoolState] = pvcStateFree
	delete(pvc.Labels, labelAssignedPod)
	return r.Patch(ctx, pvc, patch)
}

// countFreePVCSlots returns how many more agent pods could be started based on
// available PVC slots (minimum free count across all cache templates).
// Returns MaxAgents when no cache volumes are configured.
func (r *AgentPoolReconciler) countFreePVCSlots(
	ctx context.Context,
	pool *agentsv1alpha1.AgentPool,
) (int, error) {
	if len(pool.Spec.CacheVolumes) == 0 {
		return int(pool.Spec.MaxAgents), nil
	}

	minFree := int(pool.Spec.MaxAgents)
	for _, cv := range pool.Spec.CacheVolumes {
		pvcList := &corev1.PersistentVolumeClaimList{}
		if err := r.List(ctx, pvcList,
			client.InNamespace(pool.Namespace),
			client.MatchingLabels{
				labelPoolName:  pool.Name,
				labelCacheName: cv.Name,
				labelPoolState: pvcStateFree,
			}); err != nil {
			return 0, fmt.Errorf("list free PVCs for %q: %w", cv.Name, err)
		}
		if len(pvcList.Items) < minFree {
			minFree = len(pvcList.Items)
		}
	}
	return minFree, nil
}

// rollbackAssignedPVCs releases already-assigned PVCs when a multi-template
// assignment fails partway through. Errors are swallowed; next reconcile heals.
func (r *AgentPoolReconciler) rollbackAssignedPVCs(
	ctx context.Context,
	namespace string,
	assigned map[string]string,
) {
	for _, pvcName := range assigned {
		pvc := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: namespace}, pvc); err != nil {
			continue
		}
		_ = r.releasePVC(ctx, pvc)
	}
}
