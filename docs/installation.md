# Installation

## Prerequisites

- Kubernetes 1.35+
- kubectl configured to access your cluster

## Deploy

```bash
kubectl apply -f https://github.com/amaanx86/azure-devops-agent-operator/releases/download/latest/install.yaml
```

## Verify

```bash
kubectl get deployment -n azure-devops-agent-operator-system
```
