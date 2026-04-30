#!/usr/bin/env bash
# Boots the full local stack: Postgres + Dragonfly (docker), Go worker, Py worker, API.
# Streams logs to stdout with [api]/[go]/[py] prefixes; ctrl-c shuts everything down.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# 1. .env scaffolding for local dev.
if [[ ! -f .env ]]; then
  echo "→ creating .env from .env.example"
  cp .env.example .env
fi
# Bun auto-loads .env from cwd. Since the api process runs from apps/api/, a
# stale apps/api/.env there would silently shadow the root file. Replace any
# real file with a symlink to root so root is always the single source of truth.
if [[ -f apps/api/.env && ! -L apps/api/.env ]]; then
  echo "→ apps/api/.env was a real file (would shadow root .env); backing up and symlinking"
  mv apps/api/.env "apps/api/.env.bak.$(date +%s)"
fi
[[ -L apps/api/.env ]] || ln -s ../../.env apps/api/.env

grep -q '^NODE_ENV=' .env || echo 'NODE_ENV=development' >> .env
if grep -q '^DEV_AUTH_BYPASS=' .env; then
  sed -i.bak 's/^DEV_AUTH_BYPASS=.*/DEV_AUTH_BYPASS=true/' .env && rm -f .env.bak
else
  echo 'DEV_AUTH_BYPASS=true' >> .env
fi

# 2. Generate ed25519 keypair if missing.
node infra/scripts/gen-worker-keys.mjs

# 3. Bring up Postgres + Dragonfly.
echo "→ starting Postgres + Dragonfly"
docker compose -f infra/docker-compose.yml up -d
echo "→ waiting for Postgres"
until docker compose -f infra/docker-compose.yml exec -T postgres pg_isready -U osint -d osint_dev >/dev/null 2>&1; do
  sleep 1
done

# Load .env into our shell so subprocesses inherit it.
set -a; . ./.env; set +a

# 4. Worker dependencies.
echo "→ syncing py-worker venv (uv)"
(cd apps/py-worker && uv sync --quiet)

# 5. Migrations (best-effort; dev-bypass mode doesn't require schema).
echo "→ running migrations (best-effort)"
(cd apps/api && dbmate --migrations-dir src/db/migrations --schema-file src/db/schema.sql up) || \
  echo "  migrations failed — continuing in dev-bypass mode"

# 6. Resolve worker ports and verify they're free before launching.
GO_PORT="${GO_WORKER_PORT:-8181}"
PY_PORT="${PY_WORKER_PORT:-8182}"
API_PORT="${PORT:-3000}"

check_port() {
  local p="$1" name="$2"
  if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "✗ port $p ($name) is already in use; lsof:"
    lsof -nP -iTCP:"$p" -sTCP:LISTEN | head -5
    return 1
  fi
}
check_port "$API_PORT" api && check_port "$GO_PORT" go-worker && check_port "$PY_PORT" py-worker || {
  echo "Free the listed ports (or set PORT/GO_WORKER_PORT/PY_WORKER_PORT) and retry."
  exit 1
}

# 7. Launch services. Track real PIDs so ctrl-c kills them cleanly.
echo "→ launching api + go-worker + py-worker"
PIDS=()

cleanup() {
  trap - INT TERM EXIT
  for pid in "${PIDS[@]:-}"; do
    [[ -n "${pid:-}" ]] && kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup INT TERM EXIT

(cd apps/go-worker && PORT="$GO_PORT" exec go run ./cmd/worker) 2>&1 | sed -u 's/^/[go] /' &
PIDS+=($!)

(cd apps/py-worker && PORT="$PY_PORT" exec uv run python -m py_worker.main) 2>&1 | sed -u 's/^/[py] /' &
PIDS+=($!)

(cd apps/api && PORT="$API_PORT" exec bun --watch src/index.ts) 2>&1 | sed -u 's/^/[api] /' &
PIDS+=($!)

wait -n
