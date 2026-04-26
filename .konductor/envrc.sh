#!/usr/bin/env bash
# Environment setup for grafana-kubernetes-datasource
# Usage: source .konductor/envrc.sh

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ---------- Go (via gvm) ----------
GO_VERSION="go1.25.7"

export GVM_DIR="${GVM_DIR:-${HOME}/.gvm}"
if [[ -s "${GVM_DIR}/scripts/gvm" ]]; then
  # shellcheck source=/dev/null
  . "${GVM_DIR}/scripts/gvm"

  if ! gvm list 2>/dev/null | grep -q "${GO_VERSION}"; then
    echo "Installing ${GO_VERSION} via gvm..."
    gvm install "${GO_VERSION}" -B
  fi

  gvm use "${GO_VERSION}" 2>/dev/null
  echo "Go $(go version 2>/dev/null | awk '{print $3}')"
else
  echo "gvm not found — install from https://github.com/moovweb/gvm" >&2
fi

# ---------- Mage ----------
if command -v go &>/dev/null && ! command -v mage &>/dev/null; then
  echo "Installing mage..."
  go install github.com/magefile/mage@latest
fi

if command -v mage &>/dev/null; then
  echo "mage: $(command -v mage)"
fi

# ---------- Node (via nvm) ----------
export NVM_DIR="${NVM_DIR:-${HOME}/.nvm}"
if [[ -s "${NVM_DIR}/nvm.sh" ]]; then
  # shellcheck source=/dev/null
  . "${NVM_DIR}/nvm.sh"

  NODE_VERSION="$(cat "${REPO_ROOT}/.nvmrc" 2>/dev/null)"
  if [[ -n "${NODE_VERSION}" ]]; then
    nvm install "${NODE_VERSION}" --silent 2>/dev/null
    nvm use "${NODE_VERSION}" --silent 2>/dev/null
  else
    nvm use 2>/dev/null
  fi

  echo "Node $(node --version 2>/dev/null) — npm $(npm --version 2>/dev/null)"
else
  echo "nvm not found — install from https://github.com/nvm-sh/nvm" >&2
fi

# ---------- npm dependencies ----------
if [[ -d "${REPO_ROOT}" && ! -d "${REPO_ROOT}/node_modules" ]] && command -v npm &>/dev/null; then
  echo "Running npm install..."
  (cd "${REPO_ROOT}" && npm install)
fi

echo "Environment ready."
