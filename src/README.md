# Kubernetes Datasource

Query the Kubernetes API directly from Grafana. List pods, inspect deployments, watch events, view logs, and render any 
built-in resource or your own CRDs in tables, stats, and dashboards.

## Why this plugin

Most Kubernetes observability in Grafana is metrics-based — Prometheus scraping kube-state-metrics, 
then graphing the result. That works for trends, but answers like *"which pods are CrashLooping right now?"* 
or *"show me the last 50 events in this namespace"* are awkward to express in PromQL.

This plugin talks to the Kubernetes API directly, so cluster state lands in Grafana as structured tabular data: 
one row per pod, deployment, event, or custom resource. Drop it into a table panel for a live cluster console, 
into stat panels for top-level counts, or pair it with your existing metrics dashboards for context.

## Features

- **Workloads** — Pods, Deployments, DaemonSets, StatefulSets, ReplicaSets, Jobs, CronJobs
- **Networking** — Services, Ingresses, IngressClasses, NetworkPolicies
- **Config & Storage** — ConfigMaps, Secrets, PersistentVolumes, PersistentVolumeClaims, StorageClasses
- **RBAC** — Roles, RoleBindings, ClusterRoles, ClusterRoleBindings, ServiceAccounts
- **Cluster** — Nodes, Namespaces, Events, CustomResourceDefinitions
- **Pod logs** — stream container logs into a Logs panel
- **Pod summary** — pre-shaped frames (status, conditions, containers) for dashboard widgets
- **Raw YAML** — render any resource as YAML for inspection panels
- Filter by `namespace`, `name`, `labelSelector`, or `nodeName`

## Requirements

- Grafana **12.0** or later
- A Kubernetes cluster reachable from your Grafana instance
- Credentials with read access to the resources you want to query

## Getting started

1. Install and Enable Plugin from the Grafana Marketplace
2. Follow instructions in the Plugin's Configuration Page
