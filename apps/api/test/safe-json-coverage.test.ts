import { describe, it, expect } from "bun:test";
import { safeJSON } from "../src/llm/panel";

// legacySafeJSON reproduces the pre-fix logic that lived inline inside
// panel-entity-resolution.ts: fence strip + greedy { ... } match +
// JSON.parse — NO truncation repair. Used to compute the BEFORE rate
// numerically.
function legacySafeJSON(text: string): unknown {
  try {
    let s = text.trim();
    const fence = s.match(/```(?:json)?\s*\n?([\s\S]*?)\n?```/);
    if (fence) s = fence[1]!;
    const obj = s.match(/\{[\s\S]*\}/);
    if (obj) s = obj[0];
    return JSON.parse(s);
  } catch {
    return null;
  }
}

// TestSafeJSON_LLMOutputCoverageQuantitative is the proof-of-improvement
// test for iteration 10 of the /loop "make osint-agent quantitatively
// better" task.
//
// The defect: panel-entity-resolution.ts had its own inline JSON
// extractor (fence strip + greedy { ... } match) that did NOT include
// the truncation-repair logic from panel.ts's safeJSON. Real LLM
// outputs frequently truncate mid-emit at large clustering inputs
// (token limit hit, response cap, or the model just stopping
// prematurely). Without repair, a clean clustering response with one
// missing trailing "}" fell off the structured-output cliff —
// returning structured: null and forcing the caller to hand-parse the
// raw text.
//
// The fix: panel-entity-resolution.ts now imports + uses the
// safeJSON helper (which I just exported from panel.ts), getting the
// already-implemented best-effort repair pass for free.
//
// Quantitative metric: % of realistic LLM-output strings that
// produce a non-null parse result. The fixture covers:
//
//   * clean code-fenced JSON (both versions handle ✓)
//   * naked JSON object        (both versions handle ✓)
//   * prose preamble + JSON    (both versions handle ✓)
//   * truncated missing trailing } (legacy: null; repaired: object ✓)
//   * truncated missing trailing ] inside (legacy: null; repaired ✓)
//   * trailing-comma JSON      (legacy: null; repaired ✓)
//   * code-fenced + truncated  (legacy: null; repaired ✓)
//   * deeply-nested truncated  (legacy: null; repaired ✓)
//   * non-JSON prose           (both: null — control)
describe("safeJSON LLM-output coverage", () => {
  const cases: Array<{ name: string; input: string; expectStructured: boolean }> = [
    {
      name: "clean code-fenced JSON",
      input: '```json\n{"clusters":[{"id":1,"members":["a","b"]}]}\n```',
      expectStructured: true,
    },
    {
      name: "naked JSON object",
      input: '{"clusters":[{"id":1,"members":["a"]}]}',
      expectStructured: true,
    },
    {
      name: "prose preamble + JSON",
      input: 'Here is my analysis:\n\n{"clusters":[{"id":1}]}\n\nLet me know if you need more.',
      expectStructured: true,
    },
    {
      name: "truncated — missing trailing }",
      input: '{"clusters":[{"id":1,"members":["a","b"]}]',
      expectStructured: true, // legacy: null; safeJSON: repaired
    },
    {
      name: "truncated — missing trailing ] inside",
      input: '{"clusters":[{"id":1,"members":["a","b"',
      expectStructured: true, // legacy: null; safeJSON: repaired
    },
    {
      name: "trailing-comma JSON (LLM common artifact)",
      input: '{"clusters":[{"id":1,"members":["a"]},]}',
      expectStructured: true, // legacy: null in strict JSON.parse
    },
    {
      name: "code-fenced + truncated mid-response",
      input: '```json\n{"clusters":[{"id":1,"score":0.9}',
      expectStructured: true, // legacy: null; safeJSON: repaired
    },
    {
      name: "deeply-nested truncated",
      input: '{"a":{"b":{"c":{"d":[1,2,3',
      expectStructured: true, // safeJSON closes 4 braces + 1 bracket
    },
    {
      name: "non-JSON prose (negative control)",
      input: "I cannot complete this task because the input is malformed.",
      expectStructured: false,
    },
    {
      name: "empty string (negative control)",
      input: "",
      expectStructured: false,
    },
  ];

  it("legacy parser fails on truncated outputs; safeJSON recovers them", () => {
    let beforeOK = 0;
    let afterOK = 0;
    const rows: Array<{ name: string; before: boolean; after: boolean; expected: boolean }> = [];
    for (const c of cases) {
      const before = legacySafeJSON(c.input);
      const after = safeJSON(c.input);
      const beforeMatches = c.expectStructured ? before !== null : before === null;
      const afterMatches = c.expectStructured ? after !== null : after === null;
      if (beforeMatches) beforeOK++;
      if (afterMatches) afterOK++;
      rows.push({ name: c.name, before: beforeMatches, after: afterMatches, expected: c.expectStructured });
    }

    const beforePct = (beforeOK / cases.length) * 100;
    const afterPct = (afterOK / cases.length) * 100;
    const delta = afterPct - beforePct;

    console.log(`safeJSON coverage on ${cases.length} realistic LLM-output cases:`);
    console.log(`  legacy inline parser:  ${beforeOK}/${cases.length} = ${beforePct.toFixed(1)}%`);
    console.log(`  shared safeJSON:       ${afterOK}/${cases.length} = ${afterPct.toFixed(1)}%`);
    console.log(`  improvement:           +${delta.toFixed(1)} percentage points`);
    for (const r of rows) {
      console.log(`    ${r.before ? "✓" : "✗"} → ${r.after ? "✓" : "✗"}  ${r.name}`);
    }

    expect(afterPct).toBeGreaterThanOrEqual(95);
    expect(delta).toBeGreaterThanOrEqual(40);
    // Spot-check: the truncation-repair cases that flipped from null
    // to recovered must produce objects with the expected top-level shape.
    const repaired = safeJSON('{"clusters":[{"id":1,"members":["a","b"]}]') as { clusters: unknown[] };
    expect(repaired).not.toBeNull();
    expect(Array.isArray(repaired.clusters)).toBe(true);
    expect(repaired.clusters.length).toBe(1);
  });
});
