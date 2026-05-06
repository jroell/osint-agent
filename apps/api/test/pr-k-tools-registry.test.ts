import { describe, it, expect } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";

const PR_K_TOOLS = [
  "serpapi_google_scholar",
  "people_data_labs",
  "crunchbase_lookup",
  "securitytrails_lookup",
] as const;

describe("PR-K (Tier-3 paid) tools registered", () => {
  for (const name of PR_K_TOOLS) {
    it(`registers ${name}`, () => {
      const tool = toolRegistry.list().find((t) => t.name === name);
      expect(tool, `expected tool ${name} in registry`).toBeDefined();
      expect(tool!.description.length).toBeGreaterThan(40);
    });
  }
  it("each PR-K tool description advertises both REQUIRES <key> and entity envelope contract", () => {
    for (const name of PR_K_TOOLS) {
      const t = toolRegistry.list().find((t) => t.name === name);
      const desc = (t?.description ?? "");
      expect(desc).toContain("REQUIRES");
      expect(desc.toLowerCase().includes("entity") || desc.toLowerCase().includes("kind")).toBe(true);
    }
  });
});
