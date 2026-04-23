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
