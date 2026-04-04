#!/usr/bin/env bash
# Environment setup for grafana-kubernetes-datasource
# Usage: source .konductor/envrc.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ---------- Go (via gvm) ----------
GO_VERSION="go1.25.7"

export GVM_DIR="${HOME}/.gvm"
if [[ -s "${GVM_DIR}/scripts/gvm" ]]; then
  # shellcheck source=/dev/null
  . "${GVM_DIR}/scripts/gvm"
else
  echo "gvm not found at ${GVM_DIR}/scripts/gvm — install from https://github.com/moovweb/gvm" >&2
  return 1
fi

if ! gvm list | grep -q "${GO_VERSION}"; then
  echo "Installing ${GO_VERSION} via gvm..."
  gvm install "${GO_VERSION}" -B
fi

gvm use "${GO_VERSION}"

echo "Go $(go version | awk '{print $3}')"

# ---------- Mage ----------
if ! command -v mage &>/dev/null; then
  echo "Installing mage..."
  go install github.com/magefile/mage@latest
fi

echo "mage: $(command -v mage)"

# ---------- Node (via nvm) ----------
export NVM_DIR="${NVM_DIR:-${HOME}/.nvm}"
if [[ -s "${NVM_DIR}/nvm.sh" ]]; then
  # shellcheck source=/dev/null
  . "${NVM_DIR}/nvm.sh"
else
  echo "nvm not found at ${NVM_DIR}/nvm.sh — install from https://github.com/nvm-sh/nvm" >&2
  return 1
fi

NODE_VERSION="$(cat "${REPO_ROOT}/.nvmrc")"
nvm install "${NODE_VERSION}" --silent
nvm use "${NODE_VERSION}" --silent

echo "Node $(node --version) — npm $(npm --version)"

# ---------- npm dependencies ----------
if [[ ! -d "${REPO_ROOT}/node_modules" ]]; then
  echo "Running npm install..."
  (cd "${REPO_ROOT}" && npm install)
fi

echo "Environment ready."
