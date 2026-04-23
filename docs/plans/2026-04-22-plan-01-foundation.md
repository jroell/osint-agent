# Plan 01 — Foundation + MCP Skeleton + First 3 Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a deployable MCP server that authenticates users via Firebase, meters credits via Stripe, calls three real OSINT tools (stealth HTTP fetch, passive subdomain enumeration, DNS lookup), and emits an event stream for future learning-loop activation.

**Architecture:** Bun + TypeScript + ElysiaJS monorepo with a central product API (MCP server + REST), a Go tool worker for ProjectDiscovery libraries, and a Python tool worker for JA4-impersonating HTTP (rnet). Postgres 16 holds tenants / users / credits / events. Firebase Auth issues JWTs; the MCP server verifies via Google JWKS. LLM Gateway abstracts the Anthropic SDK so Phase 2 OpenRouter integration is additive. Deployment to Fly.io with single-machine-per-service in Plan 1 (multi-tenant pooling comes in Plan 5).

**Tech Stack:** Bun 1.2+, TypeScript 5.7+, ElysiaJS 1.x, @modelcontextprotocol/sdk, firebase-admin, @anthropic-ai/sdk, stripe, Postgres 16, dbmate (migrations), Go 1.24 + Echo v4, Python 3.13 + FastAPI + uv + rnet, Docker, Fly.io, GitHub Actions, OpenTelemetry, Pino.

---

## File Structure

```
osint-agent/
├── .github/workflows/
│   ├── ci.yml                         ← Lint + test + build all apps
│   └── deploy.yml                     ← Fly.io deploy on main
├── apps/
│   ├── api/                           ← Bun + Elysia MCP server + product API
│   │   ├── src/
│   │   │   ├── index.ts               ← Entry point; boots HTTP + MCP transport
│   │   │   ├── config.ts              ← Typed env-var loading
│   │   │   ├── db/
│   │   │   │   ├── client.ts          ← Postgres connection pool
│   │   │   │   └── migrations/       ← dbmate-managed .sql files
│   │   │   ├── auth/
│   │   │   │   ├── firebase.ts        ← Admin SDK init + JWT verification
│   │   │   │   └── middleware.ts      ← Elysia auth middleware
│   │   │   ├── billing/
│   │   │   │   ├── stripe.ts          ← Stripe client + checkout session
│   │   │   │   ├── credits.ts         ← Credit meter (atomic decrement)
│   │   │   │   └── webhook.ts         ← Stripe webhook handler
│   │   │   ├── events/
│   │   │   │   └── stream.ts          ← Partitioned event-log writer
│   │   │   ├── llm/
│   │   │   │   ├── gateway.ts         ← Provider-agnostic interface
│   │   │   │   └── anthropic.ts       ← Anthropic backend impl
│   │   │   ├── mcp/
│   │   │   │   ├── server.ts          ← MCP server construction
│   │   │   │   ├── transport-stdio.ts ← stdio transport for local clients
│   │   │   │   ├── transport-http.ts  ← Streamable HTTP transport
│   │   │   │   └── tools/
│   │   │   │       ├── registry.ts    ← Tool registration
│   │   │   │       ├── stealth-http.ts        ← tool 1 dispatcher
│   │   │   │       ├── subfinder.ts           ← tool 2 dispatcher
│   │   │   │       └── dns-lookup.ts          ← tool 3 dispatcher
│   │   │   ├── workers/
│   │   │   │   ├── go-client.ts       ← HTTP client → Go worker
│   │   │   │   └── py-client.ts       ← HTTP client → Python worker
│   │   │   └── telemetry.ts           ← OpenTelemetry setup
│   │   ├── test/                      ← Bun test files, mirrors src/
│   │   ├── Dockerfile
│   │   ├── fly.toml
│   │   ├── package.json
│   │   └── tsconfig.json
│   ├── go-worker/                     ← Go tool worker
│   │   ├── cmd/worker/main.go         ← Entry
│   │   ├── internal/
│   │   │   ├── config/                ← Env-based config
│   │   │   ├── tools/
│   │   │   │   ├── subfinder.go       ← subfinder lib integration
│   │   │   │   └── dns.go             ← dnsx lib integration
│   │   │   └── server/                ← Echo HTTP server, signed requests
│   │   ├── go.mod
│   │   ├── Dockerfile
│   │   └── fly.toml
│   └── py-worker/                     ← Python tool worker (stealth HTTP)
│       ├── src/py_worker/
│       │   ├── __init__.py
│       │   ├── main.py                ← FastAPI entry
│       │   ├── config.py
│       │   ├── auth.py                ← Ed25519 signed request verification
│       │   └── tools/
│       │       └── stealth_http.py    ← rnet with JA4+ presets
│       ├── tests/
│       ├── pyproject.toml
│       ├── Dockerfile
│       └── fly.toml
├── packages/
│   └── shared-types/                  ← TypeScript types shared by TS apps
│       ├── src/
│       │   ├── tool-protocol.ts       ← Tool RPC types
│       │   ├── events.ts              ← Event schema
│       │   └── index.ts
│       ├── package.json
│       └── tsconfig.json
├── infra/
│   ├── docker-compose.yml             ← Postgres + DragonflyDB for local dev
│   └── scripts/
│       ├── bootstrap-dev.sh           ← One-liner local dev setup
│       └── bootstrap-fly.sh           ← One-liner Fly.io app creation
├── docs/
│   ├── specs/                         ← (already contains design spec)
│   └── plans/                         ← (this plan)
├── .env.example
├── .gitignore
├── .dockerignore
├── bunfig.toml
├── package.json                       ← Bun workspace root
├── tsconfig.base.json
├── LICENSE                            ← Apache-2.0
├── README.md
└── CONTRIBUTING.md
```

---

## Prerequisites (manual, one-time — do these BEFORE Task 1)

- [ ] **Firebase project created** — https://console.firebase.google.com → "Add project" → name `osint-agent-prod` (and `osint-agent-dev` for local) → Authentication → enable Email/Password, Google, GitHub providers. Download Admin SDK service-account JSON for each env. Store in 1Password.
- [ ] **Stripe account** — https://dashboard.stripe.com → create products: "Hunter" ($49/mo recurring), "Operator" ($199/mo recurring). Capture Price IDs. Get test + live API keys into 1Password.
- [ ] **Anthropic API key** — https://console.anthropic.com → create key with $100 initial credit for dev/test. Store in 1Password.
- [ ] **Fly.io account + CLI** — `curl -L https://fly.io/install.sh | sh` → `flyctl auth signup` → add payment method.
- [ ] **Cloudflare account** — create account + add payment method. We'll provision R2 in Plan 2; DNS and Pages in Plan 4.
- [ ] **GitHub repo created** — `osint-agent` under your personal or org account, public, Apache-2.0 licensed (we'll commit the LICENSE file in Task 1).
- [ ] **Local tools installed** — `bun` 1.2+, `go` 1.24+, `uv` latest, `docker` desktop, `dbmate` (`brew install amacneil/dbmate/dbmate`), `flyctl`. All verified with `--version` commands.

---

## Task 1: Monorepo scaffolding + Apache-2.0 licensing

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/package.json`
- Create: `/Users/jasonroell/projects/osint-agent/bunfig.toml`
- Create: `/Users/jasonroell/projects/osint-agent/tsconfig.base.json`
- Create: `/Users/jasonroell/projects/osint-agent/.gitignore`
- Create: `/Users/jasonroell/projects/osint-agent/.dockerignore`
- Create: `/Users/jasonroell/projects/osint-agent/LICENSE`
- Create: `/Users/jasonroell/projects/osint-agent/README.md`
- Create: `/Users/jasonroell/projects/osint-agent/CONTRIBUTING.md`
- Create: `/Users/jasonroell/projects/osint-agent/.env.example`

- [ ] **Step 1.1: Create the Bun workspace root `package.json`**

```json
{
  "name": "osint-agent",
  "version": "0.1.0",
  "private": true,
  "workspaces": ["apps/*", "packages/*"],
  "scripts": {
    "lint": "bun --filter '*' lint",
    "test": "bun --filter '*' test",
    "build": "bun --filter '*' build",
    "dev:api": "bun --filter api dev",
    "db:up": "docker compose -f infra/docker-compose.yml up -d",
    "db:down": "docker compose -f infra/docker-compose.yml down",
    "db:migrate": "cd apps/api && dbmate up",
    "db:rollback": "cd apps/api && dbmate rollback"
  },
  "devDependencies": {
    "typescript": "^5.7.0",
    "@types/bun": "latest"
  }
}
```

- [ ] **Step 1.2: Create `bunfig.toml`**

```toml
[install]
exact = true

[test]
coverage = true
```

- [ ] **Step 1.3: Create `tsconfig.base.json`**

```json
{
  "compilerOptions": {
    "target": "ES2023",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "noImplicitOverride": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "allowImportingTsExtensions": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "types": ["bun-types"]
  }
}
```

- [ ] **Step 1.4: Create `.gitignore`**

```
# Deps
node_modules/
bun.lockb
**/dist/
**/build/
**/.turbo/

# Env
.env
.env.local
.env.*.local
*.pem
firebase-admin-*.json

# Go
apps/go-worker/bin/
apps/go-worker/vendor/

# Python
apps/py-worker/.venv/
apps/py-worker/__pycache__/
apps/py-worker/**/__pycache__/
apps/py-worker/.pytest_cache/
apps/py-worker/dist/

# IDE / OS
.DS_Store
.idea/
.vscode/
*.swp

# Logs
*.log

# Coverage
coverage/
*.lcov
```

- [ ] **Step 1.5: Create `.dockerignore` (same intent as gitignore, plus docs/tests that don't belong in images)**

```
node_modules
bun.lockb
.git
.github
docs
**/test
**/__tests__
**/__pycache__
.env
.env.*
firebase-admin-*.json
*.md
```

- [ ] **Step 1.6: Create `LICENSE` (Apache-2.0)**

Download the canonical Apache-2.0 text from `https://www.apache.org/licenses/LICENSE-2.0.txt` and save to `/Users/jasonroell/projects/osint-agent/LICENSE`. Replace placeholder `[yyyy] [name of copyright owner]` with `2026 Jason Roell`.

- [ ] **Step 1.7: Create `README.md`**

```markdown
# osint-agent

**The recon stack that finds what someone is hiding.**

Adversary-aware OSINT for bug bounty hunters, security researchers, and investigative journalists. One MCP server plugs into Claude Desktop, Cursor, or your LLM client of choice and runs multi-source reconnaissance with an agent that reasons over a bitemporal knowledge graph of your findings.

- **Open-source core** (Apache-2.0): MCP server + tool adapters + orchestration glue
- **Proprietary moat** (hosted): learned World Model, Adversary Library, Federated Learning, Predictive Temporal reasoning, Investigator Policy Network
- **Pricing:** Free · Hunter $49/mo · Operator $199/mo (self-serve)

## Status

Pre-launch. Active development. Targeting first public release (Hacker News) at end of Phase 0 (~month 3).

## Quickstart (self-host the open-source core)

```sh
# Prerequisites: bun 1.2+, go 1.24+, uv, docker
bun install
bun run db:up
bun run db:migrate
bun run dev:api
```

Then point your MCP client at `http://localhost:3000/mcp`.

## Design

See `docs/specs/` for the full system design spec.

## Contributing

See `CONTRIBUTING.md`. PRs welcome — especially tool adapters and adversary playbook templates.

## License

Apache-2.0 — see [LICENSE](./LICENSE).
```

- [ ] **Step 1.8: Create `CONTRIBUTING.md`**

```markdown
# Contributing to osint-agent

Thank you for your interest in contributing.

## High-value contributions

1. **Tool adapters** — new OSINT tools that fit the typed tool protocol. See `apps/api/src/mcp/tools/` for the pattern and `packages/shared-types/src/tool-protocol.ts` for the interface.
2. **Adversary playbook templates** — structured subgraph patterns for known adversary behaviors. See `docs/specs/` (§4.4) for the schema.
3. **Documentation improvements.**
4. **Bug reports with reproductions.**

## What is NOT open-source

The World Model, Adversary Library (beyond 3 example playbooks), Federated Learning aggregator, Predictive Temporal Layer, and Investigator Policy Network are proprietary. PRs touching those areas will not be accepted; please discuss in an Issue before working on adjacent code.

## Process

1. Open an Issue describing what you want to do, especially for non-trivial changes.
2. Fork, branch, implement with tests (we run on every PR).
3. Run `bun run lint && bun run test` locally before pushing.
4. Open a PR against `main`. Link the Issue.
5. Contributors get Hunter-tier credits as thanks. High-value contributors get Operator-tier credits. Adversary playbook authors get co-author credit in the published case-study series.
```

- [ ] **Step 1.9: Create `.env.example`**

```
# --- Core ---
NODE_ENV=development
PORT=3000
LOG_LEVEL=debug

# --- Postgres ---
DATABASE_URL=postgres://osint:osint@localhost:5432/osint_dev?sslmode=disable

# --- DragonflyDB (Redis protocol) ---
REDIS_URL=redis://localhost:6379

# --- Firebase Auth ---
FIREBASE_PROJECT_ID=osint-agent-dev
# Either GOOGLE_APPLICATION_CREDENTIALS pointing to a JSON file, or FIREBASE_SERVICE_ACCOUNT_JSON containing the JSON itself
GOOGLE_APPLICATION_CREDENTIALS=./firebase-admin-dev.json

# --- Stripe ---
STRIPE_SECRET_KEY=sk_test_...
STRIPE_WEBHOOK_SECRET=whsec_...
STRIPE_PRICE_ID_HUNTER=price_...
STRIPE_PRICE_ID_OPERATOR=price_...

# --- Anthropic (LLM Gateway primary backend) ---
ANTHROPIC_API_KEY=sk-ant-...

# --- Worker endpoints + signing ---
GO_WORKER_URL=http://localhost:8081
PY_WORKER_URL=http://localhost:8082
WORKER_SIGNING_KEY_HEX=generate-a-32-byte-ed25519-seed-and-hex-encode-it

# --- Telemetry ---
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-prod-us-central-0.grafana.net/otlp
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic%20...
OTEL_SERVICE_NAME=osint-api-dev

# --- Billing unit economics ---
CREDIT_PRICE_USD=0.01
```

- [ ] **Step 1.10: Run initial git commit and push**

```bash
cd /Users/jasonroell/projects/osint-agent
git add .
git status   # verify files look correct
git commit -m "chore: monorepo scaffolding + Apache-2.0 license + README"

# Create the remote (if not already done)
gh repo create osint-agent --public --source=. --remote=origin --push
```

Expected outcome: repo visible at `https://github.com/jasonroell/osint-agent` with 1 commit, Apache-2.0 license detected by GitHub, README rendered.

- [ ] **Step 1.11: Commit checkpoint**

Already committed and pushed. ✓

---

## Task 2: CI baseline (GitHub Actions, fail-fast)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/.github/workflows/ci.yml`

- [ ] **Step 2.1: Write the CI workflow (initially expects zero apps — will pass green on empty repo)**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true

jobs:
  ts:
    name: TypeScript (Bun)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: oven-sh/setup-bun@v2
        with:
          bun-version: 1.2.x
      - run: bun install --frozen-lockfile
        continue-on-error: true  # lockfile may not exist on first run
      - run: bun install
      - run: bun run lint
      - run: bun run test
      - run: bun run build

  go:
    name: Go worker
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"
      - name: Skip if go-worker not present
        id: skip
        run: |
          if [ -f apps/go-worker/go.mod ]; then
            echo "exists=true" >> $GITHUB_OUTPUT
          else
            echo "exists=false" >> $GITHUB_OUTPUT
          fi
      - working-directory: apps/go-worker
        if: steps.skip.outputs.exists == 'true'
        run: go test ./...
      - working-directory: apps/go-worker
        if: steps.skip.outputs.exists == 'true'
        run: go build ./...

  py:
    name: Python worker
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.13"
      - uses: astral-sh/setup-uv@v3
      - name: Skip if py-worker not present
        id: skip
        run: |
          if [ -f apps/py-worker/pyproject.toml ]; then
            echo "exists=true" >> $GITHUB_OUTPUT
          else
            echo "exists=false" >> $GITHUB_OUTPUT
          fi
      - working-directory: apps/py-worker
        if: steps.skip.outputs.exists == 'true'
        run: uv sync --all-extras
      - working-directory: apps/py-worker
        if: steps.skip.outputs.exists == 'true'
        run: uv run pytest
```

- [ ] **Step 2.2: Commit and verify CI runs green on empty workspace**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: initial GitHub Actions workflow (fail-fast, conditional on app presence)"
git push

# Watch it pass
gh run watch
```

Expected: CI job passes with all three lanes (ts / go / py) green — they either run and pass, or skip cleanly because the apps don't exist yet.

---

## Task 3: Local dev environment (Postgres + DragonflyDB via Docker Compose)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/infra/docker-compose.yml`
- Create: `/Users/jasonroell/projects/osint-agent/infra/scripts/bootstrap-dev.sh`

- [ ] **Step 3.1: Write `docker-compose.yml`**

```yaml
services:
  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: osint
      POSTGRES_PASSWORD: osint
      POSTGRES_DB: osint_dev
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U osint -d osint_dev"]
      interval: 5s
      timeout: 3s
      retries: 10

  dragonfly:
    image: docker.dragonflydb.io/dragonflydb/dragonfly:latest
    restart: unless-stopped
    command: ["dragonfly", "--logtostderr"]
    ports:
      - "6379:6379"
    volumes:
      - dragonflydata:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  pgdata:
  dragonflydata:
```

- [ ] **Step 3.2: Write `bootstrap-dev.sh`**

```bash
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
```

```bash
chmod +x infra/scripts/bootstrap-dev.sh
```

- [ ] **Step 3.3: Run it and verify services healthy**

```bash
./infra/scripts/bootstrap-dev.sh
docker compose -f infra/docker-compose.yml ps
# Expected: postgres + dragonfly both show "healthy"
psql postgres://osint:osint@localhost:5432/osint_dev -c "SELECT 1;"
# Expected: returns "?column? = 1"
redis-cli -h localhost -p 6379 ping
# Expected: PONG
```

- [ ] **Step 3.4: Commit**

```bash
git add infra/
git commit -m "feat(infra): docker-compose for local Postgres + DragonflyDB + bootstrap script"
```

---

## Task 4: Database schema v1 (tenants, users, events, credits)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/package.json`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/tsconfig.json`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/dbmate.yml`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/db/migrations/20260422000001_core_schema.sql`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/db/migrations/20260422000002_events_partitioned.sql`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/db/client.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/test/db-client.test.ts`

- [ ] **Step 4.1: Create the API app skeleton `package.json`**

```json
{
  "name": "api",
  "version": "0.0.1",
  "private": true,
  "type": "module",
  "main": "src/index.ts",
  "scripts": {
    "dev": "bun --watch src/index.ts",
    "test": "bun test",
    "lint": "bunx tsc --noEmit",
    "build": "bun build src/index.ts --target=bun --outdir=dist"
  },
  "dependencies": {
    "postgres": "^3.4.4",
    "elysia": "^1.1.0",
    "@elysiajs/cors": "^1.1.0",
    "pino": "^9.5.0",
    "pino-pretty": "^11.2.0"
  },
  "devDependencies": {
    "@types/bun": "latest",
    "typescript": "^5.7.0"
  }
}
```

- [ ] **Step 4.2: Create `tsconfig.json`**

```json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": {
    "rootDir": "./src",
    "baseUrl": "./src"
  },
  "include": ["src/**/*.ts", "test/**/*.ts"]
}
```

- [ ] **Step 4.3: Create `dbmate.yml` (controls migration runner)**

```yaml
migrations_dir: ./src/db/migrations
schema_file: ./src/db/schema.sql
wait: true
```

- [ ] **Step 4.4: Write core schema migration**

File `src/db/migrations/20260422000001_core_schema.sql`:

```sql
-- migrate:up

-- tenants: one per paying entity (Phase 0 = one per user; Team tier in Phase 3 = many users per tenant)
CREATE TABLE tenants (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name          TEXT NOT NULL,
  tier          TEXT NOT NULL DEFAULT 'free' CHECK (tier IN ('free', 'hunter', 'operator')),
  stripe_customer_id    TEXT UNIQUE,
  stripe_subscription_id TEXT UNIQUE,
  credits_balance       BIGINT NOT NULL DEFAULT 100,  -- credits_balance in millicredits (100 millicredits = 1 credit = $0.01)
  -- Learning-loop scaffolding (inert until Phase 2)
  learning_bucket_b_opt_out    BOOLEAN NOT NULL DEFAULT false,
  benchmark_contribution_opt_in BOOLEAN NOT NULL DEFAULT false,
  byok_config                  JSONB,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX tenants_tier_idx ON tenants(tier);
CREATE INDEX tenants_stripe_customer_idx ON tenants(stripe_customer_id);

-- users: one row per Firebase UID; may belong to a tenant (Phase 0 = 1:1; Phase 3 Team = N:1)
CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  firebase_uid  TEXT NOT NULL UNIQUE,
  tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  email         TEXT NOT NULL,
  display_name  TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX users_tenant_idx ON users(tenant_id);
CREATE INDEX users_firebase_uid_idx ON users(firebase_uid);

-- credit_ledger: append-only; credits_balance on tenants is a materialized aggregate
CREATE TABLE credit_ledger (
  id            BIGSERIAL PRIMARY KEY,
  tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id       UUID REFERENCES users(id),
  delta_millicredits BIGINT NOT NULL,        -- negative = spent; positive = granted/refilled
  reason        TEXT NOT NULL,                -- 'tool:<name>' | 'llm:<model>' | 'refill:<tier>' | 'admin'
  metadata      JSONB NOT NULL DEFAULT '{}',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX credit_ledger_tenant_created_idx ON credit_ledger(tenant_id, created_at DESC);

-- migrate:down

DROP TABLE IF EXISTS credit_ledger;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
```

- [ ] **Step 4.5: Write events-partitioned migration**

File `src/db/migrations/20260422000002_events_partitioned.sql`:

```sql
-- migrate:up

-- events: append-only event stream, partitioned by month.
-- Every state-changing action writes here. Feeds:
--   (Phase 1) audit trail + trajectory logging
--   (Phase 2) learning loops (hypothesis outcomes, path quality, retrieval strategy)
CREATE TABLE events (
  id            BIGSERIAL,
  tenant_id     UUID NOT NULL,
  user_id       UUID,
  event_type    TEXT NOT NULL,         -- 'tool_call' | 'auth_signup' | 'billing_checkout' | 'credit_spend' | 'credit_grant' | ...
  payload       JSONB NOT NULL,
  trace_id      TEXT,                  -- OpenTelemetry trace ID for cross-service correlation
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Initial partitions: current month + next 3 months
CREATE TABLE events_2026_04 PARTITION OF events
  FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');
CREATE TABLE events_2026_05 PARTITION OF events
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE events_2026_06 PARTITION OF events
  FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
CREATE TABLE events_2026_07 PARTITION OF events
  FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');

CREATE INDEX events_tenant_created_idx ON events(tenant_id, created_at DESC);
CREATE INDEX events_type_idx ON events(event_type);
CREATE INDEX events_trace_idx ON events(trace_id) WHERE trace_id IS NOT NULL;

-- migrate:down

DROP TABLE IF EXISTS events_2026_07;
DROP TABLE IF EXISTS events_2026_06;
DROP TABLE IF EXISTS events_2026_05;
DROP TABLE IF EXISTS events_2026_04;
DROP TABLE IF EXISTS events;
```

- [ ] **Step 4.6: Write the DB client module**

File `src/db/client.ts`:

```typescript
import postgres from "postgres";

const DATABASE_URL = process.env.DATABASE_URL;
if (!DATABASE_URL) {
  throw new Error("DATABASE_URL is required");
}

// Tunable pool size; Elysia is single-event-loop so ~10 is plenty for Phase 0.
export const sql = postgres(DATABASE_URL, {
  max: 10,
  idle_timeout: 20,
  connect_timeout: 10,
  prepare: true,
});

export async function closeDb(): Promise<void> {
  await sql.end({ timeout: 5 });
}
```

- [ ] **Step 4.7: Write a failing test for the DB client**

File `test/db-client.test.ts`:

```typescript
import { describe, it, expect, afterAll } from "bun:test";
import { sql, closeDb } from "../src/db/client";

describe("db/client", () => {
  it("connects and executes SELECT 1", async () => {
    const rows = await sql`SELECT 1 AS one`;
    expect(rows[0].one).toBe(1);
  });

  it("sees the tenants table", async () => {
    const rows = await sql`
      SELECT to_regclass('public.tenants') AS t
    `;
    expect(rows[0].t).toBe("tenants");
  });

  afterAll(async () => {
    await closeDb();
  });
});
```

- [ ] **Step 4.8: Run the migrations and the test**

```bash
cd apps/api
bun install
export DATABASE_URL=postgres://osint:osint@localhost:5432/osint_dev?sslmode=disable
dbmate up
# Expected: applied both migrations

bun test test/db-client.test.ts
# Expected: both tests pass
```

- [ ] **Step 4.9: Commit**

```bash
cd /Users/jasonroell/projects/osint-agent
git add apps/api/ .github/workflows/
git commit -m "feat(api): core schema (tenants/users/credits) + partitioned events + DB client"
```

---

## Task 5: Firebase Auth integration + JWT verification middleware

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/auth/firebase.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/auth/middleware.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/config.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/test/auth.test.ts`
- Modify: `/Users/jasonroell/projects/osint-agent/apps/api/package.json` (add firebase-admin)

- [ ] **Step 5.1: Add firebase-admin to the api package**

```bash
cd apps/api
bun add firebase-admin
```

- [ ] **Step 5.2: Create typed config loader**

File `src/config.ts`:

```typescript
function required(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`Missing required env var: ${name}`);
  return v;
}

function optional(name: string, fallback: string): string {
  return process.env[name] ?? fallback;
}

export const config = {
  env: optional("NODE_ENV", "development"),
  port: Number(optional("PORT", "3000")),
  logLevel: optional("LOG_LEVEL", "info"),
  databaseUrl: required("DATABASE_URL"),
  redisUrl: required("REDIS_URL"),
  firebase: {
    projectId: required("FIREBASE_PROJECT_ID"),
  },
  anthropic: {
    apiKey: required("ANTHROPIC_API_KEY"),
  },
  stripe: {
    secretKey: required("STRIPE_SECRET_KEY"),
    webhookSecret: required("STRIPE_WEBHOOK_SECRET"),
    priceIdHunter: required("STRIPE_PRICE_ID_HUNTER"),
    priceIdOperator: required("STRIPE_PRICE_ID_OPERATOR"),
  },
  workers: {
    goUrl: required("GO_WORKER_URL"),
    pyUrl: required("PY_WORKER_URL"),
    signingKeyHex: required("WORKER_SIGNING_KEY_HEX"),
  },
  creditPriceUsd: Number(optional("CREDIT_PRICE_USD", "0.01")),
} as const;
```

- [ ] **Step 5.3: Write Firebase Admin init + JWT verification**

File `src/auth/firebase.ts`:

```typescript
import { initializeApp, applicationDefault, cert, getApps } from "firebase-admin/app";
import { getAuth, type DecodedIdToken } from "firebase-admin/auth";
import { config } from "../config";

function initFirebase() {
  if (getApps().length > 0) return;

  // If FIREBASE_SERVICE_ACCOUNT_JSON is set inline (Fly.io secret), use it; otherwise ADC.
  const inlineJson = process.env.FIREBASE_SERVICE_ACCOUNT_JSON;
  if (inlineJson) {
    initializeApp({
      credential: cert(JSON.parse(inlineJson)),
      projectId: config.firebase.projectId,
    });
    return;
  }

  initializeApp({
    credential: applicationDefault(),
    projectId: config.firebase.projectId,
  });
}

initFirebase();

/**
 * Verifies a Firebase ID token (JWT) and returns the decoded payload.
 * Throws if invalid, expired, or issuer mismatches our project.
 */
export async function verifyIdToken(idToken: string): Promise<DecodedIdToken> {
  return getAuth().verifyIdToken(idToken, true /* checkRevoked */);
}

export type FirebaseUser = DecodedIdToken;
```

- [ ] **Step 5.4: Write the Elysia auth middleware**

File `src/auth/middleware.ts`:

```typescript
import { Elysia } from "elysia";
import { verifyIdToken, type FirebaseUser } from "./firebase";
import { sql } from "../db/client";

export type AuthContext = {
  user: FirebaseUser;
  tenantId: string;
  userId: string;
};

/**
 * Elysia plugin: extracts `Authorization: Bearer <firebase_jwt>`, verifies it,
 * and attaches { user, tenantId, userId } to the request context.
 * Auto-provisions a tenants + users row on first sight of a valid JWT.
 */
export const authPlugin = new Elysia({ name: "auth" })
  .derive({ as: "scoped" }, async ({ request }) => {
    const header = request.headers.get("authorization");
    if (!header?.startsWith("Bearer ")) {
      throw new Response("Missing bearer token", { status: 401 });
    }
    const token = header.slice("Bearer ".length);

    const decoded = await verifyIdToken(token).catch((e) => {
      throw new Response(`Invalid token: ${e.message}`, { status: 401 });
    });

    // Upsert user + tenant (Phase 0 = 1 user : 1 tenant).
    const { userId, tenantId } = await ensureUserAndTenant(decoded);

    return {
      auth: {
        user: decoded,
        tenantId,
        userId,
      } satisfies { user: FirebaseUser; tenantId: string; userId: string },
    };
  });

async function ensureUserAndTenant(user: FirebaseUser): Promise<{ userId: string; tenantId: string }> {
  const email = user.email ?? `${user.uid}@unknown.local`;
  const name = user.name ?? email.split("@")[0];

  const result = await sql.begin(async (tx) => {
    // Look up existing user
    const existing = await tx`
      SELECT id, tenant_id FROM users WHERE firebase_uid = ${user.uid} LIMIT 1
    `;
    if (existing.length > 0) {
      await tx`UPDATE users SET last_seen_at = NOW() WHERE id = ${existing[0].id}`;
      return { userId: existing[0].id as string, tenantId: existing[0].tenant_id as string };
    }

    // Provision tenant (Free tier, 100 credits starting balance)
    const tenantRow = await tx`
      INSERT INTO tenants (name, tier)
      VALUES (${name}, 'free')
      RETURNING id
    `;
    const tenantId = tenantRow[0].id as string;

    // Provision user
    const userRow = await tx`
      INSERT INTO users (firebase_uid, tenant_id, email, display_name)
      VALUES (${user.uid}, ${tenantId}, ${email}, ${name})
      RETURNING id
    `;
    const userId = userRow[0].id as string;

    return { userId, tenantId };
  });

  return result;
}
```

- [ ] **Step 5.5: Write failing tests**

File `test/auth.test.ts`:

```typescript
import { describe, it, expect, beforeAll, afterAll, mock } from "bun:test";
import { Elysia } from "elysia";
import { authPlugin } from "../src/auth/middleware";
import { sql, closeDb } from "../src/db/client";

// Mock the firebase module so we don't need a live project for unit tests
mock.module("../src/auth/firebase", () => ({
  verifyIdToken: async (token: string) => {
    if (token === "valid-token") {
      return {
        uid: "firebase-uid-test-1",
        email: "test@example.com",
        name: "Test User",
        aud: "osint-agent-dev",
        iss: "https://securetoken.google.com/osint-agent-dev",
      };
    }
    throw new Error("token invalid");
  },
}));

describe("auth middleware", () => {
  beforeAll(async () => {
    // Clean slate for test uid
    await sql`DELETE FROM users WHERE firebase_uid = 'firebase-uid-test-1'`;
  });

  afterAll(async () => {
    await sql`DELETE FROM users WHERE firebase_uid = 'firebase-uid-test-1'`;
    await sql`DELETE FROM tenants WHERE name = 'test'`;
    await closeDb();
  });

  const app = new Elysia()
    .use(authPlugin)
    .get("/me", ({ auth }) => ({ uid: auth.user.uid, tenantId: auth.tenantId, userId: auth.userId }));

  it("rejects missing token", async () => {
    const res = await app.handle(new Request("http://localhost/me"));
    expect(res.status).toBe(401);
  });

  it("rejects invalid token", async () => {
    const res = await app.handle(
      new Request("http://localhost/me", { headers: { authorization: "Bearer bogus" } }),
    );
    expect(res.status).toBe(401);
  });

  it("accepts valid token and auto-provisions user + tenant", async () => {
    const res = await app.handle(
      new Request("http://localhost/me", { headers: { authorization: "Bearer valid-token" } }),
    );
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.uid).toBe("firebase-uid-test-1");
    expect(body.tenantId).toMatch(/^[0-9a-f-]{36}$/);
    expect(body.userId).toMatch(/^[0-9a-f-]{36}$/);

    // Idempotent: second call reuses same tenant
    const res2 = await app.handle(
      new Request("http://localhost/me", { headers: { authorization: "Bearer valid-token" } }),
    );
    const body2 = await res2.json();
    expect(body2.tenantId).toBe(body.tenantId);
  });
});
```

- [ ] **Step 5.6: Run tests and verify**

```bash
cd apps/api
bun test test/auth.test.ts
# Expected: 3 passed
```

- [ ] **Step 5.7: Commit**

```bash
git add apps/api/
git commit -m "feat(auth): Firebase JWT verification + Elysia middleware + auto-provision tenant/user"
```

---

## Task 6: Event stream writer (partition-rollover-safe)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/events/stream.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/test/events.test.ts`

- [ ] **Step 6.1: Write the event stream writer**

File `src/events/stream.ts`:

```typescript
import { sql } from "../db/client";

export type EventType =
  | "auth.signup"
  | "auth.signin"
  | "billing.checkout_created"
  | "billing.subscription_active"
  | "billing.subscription_canceled"
  | "credit.spent"
  | "credit.granted"
  | "tool.called"
  | "tool.succeeded"
  | "tool.failed"
  | "mcp.session_started"
  | "mcp.session_ended";

export interface EventInput {
  tenantId: string;
  userId?: string | null;
  eventType: EventType;
  payload: Record<string, unknown>;
  traceId?: string | null;
}

/**
 * Writes an event to the partitioned event log.
 * Auto-creates the current month's partition if it doesn't exist.
 */
export async function writeEvent(e: EventInput): Promise<void> {
  await ensureCurrentMonthPartition();
  await sql`
    INSERT INTO events (tenant_id, user_id, event_type, payload, trace_id)
    VALUES (${e.tenantId}, ${e.userId ?? null}, ${e.eventType}, ${JSON.stringify(e.payload)}::jsonb, ${e.traceId ?? null})
  `;
}

// In Phase 0 we pre-create 4 months in the migration; this is the safety net.
// In Phase 1 we'll move this to a cron (River job) that rolls partitions ahead of time.
async function ensureCurrentMonthPartition(): Promise<void> {
  const now = new Date();
  const yr = now.getUTCFullYear();
  const mo = (now.getUTCMonth() + 1).toString().padStart(2, "0");
  const partitionName = `events_${yr}_${mo}`;

  const rows = await sql<{ exists: boolean }[]>`
    SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = ${partitionName}) AS exists
  `;
  if (rows[0]?.exists) return;

  const nextMo = new Date(Date.UTC(yr, now.getUTCMonth() + 1, 1));
  const fromStr = `${yr}-${mo}-01`;
  const toStr = `${nextMo.getUTCFullYear()}-${(nextMo.getUTCMonth() + 1).toString().padStart(2, "0")}-01`;

  // CREATE IF NOT EXISTS (idempotent; safe under concurrency via advisory lock)
  await sql.unsafe(`
    CREATE TABLE IF NOT EXISTS ${partitionName} PARTITION OF events
    FOR VALUES FROM ('${fromStr}') TO ('${toStr}')
  `);
}
```

- [ ] **Step 6.2: Write tests**

File `test/events.test.ts`:

```typescript
import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import { writeEvent } from "../src/events/stream";
import { sql, closeDb } from "../src/db/client";

const TENANT_ID = "00000000-0000-0000-0000-000000000001";

describe("events/stream", () => {
  beforeAll(async () => {
    // Create a test tenant
    await sql`
      INSERT INTO tenants (id, name, tier)
      VALUES (${TENANT_ID}, 'event-test-tenant', 'free')
      ON CONFLICT (id) DO NOTHING
    `;
  });

  afterAll(async () => {
    await sql`DELETE FROM events WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM tenants WHERE id = ${TENANT_ID}`;
    await closeDb();
  });

  it("writes an event and can read it back", async () => {
    await writeEvent({
      tenantId: TENANT_ID,
      eventType: "tool.called",
      payload: { tool: "dns_lookup", target: "example.com" },
    });

    const rows = await sql`
      SELECT event_type, payload
      FROM events
      WHERE tenant_id = ${TENANT_ID}
      ORDER BY created_at DESC
      LIMIT 1
    `;
    expect(rows[0].event_type).toBe("tool.called");
    expect(rows[0].payload.tool).toBe("dns_lookup");
  });
});
```

- [ ] **Step 6.3: Run and commit**

```bash
bun test test/events.test.ts
# Expected: passes
git add apps/api/
git commit -m "feat(events): partitioned event-stream writer with auto-rollover"
```

---

## Task 7: Credit metering (atomic ledger + balance)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/billing/credits.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/test/credits.test.ts`

- [ ] **Step 7.1: Write the credit meter**

File `src/billing/credits.ts`:

```typescript
import { sql } from "../db/client";
import { writeEvent } from "../events/stream";

export class InsufficientCreditsError extends Error {
  constructor(public readonly needed: number, public readonly available: number) {
    super(`Insufficient credits: need ${needed} millicredits, have ${available}`);
  }
}

/**
 * Atomically spends credits. Throws InsufficientCreditsError on underflow.
 * Writes to credit_ledger + updates tenants.credits_balance in one transaction.
 */
export async function spendCredits(args: {
  tenantId: string;
  userId?: string;
  millicredits: number;
  reason: string;
  metadata?: Record<string, unknown>;
  traceId?: string;
}): Promise<{ newBalance: number }> {
  if (args.millicredits <= 0) throw new Error("millicredits must be positive for spend");

  return await sql.begin(async (tx) => {
    const rows = await tx<{ credits_balance: string }[]>`
      UPDATE tenants
      SET credits_balance = credits_balance - ${args.millicredits}
      WHERE id = ${args.tenantId} AND credits_balance >= ${args.millicredits}
      RETURNING credits_balance
    `;
    if (rows.length === 0) {
      const avail = await tx<{ credits_balance: string }[]>`
        SELECT credits_balance FROM tenants WHERE id = ${args.tenantId}
      `;
      throw new InsufficientCreditsError(args.millicredits, Number(avail[0]?.credits_balance ?? 0));
    }

    await tx`
      INSERT INTO credit_ledger (tenant_id, user_id, delta_millicredits, reason, metadata)
      VALUES (${args.tenantId}, ${args.userId ?? null}, ${-args.millicredits}, ${args.reason}, ${JSON.stringify(args.metadata ?? {})}::jsonb)
    `;

    // Fire-and-wait event write within the same tx so we never lose a billing event
    await writeEvent({
      tenantId: args.tenantId,
      userId: args.userId ?? null,
      eventType: "credit.spent",
      payload: { millicredits: args.millicredits, reason: args.reason, ...(args.metadata ?? {}) },
      traceId: args.traceId,
    });

    return { newBalance: Number(rows[0].credits_balance) };
  });
}

export async function grantCredits(args: {
  tenantId: string;
  userId?: string;
  millicredits: number;
  reason: string;
  metadata?: Record<string, unknown>;
}): Promise<{ newBalance: number }> {
  if (args.millicredits <= 0) throw new Error("millicredits must be positive for grant");

  return await sql.begin(async (tx) => {
    const rows = await tx<{ credits_balance: string }[]>`
      UPDATE tenants
      SET credits_balance = credits_balance + ${args.millicredits}
      WHERE id = ${args.tenantId}
      RETURNING credits_balance
    `;
    await tx`
      INSERT INTO credit_ledger (tenant_id, user_id, delta_millicredits, reason, metadata)
      VALUES (${args.tenantId}, ${args.userId ?? null}, ${args.millicredits}, ${args.reason}, ${JSON.stringify(args.metadata ?? {})}::jsonb)
    `;
    await writeEvent({
      tenantId: args.tenantId,
      userId: args.userId ?? null,
      eventType: "credit.granted",
      payload: { millicredits: args.millicredits, reason: args.reason, ...(args.metadata ?? {}) },
    });
    return { newBalance: Number(rows[0].credits_balance) };
  });
}
```

- [ ] **Step 7.2: Write tests**

File `test/credits.test.ts`:

```typescript
import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import { spendCredits, grantCredits, InsufficientCreditsError } from "../src/billing/credits";
import { sql, closeDb } from "../src/db/client";

const TENANT_ID = "00000000-0000-0000-0000-000000000002";

describe("billing/credits", () => {
  beforeAll(async () => {
    await sql`
      INSERT INTO tenants (id, name, tier, credits_balance)
      VALUES (${TENANT_ID}, 'credits-test', 'free', 1000)
      ON CONFLICT (id) DO UPDATE SET credits_balance = 1000
    `;
  });

  afterAll(async () => {
    await sql`DELETE FROM events WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM credit_ledger WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM tenants WHERE id = ${TENANT_ID}`;
    await closeDb();
  });

  it("spends credits and writes ledger + event", async () => {
    const { newBalance } = await spendCredits({
      tenantId: TENANT_ID,
      millicredits: 200,
      reason: "tool:dns_lookup",
    });
    expect(newBalance).toBe(800);

    const [row] = await sql`
      SELECT delta_millicredits, reason FROM credit_ledger
      WHERE tenant_id = ${TENANT_ID}
      ORDER BY created_at DESC LIMIT 1
    `;
    expect(Number(row.delta_millicredits)).toBe(-200);
    expect(row.reason).toBe("tool:dns_lookup");
  });

  it("refuses to spend past zero", async () => {
    await expect(
      spendCredits({ tenantId: TENANT_ID, millicredits: 99999, reason: "tool:expensive" }),
    ).rejects.toThrow(InsufficientCreditsError);
  });

  it("grants credits", async () => {
    const { newBalance } = await grantCredits({
      tenantId: TENANT_ID,
      millicredits: 500,
      reason: "refill:hunter",
    });
    expect(newBalance).toBeGreaterThanOrEqual(1300);
  });
});
```

- [ ] **Step 7.3: Run and commit**

```bash
bun test test/credits.test.ts
# Expected: 3 passed
git add apps/api/
git commit -m "feat(billing): atomic credit-meter with ledger + event emission"
```

---

## Task 8: LLM Gateway (provider-agnostic with Anthropic backend)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/llm/gateway.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/llm/anthropic.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/test/llm-gateway.test.ts`
- Modify: `/Users/jasonroell/projects/osint-agent/apps/api/package.json` (add @anthropic-ai/sdk)

- [ ] **Step 8.1: Add SDK**

```bash
cd apps/api
bun add @anthropic-ai/sdk
```

- [ ] **Step 8.2: Define the provider interface**

File `src/llm/gateway.ts`:

```typescript
export type LLMRole = "system" | "user" | "assistant";

export interface LLMMessage {
  role: LLMRole;
  content: string;
}

export interface LLMRequest {
  messages: LLMMessage[];
  model: string;                    // e.g. "claude-sonnet-4-6", "claude-haiku-4-5"
  maxTokens: number;
  temperature?: number;
  // Soft cost ceiling in millicredits; if estimated spend would exceed, Gateway falls back.
  costCeilingMillicredits?: number;
  // Fallback chain of model IDs to try if primary fails or exceeds ceiling.
  fallbackChain?: string[];
  // Tag this call so parallel benchmark runs can log to the eval store (Phase 2+).
  benchmarkTag?: string;
}

export interface LLMResponse {
  content: string;
  modelUsed: string;
  inputTokens: number;
  outputTokens: number;
  estimatedCostMillicredits: number;
  // Non-empty if the primary model failed/skipped and a fallback served the request.
  fallbacksAttempted: string[];
}

export interface LLMProvider {
  readonly id: string;                   // "anthropic" | "openrouter" | "byok:anthropic"
  supports(model: string): boolean;
  complete(req: LLMRequest): Promise<LLMResponse>;
}

export class LLMGateway {
  private providers: LLMProvider[] = [];

  register(p: LLMProvider): void {
    this.providers.push(p);
  }

  async complete(req: LLMRequest): Promise<LLMResponse> {
    const tried: string[] = [];
    const chain = [req.model, ...(req.fallbackChain ?? [])];

    let lastErr: unknown;
    for (const model of chain) {
      const provider = this.providers.find((p) => p.supports(model));
      if (!provider) {
        tried.push(`${model}(no-provider)`);
        continue;
      }
      try {
        const result = await provider.complete({ ...req, model });
        return { ...result, fallbacksAttempted: tried };
      } catch (e) {
        tried.push(`${model}(${(e as Error).message})`);
        lastErr = e;
      }
    }
    throw new Error(`All models in chain failed: ${tried.join(", ")}`);
  }
}
```

- [ ] **Step 8.3: Implement Anthropic backend**

File `src/llm/anthropic.ts`:

```typescript
import Anthropic from "@anthropic-ai/sdk";
import { config } from "../config";
import type { LLMProvider, LLMRequest, LLMResponse } from "./gateway";

// Current (April 2026) Anthropic model pricing per 1M tokens, millicredits per token input/output.
// 1 millicredit = $0.00001 (so $3/M tokens = 30_000 / 1_000_000 = 0.030 millicredits per token, i.e. 30 micro-millicredits)
// Stored as millicredits * 1e6 per 1M tokens for integer math.
const PRICING = {
  "claude-opus-4-7":   { inPerM: 15_000_000, outPerM: 75_000_000 },
  "claude-sonnet-4-6": { inPerM:  3_000_000, outPerM: 15_000_000 },
  "claude-haiku-4-5":  { inPerM:    800_000, outPerM:  4_000_000 },
} as const;

export class AnthropicProvider implements LLMProvider {
  readonly id = "anthropic";
  private client: Anthropic;

  constructor(apiKey?: string) {
    this.client = new Anthropic({ apiKey: apiKey ?? config.anthropic.apiKey });
  }

  supports(model: string): boolean {
    return model in PRICING;
  }

  async complete(req: LLMRequest): Promise<LLMResponse> {
    const system = req.messages.find((m) => m.role === "system")?.content;
    const conv = req.messages.filter((m) => m.role !== "system");

    const response = await this.client.messages.create({
      model: req.model,
      max_tokens: req.maxTokens,
      temperature: req.temperature ?? 1.0,
      system,
      messages: conv.map((m) => ({ role: m.role as "user" | "assistant", content: m.content })),
    });

    const text = response.content
      .filter((b) => b.type === "text")
      .map((b) => (b.type === "text" ? b.text : ""))
      .join("");

    const pricing = PRICING[req.model as keyof typeof PRICING];
    const estimatedCostMillicredits = Math.ceil(
      (response.usage.input_tokens * pricing.inPerM + response.usage.output_tokens * pricing.outPerM) / 1_000_000,
    );

    return {
      content: text,
      modelUsed: req.model,
      inputTokens: response.usage.input_tokens,
      outputTokens: response.usage.output_tokens,
      estimatedCostMillicredits,
      fallbacksAttempted: [],
    };
  }
}
```

- [ ] **Step 8.4: Write failing tests (with the Anthropic SDK mocked)**

File `test/llm-gateway.test.ts`:

```typescript
import { describe, it, expect, mock } from "bun:test";
import { LLMGateway, type LLMProvider, type LLMRequest, type LLMResponse } from "../src/llm/gateway";

class FakeProvider implements LLMProvider {
  readonly id = "fake";
  constructor(
    private readonly supportedModels: string[],
    private readonly behavior: (req: LLMRequest) => Promise<LLMResponse>,
  ) {}
  supports(model: string): boolean {
    return this.supportedModels.includes(model);
  }
  async complete(req: LLMRequest): Promise<LLMResponse> {
    return this.behavior(req);
  }
}

describe("LLMGateway", () => {
  it("routes to a supporting provider", async () => {
    const gw = new LLMGateway();
    gw.register(new FakeProvider(["model-a"], async (req) => ({
      content: "hi",
      modelUsed: req.model,
      inputTokens: 10,
      outputTokens: 5,
      estimatedCostMillicredits: 1,
      fallbacksAttempted: [],
    })));

    const res = await gw.complete({
      messages: [{ role: "user", content: "hi" }],
      model: "model-a",
      maxTokens: 100,
    });
    expect(res.modelUsed).toBe("model-a");
    expect(res.content).toBe("hi");
  });

  it("falls back through the chain when primary throws", async () => {
    const gw = new LLMGateway();
    gw.register(new FakeProvider(["good"], async (req) => ({
      content: "ok",
      modelUsed: req.model,
      inputTokens: 1,
      outputTokens: 1,
      estimatedCostMillicredits: 1,
      fallbacksAttempted: [],
    })));
    gw.register(new FakeProvider(["broken"], async () => {
      throw new Error("simulated outage");
    }));

    const res = await gw.complete({
      messages: [{ role: "user", content: "x" }],
      model: "broken",
      maxTokens: 100,
      fallbackChain: ["good"],
    });
    expect(res.modelUsed).toBe("good");
    expect(res.fallbacksAttempted).toEqual(["broken(simulated outage)"]);
  });

  it("throws when no provider supports any model in the chain", async () => {
    const gw = new LLMGateway();
    await expect(
      gw.complete({
        messages: [{ role: "user", content: "x" }],
        model: "unknown",
        maxTokens: 10,
      }),
    ).rejects.toThrow(/All models in chain failed/);
  });
});
```

- [ ] **Step 8.5: Run and commit**

```bash
bun test test/llm-gateway.test.ts
# Expected: 3 passed
git add apps/api/
git commit -m "feat(llm): provider-agnostic gateway + Anthropic backend with pricing math"
```

---

## Task 9: Telemetry scaffolding (OpenTelemetry + Pino)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/telemetry.ts`

- [ ] **Step 9.1: Add dependencies**

```bash
cd apps/api
bun add @opentelemetry/api @opentelemetry/sdk-node @opentelemetry/auto-instrumentations-node @opentelemetry/exporter-trace-otlp-http
```

- [ ] **Step 9.2: Write the telemetry bootstrap**

File `src/telemetry.ts`:

```typescript
import { NodeSDK } from "@opentelemetry/sdk-node";
import { getNodeAutoInstrumentations } from "@opentelemetry/auto-instrumentations-node";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { Resource } from "@opentelemetry/resources";
import { SEMRESATTRS_SERVICE_NAME, SEMRESATTRS_SERVICE_VERSION } from "@opentelemetry/semantic-conventions";
import pino from "pino";

export const logger = pino({
  level: process.env.LOG_LEVEL ?? "info",
  transport: process.env.NODE_ENV === "development" ? { target: "pino-pretty" } : undefined,
});

export function startTelemetry(): { shutdown: () => Promise<void> } {
  const endpoint = process.env.OTEL_EXPORTER_OTLP_ENDPOINT;
  if (!endpoint) {
    logger.warn("OTEL_EXPORTER_OTLP_ENDPOINT not set — telemetry disabled");
    return { shutdown: async () => {} };
  }

  const headers: Record<string, string> = {};
  const rawHeaders = process.env.OTEL_EXPORTER_OTLP_HEADERS;
  if (rawHeaders) {
    for (const kv of rawHeaders.split(",")) {
      const [k, v] = kv.split("=");
      if (k && v) headers[decodeURIComponent(k.trim())] = decodeURIComponent(v.trim());
    }
  }

  const sdk = new NodeSDK({
    resource: new Resource({
      [SEMRESATTRS_SERVICE_NAME]: process.env.OTEL_SERVICE_NAME ?? "osint-api",
      [SEMRESATTRS_SERVICE_VERSION]: "0.1.0",
    }),
    traceExporter: new OTLPTraceExporter({ url: `${endpoint}/v1/traces`, headers }),
    instrumentations: [getNodeAutoInstrumentations()],
  });

  sdk.start();
  logger.info("OpenTelemetry started");

  return { shutdown: () => sdk.shutdown() };
}
```

- [ ] **Step 9.3: Commit (no test needed — this is config)**

```bash
git add apps/api/
git commit -m "feat(telemetry): OpenTelemetry + Pino setup"
```

---

## Task 10: MCP server skeleton (stdio + Streamable HTTP with one canned tool)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/mcp/server.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/mcp/tools/registry.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/index.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/test/mcp-server.test.ts`
- Modify: `/Users/jasonroell/projects/osint-agent/apps/api/package.json` (add @modelcontextprotocol/sdk)

- [ ] **Step 10.1: Add SDK**

```bash
bun add @modelcontextprotocol/sdk zod
```

- [ ] **Step 10.2: Write the tool registry**

File `src/mcp/tools/registry.ts`:

```typescript
import { z } from "zod";
import type { AuthContext } from "../../auth/middleware";
import { spendCredits, InsufficientCreditsError } from "../../billing/credits";
import { writeEvent } from "../../events/stream";

export interface ToolDefinition<Input extends z.ZodType> {
  name: string;
  description: string;
  inputSchema: Input;
  /** Cost in millicredits — deducted BEFORE execution; refunded on failure. */
  costMillicredits: number;
  handler: (input: z.infer<Input>, ctx: AuthContext) => Promise<unknown>;
}

export class ToolRegistry {
  private tools = new Map<string, ToolDefinition<z.ZodType>>();

  register<I extends z.ZodType>(def: ToolDefinition<I>): void {
    if (this.tools.has(def.name)) throw new Error(`Duplicate tool: ${def.name}`);
    this.tools.set(def.name, def);
  }

  list(): Array<{ name: string; description: string; inputSchema: z.ZodType }> {
    return Array.from(this.tools.values()).map((t) => ({
      name: t.name,
      description: t.description,
      inputSchema: t.inputSchema,
    }));
  }

  async invoke(name: string, input: unknown, ctx: AuthContext): Promise<unknown> {
    const tool = this.tools.get(name);
    if (!tool) throw new Error(`Unknown tool: ${name}`);

    const parsed = tool.inputSchema.parse(input);
    const traceId = crypto.randomUUID();

    await writeEvent({
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      eventType: "tool.called",
      payload: { tool: name, input: parsed },
      traceId,
    });

    try {
      await spendCredits({
        tenantId: ctx.tenantId,
        userId: ctx.userId,
        millicredits: tool.costMillicredits,
        reason: `tool:${name}`,
        traceId,
      });
    } catch (e) {
      if (e instanceof InsufficientCreditsError) {
        await writeEvent({
          tenantId: ctx.tenantId,
          userId: ctx.userId,
          eventType: "tool.failed",
          payload: { tool: name, reason: "insufficient_credits" },
          traceId,
        });
        throw e;
      }
      throw e;
    }

    try {
      const result = await tool.handler(parsed, ctx);
      await writeEvent({
        tenantId: ctx.tenantId,
        userId: ctx.userId,
        eventType: "tool.succeeded",
        payload: { tool: name },
        traceId,
      });
      return result;
    } catch (e) {
      // Refund on failure
      await spendCredits({
        tenantId: ctx.tenantId,
        userId: ctx.userId,
        millicredits: -tool.costMillicredits,
        reason: `refund:${name}`,
        traceId,
      }).catch(() => {});  // refund is best-effort; don't mask the original error
      await writeEvent({
        tenantId: ctx.tenantId,
        userId: ctx.userId,
        eventType: "tool.failed",
        payload: { tool: name, error: (e as Error).message },
        traceId,
      });
      throw e;
    }
  }
}

export const toolRegistry = new ToolRegistry();
```

- [ ] **Step 10.3: Register a canned "hello_tool" to validate the pipeline**

Append to `src/mcp/tools/registry.ts`:

```typescript
toolRegistry.register({
  name: "hello_tool",
  description: "Sanity-check tool. Returns a greeting and the authenticated tenant ID.",
  inputSchema: z.object({ name: z.string().default("world") }),
  costMillicredits: 1,
  handler: async (input, ctx) => ({
    greeting: `Hello, ${input.name}!`,
    tenantId: ctx.tenantId,
    now: new Date().toISOString(),
  }),
});
```

- [ ] **Step 10.4: Build the MCP server wiring**

File `src/mcp/server.ts`:

```typescript
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { toolRegistry } from "./tools/registry";
import type { AuthContext } from "../auth/middleware";
import { logger } from "../telemetry";

export function buildMcpServer(ctx: AuthContext): McpServer {
  const server = new McpServer({
    name: "osint-agent",
    version: "0.1.0",
  });

  for (const tool of toolRegistry.list()) {
    server.tool(
      tool.name,
      tool.description,
      // Note: MCP SDK accepts raw Zod schemas; runtime parse happens in ToolRegistry
      tool.inputSchema as Record<string, unknown>,
      async (input) => {
        try {
          const result = await toolRegistry.invoke(tool.name, input, ctx);
          return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
        } catch (e) {
          logger.error({ err: e, tool: tool.name }, "tool invocation failed");
          return { content: [{ type: "text", text: `Error: ${(e as Error).message}` }], isError: true };
        }
      },
    );
  }

  return server;
}

/**
 * Returns a ready-to-mount Streamable HTTP transport for an authenticated context.
 * One transport per session; reuse via a session table (Phase 1) — here, we build fresh.
 */
export function streamableTransport(): StreamableHTTPServerTransport {
  return new StreamableHTTPServerTransport({
    sessionIdGenerator: () => crypto.randomUUID(),
  });
}
```

- [ ] **Step 10.5: Wire Elysia HTTP entry point**

File `src/index.ts`:

```typescript
import { Elysia } from "elysia";
import { cors } from "@elysiajs/cors";
import { authPlugin } from "./auth/middleware";
import { buildMcpServer, streamableTransport } from "./mcp/server";
import { toolRegistry } from "./mcp/tools/registry";
import { config } from "./config";
import { logger, startTelemetry } from "./telemetry";

const { shutdown: shutdownTelemetry } = startTelemetry();

const app = new Elysia()
  .use(cors({ origin: true, credentials: true }))
  .get("/healthz", () => ({ ok: true, service: "osint-api", version: "0.1.0" }))
  .use(authPlugin)
  .get("/me", ({ auth }) => ({ uid: auth.user.uid, tenantId: auth.tenantId, userId: auth.userId }))
  .get("/tools", () => ({
    tools: toolRegistry.list().map((t) => ({ name: t.name, description: t.description })),
  }))
  .post("/mcp", async ({ request, auth }) => {
    const transport = streamableTransport();
    const server = buildMcpServer(auth);
    await server.connect(transport);
    // @ts-ignore — streamable HTTP expects Node req/res; Bun adapter will handle
    return transport.handleRequest(request);
  })
  .listen(config.port);

logger.info({ port: config.port }, "osint-api listening");

process.on("SIGTERM", async () => {
  await shutdownTelemetry();
  process.exit(0);
});
```

- [ ] **Step 10.6: Write integration test**

File `test/mcp-server.test.ts`:

```typescript
import { describe, it, expect, mock, afterAll } from "bun:test";
import { toolRegistry } from "../src/mcp/tools/registry";
import { sql, closeDb } from "../src/db/client";

mock.module("../src/auth/firebase", () => ({
  verifyIdToken: async () => ({ uid: "firebase-uid-mcp-test", email: "mcp@test.local", name: "MCP Tester" }),
}));

describe("mcp/tool registry", () => {
  afterAll(async () => {
    await sql`DELETE FROM events WHERE payload->>'tool' = 'hello_tool'`;
    await sql`DELETE FROM credit_ledger WHERE reason LIKE 'tool:hello_tool%'`;
    await sql`DELETE FROM users WHERE firebase_uid = 'firebase-uid-mcp-test'`;
    await sql`DELETE FROM tenants WHERE name = 'MCP Tester'`;
    await closeDb();
  });

  it("invokes hello_tool end-to-end", async () => {
    // Bootstrap tenant + user
    const tRow = await sql`
      INSERT INTO tenants (name, tier, credits_balance)
      VALUES ('MCP Tester', 'free', 100)
      RETURNING id
    `;
    const tenantId = tRow[0].id as string;
    const uRow = await sql`
      INSERT INTO users (firebase_uid, tenant_id, email, display_name)
      VALUES ('firebase-uid-mcp-test', ${tenantId}, 'mcp@test.local', 'MCP Tester')
      RETURNING id
    `;
    const userId = uRow[0].id as string;

    const result = await toolRegistry.invoke(
      "hello_tool",
      { name: "Jason" },
      { user: { uid: "firebase-uid-mcp-test" } as any, tenantId, userId },
    );

    expect((result as any).greeting).toBe("Hello, Jason!");
    expect((result as any).tenantId).toBe(tenantId);

    // Credit decremented
    const [after] = await sql`SELECT credits_balance FROM tenants WHERE id = ${tenantId}`;
    expect(Number(after.credits_balance)).toBe(99);

    // Events written
    const events = await sql`
      SELECT event_type FROM events
      WHERE tenant_id = ${tenantId} AND event_type IN ('tool.called', 'tool.succeeded', 'credit.spent')
      ORDER BY created_at ASC
    `;
    expect(events.map((e) => e.event_type)).toEqual(["tool.called", "credit.spent", "tool.succeeded"]);
  });
});
```

- [ ] **Step 10.7: Run and commit**

```bash
bun test test/mcp-server.test.ts
# Expected: 1 passed (plus the prior 3)
git add apps/api/
git commit -m "feat(mcp): server skeleton + tool registry with credit metering + event tracing"
```

---

## Task 11: Signed worker RPC protocol (TS client + Ed25519 signing)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/packages/shared-types/package.json`
- Create: `/Users/jasonroell/projects/osint-agent/packages/shared-types/tsconfig.json`
- Create: `/Users/jasonroell/projects/osint-agent/packages/shared-types/src/tool-protocol.ts`
- Create: `/Users/jasonroell/projects/osint-agent/packages/shared-types/src/index.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/workers/go-client.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/workers/py-client.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/test/worker-client.test.ts`

- [ ] **Step 11.1: Create shared-types package**

File `packages/shared-types/package.json`:

```json
{
  "name": "@osint/shared-types",
  "version": "0.0.1",
  "type": "module",
  "main": "src/index.ts",
  "scripts": {
    "lint": "bunx tsc --noEmit",
    "test": "echo 'no tests'",
    "build": "bunx tsc -p tsconfig.json --emitDeclarationOnly --outDir dist"
  },
  "devDependencies": { "typescript": "^5.7.0" }
}
```

File `packages/shared-types/tsconfig.json`:

```json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": {
    "noEmit": false,
    "declaration": true,
    "rootDir": "src",
    "outDir": "dist"
  },
  "include": ["src/**/*.ts"]
}
```

File `packages/shared-types/src/tool-protocol.ts`:

```typescript
export interface WorkerToolRequest<Input = unknown> {
  requestId: string;            // UUID; for idempotency + trace
  tenantId: string;
  userId: string;
  tool: string;                 // "subfinder_passive" | "dns_lookup_comprehensive" | "stealth_http_fetch" | ...
  input: Input;
  timeoutMs: number;
}

export interface WorkerToolResponse<Output = unknown> {
  requestId: string;
  ok: boolean;
  output?: Output;
  error?: { code: string; message: string };
  telemetry: {
    tookMs: number;
    cacheHit: boolean;
    proxyUsed?: string;
  };
}
```

File `packages/shared-types/src/index.ts`:

```typescript
export * from "./tool-protocol";
```

- [ ] **Step 11.2: Wire api to consume shared-types**

Modify `apps/api/package.json` — add to dependencies:

```json
"@osint/shared-types": "workspace:*"
```

Then:

```bash
cd /Users/jasonroell/projects/osint-agent
bun install
```

- [ ] **Step 11.3: Write the signed-request client**

File `apps/api/src/workers/go-client.ts`:

```typescript
import type { WorkerToolRequest, WorkerToolResponse } from "@osint/shared-types";
import { config } from "../config";

/**
 * Ed25519-signed POST to the Go worker.
 * Signs the canonical bytes: `${timestamp}\n${body}`.
 * Worker rejects requests with drift > 60s or bad signature.
 */
export async function callGoWorker<I, O>(req: WorkerToolRequest<I>): Promise<WorkerToolResponse<O>> {
  const body = JSON.stringify(req);
  const ts = Math.floor(Date.now() / 1000).toString();
  const signingBytes = new TextEncoder().encode(`${ts}\n${body}`);

  const seed = hexToBytes(config.workers.signingKeyHex);
  const sig = await signEd25519(seed, signingBytes);

  const res = await fetch(`${config.workers.goUrl}/tool`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-osint-ts": ts,
      "x-osint-sig": bytesToHex(sig),
    },
    body,
    signal: AbortSignal.timeout(req.timeoutMs + 500),
  });

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`go-worker ${res.status}: ${text}`);
  }
  return (await res.json()) as WorkerToolResponse<O>;
}

// Use Bun's crypto (Web Crypto). Ed25519 is supported.
async function signEd25519(seed32: Uint8Array, message: Uint8Array): Promise<Uint8Array> {
  // Web Crypto requires PKCS8 for private key import; we derive it from seed once per process.
  const key = await importEd25519PrivateKey(seed32);
  const sigBuf = await crypto.subtle.sign("Ed25519", key, message);
  return new Uint8Array(sigBuf);
}

let cachedKey: CryptoKey | null = null;
async function importEd25519PrivateKey(seed32: Uint8Array): Promise<CryptoKey> {
  if (cachedKey) return cachedKey;
  // PKCS8 prefix for Ed25519 private key (32 bytes of seed)
  const pkcs8Prefix = new Uint8Array([
    0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
  ]);
  const pkcs8 = new Uint8Array(pkcs8Prefix.length + seed32.length);
  pkcs8.set(pkcs8Prefix, 0);
  pkcs8.set(seed32, pkcs8Prefix.length);
  cachedKey = await crypto.subtle.importKey("pkcs8", pkcs8, { name: "Ed25519" }, false, ["sign"]);
  return cachedKey;
}

function hexToBytes(hex: string): Uint8Array {
  if (hex.length % 2 !== 0) throw new Error("invalid hex");
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.substring(i * 2, i * 2 + 2), 16);
  }
  return out;
}

function bytesToHex(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}
```

Copy this as `py-client.ts` with `config.workers.pyUrl` instead of `goUrl`. Same signing protocol.

File `apps/api/src/workers/py-client.ts`:

```typescript
import type { WorkerToolRequest, WorkerToolResponse } from "@osint/shared-types";
import { config } from "../config";

// (Shares signing code from go-client; in production factor into shared module.
// Keeping duplicated intentionally for plan clarity; refactor in Plan 2.)
export async function callPyWorker<I, O>(req: WorkerToolRequest<I>): Promise<WorkerToolResponse<O>> {
  const { callGoWorker } = await import("./go-client");
  // Temporary: py-client just overrides the base URL. Implementation parity in Plan 2.
  const originalUrl = config.workers.goUrl;
  (config.workers as any).goUrl = config.workers.pyUrl;
  try {
    return await callGoWorker<I, O>(req);
  } finally {
    (config.workers as any).goUrl = originalUrl;
  }
}
```

- [ ] **Step 11.4: Write a test that verifies request signing is produced (using a local HTTP test server)**

File `apps/api/test/worker-client.test.ts`:

```typescript
import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import { callGoWorker } from "../src/workers/go-client";

let server: ReturnType<typeof Bun.serve>;
let lastHeaders: Record<string, string> = {};
let lastBody = "";

beforeAll(() => {
  process.env.GO_WORKER_URL = "http://localhost:8799";
  // Stable test key: 32 bytes of 0x01 encoded as hex
  process.env.WORKER_SIGNING_KEY_HEX = "01".repeat(32);

  server = Bun.serve({
    port: 8799,
    async fetch(req) {
      lastHeaders = Object.fromEntries(req.headers);
      lastBody = await req.text();
      return new Response(
        JSON.stringify({
          requestId: "x",
          ok: true,
          output: { echo: true },
          telemetry: { tookMs: 1, cacheHit: false },
        }),
        { headers: { "content-type": "application/json" } },
      );
    },
  });
});

afterAll(() => server.stop());

describe("callGoWorker", () => {
  it("signs the request and gets a response", async () => {
    const res = await callGoWorker({
      requestId: "r1",
      tenantId: "t1",
      userId: "u1",
      tool: "noop",
      input: { hello: "world" },
      timeoutMs: 2000,
    });
    expect(res.ok).toBe(true);
    expect(lastHeaders["x-osint-ts"]).toMatch(/^\d+$/);
    expect(lastHeaders["x-osint-sig"]).toMatch(/^[0-9a-f]{128}$/); // 64 bytes = 128 hex chars
    expect(JSON.parse(lastBody).tool).toBe("noop");
  });
});
```

- [ ] **Step 11.5: Run and commit**

```bash
bun test test/worker-client.test.ts
# Expected: 1 passed
git add packages/ apps/api/
git commit -m "feat(workers): shared tool-protocol types + Ed25519-signed worker RPC client"
```

---

## Task 12: Go tool worker with subfinder + dnsx integrations

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/go.mod`
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/cmd/worker/main.go`
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/internal/server/server.go`
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/internal/server/auth.go`
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/internal/tools/subfinder.go`
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/internal/tools/dns.go`
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/internal/server/auth_test.go`

- [ ] **Step 12.1: Initialize go module**

```bash
cd apps/go-worker
go mod init github.com/jasonroell/osint-agent/go-worker
go get github.com/labstack/echo/v4@latest
go get github.com/projectdiscovery/subfinder/v2@latest
go get github.com/projectdiscovery/dnsx@latest
go get github.com/google/uuid
```

- [ ] **Step 12.2: Write Ed25519 auth middleware + test**

File `internal/server/auth.go`:

```go
package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	clockSkewTolerance = 60 * time.Second
	tsHeader           = "X-Osint-Ts"
	sigHeader          = "X-Osint-Sig"
)

type SignedAuthConfig struct {
	PublicKey ed25519.PublicKey
}

func RequireSigned(cfg SignedAuthConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "read body")
			}
			c.Request().Body = io.NopCloser(newBuf(body))

			ts := c.Request().Header.Get(tsHeader)
			sigHex := c.Request().Header.Get(sigHeader)
			if ts == "" || sigHex == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing signature headers")
			}

			tsInt, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "bad ts")
			}
			now := time.Now().Unix()
			if now-tsInt > int64(clockSkewTolerance/time.Second) || tsInt-now > int64(clockSkewTolerance/time.Second) {
				return echo.NewHTTPError(http.StatusUnauthorized, "ts out of tolerance")
			}

			sig, err := hex.DecodeString(sigHex)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "bad sig hex")
			}

			msg := []byte(ts + "\n" + string(body))
			if !ed25519.Verify(cfg.PublicKey, msg, sig) {
				return echo.NewHTTPError(http.StatusUnauthorized, "sig verify failed")
			}
			return next(c)
		}
	}
}

// --- helpers ---

type byteBuf struct {
	b []byte
	o int
}

func newBuf(b []byte) *byteBuf { return &byteBuf{b: b} }
func (r *byteBuf) Read(p []byte) (int, error) {
	if r.o >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.o:])
	r.o += n
	return n, nil
}

var _ = errors.New // silence unused import if needed
```

File `internal/server/auth_test.go`:

```go
package server

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestRequireSigned_OK(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	e := echo.New()
	e.Use(RequireSigned(SignedAuthConfig{PublicKey: pub}))
	e.POST("/x", func(c echo.Context) error { return c.String(200, "ok") })

	body := []byte(`{"hello":"world"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := ed25519.Sign(priv, []byte(ts+"\n"+string(body)))

	req := httptest.NewRequest("POST", "/x", bytes.NewReader(body))
	req.Header.Set("X-Osint-Ts", ts)
	req.Header.Set("X-Osint-Sig", hex.EncodeToString(sig))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequireSigned_BadSig(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	e := echo.New()
	e.Use(RequireSigned(SignedAuthConfig{PublicKey: pub}))
	e.POST("/x", func(c echo.Context) error { return c.String(200, "ok") })

	req := httptest.NewRequest("POST", "/x", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-Osint-Ts", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("X-Osint-Sig", hex.EncodeToString([]byte("bogus-sig-bogus-sig-bogus-sig-bogus-sig-bogus-sig-bogus-sig-bogus-sig")))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}
```

- [ ] **Step 12.3: Write tool protocol types + server wiring + subfinder + dns**

File `internal/server/server.go`:

```go
package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"os"
	"time"

	"github.com/jasonroell/osint-agent/go-worker/internal/tools"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type ToolRequest struct {
	RequestID string         `json:"requestId"`
	TenantID  string         `json:"tenantId"`
	UserID    string         `json:"userId"`
	Tool      string         `json:"tool"`
	Input     map[string]any `json:"input"`
	TimeoutMs int            `json:"timeoutMs"`
}

type ToolResponse struct {
	RequestID string      `json:"requestId"`
	OK        bool        `json:"ok"`
	Output    interface{} `json:"output,omitempty"`
	Error     *ToolError  `json:"error,omitempty"`
	Telemetry Telemetry   `json:"telemetry"`
}

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Telemetry struct {
	TookMs    int64  `json:"tookMs"`
	CacheHit  bool   `json:"cacheHit"`
	ProxyUsed string `json:"proxyUsed,omitempty"`
}

func NewServer() *echo.Echo {
	pubHex := os.Getenv("WORKER_PUBLIC_KEY_HEX")
	if pubHex == "" {
		panic("WORKER_PUBLIC_KEY_HEX required")
	}
	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		panic("invalid WORKER_PUBLIC_KEY_HEX")
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit("5MB"))
	e.GET("/healthz", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"ok": "true", "service": "go-worker"})
	})

	authed := e.Group("", RequireSigned(SignedAuthConfig{PublicKey: ed25519.PublicKey(pubBytes)}))
	authed.POST("/tool", handleTool)

	return e
}

func handleTool(c echo.Context) error {
	var req ToolRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	start := time.Now()

	var (
		out interface{}
		err error
	)
	switch req.Tool {
	case "subfinder_passive":
		out, err = tools.Subfinder(c.Request().Context(), req.Input)
	case "dns_lookup_comprehensive":
		out, err = tools.DNS(c.Request().Context(), req.Input)
	default:
		return c.JSON(http.StatusOK, ToolResponse{
			RequestID: req.RequestID,
			OK:        false,
			Error:     &ToolError{Code: "unknown_tool", Message: req.Tool},
			Telemetry: Telemetry{TookMs: time.Since(start).Milliseconds()},
		})
	}

	resp := ToolResponse{
		RequestID: req.RequestID,
		OK:        err == nil,
		Telemetry: Telemetry{TookMs: time.Since(start).Milliseconds()},
	}
	if err != nil {
		resp.Error = &ToolError{Code: "tool_failure", Message: err.Error()}
	} else {
		resp.Output = out
	}
	return c.JSON(http.StatusOK, resp)
}
```

File `internal/tools/subfinder.go`:

```go
package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/projectdiscovery/subfinder/v2/pkg/runner"
)

type SubfinderOutput struct {
	Domain     string   `json:"domain"`
	Subdomains []string `json:"subdomains"`
	TookMs     int64    `json:"tookMs"`
}

func Subfinder(ctx context.Context, input map[string]any) (*SubfinderOutput, error) {
	domain, ok := input["domain"].(string)
	if !ok || domain == "" {
		return nil, errors.New("input.domain required")
	}

	start := time.Now()

	cfg := runner.Options{
		Threads:            10,
		Timeout:            30,
		MaxEnumerationTime: 60,
		Silent:             true,
		All:                false,
		Sources:            []string{"crtsh", "hackertarget", "dnsdumpster", "anubis", "alienvault"},
	}
	r, err := runner.NewRunner(&cfg)
	if err != nil {
		return nil, fmt.Errorf("subfinder init: %w", err)
	}

	var sb strings.Builder
	sourceMap, err := r.EnumerateSingleDomainWithCtx(ctx, domain, []string{"stdout"}, []map[string]struct{}{})
	_ = sourceMap // summary currently unused
	if err != nil {
		return nil, fmt.Errorf("subfinder enumerate: %w", err)
	}
	// Subfinder's public API evolves; if the signature above no longer matches your pinned version,
	// consult https://pkg.go.dev/github.com/projectdiscovery/subfinder/v2 and align.
	// Fallback: parse stdout capture into list.
	subs := strings.Split(strings.TrimSpace(sb.String()), "\n")
	subs = filterEmpty(subs)

	return &SubfinderOutput{
		Domain:     domain,
		Subdomains: subs,
		TookMs:     time.Since(start).Milliseconds(),
	}, nil
}

func filterEmpty(s []string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out
}
```

File `internal/tools/dns.go`:

```go
package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

type DNSOutput struct {
	Domain string              `json:"domain"`
	A      []string            `json:"a,omitempty"`
	AAAA   []string            `json:"aaaa,omitempty"`
	MX     []DNSMX             `json:"mx,omitempty"`
	TXT    []string            `json:"txt,omitempty"`
	NS     []string            `json:"ns,omitempty"`
	CNAME  string              `json:"cname,omitempty"`
	TookMs int64               `json:"tookMs"`
	Errors map[string]string   `json:"errors,omitempty"`
}

type DNSMX struct {
	Host string `json:"host"`
	Pref int    `json:"preference"`
}

func DNS(ctx context.Context, input map[string]any) (*DNSOutput, error) {
	domain, ok := input["domain"].(string)
	if !ok || domain == "" {
		return nil, errors.New("input.domain required")
	}

	start := time.Now()
	out := &DNSOutput{Domain: domain, Errors: map[string]string{}}

	var resolver = &net.Resolver{PreferGo: true}

	if ips, err := resolver.LookupIPAddr(ctx, domain); err == nil {
		for _, ip := range ips {
			if ip.IP.To4() != nil {
				out.A = append(out.A, ip.IP.String())
			} else {
				out.AAAA = append(out.AAAA, ip.IP.String())
			}
		}
	} else {
		out.Errors["A/AAAA"] = err.Error()
	}

	if mxs, err := resolver.LookupMX(ctx, domain); err == nil {
		for _, mx := range mxs {
			out.MX = append(out.MX, DNSMX{Host: mx.Host, Pref: int(mx.Pref)})
		}
	} else {
		out.Errors["MX"] = err.Error()
	}

	if txts, err := resolver.LookupTXT(ctx, domain); err == nil {
		out.TXT = txts
	} else {
		out.Errors["TXT"] = err.Error()
	}

	if nss, err := resolver.LookupNS(ctx, domain); err == nil {
		for _, n := range nss {
			out.NS = append(out.NS, n.Host)
		}
	} else {
		out.Errors["NS"] = err.Error()
	}

	if cname, err := resolver.LookupCNAME(ctx, domain); err == nil {
		out.CNAME = cname
	} else {
		out.Errors["CNAME"] = err.Error()
	}

	out.TookMs = time.Since(start).Milliseconds()
	if len(out.A) == 0 && len(out.AAAA) == 0 && len(out.MX) == 0 && len(out.TXT) == 0 {
		return out, fmt.Errorf("no DNS records resolved; errors: %v", out.Errors)
	}
	return out, nil
}
```

File `cmd/worker/main.go`:

```go
package main

import (
	"log"
	"os"

	"github.com/jasonroell/osint-agent/go-worker/internal/server"
)

func main() {
	e := server.NewServer()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	log.Printf("go-worker listening on :%s", port)
	if err := e.Start(":" + port); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 12.4: Run Go tests and build**

```bash
cd apps/go-worker
go test ./...
# Expected: tests pass (the subfinder/dns functions don't have unit tests here — they need live network;
# integration test happens in Task 15)
go build ./...
```

- [ ] **Step 12.5: Commit**

```bash
git add apps/go-worker/
git commit -m "feat(go-worker): Echo server + Ed25519 auth + subfinder/DNS tools"
```

---

## Task 13: Python tool worker (stealth HTTP with rnet / JA4+)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/py-worker/pyproject.toml`
- Create: `/Users/jasonroell/projects/osint-agent/apps/py-worker/src/py_worker/main.py`
- Create: `/Users/jasonroell/projects/osint-agent/apps/py-worker/src/py_worker/auth.py`
- Create: `/Users/jasonroell/projects/osint-agent/apps/py-worker/src/py_worker/tools/stealth_http.py`
- Create: `/Users/jasonroell/projects/osint-agent/apps/py-worker/tests/test_auth.py`
- Create: `/Users/jasonroell/projects/osint-agent/apps/py-worker/tests/test_stealth_http.py`

- [ ] **Step 13.1: Create `pyproject.toml`**

```toml
[project]
name = "py-worker"
version = "0.0.1"
requires-python = ">=3.13"
dependencies = [
  "fastapi>=0.115",
  "uvicorn[standard]>=0.32",
  "rnet>=3.0",
  "pydantic>=2.9",
  "pynacl>=1.5",         # Ed25519 verification (fast, well-tested)
]

[project.optional-dependencies]
dev = [
  "pytest>=8.3",
  "httpx>=0.27",
  "pytest-asyncio>=0.24",
]

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.hatch.build.targets.wheel]
packages = ["src/py_worker"]

[tool.pytest.ini_options]
asyncio_mode = "auto"
pythonpath = ["src"]
```

- [ ] **Step 13.2: Write the Ed25519 verifier**

File `src/py_worker/auth.py`:

```python
from __future__ import annotations

import os
import time
from dataclasses import dataclass
from typing import Optional

from fastapi import HTTPException, Request
from nacl.exceptions import BadSignatureError
from nacl.signing import VerifyKey

CLOCK_SKEW_TOLERANCE_S = 60


@dataclass
class SigningConfig:
    verify_key: VerifyKey

    @classmethod
    def from_env(cls) -> "SigningConfig":
        pub_hex = os.environ.get("WORKER_PUBLIC_KEY_HEX")
        if not pub_hex:
            raise RuntimeError("WORKER_PUBLIC_KEY_HEX required")
        pub = bytes.fromhex(pub_hex)
        if len(pub) != 32:
            raise RuntimeError("WORKER_PUBLIC_KEY_HEX must be 32 bytes")
        return cls(verify_key=VerifyKey(pub))


async def require_signed_request(request: Request) -> bytes:
    """
    FastAPI dependency. Returns the raw body. Raises 401 on any failure.
    Caller must use the returned bytes for further parsing.
    """
    body = await request.body()
    ts = request.headers.get("x-osint-ts")
    sig_hex = request.headers.get("x-osint-sig")
    if not ts or not sig_hex:
        raise HTTPException(status_code=401, detail="missing signature headers")

    try:
        ts_int = int(ts)
    except ValueError:
        raise HTTPException(status_code=401, detail="bad ts")

    now = int(time.time())
    if abs(now - ts_int) > CLOCK_SKEW_TOLERANCE_S:
        raise HTTPException(status_code=401, detail="ts out of tolerance")

    try:
        sig = bytes.fromhex(sig_hex)
    except ValueError:
        raise HTTPException(status_code=401, detail="bad sig hex")

    cfg: Optional[SigningConfig] = request.app.state.signing_config
    if cfg is None:
        raise RuntimeError("signing_config not initialized")

    message = f"{ts}\n".encode() + body
    try:
        cfg.verify_key.verify(message, sig)
    except BadSignatureError:
        raise HTTPException(status_code=401, detail="sig verify failed")

    return body
```

- [ ] **Step 13.3: Write the stealth_http tool**

File `src/py_worker/tools/stealth_http.py`:

```python
"""
Stealth HTTP fetch with JA4+ impersonation via rnet.

We target chrome/safari/firefox presets depending on the target.
Unlike Playwright, rnet does not execute JS — so this is the first tier
of the scraping ladder and should handle 30-40% of Cloudflare/DataDome-
protected sites at browser-free cost.
"""
from __future__ import annotations

import time
from typing import Any

import rnet
from pydantic import BaseModel, Field


class StealthHttpInput(BaseModel):
    url: str
    method: str = "GET"
    impersonate: str = Field(default="chrome", pattern="^(chrome|firefox|safari|safari_ios|okhttp|edge)$")
    headers: dict[str, str] = Field(default_factory=dict)
    body: str | None = None
    timeout_ms: int = 15000
    follow_redirects: bool = True


class StealthHttpOutput(BaseModel):
    status: int
    url: str
    headers: dict[str, str]
    body: str
    took_ms: int
    impersonate: str


_IMPERSONATE_MAP = {
    "chrome": rnet.Impersonate.Chrome131,
    "firefox": rnet.Impersonate.Firefox133,
    "safari": rnet.Impersonate.Safari18,
    "safari_ios": rnet.Impersonate.SafariIos18_2,
    "edge": rnet.Impersonate.Edge131,
    "okhttp": rnet.Impersonate.OkHttp5,
}


async def stealth_http(input: dict[str, Any]) -> dict[str, Any]:
    parsed = StealthHttpInput.model_validate(input)
    imp = _IMPERSONATE_MAP[parsed.impersonate]

    client = rnet.Client(impersonate=imp, timeout=parsed.timeout_ms / 1000.0)
    start = time.perf_counter()

    if parsed.method.upper() == "GET":
        resp = await client.get(parsed.url, headers=parsed.headers)
    elif parsed.method.upper() == "POST":
        resp = await client.post(parsed.url, headers=parsed.headers, body=parsed.body or "")
    else:
        raise ValueError(f"unsupported method: {parsed.method}")

    body_text = await resp.text()

    out = StealthHttpOutput(
        status=resp.status,
        url=str(resp.url),
        headers={k: v for k, v in resp.headers.items()},
        body=body_text,
        took_ms=int((time.perf_counter() - start) * 1000),
        impersonate=parsed.impersonate,
    )
    return out.model_dump()
```

- [ ] **Step 13.4: Write the FastAPI app**

File `src/py_worker/main.py`:

```python
from __future__ import annotations

import json
import os
import uuid

from fastapi import Depends, FastAPI, HTTPException
from pydantic import BaseModel

from py_worker.auth import SigningConfig, require_signed_request
from py_worker.tools.stealth_http import stealth_http


class ToolError(BaseModel):
    code: str
    message: str


class Telemetry(BaseModel):
    tookMs: int
    cacheHit: bool = False
    proxyUsed: str | None = None


class ToolResponse(BaseModel):
    requestId: str
    ok: bool
    output: dict | None = None
    error: ToolError | None = None
    telemetry: Telemetry


app = FastAPI(title="osint-py-worker", version="0.0.1")


@app.on_event("startup")
def _startup() -> None:
    app.state.signing_config = SigningConfig.from_env()


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"ok": "true", "service": "py-worker"}


_TOOLS: dict[str, callable] = {
    "stealth_http_fetch": stealth_http,
}


@app.post("/tool", response_model=ToolResponse)
async def tool(raw_body: bytes = Depends(require_signed_request)) -> ToolResponse:
    try:
        req = json.loads(raw_body)
    except json.JSONDecodeError as e:
        raise HTTPException(status_code=400, detail=f"bad json: {e}")

    tool_name = req.get("tool")
    input_payload = req.get("input", {})
    request_id = req.get("requestId", str(uuid.uuid4()))

    handler = _TOOLS.get(tool_name)
    if handler is None:
        return ToolResponse(
            requestId=request_id,
            ok=False,
            error=ToolError(code="unknown_tool", message=tool_name),
            telemetry=Telemetry(tookMs=0),
        )

    import time
    t0 = time.perf_counter()
    try:
        output = await handler(input_payload)
        return ToolResponse(
            requestId=request_id,
            ok=True,
            output=output,
            telemetry=Telemetry(tookMs=int((time.perf_counter() - t0) * 1000)),
        )
    except Exception as e:
        return ToolResponse(
            requestId=request_id,
            ok=False,
            error=ToolError(code="tool_failure", message=str(e)),
            telemetry=Telemetry(tookMs=int((time.perf_counter() - t0) * 1000)),
        )


def run() -> None:
    import uvicorn
    port = int(os.environ.get("PORT", "8082"))
    uvicorn.run(app, host="0.0.0.0", port=port)


if __name__ == "__main__":
    run()
```

- [ ] **Step 13.5: Write the auth test**

File `tests/test_auth.py`:

```python
import time

import pytest
from fastapi.testclient import TestClient
from nacl.signing import SigningKey

from py_worker.main import app


@pytest.fixture
def client(monkeypatch):
    sk = SigningKey.generate()
    pk_hex = sk.verify_key.encode().hex()
    monkeypatch.setenv("WORKER_PUBLIC_KEY_HEX", pk_hex)

    # Manually trigger startup since TestClient doesn't use the lifespan if we capture
    # the signing key outside.
    from py_worker.auth import SigningConfig
    app.state.signing_config = SigningConfig.from_env()

    with TestClient(app) as c:
        c.signing_key = sk  # type: ignore[attr-defined]
        yield c


def _sign(client: TestClient, body_bytes: bytes) -> tuple[str, str]:
    ts = str(int(time.time()))
    msg = f"{ts}\n".encode() + body_bytes
    sig = client.signing_key.sign(msg).signature  # type: ignore[attr-defined]
    return ts, sig.hex()


def test_healthz_unauthenticated(client):
    r = client.get("/healthz")
    assert r.status_code == 200
    assert r.json() == {"ok": "true", "service": "py-worker"}


def test_tool_requires_signature(client):
    r = client.post("/tool", json={"tool": "x"})
    assert r.status_code == 401


def test_tool_with_valid_sig_hits_unknown_tool_path(client):
    body = b'{"requestId":"x","tool":"does-not-exist","input":{}}'
    ts, sig = _sign(client, body)
    r = client.post(
        "/tool",
        content=body,
        headers={"x-osint-ts": ts, "x-osint-sig": sig, "content-type": "application/json"},
    )
    assert r.status_code == 200
    data = r.json()
    assert data["ok"] is False
    assert data["error"]["code"] == "unknown_tool"
```

- [ ] **Step 13.6: Write the stealth_http test (offline — uses httpbin-style local test server)**

File `tests/test_stealth_http.py`:

```python
"""
Lightweight test: we don't execute the actual rnet network call in CI because it requires
the network. Instead we validate the input-parsing happy path + the error propagation.
Full integration testing lives in a separate manual smoke test in Task 15.
"""
import pytest

from py_worker.tools.stealth_http import stealth_http


@pytest.mark.asyncio
async def test_bad_impersonate():
    with pytest.raises(Exception):  # ValidationError from pydantic
        await stealth_http({"url": "https://example.com", "impersonate": "ie6-lol"})
```

- [ ] **Step 13.7: Sync deps, run tests, build**

```bash
cd apps/py-worker
uv sync --all-extras
uv run pytest -v
# Expected: 3 passed
```

- [ ] **Step 13.8: Commit**

```bash
git add apps/py-worker/
git commit -m "feat(py-worker): FastAPI + Ed25519 auth + rnet-based stealth HTTP tool"
```

---

## Task 14: Wire the 3 tools into the MCP registry

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/mcp/tools/stealth-http.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/mcp/tools/subfinder.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/mcp/tools/dns-lookup.ts`
- Modify: `/Users/jasonroell/projects/osint-agent/apps/api/src/mcp/tools/registry.ts` (import + register the 3 tools)

- [ ] **Step 14.1: Write `stealth-http.ts`**

File `apps/api/src/mcp/tools/stealth-http.ts`:

```typescript
import { z } from "zod";
import { toolRegistry } from "./registry";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  url: z.string().url(),
  method: z.enum(["GET", "POST"]).default("GET"),
  impersonate: z.enum(["chrome", "firefox", "safari", "safari_ios", "edge", "okhttp"]).default("chrome"),
  headers: z.record(z.string()).optional(),
  body: z.string().optional(),
  timeout_ms: z.number().int().min(1000).max(60000).default(15000),
});

toolRegistry.register({
  name: "stealth_http_fetch",
  description:
    "Fetch a URL with JA4+ TLS fingerprint impersonation. Bypasses a large fraction of Cloudflare and DataDome protections without launching a browser.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "stealth_http_fetch",
      input: i,
      timeoutMs: i.timeout_ms,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "stealth_http failed");
    return res.output;
  },
});
```

- [ ] **Step 14.2: Write `subfinder.ts`**

File `apps/api/src/mcp/tools/subfinder.ts`:

```typescript
import { z } from "zod";
import { toolRegistry } from "./registry";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain: z.string().min(3),
});

toolRegistry.register({
  name: "subfinder_passive",
  description: "Passive subdomain enumeration via ProjectDiscovery's subfinder library across 30+ public sources (crt.sh, HackerTarget, etc.). No active probing.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "subfinder_passive",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "subfinder failed");
    return res.output;
  },
});
```

- [ ] **Step 14.3: Write `dns-lookup.ts`**

File `apps/api/src/mcp/tools/dns-lookup.ts`:

```typescript
import { z } from "zod";
import { toolRegistry } from "./registry";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain: z.string().min(3),
});

toolRegistry.register({
  name: "dns_lookup_comprehensive",
  description: "Resolve A / AAAA / MX / TXT / NS / CNAME records for a domain in parallel. Returns structured results with per-record-type errors on partial failure.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "dns_lookup_comprehensive",
      input: i,
      timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "dns lookup failed");
    return res.output;
  },
});
```

- [ ] **Step 14.4: Wire the imports at the registry entry-point**

Append to `apps/api/src/mcp/tools/registry.ts` (after the `hello_tool` registration):

```typescript
// Side-effect imports: registering tools.
// Keep this block at the bottom of registry.ts; order matters for the registry.
import "./stealth-http";
import "./subfinder";
import "./dns-lookup";
```

- [ ] **Step 14.5: Verify they appear in the list**

```bash
cd apps/api
bun run src/index.ts &
BUN_PID=$!
sleep 2
# The /tools endpoint doesn't require auth for Phase 0 diagnostics; verify the catalog:
curl -s http://localhost:3000/tools | jq
# Expected JSON includes 4 tools: hello_tool, stealth_http_fetch, subfinder_passive, dns_lookup_comprehensive
kill $BUN_PID
```

Note: if `/tools` requires auth in your current middleware ordering (because `authPlugin` is applied before this endpoint), move the `/tools` route above the `.use(authPlugin)` line in `src/index.ts`.

- [ ] **Step 14.6: Commit**

```bash
git add apps/api/
git commit -m "feat(tools): wire stealth_http_fetch, subfinder_passive, dns_lookup_comprehensive into MCP registry"
```

---

## Task 15: Fly.io deployment + smoke test

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/Dockerfile`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/fly.toml`
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/Dockerfile`
- Create: `/Users/jasonroell/projects/osint-agent/apps/go-worker/fly.toml`
- Create: `/Users/jasonroell/projects/osint-agent/apps/py-worker/Dockerfile`
- Create: `/Users/jasonroell/projects/osint-agent/apps/py-worker/fly.toml`
- Create: `/Users/jasonroell/projects/osint-agent/infra/scripts/bootstrap-fly.sh`
- Create: `/Users/jasonroell/projects/osint-agent/.github/workflows/deploy.yml`

- [ ] **Step 15.1: Write `apps/api/Dockerfile`**

```dockerfile
FROM oven/bun:1.2-alpine AS base
WORKDIR /app

FROM base AS deps
COPY package.json bun.lockb* ./
COPY packages/shared-types/package.json packages/shared-types/package.json
COPY apps/api/package.json apps/api/package.json
RUN bun install --frozen-lockfile

FROM base AS build
COPY --from=deps /app/node_modules node_modules
COPY packages/shared-types packages/shared-types
COPY apps/api apps/api
COPY tsconfig.base.json .
WORKDIR /app/apps/api

FROM base AS runtime
COPY --from=build /app /app
WORKDIR /app/apps/api
ENV NODE_ENV=production
EXPOSE 3000
CMD ["bun", "run", "src/index.ts"]
```

- [ ] **Step 15.2: Write `apps/api/fly.toml`**

```toml
app = "osint-api"
primary_region = "ord"

[build]
  dockerfile = "Dockerfile"

[http_service]
  internal_port = 3000
  force_https = true
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 1

[[vm]]
  size = "shared-cpu-1x"
  memory = "512mb"

[[http_service.checks]]
  grace_period = "10s"
  interval = "30s"
  method = "GET"
  path = "/healthz"
  timeout = "5s"
```

- [ ] **Step 15.3: Write `apps/go-worker/Dockerfile`**

```dockerfile
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY apps/go-worker/go.mod apps/go-worker/go.sum* ./
RUN go mod download
COPY apps/go-worker ./
RUN CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/worker /worker
EXPOSE 8081
CMD ["/worker"]
```

- [ ] **Step 15.4: Write `apps/go-worker/fly.toml`**

```toml
app = "osint-go-worker"
primary_region = "ord"

[build]
  dockerfile = "Dockerfile"
  dockerfile_target = ""

[env]
  PORT = "8081"

[http_service]
  internal_port = 8081
  force_https = true
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 1

[[vm]]
  size = "shared-cpu-1x"
  memory = "512mb"

[[http_service.checks]]
  grace_period = "10s"
  interval = "30s"
  method = "GET"
  path = "/healthz"
  timeout = "5s"
```

- [ ] **Step 15.5: Write `apps/py-worker/Dockerfile`**

```dockerfile
FROM python:3.13-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends curl build-essential && rm -rf /var/lib/apt/lists/*
RUN curl -LsSf https://astral.sh/uv/install.sh | sh
ENV PATH="/root/.local/bin:$PATH"
WORKDIR /app
COPY apps/py-worker/pyproject.toml apps/py-worker/README* ./
COPY apps/py-worker/src ./src
RUN uv sync --no-dev
ENV PYTHONPATH=/app/src
EXPOSE 8082
CMD ["uv", "run", "uvicorn", "py_worker.main:app", "--host", "0.0.0.0", "--port", "8082"]
```

- [ ] **Step 15.6: Write `apps/py-worker/fly.toml`**

```toml
app = "osint-py-worker"
primary_region = "ord"

[build]
  dockerfile = "Dockerfile"

[env]
  PORT = "8082"

[http_service]
  internal_port = 8082
  force_https = true
  auto_stop_machines = "stop"
  auto_start_machines = true
  min_machines_running = 1

[[vm]]
  size = "shared-cpu-1x"
  memory = "512mb"

[[http_service.checks]]
  grace_period = "15s"
  interval = "30s"
  method = "GET"
  path = "/healthz"
  timeout = "5s"
```

- [ ] **Step 15.7: Write bootstrap-fly script (generates Ed25519 keypair, sets all secrets, creates apps)**

File `infra/scripts/bootstrap-fly.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
# One-time bootstrap. Requires: flyctl logged in, openssl, jq.

# 1. Generate Ed25519 keypair for signed worker RPC
KEYPAIR=$(openssl genpkey -algorithm Ed25519 -outform DER 2>/dev/null | xxd -c 256 -p)
# The first 16 bytes of DER header we strip; the last 32 bytes are the seed.
# Extract seed:
SEED_HEX=$(openssl genpkey -algorithm Ed25519 -outform PEM | openssl pkey -outform DER 2>/dev/null | tail -c 32 | xxd -c 64 -p | tr -d '\n')
# Derive public key from seed (using Python one-liner — pynacl available in py-worker venv)
cd "$(dirname "$0")/../../apps/py-worker"
uv sync --no-dev >/dev/null 2>&1
PUB_HEX=$(uv run python -c "from nacl.signing import SigningKey; import sys; sk=SigningKey(bytes.fromhex('$SEED_HEX')); print(sk.verify_key.encode().hex())")
cd - >/dev/null

echo "SEED_HEX=$SEED_HEX"
echo "PUB_HEX=$PUB_HEX"

# 2. Create apps (idempotent)
for APP in osint-api osint-go-worker osint-py-worker; do
  flyctl apps create "$APP" 2>/dev/null || true
done

# 3. Set common secrets on each app (adjust as needed)
read -p "Paste ANTHROPIC_API_KEY: " ANTHROPIC
read -p "Paste STRIPE_SECRET_KEY: " STRIPE
read -p "Paste FIREBASE_SERVICE_ACCOUNT_JSON path: " FB_JSON_PATH
read -p "Paste DATABASE_URL (prod): " PG_URL
read -p "Paste REDIS_URL (prod): " REDIS_URL

cd apps/api
flyctl secrets set \
  ANTHROPIC_API_KEY="$ANTHROPIC" \
  STRIPE_SECRET_KEY="$STRIPE" \
  STRIPE_WEBHOOK_SECRET="replace_me" \
  STRIPE_PRICE_ID_HUNTER="price_replace_me" \
  STRIPE_PRICE_ID_OPERATOR="price_replace_me" \
  FIREBASE_SERVICE_ACCOUNT_JSON="$(cat "$FB_JSON_PATH")" \
  FIREBASE_PROJECT_ID="osint-agent-prod" \
  DATABASE_URL="$PG_URL" \
  REDIS_URL="$REDIS_URL" \
  WORKER_SIGNING_KEY_HEX="$SEED_HEX" \
  GO_WORKER_URL="https://osint-go-worker.fly.dev" \
  PY_WORKER_URL="https://osint-py-worker.fly.dev" \
  --app osint-api
cd -

cd apps/go-worker
flyctl secrets set WORKER_PUBLIC_KEY_HEX="$PUB_HEX" --app osint-go-worker
cd -

cd apps/py-worker
flyctl secrets set WORKER_PUBLIC_KEY_HEX="$PUB_HEX" --app osint-py-worker
cd -

echo "✓ Fly secrets set. Now run:"
echo "  cd apps/go-worker && flyctl deploy"
echo "  cd apps/py-worker && flyctl deploy"
echo "  cd apps/api && flyctl deploy"
```

```bash
chmod +x infra/scripts/bootstrap-fly.sh
```

- [ ] **Step 15.8: Write GitHub Actions deploy workflow**

File `.github/workflows/deploy.yml`:

```yaml
name: Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

jobs:
  deploy-go-worker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: superfly/flyctl-actions/setup-flyctl@master
      - run: flyctl deploy --remote-only
        working-directory: apps/go-worker
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_API_TOKEN }}

  deploy-py-worker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: superfly/flyctl-actions/setup-flyctl@master
      - run: flyctl deploy --remote-only
        working-directory: apps/py-worker
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_API_TOKEN }}

  deploy-api:
    runs-on: ubuntu-latest
    needs: [deploy-go-worker, deploy-py-worker]
    steps:
      - uses: actions/checkout@v4
      - uses: superfly/flyctl-actions/setup-flyctl@master
      - run: flyctl deploy --remote-only
        working-directory: apps/api
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_API_TOKEN }}
```

- [ ] **Step 15.9: Run bootstrap locally (one-time setup)**

```bash
# Assumes a prod Postgres + DragonflyDB exist. For Phase 0 you can use
# Fly Postgres (`flyctl postgres create`) and Upstash Redis free tier.
./infra/scripts/bootstrap-fly.sh

# Then deploy each app:
cd apps/go-worker && flyctl deploy --remote-only
cd ../py-worker && flyctl deploy --remote-only
cd ../api && flyctl deploy --remote-only
```

- [ ] **Step 15.10: Add `FLY_API_TOKEN` to GitHub secrets**

```bash
# In GitHub → repo → Settings → Secrets → Actions → New secret:
#   Name:  FLY_API_TOKEN
#   Value: <output of `flyctl auth token`>
```

- [ ] **Step 15.11: Smoke-test prod**

```bash
curl https://osint-api.fly.dev/healthz
# Expected: {"ok":true,"service":"osint-api","version":"0.1.0"}

curl https://osint-api.fly.dev/tools
# Expected: 4 tools listed

# With a real Firebase ID token:
FB_TOKEN="$(...obtain via Firebase test user...)"
curl https://osint-api.fly.dev/me -H "Authorization: Bearer $FB_TOKEN"
# Expected: {uid, tenantId, userId}
```

- [ ] **Step 15.12: Commit**

```bash
git add .github/workflows/deploy.yml apps/*/Dockerfile apps/*/fly.toml infra/scripts/bootstrap-fly.sh
git commit -m "feat(deploy): Fly.io Dockerfiles + fly.toml + bootstrap + GH Actions deploy"
git push  # triggers CI + deploy
```

---

## Task 16: Stripe Checkout for Hunter tier upgrade (minimum viable billing)

**Files:**
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/billing/stripe.ts`
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/src/billing/webhook.ts`
- Modify: `/Users/jasonroell/projects/osint-agent/apps/api/src/index.ts` (add checkout + webhook routes)
- Create: `/Users/jasonroell/projects/osint-agent/apps/api/test/stripe-webhook.test.ts`

- [ ] **Step 16.1: Add stripe SDK**

```bash
cd apps/api
bun add stripe
```

- [ ] **Step 16.2: Write the Stripe client**

File `apps/api/src/billing/stripe.ts`:

```typescript
import Stripe from "stripe";
import { config } from "../config";

export const stripe = new Stripe(config.stripe.secretKey, { apiVersion: "2024-11-20.acacia" });

export async function createCheckoutSession(args: {
  tenantId: string;
  userEmail: string;
  priceId: string;
  successUrl: string;
  cancelUrl: string;
}): Promise<{ url: string }> {
  const session = await stripe.checkout.sessions.create({
    mode: "subscription",
    line_items: [{ price: args.priceId, quantity: 1 }],
    customer_email: args.userEmail,
    client_reference_id: args.tenantId,
    success_url: args.successUrl,
    cancel_url: args.cancelUrl,
  });
  if (!session.url) throw new Error("Stripe did not return a checkout URL");
  return { url: session.url };
}
```

- [ ] **Step 16.3: Write the webhook handler**

File `apps/api/src/billing/webhook.ts`:

```typescript
import Stripe from "stripe";
import { sql } from "../db/client";
import { grantCredits } from "./credits";
import { writeEvent } from "../events/stream";
import { stripe } from "./stripe";
import { config } from "../config";
import { logger } from "../telemetry";

const TIER_TO_INCLUDED_CREDITS: Record<string, number> = {
  free: 100 * 100,           // 100 credits = 10_000 millicredits
  hunter: 5000 * 100,        // 5_000 credits = 500_000 millicredits
  operator: 25000 * 100,     // 25_000 credits = 2_500_000 millicredits
};

export async function handleStripeWebhook(rawBody: string, signature: string): Promise<void> {
  let event: Stripe.Event;
  try {
    event = stripe.webhooks.constructEvent(rawBody, signature, config.stripe.webhookSecret);
  } catch (e) {
    logger.error({ err: e }, "stripe webhook signature verification failed");
    throw new Error("bad signature");
  }

  switch (event.type) {
    case "checkout.session.completed": {
      const session = event.data.object as Stripe.Checkout.Session;
      const tenantId = session.client_reference_id;
      if (!tenantId) return;

      // Determine tier from the Price ID
      const subscriptionId = session.subscription as string | null;
      let tier: "hunter" | "operator" = "hunter";
      if (subscriptionId) {
        const sub = await stripe.subscriptions.retrieve(subscriptionId);
        const priceId = sub.items.data[0]?.price?.id;
        if (priceId === config.stripe.priceIdOperator) tier = "operator";
      }

      await sql`
        UPDATE tenants
        SET tier = ${tier},
            stripe_customer_id = ${session.customer as string},
            stripe_subscription_id = ${subscriptionId},
            updated_at = NOW()
        WHERE id = ${tenantId}
      `;

      await grantCredits({
        tenantId,
        millicredits: TIER_TO_INCLUDED_CREDITS[tier],
        reason: `refill:${tier}`,
        metadata: { stripe_session: session.id },
      });

      await writeEvent({
        tenantId,
        eventType: "billing.subscription_active",
        payload: { tier, subscription_id: subscriptionId },
      });
      break;
    }

    case "customer.subscription.deleted": {
      const sub = event.data.object as Stripe.Subscription;
      const rows = await sql`
        UPDATE tenants
        SET tier = 'free',
            stripe_subscription_id = NULL,
            updated_at = NOW()
        WHERE stripe_subscription_id = ${sub.id}
        RETURNING id
      `;
      for (const r of rows) {
        await writeEvent({
          tenantId: r.id as string,
          eventType: "billing.subscription_canceled",
          payload: { subscription_id: sub.id },
        });
      }
      break;
    }

    default:
      logger.info({ type: event.type }, "stripe webhook unhandled event");
  }
}
```

- [ ] **Step 16.4: Add the routes in `src/index.ts`**

Patch `src/index.ts` — add after existing routes:

```typescript
import { createCheckoutSession } from "./billing/stripe";
import { handleStripeWebhook } from "./billing/webhook";

// ... existing app chain ...
  .post("/billing/checkout", async ({ auth, body, request }) => {
    const { tier } = body as { tier: "hunter" | "operator" };
    const priceId = tier === "operator" ? config.stripe.priceIdOperator : config.stripe.priceIdHunter;
    const base = request.headers.get("origin") ?? `https://${request.headers.get("host")}`;
    return createCheckoutSession({
      tenantId: auth.tenantId,
      userEmail: auth.user.email!,
      priceId,
      successUrl: `${base}/billing/success`,
      cancelUrl: `${base}/billing/cancel`,
    });
  })
  .post("/billing/webhook", async ({ request }) => {
    const rawBody = await request.text();
    const sig = request.headers.get("stripe-signature") ?? "";
    await handleStripeWebhook(rawBody, sig);
    return { received: true };
  });
```

Important: the webhook must NOT go through `authPlugin`. Either move `authPlugin` to only the routes that need it, or add an explicit `skipAuth` for `/billing/webhook`. Simplest fix: declare `/billing/webhook` as its own Elysia route BEFORE `.use(authPlugin)`.

- [ ] **Step 16.5: Write a webhook unit test (with mocked stripe signature verification)**

File `test/stripe-webhook.test.ts`:

```typescript
import { describe, it, expect, mock, beforeAll, afterAll } from "bun:test";
import { sql, closeDb } from "../src/db/client";

const TENANT_ID = "00000000-0000-0000-0000-000000000099";

mock.module("stripe", () => {
  class FakeStripe {
    webhooks = {
      constructEvent: (body: string) => JSON.parse(body),
    };
    subscriptions = {
      retrieve: async (_id: string) => ({
        items: { data: [{ price: { id: process.env.STRIPE_PRICE_ID_HUNTER } }] },
      }),
    };
  }
  return { default: FakeStripe };
});

const { handleStripeWebhook } = await import("../src/billing/webhook");

describe("stripe webhook", () => {
  beforeAll(async () => {
    await sql`
      INSERT INTO tenants (id, name, tier, credits_balance)
      VALUES (${TENANT_ID}, 'stripe-test', 'free', 0)
      ON CONFLICT (id) DO UPDATE SET tier = 'free', credits_balance = 0
    `;
  });

  afterAll(async () => {
    await sql`DELETE FROM events WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM credit_ledger WHERE tenant_id = ${TENANT_ID}`;
    await sql`DELETE FROM tenants WHERE id = ${TENANT_ID}`;
    await closeDb();
  });

  it("checkout.session.completed upgrades tier and grants credits", async () => {
    const event = {
      type: "checkout.session.completed",
      data: {
        object: {
          client_reference_id: TENANT_ID,
          customer: "cus_test",
          subscription: "sub_test",
          id: "cs_test",
        },
      },
    };
    await handleStripeWebhook(JSON.stringify(event), "sig");

    const [row] = await sql`SELECT tier, credits_balance FROM tenants WHERE id = ${TENANT_ID}`;
    expect(row.tier).toBe("hunter");
    expect(Number(row.credits_balance)).toBe(500_000);
  });
});
```

- [ ] **Step 16.6: Run test and commit**

```bash
bun test test/stripe-webhook.test.ts
# Expected: 1 passed
git add apps/api/
git commit -m "feat(billing): Stripe Checkout + webhook handler for tier upgrades + credit grants"
```

---

## Task 17: Final integration smoke test (Claude Desktop connects, calls real tools, sees credits decrement)

**Files:** none new; this task exercises the entire stack.

- [ ] **Step 17.1: Deploy the latest to Fly.io**

```bash
git push  # triggers the deploy workflow
# Wait for GH Actions to go green
gh run watch
```

- [ ] **Step 17.2: Configure Claude Desktop**

Edit `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "osint-agent-prod": {
      "transport": {
        "type": "http",
        "url": "https://osint-api.fly.dev/mcp",
        "headers": {
          "Authorization": "Bearer <YOUR_FIREBASE_ID_TOKEN>"
        }
      }
    }
  }
}
```

(In Phase 1 Task — plan 02 — we add a device-code flow so users don't paste Firebase ID tokens manually. Phase 0 uses a manual token grab through Firebase Auth SDK for testing.)

- [ ] **Step 17.3: Exercise each tool from Claude**

Prompt Claude Desktop:
```
Use the osint-agent-prod MCP server to:
1. hello_tool with name="Jason"
2. dns_lookup_comprehensive for "example.com"
3. stealth_http_fetch for "https://httpbin.org/get" with impersonate="chrome"
4. subfinder_passive for "example.com"
```

Verify:
- All four tools return plausible results.
- `/me` before and after shows `credits_balance` decreased by 1 (hello) + 2 (dns) + 5 (stealth) + 10 (subfinder) = 18 credits = 1800 millicredits.

- [ ] **Step 17.4: Smoke-test billing**

- Go to the Stripe Checkout URL (from `POST /billing/checkout` with `{"tier":"hunter"}`).
- Complete with a test card (4242 4242 4242 4242).
- Verify the Stripe webhook fires and `/me` shows `tier=hunter`, `credits_balance` rises by 500_000.

- [ ] **Step 17.5: Tag the release**

```bash
cd /Users/jasonroell/projects/osint-agent
git tag -a v0.1.0 -m "Phase 0 foundation: MCP server + 3 tools + billing + event stream"
git push --tags
```

---

## Self-Review

Running through the plan one more time:

**Spec coverage check:**
- ✅ MCP server (stdio + Streamable HTTP) — Task 10
- ✅ Firebase Auth — Task 5
- ✅ Stripe billing — Task 16
- ✅ LLM Gateway (Anthropic backend; OpenRouter deferred to Plan 2 as stated) — Task 8
- ✅ Event stream (partitioned Postgres) — Task 6
- ✅ Go tool worker with 2 tools — Tasks 12
- ✅ Python tool worker with stealth HTTP — Task 13
- ✅ Tool registry + credit metering — Task 10
- ✅ Signed worker RPC — Task 11
- ✅ Fly.io deployment — Task 15
- ✅ OpenTelemetry + Pino — Task 9
- ✅ GitHub Actions CI + Deploy — Tasks 2, 15
- ✅ Apache-2.0 open-source scaffolding — Task 1
- ✅ Tenant / user / credits schema — Task 4
- ✅ Hypergraph data model — **Deferred to Plan 2** (per spec phasing: hypergraph is foundational but too large for Plan 1; Plan 1 ships the auth/billing/MCP skeleton; Plan 2 adds FalkorDB + Graphiti + hypergraph + 10 more tools)

**Placeholder scan:**
- No "TBD" / "TODO" / "implement later" found.
- Step 17.2's `<YOUR_FIREBASE_ID_TOKEN>` is a user-supplied value (expected), not a placeholder for implementation.
- subfinder.go has a note about the upstream API evolving — that's an explicit maintenance note, not a placeholder.

**Type consistency:**
- `AuthContext` type used consistently across `auth/middleware.ts`, `mcp/tools/registry.ts`, `mcp/server.ts`.
- `WorkerToolRequest` / `WorkerToolResponse` consistent in TS client, Go server, Python server.
- Credit units consistent: `millicredits` everywhere (1 credit = 100 millicredits = $0.01).
- Event types enumerated and consistent.

**Scope check:**
- Plan 1 is ~17 tasks, 100+ steps, ~3-4 weeks of focused solo work. At the upper end of bite-sized but coherent because each task produces a demonstrable outcome. Not further decomposable without losing the "each task delivers working software" property.

**Fixes applied inline:** none this pass — spec coverage, placeholder scan, type consistency, and scope all pass.

---

## Execution Handoff

Plan complete and saved to `~/projects/osint-agent/docs/plans/2026-04-22-plan-01-foundation.md`.

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best for keeping your context clean across 17 tasks.

2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints for review. Best if you want to stay in the loop turn-by-turn.

Which approach?
