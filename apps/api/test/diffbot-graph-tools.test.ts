import { describe, it, expect } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";

const DIFFBOT_GRAPH_TOOLS = [
  "diffbot_entity_network",
  "diffbot_common_neighbors",
  "diffbot_article_co_mentions",
] as const;

describe("Diffbot graph tools", () => {
  for (const name of DIFFBOT_GRAPH_TOOLS) {
    it(`registers ${name} with a graph-oriented contract`, () => {
      const tool = toolRegistry.list().find((t) => t.name === name);
      expect(tool, `expected tool ${name} in registry`).toBeDefined();
      const desc = tool!.description.toLowerCase();
      expect(desc).toContain("diffbot");
      expect(desc).toContain("knowledge graph");
      expect(desc).toContain("relationship");
      expect(desc).toContain("canonical");
      expect(tool!.inputSchema).toBeDefined();
    });
  }
});
