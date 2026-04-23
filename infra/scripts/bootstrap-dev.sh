#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

echo "→ Starting Postgres + DragonflyDB"
docker compose -f infra/docker-compose.yml up -d

echo "→ Waiting for Postgres healthcheck"
until docker compose -f infra/docker-compose.yml exec -T postgres pg_isready -U osint -d osint_dev; do
  sleep 1
done

echo "→ Installing Bun workspace deps"
bun install

echo "→ Done. Next: bun run db:migrate && bun run dev:api"
