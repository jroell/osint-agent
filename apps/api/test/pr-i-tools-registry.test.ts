import { describe, it, expect } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";

const PR_I_TOOLS = [
  "icij_offshore_leaks",
  "instagram_rapidapi",
  "browserbase_session",
  "inaturalist_search",
] as const;

describe("PR-I (deferred high-value batch) tools registered", () => {
  for (const name of PR_I_TOOLS) {
    it(`registers ${name}`, () => {
      const tool = toolRegistry.list().find((t) => t.name === name);
      expect(tool, `expected tool ${name} in registry`).toBeDefined();
      expect(tool!.description.length).toBeGreaterThan(40);
    });
  }
  it("each tool description advertises the entity envelope contract", () => {
    for (const name of PR_I_TOOLS) {
      const t = toolRegistry.list().find((t) => t.name === name);
      const desc = (t?.description ?? "").toLowerCase();
      expect(desc.includes("entity") || desc.includes("kind")).toBe(true);
    }
  });
  it("ICIJ description advertises Offshore Leaks coverage", () => {
    const t = toolRegistry.list().find((t) => t.name === "icij_offshore_leaks");
    expect(t?.description).toContain("Offshore Leaks");
    expect(t?.description.toLowerCase()).toMatch(/pandora|panama|paradise/);
  });
});
