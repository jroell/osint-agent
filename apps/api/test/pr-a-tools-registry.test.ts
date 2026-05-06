import { describe, it, expect } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";

const PR_A_TOOLS = [
  "tmdb_lookup",
  "tvmaze_lookup",
  "scryfall_lookup",
  "ygoprodeck_lookup",
] as const;

describe("PR-A tools registered", () => {
  for (const name of PR_A_TOOLS) {
    it(`registers ${name}`, () => {
      const tool = toolRegistry.list().find((t) => t.name === name);
      expect(tool, `expected tool ${name} in registry`).toBeDefined();
      expect(tool!.description.length).toBeGreaterThan(40);
      expect(tool!.inputSchema, `${name} must have input schema`).toBeDefined();
    });
  }

  it("each tool emits a knowledge-graph entity envelope (schema-only check)", () => {
    // Schema-level guarantee: each tool description mentions 'entity' or 'kind' so the
    // panel_entity_resolution / entity_link_finder pipelines can ingest outputs. The
    // actual envelope is produced by the Go worker; this test asserts the API
    // descriptions correctly advertise the contract.
    for (const name of PR_A_TOOLS) {
      const t = toolRegistry.list().find((t) => t.name === name);
      const desc = (t?.description ?? "").toLowerCase();
      expect(desc.includes("entity") || desc.includes("kind") || desc.includes("typed")).toBe(true);
    }
  });
});
