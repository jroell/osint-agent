import { afterEach, describe, expect, it } from "bun:test";
import "../src/mcp/tools/registry";
import { toolRegistry } from "../src/mcp/tools/instance";
import { classifyFetchForEscalation } from "../src/browserbase/client";

const ctx = { tenantId: "tenant-test", userId: "user-test", email: "jason@example.com" };
const originalFetch = globalThis.fetch;
const originalApiKey = process.env.BROWSERBASE_API_KEY;
const originalProjectId = process.env.BROWSERBASE_PROJECT_ID;
const originalFunctionId = process.env.BROWSERBASE_SWARM_FUNCTION_ID;

function tool(name: string) {
  return (toolRegistry as unknown as { tools: Map<string, { inputSchema: { parse: (v: unknown) => unknown }; handler: Function }> })
    .tools.get(name)!;
}

afterEach(() => {
  globalThis.fetch = originalFetch;
  process.env.BROWSERBASE_API_KEY = originalApiKey;
  process.env.BROWSERBASE_PROJECT_ID = originalProjectId;
  process.env.BROWSERBASE_SWARM_FUNCTION_ID = originalFunctionId;
});

describe("Browserbase tools", () => {
  it("classifies blocked or short Browserbase Fetch responses for browser escalation", () => {
    expect(
      classifyFetchForEscalation(
        { url: "https://example.com", statusCode: 403, content: "Just a moment... Cloudflare" },
        500,
      ),
    ).toEqual({
      blocked: true,
      reasons: ["status_403", "short_content_27", "matched_cloudflare"],
    });
  });

  it("browserbase_retrieve searches, fetches, and escalates blocked pages to sessions", async () => {
    process.env.BROWSERBASE_API_KEY = "bb_test";
    process.env.BROWSERBASE_PROJECT_ID = "proj_test";
    const calls: Array<{ url: string; body: any }> = [];

    globalThis.fetch = (async (url, init) => {
      const body = JSON.parse(String(init?.body ?? "{}"));
      calls.push({ url: String(url), body });
      if (String(url).endsWith("/v1/search")) {
        return Response.json({
          requestId: "req_1",
          results: [
            { title: "Blocked", url: "https://blocked.example", snippet: "blocked result" },
            { title: "Open", url: "https://open.example", snippet: "open result" },
          ],
        });
      }
      if (String(url).endsWith("/v1/fetch") && body.url === "https://blocked.example") {
        return Response.json({ url: body.url, statusCode: 403, content: "Just a moment... Cloudflare" });
      }
      if (String(url).endsWith("/v1/fetch")) {
        return Response.json({ url: body.url, statusCode: 200, content: "x".repeat(700), metadata: { title: "Open" } });
      }
      if (String(url).endsWith("/v1/sessions")) {
        return Response.json({ id: "sess_1", status: "RUNNING", connectUrl: "wss://connect.example" });
      }
      return Response.json({ message: "unexpected" }, { status: 500 });
    }) as typeof fetch;

    const def = tool("browserbase_retrieve");
    const parsed = def.inputSchema.parse({ query: "blocked target", fetch_top_n: 2 });
    const out = await def.handler(parsed, ctx) as any;

    expect(out.search.results).toHaveLength(2);
    expect(out.pages).toHaveLength(2);
    expect(out.pages[0].escalation.required).toBe(true);
    expect(out.pages[0].escalation.session.id).toBe("sess_1");
    expect(out.pages[1].escalation.required).toBe(false);
    expect(calls.map((c) => c.url)).toEqual([
      "https://api.browserbase.com/v1/search",
      "https://api.browserbase.com/v1/fetch",
      "https://api.browserbase.com/v1/fetch",
      "https://api.browserbase.com/v1/sessions",
    ]);
    expect(calls.at(3)?.body.userMetadata.escalation_reasons).toContain("status_403");
  });

  it("browserbase_swarm creates one Browserbase session per worker", async () => {
    process.env.BROWSERBASE_API_KEY = "bb_test";
    process.env.BROWSERBASE_PROJECT_ID = "proj_test";
    const bodies: any[] = [];

    globalThis.fetch = (async (_url, init) => {
      const body = JSON.parse(String(init?.body ?? "{}"));
      bodies.push(body);
      return Response.json({
        id: `sess_${bodies.length}`,
        status: "RUNNING",
        connectUrl: `wss://connect.example/${bodies.length}`,
      });
    }) as typeof fetch;

    const def = tool("browserbase_swarm");
    const parsed = def.inputSchema.parse({
      goal: "Compare two blocked portals",
      tasks: [
        { id: "a", url: "https://a.example", instruction: "Open and inspect account status" },
        { id: "b", url: "https://b.example", instruction: "Open and inspect account status", depends_on: ["a"] },
      ],
    });
    const out = await def.handler(parsed, ctx) as any;

    expect(out.workers).toHaveLength(2);
    expect(out.workers[0].session.id).toBe("sess_1");
    expect(out.coordination.dependency_edges).toEqual([{ from: "a", to: "b" }]);
    expect(bodies[0].userMetadata.goal).toBe("Compare two blocked portals");
    expect(bodies[1].userMetadata.depends_on).toEqual(["a"]);
  });
});
