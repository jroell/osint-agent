import { describe, it, expect } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";

const PR_L_TOOLS = [
  "marinetraffic_lookup",
  "flightaware_lookup",
  "sentinel_hub_imagery",
  "brave_search",
] as const;

describe("PR-L (final paid commercial batch) tools registered", () => {
  for (const name of PR_L_TOOLS) {
    it(`registers ${name}`, () => {
      const tool = toolRegistry.list().find((t) => t.name === name);
      expect(tool, `expected tool ${name} in registry`).toBeDefined();
      expect(tool!.description.length).toBeGreaterThan(40);
    });
  }
  it("each PR-L tool description advertises REQUIRES <key> + entity envelope contract", () => {
    for (const name of PR_L_TOOLS) {
      const t = toolRegistry.list().find((t) => t.name === name);
      const desc = (t?.description ?? "");
      expect(desc).toContain("REQUIRES");
      expect(desc.toLowerCase().includes("entity") || desc.toLowerCase().includes("kind")).toBe(true);
    }
  });
});
