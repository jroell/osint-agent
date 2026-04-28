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
