import { describe, it, expect } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";

const PR_H_TOOLS = [
  "tiktok_lookup",
  "twitter_rapidapi",
  "youtube_search_rapidapi",
  // linkedin_proxycurl already existed; we extended it but it remains the same name.
  "linkedin_proxycurl",
  "cia_factbook",
] as const;

describe("PR-G/H (vurvey port + factbook) tools registered", () => {
  for (const name of PR_H_TOOLS) {
    it(`registers ${name}`, () => {
      const tool = toolRegistry.list().find((t) => t.name === name);
      expect(tool, `expected tool ${name} in registry`).toBeDefined();
      expect(tool!.description.length).toBeGreaterThan(40);
    });
  }
  it("LinkedIn Proxycurl tool description advertises new multi-mode capability", () => {
    const tool = toolRegistry.list().find((t) => t.name === "linkedin_proxycurl");
    expect(tool?.description).toContain("7 modes");
    expect(tool?.description).toContain("company_profile");
    expect(tool?.description).toContain("find_company_role");
  });
  it("Each PR-H social tool description advertises entity envelope contract", () => {
    for (const name of ["tiktok_lookup", "twitter_rapidapi", "youtube_search_rapidapi"]) {
      const t = toolRegistry.list().find((t) => t.name === name);
      const desc = (t?.description ?? "").toLowerCase();
      expect(desc.includes("entity") || desc.includes("kind")).toBe(true);
    }
  });
});
