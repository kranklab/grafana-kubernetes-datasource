# Development Setup

## Repos

Three repos work together. All should be cloned under the same parent directory:

```
kranklab/
  grafana-kubernetes-datasource/   ← datasource plugin (Go + TS), hosts the dev Grafana instance
  kranklab-kubernetes-panel/       ← panel plugin (TS)
  kranklab-kubernetesdashboard-app/ ← app plugin (Go + TS)
```

## Prerequisites

- **Node** via nvm
- **Go** via gvm — project requires Go 1.25+
- **mage** — Go build tool for the backend
- **Docker + Docker Compose**
- **Minikube** — local Kubernetes cluster

### Install mage (once)

```bash
source ~/.gvm/scripts/gvm
gvm use go1.25
go install github.com/magefile/mage@latest
```

Add to IntelliJ terminal PATH via **Settings → Tools → Terminal → Environment variables**:
```
PATH=/home/sdonnell/.gvm/gos/go1.25/bin:/home/sdonnell/go/bin:$PATH
```

## Kubernetes Certs

The datasource connects to Minikube using mTLS. The cert files must be copied from Minikube into the datasource repo root (they are mounted into the Grafana container at runtime):

```bash
y# Run from the datasource repo root
cp ~/.minikube/ca.crt ./ca.crt
cp ~/.minikube/profiles/minikube/client.crt ./client.crt
cp ~/.minikube/profiles/minikube/client.key ./client.key
```

> These certs change when Minikube is recreated. Re-run the above if you get connection errors.

## Build

### Backend (datasource + app — Go)

```bash
# datasource
cd ~/repo/repo2/github/kranklab/grafana-kubernetes-datasource
mage -v build:linux

# app
cd ~/repo/repo2/github/kranklab/kranklab-kubernetesdashboard-app
mage -v build:linux
```

### Frontend (all three — run each in a separate terminal, keep running)

```bash
cd ~/repo/repo2/github/kranklab/grafana-kubernetes-datasource && npm run dev
cd ~/repo/repo2/github/kranklab/kranklab-kubernetes-panel && npm run dev
cd ~/repo/repo2/github/kranklab/kranklab-kubernetesdashboard-app && npm run dev
```

> The three webpack processes all try to use port 35729 for LiveReload — only the first one wins. This is harmless; webpack still rebuilds on file changes. Use **Ctrl+Shift+R** in the browser to pick up frontend changes.

## Start Grafana

Run from the datasource repo (this docker-compose hosts all three plugins):

```bash
cd ~/repo/repo2/github/kranklab/grafana-kubernetes-datasource
docker compose up --build
```

Grafana is available at **http://localhost:3000** (admin / admin).

The following plugins are loaded via volume mounts:
- `./dist` → datasource
- `../kranklab-kubernetes-panel/dist` → panel
- `../kranklab-kubernetesdashboard-app/dist` → app

The Kubernetes datasource is pre-provisioned pointing to `https://192.168.49.2:8443` (Minikube default).

## Making Changes

| Change type | Action needed |
|---|---|
| Frontend (TS/TSX) | webpack rebuilds automatically; hard-refresh browser |
| Backend (Go) | `mage -v build:linux` then `docker compose restart` |
| docker-compose.yaml | `docker compose up --build` |
| Certs | Copy fresh certs, then `docker compose restart` |

## Querying in Explore

Fields for the query editor:

| Field | Values |
|---|---|
| Action | `list`, `summary` |
| Namespace | `default`, or any namespace name |
| Resource | `pods`, `deployments`, `daemonsets`, `statefulsets`, `replicasets`, `jobs`, `cronjobs` |