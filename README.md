# Grafana Kubernetes Datasource

A Grafana backend datasource plugin that queries the Kubernetes API directly, supporting workloads, RBAC, storage, networking, CRDs, and more.

## Features

- List and inspect Pods, Deployments, DaemonSets, StatefulSets, Jobs, CronJobs
- Namespaces, Nodes, Events, ConfigMaps, Secrets, ServiceAccounts
- Services, Ingresses, NetworkPolicies, Endpoints
- PersistentVolumes, PersistentVolumeClaims, StorageClasses
- Roles, RoleBindings, ClusterRoles, ClusterRoleBindings
- CustomResourceDefinitions (CRDs)
- Label selector and node name filtering

## Authentication

The plugin supports four authentication modes, configured via `certInputMode` in `jsonData`.

### Client Certificate (file paths)

Mount cert files into the Grafana container and reference their paths.

```yaml
datasources:
  - name: 'kubernetes'
    type: 'kranklab-kubernetes-datasource'
    access: proxy
    jsonData:
      url: https://192.168.49.2:8443
      certInputMode: file
      clientCert: /path/to/client.crt
      clientKey: /path/to/client.key
      caCert: /path/to/ca.crt
```

### Client Certificate (inline PEM)

Provide certificate data directly in `secureJsonData`.

```yaml
datasources:
  - name: 'kubernetes'
    type: 'kranklab-kubernetes-datasource'
    access: proxy
    jsonData:
      url: https://192.168.49.2:8443
      certInputMode: inline
    secureJsonData:
      clientCertData: |
        -----BEGIN CERTIFICATE-----
        ...
        -----END CERTIFICATE-----
      clientKeyData: |
        -----BEGIN RSA PRIVATE KEY-----
        ...
        -----END RSA PRIVATE KEY-----
      caCertData: |
        -----BEGIN CERTIFICATE-----
        ...
        -----END CERTIFICATE-----
```

### Bearer Token

Use a Kubernetes service account token. The CA can be provided inline or as a file path.

```yaml
datasources:
  - name: 'kubernetes'
    type: 'kranklab-kubernetes-datasource'
    access: proxy
    jsonData:
      url: https://192.168.49.2:8443
      certInputMode: token
      caCertMode: inline  # or omit for file-based CA
      caCert: /path/to/ca.crt  # used when caCertMode is not "inline"
    secureJsonData:
      bearerToken: 'eyJhbGciOi...'
      caCertData: |  # used when caCertMode is "inline"
        -----BEGIN CERTIFICATE-----
        ...
        -----END CERTIFICATE-----
```

### AWS EKS

Connects to an EKS cluster by automatically fetching the cluster endpoint, CA, and generating a pre-signed STS token. No `url` is needed.

#### IAM User (static credentials)

```yaml
datasources:
  - name: 'kubernetes-eks'
    type: 'kranklab-kubernetes-datasource'
    access: proxy
    jsonData:
      certInputMode: aws
      awsRegion: eu-west-1
      eksClusterName: my-cluster
      awsIamMode: user
    secureJsonData:
      awsAccessKeyId: AKIA...
      awsSecretAccessKey: '...'
```

#### IAM Role (assume role)

```yaml
datasources:
  - name: 'kubernetes-eks'
    type: 'kranklab-kubernetes-datasource'
    access: proxy
    jsonData:
      certInputMode: aws
      awsRegion: eu-west-1
      eksClusterName: my-cluster
      awsIamMode: role
      awsRoleArn: arn:aws:iam::123456789012:role/my-role
      # awsExternalId: optional-external-id
```

When using role mode, the plugin calls `sts:AssumeRole` using base credentials resolved from:

1. Static keys in `secureJsonData` (`awsAccessKeyId` / `awsSecretAccessKey`), if provided
2. The AWS SDK default credential chain (env vars, `~/.aws/credentials`, EC2/ECS instance profile)

The IAM identity (user or assumed role) must have `eks:DescribeCluster` and `sts:GetCallerIdentity` permissions, and be mapped to a Kubernetes RBAC identity via the EKS `aws-auth` ConfigMap or EKS access entries.

## Configuration Reference

### `jsonData`

| Field | Description |
|-------|-------------|
| `certInputMode` | Auth mode: `file`, `inline`, `token`, or `aws` |
| `url` | Kubernetes API URL (not needed for `aws` mode) |
| `defaultNamespace` | Default namespace for queries when none is specified. Use `_all` for all namespaces. Defaults to `default` |
| `clientCert` / `clientKey` / `caCert` | Paths to cert files (`file` mode) |
| `caCertMode` | Set to `inline` to use `caCertData` with token auth |
| `awsRegion` | AWS region of the EKS cluster |
| `eksClusterName` | EKS cluster name |
| `awsIamMode` | `user` or `role` |
| `awsRoleArn` | IAM role ARN to assume (`role` mode) |
| `awsExternalId` | STS external ID (optional, `role` mode) |

### `secureJsonData`

| Field | Description |
|-------|-------------|
| `clientCertData` / `clientKeyData` / `caCertData` | Inline PEM data (`inline` / `token` modes) |
| `bearerToken` | Service account token (`token` mode) |
| `awsAccessKeyId` / `awsSecretAccessKey` | IAM user credentials (`aws` mode) |
