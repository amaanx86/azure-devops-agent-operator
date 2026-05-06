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

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a minimal ADO REST API client for agent pool operations.
// It is safe for concurrent use.
type Client struct {
	httpClient       *http.Client
	authorizationHdr string
}

// NewClient constructs a Client with HTTP Basic auth for the given PAT.
// pat is the raw Personal Access Token string.
func NewClient(pat string) *Client {
	// ADO uses HTTP Basic auth with empty username: base64(":" + pat)
	authValue := base64.StdEncoding.EncodeToString([]byte(":" + pat))
	authHeader := "Basic " + authValue

	return &Client{
		httpClient:       &http.Client{Timeout: 30 * time.Second},
		authorizationHdr: authHeader,
	}
}

// GetPoolID queries ADO to find the numeric ID of the named agent pool.
// Returns 0 and an error if not found or if the API call fails.
func (c *Client) GetPoolID(ctx context.Context, orgURL,
	poolName string) (int, error) {
	url := fmt.Sprintf(
		"%s/_apis/distributedtask/pools?poolName=%s&api-version=7.1",
		orgURL, poolName)

	var poolList PoolList
	if err := c.getJSON(ctx, url, &poolList); err != nil {
		return 0, err
	}

	if poolList.Count == 0 {
		return 0, fmt.Errorf("agent pool %q not found in ADO", poolName)
	}
	if poolList.Count > 1 {
		return 0, fmt.Errorf("multiple pools named %q found (expected 1)",
			poolName)
	}

	return poolList.Value[0].ID, nil
}

// GetJobCounts returns (pending, running) job counts from the ADO queue.
// A job is "pending" if finishTime == nil (not yet completed).
// A job is "running" if reservedAgent != nil && finishTime == nil.
// Both pending and running count toward scaling decisions.
func (c *Client) GetJobCounts(ctx context.Context, orgURL string,
	poolID int) (pending, running int, err error) {
	url := fmt.Sprintf(
		"%s/_apis/distributedtask/pools/%d/jobrequests?completedRequestCount=0&api-version=7.1",
		orgURL, poolID)

	var jobList JobRequestList
	if err := c.getJSON(ctx, url, &jobList); err != nil {
		return 0, 0, err
	}

	for _, job := range jobList.Value {
		if job.FinishTime == nil {
			pending++
			if job.ReservedAgent != nil {
				running++
			}
		}
	}

	return pending, running, nil
}

// RegisterDummyAgent registers a disabled/offline agent with ADO.
// The dummy agent has enabled=false and provisioningState="Unavailable",
// which signals to ADO that the agent is not ready but the pool is active.
// This enables true scale-to-zero: ADO accepts jobs into the queue knowing
// that agents may come online. When real agents are registered, the dummy is
// unregistered.
func (c *Client) RegisterDummyAgent(ctx context.Context, orgURL string,
	poolID int, name string) (int, error) {
	req := RegisterAgentRequest{
		Name:              name,
		Version:           "1.0.0",
		OSDescription:     "Kubernetes",
		Enabled:           false,
		ProvisioningState: "Unavailable",
		SystemCapabilities: map[string]string{
			"Agent.OS": "Linux",
		},
	}

	url := fmt.Sprintf("%s/_apis/distributedtask/pools/%d/agents?api-version=7.1",
		orgURL, poolID)

	respBody, err := c.postJSON(ctx, url, req)
	if err != nil {
		return 0, err
	}

	var resp RegisterAgentResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return 0, fmt.Errorf("parse ADO agent registration response: %w", err)
	}

	return resp.ID, nil
}

// UnregisterAgent deletes an agent registration from ADO.
func (c *Client) UnregisterAgent(ctx context.Context, orgURL string,
	poolID, agentID int) error {
	url := fmt.Sprintf(
		"%s/_apis/distributedtask/pools/%d/agents/%d?api-version=7.1",
		orgURL, poolID, agentID)

	return c.deleteJSON(ctx, url)
}

// getJSON makes a GET request and unmarshals the JSON response.
// The result type must be passed in as a reference.
func (c *Client) getJSON(ctx context.Context, url string,
	result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", c.authorizationHdr)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ADO API call failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ADO API returned %d: %s", resp.StatusCode,
			string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}

	return nil
}

// postJSON makes a POST request with JSON body and returns the response body.
func (c *Client) postJSON(ctx context.Context, url string,
	body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", c.authorizationHdr)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ADO API call failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ADO API returned %d: %s", resp.StatusCode,
			string(respBody))
	}

	return respBody, nil
}

// deleteJSON makes a DELETE request.
func (c *Client) deleteJSON(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", c.authorizationHdr)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ADO API call failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ADO API returned %d: %s", resp.StatusCode,
			string(body))
	}

	return nil
}
