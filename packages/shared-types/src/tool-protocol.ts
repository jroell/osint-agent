export interface WorkerToolRequest<Input = unknown> {
  requestId: string;            // UUID; for idempotency + trace
  tenantId: string;
  userId: string;
  tool: string;                 // "subfinder_passive" | "dns_lookup_comprehensive" | "stealth_http_fetch" | ...
  input: Input;
  timeoutMs: number;
}

export interface WorkerToolResponse<Output = unknown> {
  requestId: string;
  ok: boolean;
  output?: Output;
  error?: { code: string; message: string };
  telemetry: {
    tookMs: number;
    cacheHit: boolean;
    proxyUsed?: string;
  };
}
