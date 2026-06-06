# Production Deployment Guide

## Warm Cache Volumes

The Azure Pipelines agent binary (~550 MB) downloads at container startup, which can take several
minutes on a cold pod. The operator solves this with pre-provisioned, exclusively-bound PVC pools.

### How It Works

When `cacheVolumes` is configured, the operator:

1. Pre-provisions `maxAgents` PVCs per cache template at pool creation time, labeled as `free`
2. Assigns one PVC per template exclusively to each new agent pod (labeled `assigned`, pod name recorded)
3. Releases the PVC back to the free pool when the pod completes

Because the PVC is reused across pod restarts, the agent cache directory survives pod replacement.
Subsequent agent pods start in seconds rather than minutes.

Example configuration:

```yaml
spec:
  minAgents: 0
  maxAgents: 10

  cacheVolumes:
    - name: agent-cache
      mountPath: /azp/_work
      size: "20Gi"
      # storageClassName: "fast-ssd"  # omit to use the cluster default

    - name: toolcache
      mountPath: /opt/hostedtoolcache
      size: "10Gi"
```

Each template creates `maxAgents` PVCs named `<pool>-cache-<template>-<slot>` (e.g., `build-agents-cache-agent-cache-00`).

The access mode is `ReadWriteOncePod` (GA in Kubernetes 1.29), which guarantees exclusive
single-pod mounting at the storage layer.

### Sizing Recommendations

| Content | Recommended Size |
| --- | --- |
| Agent binary + work directory | 10-20 Gi |
| Tool cache (Node, Python, Go, etc.) | 10-30 Gi |
| Docker layer cache | 20-50 Gi |
| Combined (single volume) | 30-80 Gi |

Use a storage class backed by SSDs for best job throughput. Set `storageClassName` to select a specific class.

## One-Job-Per-Pod Behavior

Each agent pod runs with `--once` passed to the agent startup script. This causes the agent to:

1. Register with Azure DevOps
2. Pick up exactly one job
3. Deregister and exit cleanly

The operator detects completed pods (`Succeeded` or `Failed` phase), releases their PVCs, and
deletes them. The reconcile loop then creates new pods to match `minAgents` or pending job demand.

This model prevents job queue pollution from long-lived agents and gives each job a clean environment.

## Scale-to-Zero

Setting `minAgents: 0` enables true scale-to-zero. When no jobs are pending:

- All real agent pods are deleted
- The operator registers an offline "dummy" agent in the pool

Azure DevOps queues jobs against the offline dummy agent rather than rejecting them as
"no agents available". When a job is detected, the operator removes the dummy and scales up real agents.

The dummy agent is an ADO agent object with no corresponding pod. It relies on the Azure DevOps
queuing behavior for offline agents. Verify this works in your ADO organization before relying on it.

## Resource Recommendations

Minimum per agent pod for a typical CI workload:

```yaml
agentResources:
  requests:
    cpu: "500m"
    memory: "1Gi"
  limits:
    cpu: "2"
    memory: "4Gi"
```

For Docker-in-Docker or heavy builds, increase memory limits to 8-16 Gi.

## Pod Placement

Use `nodeSelector`, `tolerations`, and `affinity` to place agents on dedicated build nodes:

```yaml
spec:
  nodeSelector:
    kubernetes.io/os: linux
    node-role: build

  tolerations:
    - key: "build-only"
      operator: "Exists"
      effect: "NoSchedule"
```

## Custom Agent Image

The operator uses the bundled agent image by default. To use a custom image:

```yaml
spec:
  agentImage: "myregistry.azurecr.io/azp-agent:latest"
  imagePullSecrets:
    - name: registry-credentials
```

The image must include the Azure Pipelines agent startup script (`start.sh` or equivalent).
The operator passes `--once` as a container argument, so the startup script must forward `"$@"` to `run.sh`.

Reference Dockerfile in `docker/` for the bundled image.

## Init Containers

Use `initContainers` to run setup steps before the agent container starts. Init containers share the pod's volumes:

```yaml
spec:
  initContainers:
    - name: install-tools
      image: busybox
      command: ["sh", "-c", "cp /tools/* /shared/"]
      volumeMounts:
        - name: tools
          mountPath: /shared
```

## Service Account

Assign a specific service account if agents need cluster API access:

```yaml
spec:
  serviceAccountName: "azp-agent"
```

The service account must exist in the same namespace.

## Observability

The operator exposes Prometheus metrics on the controller pod:

| Metric | Type | Description |
| --- | --- | --- |
| `azp_active_agents` | Gauge | Currently running agent pods |
| `azp_pending_jobs` | Gauge | Jobs waiting in the ADO queue |
| `azp_available_pvc_slots` | Gauge | Free PVC slots available for scale-up |
| `azp_reconcile_errors_total` | Counter | Reconcile errors by reason |

All metrics carry `pool` and `namespace` labels.

Use these alongside standard Kubernetes pod metrics for capacity planning and alerting.

## High Availability

The operator supports leader election for multi-replica deployments:

```bash
# Enable in the controller Deployment
--leader-elect=true
```

Only one replica is active at a time. Configure at least two replicas to avoid downtime during node failures.

## Troubleshooting

### Agents not scaling up

1. Check the `Available` condition on the AgentPool: `kubectl describe agentpool <name>`
2. Look for errors in controller logs: `kubectl logs deploy/azure-devops-agent-operator-controller-manager`
3. Verify the PAT is valid and has the correct scope

### PVC pending after scale-up

The PVC pool is pre-provisioned at `maxAgents` size. If PVCs remain `Pending`, the storage class
may not have available capacity or the cluster lacks a default StorageClass.
Specify `storageClassName` explicitly in `cacheVolumes`.

### Dummy agent not accepting jobs

The offline dummy agent behavior depends on Azure DevOps queuing semantics. If jobs fail immediately
instead of queuing, verify that your ADO organization queues work against offline agents by checking
under **Agent pools** > **Jobs** in the ADO UI.

### Pods evicted or OOMKilled

Increase `agentResources.limits.memory`. For Docker-in-Docker workloads, also check the DinD sidecar container limits.
