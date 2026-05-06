import { describe, it, expect } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";

const PR_E_TOOLS = [
  "hathitrust_search",
  "gallica_search",
  "npgallery_search",
  "ndl_japan_search",
] as const;

describe("PR-E (heritage / non-English archives) tools registered", () => {
  for (const name of PR_E_TOOLS) {
    it(`registers ${name}`, () => {
      const tool = toolRegistry.list().find((t) => t.name === name);
      expect(tool, `expected tool ${name} in registry`).toBeDefined();
      expect(tool!.description.length).toBeGreaterThan(40);
    });
  }
  it("each tool description advertises the entity envelope contract", () => {
    for (const name of PR_E_TOOLS) {
      const t = toolRegistry.list().find((t) => t.name === name);
      const desc = (t?.description ?? "").toLowerCase();
      expect(desc.includes("entity") || desc.includes("kind")).toBe(true);
    }
  });
});
