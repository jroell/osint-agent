import postgres from "postgres";
const DATABASE_URL = process.env.DATABASE_URL
  ?? (process.env.NODE_ENV === "development" && process.env.DEV_AUTH_BYPASS === "true"
    ? "postgres://osint:osint@localhost:5434/osint_dev?sslmode=disable"
    : undefined);
if (!DATABASE_URL) {
  throw new Error("DATABASE_URL is required");
}

// Tunable pool size; Elysia is single-event-loop so ~10 is plenty for Phase 0.
export const sql = postgres(DATABASE_URL, {
  max: 10,
  idle_timeout: 20,
  connect_timeout: 10,
  prepare: true,
});

export async function closeDb(): Promise<void> {
  await sql.end({ timeout: 5 });
}
