function required(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`Missing required env var: ${name}`);
  return v;
}

function optional(name: string, fallback: string): string {
  return process.env[name] ?? fallback;
}
const env = optional("NODE_ENV", "development");
const devAuthBypass = env === "development" && process.env.DEV_AUTH_BYPASS === "true";

function requiredUnlessDevAuthBypass(name: string, fallback: string): string {
  const v = process.env[name];
  if (v) return v;
  if (devAuthBypass) return fallback;
  throw new Error(`Missing required env var: ${name}`);
}

export const config = {
  env,
  port: Number(optional("PORT", "3000")),
  logLevel: optional("LOG_LEVEL", "info"),
  dev: {
    authBypass: devAuthBypass,
  },
  databaseUrl: requiredUnlessDevAuthBypass("DATABASE_URL", "postgres://osint:osint@localhost:5434/osint_dev?sslmode=disable"),
  redisUrl: requiredUnlessDevAuthBypass("REDIS_URL", "redis://localhost:6380"),
  firebase: {
    projectId: requiredUnlessDevAuthBypass("FIREBASE_PROJECT_ID", "osint-agent-dev"),
  },
  anthropic: {
    apiKey: requiredUnlessDevAuthBypass("ANTHROPIC_API_KEY", "sk-ant-local-dev-placeholder"),
  },
  stripe: {
    secretKey: requiredUnlessDevAuthBypass("STRIPE_SECRET_KEY", "sk_test_local_dev_placeholder"),
    webhookSecret: requiredUnlessDevAuthBypass("STRIPE_WEBHOOK_SECRET", "whsec_local_dev_placeholder"),
    priceIdHunter: requiredUnlessDevAuthBypass("STRIPE_PRICE_ID_HUNTER", "price_local_hunter"),
    priceIdOperator: requiredUnlessDevAuthBypass("STRIPE_PRICE_ID_OPERATOR", "price_local_operator"),
  },
  workers: {
    goUrl: requiredUnlessDevAuthBypass("GO_WORKER_URL", "http://localhost:8181"),
    pyUrl: requiredUnlessDevAuthBypass("PY_WORKER_URL", "http://localhost:8182"),
    signingKeyHex: requiredUnlessDevAuthBypass(
      "WORKER_SIGNING_KEY_HEX",
      "0000000000000000000000000000000000000000000000000000000000000000",
    ),
  },
  creditPriceUsd: Number(optional("CREDIT_PRICE_USD", "0.01")),
} as const;
