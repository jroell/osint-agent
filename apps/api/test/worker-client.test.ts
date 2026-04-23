import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import { callGoWorker } from "../src/workers/go-client";

let server: ReturnType<typeof Bun.serve>;
let lastHeaders: Record<string, string> = {};
let lastBody = "";

beforeAll(() => {
  process.env.GO_WORKER_URL = "http://localhost:8799";
  // Stable test key: 32 bytes of 0x01 encoded as hex
  process.env.WORKER_SIGNING_KEY_HEX = "01".repeat(32);

  server = Bun.serve({
    port: 8799,
    async fetch(req) {
      lastHeaders = Object.fromEntries(req.headers);
      lastBody = await req.text();
      return new Response(
        JSON.stringify({
          requestId: "x",
          ok: true,
          output: { echo: true },
          telemetry: { tookMs: 1, cacheHit: false },
        }),
        { headers: { "content-type": "application/json" } },
      );
    },
  });
});

afterAll(() => server.stop());

describe("callGoWorker", () => {
  it("signs the request and gets a response", async () => {
    const res = await callGoWorker({
      requestId: "r1",
      tenantId: "t1",
      userId: "u1",
      tool: "noop",
      input: { hello: "world" },
      timeoutMs: 2000,
    });
    expect(res.ok).toBe(true);
    expect(lastHeaders["x-osint-ts"]).toMatch(/^\d+$/);
    expect(lastHeaders["x-osint-sig"]).toMatch(/^[0-9a-f]{128}$/); // 64 bytes = 128 hex chars
    expect(JSON.parse(lastBody).tool).toBe("noop");
  });
});
