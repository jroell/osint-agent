# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`osint-agent` is an MCP server that exposes ~60 OSINT tools to LLM clients (Claude Desktop, Cursor, etc.) over Streamable HTTP. The repo is the open-source core (Apache-2.0); the proprietary World Model / Adversary Library / Federated Learning / Predictive Temporal layer / Investigator Policy Network are not in this tree — see `CONTRIBUTING.md` before touching anything that looks like learning-loop scaffolding.

Phase 0 / Plan 01 is the current scope. Design spec: `docs/specs/2026-04-22-osint-agent-design.md`. Implementation plan with task checkboxes: `docs/plans/2026-04-22-plan-01-foundation.md`.

## Three-process architecture

```
LLM client ──HTTP/JSON-RPC──► apps/api (Bun + Elysia + MCP SDK)
                                  │
                                  │  Ed25519-signed POST  body=`${ts}\n${json}`
                                  │  headers: x-osint-ts, x-osint-sig
                                  │  60s clock-skew tolerance
                                  ▼
                    apps/go-worker  (Echo, ProjectDiscovery libs, paid HTTP APIs)
                    apps/py-worker  (FastAPI, rnet/JA4+, maigret, holehe, ghunt, exif/pdf)
```

- **`apps/api`** — single user-facing process. Verifies Firebase JWTs, meters credits, persists events, dispatches to workers. MCP transport is `WebStandardStreamableHTTPServerTransport` and is currently **stateless per request** (no `Mcp-Session-Id` reuse — that's Phase 1).
- **`apps/go-worker`** — Go 1.26 + Echo. Hosts CPU-bound / library-heavy OSINT tools (subfinder, dnsx, port scan, paid API wrappers like Shodan/Censys/Tavily/Perplexity, urlscan, dnstwist). Tool dispatch is a `switch req.Tool` in `internal/server/server.go` — adding a tool means adding both the case and a function in `internal/tools/`.
- **`apps/py-worker`** — Python 3.13 + FastAPI + uv. Hosts identity/social tools that need Python-only libs (`rnet` for JA4+ TLS impersonation, `maigret`, `sherlock`, `holehe`, `pypdf`, `exifread`). Tool dispatch is a dict lookup in `src/py_worker/main.py::_TOOLS`. Note: `ghunt` and `theHarvester` have conflicting transitive deps; they run as isolated `uv tool` subprocesses, not in-process — install once with `uv tool install ghunt` / `uv tool install --from git+https://github.com/laramies/theHarvester.git theHarvester`.

`packages/shared-types` defines the wire contract (`WorkerToolRequest` / `WorkerToolResponse`) shared between `apps/api` (TS) and consumers — Go and Python re-implement it manually, so any change must land in all three places.

### Adding a new MCP tool

A new tool typically requires three coordinated edits:

1. **Worker side**: implement the Go function in `apps/go-worker/internal/tools/<name>.go` and add a case to the `switch req.Tool` in `apps/go-worker/internal/server/server.go` (or the Python equivalent in `apps/py-worker`).
2. **API side**: create `apps/api/src/mcp/tools/<kebab-name>.ts` that calls `toolRegistry.register({ name, description, inputSchema (zod), costMillicredits, handler })` and uses `callGoWorker`/`callPyWorker` to dispatch.
3. **Registry**: add `import "./<kebab-name>"` to `apps/api/src/mcp/tools/registry.ts` **above the meta-tools section** at the bottom (`person-aggregate`, `domain-aggregate` enumerate other tools by name at registration time, so they must be imported last).

The tool name string must match exactly across all three layers — `apps/api/src/mcp/tools/instance.ts::ToolRegistry.invoke` looks up by name, the worker's switch matches by name, and `person-aggregate.ts`'s dispatch plan references by name. There is no compile-time check for this.

### Credit metering invariants

`apps/api/src/mcp/tools/instance.ts::ToolRegistry.invoke` is the single chokepoint for tool execution:

1. Writes `tool.called` event
2. Calls `spendCredits` (atomic UPDATE-WHERE-balance>=cost; throws `InsufficientCreditsError` on underflow)
3. Runs the handler
4. On success: writes `tool.succeeded` event
5. On failure: refunds via `spendCredits` with negative millicredits (best-effort, errors swallowed so original error isn't masked) and writes `tool.failed`

`spendCredits`/`grantCredits` rely on `tenants.credits_balance` as a materialized aggregate of the append-only `credit_ledger`. **Don't update `credits_balance` outside these helpers** — the ledger is the source of truth and they keep both in sync inside one transaction.

When `DEV_AUTH_BYPASS=true` (only honored when `NODE_ENV=development`), the entire metering + event-write block is skipped — useful when the DB isn't running, but never let bypass flow into a deployed environment.

## Common commands

All commands run from the repo root unless noted. Bun is the only package manager for the TS workspaces — never use `npm`/`pnpm`/`yarn` here.

| Task | Command |
|---|---|
| Install all deps | `bun install` |
| Boot full local stack (Postgres + Dragonfly + 3 services, with auto-keygen + DEV_AUTH_BYPASS) | `bun run dev` |
| API only (against already-running infra) | `bun run dev:api` |
| Go worker only | `bun run dev:go-worker` |
| Python worker only | `bun run dev:py-worker` |
| Bring up / down infra (Postgres + Dragonfly) | `bun run db:up` / `bun run db:down` |
| Run migrations (requires `dbmate` on PATH) | `bun run db:migrate` |
| Roll back last migration batch | `bun run db:rollback` |
| Lint everything | `bun run lint` (per-workspace, runs `tsc --noEmit` for TS) |
| Test everything | `bun run test` (per-workspace) |
| Single TS test file | `cd apps/api && bun test src/path/to.test.ts` |
| Single Go test | `cd apps/go-worker && go test ./internal/tools -run TestName` |
| Single Py test | `cd apps/py-worker && uv run pytest tests/path/test_x.py::test_y` |
| Build all | `bun run build` |
| Regenerate worker keypair (idempotent — won't overwrite valid existing keys) | `bun run gen:worker-keys` |

CI mirrors these (`.github/workflows/ci.yml`): TS lane spins up Postgres, runs migrations, then `bun run lint && bun run test && bun run build`. Go and Py lanes run independently.

## Local infra quirks

- **Non-standard host ports**: Postgres maps `5434→5432`, Dragonfly maps `6380→6379` (chosen to coexist with other Vurvey-stack containers). The `.env.example` URLs already reflect this — `DATABASE_URL=postgres://osint:osint@localhost:5434/osint_dev`.
- **Worker ports**: API expects Go on `:8081` and Py on `:8082` per `.env.example`. The dev script (`infra/scripts/dev.sh`) starts them on `:8181`/`:8182` by default via `GO_WORKER_PORT`/`PY_WORKER_PORT`. Make sure `GO_WORKER_URL`/`PY_WORKER_URL` in `.env` agree with whatever the dev script binds.
- **`apps/api/.env` is a symlink** to the repo-root `.env`. Bun auto-loads `.env` from `cwd`, so the API process (which runs from `apps/api/`) would silently shadow root config if a real file existed there. The dev script enforces this; if you see `.env.bak.<timestamp>` in `apps/api/`, that's the old file the script moved aside — leave it.
- **Worker keypair** is auto-generated into root `.env` on `bun run dev` (or via `bun run gen:worker-keys`). The seed (`WORKER_SIGNING_KEY_HEX`) is read by the API; both workers verify with the matching `WORKER_PUBLIC_KEY_HEX`.

## Database

- Postgres 16, single schema, `postgres` driver (not Knex/Prisma). Migrations are plain SQL via `dbmate` in `apps/api/src/db/migrations/`. Schema dump lands at `apps/api/src/db/schema.sql`.
- Tenants/users are auto-provisioned on first valid Firebase JWT (`apps/api/src/auth/middleware.ts::ensureUserAndTenant`). Phase 0 = 1 user : 1 tenant; Phase 3 introduces Team tier (N:1).
- **`events` is partitioned by month**. The migration pre-creates 4 months; `apps/api/src/events/stream.ts::ensureCurrentMonthPartition` is the runtime safety net that creates the current partition if missing. In Phase 1 this moves to a cron — until then, leave the safety-net call in place.
- All migrations must be idempotent (`IF NOT EXISTS` / `IF EXISTS` / `hasTable` guards). Per the user's universal rule (`~/.claude/CLAUDE.md`), never write a migration that fails on re-run, and never make breaking schema changes without explicit approval.

## Auth

- Firebase Admin SDK verifies `Authorization: Bearer <id-token>`. Service-account credentials come from either `FIREBASE_SERVICE_ACCOUNT_JSON` (inline, used on Fly) or `GOOGLE_APPLICATION_CREDENTIALS` (file path, local).
- The `authPlugin` is an Elysia scoped plugin that adds `auth: { user, tenantId, userId }` to the request context. Routes mounted **before** `.use(authPlugin)` are public (`/`, `/healthz`, `/billing/webhook`); routes after are gated.
- `/billing/webhook` is intentionally placed before the auth plugin and reads the raw body for Stripe signature verification — don't move it.

## LLM Gateway

`apps/api/src/llm/gateway.ts` defines a provider-agnostic `LLMGateway` with a `fallbackChain` (try primary model → next → next on failure). Phase 0 only ships the Anthropic backend; OpenRouter / BYOK are additive in Phase 2. When wiring new code that needs an LLM, depend on `LLMGateway` not `Anthropic` directly.

## Style + repo conventions

- TypeScript: ESM, `strict` + `noUncheckedIndexedAccess`, target ES2023 (see `tsconfig.base.json`). Schemas are `zod` 4.x.
- HTTP framework on the API side is **Elysia 1.x**, not Express/Fastify — handlers are `({ auth, body, request }) => ...`. The `as: "scoped"` derive in the auth plugin is intentional; without it, downstream routes lose the `auth` context.
- Logging: `pino` (pretty in dev). Use the exported `logger` from `apps/api/src/telemetry.ts`. Don't add `console.log` to checked-in code.
- Telemetry: OpenTelemetry NodeSDK + auto-instrumentations, OTLP/HTTP exporter to Grafana Cloud. Disabled if `OTEL_EXPORTER_OTLP_ENDPOINT` is unset (logs a warning).
- The repo follows the org-wide commit/branch/PR convention (see user-level `~/.claude/CLAUDE.md` for the full table). Conventional Commits with the ticket scope; **no `Co-Authored-By: Claude` trailer** on commits — that's an org-wide security rule.

## Deployment (Fly.io)

`.github/workflows/deploy.yml` deploys all three services on push to `main`: workers first, then API (which depends on workers being up). The API deploy uses `--config apps/api/fly.toml --build-context .` from the repo root because the API Dockerfile pulls in `packages/shared-types`. Don't change the build context without testing — see commit `b0fd375 fix(deploy): correct Dockerfile build contexts for Fly deploy`.

## What NOT to add to this repo

Per `CONTRIBUTING.md`, the proprietary moat (World Model, Adversary Library beyond 3 example playbooks, Federated Learning aggregator, Predictive Temporal Layer, Investigator Policy Network) lives in a private repo. PRs touching those are rejected. The `learning_bucket_b_opt_out` / `benchmark_contribution_opt_in` columns on `tenants` are scaffolding for the future hosted layer — leave them alone in Phase 0.
