# Usage

## Creating an AgentPool

Create an AgentPool resource to define your Azure DevOps agent pool:

```yaml
apiVersion: agents.amaanx86.github.io/v1alpha1
kind: AgentPool
metadata:
  name: my-agents
spec:
  organization: myorg
  poolName: my-pool
  minAgents: 0
  maxAgents: 10
```

The operator will automatically:
- Provision and manage agent pods
- Scale based on demand
- Manage caching and volumes
- Monitor agent health
