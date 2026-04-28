import type { WorkerToolRequest, WorkerToolResponse } from "@osint/shared-types";
import { config } from "../config";

/**
 * Ed25519-signed POST to the Go worker.
 * Signs the canonical bytes: `${timestamp}\n${body}`.
 * Worker rejects requests with drift > 60s or bad signature.
 */
export async function callGoWorker<I, O>(req: WorkerToolRequest<I>): Promise<WorkerToolResponse<O>> {
  const body = JSON.stringify(req);
  const ts = Math.floor(Date.now() / 1000).toString();
  const signingBytes = new TextEncoder().encode(`${ts}\n${body}`);

  const seed = hexToBytes(config.workers.signingKeyHex);
  const sig = await signEd25519(seed, signingBytes);

  const res = await fetch(`${config.workers.goUrl}/tool`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-osint-ts": ts,
      "x-osint-sig": bytesToHex(sig),
    },
    body,
    signal: AbortSignal.timeout(req.timeoutMs + 500),
  });

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`go-worker ${res.status}: ${text}`);
  }
  return (await res.json()) as WorkerToolResponse<O>;
}

// Use Bun's crypto (Web Crypto). Ed25519 is supported.
async function signEd25519(seed32: Uint8Array, message: Uint8Array): Promise<Uint8Array> {
  // Web Crypto requires PKCS8 for private key import; we derive it from seed once per process.
  const key = await importEd25519PrivateKey(seed32);
  const sigBuf = await crypto.subtle.sign("Ed25519", key, message as BufferSource);
  return new Uint8Array(sigBuf);
}

let cachedKey: CryptoKey | null = null;
async function importEd25519PrivateKey(seed32: Uint8Array): Promise<CryptoKey> {
  if (cachedKey) return cachedKey;
  // PKCS8 prefix for Ed25519 private key (32 bytes of seed)
  const pkcs8Prefix = new Uint8Array([
    0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
  ]);
  const pkcs8 = new Uint8Array(pkcs8Prefix.length + seed32.length);
  pkcs8.set(pkcs8Prefix, 0);
  pkcs8.set(seed32, pkcs8Prefix.length);
  cachedKey = await crypto.subtle.importKey("pkcs8", pkcs8, { name: "Ed25519" }, false, ["sign"]);
  return cachedKey;
}

function hexToBytes(hex: string): Uint8Array {
  if (hex.length % 2 !== 0) throw new Error("invalid hex");
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.substring(i * 2, i * 2 + 2), 16);
  }
  return out;
}

function bytesToHex(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}
