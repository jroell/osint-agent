// Test isolation: Bun auto-loads root `.env`, which during local dev contains
// `DEV_AUTH_BYPASS=true` (set by `bun run dev`). That causes `apps/api/src/config.ts`
// to fall back to placeholder values and the auth middleware to bypass token checks,
// which silently invalidates integration tests. CI doesn't have a `.env` so it's fine
// there — this preload makes local `bun test` behave the same way as CI.
//
// Loaded via `apps/api/bunfig.toml` `[test] preload = ["./test/setup.ts"]`,
// which runs before any test file imports config.ts.

const ciDefaults: Record<string, string> = {
  NODE_ENV: "test",
  DEV_AUTH_BYPASS: "false",
  DATABASE_URL: "postgres://osint:osint@localhost:5434/osint_dev?sslmode=disable",
  REDIS_URL: "redis://localhost:6380",
  FIREBASE_PROJECT_ID: "osint-agent-ci",
  ANTHROPIC_API_KEY: "sk-ant-ci-placeholder",
  STRIPE_SECRET_KEY: "sk_test_ci",
  STRIPE_WEBHOOK_SECRET: "whsec_ci",
  STRIPE_PRICE_ID_HUNTER: "price_hunter_ci",
  STRIPE_PRICE_ID_OPERATOR: "price_operator_ci",
  GO_WORKER_URL: "http://localhost:8081",
  PY_WORKER_URL: "http://localhost:8082",
  WORKER_SIGNING_KEY_HEX: "0101010101010101010101010101010101010101010101010101010101010101",
};

// Always force these — they're the ones that cause silent bypass / wrong-uid failures.
process.env.NODE_ENV = "test";
process.env.DEV_AUTH_BYPASS = "false";

// Fill in the rest only if absent, so a contributor with a real test env can override.
for (const [k, v] of Object.entries(ciDefaults)) {
  if (!process.env[k]) process.env[k] = v;
}
