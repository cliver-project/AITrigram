# OpenShift-Specific Notes for E2E Storage Tests

## Background

This project is designed for native Kubernetes platforms. However, if you're running E2E tests on OpenShift, you'll encounter Security Context Constraint (SCC) restrictions that are more stringent than Kubernetes PodSecurity Standards.

## The Issue

OpenShift uses SCCs to control what privileges pods can request. The NFS server used in E2E tests requires:
- `privileged: true`
- `runAsUser: 0` (root)
- Capabilities: `SYS_ADMIN`, `SETPCAP`

By default, ServiceAccounts cannot use SCCs that allow these permissions.

## Solution Implemented

We've created a dedicated ServiceAccount (`nfs-server`) with permissions to use OpenShift's `privileged` SCC:

```yaml
# test/e2e/storage/nfs-server-serviceaccount.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: nfs-server
  namespace: aitrigram-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: nfs-server-privileged-scc
  namespace: aitrigram-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:openshift:scc:privileged
subjects:
- kind: ServiceAccount
  name: nfs-server
  namespace: aitrigram-system
```

This is automatically applied by the E2E test suite before deploying the NFS server.

## Alternative: Skip NFS Tests on OpenShift

If you prefer not to use privileged containers, you can skip the NFS-specific tests and rely on:
- **HostPath tests** - Works on both Kubernetes and OpenShift
- **PVC RWO tests** - Works with any storage class
- **PVC RWX tests** - Works if you have RWX-capable storage

To skip NFS tests, simply don't deploy the NFS server infrastructure and let the tests skip automatically when NFS is unavailable.

## Security Considerations

**For Production**: The operator itself does NOT require privileged mode. Only the E2E test infrastructure (NFS server) requires it. Your production ModelRepository and LLMEngine workloads use standard, non-privileged security contexts.

**For E2E Testing**: The privileged NFS server runs only during tests in an isolated namespace and is cleaned up afterward.

## Native Kubernetes vs OpenShift

| Feature | Native K8s | OpenShift |
|---------|-----------|-----------|
| PodSecurity | PSP/PSS (warnings only) | SCC (enforced) |
| NFS Server | Works with namespace labels | Requires ServiceAccount + SCC binding |
| Production Workloads | No special config needed | No special config needed |
| E2E Tests | Apply namespace-config.yaml | Apply nfs-server-serviceaccount.yaml |

## Recommendations

1. **For Native Kubernetes Environments**: The current setup works out of the box
2. **For OpenShift Environments**: Use the provided ServiceAccount configuration (already integrated in E2E tests)
3. **For Maximum Security**: Consider using production-grade NFS servers instead of the test NFS server for persistent environments
