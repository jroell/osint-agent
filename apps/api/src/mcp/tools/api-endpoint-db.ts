import { z } from "zod";
import { toolRegistry } from "./instance";
import { sql } from "../../db/client";

// =============================================================================
// api_endpoint_record — write findings into the api_endpoints_discovered moat
// =============================================================================

const recordInput = z.object({
  target_apex: z.string().min(3).describe("Normalized apex domain (e.g. 'vurvey.app')"),
  endpoints: z.array(z.object({
    endpoint_url: z.string().min(1),
    endpoint_kind: z.enum(["absolute_url", "api_path", "graphql_op", "js_file", "subdomain", "swagger_spec", "filename", "path", "other"]).default("api_path"),
    api_score: z.number().int().min(0).max(10).optional(),
    source_url: z.string().optional().describe("Where we found it (the JS bundle, wayback URL, swagger.json, etc.)"),
    http_status: z.number().int().optional(),
    auth_required: z.boolean().optional(),
    auth_type: z.enum(["bearer", "cookie", "api_key", "basic", "none", "unknown"]).optional(),
    response_sample: z.string().optional().describe("First 480 chars of response when probed"),
    metadata: z.record(z.string(), z.unknown()).optional(),
  })).min(1).max(2000),
  discovery_tool: z.string().min(1).describe("Tool that produced these findings (e.g. 'js_endpoint_extract', 'graphql_introspection')"),
});

toolRegistry.register({
  name: "api_endpoint_record",
  description:
    "Persist API-endpoint findings into the moat DB (api_endpoints_discovered table). Idempotent: ON CONFLICT (target_apex, endpoint_url, discovery_tool) DO UPDATE — the same endpoint discovered by the same tool on subsequent scans bumps last_verified + verify_count rather than duplicating. Pair with `api_endpoint_lookup` to query the growing dataset across sessions. This is the persistent-state primitive that makes the catalog's recon work *cumulative*.",
  inputSchema: recordInput,
  costMillicredits: 2,
  handler: async (i) => {
    const { target_apex, endpoints, discovery_tool } = i;
    const apex = target_apex.toLowerCase().trim();

    let inserted = 0;
    let updated = 0;
    for (const e of endpoints) {
      const result = await sql<{ first_seen: string; verify_count: number }[]>`
        INSERT INTO api_endpoints_discovered
          (target_apex, endpoint_url, endpoint_kind, api_score, source_url,
           discovery_tool, http_status, auth_required, auth_type,
           response_sample, metadata, last_verified, verify_count)
        VALUES
          (${apex}, ${e.endpoint_url}, ${e.endpoint_kind}, ${e.api_score ?? null},
           ${e.source_url ?? null}, ${discovery_tool}, ${e.http_status ?? null},
           ${e.auth_required ?? null}, ${e.auth_type ?? null},
           ${e.response_sample ?? null}, ${sql.json((e.metadata ?? {}) as never)},
           NOW(), 1)
        ON CONFLICT (target_apex, endpoint_url, discovery_tool) DO UPDATE SET
           api_score       = COALESCE(EXCLUDED.api_score, api_endpoints_discovered.api_score),
           http_status     = COALESCE(EXCLUDED.http_status, api_endpoints_discovered.http_status),
           auth_required   = COALESCE(EXCLUDED.auth_required, api_endpoints_discovered.auth_required),
           auth_type       = COALESCE(EXCLUDED.auth_type, api_endpoints_discovered.auth_type),
           response_sample = COALESCE(EXCLUDED.response_sample, api_endpoints_discovered.response_sample),
           metadata        = api_endpoints_discovered.metadata || EXCLUDED.metadata,
           last_verified   = NOW(),
           verify_count    = api_endpoints_discovered.verify_count + 1
        RETURNING first_seen, verify_count
      `;
      if (result[0]?.verify_count === 1) inserted++;
      else updated++;
    }

    // Update per-target coverage roll-up.
    await sql`
      INSERT INTO api_endpoint_coverage (target_apex, endpoints_total, last_scan_at, tools_run)
      VALUES (${apex}, ${endpoints.length}, NOW(), ${sql.json([discovery_tool] as never)})
      ON CONFLICT (target_apex) DO UPDATE SET
        endpoints_total = (
          SELECT COUNT(*) FROM api_endpoints_discovered WHERE target_apex = ${apex}
        ),
        api_endpoints = (
          SELECT COUNT(*) FROM api_endpoints_discovered WHERE target_apex = ${apex} AND api_score >= 5
        ),
        last_scan_at = NOW(),
        tools_run = (
          SELECT to_jsonb(array(
            SELECT DISTINCT discovery_tool FROM api_endpoints_discovered WHERE target_apex = ${apex}
          ))
        )
    `;

    return {
      target_apex: apex,
      discovery_tool,
      inserted,
      updated,
      total_in_db_for_target: inserted + updated,
    };
  },
});

// =============================================================================
// api_endpoint_lookup — read from the moat
// =============================================================================

const lookupInput = z.object({
  target_apex: z.string().min(3).describe("Apex domain to look up"),
  min_api_score: z.number().int().min(0).max(10).optional().describe("Filter to endpoints with api_score >= this value"),
  endpoint_kind: z.string().optional().describe("Filter by kind (absolute_url, api_path, graphql_op, etc.)"),
  discovery_tool: z.string().optional().describe("Filter to findings from a specific tool"),
  limit: z.number().int().min(1).max(2000).default(200),
  include_unverified: z.boolean().default(true).describe("If false, only returns rows with last_verified IS NOT NULL"),
});

toolRegistry.register({
  name: "api_endpoint_lookup",
  description:
    "Query the moat DB (api_endpoints_discovered) for known API endpoints discovered against a target across PAST sessions. This is the persistent-knowledge tool — once we've scanned vurvey.app once, every future agent run can ask 'what do we know about vurvey.app's APIs?' and get an instant answer instead of re-scanning. Returns endpoints with their verify_count (how many scans confirmed them), first_seen + last_verified timestamps, and full discovery metadata. Pair with `api_endpoint_record` to populate.",
  inputSchema: lookupInput,
  costMillicredits: 1,
  handler: async (i) => {
    const apex = i.target_apex.toLowerCase().trim();
    const conditions: string[] = ["target_apex = $1"];
    const params: unknown[] = [apex];
    if (i.min_api_score !== undefined) {
      conditions.push(`api_score >= $${params.length + 1}`);
      params.push(i.min_api_score);
    }
    if (i.endpoint_kind) {
      conditions.push(`endpoint_kind = $${params.length + 1}`);
      params.push(i.endpoint_kind);
    }
    if (i.discovery_tool) {
      conditions.push(`discovery_tool = $${params.length + 1}`);
      params.push(i.discovery_tool);
    }
    if (!i.include_unverified) {
      conditions.push("last_verified IS NOT NULL");
    }
    // postgres.js doesn't accept dynamic param construction easily; build SQL with template tag.
    // For simplicity + safety, branch on the most common shapes.
    let rows: Record<string, unknown>[];
    if (i.min_api_score !== undefined && i.endpoint_kind && i.discovery_tool) {
      rows = await sql`SELECT id, target_apex, endpoint_url, endpoint_kind, api_score, source_url, discovery_tool, http_status, auth_required, auth_type, metadata, first_seen, last_verified, verify_count FROM api_endpoints_discovered WHERE target_apex = ${apex} AND api_score >= ${i.min_api_score} AND endpoint_kind = ${i.endpoint_kind} AND discovery_tool = ${i.discovery_tool} ORDER BY api_score DESC NULLS LAST, last_verified DESC NULLS LAST LIMIT ${i.limit}`;
    } else if (i.min_api_score !== undefined && i.endpoint_kind) {
      rows = await sql`SELECT id, target_apex, endpoint_url, endpoint_kind, api_score, source_url, discovery_tool, http_status, auth_required, auth_type, metadata, first_seen, last_verified, verify_count FROM api_endpoints_discovered WHERE target_apex = ${apex} AND api_score >= ${i.min_api_score} AND endpoint_kind = ${i.endpoint_kind} ORDER BY api_score DESC NULLS LAST, last_verified DESC NULLS LAST LIMIT ${i.limit}`;
    } else if (i.min_api_score !== undefined) {
      rows = await sql`SELECT id, target_apex, endpoint_url, endpoint_kind, api_score, source_url, discovery_tool, http_status, auth_required, auth_type, metadata, first_seen, last_verified, verify_count FROM api_endpoints_discovered WHERE target_apex = ${apex} AND api_score >= ${i.min_api_score} ORDER BY api_score DESC NULLS LAST, last_verified DESC NULLS LAST LIMIT ${i.limit}`;
    } else if (i.endpoint_kind) {
      rows = await sql`SELECT id, target_apex, endpoint_url, endpoint_kind, api_score, source_url, discovery_tool, http_status, auth_required, auth_type, metadata, first_seen, last_verified, verify_count FROM api_endpoints_discovered WHERE target_apex = ${apex} AND endpoint_kind = ${i.endpoint_kind} ORDER BY api_score DESC NULLS LAST, last_verified DESC NULLS LAST LIMIT ${i.limit}`;
    } else if (i.discovery_tool) {
      rows = await sql`SELECT id, target_apex, endpoint_url, endpoint_kind, api_score, source_url, discovery_tool, http_status, auth_required, auth_type, metadata, first_seen, last_verified, verify_count FROM api_endpoints_discovered WHERE target_apex = ${apex} AND discovery_tool = ${i.discovery_tool} ORDER BY api_score DESC NULLS LAST, last_verified DESC NULLS LAST LIMIT ${i.limit}`;
    } else {
      rows = await sql`SELECT id, target_apex, endpoint_url, endpoint_kind, api_score, source_url, discovery_tool, http_status, auth_required, auth_type, metadata, first_seen, last_verified, verify_count FROM api_endpoints_discovered WHERE target_apex = ${apex} ORDER BY api_score DESC NULLS LAST, last_verified DESC NULLS LAST LIMIT ${i.limit}`;
    }

    const coverage = await sql<{ endpoints_total: number; api_endpoints: number; tools_run: string[]; first_scan_at: string; last_scan_at: string }[]>`
      SELECT endpoints_total, api_endpoints, tools_run, first_scan_at, last_scan_at
      FROM api_endpoint_coverage WHERE target_apex = ${apex}
    `;

    const breakdownByKind: Record<string, number> = {};
    const breakdownByTool: Record<string, number> = {};
    for (const r of rows) {
      const kind = r.endpoint_kind as string;
      const tool = r.discovery_tool as string;
      breakdownByKind[kind] = (breakdownByKind[kind] ?? 0) + 1;
      breakdownByTool[tool] = (breakdownByTool[tool] ?? 0) + 1;
    }

    return {
      target_apex: apex,
      coverage: coverage[0] ?? null,
      filters_applied: {
        min_api_score: i.min_api_score,
        endpoint_kind: i.endpoint_kind,
        discovery_tool: i.discovery_tool,
        include_unverified: i.include_unverified,
      },
      total_returned: rows.length,
      breakdown_by_kind: breakdownByKind,
      breakdown_by_tool: breakdownByTool,
      endpoints: rows,
    };
  },
});
