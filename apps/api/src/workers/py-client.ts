import type { WorkerToolRequest, WorkerToolResponse } from "@osint/shared-types";
import { config } from "../config";

// (Shares signing code from go-client; in production factor into shared module.
// Keeping duplicated intentionally for plan clarity; refactor in Plan 2.)
export async function callPyWorker<I, O>(req: WorkerToolRequest<I>): Promise<WorkerToolResponse<O>> {
  const { callGoWorker } = await import("./go-client");
  // Temporary: py-client just overrides the base URL. Implementation parity in Plan 2.
  const originalUrl = config.workers.goUrl;
  (config.workers as any).goUrl = config.workers.pyUrl;
  try {
    return await callGoWorker<I, O>(req);
  } finally {
    (config.workers as any).goUrl = originalUrl;
  }
}
