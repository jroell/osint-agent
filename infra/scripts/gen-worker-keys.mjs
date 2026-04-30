#!/usr/bin/env node
/**
 * Generates an Ed25519 keypair for API ↔ worker request signing and writes/updates
 *   WORKER_SIGNING_KEY_HEX (seed, used by apps/api)
 *   WORKER_PUBLIC_KEY_HEX  (public key, used by apps/go-worker + apps/py-worker)
 * in the project-root `.env` file. Idempotent: if both keys already look like
 * 32-byte hex values, the script is a no-op so re-running `bun run dev` keeps
 * the existing keys.
 *
 * Runnable under both Bun and Node (uses node:crypto, no Web Crypto needed).
 */
import { generateKeyPairSync } from "node:crypto";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
const ENV_PATH = resolve(ROOT, ".env");
const HEX64 = /^[0-9a-fA-F]{64}$/;

function loadEnv(path) {
  if (!existsSync(path)) return { lines: [], map: new Map() };
  const text = readFileSync(path, "utf8");
  const lines = text.split("\n");
  const map = new Map();
  for (const line of lines) {
    const m = line.match(/^([A-Z_][A-Z0-9_]*)=(.*)$/);
    if (m) map.set(m[1], m[2]);
  }
  return { lines, map };
}

function upsert(lines, key, value) {
  const idx = lines.findIndex((l) => l.startsWith(`${key}=`));
  if (idx >= 0) lines[idx] = `${key}=${value}`;
  else lines.push(`${key}=${value}`);
  return lines;
}

const { lines, map } = loadEnv(ENV_PATH);

const seedExisting = map.get("WORKER_SIGNING_KEY_HEX") ?? "";
const pubExisting = map.get("WORKER_PUBLIC_KEY_HEX") ?? "";
if (HEX64.test(seedExisting) && HEX64.test(pubExisting)) {
  console.log("worker keypair already present in .env — skipping");
  process.exit(0);
}

const { publicKey, privateKey } = generateKeyPairSync("ed25519");
// PKCS8 DER for ed25519 ends with the 32-byte seed; SPKI DER ends with the 32-byte public key.
const privDer = privateKey.export({ format: "der", type: "pkcs8" });
const pubDer = publicKey.export({ format: "der", type: "spki" });
const seedHex = privDer.subarray(privDer.length - 32).toString("hex");
const pubHex = pubDer.subarray(pubDer.length - 32).toString("hex");

let next = lines;
if (next.length && next[next.length - 1] !== "") next.push("");
next = upsert(next, "WORKER_SIGNING_KEY_HEX", seedHex);
next = upsert(next, "WORKER_PUBLIC_KEY_HEX", pubHex);

writeFileSync(ENV_PATH, next.join("\n"));
console.log(`wrote ed25519 keypair to ${ENV_PATH}`);
