# Authentication

## Required PAT Scope

The operator authenticates to Azure DevOps using a Personal Access Token (PAT). Only one scope is required:

```
Agent Pools > Read & Manage
```

This allows the operator to:
- Resolve pool IDs by name
- Register and unregister agent instances (including the dummy agent)
- Query job requests to detect pending work

## Creating a PAT

1. Navigate to `https://dev.azure.com/{your-org}/_usersettings/tokens`
2. Click **+ New Token**
3. Set a name and expiration (90 days recommended)
4. Choose **Custom defined** under Scopes
5. Expand **Agent Pools** and check **Read & Manage**
6. Click **Create** and copy the token immediately - it is not shown again

## Storing the Token as a Secret

```bash
kubectl create secret generic ado-token \
  --from-literal=pat='<YOUR_TOKEN>' \
  -n azure-devops-agent-operator-system
```

Reference the secret in your AgentPool spec:

```yaml
spec:
  tokenSecretRef:
    name: ado-token
    key: pat
```

The operator reads the secret once per reconcile cycle. Rotating the token does not require redeploying the operator.

## Token Rotation

To rotate an expired or compromised token:

```bash
# Delete the old secret
kubectl delete secret ado-token -n azure-devops-agent-operator-system

# Create a new secret with the new token
kubectl create secret generic ado-token \
  --from-literal=pat='<NEW_TOKEN>' \
  -n azure-devops-agent-operator-system
```

The operator picks up the new token on its next reconcile. If you need it to take effect immediately, restart the controller:

```bash
kubectl rollout restart deploy/azure-devops-agent-operator-controller-manager \
  -n azure-devops-agent-operator-system
```

## Security Recommendations

- Never commit tokens to source control
- Use a dedicated service identity with minimal scope - only **Agent Pools Read & Manage**
- Set an expiration on the token and rotate before it expires
- If managing multiple pools across environments, use separate tokens per environment
- Monitor the Azure DevOps audit log for unexpected token usage

## Verifying the Token

To confirm the secret contains a valid token:

```bash
kubectl get secret ado-token \
  -n azure-devops-agent-operator-system \
  -o jsonpath='{.data.pat}' | base64 -d | head -c 20
```

This prints the first 20 characters. Compare against the expected prefix from your password manager.

## References

- [Azure DevOps PAT documentation](https://learn.microsoft.com/en-us/azure/devops/organizations/accounts/use-personal-access-tokens-to-authenticate)
- [PAT scope reference](https://learn.microsoft.com/en-us/azure/devops/organizations/accounts/use-personal-access-tokens-to-authenticate#scopes)
- [Agent pools documentation](https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/pools-queues)
