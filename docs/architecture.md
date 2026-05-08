# Architecture

## Overview

The Azure DevOps Agent Operator is built on Kubernetes controller-runtime and uses the Kubebuilder framework.

## Key Components

- **AgentPool CRD**: Custom resource that describes a pool of Azure DevOps agents
- **AgentPoolReconciler**: Main controller that watches AgentPool resources and ensures cluster state matches desired spec
- **Condition-based Status**: Uses Kubernetes standard conditions to signal resource health

