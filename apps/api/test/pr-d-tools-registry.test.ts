import { describe, it, expect } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";

const PR_D_TOOLS = [
  "wikitree_lookup",
  "adb_search",
  "familysearch_lookup",
] as const;

describe("PR-D (genealogy) tools registered", () => {
  for (const name of PR_D_TOOLS) {
    it(`registers ${name}`, () => {
      const tool = toolRegistry.list().find((t) => t.name === name);
      expect(tool, `expected tool ${name} in registry`).toBeDefined();
      expect(tool!.description.length).toBeGreaterThan(40);
    });
  }
  it("each tool description advertises the entity envelope contract", () => {
    for (const name of PR_D_TOOLS) {
      const t = toolRegistry.list().find((t) => t.name === name);
      const desc = (t?.description ?? "").toLowerCase();
      expect(desc.includes("entity") || desc.includes("kind")).toBe(true);
    }
  });
});
