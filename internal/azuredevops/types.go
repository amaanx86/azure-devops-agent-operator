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

package azuredevops

// PoolList is the top-level response from the ADO pools list endpoint.
type PoolList struct {
	Count int    `json:"count"`
	Value []Pool `json:"value"`
}

// Pool represents a single Azure DevOps agent pool.
type Pool struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// JobRequestList is the response from the ADO job requests endpoint.
type JobRequestList struct {
	Count int          `json:"count"`
	Value []JobRequest `json:"value"`
}

// JobRequest represents a single pipeline job in the ADO queue.
// We unmarshal only the fields we need.
type JobRequest struct {
	// RequestID is the unique identifier for this job.
	RequestID int64 `json:"requestId"`

	// QueueTime is the timestamp when the job was queued (ISO8601).
	QueueTime string `json:"queueTime"`

	// AssignTime is the timestamp when the job was assigned to an agent (ISO8601).
	// nil if not yet assigned.
	AssignTime *string `json:"assignTime"`

	// FinishTime is the timestamp when the job completed (ISO8601).
	// nil if the job is still pending or running.
	FinishTime *string `json:"finishTime"`

	// ReservedAgent is set when a real agent has picked up the job.
	// nil if the job is still in queue or assigned but not yet picked up.
	ReservedAgent *AgentRef `json:"reservedAgent"`
}

// AgentRef is a lightweight reference to an agent embedded in a job request.
type AgentRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// RegisterAgentRequest is the request body for registering a new agent with ADO.
type RegisterAgentRequest struct {
	// Name is the agent name.
	Name string `json:"name"`

	// Version is the agent version.
	Version string `json:"version"`

	// OSDescription describes the operating system.
	OSDescription string `json:"osDescription"`

	// Enabled indicates if the agent is enabled.
	// Set to false for dummy agents.
	Enabled bool `json:"enabled"`

	// ProvisioningState indicates the agent's provisioning state.
	// Set to "Unavailable" for dummy/offline agents.
	ProvisioningState string `json:"provisioningState"`

	// SystemCapabilities are key-value labels the ADO platform uses for job routing
	// and demands matching.
	SystemCapabilities map[string]string `json:"systemCapabilities"`
}

// RegisterAgentResponse is the response from posting to the agents endpoint.
type RegisterAgentResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
