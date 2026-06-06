# Setup Guide

## Prerequisites

- Kubernetes cluster (1.29+)
- `kubectl` configured with cluster access
- Azure DevOps organization account

## Install the Operator

```bash
kubectl apply -f https://github.com/amaanx86/azure-devops-agent-operator/releases/download/latest/install.yaml
```

Verify the controller is running:

```bash
kubectl get deployment -n azure-devops-agent-operator-system
```

## Step 1: Create a Personal Access Token

1. Go to `https://dev.azure.com/{your-org}/_usersettings/tokens`
2. Click **+ New Token**
3. Set expiration (90 days recommended)
4. Under **Scopes**, select **Custom defined**
5. Expand **Agent Pools** and enable **Read & Manage**
6. Click **Create** and copy the token immediately

Required scope summary:

```
Agent Pools > Read & Manage
```

## Step 2: Create the Kubernetes Secret

```bash
kubectl create secret generic ado-token \
  --from-literal=pat='<YOUR_PAT_TOKEN>' \
  -n azure-devops-agent-operator-system
```

The secret key must be `pat`. The `tokenSecretRef.key` field in the AgentPool spec refers to this key name.

## Step 3: Create the Agent Pool in Azure DevOps

1. Go to **Organization settings** > **Agent pools**
2. Click **+ New agent pool**
3. Choose **Self-hosted** and enter the pool name
4. Click **Create**

The `spec.poolName` in your AgentPool resource must match this name exactly.

## Step 4: Deploy an AgentPool Resource

Minimal configuration:

```yaml
apiVersion: agents.amaanx86.github.io/v1alpha1
kind: AgentPool
metadata:
  name: build-agents
  namespace: azure-devops-agent-operator-system
spec:
  organizationURL: "https://dev.azure.com/your-org"
  poolName: "shared-pool"
  tokenSecretRef:
    name: ado-token
    key: pat
  minAgents: 0
  maxAgents: 5
```

Apply it:

```bash
kubectl apply -f agentpool.yaml
```

## Step 5: Verify Reconciliation

Watch the AgentPool status:

```bash
kubectl get agentpool build-agents -n azure-devops-agent-operator-system -w
```

View controller logs:

```bash
kubectl logs -f deploy/azure-devops-agent-operator-controller-manager \
  -n azure-devops-agent-operator-system
```

Watch agent pods:

```bash
kubectl get pods -n azure-devops-agent-operator-system -l agentpool=build-agents
```

Once running, agents appear in the Azure DevOps pool within 30-60 seconds of pod startup.

## Full Spec Reference

```yaml
apiVersion: agents.amaanx86.github.io/v1alpha1
kind: AgentPool
metadata:
  name: build-agents
  namespace: azure-devops-agent-operator-system
spec:
  organizationURL: "https://dev.azure.com/your-org"
  poolName: "shared-pool"
  tokenSecretRef:
    name: ado-token
    key: pat

  minAgents: 0
  maxAgents: 10

  # Custom agent image (defaults to the bundled image)
  # agentImage: "myregistry.azurecr.io/azp-agent:latest"

  agentResources:
    requests:
      cpu: "500m"
      memory: "1Gi"
    limits:
      cpu: "2"
      memory: "4Gi"

  # Pre-provisioned warm cache volumes (one PVC per agent slot, per template)
  cacheVolumes:
    - name: buildcache
      mountPath: /cache
      size: "20Gi"
      # storageClassName: "fast-ssd"

  # Agent placement
  nodeSelector:
    kubernetes.io/os: linux
  tolerations: []
  # affinity: ...

  # Extra environment variables passed to every agent pod
  extraEnv:
    - name: DOCKER_HOST
      value: "tcp://localhost:2376"

  # Init containers run before the agent container
  # initContainers:
  #   - name: setup
  #     image: busybox
  #     command: ["sh", "-c", "echo ready"]

  # Pod metadata
  podLabels:
    team: platform
  podAnnotations:
    prometheus.io/scrape: "false"

  # serviceAccountName: "azp-agent"
  # imagePullSecrets:
  #   - name: registry-credentials

  # podSecurityContext:
  #   runAsNonRoot: true
  #   runAsUser: 1000
```

## Troubleshooting

### "agent pool not found in ADO"

The pool name in `spec.poolName` does not match an existing self-hosted pool in Azure DevOps. Create the pool first, then reapply.

### "authentication failed" or "invalid token"

The PAT is expired or missing the **Agent Pools Read & Manage** scope. Regenerate the token:

```bash
kubectl delete secret ado-token -n azure-devops-agent-operator-system
kubectl create secret generic ado-token \
  --from-literal=pat='<NEW_TOKEN>' \
  -n azure-devops-agent-operator-system
kubectl rollout restart deploy/azure-devops-agent-operator-controller-manager \
  -n azure-devops-agent-operator-system
```

### Agents not appearing in ADO pool

1. Check pods are running: `kubectl get pods -n azure-devops-agent-operator-system`
2. Check pod logs: `kubectl logs <pod-name> -n azure-devops-agent-operator-system`
3. Verify network access to `dev.azure.com` from within the cluster
4. Confirm `maxAgents` is greater than the current number of registered agents
