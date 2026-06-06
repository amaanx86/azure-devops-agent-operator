# Azure DevOps Agent Operator

Kubernetes-native elastic scaling for Azure DevOps self-hosted agents.
Run on any cloud or on-premises cluster - no Azure lock-in.

---

## Why This Operator?

Azure DevOps has several scaling options, each with trade-offs:

- **Microsoft-hosted agents** - fixed VM sizes, no caching, limited customization
- **Managed DevOps Pools** - Azure-only, limited observability integration
- **KEDA + Azure Pipelines** - unsafe cache sharing, no multi-container support
- **VM Scale Sets** - slow provisioning (minutes), high maintenance burden

This operator runs agents as plain Kubernetes Pods and manages the full lifecycle:
scale-to-zero, warm cache volumes, safe scale-down, and condition-based status.

---

## Key Features

| Feature | Description |
| --- | --- |
| Multi-cloud and on-prem | Runs on any Kubernetes cluster - AWS, GCP, Azure, OpenShift, bare metal |
| True scale-to-zero | Registers an offline dummy agent so ADO queues jobs when idle; scales up on demand |
| Warm cache volumes | Pre-provisioned PVCs bound exclusively per pod; cache survives pod replacement |
| Safe scale-down | Skips pods executing a job; never kills a running build |
| Full observability | Pods visible to Prometheus, Grafana, and your existing logging stack |
| One-job-per-pod | Each pod runs exactly one job then exits cleanly |

---

## When to Use This

- Regulatory requirements forbid Microsoft-managed compute (financial services, healthcare, defense)
- Multi-cloud organizations standardized on Kubernetes
- Air-gapped or sovereign cloud environments
- Azure regions where Managed DevOps Pools are unavailable
- Cost-sensitive CI on spare cluster capacity

For most teams, Managed DevOps Pools or KEDA is the right choice. This operator targets cases where neither fits.

---

## Quick Start

1. [Setup](setup.md) - install the operator and create your first AgentPool
2. [Authentication](authentication.md) - configure the PAT token
3. [Usage](usage.md) - full spec reference and scaling behavior
4. [Production](production.md) - cache volumes, placement, and observability
