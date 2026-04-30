/**
 * BrowseComp dataset prep: decrypts the public CSV using per-row canary
 * keys, emits a clean JSONL of {id, question, answer, topic} rows.
 *
 * The CSV is at:
 *   https://openaipublic.blob.core.windows.net/simple-evals/browse_comp_test_set.csv
 *
 * Each row has columns problem,answer,problem_topic,canary. Both `problem`
 * and `answer` are XOR-encrypted with a SHA256-derived key from `canary`,
 * base64-encoded. Decryption mirrors the reference at
 * https://github.com/openai/simple-evals/blob/main/browsecomp_eval.py
 *
 * Run:
 *   bun packages/benchmark-suite/src/benchmarks/browsecomp-prep.ts \
 *     benchmark-results/datasets/browsecomp.csv \
 *     benchmark-results/datasets/browsecomp.jsonl
 */
import { readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";

function deriveKey(password: string, length: number): Uint8Array {
  const hashed = createHash("sha256").update(password).digest();
  const out = new Uint8Array(length);
  for (let i = 0; i < length; i++) out[i] = hashed[i % hashed.length]!;
  return out;
}

function decrypt(b64: string, password: string): string {
  const cipher = Uint8Array.from(Buffer.from(b64, "base64"));
  const key = deriveKey(password, cipher.length);
  const plain = new Uint8Array(cipher.length);
  for (let i = 0; i < cipher.length; i++) plain[i] = cipher[i]! ^ key[i]!;
  return new TextDecoder().decode(plain);
}

/** RFC-4180-ish CSV parser sufficient for this dataset (no embedded quotes in our fields). */
function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let inQuotes = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i]!;
    if (inQuotes) {
      if (c === '"' && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (c === '"') {
        inQuotes = false;
      } else {
        cell += c;
      }
    } else {
      if (c === '"') inQuotes = true;
      else if (c === ",") {
        row.push(cell);
        cell = "";
      } else if (c === "\n") {
        row.push(cell);
        rows.push(row);
        row = [];
        cell = "";
      } else if (c === "\r") {
        // skip
      } else {
        cell += c;
      }
    }
  }
  if (cell.length > 0 || row.length > 0) {
    row.push(cell);
    rows.push(row);
  }
  return rows;
}

const inPath = process.argv[2] ?? "benchmark-results/datasets/browsecomp.csv";
const outPath = process.argv[3] ?? "benchmark-results/datasets/browsecomp.jsonl";

const csv = parseCsv(readFileSync(inPath, "utf8"));
const header = csv[0]!;
const idx = (name: string) => header.indexOf(name);
const iProblem = idx("problem");
const iAnswer = idx("answer");
const iTopic = idx("problem_topic");
const iCanary = idx("canary");

const out: string[] = [];
let id = 0;
for (let r = 1; r < csv.length; r++) {
  const row = csv[r]!;
  if (row.length < header.length) continue;
  const canary = row[iCanary]!;
  if (!canary) continue;
  try {
    const question = decrypt(row[iProblem]!, canary);
    const answer = decrypt(row[iAnswer]!, canary);
    const topic = row[iTopic] ?? "";
    out.push(JSON.stringify({ id: `bc-${String(id).padStart(4, "0")}`, question, answer, topic }));
    id++;
  } catch (e) {
    // skip malformed rows; report at end
  }
}

writeFileSync(outPath, out.join("\n") + "\n");
console.log(`decrypted ${out.length} questions → ${outPath}`);
