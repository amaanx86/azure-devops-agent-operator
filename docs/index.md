# Azure DevOps Agent Operator

## Kubernetes-native Azure DevOps self-hosted agents with true elastic scaling

A purpose-built Kubernetes operator that brings enterprise-grade, demand-aware agent scaling to Azure DevOps pipelines. Run agents on any cloud or on-premises Kubernetes cluster with full observability and zero cold-start overhead.

---

## Why This Project?

### The Problem
Azure DevOps offers several scaling options, but each has limitations:

- **Microsoft-hosted agents** → Fixed VM sizes, no agent caching, limited customization
- **Managed DevOps Pools** → Azure-only, opaque to your observability stack
- **KEDA + Azure Pipelines** → Can't handle multi-container agents, unsafe cache sharing
- **VM Scale Sets** → Slow provisioning (minutes), high maintenance burden

### The Solution
**`azure-devops-agent-operator`** is a pure Kubernetes operator that solves these constraints:

---

## Key Features

| Feature | Benefit |
|---------|---------|
| **Multi-Cloud & On-Prem** | Run on AWS, GCP, Azure, OpenShift, or on-premises Kubernetes clusters. No cloud lock-in. |
| **Demand-Aware Scaling** | Automatically match agents to job requirements using Azure DevOps demands and capabilities. No parallel pool management. |
| **True Scale-to-Zero** | Scales to zero pods automatically with intelligent dummy-agent management. Saves compute costs. |
| **Exclusive Cache Volumes** | Each pod gets dedicated cache storage. No cold-cache penalties on every build. |
| **Full Observability** | Agents run as plain Kubernetes Pods. Monitor and debug alongside your entire stack in Grafana, Prometheus, and ELK. |
| **Controller-Managed Lifecycle** | Operators never kill a pod mid-job. Fine-grained control over pod lifecycle and graceful shutdown. |

---

## Perfect For

- **Financial services, healthcare, defense** — Regulatory requirements forbid Microsoft-managed compute
- **Multi-cloud organizations** — Standardized on Kubernetes across AWS, GCP, and Azure
- **Air-gapped environments** — Sovereign clouds and on-premises deployments
- **Unsupported regions** — Managed DevOps Pools unavailable in your Azure region
- **Cost-sensitive CI** — Spare cluster capacity is cheaper than additional cloud vendor fees

---

## Next Steps

- **[Installation](installation.md)** — Get the operator running in your cluster
- **[Architecture](architecture.md)** — Understand how it works
- **[Usage](usage.md)** — Configure agent pools for your workloads
- **[GitHub](https://github.com/amaanx86/azure-devops-agent-operator)** — View source, report issues, contribute
