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
type JobRequest struct {
	RequestID     int64     `json:"requestId"`
	QueueTime     string    `json:"queueTime"`
	AssignTime    *string   `json:"assignTime"`
	FinishTime    *string   `json:"finishTime"`
	ReservedAgent *AgentRef `json:"reservedAgent"`
	Demands       []string  `json:"demands"`
}

// AgentRef is a lightweight reference to an agent embedded in a job request.
type AgentRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// RegisterAgentRequest is the request body for registering a new agent with ADO.
type RegisterAgentRequest struct {
	Name               string            `json:"name"`
	Version            string            `json:"version"`
	OSDescription      string            `json:"osDescription"`
	Enabled            bool              `json:"enabled"`
	ProvisioningState  string            `json:"provisioningState,omitempty"`
	SystemCapabilities map[string]string `json:"systemCapabilities"`
}

// RegisterAgentResponse is the response from posting to the agents endpoint.
type RegisterAgentResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// AgentList is the response from the ADO agents list endpoint.
type AgentList struct {
	Count int     `json:"count"`
	Value []Agent `json:"value"`
}

// Agent represents a registered Azure DevOps agent.
type Agent struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`
}

// JobStatus aggregates queue state for scaling decisions.
type JobStatus struct {
	// Pending is the total number of unfinished jobs (waiting + executing).
	Pending int

	// BusyAgentNames is the set of agent names currently executing a job.
	// Used to avoid killing pods mid-job during scale-down.
	BusyAgentNames map[string]bool
}
