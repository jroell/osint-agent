import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(1).describe("Hostname or IP to probe"),
  ports: z.array(z.number().int().min(1).max(65535)).optional()
    .describe("Optional explicit port list. Default: top-100 well-known ports."),
  per_port_timeout_ms: z.number().int().min(200).max(10000).default(1500),
  concurrency: z.number().int().min(1).max(256).default(64),
});

toolRegistry.register({
  name: "port_scan_passive",
  description:
    "TCP connect-scan against a host's top-100 well-known ports (or a caller-supplied list). Stdlib-only Go implementation — no SYN scanning, no root, no naabu. NOTE: this DOES actively probe the target — only run against hosts you have explicit authorization to scan. Despite the 'passive' label inherited from the design doc, the network behavior is active.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "port_scan_passive",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "port_scan_passive failed");
    return res.output;
  },
});
