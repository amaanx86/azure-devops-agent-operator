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

// AgentPoolSpec defines the desired state of AgentPool
type AgentPoolSpec struct {
	// organizationURL is the Azure DevOps organization URL.
	// Example: https://dev.azure.com/myorg
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	OrganizationURL string `json:"organizationURL"`

	// tokenSecretRef references the Kubernetes Secret containing the ADO PAT.
	// The controller reads the value from Secret[tokenSecretRef.name][tokenSecretRef.key].
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
	// Set to 0 to enable true scale-to-zero (dummy agent registered with ADO).
	// Defaults to 0.
	//
	// +optional
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	MinAgents int32 `json:"minAgents,omitempty"`

	// maxAgents is the maximum number of concurrent agent Pods.
	// The controller will not create more agents than this, even if more jobs are pending.
	// Defaults to 10.
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
	// These are appended to the standard AZP_* environment variables.
	//
	// +optional
	ExtraEnv []corev1.EnvVar `json:"extraEnv,omitempty"`

	// cacheVolumes defines PVC templates for per-agent build cache volumes.
	// Each agent Pod gets its own exclusive PVC based on these templates.
	// The PVC is bound to a single Pod for the duration of its execution and
	// released back to the pool when the Pod completes.
	//
	// +optional
	CacheVolumes []CacheVolumeTemplate `json:"cacheVolumes,omitempty"`
}

// CacheVolumeTemplate describes a PVC template for exclusive per-agent cache binding.
type CacheVolumeTemplate struct {
	// name is the volume name used in the Pod's volumeMounts.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// mountPath is the mount path inside the agent container where the volume
	// is mounted.
	//
	// +kubebuilder:validation:Required
	MountPath string `json:"mountPath"`

	// storageClassName is the name of the StorageClass.
	// If omitted, the default StorageClass is used.
	//
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// size is the requested storage capacity (e.g., "50Gi", "100Gi").
	//
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`
}

// AgentPoolStatus defines the observed state of AgentPool.
type AgentPoolStatus struct {
	// conditions represent the current state of the AgentPool resource.
	// Each condition has a unique type and reflects the status of a specific aspect.
	//
	// Standard condition types:
	// - "Available": the pool is functioning and agents are being scaled correctly
	// - "Progressing": currently creating/deleting agents or resolving pool ID
	// - "Degraded": reconciliation failed (e.g., ADO API unreachable, invalid token)
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// activeAgents is the count of agent Pods currently in Running or Pending phase.
	//
	// +optional
	ActiveAgents int32 `json:"activeAgents,omitempty"`

	// pendingJobs is the count of jobs in the ADO queue waiting for an agent.
	// Updated on each reconciliation poll.
	//
	// +optional
	PendingJobs int32 `json:"pendingJobs,omitempty"`

	// poolID is the resolved numeric ID of the ADO agent pool.
	// Cached here to avoid repeated ADO API lookups; set to 0 if not yet resolved.
	//
	// +optional
	PoolID int32 `json:"poolID,omitempty"`

	// dummyAgentID is the agent ID of the registered dummy/offline agent.
	// Set to 0 if no dummy is currently registered.
	// Used to implement true scale-to-zero: when activeAgents==0, a dummy
	// is registered with ADO so the platform accepts jobs into the queue.
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
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentPool is the Schema for the agentpools API
type AgentPool struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AgentPool
	// +required
	Spec AgentPoolSpec `json:"spec"`

	// status defines the observed state of AgentPool
	// +optional
	Status AgentPoolStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AgentPoolList contains a list of AgentPool
type AgentPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AgentPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentPool{}, &AgentPoolList{})
}
