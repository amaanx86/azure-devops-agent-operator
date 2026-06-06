# Usage

## Minimal AgentPool

The minimum required fields are `organizationURL`, `poolName`, `tokenSecretRef`, and `maxAgents`:

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

The operator reconciles the desired state continuously. Changes to spec fields take effect on the next reconcile cycle.

## Scaling Behavior

The operator uses the number of queued jobs in Azure DevOps to determine how many agent pods to run:

- If `pendingJobs > activePods`, scale up (capped at `maxAgents`)
- If `activePods > pendingJobs` and `activePods > minAgents`, scale down
- Scale down skips pods that are currently executing a job

Setting `minAgents: 0` enables scale-to-zero. The operator registers an offline dummy agent when idle so Azure DevOps can queue jobs. Setting `minAgents > 0` keeps a baseline of warm agents always ready.

## Cache Volumes

Cache volumes pre-provision PVCs and bind them exclusively to agent pods for warm cache reuse across job runs:

```yaml
spec:
  cacheVolumes:
    - name: buildcache
      mountPath: /cache
      size: "20Gi"
      # storageClassName: "fast-ssd"
```

Multiple templates are supported. The operator creates `maxAgents` PVCs per template (e.g., `build-agents-cache-buildcache-00` through `build-agents-cache-buildcache-09`).

PVCs are released back to the pool when a pod completes, making the warm cache available to the next pod that picks up that slot.

## Placement

Control which nodes agents run on:

```yaml
spec:
  nodeSelector:
    kubernetes.io/os: linux

  tolerations:
    - key: "dedicated"
      operator: "Equal"
      value: "build"
      effect: "NoSchedule"

  affinity:
    nodeAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        nodeSelectorTerms:
          - matchExpressions:
              - key: node-role
                operator: In
                values: ["build"]
```

## Extra Environment Variables

Pass additional environment variables to every agent pod:

```yaml
spec:
  extraEnv:
    - name: DOCKER_HOST
      value: "tcp://localhost:2376"
    - name: BUILDKIT_PROGRESS
      value: "plain"
```

## Pod Metadata

Attach labels and annotations to agent pods for monitoring or admission webhook targeting:

```yaml
spec:
  podLabels:
    team: platform
    env: production

  podAnnotations:
    prometheus.io/scrape: "false"
    cluster-autoscaler.kubernetes.io/safe-to-evict: "false"
```

## Service Account and Image Pull Secrets

```yaml
spec:
  serviceAccountName: "azp-agent"
  imagePullSecrets:
    - name: registry-credentials
```

## Init Containers

Init containers run before the agent container and share pod volumes:

```yaml
spec:
  initContainers:
    - name: setup
      image: busybox
      command: ["sh", "-c", "echo initializing"]
```

## Checking Status

```bash
# Summary view
kubectl get agentpool -n azure-devops-agent-operator-system

# Detailed conditions
kubectl describe agentpool build-agents -n azure-devops-agent-operator-system

# Agent pods
kubectl get pods -n azure-devops-agent-operator-system -l agentpool=build-agents

# Controller logs
kubectl logs -f deploy/azure-devops-agent-operator-controller-manager \
  -n azure-devops-agent-operator-system
```

The `Available` condition on the AgentPool status reflects whether the last reconcile succeeded.

## Patching Spec Fields

```bash
# Change scale bounds
kubectl patch agentpool build-agents \
  -n azure-devops-agent-operator-system \
  --type merge -p '{"spec":{"minAgents":2,"maxAgents":10}}'
```

The operator reacts to spec changes within the next reconcile interval (default 30 seconds, or immediately on resource change events).
