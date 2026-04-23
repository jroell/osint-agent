import { sql } from "../db/client";

export type EventType =
  | "auth.signup"
  | "auth.signin"
  | "billing.checkout_created"
  | "billing.subscription_active"
  | "billing.subscription_canceled"
  | "credit.spent"
  | "credit.granted"
  | "tool.called"
  | "tool.succeeded"
  | "tool.failed"
  | "mcp.session_started"
  | "mcp.session_ended";

export interface EventInput {
  tenantId: string;
  userId?: string | null;
  eventType: EventType;
  payload: Record<string, unknown>;
  traceId?: string | null;
}

/**
 * Writes an event to the partitioned event log.
 * Auto-creates the current month's partition if it doesn't exist.
 */
export async function writeEvent(e: EventInput): Promise<void> {
  await ensureCurrentMonthPartition();
  await sql`
    INSERT INTO events (tenant_id, user_id, event_type, payload, trace_id)
    VALUES (${e.tenantId}, ${e.userId ?? null}, ${e.eventType}, ${sql.json(e.payload as never)}, ${e.traceId ?? null})
  `;
}

// In Phase 0 we pre-create 4 months in the migration; this is the safety net.
// In Phase 1 we'll move this to a cron (River job) that rolls partitions ahead of time.
async function ensureCurrentMonthPartition(): Promise<void> {
  const now = new Date();
  const yr = now.getUTCFullYear();
  const mo = (now.getUTCMonth() + 1).toString().padStart(2, "0");
  const partitionName = `events_${yr}_${mo}`;

  const rows = await sql<{ exists: boolean }[]>`
    SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname = ${partitionName}) AS exists
  `;
  if (rows[0]?.exists) return;

  const nextMo = new Date(Date.UTC(yr, now.getUTCMonth() + 1, 1));
  const fromStr = `${yr}-${mo}-01`;
  const toStr = `${nextMo.getUTCFullYear()}-${(nextMo.getUTCMonth() + 1).toString().padStart(2, "0")}-01`;

  // CREATE IF NOT EXISTS (idempotent; safe under concurrency via advisory lock)
  await sql.unsafe(`
    CREATE TABLE IF NOT EXISTS ${partitionName} PARTITION OF events
    FOR VALUES FROM ('${fromStr}') TO ('${toStr}')
  `);
}
