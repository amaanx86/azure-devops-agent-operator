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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentPoolSpec defines the desired state of AgentPool.
type AgentPoolSpec struct {
	// organizationURL is the Azure DevOps organization URL.
	// Example: https://dev.azure.com/myorg
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	OrganizationURL string `json:"organizationURL"`

	// tokenSecretRef references the Kubernetes Secret containing the ADO PAT.
	//
	// +kubebuilder:validation:Required
	TokenSecretRef corev1.SecretKeySelector `json:"tokenSecretRef"`

	// poolName is the name of the Azure DevOps agent pool.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	PoolName string `json:"poolName"`

	// agentImage is the container image URI for agent Pods.
	// Defaults to mcr.microsoft.com/azure-pipelines/vsts-agent:latest if omitted.
	//
	// +optional
	AgentImage string `json:"agentImage,omitempty"`

	// minAgents is the minimum number of agent Pods that should be running.
	// Set to 0 to enable true scale-to-zero (requires a dummy agent registered with ADO).
	//
	// +optional
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	MinAgents int32 `json:"minAgents,omitempty"`

	// maxAgents is the maximum number of concurrent agent Pods.
	// Also controls the PVC pool size when cacheVolumes are configured.
	//
	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	MaxAgents int32 `json:"maxAgents,omitempty"`

	// agentResources specifies CPU and memory requests/limits for each agent Pod.
	//
	// +optional
	AgentResources corev1.ResourceRequirements `json:"agentResources,omitempty"`

	// extraEnv contains additional environment variables injected into agent Pods.
	// These are appended after the standard AZP_* variables.
	//
	// +optional
	ExtraEnv []corev1.EnvVar `json:"extraEnv,omitempty"`

	// cacheVolumes defines PVC templates for per-agent build cache volumes.
	// The operator pre-provisions MaxAgents PVCs per template and assigns them
	// exclusively to individual agent pods. When a pod completes, its PVCs are
	// returned to the pool so the next pod starts with a warm cache.
	//
	// +optional
	CacheVolumes []CacheVolumeTemplate `json:"cacheVolumes,omitempty"`

	// nodeSelector constrains which nodes agent Pods may be scheduled on.
	//
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// tolerations are applied to agent Pods to allow scheduling on tainted nodes.
	//
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// affinity provides advanced scheduling constraints for agent Pods.
	//
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// imagePullSecrets are optional references to Secrets in the same namespace
	// for pulling the agentImage from a private registry.
	//
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// serviceAccountName is the ServiceAccount to assign to agent Pods.
	// Defaults to the namespace default ServiceAccount when unset.
	//
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// podAnnotations are additional annotations merged onto agent Pods.
	// Annotations whose keys begin with "azp-" are reserved by the operator.
	//
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// podLabels are additional labels merged onto agent Pods.
	// Labels whose keys begin with "azp-" are reserved by the operator.
	//
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// podSecurityContext defines the PodSecurityContext for agent Pods.
	//
	// +optional
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`

	// initContainers are additional init containers prepended to agent Pods.
	// Useful for pre-warming toolchains or populating the cache volume.
	//
	// +optional
	InitContainers []corev1.Container `json:"initContainers,omitempty"`
}

// CacheVolumeTemplate describes a PVC template for exclusive per-agent cache binding.
type CacheVolumeTemplate struct {
	// name is the volume name used in the Pod's volumeMounts.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// mountPath is the path inside the agent container where the volume is mounted.
	//
	// +kubebuilder:validation:Required
	MountPath string `json:"mountPath"`

	// storageClassName is the name of the StorageClass.
	// If omitted, the default StorageClass is used.
	//
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// size is the requested storage capacity (e.g., "50Gi").
	//
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`
}

// AgentPoolStatus defines the observed state of AgentPool.
type AgentPoolStatus struct {
	// conditions represent the latest observed state of the AgentPool.
	//
	// Known types:
	//   "Available"   - pool is functioning and scaling correctly
	//   "Progressing" - a reconcile operation is in flight
	//   "Degraded"    - reconciliation failed; check the message
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// activeAgents is the count of agent Pods in Running or Pending phase.
	//
	// +optional
	ActiveAgents *int32 `json:"activeAgents,omitempty"`

	// pendingJobs is the count of unfinished jobs in the ADO queue.
	// Updated on each reconciliation poll.
	//
	// +optional
	PendingJobs *int32 `json:"pendingJobs,omitempty"`

	// poolID is the resolved numeric ID of the ADO agent pool.
	// Cached to avoid repeated ADO lookups; 0 means not yet resolved.
	//
	// +optional
	PoolID int32 `json:"poolID,omitempty"`

	// dummyAgentID is the ADO agent ID of the registered offline placeholder agent.
	// Non-zero only when activeAgents == 0 and scale-to-zero is in effect.
	//
	// +optional
	DummyAgentID int32 `json:"dummyAgentID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ap
// +kubebuilder:printcolumn:name="Pool",type=string,JSONPath=`.spec.poolName`
// +kubebuilder:printcolumn:name="Active",type=integer,JSONPath=`.status.activeAgents`
// +kubebuilder:printcolumn:name="Pending",type=integer,JSONPath=`.status.pendingJobs`
// +kubebuilder:printcolumn:name="Available",type=string,JSONPath=`.status.conditions[?(@.type=="Available")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentPool is the Schema for the agentpools API.
type AgentPool struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AgentPoolSpec `json:"spec"`

	// +optional
	Status AgentPoolStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AgentPoolList contains a list of AgentPool.
type AgentPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AgentPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentPool{}, &AgentPoolList{})
}
