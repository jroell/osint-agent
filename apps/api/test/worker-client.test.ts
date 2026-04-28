import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import { callGoWorker } from "../src/workers/go-client";
import { config } from "../src/config";

let server: ReturnType<typeof Bun.serve>;
let lastHeaders: Record<string, string> = {};
let lastBody = "";

// Remember original values so we can restore after the test (other tests may
// have loaded `config` before us and expect the original worker URL).
let originalGoUrl: string;
let originalSigningKey: string;

beforeAll(() => {
  // Mutate the already-loaded config object directly; `config` is a frozen
  // facade over process.env at module-load time, so changing process.env here
  // would be a no-op.
  originalGoUrl = config.workers.goUrl;
  originalSigningKey = config.workers.signingKeyHex;
  (config.workers as { goUrl: string }).goUrl = "http://localhost:8799";
  (config.workers as { signingKeyHex: string }).signingKeyHex = "01".repeat(32);

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

afterAll(() => {
  server.stop();
  (config.workers as { goUrl: string }).goUrl = originalGoUrl;
  (config.workers as { signingKeyHex: string }).signingKeyHex = originalSigningKey;
});

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
