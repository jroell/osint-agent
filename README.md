# osint-agent

**The recon stack that finds what someone is hiding.**

Adversary-aware OSINT for bug bounty hunters, security researchers, and investigative journalists. One MCP server plugs into Claude Desktop, Cursor, or any MCP-capable LLM client and exposes ~186 reconnaissance tools — domain & DNS recon, breach data, social/identity correlation, code search, geo / vision, blockchain, sanctions, scientific & legal corpora — through a single typed protocol.

- **Open-source core** (Apache-2.0): MCP server, tool adapters, orchestration glue, Ed25519-signed worker dispatch.
- **Proprietary moat** (hosted): learned World Model, Adversary Library, Federated Learning, Predictive Temporal reasoning, Investigator Policy Network. Not in this repo — see [`CONTRIBUTING.md`](./CONTRIBUTING.md).
- **Pricing (hosted):** Free · Hunter $49/mo · Operator $199/mo (self-serve).

> **Status:** Pre-launch, active development. Phase 0 (`docs/plans/2026-04-22-plan-01-foundation.md`) is the current scope. The OSS core in this repo runs end-to-end against a local Postgres + Dragonfly stack today.

---

## Table of contents

1. [What you get](#what-you-get)
2. [Architecture at a glance](#architecture-at-a-glance)
3. [Prerequisites](#prerequisites)
4. [Quickstart — local self-host](#quickstart--local-self-host)
5. [Connecting an MCP client](#connecting-an-mcp-client)
6. [The tool catalog](#the-tool-catalog)
7. [Using it effectively for OSINT](#using-it-effectively-for-osint)
8. [Configuration reference](#configuration-reference)
9. [Adding a new tool](#adding-a-new-tool)
10. [Database & migrations](#database--migrations)
11. [Testing, linting, building](#testing-linting-building)
12. [Deployment](#deployment)
13. [Contributing](#contributing)
14. [License](#license)

---

## What you get

- **A single MCP endpoint** at `http://localhost:3000/mcp` (Streamable HTTP) that exposes all registered tools to your LLM client. No per-tool plumbing in the client — Claude/Cursor/etc. discover tool names, JSON-Schemas, and call them directly.
- **~186 production-grade OSINT tools** in three families:
  - **Infrastructure / surface mapping** — `subfinder`, `dns-lookup`, `whois`, `ct` (CT logs), `asn`, `port-scan`, `http-probe`, `takeover`, `tech-stack`, `wayback`, `urlscan`, `shodan`, `censys`, `hackertarget-recon`, `well-known-recon`, `favicon-pivot`, `ssl-cert-chain-inspect`, `spf-dmarc-chain`, `js-endpoint-extract`, `swagger-openapi-finder`, `graphql-introspection`/`-clairvoyance`, `mcp-endpoint-finder`, …
  - **Identity / social / breach** — `holehe`, `maigret`, `sherlock`, `theharvester`, `ghunt`, `hibp`, `dehashed`, `intelx`, `hunter-io`, `gravatar`, `keybase`, `github-user`/`-emails`/`-org-intel`, `linkedin`, `twitter`, `reddit-*`, `bluesky`, `mastodon`, `hackernews-*`, `instagram`, `discord-invite-resolve`, `telegram-channel-resolve`, `farcaster-user-lookup`, `lichess-user-lookup`, `steam-profile-lookup`, …
  - **Civic / corporate / scientific / specialized** — `opencorporates`, `opensanctions`, `gleif-lei-lookup`, `sec-edgar`, `propublica-nonprofit`, `fec-donations-lookup`, `lda-lobbying-search`, `usaspending-search`, `courtlistener-search`, `federal-register-search`, `govtrack-search`, `cfpb-complaints-search`, `nih-reporter-search`, `nsf-awards-search`, `clinicaltrials-search`, `pubmed-search`, `arxiv-search`, `biorxiv-search`, `dblp-search`, `crossref-paper-search`, `openalex-search`, `orcid-search`, `ror-org-lookup`, `wikidata-lookup`, `wikipedia-search`, BigQuery wrappers (`gdelt`, `github-archive`, `patents`, `stack-overflow`, `wikipedia-pageviews`, `trending-now`), crypto/blockchain (`ens-resolve`, `onchain-tx-analysis`, `defillama-intel`, `coingecko-search`), geo/vision (`geo-vision`, `reverse-image`, `google-lens-search`, `nominatim-geocode`, `osm-overpass-query`, `google-maps-places`, `census-geocoder`, `census-acs-tract`, `usgs-earthquake-search`, `openmeteo-search`, `usno-astronomy`), document analysis (`pdf-analyze`, `exif`, `documentcloud-search`), and **meta-tools** (`person-aggregate`, `domain-aggregate`, `panel-consult`, `panel-entity-resolution`, `synthesis`) that orchestrate other tools into multi-step reconnaissance plans.
- **Built-in credit metering**: every tool call is priced in millicredits, charged atomically, and refunded on failure. Append-only `credit_ledger` is the source of truth; `tenants.credits_balance` is the materialized aggregate.
- **Per-tenant event log** (`tool.called` / `tool.succeeded` / `tool.failed`) on a monthly-partitioned `events` table — gives you replayable audit history per investigation.
- **Provider-agnostic LLM gateway** (`apps/api/src/llm/gateway.ts`) with fallback chains. Phase 0 ships Anthropic; OpenRouter / BYOK come in Phase 2.

## Architecture at a glance

```
LLM client ──HTTP/JSON-RPC──► apps/api  (Bun + Elysia + MCP SDK, port 3000)
                                  │   ├─ Firebase JWT verify
                                  │   ├─ Credit metering + event log (Postgres)
                                  │   ├─ MCP transport: WebStandardStreamableHTTP (stateless per-request)
                                  │   └─ LLM Gateway → Anthropic
                                  │
                                  │   Ed25519-signed POST  body=`${ts}\n${json}`
                                  │   headers: x-osint-ts, x-osint-sig    (60s skew)
                                  ▼
              ┌──────────────────────────────────────────┐
              │ apps/go-worker (Echo, Go 1.26, port 8081)│  ← CPU-bound + paid-API tools
              │   subfinder, dnsx, port-scan, shodan,    │
              │   censys, urlscan, dnstwist, BigQuery,…  │
              ├──────────────────────────────────────────┤
              │ apps/py-worker (FastAPI, Py 3.13, 8082)  │  ← TLS impersonation + py-only libs
              │   rnet (JA4+), maigret, sherlock,        │
              │   holehe, ghunt*, theHarvester*,         │
              │   pypdf, exifread                        │
              └──────────────────────────────────────────┘
              * ghunt + theHarvester run as isolated `uv tool` subprocesses
                (conflicting transitive deps — not in-process).
```

- **One user-facing process** (`apps/api`). Workers are private, signed-only.
- **Stateless MCP transport** in Phase 0 (no `Mcp-Session-Id` reuse — that lands in Phase 1).
- `packages/shared-types` defines the wire contract between API ↔ workers (`WorkerToolRequest` / `WorkerToolResponse`). Go and Python re-implement it manually, so any change must land in **all three places**.

## Prerequisites

| Tool | Min version | Notes |
|---|---|---|
| **Bun** | 1.2+ | Single TS package manager — never use `npm`/`pnpm`/`yarn` here. |
| **Go** | 1.26+ | For `apps/go-worker`. |
| **Python** | 3.13+ | For `apps/py-worker`. |
| **uv** | latest | Python package/env manager. `pip` and `pipx` are aliases here. |
| **Docker** (or compatible) | recent | For Postgres 16 + Dragonfly. |
| **dbmate** | latest | Migration runner: `brew install dbmate`. Required for `bun run db:migrate`. |

Optional (only if you'll use those tools):

- `uv tool install ghunt`
- `uv tool install --from git+https://github.com/laramies/theHarvester.git theHarvester`

## Quickstart — local self-host

```sh
git clone https://github.com/<your-fork>/osint-agent.git
cd osint-agent
bun install

# One command boots Postgres + Dragonfly + all three services with DEV_AUTH_BYPASS=true
# and auto-generates an Ed25519 worker keypair into .env.
bun run dev
```

`bun run dev` (driven by [`infra/scripts/dev.sh`](./infra/scripts/dev.sh)) does the following so you don't have to:

1. Copies `.env.example` → `.env` if missing.
2. Symlinks `apps/api/.env` → root `.env` (Bun auto-loads `.env` from `cwd`; without the symlink the API process would silently shadow root config — see the dev script for the rationale).
3. Sets `DEV_AUTH_BYPASS=true` so you can run with no Firebase project / no DB writes.
4. Generates an Ed25519 keypair (idempotent) into `WORKER_SIGNING_KEY_HEX` / `WORKER_PUBLIC_KEY_HEX`.
5. Brings up Postgres (host `5434`→container `5432`) and Dragonfly (host `6380`→container `6379`).
6. Runs `dbmate up` (best-effort).
7. Spawns `go-worker` on `:8181`, `py-worker` on `:8182`, API on `:3000`. Logs are streamed with `[api]` / `[go]` / `[py]` prefixes; `Ctrl-C` shuts everything down.

> **Non-standard ports** are intentional — they coexist with other Vurvey-stack containers on the same machine. The `.env.example` URLs already match.

If you want to run components separately:

```sh
bun run db:up            # just the docker infra
bun run db:migrate       # apply migrations
bun run dev:go-worker    # in its own terminal
bun run dev:py-worker    # in its own terminal
bun run dev:api          # in its own terminal
```

Verify it's alive:

```sh
curl http://localhost:3000/healthz       # → {"ok":true}
curl http://localhost:3000/              # → service banner
```

## Connecting an MCP client

The MCP endpoint is `http://localhost:3000/mcp` (Streamable HTTP transport).

### Claude Desktop

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or the Windows equivalent:

```jsonc
{
  "mcpServers": {
    "osint-agent": {
      "transport": "http",
      "url": "http://localhost:3000/mcp"
    }
  }
}
```

Restart Claude Desktop. You should see all ~186 tools enumerated under the 🔌 menu.

### Cursor

Open **Settings → Features → Model Context Protocol** and add a new server:

- **Name:** `osint-agent`
- **Transport:** `streamable-http`
- **URL:** `http://localhost:3000/mcp`

### Other clients (raw JSON-RPC)

Any MCP-compliant client over Streamable HTTP works. Quick smoke test from the CLI:

```sh
# List all tools (DEV_AUTH_BYPASS must be on, or send a real Firebase Bearer token)
curl -s http://localhost:3000/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq '.result.tools | length'

# Invoke a tool
curl -s http://localhost:3000/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call",
       "params":{"name":"dns_lookup","arguments":{"domain":"example.com"}}}' | jq
```

### Production auth

When `DEV_AUTH_BYPASS=false` (any non-development environment), every request must carry `Authorization: Bearer <Firebase ID token>`. On first valid token the API auto-provisions a Tenant + User row (Phase 0 is 1 user : 1 tenant; Team-tier multi-user lands in Phase 3).

## The tool catalog

Tool definitions live in `apps/api/src/mcp/tools/<kebab-name>.ts` and dispatch to the relevant worker. Categories (selected highlights — see `apps/api/src/mcp/tools/registry.ts` for the full list):

| Category | Representative tools |
|---|---|
| **Domain / DNS / TLS** | `subfinder`, `dns-lookup`, `reverse-dns`, `whois`, `ct`, `ct-brand-watch`, `asn`, `ssl-cert-chain-inspect`, `spf-dmarc-chain`, `well-known-recon`, `favicon-pivot`, `typosquat` |
| **HTTP / surface** | `http-probe`, `stealth-http` (rnet/JA4+), `tech-stack`, `js-endpoint-extract`, `swagger-openapi-finder`, `graphql-introspection`, `graphql-clairvoyance`, `mcp-endpoint-finder`, `api-endpoint-db`, `wayback-endpoint-extract` |
| **Asset / vuln** | `port-scan`, `shodan`, `shodan-internetdb`, `censys`, `urlscan`, `hackertarget-recon`, `osv-vuln-search`, `cisa-kev-lookup`, `epss-score`, `cve-intel-chain`, `takeover`, `exposed-assets`, `git-secrets` |
| **Identity / social** | `holehe`, `maigret`, `sherlock`, `theharvester`, `ghunt`, `hunter-io`, `gravatar`, `keybase`, `github-user`/`-emails`, `linkedin`, `twitter`, `reddit*`, `bluesky`, `mastodon`, `hackernews*`, `instagram`, `farcaster-user-lookup`, `bio-link-resolve`, `bsky-starter-pack-extract` |
| **Breach / leak** | `hibp`, `dehashed`, `intelx`, `hudsonrock-cavalier` |
| **Corporate / civic** | `opencorporates`, `opensanctions`, `gleif-lei-lookup`, `sec-edgar`, `propublica-nonprofit`, `fec-donations-lookup`, `lda-lobbying-search`, `usaspending-search`, `courtlistener-search`, `federal-register-search`, `govtrack-search` |
| **Scientific / academic** | `pubmed-search`, `arxiv-search`, `biorxiv-search`, `dblp-search`, `crossref-paper-search`, `openalex-search`, `orcid-search`, `ror-org-lookup`, `nih-reporter-search`, `nsf-awards-search`, `clinicaltrials-search`, `openfda-search`, `pubchem-compound-lookup` |
| **BigQuery (paid)** | `bigquery-gdelt`, `bigquery-github-archive`, `bigquery-patents`, `bigquery-stack-overflow`, `bigquery-wikipedia-pageviews`, `bigquery-trending-now` |
| **Crypto / blockchain** | `ens-resolve`, `onchain-tx-analysis`, `defillama-intel`, `coingecko-search` |
| **Geo / vision** | `geo-vision`, `reverse-image`, `google-lens-search`, `nominatim-geocode`, `osm-overpass-query`, `google-maps-places`, `census-geocoder`, `census-acs-tract`, `rest-countries-lookup`, `usgs-earthquake-search`, `openmeteo-search`, `usno-astronomy` |
| **Documents / files** | `pdf-analyze`, `exif`, `documentcloud-search`, `internet-archive-search`, `wayback`, `wayback-url-history` |
| **Trackers / fingerprinting** | `tracker-extract`, `tracker-extract-rendered`, `tracker-correlate`, `tracker-pivot`, `mail-correlate`, `unicode-homoglyph-normalize`, `entity-match`, `entity-link-finder` |
| **LLM / agentic** | `gemini-tools`, `gemini-code-execution`, `gemini-image-analyze`, `panel-consult`, `panel-entity-resolution`, `prompt-injection-scanner` |
| **Meta-tools (orchestrators)** | `person-aggregate`, `domain-aggregate`, `synthesis` |

## Using it effectively for OSINT

Three patterns get you the most leverage from the catalog. All examples assume you're talking to your LLM client (Claude Desktop / Cursor) with the MCP server connected — the LLM picks tools, you direct strategy.

### 1. Start with a meta-tool, then drill down

`person-aggregate` and `domain-aggregate` are orchestrators: they fan out to a curated set of underlying tools, normalize results, and hand back a single structured summary. **Always start here** instead of hand-wiring a 12-tool sequence.

> *"Use `domain-aggregate` on `acme.com`. Then for any subdomain that scores >0.7 confidence, run `tech-stack`, `http-probe`, and `swagger-openapi-finder`. Surface anything pointing to staging/dev environments."*

> *"Use `person-aggregate` on `username=jdoe, email=jdoe@example.com`. For every social platform with a hit, fetch the most recent 30 days of activity. Cross-reference timestamps for timezone leakage."*

### 2. Use multi-source corroboration before acting on a single hit

OSINT is full of false positives — username collisions, expired CT entries, scraped/stale breach indices. The catalog has overlapping tools on purpose. Tell the LLM which corroboration chain you want:

- **Username → real identity:** `maigret` ∪ `sherlock` → `gravatar` + `keybase` + `github-user` → `hibp` / `dehashed` → `mail-correlate`.
- **Domain → ownership:** `whois` + `ct` + `dns-lookup` → `favicon-pivot` (find sister sites) → `wayback` (historical owners) → `opencorporates` (registered org).
- **Image → location:** `exif` (EXIF GPS) → `reverse-image` + `google-lens-search` → `geo-vision` (LLM panel) → `nominatim-geocode` for human-readable.

### 3. Treat every call as auditable

Every tool invocation writes `tool.called` (with arguments) and either `tool.succeeded` or `tool.failed` to the `events` table, partitioned by month per tenant. Useful for:

- Reconstructing an investigation timeline weeks later.
- Showing a client/editor exactly which sources were touched.
- Replaying a recon session (the inputs are persisted).
- Billing transparency (millicredit cost is on every event).

You can query directly: `SELECT * FROM events WHERE tenant_id = $1 ORDER BY ts DESC;` — partitioned tables are transparent in Postgres.

> **Legal & ethical note:** This stack is built for authorized engagements (bug bounties with scope, your own assets, sanctioned investigative journalism, CTFs, security research). Several tools query paid/private breach indices that are restricted to specific use cases. Read each tool's source for upstream ToS, and don't use the platform to harass, dox, or stalk private individuals.

## Configuration reference

All configuration is via `.env` at the repo root. `.env.example` is the canonical template — copy it and fill in real values for anything you need.

### Core

| Var | Default | Notes |
|---|---|---|
| `NODE_ENV` | `development` | `development` is the only env where `DEV_AUTH_BYPASS=true` is honored. |
| `PORT` | `3000` | API listen port. |
| `LOG_LEVEL` | `debug` | `pino` levels (`trace`/`debug`/`info`/`warn`/`error`/`fatal`). |
| `DEV_AUTH_BYPASS` | `false` | When `true` (+ `NODE_ENV=development`), skips Firebase verify, credit metering, event writes. **Never set in any deployed env.** |

### Postgres / Dragonfly

| Var | Default |
|---|---|
| `DATABASE_URL` | `postgres://osint:osint@localhost:5434/osint_dev?sslmode=disable` |
| `REDIS_URL` | `redis://localhost:6380` |

### Firebase Auth

Provide either a file path **or** inline JSON — not both:

```env
GOOGLE_APPLICATION_CREDENTIALS=./firebase-admin-dev.json
# OR
FIREBASE_SERVICE_ACCOUNT_JSON={"type":"service_account",...}

FIREBASE_PROJECT_ID=osint-agent-dev
```

### Worker dispatch (Ed25519 signed)

```env
GO_WORKER_URL=http://localhost:8181
PY_WORKER_URL=http://localhost:8182
WORKER_SIGNING_KEY_HEX=<32-byte ed25519 seed, hex>
WORKER_PUBLIC_KEY_HEX=<32-byte ed25519 public key, hex>
```

`bun run gen:worker-keys` generates a fresh matched pair (idempotent — won't overwrite valid existing values). The API signs `${unix_timestamp}\n${json_body}`; both workers verify the signature and reject anything more than 60s out of skew.

### Stripe (only for hosted-tier billing)

```env
STRIPE_SECRET_KEY=sk_...
STRIPE_WEBHOOK_SECRET=whsec_...
STRIPE_PRICE_ID_HUNTER=price_...
STRIPE_PRICE_ID_OPERATOR=price_...
CREDIT_PRICE_USD=0.01
```

### LLM Gateway

```env
ANTHROPIC_API_KEY=sk-ant-...    # Phase 0 primary backend
```

### Telemetry (optional)

```env
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-prod-us-central-0.grafana.net/otlp
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic%20...
OTEL_SERVICE_NAME=osint-api-dev
```

If `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, OpenTelemetry is disabled (warned to the log).

### Per-tool API keys

Many tools require their own upstream key (Shodan, Censys, IntelX, HIBP, urlscan, hunter.io, …). Each tool reads its key from `.env` directly — see the tool source for the variable name. Tools without a key registered will return a structured error rather than crash.

## Adding a new tool

A new tool requires three coordinated edits, in this order:

1. **Worker side** — implement the function in `apps/go-worker/internal/tools/<name>.go` (or `apps/py-worker/src/py_worker/tools/<name>.py`) and register it in the dispatch:
   - Go: add a `case` to `switch req.Tool` in `apps/go-worker/internal/server/server.go`.
   - Py: add an entry to the `_TOOLS` dict in `apps/py-worker/src/py_worker/main.py`.

2. **API side** — create `apps/api/src/mcp/tools/<kebab-name>.ts` and call `toolRegistry.register({ name, description, inputSchema, costMillicredits, handler })`. Use `callGoWorker` / `callPyWorker` inside the handler to dispatch to the worker.

3. **Registry** — add `import "./<kebab-name>";` to `apps/api/src/mcp/tools/registry.ts` **above the meta-tools section at the bottom**. `person-aggregate` and `domain-aggregate` enumerate other tools by name at registration time, so they must import last.

> **Critical:** the tool name string must match exactly across all three layers — the `ToolRegistry.invoke` lookup, the worker switch, and the meta-tool dispatch plan. There is no compile-time check; a typo silently routes to "unknown tool".

`apps/api/src/mcp/tools/instance.ts::ToolRegistry.invoke` is the single chokepoint for execution: it writes `tool.called`, calls `spendCredits` (atomic UPDATE-WHERE-balance>=cost), runs the handler, and on failure refunds via negative-cost `spendCredits` and writes `tool.failed`. **Don't update `tenants.credits_balance` outside `spendCredits` / `grantCredits`** — they keep the balance in sync with the append-only `credit_ledger` inside one transaction.

## Database & migrations

- Postgres 16, single schema, `postgres` driver (no Knex/Prisma).
- Migrations are plain SQL via `dbmate` in `apps/api/src/db/migrations/`. Schema dump lands at `apps/api/src/db/schema.sql`.
- Every migration **must** be idempotent (`IF NOT EXISTS` / `IF EXISTS` / `hasTable` guards). Never write a migration that fails on re-run.
- `events` is partitioned by month. The migration pre-creates 4 months; `apps/api/src/events/stream.ts::ensureCurrentMonthPartition` is the runtime safety net. (Phase 1 moves this to a cron.)

```sh
bun run db:up          # docker compose up -d
bun run db:migrate     # dbmate up
bun run db:rollback    # dbmate rollback (last batch)
bun run db:down        # docker compose down
```

## Testing, linting, building

```sh
bun run lint           # per-workspace; runs `tsc --noEmit` for TS workspaces
bun run test           # per-workspace
bun run build          # per-workspace
```

Single-test commands:

```sh
# TS
cd apps/api && bun test src/path/to.test.ts

# Go
cd apps/go-worker && go test ./internal/tools -run TestName

# Python
cd apps/py-worker && uv run pytest tests/path/test_x.py::test_y
```

CI (`.github/workflows/ci.yml`) runs all three lanes independently. The TS lane spins up a Postgres service container, runs migrations, then `bun run lint && bun run test && bun run build`. **Always run `bun run build` locally before pushing** — `tsc --noEmit` is necessary but not sufficient; the build pipeline includes additional validation.

## Deployment

Three Fly.io machines, deployed via `.github/workflows/deploy.yml` on push to `main`:

1. Workers first (`go-worker`, `py-worker`).
2. API last (depends on workers being up).

The API deploy uses `--config apps/api/fly.toml --build-context .` from the repo root because the API Dockerfile pulls in `packages/shared-types`. Don't change the build context without testing — see `b0fd375 fix(deploy): correct Dockerfile build contexts for Fly deploy`.

## Contributing

PRs welcome — especially:

1. **Tool adapters** following the protocol above.
2. **Adversary playbook templates** (structured subgraph patterns).
3. **Documentation improvements.**
4. **Bug reports with reproductions.**

Before opening a PR, read [`CONTRIBUTING.md`](./CONTRIBUTING.md). The proprietary moat (World Model, Adversary Library beyond 3 example playbooks, Federated Learning, Predictive Temporal Layer, Investigator Policy Network) lives in a private repo — PRs touching those will be redirected.

Contributors get Hunter-tier credits as thanks. High-value contributors get Operator-tier credits. Adversary playbook authors get co-author credit in the published case-study series.

## License

Apache-2.0 — see [LICENSE](./LICENSE).
