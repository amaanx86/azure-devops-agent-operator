# azure-devops-agent-operator

A Kubernetes operator for elastically-scalable Azure DevOps self-hosted agents.

## Background

Azure DevOps offers several ways to run self-hosted, elastically-scalable
agents. As of May 2026 the landscape looks like this:

| Option | What it is | Where it falls short for our audience |
|---|---|---|
| **[Microsoft-hosted agents](https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/hosted)** | Fully managed by Microsoft | Fixed VM sizes, no agent-side caching between jobs, no VNET integration, no custom images |
| **[Azure VM Scale Set agents](https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/scale-set-agents)** | Self-managed VMSS in your subscription | Slow provisioning (minutes), one agent per VM, high maintenance |
| **[Managed DevOps Pools (MDP)](https://learn.microsoft.com/en-us/azure/devops/managed-devops-pools/overview)** | Microsoft's fully-managed evolution of VMSS, GA November 2024 | Azure-only; agents run in a Microsoft-owned subscription via the host-on-behalf model; not available in every Azure region; opaque to your observability stack |
| **[KEDA Azure Pipelines scaler](https://keda.sh/docs/latest/scalers/azure-pipelines/)** | General-purpose K8s autoscaler with an Azure Pipelines scaler | Structural limitations documented below |
| **[Azure Container Apps Jobs + KEDA](https://learn.microsoft.com/en-us/azure/container-apps/jobs)** | Serverless containers with KEDA-based scale-to-zero | Same KEDA limitations; Azure-only; no Docker-in-Docker without privileged mode |
| **Purpose-built K8s operators** | What this project is | Previously only [MShekow/azure-pipelines-k8s-agent-scaler](https://github.com/MShekow/azure-pipelines-k8s-agent-scaler), [archived July 4, 2025](https://github.com/MShekow/azure-pipelines-k8s-agent-scaler) (maintainer recommended switching to MDP); [microsoft/azure-pipelines-orchestrator](https://github.com/microsoft/azure-pipelines-orchestrator) and [ogmaresca/azp-agent-autoscaler](https://github.com/ogmaresca/azp-agent-autoscaler), both archived earlier |

### Why not Managed DevOps Pools?

If you can use MDP, you probably should — it is the right answer for most
Azure-native teams. This operator exists for the cases MDP doesn't serve:

- **Multi-cloud and on-prem Kubernetes** — MDP runs in Microsoft Azure. If
  your organisation has standardised on AWS, GCP, OpenShift, or on-prem
  Kubernetes for everything else, taking a hard Azure dependency just for
  CI compute is operationally awkward and creates a single-cloud lock-in
  for your build infrastructure.
- **Air-gapped, sovereign, and regulated environments** — financial
  services back offices, government, defense, and healthcare workloads
  with data-residency or "no Microsoft-managed compute" requirements
  cannot use MDP's host-on-behalf model. They run Azure DevOps Server
  on-prem and need agents in their own clusters.
- **Region-restricted tenants** — MDP isn't available in every Azure
  region. Teams in unsupported regions still need a Kubernetes-native
  option.
- **High-volume CI on existing capacity** — both MDP agents and agents
  run by this operator are "self-hosted" from Azure DevOps' perspective
  and pay the same standard $15/parallel-job/month Azure DevOps fee. The
  difference is the underlying compute: MDP additionally charges Azure
  VM, storage, and egress rates for the agents Microsoft runs on your
  behalf, while running agents on your existing Kubernetes cluster
  consumes capacity you already pay for. For high-volume CI workloads
  with spare cluster capacity, this can be materially cheaper.
- **Full observability** — MDP agents are largely a black box: you
  can't remote into them, run custom Prometheus exporters on the host,
  or profile builds at the kernel level. Platform teams that want CI
  agent telemetry alongside the rest of their workloads in the same
  Grafana stack benefit from running agents as plain Pods they own.

### Why not KEDA?

If MDP is off the table, KEDA's first-party Azure Pipelines scaler is
the sanctioned alternative. It is also the path Microsoft pointed users
at when they archived [`azure-pipelines-orchestrator`](https://github.com/microsoft/azure-pipelines-orchestrator).
KEDA works, and for the simplest workloads it is the right tool. But it
has structural limitations the operator pattern can solve cleanly:

- **Multi-container agents are cumbersome.** You cannot use Azure
  Pipelines' native *demands / capabilities* feature to route jobs to
  pods with different toolchains. Instead you have to create a dedicated
  agent pool per toolchain and maintain a parallel set of KEDA
  `ScaledJob` manifests for each. This scales poorly past a handful of
  toolchains.
- **Dynamically-defined containers from pipeline YAML are not
  supported.** If job #1 builds an image with a tag derived from a
  pipeline variable and job #2 needs to run inside that image, the only
  KEDA-compatible workaround is an *ephemeral container* injected into
  a running pod — which can't be protected via `preStop` lifecycle
  hooks, is invisible to most tooling, and whose resource usage is not
  accounted for via `requests`/`limits`.
- **True scale-to-zero requires manual dummy-agent management.** KEDA
  requires `minReplicaCount > 0` for each agent pool, otherwise the
  Azure Pipelines platform won't dispatch jobs at all (this is an Azure
  Pipelines platform behavior, not a KEDA bug). To scale to zero you
  have to register fake/offline dummy agents yourself for every pool
  and every demand combination.
- **`ScaledObject` mode can kill long-running agent pods mid-job.**
  When using KEDA's Deployment-based `ScaledObject` scaler, scaling
  decisions are based on pending-job count alone. If two jobs are
  pending and KEDA schedules two pods, then one finishes quickly, KEDA
  reduces the desired replica count and Kubernetes may pick the
  still-running pod to terminate. KEDA's `ScaledJob` mode (one Job per
  pending pipeline job) avoids this — but at the cost of the next
  bullet:
- **Ephemeral `ScaledJob` pods cannot safely share cache volumes.**
  Running agents as ephemeral `Kubernetes Job`s with the AZP agent's
  `--once` flag is the recommended KEDA pattern, and it does avoid the
  mid-job-kill class of bug. But Kubernetes has no mechanism to ensure
  a `ReadWriteOnce` PVC is mounted to only one Job at a time (the
  `Once` in `RWO` is per-node, not per-Pod). For workloads where build
  cache is a major performance lever — BuildKit, Gradle, Maven, Bazel,
  Nix — this means you pay the cold-cache penalty on every job, or you
  shift to expensive `ReadWriteMany` storage and accept its own
  trade-offs.

### What this project does

`azure-devops-agent-operator` (this project) is a pure Kubernetes
operator that solves the above with controller-managed pod lifecycle,
demand-aware capability matching, true scale-to-zero with automatic
dummy-agent management, and exclusive cache-volume binding per pod. The
original solution to this shape of problem was MShekow's
[`azure-pipelines-k8s-agent-scaler`](https://github.com/MShekow/azure-pipelines-k8s-agent-scaler);
that project was archived on July 4, 2025 with the maintainer
recommending Managed DevOps Pools as the replacement. For the audiences
listed above that cannot or will not use MDP, no actively-maintained
Kubernetes-native option remained — which is why this project exists.

This is **not** a fork or rewrite of MShekow's code. The architecture
and API design are independent. Where MShekow made design choices
documented in his blog and README, those documents have been valuable
prior art for understanding the problem space.

### Acknowledgments

This project owes a substantial intellectual debt to:

- Marius Shekow's [blog post](https://www.augmentedmind.de/2023/12/10/azure-pipelines-agents-kubernetes-operator/)
  and the archived [`azure-pipelines-k8s-agent-scaler`](https://github.com/MShekow/azure-pipelines-k8s-agent-scaler)
  project, which mapped the problem space clearly
- The KEDA project for the [Azure Pipelines scaler](https://keda.sh/docs/latest/scalers/azure-pipelines/),
  which establishes the queue-polling pattern this operator builds on
- Microsoft's [`azure-pipelines-orchestrator`](https://github.com/microsoft/azure-pipelines-orchestrator)
  (also archived), which validated the operator pattern was viable

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute.

## License

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
